package ui

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cfbevan/multiverse/internal/models"
	"github.com/cfbevan/multiverse/internal/platform/activitypub"
	_ "github.com/lib/pq"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

const (
	integrationTestPassword = "password-1234"
	localhostDomain         = "localhost"
)

// TestIntegrationSignupLoginAndProtectedMicroPost covers signup, login, and protected micro-post creation.
func TestIntegrationSignupLoginAndProtectedMicroPost(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped in short mode")
	}

	db := integrationDB(t)
	resetDatabase(t, db)

	app := newIntegrationApp(t)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	app.config.ActivityPubBaseURL = srv.URL

	handle := "u" + strings.ReplaceAll(time.Now().UTC().Format("150405.000000"), ".", "")
	signupBody := map[string]any{
		emailKey:       handle + "@example.com",
		handleKey:      handle,
		displayNameKey: "Tester",
		passwordKey:    integrationTestPassword,
	}
	signupRes := doJSON(t, http.MethodPost, srv.URL+"/v1/accounts/signup", signupBody, "")
	defer func() { _ = signupRes.Body.Close() }()
	if signupRes.StatusCode != http.StatusCreated {
		t.Fatalf("signup status = %d, want %d", signupRes.StatusCode, http.StatusCreated)
	}
	signupPayload := decodeJSON(t, signupRes.Body)
	token := extractString(signupPayload, "token")
	if token == "" {
		t.Fatal("signup token missing")
	}

	meRes := doJSON(t, http.MethodGet, srv.URL+"/v1/accounts/me", nil, token)
	defer func() { _ = meRes.Body.Close() }()
	if meRes.StatusCode != http.StatusOK {
		t.Fatalf("/me status = %d, want %d", meRes.StatusCode, http.StatusOK)
	}

	loginBody := map[string]any{
		emailKey:    handle + "@example.com",
		passwordKey: integrationTestPassword,
	}
	loginRes := doJSON(t, http.MethodPost, srv.URL+"/v1/accounts/login", loginBody, "")
	defer func() { _ = loginRes.Body.Close() }()
	if loginRes.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want %d", loginRes.StatusCode, http.StatusOK)
	}
	loginPayload := decodeJSON(t, loginRes.Body)
	loginToken := extractString(loginPayload, "token")
	if loginToken == "" {
		t.Fatal("login token missing")
	}

	unauthRes := doJSON(
		t,
		http.MethodPost,
		srv.URL+"/v1/micro-posts",
		map[string]any{contentKey: "test"},
		"",
	)
	defer func() { _ = unauthRes.Body.Close() }()
	if unauthRes.StatusCode != http.StatusUnauthorized {
		t.Fatalf(
			"unauth micro-post status = %d, want %d",
			unauthRes.StatusCode,
			http.StatusUnauthorized,
		)
	}

	createRes := doJSON(
		t,
		http.MethodPost,
		srv.URL+"/v1/micro-posts",
		map[string]any{contentKey: "hello federation"},
		loginToken,
	)
	defer func() { _ = createRes.Body.Close() }()
	if createRes.StatusCode != http.StatusCreated {
		t.Fatalf("create micro-post status = %d, want %d", createRes.StatusCode, http.StatusCreated)
	}
}

// TestIntegrationSiteSettingsHideDisabledVerticals verifies hidden sections disappear from the home page and endpoints.
func TestIntegrationSiteSettingsHideDisabledVerticals(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped in short mode")
	}

	db := integrationDB(t)
	resetDatabase(t, db)

	app := newIntegrationApp(t)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()

	sections := []struct {
		key  string
		path string
		href string
	}{
		{key: "blog_enabled", path: "/blog", href: "href=\"/blog\""},
		{key: "micro_blog_enabled", path: "/micro-blog", href: "href=\"/micro-blog\""},
		{key: "pictures_enabled", path: "/pictures", href: "href=\"/pictures\""},
		{key: "videos_enabled", path: "/videos", href: "href=\"/videos\""},
		{key: "audio_enabled", path: "/audio", href: "href=\"/audio\""},
	}

	for _, section := range sections {
		if _, err := db.Exec(
			`UPDATE site_configs SET enabled = false WHERE key = $1`,
			section.key,
		); err != nil {
			t.Fatal(err)
		}

		homeRes, err := http.Get(srv.URL + "/")
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(homeRes.Body)
		_ = homeRes.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), section.href) {
			t.Fatalf("home page still shows %s after disabling %s", section.path, section.key)
		}

		sectionRes, err := http.Get(srv.URL + section.path)
		if err != nil {
			t.Fatal(err)
		}
		_ = sectionRes.Body.Close()
		if sectionRes.StatusCode != http.StatusNotFound {
			t.Fatalf(
				"%s status = %d, want %d",
				section.path,
				sectionRes.StatusCode,
				http.StatusNotFound,
			)
		}
	}
}

// TestIntegrationMicroBlogFeeds validates local, federated, and follow-based micro-blog feeds.
func TestIntegrationMicroBlogFeeds(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped in short mode")
	}

	db := integrationDB(t)
	resetDatabase(t, db)

	app := newIntegrationApp(t)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	app.config.ActivityPubBaseURL = srv.URL
	app.config.ActivityPubDomain = localhostDomain

	aliceHandle := "alice" + strings.ReplaceAll(time.Now().UTC().Format("150405.000000"), ".", "")
	bobHandle := "bob" + strings.ReplaceAll(time.Now().UTC().Format("150405.000000"), ".", "")
	aliceToken := signupUser(t, srv, aliceHandle, "Alice")
	bobToken := signupUser(t, srv, bobHandle, "Bob")

	createFollow(t, db, aliceHandle, bobHandle)

	createMicroPost(t, srv, aliceToken, "alice local post")
	createMicroPost(t, srv, bobToken, "bob local post")

	remotePub, _, err := activitypub.GenerateActorKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	var remoteActorID int64
	if err := db.QueryRow(`
		INSERT INTO actors (user_id, handle, domain, inbox_url, outbox_url, followers_url, following_url, public_key_id, public_key_pem, private_key_pem, is_local)
		VALUES (NULL, 'carol', 'remote.example', 'https://remote.example/inbox', 'https://remote.example/outbox', 'https://remote.example/followers', 'https://remote.example/following', 'https://remote.example/actors/carol#main-key', $1, '', false)
		RETURNING id`, remotePub).Scan(&remoteActorID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO micro_posts (actor_id, content, visibility, ap_object_id) VALUES ($1, $2, $3, $4)`,
		remoteActorID,
		"remote federated post",
		models.VisibilityPublic,
		"https://remote.example/objects/micro/1",
	); err != nil {
		t.Fatal(err)
	}

	myRes := doJSON(t, http.MethodGet, srv.URL+"/v1/micro-posts?feed=my", nil, aliceToken)
	defer func() { _ = myRes.Body.Close() }()
	requireJSONList(t, myRes, "micro_posts", 2)
	localRes := doJSON(t, http.MethodGet, srv.URL+"/v1/micro-posts?feed=local", nil, "")
	defer func() { _ = localRes.Body.Close() }()
	requireJSONList(t, localRes, "micro_posts", 2)
	federatedRes := doJSON(t, http.MethodGet, srv.URL+"/v1/micro-posts?feed=federated", nil, "")
	defer func() { _ = federatedRes.Body.Close() }()
	requireJSONList(t, federatedRes, "micro_posts", 1)
	trendRes := doJSON(t, http.MethodGet, srv.URL+"/v1/micro-posts?feed=trending", nil, aliceToken)
	defer func() { _ = trendRes.Body.Close() }()
	requireJSONList(t, trendRes, "micro_posts", 3)
}

// TestIntegrationPrivatePostsVisibleInMyFeed ensures private posts appear in a viewer's personal feed.
func TestIntegrationPrivatePostsVisibleInMyFeed(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped in short mode")
	}

	db := integrationDB(t)
	resetDatabase(t, db)

	app := newIntegrationApp(t)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	app.config.ActivityPubBaseURL = srv.URL

	handle := "privatefeed" + strings.ReplaceAll(time.Now().UTC().Format("150405.000000"), ".", "")
	signup := doJSON(t, http.MethodPost, srv.URL+"/v1/accounts/signup", map[string]any{
		emailKey:       handle + "@example.com",
		handleKey:      handle,
		displayNameKey: "Private Feed User",
		passwordKey:    integrationTestPassword,
	}, "")
	defer func() { _ = signup.Body.Close() }()
	if signup.StatusCode != http.StatusCreated {
		t.Fatalf("signup status = %d, want %d", signup.StatusCode, http.StatusCreated)
	}
	token := extractString(decodeJSON(t, signup.Body), "token")

	var actorID int64
	if err := db.QueryRow(`SELECT id FROM actors WHERE handle = $1`, handle).
		Scan(&actorID); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec(
		`INSERT INTO micro_posts (actor_id, content, visibility, ap_object_id) VALUES ($1, $2, $3, $4)`,
		actorID,
		"hidden private post",
		models.VisibilityPrivate,
		"https://localhost/objects/private/micro/1",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO blog_posts (actor_id, title, slug, body_markdown, body_html, visibility, ap_object_id) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		actorID,
		"Private article",
		"private-article",
		"secret",
		"<p>secret</p>",
		models.VisibilityPrivate,
		"https://localhost/objects/private/blog/1",
	); err != nil {
		t.Fatal(err)
	}
	var assetID int64
	if err := db.QueryRow(`INSERT INTO media_assets (owner_actor_id, bucket, object_key, media_type, byte_size, sha256_hex, original_filename, is_public) VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`, actorID, "audio", "private-track.mp3", "audio/mpeg", 1234, "abc123", "private-track.mp3", false).
		Scan(&assetID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO audio_posts (actor_id, title, description, visibility, media_asset_id, ap_object_id) VALUES ($1, $2, $3, $4, $5, $6)`,
		actorID,
		"Private audio",
		"private audio track",
		models.VisibilityPrivate,
		assetID,
		"https://localhost/objects/private/audio/1",
	); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		path string
		key  string
	}{
		{name: "micro-post", path: srv.URL + "/v1/micro-posts?feed=my", key: "micro_posts"},
		{name: "blog-post", path: srv.URL + "/v1/blog-posts?feed=my", key: "blog_posts"},
		{name: "audio-post", path: srv.URL + "/v1/audio-posts?feed=my", key: "audio_posts"},
	} {
		res := doJSON(t, http.MethodGet, tc.path, nil, token)
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s my-feed status = %d, want %d", tc.name, res.StatusCode, http.StatusOK)
		}
		payload := decodeJSON(t, res.Body)
		items, ok := payload[tc.key].([]any)
		if !ok || len(items) == 0 {
			t.Fatalf(
				"%s my-feed returned %d items, want at least 1 private item",
				tc.name,
				len(items),
			)
		}
	}
}

// TestIntegrationBlogAndAudioFeedEndpoints exercises blog and audio feed listing across content modes.
func TestIntegrationBlogAndAudioFeedEndpoints(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped in short mode")
	}

	db := integrationDB(t)
	resetDatabase(t, db)

	app := newIntegrationApp(t)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	app.config.ActivityPubBaseURL = srv.URL
	app.config.ActivityPubDomain = localhostDomain

	aliceHandle := "alicefeed" + strings.ReplaceAll(
		time.Now().UTC().Format("150405.000000"),
		".",
		"",
	)
	bobHandle := "bobfeed" + strings.ReplaceAll(time.Now().UTC().Format("150405.000000"), ".", "")

	aliceToken := signupUser(t, srv, aliceHandle, "Alice Feed")
	signupUser(t, srv, bobHandle, "Bob Feed")

	var aliceActorID int64
	if err := db.QueryRow(`SELECT id FROM actors WHERE handle = $1`, aliceHandle).
		Scan(&aliceActorID); err != nil {
		t.Fatal(err)
	}
	var bobActorID int64
	if err := db.QueryRow(`SELECT id FROM actors WHERE handle = $1`, bobHandle).
		Scan(&bobActorID); err != nil {
		t.Fatal(err)
	}

	createFollow(t, db, aliceHandle, bobHandle)
	remotePub, _, err := activitypub.GenerateActorKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	var remoteActorID int64
	if err := db.QueryRow(`
		INSERT INTO actors (user_id, handle, domain, inbox_url, outbox_url, followers_url, following_url, public_key_id, public_key_pem, private_key_pem, is_local)
		VALUES (NULL, 'carolfeed', 'remote.example', 'https://remote.example/inbox', 'https://remote.example/outbox', 'https://remote.example/followers', 'https://remote.example/following', 'https://remote.example/actors/carolfeed#main-key', $1, '', false)
		RETURNING id`, remotePub).Scan(&remoteActorID); err != nil {
		t.Fatal(err)
	}
	var remoteMediaAssetID int64
	if err := db.QueryRow(`INSERT INTO media_assets (owner_actor_id, bucket, object_key, media_type, byte_size, sha256_hex, original_filename, is_public) VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`, remoteActorID, "audio", "carol-track.mp3", "audio/mpeg", 4321, "def456", "carol-track.mp3", true).
		Scan(&remoteMediaAssetID); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec(
		`INSERT INTO blog_posts (actor_id, title, slug, body_markdown, body_html, visibility, ap_object_id) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		aliceActorID,
		"Alice article",
		"alice-article",
		"hello from alice",
		"<p>hello from alice</p>",
		models.VisibilityPublic,
		"https://localhost/objects/blog/alice-1",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO blog_posts (actor_id, title, slug, body_markdown, body_html, visibility, ap_object_id) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		bobActorID,
		"Bob article",
		"bob-article",
		"hello from bob",
		"<p>hello from bob</p>",
		models.VisibilityPublic,
		"https://localhost/objects/blog/bob-1",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO blog_posts (actor_id, title, slug, body_markdown, body_html, visibility, ap_object_id) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		remoteActorID,
		"Remote article",
		"remote-article",
		"hello from remote",
		"<p>hello from remote</p>",
		models.VisibilityPublic,
		"https://remote.example/objects/blog/remote-1",
	); err != nil {
		t.Fatal(err)
	}
	var mediaAssetID int64
	if err := db.QueryRow(`INSERT INTO media_assets (owner_actor_id, bucket, object_key, media_type, byte_size, sha256_hex, original_filename, is_public) VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`, aliceActorID, "audio", "alice-track.mp3", "audio/mpeg", 1234, "abc123", "alice-track.mp3", true).
		Scan(&mediaAssetID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO audio_posts (actor_id, title, description, visibility, media_asset_id, ap_object_id) VALUES ($1, $2, $3, $4, $5, $6)`,
		aliceActorID,
		"Alice audio",
		"alice audio track",
		models.VisibilityPublic,
		mediaAssetID,
		"https://localhost/objects/audio/alice-1",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO audio_posts (actor_id, title, description, visibility, media_asset_id, ap_object_id) VALUES ($1, $2, $3, $4, $5, $6)`,
		bobActorID,
		"Bob audio",
		"bob audio track",
		models.VisibilityPublic,
		mediaAssetID,
		"https://localhost/objects/audio/bob-1",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO audio_posts (actor_id, title, description, visibility, media_asset_id, ap_object_id) VALUES ($1, $2, $3, $4, $5, $6)`,
		remoteActorID,
		"Remote audio",
		"remote audio track",
		models.VisibilityPublic,
		remoteMediaAssetID,
		"https://remote.example/objects/audio/remote-1",
	); err != nil {
		t.Fatal(err)
	}

	for _, feed := range []string{"my", localFeedValue, federatedFeedValue, "trending"} {
		blogRes := doJSON(t, http.MethodGet, srv.URL+"/v1/blog-posts?feed="+feed, nil, aliceToken)
		defer func() { _ = blogRes.Body.Close() }()
		requireJSONList(t, blogRes, "blog_posts", 1)

		audioRes := doJSON(t, http.MethodGet, srv.URL+"/v1/audio-posts?feed="+feed, nil, aliceToken)
		defer func() { _ = audioRes.Body.Close() }()
		requireJSONList(t, audioRes, "audio_posts", 1)
	}
}

// TestIntegrationSectionPagesLoadTheirFeedContainers verifies that the section pages are wired to load their feed partials.
func TestIntegrationSectionPagesLoadTheirFeedContainers(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped in short mode")
	}

	srv := httptest.NewServer(newIntegrationApp(t).Routes())
	defer srv.Close()

	for _, tc := range []struct {
		name        string
		path        string
		containerID string
		partialPath string
	}{
		{name: "blog", path: "/blog", containerID: "blogList", partialPath: "/blog/partials/list?feed=local"},
		{name: "audio", path: "/audio", containerID: "audioList", partialPath: "/audio/partials/list?feed=local"},
		{name: "pictures", path: "/pictures", containerID: "pictureList", partialPath: "/pictures/partials/list?feed=local"},
		{name: "videos", path: "/videos", containerID: "videoList", partialPath: "/videos/partials/list?feed=local"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := requirePageBody(t, srv.URL+tc.path)
			requirePageContains(t, body, `id="`+tc.containerID+`"`)
			requirePageContains(t, body, `hx-get="`+tc.partialPath+`"`)
			requirePageContains(t, body, `hx-trigger="load"`)
		})
	}
}

// TestIntegrationPictureAndVideoFeedEndpoints exercises picture and video feed listing across content modes.
func TestIntegrationPictureAndVideoFeedEndpoints(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped in short mode")
	}

	db := integrationDB(t)
	resetDatabase(t, db)

	app := newIntegrationApp(t)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	app.config.ActivityPubBaseURL = srv.URL
	app.config.ActivityPubDomain = localhostDomain

	aliceHandle := "alicepics" + strings.ReplaceAll(
		time.Now().UTC().Format("150405.000000"),
		".",
		"",
	)
	bobHandle := "bobpics" + strings.ReplaceAll(time.Now().UTC().Format("150405.000000"), ".", "")

	aliceToken := signupUser(t, srv, aliceHandle, "Alice Pictures")
	signupUser(t, srv, bobHandle, "Bob Pictures")

	var aliceActorID int64
	if err := db.QueryRow(`SELECT id FROM actors WHERE handle = $1`, aliceHandle).
		Scan(&aliceActorID); err != nil {
		t.Fatal(err)
	}
	var bobActorID int64
	if err := db.QueryRow(`SELECT id FROM actors WHERE handle = $1`, bobHandle).
		Scan(&bobActorID); err != nil {
		t.Fatal(err)
	}

	createFollow(t, db, aliceHandle, bobHandle)
	remotePub, _, err := activitypub.GenerateActorKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	var remoteActorID int64
	if err := db.QueryRow(`
		INSERT INTO actors (user_id, handle, domain, inbox_url, outbox_url, followers_url, following_url, public_key_id, public_key_pem, private_key_pem, is_local)
		VALUES (NULL, 'carolpics', 'remote.example', 'https://remote.example/inbox', 'https://remote.example/outbox', 'https://remote.example/followers', 'https://remote.example/following', 'https://remote.example/actors/carolpics#main-key', $1, '', false)
		RETURNING id`, remotePub).Scan(&remoteActorID); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec(
		`INSERT INTO picture_posts (actor_id, caption, visibility, ap_object_id) VALUES ($1, $2, $3, $4)`,
		aliceActorID,
		"Alice picture",
		models.VisibilityPublic,
		"https://localhost/objects/picture/alice-1",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO picture_posts (actor_id, caption, visibility, ap_object_id) VALUES ($1, $2, $3, $4)`,
		bobActorID,
		"Bob picture",
		models.VisibilityPublic,
		"https://localhost/objects/picture/bob-1",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO picture_posts (actor_id, caption, visibility, ap_object_id) VALUES ($1, $2, $3, $4)`,
		remoteActorID,
		"Remote picture",
		models.VisibilityPublic,
		"https://remote.example/objects/picture/remote-1",
	); err != nil {
		t.Fatal(err)
	}

	var localVideoAssetID int64
	if err := db.QueryRow(`INSERT INTO media_assets (owner_actor_id, bucket, object_key, media_type, byte_size, sha256_hex, original_filename, is_public) VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`, aliceActorID, "video", "alice-clip.mp4", "video/mp4", 2048, "abc789", "alice-clip.mp4", true).
		Scan(&localVideoAssetID); err != nil {
		t.Fatal(err)
	}
	var remoteVideoAssetID int64
	if err := db.QueryRow(`INSERT INTO media_assets (owner_actor_id, bucket, object_key, media_type, byte_size, sha256_hex, original_filename, is_public) VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`, remoteActorID, "video", "remote-clip.mp4", "video/mp4", 4096, "def789", "remote-clip.mp4", true).
		Scan(&remoteVideoAssetID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO video_posts (actor_id, title, description, visibility, media_asset_id, ap_object_id) VALUES ($1, $2, $3, $4, $5, $6)`,
		aliceActorID,
		"Alice video",
		"alice video clip",
		models.VisibilityPublic,
		localVideoAssetID,
		"https://localhost/objects/video/alice-1",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO video_posts (actor_id, title, description, visibility, media_asset_id, ap_object_id) VALUES ($1, $2, $3, $4, $5, $6)`,
		bobActorID,
		"Bob video",
		"bob video clip",
		models.VisibilityPublic,
		localVideoAssetID,
		"https://localhost/objects/video/bob-1",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO video_posts (actor_id, title, description, visibility, media_asset_id, ap_object_id) VALUES ($1, $2, $3, $4, $5, $6)`,
		remoteActorID,
		"Remote video",
		"remote video clip",
		models.VisibilityPublic,
		remoteVideoAssetID,
		"https://remote.example/objects/video/remote-1",
	); err != nil {
		t.Fatal(err)
	}

	for _, feed := range []string{"my", localFeedValue, federatedFeedValue, trendingFeedValue} {
		pictureBody := requireSectionFragment(
			t,
			srv,
			"/pictures/partials/list?feed="+feed,
			aliceToken,
		)
		videoBody := requireSectionFragment(t, srv, "/videos/partials/list?feed="+feed, aliceToken)
		assertPictureFeedFragment(t, feed, pictureBody)
		assertVideoFeedFragment(t, feed, videoBody)
	}
}

// TestIntegrationWebfingerActorAndOutboxConformance checks actor metadata and outbox conformance.
func TestIntegrationWebfingerActorAndOutboxConformance(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped in short mode")
	}

	db := integrationDB(t)
	resetDatabase(t, db)

	app := newIntegrationApp(t)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	app.config.ActivityPubBaseURL = srv.URL

	handle := "f" + strings.ReplaceAll(time.Now().UTC().Format("150405.000000"), ".", "")
	token := signupUser(t, srv, handle, "Fed Tester")

	createMicroPost(t, srv, token, "note content")
	createBlogPost(t, srv, token, "Post", "Long body")

	checkWebfingerResponse(t, srv, app, handle)
	checkActorMetadata(t, srv, handle)
	checkOutboxContainsTypes(t, srv, handle)
}

// TestIntegrationInboxSignatureAndOutboxWorker validates inbox signatures and scheduled outbox delivery.
func TestIntegrationInboxSignatureAndOutboxWorker(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped in short mode")
	}

	db := integrationDB(t)
	resetDatabase(t, db)

	app := newIntegrationApp(t)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	app.config.ActivityPubBaseURL = srv.URL
	app.config.OutboxPollSec = 1

	var remoteHits atomic.Int32
	remoteServer := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/inbox" {
				remoteHits.Add(1)
				w.WriteHeader(http.StatusAccepted)

				return
			}
			w.WriteHeader(http.StatusNotFound)
		}),
	)
	defer remoteServer.Close()

	handle := "q" + strings.ReplaceAll(time.Now().UTC().Format("150405.000000"), ".", "")
	token := signupUser(t, srv, handle, "Queue Tester")

	var localActorID int64
	if err := db.QueryRow(`SELECT id FROM actors WHERE handle = $1`, handle).
		Scan(&localActorID); err != nil {
		t.Fatal(err)
	}

	remotePub, remotePriv, err := activitypub.GenerateActorKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	remoteKeyID := "https://remote.example/actors/alice#main-key"
	var remoteActorID int64
	if err := db.QueryRow(`
INSERT INTO actors (user_id, handle, domain, inbox_url, outbox_url, followers_url, following_url, public_key_id, public_key_pem, private_key_pem, is_local)
VALUES (NULL, 'alice', 'remote.example', $1, 'https://remote.example/outbox', 'https://remote.example/followers', 'https://remote.example/following', $2, $3, '', false)
RETURNING id`, remoteServer.URL+"/inbox", remoteKeyID, remotePub).Scan(&remoteActorID); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec(
		`INSERT INTO follows (follower_actor_id, followed_actor_id, ap_follow_activity_id) VALUES ($1, $2, $3)`,
		remoteActorID,
		localActorID,
		"https://remote.example/activities/follow/1",
	); err != nil {
		t.Fatal(err)
	}

	workerCtx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() {
		app.runOutboxWorker(workerCtx)
	})
	defer func() {
		cancel()
		wg.Wait()
	}()

	createMicroPost(t, srv, token, "delivery check")

	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if remoteHits.Load() > 0 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if remoteHits.Load() == 0 {
		t.Fatal("expected outbox worker delivery to remote inbox")
	}

	activity := []byte(`{"type":"Follow","actor":"https://remote.example/actors/alice"}`)
	inboxReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/inbox", bytes.NewReader(activity))
	inboxReq.Header.Set("Content-Type", "application/activity+json")
	inboxReq.Header.Set("Date", time.Now().UTC().Format(time.RFC1123))
	inboxReq.Header.Set("Digest", activitypub.BuildDigestHeader(activity))
	if err := activitypub.SignRequest(inboxReq, activity, remoteKeyID, remotePriv); err != nil {
		t.Fatal(err)
	}
	inboxRes, err := http.DefaultClient.Do(inboxReq)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = inboxRes.Body.Close() }()
	if inboxRes.StatusCode != http.StatusAccepted {
		t.Fatalf("signed inbox status = %d, want %d", inboxRes.StatusCode, http.StatusAccepted)
	}
}

func signupUser(t *testing.T, srv *httptest.Server, handle, displayName string) string {
	t.Helper()

	res := doJSON(t, http.MethodPost, srv.URL+"/v1/accounts/signup", map[string]any{
		emailKey:       handle + "@example.com",
		handleKey:      handle,
		displayNameKey: displayName,
		passwordKey:    integrationTestPassword,
	}, "")
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("signup status = %d, want %d", res.StatusCode, http.StatusCreated)
	}

	return extractString(decodeJSON(t, res.Body), "token")
}

func createFollow(t *testing.T, db *sql.DB, followerHandle, followedHandle string) {
	t.Helper()

	var followerID, followedID int64
	if err := db.QueryRow(`SELECT id FROM actors WHERE handle = $1`, followerHandle).
		Scan(&followerID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT id FROM actors WHERE handle = $1`, followedHandle).
		Scan(&followedID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO follows (follower_actor_id, followed_actor_id, ap_follow_activity_id) VALUES ($1, $2, $3)`,
		followerID,
		followedID,
		"https://localhost/activities/follow/1",
	); err != nil {
		t.Fatal(err)
	}
}

func createMicroPost(t *testing.T, srv *httptest.Server, token, content string) {
	t.Helper()

	res := doJSON(
		t,
		http.MethodPost,
		srv.URL+"/v1/micro-posts",
		map[string]any{contentKey: content},
		token,
	)
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create micro-post status = %d, want %d", res.StatusCode, http.StatusCreated)
	}
}

func createBlogPost(t *testing.T, srv *httptest.Server, token, title, body string) {
	t.Helper()

	res := doJSON(
		t,
		http.MethodPost,
		srv.URL+"/v1/blog-posts",
		map[string]any{titleKey: title, "body": body},
		token,
	)
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create blog status = %d, want %d", res.StatusCode, http.StatusCreated)
	}
}

func requireSectionFragment(t *testing.T, srv *httptest.Server, path, token string) string {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("%s status = %d, want %d", path, res.StatusCode, http.StatusOK)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}

	return string(body)
}

func requirePageBody(t *testing.T, url string) string {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("%s status = %d, want %d", url, res.StatusCode, http.StatusOK)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}

	return string(body)
}

func requirePageContains(t *testing.T, body, expected string) {
	t.Helper()
	if !strings.Contains(body, expected) {
		t.Fatalf("page body missing %q", expected)
	}
}

func requireFragmentContains(t *testing.T, body string, expected ...string) {
	t.Helper()
	for _, want := range expected {
		if !strings.Contains(body, want) {
			t.Fatalf("fragment missing %q", want)
		}
	}
}

func requireFragmentNotContains(t *testing.T, body string, unexpected ...string) {
	t.Helper()
	for _, want := range unexpected {
		if strings.Contains(body, want) {
			t.Fatalf("fragment unexpectedly contained %q", want)
		}
	}
}

func assertPictureFeedFragment(t *testing.T, feed, body string) {
	t.Helper()

	switch feed {
	case "my", localFeedValue:
		requireFragmentContains(t, body, "Alice picture", "Bob picture")
		requireFragmentNotContains(t, body, "Remote picture")
	case federatedFeedValue:
		requireFragmentContains(t, body, "Remote picture")
		requireFragmentNotContains(t, body, "Alice picture", "Bob picture")
	case trendingFeedValue:
		requireFragmentContains(t, body, "Alice picture", "Bob picture", "Remote picture")
	default:
		t.Fatalf("unexpected feed %q", feed)
	}
}

func assertVideoFeedFragment(t *testing.T, feed, body string) {
	t.Helper()

	requireFragmentContains(t, body, "<video", "/videos/", "/preview")

	switch feed {
	case "my", localFeedValue:
		requireFragmentContains(t, body, "Alice video", "Bob video")
		requireFragmentNotContains(t, body, "Remote video")
	case federatedFeedValue:
		requireFragmentContains(t, body, "Remote video")
		requireFragmentNotContains(t, body, "Alice video", "Bob video")
	case trendingFeedValue:
		requireFragmentContains(t, body, "Alice video", "Bob video", "Remote video")
	default:
		t.Fatalf("unexpected feed %q", feed)
	}
}

func requireJSONList(t *testing.T, res *http.Response, key string, minLen int) {
	t.Helper()
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}
	payload := decodeJSON(t, res.Body)
	items, ok := payload[key].([]any)
	if !ok || len(items) < minLen {
		t.Fatalf("%s item count = %d, want >= %d", key, len(items), minLen)
	}
}

func checkWebfingerResponse(t *testing.T, srv *httptest.Server, app *Application, handle string) {
	t.Helper()

	wfURL := srv.URL + "/.well-known/webfinger?resource=acct:" + handle + "@" + app.config.ActivityPubDomain
	wfReq, _ := http.NewRequest(http.MethodGet, wfURL, nil)
	wfRes, err := http.DefaultClient.Do(wfReq)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = wfRes.Body.Close() }()
	if wfRes.StatusCode != http.StatusOK {
		t.Fatalf("webfinger status = %d, want %d", wfRes.StatusCode, http.StatusOK)
	}
	if ct := wfRes.Header.Get("Content-Type"); !strings.Contains(ct, "application/jrd+json") {
		t.Fatalf("webfinger content-type = %q", ct)
	}
	wf := decodeJSON(t, wfRes.Body)
	if extractString(wf, "subject") == "" {
		t.Fatal("webfinger subject missing")
	}
}

func checkActorMetadata(t *testing.T, srv *httptest.Server, handle string) {
	t.Helper()

	actorReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/actors/"+handle, nil)
	actorRes, err := http.DefaultClient.Do(actorReq)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = actorRes.Body.Close() }()
	if actorRes.StatusCode != http.StatusOK {
		t.Fatalf("actor status = %d, want %d", actorRes.StatusCode, http.StatusOK)
	}
	if ct := actorRes.Header.Get(
		"Content-Type",
	); !strings.Contains(
		ct,
		"application/activity+json",
	) {
		t.Fatalf("actor content-type = %q", ct)
	}
	actorPayload := decodeJSON(t, actorRes.Body)
	if extractString(actorPayload, "preferredUsername") != handle {
		t.Fatalf("actor preferredUsername mismatch")
	}
}

func checkOutboxContainsTypes(t *testing.T, srv *httptest.Server, handle string) {
	t.Helper()

	outboxReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/actors/"+handle+"/outbox", nil)
	outboxRes, err := http.DefaultClient.Do(outboxReq)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = outboxRes.Body.Close() }()
	if outboxRes.StatusCode != http.StatusOK {
		t.Fatalf("outbox status = %d, want %d", outboxRes.StatusCode, http.StatusOK)
	}
	outboxPayload := decodeJSON(t, outboxRes.Body)
	items, ok := outboxPayload["orderedItems"].([]any)
	if !ok || len(items) < 2 {
		t.Fatalf("outbox orderedItems length = %d, want >= 2", len(items))
	}

	hasNote := false
	hasArticle := false
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		obj, ok := m["object"].(map[string]any)
		if !ok {
			continue
		}
		typeName, _ := obj["type"].(string)
		if typeName == noteObjectType {
			hasNote = true
		}
		if typeName == "Article" {
			hasArticle = true
		}
	}
	if !hasNote || !hasArticle {
		t.Fatalf("outbox missing note/article, hasNote=%t hasArticle=%t", hasNote, hasArticle)
	}
}

// TestIntegrationAudioAndPictureUploadToMinio verifies media uploads persist in MinIO.
//
//nolint:gocognit // end-to-end upload assertions intentionally exercise many branches.
func TestIntegrationAudioAndPictureUploadToMinio(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped in short mode")
	}

	db := integrationDB(t)
	resetDatabase(t, db)

	app := newIntegrationApp(t)
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()
	app.config.ActivityPubBaseURL = srv.URL
	app.config.ActivityPubDomain = localhostDomain

	client, err := minio.New("localhost:9000", &minio.Options{
		Creds:  credentials.NewStaticV4("minioadmin", "minioadmin", ""),
		Secure: false,
	})
	if err != nil {
		t.Skipf("minio client setup failed: %v", err)
	}
	ctx := context.Background()
	if _, err := client.ListBuckets(ctx); err != nil {
		t.Skipf("minio service unavailable: %v", err)
	}

	handle := "media" + strings.ReplaceAll(time.Now().UTC().Format("150405.000000"), ".", "")
	token := signupUser(t, srv, handle, "Media User")
	actorID := mustActorID(t, db, handle)

	pictureContents := []byte("hello picture bytes")
	pictureReq := multipartUploadRequest(t, srv.URL+"/pictures/partials/create", map[string]string{
		"token":      token,
		"caption":    "test picture",
		"visibility": models.VisibilityPublic,
	}, "photo.png", "image/png", pictureContents)
	pictureRes := doMultipart(t, pictureReq)
	defer func() { _ = pictureRes.Body.Close() }()
	if pictureRes.StatusCode != http.StatusCreated {
		t.Fatalf("picture upload status = %d, want %d", pictureRes.StatusCode, http.StatusCreated)
	}
	pictureFragment, err := io.ReadAll(pictureRes.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pictureFragment), `/pictures/`) ||
		!strings.Contains(string(pictureFragment), `/preview`) {
		t.Fatalf("picture create response missing preview image path")
	}

	var pictureBucket, pictureKey, pictureFilename string
	if err := db.QueryRow(`SELECT bucket, object_key, original_filename FROM media_assets WHERE owner_actor_id = $1 ORDER BY id DESC LIMIT 1`, actorID).
		Scan(&pictureBucket, &pictureKey, &pictureFilename); err != nil {
		t.Fatal(err)
	}
	if pictureBucket != "pictures" {
		t.Fatalf("picture bucket = %q, want %q", pictureBucket, "pictures")
	}
	if pictureFilename != "photo.png" {
		t.Fatalf("picture filename = %q, want %q", pictureFilename, "photo.png")
	}
	if obj, err := client.GetObject(
		ctx,
		pictureBucket,
		pictureKey,
		minio.GetObjectOptions{},
	); err != nil {
		t.Fatalf("picture object missing from minio: %v", err)
	} else {
		defer func() { _ = obj.Close() }()
		data, err := io.ReadAll(obj)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(data, pictureContents) {
			t.Fatalf("picture object bytes = %q, want %q", string(data), string(pictureContents))
		}
	}

	audioContents := []byte("hello audio bytes")
	audioReq := multipartUploadRequest(t, srv.URL+"/audio/partials/create", map[string]string{
		"token":       token,
		"title":       "test audio",
		"description": "test description",
		"visibility":  models.VisibilityPublic,
	}, "track.mp3", "audio/mpeg", audioContents)
	audioRes := doMultipart(t, audioReq)
	defer func() { _ = audioRes.Body.Close() }()
	if audioRes.StatusCode != http.StatusCreated {
		t.Fatalf("audio upload status = %d, want %d", audioRes.StatusCode, http.StatusCreated)
	}
	audioFragment, err := io.ReadAll(audioRes.Body)
	if err != nil {
		t.Fatal(err)
	}
	requireFragmentContains(t, string(audioFragment), "<audio", "/audio/", "/preview")

	var audioBucket, audioKey, audioFilename string
	if err := db.QueryRow(`SELECT ma.bucket, ma.object_key, ma.original_filename FROM audio_posts ap JOIN media_assets ma ON ma.id = ap.media_asset_id WHERE ap.actor_id = $1 ORDER BY ap.id DESC LIMIT 1`, actorID).
		Scan(&audioBucket, &audioKey, &audioFilename); err != nil {
		t.Fatal(err)
	}
	if audioBucket != "audio" {
		t.Fatalf("audio bucket = %q, want %q", audioBucket, "audio")
	}
	if audioFilename != "track.mp3" {
		t.Fatalf("audio filename = %q, want %q", audioFilename, "track.mp3")
	}
	if obj, err := client.GetObject(
		ctx,
		audioBucket,
		audioKey,
		minio.GetObjectOptions{},
	); err != nil {
		t.Fatalf("audio object missing from minio: %v", err)
	} else {
		defer func() { _ = obj.Close() }()
		data, err := io.ReadAll(obj)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(data, audioContents) {
			t.Fatalf("audio object bytes = %q, want %q", string(data), string(audioContents))
		}
	}
}

func newIntegrationApp(t *testing.T) *Application {
	t.Helper()
	dsn := integrationDSN()
	cfg := Config{
		Env:                 "test",
		Port:                0,
		DatabaseDSN:         dsn,
		DatabaseMaxOpenConn: 5,
		DatabaseMaxIdleConn: 5,
		DatabaseMaxIdleTime: "5m",
		JWTSecret:           "integration-secret-123456",
		ActivityPubBaseURL:  "http://localhost",
		ActivityPubDomain:   localhostDomain,
		StorageEndpoint:     "http://localhost:9000",
		StorageBucket:       "multiverse",
		StorageRegion:       "us-east-1",
		StorageAccessKey:    "minioadmin",
		StorageSecretKey:    "minioadmin",
		StorageUseSSL:       false,
		InboxMaxDateSkewSec: 300,
		OutboxPollSec:       1,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	return NewApplication(cfg, logger)
}

func mustActorID(t *testing.T, db *sql.DB, handle string) int64 {
	t.Helper()
	var actorID int64
	if err := db.QueryRow(`SELECT id FROM actors WHERE handle = $1`, handle).
		Scan(&actorID); err != nil {
		t.Fatal(err)
	}

	return actorID
}

func multipartUploadRequest(
	t *testing.T,
	url string,
	values map[string]string,
	fileName, contentType string,
	contents []byte,
) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range values {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	fileWriter, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fileWriter.Write(contents); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, url, &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if contentType != "" {
		req.Header.Set("Content-Type", writer.FormDataContentType())
	}

	return req
}

func doMultipart(t *testing.T, req *http.Request) *http.Response {
	t.Helper()
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	return res
}

func integrationDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", integrationDSN())
	if err != nil {
		t.Skipf("integration db open failed: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Skipf("integration db ping failed: %v", err)
	}
	runMigrations(t, db)

	return db
}

func integrationDSN() string {
	dsn := strings.TrimSpace(os.Getenv("INTEGRATION_TEST_DSN"))
	if dsn != "" {
		return dsn
	}

	return "postgres://multiverse:multiverse@localhost:5432/multiverse?sslmode=disable"
}

func runMigrations(t *testing.T, db *sql.DB) {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	migrationsDir := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "migrations"))
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	files := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		files = append(files, entry.Name())
	}
	sort.Strings(files)
	for _, name := range files {
		// #nosec G304 -- migration names come from filtered os.ReadDir entries under a fixed test directory.
		content, err := os.ReadFile(filepath.Join(migrationsDir, name))
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		if _, err := db.Exec(string(content)); err != nil {
			t.Fatalf("exec migration %s: %v", name, err)
		}
	}
}

func resetDatabase(t *testing.T, db *sql.DB) {
	t.Helper()
	stmt := `
TRUNCATE TABLE
  follows,
  blog_posts,
  micro_posts,
  picture_post_assets,
  picture_posts,
  video_posts,
	audio_posts,
  comments,
  activities,
  inbox_events,
  outbox_events,
  token_revocations,
  media_assets,
	password_reset_tokens,
	user_settings,
	site_configs,
  actors,
  users
RESTART IDENTITY CASCADE;`
	if _, err := db.Exec(stmt); err != nil {
		t.Fatalf("truncate tables: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO site_configs (key, enabled, description)
		VALUES
		    ('signup_enabled', true, 'Allow new user signups'),
		    ('password_reset_enabled', true, 'Allow users to request password reset emails'),
		    ('federation_enabled', true, 'Allow federation delivery and inbox processing'),
		    ('oidc_enabled', true, 'Allow login through OpenID Connect providers'),
		    ('oidc_login_enabled', true, 'Show OIDC login buttons in the UI'),
		    ('blog_enabled', true, 'Show the blog vertical and its feeds'),
		    ('micro_blog_enabled', true, 'Show the micro-blog vertical and its feeds'),
		    ('pictures_enabled', true, 'Show the pictures vertical'),
		    ('videos_enabled', true, 'Show the videos vertical'),
		    ('audio_enabled', true, 'Show the audio vertical')
		ON CONFLICT (key) DO UPDATE SET enabled = EXCLUDED.enabled, description = EXCLUDED.description, updated_at = now()
	`); err != nil {
		t.Fatalf("seed site configs: %v", err)
	}
}

func doJSON(t *testing.T, method, url string, payload any, token string) *http.Response {
	t.Helper()
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	responseBody, err := io.ReadAll(res.Body)
	if err != nil {
		_ = res.Body.Close()
		t.Fatal(err)
	}
	if err := res.Body.Close(); err != nil {
		t.Fatal(err)
	}
	res.Body = io.NopCloser(bytes.NewReader(responseBody))
	res.ContentLength = int64(len(responseBody))

	return res
}

func decodeJSON(t *testing.T, r io.ReadCloser) map[string]any {
	t.Helper()
	defer func() { _ = r.Close() }()
	var out map[string]any
	if err := json.NewDecoder(r).Decode(&out); err != nil {
		t.Fatal(err)
	}

	return out
}

func extractString(m map[string]any, key string) string {
	value, _ := m[key].(string)

	return value
}
