package ui

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/cfbevan/multiverse/internal/models"
	"github.com/cfbevan/multiverse/internal/validator"
)

const (
	maxAudioTitleLength       = 200
	maxAudioDescriptionLength = 2000
	maxAudioUploadBytes       = 50 << 20
	audioCreateTimeout        = 5 * time.Second
)

func (app *Application) audio(w http.ResponseWriter, r *http.Request) {
	if !app.requireSiteSectionEnabled(w, r, "audio_enabled") {
		return
	}

	app.render(w, r, "audio.html", map[string]any{
		feedLabelKey:       localFeedValue,
		isAuthenticatedKey: app.contextGetUser(r) != nil,
	}, http.StatusOK)
}

func (app *Application) audioListPartial(w http.ResponseWriter, r *http.Request) {
	app.renderSectionListPartial(
		w,
		r,
		"audio_enabled",
		"audio.html",
		"audioList",
		func(req *http.Request) (string, *models.User, error) {
			return app.resolveSectionFeed(req, localFeedValue)
		},
		app.loadAudioFeedPosts,
	)
}

func (app *Application) createAudioPost(w http.ResponseWriter, r *http.Request) {
	if !app.requireSiteSectionEnabled(w, r, "audio_enabled") {
		return
	}

	user := app.contextGetUser(r)
	if user == nil {
		app.authenticationRequiredResponse(w, r)

		return
	}

	var input struct {
		Title        string `json:"title"`
		Description  string `json:"description"`
		Visibility   string `json:"visibility"`
		MediaAssetID int64  `json:"media_asset_id"`
	}
	if err := app.readJSON(w, r, &input); err != nil {
		app.badRequestResponse(w, r, err)

		return
	}

	if strings.TrimSpace(input.Visibility) == "" {
		input.Visibility = models.VisibilityPublic
	}

	v := validator.NewValidator()
	v.CheckField(validator.NotBlank(input.Title), "title", "must be provided")
	v.CheckField(
		validator.MaxChars(input.Title, maxAudioTitleLength),
		"title",
		"must not be more than 200 characters long",
	)
	v.CheckField(validator.PermittedValue(input.Visibility,
		models.VisibilityPublic,
		models.VisibilityUnlisted,
		models.VisibilityFollowers,
		models.VisibilityPrivate,
	), "visibility", "must be one of public, unlisted, followers, private")
	v.CheckField(input.MediaAssetID > 0, "media_asset_id", "must be provided")
	if !v.Valid() {
		app.failedValidationResponse(w, r, v.FieldErrors)

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), defaultRequestTimeout)
	defer cancel()

	actor, err := app.actors.GetByUserID(ctx, user.ID)
	if err != nil {
		if errors.Is(err, models.ErrRecordNotFound) {
			app.authenticationRequiredResponse(w, r)

			return
		}
		app.serverErrorResponse(w, r, err)

		return
	}

	baseURL := strings.TrimSuffix(app.config.ActivityPubBaseURL, "/")
	post := &models.AudioPost{
		ActorID:      actor.ID,
		Title:        strings.TrimSpace(input.Title),
		Description:  strings.TrimSpace(input.Description),
		Visibility:   input.Visibility,
		MediaAssetID: input.MediaAssetID,
		APObjectID: fmt.Sprintf(
			"%s/objects/audio/%d-%d",
			baseURL,
			actor.ID,
			time.Now().UnixNano(),
		),
	}

	if err := app.audioPosts.Insert(ctx, post); err != nil {
		app.serverErrorResponse(w, r, err)

		return
	}

	activityID := fmt.Sprintf("%s/activities/create/audio/%d", baseURL, post.ID)
	activityObject := envelope{
		"id":            post.APObjectID,
		typeKey:         "Audio",
		nameKey:         post.Title,
		"summary":       post.Description,
		publishedKey:    post.PublishedAt.UTC().Format(time.RFC3339),
		attributedToKey: actorURL(baseURL, actor.Handle),
	}
	if err := app.enqueueCreateForFollowers(ctx, actor, activityObject, activityID); err != nil {
		app.logger.Error(
			"failed to enqueue audio federation",
			"error",
			err.Error(),
			"post_id",
			post.ID,
		)
	}

	err = app.writeJSON(w, http.StatusCreated, envelope{
		"audio_post": envelope{
			"id":            post.ID,
			actorIDKey:      post.ActorID,
			titleKey:        post.Title,
			descriptionKey:  post.Description,
			visibilityKey:   post.Visibility,
			mediaAssetIDKey: post.MediaAssetID,
			apObjectIDKey:   post.APObjectID,
			publishedAtKey:  post.PublishedAt,
		},
	}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) createAudioPostHTMX(w http.ResponseWriter, r *http.Request) {
	app.createTitledMediaPostHTMX(w, r, "audio")
}

func (app *Application) validateAudioUploadRequest(
	w http.ResponseWriter,
	r *http.Request,
	upload *multipartUpload,
) (*models.Actor, string, string, string, bool) {
	return app.validateAudioVideoUploadRequest(
		w,
		r,
		upload,
		maxAudioUploadBytes,
		maxAudioTitleLength,
		maxAudioDescriptionLength,
		"audio.html",
		"audioFormError",
		"must be less than 50MB",
	)
}

func (app *Application) createAudioPostFromUpload(
	ctx context.Context,
	actor *models.Actor,
	upload *multipartUpload,
	title string,
	description string,
	visibility string,
) (*models.AudioPost, error) {
	mediaType := strings.TrimSpace(upload.FileContentType)
	if mediaType == "" {
		mediaType = defaultMediaType
	}
	originalFilename := strings.TrimSpace(upload.FileName)
	if originalFilename == "" {
		originalFilename = defaultUploadFilename
	}

	objectKey := fmt.Sprintf(
		"%d/%d%s",
		actor.ID,
		time.Now().UnixNano(),
		strings.ToLower(filepath.Ext(originalFilename)),
	)
	isPublic := visibility == models.VisibilityPublic || visibility == models.VisibilityUnlisted
	if err := app.uploadMediaFile(ctx, "audio", objectKey, mediaType, upload.FileData); err != nil {
		return nil, err
	}

	var mediaAssetID int64
	if err := app.db.QueryRowContext(
		ctx,
		`
		INSERT INTO media_assets (
			owner_actor_id,
			bucket,
			object_key,
			media_type,
			byte_size,
			sha256_hex,
			original_filename,
			is_public
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id`,
		actor.ID,
		"audio",
		objectKey,
		mediaType,
		upload.FileSize,
		upload.FileSHA256,
		originalFilename,
		isPublic,
	).Scan(&mediaAssetID); err != nil {
		return nil, err
	}

	post := &models.AudioPost{
		ActorID:      actor.ID,
		Title:        strings.TrimSpace(title),
		Description:  strings.TrimSpace(description),
		Visibility:   visibility,
		MediaAssetID: mediaAssetID,
		APObjectID: fmt.Sprintf(
			"%s/objects/audio/%d-%d",
			strings.TrimSuffix(app.config.ActivityPubBaseURL, "/"),
			actor.ID,
			time.Now().UnixNano(),
		),
	}
	if err := app.audioPosts.Insert(ctx, post); err != nil {
		return nil, err
	}

	return post, nil
}

func (app *Application) listAudioPosts(w http.ResponseWriter, r *http.Request) {
	if !app.requireSiteSectionEnabled(w, r, "audio_enabled") {
		return
	}

	handle := strings.TrimSpace(r.URL.Query().Get("handle"))
	feed, user, err := app.resolveSectionFeed(r, localFeedValue)
	if err != nil {
		app.serverErrorResponse(w, r, err)

		return
	}

	limit, offset := sectionListQuery(r)
	ctx, cancel := context.WithTimeout(r.Context(), defaultRequestTimeout)
	defer cancel()

	posts, err := app.loadSectionPostsByHandle(
		ctx,
		feed,
		user,
		handle,
		limit,
		offset,
		app.loadAudioFeedPosts,
		app.audioPosts.ListByActor,
	)
	if err != nil {
		if errors.Is(err, models.ErrRecordNotFound) {
			app.notFoundResponse(w, r)

			return
		}
		app.serverErrorResponse(w, r, err)

		return
	}

	items := make([]envelope, 0, len(posts))
	for _, p := range posts {
		items = append(items, envelope{
			"id":            p.ID,
			actorIDKey:      p.ActorID,
			titleKey:        p.Title,
			descriptionKey:  p.Description,
			visibilityKey:   p.Visibility,
			mediaAssetIDKey: p.MediaAssetID,
			apObjectIDKey:   p.APObjectID,
			publishedAtKey:  p.PublishedAt,
		})
	}

	if err = app.writeJSON(
		w,
		http.StatusOK,
		envelope{"audio_posts": items, countKey: len(items), feedKey: feed},
		nil,
	); err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) loadAudioFeedPosts(
	ctx context.Context,
	feed string,
	user *models.User,
	limit, offset int,
) ([]models.AudioPost, error) {
	return app.loadFeedPostsBySection(
		ctx,
		feed,
		user,
		limit,
		offset,
		app.audioPosts.ListPublic,
		audioLocalQuery(),
		audioFederatedQuery(),
		audioFollowingQuery(),
		scanAudioPosts,
	)
}

func audioLocalQuery() string {
	return `SELECT ap.id, ap.actor_id, ap.title, ap.description, ap.visibility, ap.media_asset_id, ap.ap_object_id, ap.published_at, ap.updated_at, ap.deleted_at, ap.version FROM audio_posts ap JOIN actors a ON a.id = ap.actor_id WHERE ap.deleted_at IS NULL AND ap.visibility = 'public' AND a.is_local = true ORDER BY ap.published_at DESC LIMIT $1 OFFSET $2`
}

func audioFederatedQuery() string {
	return `SELECT ap.id, ap.actor_id, ap.title, ap.description, ap.visibility, ap.media_asset_id, ap.ap_object_id, ap.published_at, ap.updated_at, ap.deleted_at, ap.version FROM audio_posts ap JOIN actors a ON a.id = ap.actor_id WHERE ap.deleted_at IS NULL AND ap.visibility = 'public' AND a.is_local = false ORDER BY ap.published_at DESC LIMIT $1 OFFSET $2`
}

func audioFollowingQuery() string {
	return `SELECT ap.id, ap.actor_id, ap.title, ap.description, ap.visibility, ap.media_asset_id, ap.ap_object_id, ap.published_at, ap.updated_at, ap.deleted_at, ap.version FROM audio_posts ap JOIN actors a ON a.id = ap.actor_id WHERE ap.deleted_at IS NULL AND ap.actor_id IN (SELECT followed_actor_id FROM follows WHERE follower_actor_id = $1 UNION SELECT $1) AND (ap.visibility = 'public' OR ap.actor_id = $1) ORDER BY ap.published_at DESC LIMIT $2 OFFSET $3`
}

func scanAudioPosts(rows *sql.Rows) ([]models.AudioPost, error) {
	return scanRows(rows, func(rows *sql.Rows, p *models.AudioPost) error {
		return rows.Scan(
			&p.ID,
			&p.ActorID,
			&p.Title,
			&p.Description,
			&p.Visibility,
			&p.MediaAssetID,
			&p.APObjectID,
			&p.PublishedAt,
			&p.UpdatedAt,
			&p.DeletedAt,
			&p.Version,
		)
	})
}
