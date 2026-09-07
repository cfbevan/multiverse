package ui

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cfbevan/multiverse/internal/models"
	"github.com/cfbevan/multiverse/internal/validator"
	"github.com/minio/minio-go/v7"
)

const (
	maxPictureUploadBytes       = 50 << 20
	maxVideoUploadBytes         = 500 << 20
	mediaCreateTimeout          = 5 * time.Second
	maxVideoTitleLength         = 200
	maxVideoDescriptionLength   = 2000
	maxVideoDescriptionFileSize = 500 * 1024 * 1024
)

func (app *Application) blogItemPage(w http.ResponseWriter, r *http.Request) {
	post, err := app.loadBlogPostByID(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, models.ErrRecordNotFound) {
			app.notFoundResponse(w, r)

			return
		}
		app.serverErrorResponse(w, r, err)

		return
	}

	app.renderItemPage(w, r, "blog_enabled", "blog.html", []models.BlogPost{*post})
}

func (app *Application) microBlogItemPage(w http.ResponseWriter, r *http.Request) {
	post, err := app.loadMicroPostByID(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, models.ErrRecordNotFound) {
			app.notFoundResponse(w, r)

			return
		}
		app.serverErrorResponse(w, r, err)

		return
	}

	app.renderItemPage(w, r, "micro_blog_enabled", "micro-blog.html", []models.MicroPost{*post})
}

func (app *Application) audioItemPage(w http.ResponseWriter, r *http.Request) {
	app.renderAudioVideoItemPage(
		w,
		r,
		"audio_enabled",
		"audio.html",
		`SELECT id, actor_id, title, description, visibility, media_asset_id, ap_object_id, published_at, updated_at, deleted_at, version FROM audio_posts WHERE id = $1 AND deleted_at IS NULL`,
		func(row *sql.Row, item *models.AudioPost) error {
			return row.Scan(
				&item.ID,
				&item.ActorID,
				&item.Title,
				&item.Description,
				&item.Visibility,
				&item.MediaAssetID,
				&item.APObjectID,
				&item.PublishedAt,
				&item.UpdatedAt,
				&item.DeletedAt,
				&item.Version,
			)
		},
	)
}

func (app *Application) pictureItemPage(w http.ResponseWriter, r *http.Request) {
	post, err := app.loadPicturePostByID(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, models.ErrRecordNotFound) {
			app.notFoundResponse(w, r)

			return
		}
		app.serverErrorResponse(w, r, err)

		return
	}

	app.renderItemPage(w, r, "pictures_enabled", "pictures.html", []models.PicturePost{*post})
}

func (app *Application) audioPreview(w http.ResponseWriter, r *http.Request) {
	app.streamPostMediaPreview(
		w,
		r,
		"audio_enabled",
		`SELECT ap.actor_id, ap.visibility, ma.bucket, ma.object_key, ma.media_type
		FROM audio_posts ap
		JOIN media_assets ma ON ma.id = ap.media_asset_id
		WHERE ap.id = $1 AND ap.deleted_at IS NULL
		LIMIT 1`,
	)
}

func (app *Application) picturePreview(w http.ResponseWriter, r *http.Request) {
	app.streamPostMediaPreview(
		w,
		r,
		"pictures_enabled",
		`SELECT pp.actor_id, pp.visibility, ma.bucket, ma.object_key, ma.media_type
		FROM picture_posts pp
		JOIN picture_post_assets ppa ON ppa.picture_post_id = pp.id
		JOIN media_assets ma ON ma.id = ppa.media_asset_id
		WHERE pp.id = $1 AND pp.deleted_at IS NULL
		ORDER BY ppa.position ASC
		LIMIT 1`,
	)
}

func (app *Application) videoPreview(w http.ResponseWriter, r *http.Request) {
	app.streamPostMediaPreview(
		w,
		r,
		"videos_enabled",
		`SELECT vp.actor_id, vp.visibility, ma.bucket, ma.object_key, ma.media_type
		FROM video_posts vp
		JOIN media_assets ma ON ma.id = vp.media_asset_id
		WHERE vp.id = $1 AND vp.deleted_at IS NULL
		LIMIT 1`,
	)
}

func (app *Application) streamPostMediaPreview(
	w http.ResponseWriter,
	r *http.Request,
	section string,
	query string,
) {
	if !app.requireSiteSectionEnabled(w, r, section) {
		return
	}

	postID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || postID <= 0 {
		app.notFoundResponse(w, r)

		return
	}

	if app.storageClient == nil {
		app.notFoundResponse(w, r)

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), mediaCreateTimeout)
	defer cancel()

	var (
		ownerActorID int64
		visibility   string
		bucket       string
		objectKey    string
		mediaType    string
	)

	err = app.db.QueryRowContext(ctx, query, postID).Scan(
		&ownerActorID,
		&visibility,
		&bucket,
		&objectKey,
		&mediaType,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			app.notFoundResponse(w, r)

			return
		}
		app.serverErrorResponse(w, r, err)

		return
	}

	canView, err := app.canViewContentVisibility(
		ctx,
		ownerActorID,
		visibility,
		app.contextGetUser(r),
	)
	if err != nil {
		app.serverErrorResponse(w, r, err)

		return
	}
	if !canView {
		app.notFoundResponse(w, r)

		return
	}

	obj, err := app.storageClient.GetObject(ctx, bucket, objectKey, minio.GetObjectOptions{})
	if err != nil {
		app.notFoundResponse(w, r)

		return
	}
	defer func() { _ = obj.Close() }()

	info, err := obj.Stat()
	if err != nil {
		app.notFoundResponse(w, r)

		return
	}

	contentType := strings.TrimSpace(mediaType)
	if contentType == "" {
		contentType = strings.TrimSpace(info.ContentType)
	}
	if contentType == "" {
		contentType = defaultMediaType
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=60")
	if info.Size >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size, 10))
	}

	if _, err := io.Copy(w, obj); err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) canViewContentVisibility(
	ctx context.Context,
	ownerActorID int64,
	visibility string,
	user *models.User,
) (bool, error) {
	switch visibility {
	case models.VisibilityPublic, models.VisibilityUnlisted:
		return true, nil
	case models.VisibilityPrivate, models.VisibilityFollowers:
		if user == nil {
			return false, nil
		}

		viewerActor, err := app.actors.GetByUserID(ctx, user.ID)
		if err != nil {
			if errors.Is(err, models.ErrRecordNotFound) {
				return false, nil
			}

			return false, err
		}

		if viewerActor.ID == ownerActorID {
			return true, nil
		}

		if visibility == models.VisibilityPrivate {
			return false, nil
		}

		var isFollower bool
		err = app.db.QueryRowContext(
			ctx,
			`SELECT EXISTS (SELECT 1 FROM follows WHERE follower_actor_id = $1 AND followed_actor_id = $2)`,
			viewerActor.ID,
			ownerActorID,
		).Scan(&isFollower)
		if err != nil {
			return false, err
		}

		return isFollower, nil
	default:
		return false, nil
	}
}

func (app *Application) videoItemPage(w http.ResponseWriter, r *http.Request) {
	app.renderAudioVideoItemPage(
		w,
		r,
		"videos_enabled",
		"videos.html",
		`SELECT id, actor_id, title, description, visibility, media_asset_id, ap_object_id, published_at, updated_at, deleted_at, version FROM video_posts WHERE id = $1 AND deleted_at IS NULL`,
		func(row *sql.Row, item *models.VideoPost) error {
			return row.Scan(
				&item.ID,
				&item.ActorID,
				&item.Title,
				&item.Description,
				&item.Visibility,
				&item.MediaAssetID,
				&item.APObjectID,
				&item.PublishedAt,
				&item.UpdatedAt,
				&item.DeletedAt,
				&item.Version,
			)
		},
	)
}

func (app *Application) renderAudioVideoItemPage[T any](
	w http.ResponseWriter,
	r *http.Request,
	section string,
	templateName string,
	query string,
	scan func(*sql.Row, *T) error,
) {
	app.renderMediaItemPage(w, r, section, templateName, query, scan)
}

func (app *Application) renderMediaItemPage[T any](
	w http.ResponseWriter,
	r *http.Request,
	section string,
	templateName string,
	query string,
	scan func(*sql.Row, *T) error,
) {
	post, err := app.loadGenericByID[T](r.Context(), r.PathValue("id"), query, scan)
	if err != nil {
		if errors.Is(err, models.ErrRecordNotFound) {
			app.notFoundResponse(w, r)

			return
		}
		app.serverErrorResponse(w, r, err)

		return
	}

	app.renderItemPage(w, r, section, templateName, []T{*post})
}

func (app *Application) renderItemPage(
	w http.ResponseWriter,
	r *http.Request,
	section string,
	templateName string,
	posts any,
) {
	if !app.requireSiteSectionEnabled(w, r, section) {
		return
	}

	app.render(w, r, templateName, map[string]any{
		feedLabelKey:       localFeedValue,
		postsKey:           posts,
		isAuthenticatedKey: app.contextGetUser(r) != nil,
	}, http.StatusOK)
}

func (app *Application) listComments(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	entityType := strings.TrimSpace(qs.Get("entity_type"))
	entityID, err := strconv.ParseInt(strings.TrimSpace(qs.Get("entity_id")), 10, 64)
	if err != nil || entityID <= 0 || entityType == "" {
		app.badRequestResponse(w, r, errors.New("entity_type and entity_id are required"))

		return
	}

	limit := 20
	offset := 0
	if s := strings.TrimSpace(qs.Get("limit")); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	if s := strings.TrimSpace(qs.Get("offset")); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			offset = n
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), defaultRequestTimeout)
	defer cancel()

	comments, err := app.comments.ListByEntity(ctx, entityType, entityID, limit, offset)
	if err != nil {
		app.serverErrorResponse(w, r, err)

		return
	}

	items := make([]envelope, 0, len(comments))
	for _, comment := range comments {
		items = append(items, envelope{
			"id":                  comment.ID,
			entityTypeKey:         comment.EntityType,
			entityIDKey:           comment.EntityID,
			actorIDKey:            comment.ActorID,
			contentKey:            comment.Content,
			"reply_to_comment_id": comment.ReplyToCommentID,
			"created_at":          comment.CreatedAt,
			"updated_at":          comment.UpdatedAt,
		})
	}

	if err := app.writeJSON(
		w,
		http.StatusOK,
		envelope{"comments": items, countKey: len(items)},
		nil,
	); err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) createComment(w http.ResponseWriter, r *http.Request) {
	user := app.contextGetUser(r)
	if user == nil {
		app.authenticationRequiredResponse(w, r)

		return
	}

	var input struct {
		EntityType       string `json:"entity_type"`
		EntityID         int64  `json:"entity_id"`
		Content          string `json:"content"`
		ReplyToCommentID *int64 `json:"reply_to_comment_id"`
	}
	if err := app.readJSON(w, r, &input); err != nil {
		app.badRequestResponse(w, r, err)

		return
	}

	input.EntityType = strings.TrimSpace(input.EntityType)
	input.Content = strings.TrimSpace(input.Content)
	if !validCommentEntityType(input.EntityType) {
		app.failedValidationResponse(
			w,
			r,
			map[string]string{
				entityTypeKey: "must be one of micro_post, blog_post, audio_post, picture_post, video_post",
			},
		)

		return
	}
	if input.EntityID <= 0 {
		app.failedValidationResponse(w, r, map[string]string{entityIDKey: "must be provided"})

		return
	}
	if strings.TrimSpace(input.Content) == "" {
		app.failedValidationResponse(w, r, map[string]string{contentKey: "must be provided"})

		return
	}

	actor, err := app.actors.GetByUserID(r.Context(), user.ID)
	if err != nil {
		if errors.Is(err, models.ErrRecordNotFound) {
			app.authenticationRequiredResponse(w, r)

			return
		}
		app.serverErrorResponse(w, r, err)

		return
	}

	comment := &models.Comment{
		EntityType:       input.EntityType,
		EntityID:         input.EntityID,
		ActorID:          actor.ID,
		Content:          input.Content,
		ReplyToCommentID: input.ReplyToCommentID,
	}
	ctx, cancel := context.WithTimeout(r.Context(), defaultRequestTimeout)
	defer cancel()
	if err := app.comments.Insert(ctx, comment); err != nil {
		app.serverErrorResponse(w, r, err)

		return
	}

	if err := app.writeJSON(w, http.StatusCreated, envelope{
		"comment": envelope{
			"id":                  comment.ID,
			entityTypeKey:         comment.EntityType,
			entityIDKey:           comment.EntityID,
			actorIDKey:            comment.ActorID,
			contentKey:            comment.Content,
			"reply_to_comment_id": comment.ReplyToCommentID,
			"created_at":          comment.CreatedAt,
			"updated_at":          comment.UpdatedAt,
		},
	}, nil); err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func validCommentEntityType(entityType string) bool {
	switch entityType {
	case "micro_post", "blog_post", "audio_post", "picture_post", "video_post":
		return true
	default:
		return false
	}
}

func (app *Application) createPicturePost(w http.ResponseWriter, r *http.Request) {
	if !app.requireSiteSectionEnabled(w, r, "pictures_enabled") {
		return
	}
	user := app.contextGetUser(r)
	if user == nil {
		app.authenticationRequiredResponse(w, r)

		return
	}

	var input struct {
		Caption    string  `json:"caption"`
		Visibility string  `json:"visibility"`
		AssetIDs   []int64 `json:"media_asset_ids"`
	}
	if err := app.readJSON(w, r, &input); err != nil {
		app.badRequestResponse(w, r, err)

		return
	}
	if strings.TrimSpace(input.Visibility) == "" {
		input.Visibility = models.VisibilityPublic
	}
	v := validator.NewValidator()
	v.CheckField(validator.PermittedValue(input.Visibility,
		models.VisibilityPublic,
		models.VisibilityUnlisted,
		models.VisibilityFollowers,
		models.VisibilityPrivate,
	), "visibility", "must be one of public, unlisted, followers, private")
	if !v.Valid() {
		app.failedValidationResponse(w, r, v.FieldErrors)

		return
	}

	actor, err := app.actors.GetByUserID(r.Context(), user.ID)
	if err != nil {
		if errors.Is(err, models.ErrRecordNotFound) {
			app.authenticationRequiredResponse(w, r)

			return
		}
		app.serverErrorResponse(w, r, err)

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), defaultRequestTimeout)
	defer cancel()

	baseURL := strings.TrimSuffix(app.config.ActivityPubBaseURL, "/")
	post := &models.PicturePost{
		ActorID:    actor.ID,
		Caption:    strings.TrimSpace(input.Caption),
		Visibility: input.Visibility,
		APObjectID: fmt.Sprintf(
			"%s/objects/picture/%d-%d",
			baseURL,
			actor.ID,
			time.Now().UnixNano(),
		),
	}
	if err := app.db.QueryRowContext(ctx, `
		INSERT INTO picture_posts (actor_id, caption, visibility, ap_object_id)
		VALUES ($1, $2, $3, $4)
		RETURNING id, published_at, updated_at, version`,
		post.ActorID, post.Caption, post.Visibility, post.APObjectID,
	).Scan(&post.ID, &post.PublishedAt, &post.UpdatedAt, &post.Version); err != nil {
		app.serverErrorResponse(w, r, err)

		return
	}
	for i, assetID := range input.AssetIDs {
		if _, err := app.db.ExecContext(
			ctx,
			`INSERT INTO picture_post_assets (picture_post_id, media_asset_id, position) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
			post.ID,
			assetID,
			i,
		); err != nil {
			app.serverErrorResponse(w, r, err)

			return
		}
	}

	if err := app.writeJSON(
		w,
		http.StatusCreated,
		envelope{
			"picture_post": envelope{
				"id":           post.ID,
				actorIDKey:     post.ActorID,
				"caption":      post.Caption,
				visibilityKey:  post.Visibility,
				apObjectIDKey:  post.APObjectID,
				publishedAtKey: post.PublishedAt,
			},
		},
		nil,
	); err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) createVideoPost(w http.ResponseWriter, r *http.Request) {
	if !app.requireSiteSectionEnabled(w, r, "videos_enabled") {
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
	actor, err := app.actors.GetByUserID(r.Context(), user.ID)
	if err != nil {
		if errors.Is(err, models.ErrRecordNotFound) {
			app.authenticationRequiredResponse(w, r)

			return
		}
		app.serverErrorResponse(w, r, err)

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), defaultRequestTimeout)
	defer cancel()
	baseURL := strings.TrimSuffix(app.config.ActivityPubBaseURL, "/")
	post := &models.VideoPost{
		ActorID:      actor.ID,
		Title:        strings.TrimSpace(input.Title),
		Description:  strings.TrimSpace(input.Description),
		Visibility:   input.Visibility,
		MediaAssetID: input.MediaAssetID,
		APObjectID: fmt.Sprintf(
			"%s/objects/video/%d-%d",
			baseURL,
			actor.ID,
			time.Now().UnixNano(),
		),
	}
	if err := app.db.QueryRowContext(
		ctx,
		`
		INSERT INTO video_posts (actor_id, title, description, visibility, media_asset_id, ap_object_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, published_at, updated_at, version`,
		post.ActorID,
		post.Title,
		post.Description,
		post.Visibility,
		post.MediaAssetID,
		post.APObjectID,
	).Scan(&post.ID, &post.PublishedAt, &post.UpdatedAt, &post.Version); err != nil {
		app.serverErrorResponse(w, r, err)

		return
	}
	if err := app.writeJSON(
		w,
		http.StatusCreated,
		envelope{
			"video_post": envelope{
				"id":            post.ID,
				actorIDKey:      post.ActorID,
				titleKey:        post.Title,
				descriptionKey:  post.Description,
				visibilityKey:   post.Visibility,
				mediaAssetIDKey: post.MediaAssetID,
				apObjectIDKey:   post.APObjectID,
				publishedAtKey:  post.PublishedAt,
			},
		},
		nil,
	); err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) createPicturePostHTMX(w http.ResponseWriter, r *http.Request) {
	if !app.requireSiteSectionEnabled(w, r, "pictures_enabled") {
		return
	}

	upload, err := parseMultipartUpload(w, r, maxPictureUploadBytes)
	if err != nil {
		app.clientError(w, http.StatusBadRequest)

		return
	}

	actor, caption, visibility, ok := app.validatePictureUploadRequest(w, r, upload)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), mediaCreateTimeout)
	defer cancel()

	post, err := app.createPicturePostFromUpload(ctx, actor, upload, caption, visibility)
	if err != nil {
		app.serverError(w, r, err)

		return
	}

	app.renderFragment(w, r, http.StatusCreated, "pictures.html", "pictureCard", post)
}

func (app *Application) createVideoPostHTMX(w http.ResponseWriter, r *http.Request) {
	app.createTitledMediaPostHTMX(w, r, "video")
}

func (app *Application) createTitledMediaPostHTMX(
	w http.ResponseWriter,
	r *http.Request,
	mediaKind string,
) {
	if mediaKind == "audio" {
		app.createMediaPostHTMX(
			w,
			r,
			"audio_enabled",
			maxAudioUploadBytes,
			app.validateAudioUploadRequest,
			func(
				ctx context.Context,
				actor *models.Actor,
				upload *multipartUpload,
				title string,
				description string,
				visibility string,
			) error {
				post, err := app.createAudioPostFromUpload(
					ctx,
					actor,
					upload,
					title,
					description,
					visibility,
				)
				if err != nil {
					return err
				}

				app.renderFragment(
					w,
					r,
					http.StatusCreated,
					"audio.html",
					"audioList",
					map[string]any{
						"Posts": []models.AudioPost{*post},
					},
				)

				return nil
			},
		)

		return
	}

	app.createMediaPostHTMX(
		w,
		r,
		"videos_enabled",
		maxVideoUploadBytes,
		app.validateVideoUploadRequest,
		func(
			ctx context.Context,
			actor *models.Actor,
			upload *multipartUpload,
			title string,
			description string,
			visibility string,
		) error {
			post, err := app.createVideoPostFromUpload(
				ctx,
				actor,
				upload,
				title,
				description,
				visibility,
			)
			if err != nil {
				return err
			}

			app.renderFragment(w, r, http.StatusCreated, "videos.html", "videoList", map[string]any{
				"Posts": []models.VideoPost{*post},
			})

			return nil
		},
	)
}

func (app *Application) createMediaPostHTMX(
	w http.ResponseWriter,
	r *http.Request,
	sectionEnabled string,
	maxUploadBytes int64,
	validateFn func(http.ResponseWriter, *http.Request, *multipartUpload) (*models.Actor, string, string, string, bool),
	createFn func(context.Context, *models.Actor, *multipartUpload, string, string, string) error,
) {
	if !app.requireSiteSectionEnabled(w, r, sectionEnabled) {
		return
	}

	upload, err := parseMultipartUpload(w, r, maxUploadBytes)
	if err != nil {
		app.clientError(w, http.StatusBadRequest)

		return
	}

	actor, title, description, visibility, ok := validateFn(w, r, upload)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), mediaCreateTimeout)
	defer cancel()

	if err := createFn(ctx, actor, upload, title, description, visibility); err != nil {
		app.serverError(w, r, err)
	}
}

func (app *Application) validateVideoUploadRequest(
	w http.ResponseWriter,
	r *http.Request,
	upload *multipartUpload,
) (*models.Actor, string, string, string, bool) {
	return app.validateAudioVideoUploadRequest(
		w,
		r,
		upload,
		maxVideoUploadBytes,
		maxVideoTitleLength,
		maxVideoDescriptionLength,
		"videos.html",
		"videoFormError",
		"must be less than 500MB",
	)
}

func (app *Application) validateAudioVideoUploadRequest(
	w http.ResponseWriter,
	r *http.Request,
	upload *multipartUpload,
	maxUploadBytes int64,
	maxTitleLength int,
	maxDescriptionLength int,
	templateName string,
	errorFragment string,
	fileTooLargeMessage string,
) (*models.Actor, string, string, string, bool) {
	token := bearerTokenFromRequest(r, upload.Values["token"])
	if token == "" {
		app.clientError(w, http.StatusUnauthorized)

		return nil, "", "", "", false
	}

	user, err := app.userFromBearerToken(r.Context(), token)
	if err != nil {
		app.clientError(w, http.StatusUnauthorized)

		return nil, "", "", "", false
	}

	ctx, cancel := context.WithTimeout(r.Context(), mediaCreateTimeout)
	defer cancel()

	actor, err := app.actors.GetByUserID(ctx, user.ID)
	if err != nil {
		if errors.Is(err, models.ErrRecordNotFound) {
			app.clientError(w, http.StatusUnauthorized)

			return nil, "", "", "", false
		}
		app.serverError(w, r, err)

		return nil, "", "", "", false
	}

	title := upload.Values["title"]
	description := upload.Values["description"]
	visibility := upload.Values["visibility"]
	if visibility == "" {
		visibility = models.VisibilityPublic
	}

	v := validator.NewValidator()
	v.CheckField(validator.NotBlank(title), "title", "must be provided")
	v.CheckField(
		validator.MaxChars(title, maxTitleLength),
		"title",
		"must not be more than 200 characters long",
	)
	v.CheckField(
		validator.MaxChars(description, maxDescriptionLength),
		"description",
		"must not be more than 2000 characters long",
	)
	v.CheckField(validator.PermittedValue(visibility,
		models.VisibilityPublic,
		models.VisibilityUnlisted,
		models.VisibilityFollowers,
		models.VisibilityPrivate,
	), "visibility", "must be one of public, unlisted, followers, private")
	if !upload.HasFile {
		v.AddFieldError("file", "must be provided")
	} else if upload.FileSize > maxUploadBytes {
		v.AddFieldError("file", fileTooLargeMessage)
	}

	if !v.Valid() {
		app.renderFragment(
			w,
			r,
			http.StatusUnprocessableEntity,
			templateName,
			errorFragment,
			map[string]any{errorsKey: v.FieldErrors},
		)

		return nil, "", "", "", false
	}

	return actor, title, description, visibility, true
}

func (app *Application) createVideoPostFromUpload(
	ctx context.Context,
	actor *models.Actor,
	upload *multipartUpload,
	title string,
	description string,
	visibility string,
) (*models.VideoPost, error) {
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
	if err := app.uploadMediaFile(ctx, "video", objectKey, mediaType, upload.FileData); err != nil {
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
		"video",
		objectKey,
		mediaType,
		upload.FileSize,
		upload.FileSHA256,
		originalFilename,
		isPublic,
	).Scan(&mediaAssetID); err != nil {
		return nil, err
	}

	post := &models.VideoPost{
		ActorID:      actor.ID,
		Title:        strings.TrimSpace(title),
		Description:  strings.TrimSpace(description),
		Visibility:   visibility,
		MediaAssetID: mediaAssetID,
		APObjectID: fmt.Sprintf(
			"%s/objects/video/%d-%d",
			strings.TrimSuffix(app.config.ActivityPubBaseURL, "/"),
			actor.ID,
			time.Now().UnixNano(),
		),
	}
	if err := app.db.QueryRowContext(
		ctx,
		`
		INSERT INTO video_posts (actor_id, title, description, visibility, media_asset_id, ap_object_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, published_at, updated_at, version`,
		post.ActorID,
		post.Title,
		post.Description,
		post.Visibility,
		post.MediaAssetID,
		post.APObjectID,
	).Scan(&post.ID, &post.PublishedAt, &post.UpdatedAt, &post.Version); err != nil {
		return nil, err
	}

	return post, nil
}

func (app *Application) loadPictureFeedPosts(
	ctx context.Context,
	feed string,
	user *models.User,
	limit, offset int,
) ([]models.PicturePost, error) {
	queries := sectionFeedQueries{
		Public: `
			SELECT pp.id, pp.actor_id, pp.caption, pp.visibility,
			EXISTS (
				SELECT 1
				FROM picture_post_assets ppa
				JOIN media_assets ma ON ma.id = ppa.media_asset_id
				WHERE ppa.picture_post_id = pp.id
			) AS has_preview,
			pp.ap_object_id, pp.published_at, pp.updated_at, pp.deleted_at, pp.version
			FROM picture_posts pp
			JOIN actors a ON a.id = pp.actor_id
			WHERE pp.deleted_at IS NULL AND pp.visibility = 'public'`,
		My: `
			SELECT pp.id, pp.actor_id, pp.caption, pp.visibility,
			EXISTS (
				SELECT 1
				FROM picture_post_assets ppa
				JOIN media_assets ma ON ma.id = ppa.media_asset_id
				WHERE ppa.picture_post_id = pp.id
			) AS has_preview,
			pp.ap_object_id, pp.published_at, pp.updated_at, pp.deleted_at, pp.version
			FROM picture_posts pp
			JOIN actors a ON a.id = pp.actor_id
			WHERE pp.deleted_at IS NULL
			  AND pp.actor_id IN (
				SELECT followed_actor_id FROM follows WHERE follower_actor_id = $1
				UNION
				SELECT $1
			  )
			  AND (pp.visibility = 'public' OR pp.actor_id = $1)`,
		LocalFilter:     "a.is_local = true",
		FederatedFilter: "a.is_local = false",
		OrderBy:         "pp.published_at DESC",
	}

	rows, err := app.querySectionFeedRows(ctx, feed, user, queries, limit, offset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanPicturePosts(rows)
}

func (app *Application) loadVideoFeedPosts(
	ctx context.Context,
	feed string,
	user *models.User,
	limit, offset int,
) ([]models.VideoPost, error) {
	queries := sectionFeedQueries{
		Public: `
			SELECT vp.id, vp.actor_id, vp.title, vp.description, vp.visibility, vp.media_asset_id, vp.ap_object_id, vp.published_at, vp.updated_at, vp.deleted_at, vp.version
			FROM video_posts vp
			JOIN actors a ON a.id = vp.actor_id
			WHERE vp.deleted_at IS NULL AND vp.visibility = 'public'`,
		My: `
			SELECT vp.id, vp.actor_id, vp.title, vp.description, vp.visibility, vp.media_asset_id, vp.ap_object_id, vp.published_at, vp.updated_at, vp.deleted_at, vp.version
			FROM video_posts vp
			JOIN actors a ON a.id = vp.actor_id
			WHERE vp.deleted_at IS NULL
			  AND vp.actor_id IN (
				SELECT followed_actor_id FROM follows WHERE follower_actor_id = $1
				UNION
				SELECT $1
			  )
			  AND (vp.visibility = 'public' OR vp.actor_id = $1)`,
		LocalFilter:     "a.is_local = true",
		FederatedFilter: "a.is_local = false",
		OrderBy:         "vp.published_at DESC",
	}

	rows, err := app.querySectionFeedRows(ctx, feed, user, queries, limit, offset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanVideoPosts(rows)
}

type sectionFeedQueries struct {
	Public          string
	My              string
	LocalFilter     string
	FederatedFilter string
	OrderBy         string
}

func (app *Application) querySectionFeedRows(
	ctx context.Context,
	feed string,
	user *models.User,
	queries sectionFeedQueries,
	limit, offset int,
) (*sql.Rows, error) {
	actorID, hasActorID, err := app.resolveFeedActorID(ctx, user)
	if err != nil {
		return nil, err
	}

	switch feed {
	case "my":
		if !hasActorID {
			query := fmt.Sprintf(
				"%s ORDER BY %s LIMIT $1 OFFSET $2",
				queries.Public,
				queries.OrderBy,
			)

			return app.db.QueryContext(ctx, query, limit, offset)
		}
		query := fmt.Sprintf("%s ORDER BY %s LIMIT $2 OFFSET $3", queries.My, queries.OrderBy)

		return app.db.QueryContext(ctx, query, actorID, limit, offset)
	case localFeedValue:
		query := fmt.Sprintf(
			"%s AND %s ORDER BY %s LIMIT $1 OFFSET $2",
			queries.Public,
			queries.LocalFilter,
			queries.OrderBy,
		)

		return app.db.QueryContext(ctx, query, limit, offset)
	case federatedFeedValue:
		query := fmt.Sprintf(
			"%s AND %s ORDER BY %s LIMIT $1 OFFSET $2",
			queries.Public,
			queries.FederatedFilter,
			queries.OrderBy,
		)

		return app.db.QueryContext(ctx, query, limit, offset)
	default:
		query := fmt.Sprintf("%s ORDER BY %s LIMIT $1 OFFSET $2", queries.Public, queries.OrderBy)

		return app.db.QueryContext(ctx, query, limit, offset)
	}
}

func (app *Application) resolveFeedActorID(
	ctx context.Context,
	user *models.User,
) (int64, bool, error) {
	if user == nil {
		return 0, false, nil
	}

	actor, err := app.actors.GetByUserID(ctx, user.ID)
	if err != nil {
		if errors.Is(err, models.ErrRecordNotFound) {
			return 0, false, nil
		}

		return 0, false, err
	}

	return actor.ID, true, nil
}

func (app *Application) validatePictureUploadRequest(
	w http.ResponseWriter,
	r *http.Request,
	upload *multipartUpload,
) (*models.Actor, string, string, bool) {
	token := bearerTokenFromRequest(r, upload.Values["token"])
	if token == "" {
		app.clientError(w, http.StatusUnauthorized)

		return nil, "", "", false
	}

	user, err := app.userFromBearerToken(r.Context(), token)
	if err != nil {
		app.clientError(w, http.StatusUnauthorized)

		return nil, "", "", false
	}

	ctx, cancel := context.WithTimeout(r.Context(), mediaCreateTimeout)
	defer cancel()

	actor, err := app.actors.GetByUserID(ctx, user.ID)
	if err != nil {
		if errors.Is(err, models.ErrRecordNotFound) {
			app.clientError(w, http.StatusUnauthorized)

			return nil, "", "", false
		}
		app.serverError(w, r, err)

		return nil, "", "", false
	}

	caption := upload.Values["caption"]
	visibility := upload.Values["visibility"]
	if visibility == "" {
		visibility = models.VisibilityPublic
	}

	v := validator.NewValidator()
	v.CheckField(validator.NotBlank(caption), "caption", "must be provided")
	v.CheckField(validator.PermittedValue(visibility,
		models.VisibilityPublic,
		models.VisibilityUnlisted,
		models.VisibilityFollowers,
		models.VisibilityPrivate,
	), "visibility", "must be one of public, unlisted, followers, private")
	if !upload.HasFile {
		v.AddFieldError("file", "must be provided")
	} else if upload.FileSize > maxPictureUploadBytes {
		v.AddFieldError("file", "must be less than 50MB")
	}

	if !v.Valid() {
		app.renderFragment(
			w,
			r,
			http.StatusUnprocessableEntity,
			"pictures.html",
			"pictureFormError",
			map[string]any{errorsKey: v.FieldErrors},
		)

		return nil, "", "", false
	}

	return actor, caption, visibility, true
}

func (app *Application) createPicturePostFromUpload(
	ctx context.Context,
	actor *models.Actor,
	upload *multipartUpload,
	caption string,
	visibility string,
) (*models.PicturePost, error) {
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

	if err := app.uploadMediaFile(
		ctx,
		"pictures",
		objectKey,
		mediaType,
		upload.FileData,
	); err != nil {
		return nil, err
	}

	var mediaAssetID int64
	err := app.db.QueryRowContext(
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
		"pictures",
		objectKey,
		mediaType,
		upload.FileSize,
		upload.FileSHA256,
		originalFilename,
		isPublic,
	).Scan(&mediaAssetID)
	if err != nil {
		return nil, err
	}

	baseURL := strings.TrimSuffix(app.config.ActivityPubBaseURL, "/")
	post := &models.PicturePost{
		ActorID:    actor.ID,
		Caption:    caption,
		Visibility: visibility,
		HasPreview: true,
		APObjectID: fmt.Sprintf(
			"%s/objects/picture/%d-%d",
			baseURL,
			actor.ID,
			time.Now().UnixNano(),
		),
	}
	err = app.db.QueryRowContext(
		ctx,
		`
		INSERT INTO picture_posts (actor_id, caption, visibility, ap_object_id)
		VALUES ($1, $2, $3, $4)
		RETURNING id, published_at, updated_at, version`,
		post.ActorID,
		post.Caption,
		post.Visibility,
		post.APObjectID,
	).Scan(&post.ID, &post.PublishedAt, &post.UpdatedAt, &post.Version)
	if err != nil {
		return nil, err
	}

	_, err = app.db.ExecContext(
		ctx,
		`INSERT INTO picture_post_assets (picture_post_id, media_asset_id, position) VALUES ($1, $2, $3)`,
		post.ID,
		mediaAssetID,
		0,
	)
	if err != nil {
		return nil, err
	}

	return post, nil
}

func scanPicturePosts(rows *sql.Rows) ([]models.PicturePost, error) {
	return scanRows(rows, func(rows *sql.Rows, p *models.PicturePost) error {
		return rows.Scan(
			&p.ID,
			&p.ActorID,
			&p.Caption,
			&p.Visibility,
			&p.HasPreview,
			&p.APObjectID,
			&p.PublishedAt,
			&p.UpdatedAt,
			&p.DeletedAt,
			&p.Version,
		)
	})
}

func scanVideoPosts(rows *sql.Rows) ([]models.VideoPost, error) {
	return scanRows(rows, func(rows *sql.Rows, p *models.VideoPost) error {
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

func (app *Application) loadBlogPostByID(
	ctx context.Context,
	idString string,
) (*models.BlogPost, error) {
	id, err := strconv.ParseInt(idString, 10, 64)
	if err != nil || id <= 0 {
		return nil, models.ErrRecordNotFound
	}
	row := app.db.QueryRowContext(ctx, `
		SELECT id, actor_id, title, slug, body_markdown, body_html, visibility, ap_object_id, published_at, updated_at, deleted_at, version
		FROM blog_posts WHERE id = $1 AND deleted_at IS NULL`, id)
	var post models.BlogPost
	if err := row.Scan(
		&post.ID,
		&post.ActorID,
		&post.Title,
		&post.Slug,
		&post.BodyMarkdown,
		&post.BodyHTML,
		&post.Visibility,
		&post.APObjectID,
		&post.PublishedAt,
		&post.UpdatedAt,
		&post.DeletedAt,
		&post.Version,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, models.ErrRecordNotFound
		}

		return nil, err
	}

	return &post, nil
}

func (app *Application) loadMicroPostByID(
	ctx context.Context,
	idString string,
) (*models.MicroPost, error) {
	return app.loadGenericByID(ctx, idString, `
		SELECT id, actor_id, content, visibility, reply_to_micro_post_id, ap_object_id, published_at, updated_at, deleted_at, version
		FROM micro_posts WHERE id = $1 AND deleted_at IS NULL`, func(row *sql.Row, post *models.MicroPost) error {
		return row.Scan(
			&post.ID,
			&post.ActorID,
			&post.Content,
			&post.Visibility,
			&post.ReplyToMicroPostID,
			&post.APObjectID,
			&post.PublishedAt,
			&post.UpdatedAt,
			&post.DeletedAt,
			&post.Version,
		)
	})
}

func (app *Application) loadGenericByID[T any](
	ctx context.Context,
	idString string,
	query string,
	scan func(*sql.Row, *T) error,
) (*T, error) {
	id, err := strconv.ParseInt(idString, 10, 64)
	if err != nil || id <= 0 {
		return nil, models.ErrRecordNotFound
	}

	row := app.db.QueryRowContext(ctx, query, id)
	var item T
	if err := scan(row, &item); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, models.ErrRecordNotFound
		}

		return nil, err
	}

	return &item, nil
}

func (app *Application) loadPicturePostByID(
	ctx context.Context,
	idString string,
) (*models.PicturePost, error) {
	return app.loadGenericByID(
		ctx,
		idString,
		`SELECT pp.id, pp.actor_id, pp.caption, pp.visibility,
		EXISTS (
			SELECT 1
			FROM picture_post_assets ppa
			JOIN media_assets ma ON ma.id = ppa.media_asset_id
			WHERE ppa.picture_post_id = pp.id
		) AS has_preview,
		pp.ap_object_id, pp.published_at, pp.updated_at, pp.deleted_at, pp.version
		FROM picture_posts pp
		WHERE pp.id = $1 AND pp.deleted_at IS NULL`,
		func(row *sql.Row, post *models.PicturePost) error {
			return row.Scan(
				&post.ID,
				&post.ActorID,
				&post.Caption,
				&post.Visibility,
				&post.HasPreview,
				&post.APObjectID,
				&post.PublishedAt,
				&post.UpdatedAt,
				&post.DeletedAt,
				&post.Version,
			)
		},
	)
}
