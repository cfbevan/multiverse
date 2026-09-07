package ui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cfbevan/multiverse/internal/models"
	ap "github.com/cfbevan/multiverse/internal/platform/activitypub"
)

const (
	activityContentType = "application/activity+json"
	jrdContentType      = "application/jrd+json"
	actorOutboxPageSize = 20
	defaultInboxSkew    = 5 * time.Minute
	acctPartsCount      = 2
)

func (app *Application) webfinger(w http.ResponseWriter, r *http.Request) {
	resource := strings.TrimSpace(r.URL.Query().Get("resource"))
	handle, domain, err := parseAcctResource(resource)
	if err != nil {
		app.badRequestResponse(w, r, err)

		return
	}

	if domain != app.config.ActivityPubDomain {
		app.notFoundResponse(w, r)

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), defaultRequestTimeout)
	defer cancel()

	actor, err := app.actors.GetByHandleAndDomain(ctx, handle, domain)
	if err != nil {
		if errors.Is(err, models.ErrRecordNotFound) {
			app.notFoundResponse(w, r)

			return
		}
		app.serverErrorResponse(w, r, err)

		return
	}

	actorURL := actorURL(strings.TrimSuffix(app.config.ActivityPubBaseURL, "/"), actor.Handle)
	payload := envelope{
		"subject": fmt.Sprintf("acct:%s@%s", actor.Handle, actor.Domain),
		"links": []envelope{
			{
				"rel":   "self",
				typeKey: activityContentType,
				"href":  actorURL,
			},
		},
	}

	headers := make(http.Header)
	headers.Set("Content-Type", jrdContentType)
	if err := app.writeJSON(w, http.StatusOK, payload, headers); err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) actor(w http.ResponseWriter, r *http.Request) {
	handle := strings.TrimSpace(r.PathValue("name"))
	if handle == "" {
		app.notFoundResponse(w, r)

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), defaultRequestTimeout)
	defer cancel()

	actor, err := app.actors.GetByHandleAndDomain(ctx, handle, app.config.ActivityPubDomain)
	if err != nil {
		if errors.Is(err, models.ErrRecordNotFound) {
			app.notFoundResponse(w, r)

			return
		}
		app.serverErrorResponse(w, r, err)

		return
	}

	baseURL := strings.TrimSuffix(app.config.ActivityPubBaseURL, "/")
	payload := envelope{
		activityContextKey: []string{
			activityStreamsNS,
			"https://w3id.org/security/v1",
		},
		"id":                actorURL(baseURL, actor.Handle),
		typeKey:             "Person",
		"preferredUsername": actor.Handle,
		"inbox":             actor.InboxURL,
		"outbox":            actor.OutboxURL,
		"followers":         actor.FollowersURL,
		"following":         actor.FollowingURL,
		"publicKey": envelope{
			"id":           actor.PublicKeyID,
			"owner":        actorURL(baseURL, actor.Handle),
			"publicKeyPem": actor.PublicKeyPEM,
		},
	}

	headers := make(http.Header)
	headers.Set("Content-Type", activityContentType)
	if err := app.writeJSON(w, http.StatusOK, payload, headers); err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) actorOutbox(w http.ResponseWriter, r *http.Request) {
	handle := strings.TrimSpace(r.PathValue("name"))
	if handle == "" {
		app.notFoundResponse(w, r)

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), defaultRequestTimeout)
	defer cancel()

	actor, err := app.actors.GetByHandleAndDomain(ctx, handle, app.config.ActivityPubDomain)
	if err != nil {
		if errors.Is(err, models.ErrRecordNotFound) {
			app.notFoundResponse(w, r)

			return
		}
		app.serverErrorResponse(w, r, err)

		return
	}

	posts, err := app.microPosts.ListByActor(ctx, actor.ID, actorOutboxPageSize, 0)
	if err != nil {
		app.serverErrorResponse(w, r, err)

		return
	}

	blogPosts, err := app.blogPosts.ListByActor(ctx, actor.ID, actorOutboxPageSize, 0)
	if err != nil {
		app.serverErrorResponse(w, r, err)

		return
	}

	audioPosts, err := app.audioPosts.ListByActor(ctx, actor.ID, actorOutboxPageSize, 0)
	if err != nil {
		app.serverErrorResponse(w, r, err)

		return
	}

	baseURL := strings.TrimSuffix(app.config.ActivityPubBaseURL, "/")
	items := make([]envelope, 0, len(posts))
	for _, p := range posts {
		items = append(items, envelope{
			"id":     fmt.Sprintf("%s/activities/create/micro/%d", baseURL, p.ID),
			typeKey:  createActivityType,
			actorKey: actorURL(baseURL, actor.Handle),
			objectKey: envelope{
				"id":            p.APObjectID,
				typeKey:         noteObjectType,
				contentKey:      p.Content,
				publishedKey:    p.PublishedAt.UTC().Format(time.RFC3339),
				attributedToKey: actorURL(baseURL, actor.Handle),
			},
			publishedKey: p.PublishedAt.UTC().Format(time.RFC3339),
		})
	}

	for _, p := range blogPosts {
		items = append(items, envelope{
			"id":     fmt.Sprintf("%s/activities/create/blog/%d", baseURL, p.ID),
			typeKey:  createActivityType,
			actorKey: actorURL(baseURL, actor.Handle),
			objectKey: envelope{
				"id":            p.APObjectID,
				typeKey:         "Article",
				nameKey:         p.Title,
				contentKey:      p.BodyHTML,
				publishedKey:    p.PublishedAt.UTC().Format(time.RFC3339),
				attributedToKey: actorURL(baseURL, actor.Handle),
			},
			publishedKey: p.PublishedAt.UTC().Format(time.RFC3339),
		})
	}

	for _, p := range audioPosts {
		items = append(items, envelope{
			"id":     fmt.Sprintf("%s/activities/create/audio/%d", baseURL, p.ID),
			typeKey:  createActivityType,
			actorKey: actorURL(baseURL, actor.Handle),
			objectKey: envelope{
				"id":            p.APObjectID,
				typeKey:         "Audio",
				nameKey:         p.Title,
				"summary":       p.Description,
				publishedKey:    p.PublishedAt.UTC().Format(time.RFC3339),
				attributedToKey: actorURL(baseURL, actor.Handle),
			},
			publishedKey: p.PublishedAt.UTC().Format(time.RFC3339),
		})
	}

	payload := envelope{
		"@context":     []string{activityStreamsNS},
		"id":           actor.OutboxURL,
		typeKey:        "OrderedCollection",
		"totalItems":   len(items),
		"orderedItems": items,
	}

	headers := make(http.Header)
	headers.Set("Content-Type", activityContentType)
	if err := app.writeJSON(w, http.StatusOK, payload, headers); err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) actorFollowers(w http.ResponseWriter, r *http.Request) {
	app.actorCollectionCounts(w, r, true)
}

func (app *Application) actorFollowing(w http.ResponseWriter, r *http.Request) {
	app.actorCollectionCounts(w, r, false)
}

func (app *Application) actorCollectionCounts(
	w http.ResponseWriter,
	r *http.Request,
	followers bool,
) {
	handle := strings.TrimSpace(r.PathValue("name"))
	if handle == "" {
		app.notFoundResponse(w, r)

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), defaultRequestTimeout)
	defer cancel()

	actor, err := app.actors.GetByHandleAndDomain(ctx, handle, app.config.ActivityPubDomain)
	if err != nil {
		if errors.Is(err, models.ErrRecordNotFound) {
			app.notFoundResponse(w, r)

			return
		}
		app.serverErrorResponse(w, r, err)

		return
	}

	var count int
	if followers {
		count, err = app.actors.CountFollowers(ctx, actor.ID)
	} else {
		count, err = app.actors.CountFollowing(ctx, actor.ID)
	}
	if err != nil {
		app.serverErrorResponse(w, r, err)

		return
	}

	collectionID := actor.FollowingURL
	if followers {
		collectionID = actor.FollowersURL
	}

	payload := envelope{
		"@context":     []string{activityStreamsNS},
		"id":           collectionID,
		typeKey:        "OrderedCollection",
		"totalItems":   count,
		"orderedItems": []any{},
	}

	headers := make(http.Header)
	headers.Set("Content-Type", activityContentType)
	if err := app.writeJSON(w, http.StatusOK, payload, headers); err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) sharedInbox(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		app.methodNotAllowedResponse(w, r)

		return
	}

	if !isActivityJSONRequest(r) {
		app.invalidAuthenticationTokenResponse(w, r)

		return
	}

	body, err := app.readInboxBody(w, r)
	if err != nil {
		if errors.Is(err, models.ErrEditConflict) {
			return
		}
		app.invalidAuthenticationTokenResponse(w, r)

		return
	}

	sigParams, err := app.parseInboxSignature(r)
	if err != nil {
		app.invalidAuthenticationTokenResponse(w, r)

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), defaultRequestTimeout)
	defer cancel()

	if err := app.verifyInboxSignature(ctx, r, sigParams, body); err != nil {
		app.invalidAuthenticationTokenResponse(w, r)

		return
	}

	if err := app.persistInboxEvent(ctx, r, sigParams, body); err != nil {
		if errors.Is(err, models.ErrEditConflict) {
			w.WriteHeader(http.StatusAccepted)

			return
		}
		app.serverErrorResponse(w, r, err)

		return
	}

	if err := app.writeJSON(w, http.StatusAccepted, envelope{
		statusKey:  "accepted",
		"event_id": 0,
	}, nil); err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func isActivityJSONRequest(r *http.Request) bool {
	ct := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))

	return strings.Contains(ct, "application/activity+json") ||
		strings.Contains(ct, "application/ld+json")
}

func (app *Application) readInboxBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	dateHeader := strings.TrimSpace(r.Header.Get("Date"))
	dateValue, err := ap.ParseDateHeader(dateHeader)
	if err != nil {
		return nil, err
	}

	maxSkew := time.Duration(app.config.InboxMaxDateSkewSec) * time.Second
	if app.config.InboxMaxDateSkewSec <= 0 {
		maxSkew = defaultInboxSkew
	}
	if err := ap.ValidateDateSkew(dateValue, time.Now().UTC(), maxSkew); err != nil {
		return nil, err
	}

	maxBodyBytes := app.config.InboxMaxBodyBytes
	if maxBodyBytes <= 0 {
		maxBodyBytes = 1_048_576
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, errors.New("body must not be empty")
	}

	digestHeader := strings.TrimSpace(r.Header.Get("Digest"))
	if err := ap.ValidateDigest(body, digestHeader); err != nil {
		return nil, err
	}

	return body, nil
}

func (app *Application) parseInboxSignature(r *http.Request) (ap.SignatureParams, error) {
	signatureHeader := strings.TrimSpace(r.Header.Get("Signature"))
	sigParams, err := ap.ParseSignatureHeader(signatureHeader)
	if err != nil {
		return ap.SignatureParams{}, err
	}

	policy := ap.SignaturePolicy{
		AllowedAlgorithms: map[string]struct{}{"rsa-sha256": {}},
		RequiredHeaders: map[string]struct{}{
			"(request-target)": {},
			"date":             {},
			"digest":           {},
		},
		RequireHost: app.config.InboxRequireHost,
	}
	if err := ap.ValidateSignaturePolicy(sigParams, policy); err != nil {
		return ap.SignatureParams{}, err
	}

	return sigParams, nil
}

func (app *Application) verifyInboxSignature(
	ctx context.Context,
	r *http.Request,
	sigParams ap.SignatureParams,
	body []byte,
) error {
	actor, err := app.actors.GetByPublicKeyID(ctx, sigParams.KeyID)
	if err != nil {
		return err
	}

	signingString, err := ap.BuildSigningString(r, sigParams.Headers)
	if err != nil {
		return err
	}

	if err := ap.VerifyRSASignature(
		signingString,
		sigParams.Signature,
		actor.PublicKeyPEM,
	); err != nil {
		return err
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return err
	}

	return nil
}

func (app *Application) persistInboxEvent(
	ctx context.Context,
	r *http.Request,
	sigParams ap.SignatureParams,
	body []byte,
) error {
	actor, err := app.actors.GetByPublicKeyID(ctx, sigParams.KeyID)
	if err != nil {
		return err
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return err
	}

	sourceDomain := domainFromActorField(payload)
	if sourceDomain == "" {
		sourceDomain = "unknown"
	}

	idemKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idemKey == "" {
		hash := sha256.Sum256(body)
		idemKey = hex.EncodeToString(hash[:])
	}

	event := &models.InboxEvent{
		ActorID:        &actor.ID,
		SourceDomain:   sourceDomain,
		SignatureKeyID: sigParams.KeyID,
		Digest:         strings.TrimSpace(r.Header.Get("Digest")),
		RequestID:      strings.TrimSpace(r.Header.Get("X-Request-ID")),
		IdempotencyKey: idemKey,
		Payload:        body,
		Status:         "pending",
	}

	return app.inboxEvents.Insert(ctx, event)
}

func parseAcctResource(resource string) (string, string, error) {
	if !strings.HasPrefix(resource, "acct:") {
		return "", "", errors.New("resource must use acct: scheme")
	}
	acct := strings.TrimPrefix(resource, "acct:")
	parts := strings.Split(acct, "@")
	if len(parts) != acctPartsCount {
		return "", "", errors.New("resource must be in acct:user@domain form")
	}
	handle := strings.TrimSpace(parts[0])
	domain := strings.TrimSpace(parts[1])
	if handle == "" || domain == "" {
		return "", "", errors.New("resource contains empty handle or domain")
	}

	return handle, domain, nil
}

func domainFromActorField(payload map[string]any) string {
	rawActor, ok := payload["actor"].(string)
	if !ok {
		return ""
	}

	u, err := url.Parse(rawActor)
	if err != nil {
		return ""
	}

	return u.Hostname()
}

func actorURL(baseURL, handle string) string {
	return baseURL + "/actors/" + handle
}
