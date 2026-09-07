package ui

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cfbevan/multiverse/internal/models"
	"github.com/cfbevan/multiverse/internal/validator"
)

const (
	maxMicroPostChars = 500
	trendingFeedValue = "trending"
)

func (app *Application) createMicroPost(w http.ResponseWriter, r *http.Request) {
	if !app.requireSiteSectionEnabled(w, r, "micro_blog_enabled") {
		return
	}

	user := app.contextGetUser(r)
	if user == nil {
		app.authenticationRequiredResponse(w, r)

		return
	}

	var input struct {
		Content            string `json:"content"`
		Visibility         string `json:"visibility"`
		ReplyToMicroPostID *int64 `json:"reply_to_micro_post_id"`
	}

	if err := app.readJSON(w, r, &input); err != nil {
		app.badRequestResponse(w, r, err)

		return
	}

	if strings.TrimSpace(input.Visibility) == "" {
		input.Visibility = models.VisibilityPublic
	}

	v := validator.NewValidator()
	v.CheckField(validator.NotBlank(input.Content), "content", "must be provided")
	v.CheckField(
		validator.MaxChars(input.Content, maxMicroPostChars),
		"content",
		"must not be more than 500 characters long",
	)
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
	objectID := fmt.Sprintf("%s/objects/micro/%d-%d", baseURL, actor.ID, time.Now().UnixNano())
	post := &models.MicroPost{
		ActorID:            actor.ID,
		Content:            strings.TrimSpace(input.Content),
		Visibility:         input.Visibility,
		ReplyToMicroPostID: input.ReplyToMicroPostID,
		APObjectID:         objectID,
	}

	if err := app.microPosts.Insert(ctx, post); err != nil {
		app.serverErrorResponse(w, r, err)

		return
	}

	activityID := fmt.Sprintf("%s/activities/create/micro/%d", baseURL, post.ID)
	activityObject := envelope{
		"id":            post.APObjectID,
		typeKey:         noteObjectType,
		contentKey:      post.Content,
		publishedKey:    post.PublishedAt.UTC().Format(time.RFC3339),
		attributedToKey: actorURL(baseURL, actor.Handle),
	}
	if err := app.enqueueCreateForFollowers(ctx, actor, activityObject, activityID); err != nil {
		app.logger.Error(
			"failed to enqueue micro-post federation",
			"error",
			err.Error(),
			"post_id",
			post.ID,
		)
	}

	err = app.writeJSON(w, http.StatusCreated, envelope{
		"micro_post": envelope{
			"id":                     post.ID,
			actorIDKey:               post.ActorID,
			contentKey:               post.Content,
			visibilityKey:            post.Visibility,
			"reply_to_micro_post_id": post.ReplyToMicroPostID,
			apObjectIDKey:            post.APObjectID,
			publishedAtKey:           post.PublishedAt,
		},
	}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) createMicroPostHTMX(w http.ResponseWriter, r *http.Request) {
	if !app.requireSiteSectionEnabled(w, r, "micro_blog_enabled") {
		return
	}

	if err := r.ParseForm(); err != nil {
		app.clientError(w, http.StatusBadRequest)

		return
	}

	token := strings.TrimSpace(r.FormValue("token"))
	if token == "" {
		authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
		if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
			token = strings.TrimSpace(authHeader[7:])
		}
	}
	if token == "" {
		app.clientError(w, http.StatusUnauthorized)

		return
	}

	user, err := app.userFromBearerToken(r.Context(), token)
	if err != nil {
		app.clientError(w, http.StatusUnauthorized)

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), defaultRequestTimeout)
	defer cancel()

	actor, err := app.actors.GetByUserID(ctx, user.ID)
	if err != nil {
		if errors.Is(err, models.ErrRecordNotFound) {
			app.clientError(w, http.StatusUnauthorized)

			return
		}
		app.serverError(w, r, err)

		return
	}

	visibility := strings.TrimSpace(r.FormValue("visibility"))
	if visibility == "" {
		visibility = models.VisibilityPublic
	}

	content := strings.TrimSpace(r.FormValue("content"))

	v := validator.NewValidator()
	v.CheckField(validator.NotBlank(content), "content", "must be provided")
	v.CheckField(
		validator.MaxChars(content, maxMicroPostChars),
		"content",
		"must not be more than 500 characters long",
	)
	v.CheckField(validator.PermittedValue(visibility,
		models.VisibilityPublic,
		models.VisibilityUnlisted,
		models.VisibilityFollowers,
		models.VisibilityPrivate,
	), "visibility", "must be one of public, unlisted, followers, private")
	if !v.Valid() {
		app.renderFragment(
			w,
			r,
			http.StatusUnprocessableEntity,
			"micro-blog.html",
			"microBlogFormError",
			map[string]any{errorsKey: v.FieldErrors},
		)

		return
	}

	baseURL := strings.TrimSuffix(app.config.ActivityPubBaseURL, "/")
	objectID := fmt.Sprintf("%s/objects/micro/%d-%d", baseURL, actor.ID, time.Now().UnixNano())
	post := &models.MicroPost{
		ActorID:    actor.ID,
		Content:    content,
		Visibility: visibility,
		APObjectID: objectID,
	}

	if err := app.microPosts.Insert(ctx, post); err != nil {
		app.serverError(w, r, err)

		return
	}

	activityID := fmt.Sprintf("%s/activities/create/micro/%d", baseURL, post.ID)
	activityObject := envelope{
		"id":            post.APObjectID,
		typeKey:         noteObjectType,
		contentKey:      post.Content,
		publishedKey:    post.PublishedAt.UTC().Format(time.RFC3339),
		attributedToKey: actorURL(baseURL, actor.Handle),
	}
	if err := app.enqueueCreateForFollowers(ctx, actor, activityObject, activityID); err != nil {
		app.logger.Error(
			"failed to enqueue micro-post federation",
			"error",
			err.Error(),
			"post_id",
			post.ID,
		)
	}

	app.renderFragment(w, r, http.StatusCreated, "micro-blog.html", "microBlogCard", post)
}

func (app *Application) listMicroPosts(w http.ResponseWriter, r *http.Request) {
	if !app.requireSiteSectionEnabled(w, r, "micro_blog_enabled") {
		return
	}

	feed, user, err := app.resolveMicroFeed(r)
	if err != nil {
		app.serverErrorResponse(w, r, err)

		return
	}

	limit, offset := parseMicroListPagination(r.URL.Query())
	ctx, cancel := context.WithTimeout(r.Context(), defaultRequestTimeout)
	defer cancel()

	posts, err := app.loadMicroPostsForList(
		ctx,
		feed,
		user,
		strings.TrimSpace(r.URL.Query().Get("handle")),
		limit,
		offset,
	)
	if err != nil {
		if errors.Is(err, models.ErrRecordNotFound) {
			app.notFoundResponse(w, r)

			return
		}
		app.serverErrorResponse(w, r, err)

		return
	}

	app.writeMicroPostsResponse(w, r, feed, posts)
}

func parseMicroListPagination(values url.Values) (int, int) {
	limit := 20
	offset := 0
	if s := strings.TrimSpace(values.Get("limit")); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	if s := strings.TrimSpace(values.Get("offset")); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			offset = n
		}
	}

	return limit, offset
}

func (app *Application) loadMicroPostsForList(
	ctx context.Context,
	feed string,
	user *models.User,
	handle string,
	limit, offset int,
) ([]models.MicroPost, error) {
	if handle != "" {
		return app.loadMicroPostsForHandle(ctx, handle, limit, offset)
	}

	return app.loadMicroFeedPosts(ctx, feed, user, limit, offset)
}

func (app *Application) loadMicroPostsForHandle(
	ctx context.Context,
	handle string,
	limit, offset int,
) ([]models.MicroPost, error) {
	userRecord, err := app.users.GetByHandle(ctx, handle)
	if err != nil {
		return nil, err
	}

	actor, err := app.actors.GetByUserID(ctx, userRecord.ID)
	if err != nil {
		return nil, err
	}

	return app.microPosts.ListByActor(ctx, actor.ID, limit, offset)
}

func (app *Application) writeMicroPostsResponse(
	w http.ResponseWriter,
	r *http.Request,
	feed string,
	posts []models.MicroPost,
) {
	items := make([]envelope, 0, len(posts))
	for _, p := range posts {
		items = append(items, envelope{
			"id":                     p.ID,
			actorIDKey:               p.ActorID,
			contentKey:               p.Content,
			visibilityKey:            p.Visibility,
			"reply_to_micro_post_id": p.ReplyToMicroPostID,
			apObjectIDKey:            p.APObjectID,
			publishedAtKey:           p.PublishedAt,
		})
	}

	if err := app.writeJSON(w, http.StatusOK, envelope{
		"micro_posts": items,
		countKey:      len(items),
		feedKey:       feed,
	}, nil); err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) microBlogListPartial(w http.ResponseWriter, r *http.Request) {
	app.renderSectionListPartial(
		w,
		r,
		"micro_blog_enabled",
		"micro-blog.html",
		"microBlogList",
		app.resolveMicroFeed,
		app.loadMicroFeedPosts,
	)
}

func (app *Application) resolveSectionFeed(
	r *http.Request,
	_ string,
) (string, *models.User, error) {
	feed := strings.TrimSpace(r.URL.Query().Get("feed"))
	user := app.contextGetUser(r)

	switch feed {
	case "", "my", localFeedValue, federatedFeedValue, trendingFeedValue:
		if feed == "" {
			return localFeedValue, user, nil
		}

		return feed, user, nil
	default:
		return localFeedValue, user, nil
	}
}

func (app *Application) resolveMicroFeed(r *http.Request) (string, *models.User, error) {
	return app.resolveSectionFeed(r, localFeedValue)
}

type sectionListLoader[T any] func(context.Context, string, *models.User, int, int) ([]T, error)
type sectionActorListLoader[T any] func(context.Context, int64, int, int) ([]T, error)

func (app *Application) renderSectionListPartial[T any](
	w http.ResponseWriter,
	r *http.Request,
	feature string,
	templateName, fragmentName string,
	resolver func(*http.Request) (string, *models.User, error),
	loader sectionListLoader[T],
) {
	if !app.requireSiteSectionEnabled(w, r, feature) {
		return
	}

	feed, user, err := resolver(r)
	if err != nil {
		app.serverErrorResponse(w, r, err)

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), defaultRequestTimeout)
	defer cancel()

	posts, err := loader(ctx, feed, user, actorOutboxPageSize, 0)
	if err != nil {
		app.serverErrorResponse(w, r, err)

		return
	}

	app.renderFragment(w, r, http.StatusOK, templateName, fragmentName, map[string]any{
		feedLabelKey: feed,
		postsKey:     posts,
	})
}

func (app *Application) loadSectionPostsByHandle[T any](
	ctx context.Context,
	feed string,
	user *models.User,
	handle string,
	limit, offset int,
	feedLoader sectionListLoader[T],
	actorLoader sectionActorListLoader[T],
) ([]T, error) {
	if handle == "" {
		return feedLoader(ctx, feed, user, limit, offset)
	}

	userRecord, err := app.users.GetByHandle(ctx, handle)
	if err != nil {
		if errors.Is(err, models.ErrRecordNotFound) {
			return nil, models.ErrRecordNotFound
		}

		return nil, err
	}

	actor, err := app.actors.GetByUserID(ctx, userRecord.ID)
	if err != nil {
		if errors.Is(err, models.ErrRecordNotFound) {
			return nil, models.ErrRecordNotFound
		}

		return nil, err
	}

	return actorLoader(ctx, actor.ID, limit, offset)
}

func (app *Application) loadMicroFeedPosts(
	ctx context.Context,
	feed string,
	user *models.User,
	limit, offset int,
) ([]models.MicroPost, error) {
	if user == nil {
		switch feed {
		case "my":
			return app.microPosts.ListPublic(ctx, limit, offset)
		case localFeedValue:
			return app.queryMicroFeedPosts(
				ctx,
				baseMicroFeedQuery()+` AND a.is_local = true ORDER BY mp.published_at DESC LIMIT $1 OFFSET $2`,
				limit,
				offset,
			)
		case federatedFeedValue:
			return app.queryMicroFeedPosts(
				ctx,
				baseMicroFeedQuery()+` AND a.is_local = false ORDER BY mp.published_at DESC LIMIT $1 OFFSET $2`,
				limit,
				offset,
			)
		case trendingFeedValue:
			return app.queryMicroFeedPosts(ctx, trendingMicroFeedQuery(), limit, offset)
		default:
			return app.microPosts.ListPublic(ctx, limit, offset)
		}
	}

	actorID, err := app.currentActorID(ctx, user)
	if err != nil {
		if errors.Is(err, models.ErrRecordNotFound) {
			return app.microPosts.ListPublic(ctx, limit, offset)
		}

		return nil, err
	}

	switch feed {
	case "my":
		return app.loadMyMicroFeedPosts(ctx, actorID, limit, offset)
	case localFeedValue:
		return app.queryMicroFeedPosts(
			ctx,
			baseMicroFeedQuery()+` AND a.is_local = true ORDER BY mp.published_at DESC LIMIT $1 OFFSET $2`,
			limit,
			offset,
		)
	case federatedFeedValue:
		return app.queryMicroFeedPosts(
			ctx,
			baseMicroFeedQuery()+` AND a.is_local = false ORDER BY mp.published_at DESC LIMIT $1 OFFSET $2`,
			limit,
			offset,
		)
	case trendingFeedValue:
		return app.queryMicroFeedPosts(ctx, trendingMicroFeedQuery(), limit, offset)
	default:
		return app.microPosts.ListPublic(ctx, limit, offset)
	}
}

func (app *Application) currentActorID(ctx context.Context, user *models.User) (*int64, error) {
	if user == nil {
		return nil, models.ErrRecordNotFound
	}

	actor, err := app.actors.GetByUserID(ctx, user.ID)
	if err != nil {
		if errors.Is(err, models.ErrRecordNotFound) {
			return nil, models.ErrRecordNotFound
		}

		return nil, err
	}

	return &actor.ID, nil
}

func baseMicroFeedQuery() string {
	return `
		SELECT mp.id, mp.actor_id, mp.content, mp.visibility, mp.reply_to_micro_post_id, mp.ap_object_id, mp.published_at, mp.updated_at, mp.deleted_at, mp.version
		FROM micro_posts mp
		JOIN actors a ON a.id = mp.actor_id
		WHERE mp.deleted_at IS NULL AND mp.visibility = 'public'`
}

func trendingMicroFeedQuery() string {
	return `
		SELECT mp.id, mp.actor_id, mp.content, mp.visibility, mp.reply_to_micro_post_id, mp.ap_object_id, mp.published_at, mp.updated_at, mp.deleted_at, mp.version
		FROM micro_posts mp
		JOIN actors a ON a.id = mp.actor_id
		WHERE mp.deleted_at IS NULL AND mp.visibility = 'public'
		ORDER BY (
			SELECT COUNT(*)
			FROM micro_posts replies
			WHERE replies.reply_to_micro_post_id = mp.id
		) DESC, mp.published_at DESC
		LIMIT $1 OFFSET $2`
}

func (app *Application) loadMyMicroFeedPosts(
	ctx context.Context,
	actorID *int64,
	limit, offset int,
) ([]models.MicroPost, error) {
	if actorID == nil {
		return app.microPosts.ListPublic(ctx, limit, offset)
	}

	query := `
		SELECT mp.id, mp.actor_id, mp.content, mp.visibility, mp.reply_to_micro_post_id, mp.ap_object_id, mp.published_at, mp.updated_at, mp.deleted_at, mp.version
		FROM micro_posts mp
		JOIN actors a ON a.id = mp.actor_id
		WHERE mp.deleted_at IS NULL
		  AND mp.actor_id IN (
			SELECT followed_actor_id FROM follows WHERE follower_actor_id = $1
			UNION
			SELECT $1
		  )
		  AND (mp.visibility = 'public' OR mp.actor_id = $1)
		ORDER BY mp.published_at DESC
		LIMIT $2 OFFSET $3`

	return app.queryMicroFeedPosts(ctx, query, *actorID, limit, offset)
}

func (app *Application) queryMicroFeedPosts(
	ctx context.Context,
	query string,
	args ...any,
) ([]models.MicroPost, error) {
	rows, err := app.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanMicroPosts(rows)
}

func scanMicroPosts(rows *sql.Rows) ([]models.MicroPost, error) {
	posts := make([]models.MicroPost, 0)
	for rows.Next() {
		var p models.MicroPost
		if err := rows.Scan(
			&p.ID,
			&p.ActorID,
			&p.Content,
			&p.Visibility,
			&p.ReplyToMicroPostID,
			&p.APObjectID,
			&p.PublishedAt,
			&p.UpdatedAt,
			&p.DeletedAt,
			&p.Version,
		); err != nil {
			return nil, err
		}
		posts = append(posts, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return posts, nil
}
