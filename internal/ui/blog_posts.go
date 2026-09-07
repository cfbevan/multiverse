package ui

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cfbevan/multiverse/internal/models"
	"github.com/cfbevan/multiverse/internal/validator"
)

var nonAlphaNumRX = regexp.MustCompile(`[^a-z0-9]+`)

const (
	maxBlogTitleLength = 200
	maxSlugLength      = 80
)

type createBlogInput struct {
	Title      string
	Body       string
	Visibility string
}

func (app *Application) createBlogPost(w http.ResponseWriter, r *http.Request) {
	if !app.requireSiteSectionEnabled(w, r, "blog_enabled") {
		return
	}

	user := app.contextGetUser(r)
	if user == nil {
		app.authenticationRequiredResponse(w, r)

		return
	}

	var input struct {
		Title      string `json:"title"`
		Body       string `json:"body"`
		Visibility string `json:"visibility"`
	}
	if err := app.readJSON(w, r, &input); err != nil {
		app.badRequestResponse(w, r, err)

		return
	}

	post, fieldErrors, err := app.createBlogPostForUser(r.Context(), user.ID, createBlogInput{
		Title:      input.Title,
		Body:       input.Body,
		Visibility: input.Visibility,
	})
	if len(fieldErrors) > 0 {
		app.failedValidationResponse(w, r, fieldErrors)

		return
	}
	if err != nil {
		if errors.Is(err, models.ErrRecordNotFound) {
			app.authenticationRequiredResponse(w, r)

			return
		}
		app.serverErrorResponse(w, r, err)

		return
	}

	err = app.writeJSON(w, http.StatusCreated, envelope{
		"blog_post": envelope{
			"id":           post.ID,
			actorIDKey:     post.ActorID,
			titleKey:       post.Title,
			"slug":         post.Slug,
			"body_html":    post.BodyHTML,
			visibilityKey:  post.Visibility,
			apObjectIDKey:  post.APObjectID,
			publishedAtKey: post.PublishedAt,
		},
	}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) blogPostListPartial(w http.ResponseWriter, r *http.Request) {
	app.renderSectionListPartial(
		w,
		r,
		"blog_enabled",
		"blog.html",
		"blogPostList",
		app.resolveMicroFeed,
		app.loadBlogFeedPosts,
	)
}

func (app *Application) createBlogPostHTMX(w http.ResponseWriter, r *http.Request) {
	if !app.requireSiteSectionEnabled(w, r, "blog_enabled") {
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

	post, fieldErrors, err := app.createBlogPostForUser(r.Context(), user.ID, createBlogInput{
		Title:      r.FormValue("title"),
		Body:       r.FormValue("body"),
		Visibility: r.FormValue("visibility"),
	})
	if err != nil {
		app.serverError(w, r, err)

		return
	}
	if len(fieldErrors) > 0 {
		app.renderFragment(
			w,
			r,
			http.StatusUnprocessableEntity,
			"blog.html",
			"blogFormError",
			map[string]any{errorsKey: fieldErrors},
		)

		return
	}

	app.renderFragment(w, r, http.StatusCreated, "blog.html", "blogPostCard", post)
}

func (app *Application) createBlogPostForUser(
	ctx context.Context,
	userID int64,
	input createBlogInput,
) (*models.BlogPost, map[string]string, error) {
	if strings.TrimSpace(input.Visibility) == "" {
		input.Visibility = models.VisibilityPublic
	}

	v := validator.NewValidator()
	v.CheckField(validator.NotBlank(input.Title), "title", "must be provided")
	v.CheckField(
		validator.MaxChars(input.Title, maxBlogTitleLength),
		"title",
		"must not be more than 200 characters long",
	)
	v.CheckField(validator.NotBlank(input.Body), "body", "must be provided")
	v.CheckField(validator.PermittedValue(input.Visibility,
		models.VisibilityPublic,
		models.VisibilityUnlisted,
		models.VisibilityFollowers,
		models.VisibilityPrivate,
	), "visibility", "must be one of public, unlisted, followers, private")
	if !v.Valid() {
		return nil, v.FieldErrors, nil
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, defaultRequestTimeout)
	defer cancel()

	actor, err := app.actors.GetByUserID(timeoutCtx, userID)
	if err != nil {
		if errors.Is(err, models.ErrRecordNotFound) {
			return nil, nil, models.ErrRecordNotFound
		}

		return nil, nil, err
	}

	baseURL := strings.TrimSuffix(app.config.ActivityPubBaseURL, "/")
	title := strings.TrimSpace(input.Title)
	body := strings.TrimSpace(input.Body)
	slug := slugifyTitle(title)
	objectID := fmt.Sprintf("%s/objects/blog/%d-%d", baseURL, actor.ID, time.Now().UnixNano())
	post := &models.BlogPost{
		ActorID:      actor.ID,
		Title:        title,
		Slug:         slug,
		BodyMarkdown: body,
		BodyHTML:     "<p>" + htmlEscape(body) + "</p>",
		Visibility:   input.Visibility,
		APObjectID:   objectID,
	}

	if err := app.blogPosts.Insert(timeoutCtx, post); err != nil {
		return nil, nil, err
	}

	activityID := fmt.Sprintf("%s/activities/create/blog/%d", baseURL, post.ID)
	activityObject := envelope{
		"id":            post.APObjectID,
		typeKey:         "Article",
		nameKey:         post.Title,
		contentKey:      post.BodyHTML,
		publishedKey:    post.PublishedAt.UTC().Format(time.RFC3339),
		attributedToKey: actorURL(baseURL, actor.Handle),
	}
	if err := app.enqueueCreateForFollowers(
		timeoutCtx,
		actor,
		activityObject,
		activityID,
	); err != nil {
		app.logger.Error(
			"failed to enqueue blog federation",
			"error",
			err.Error(),
			"post_id",
			post.ID,
		)
	}

	return post, nil, nil
}

func sectionListQuery(r *http.Request) (int, int) {
	limit := 20
	offset := 0
	qs := r.URL.Query()
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

	return limit, offset
}

func (app *Application) listBlogPosts(w http.ResponseWriter, r *http.Request) {
	if !app.requireSiteSectionEnabled(w, r, "blog_enabled") {
		return
	}

	handle := strings.TrimSpace(r.URL.Query().Get("handle"))
	feed, user, err := app.resolveMicroFeed(r)
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
		app.loadBlogFeedPosts,
		app.blogPosts.ListByActor,
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
			"id":           p.ID,
			actorIDKey:     p.ActorID,
			titleKey:       p.Title,
			"slug":         p.Slug,
			"body_html":    p.BodyHTML,
			visibilityKey:  p.Visibility,
			apObjectIDKey:  p.APObjectID,
			publishedAtKey: p.PublishedAt,
		})
	}

	if err = app.writeJSON(w, http.StatusOK, envelope{
		"blog_posts": items,
		countKey:     len(items),
		feedKey:      feed,
	}, nil); err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) loadBlogFeedPosts(
	ctx context.Context,
	feed string,
	user *models.User,
	limit, offset int,
) ([]models.BlogPost, error) {
	return app.loadFeedPostsBySection(
		ctx,
		feed,
		user,
		limit,
		offset,
		app.blogPosts.ListPublic,
		blogLocalQuery(),
		blogFederatedQuery(),
		blogFollowingQuery(),
		scanBlogPosts,
	)
}

func blogLocalQuery() string {
	return `SELECT bp.id, bp.actor_id, bp.title, bp.slug, bp.body_markdown, bp.body_html, bp.visibility, bp.ap_object_id, bp.published_at, bp.updated_at, bp.deleted_at, bp.version FROM blog_posts bp JOIN actors a ON a.id = bp.actor_id WHERE bp.deleted_at IS NULL AND bp.visibility = 'public' AND a.is_local = true ORDER BY bp.published_at DESC LIMIT $1 OFFSET $2`
}

func blogFederatedQuery() string {
	return `SELECT bp.id, bp.actor_id, bp.title, bp.slug, bp.body_markdown, bp.body_html, bp.visibility, bp.ap_object_id, bp.published_at, bp.updated_at, bp.deleted_at, bp.version FROM blog_posts bp JOIN actors a ON a.id = bp.actor_id WHERE bp.deleted_at IS NULL AND bp.visibility = 'public' AND a.is_local = false ORDER BY bp.published_at DESC LIMIT $1 OFFSET $2`
}

func blogFollowingQuery() string {
	return `SELECT bp.id, bp.actor_id, bp.title, bp.slug, bp.body_markdown, bp.body_html, bp.visibility, bp.ap_object_id, bp.published_at, bp.updated_at, bp.deleted_at, bp.version FROM blog_posts bp JOIN actors a ON a.id = bp.actor_id WHERE bp.deleted_at IS NULL AND bp.actor_id IN (SELECT followed_actor_id FROM follows WHERE follower_actor_id = $1 UNION SELECT $1) AND (bp.visibility = 'public' OR bp.actor_id = $1) ORDER BY bp.published_at DESC LIMIT $2 OFFSET $3`
}

func (app *Application) loadFeedPostsBySection[T any](
	ctx context.Context,
	feed string,
	user *models.User,
	limit, offset int,
	listPublic func(context.Context, int, int) ([]T, error),
	localQuery, federatedQuery, myQuery string,
	scan func(*sql.Rows) ([]T, error),
) ([]T, error) {
	var actorID *int64
	if user != nil {
		actor, err := app.actors.GetByUserID(ctx, user.ID)
		if err != nil {
			if errors.Is(err, models.ErrRecordNotFound) {
				return nil, nil
			}

			return nil, err
		}
		actorID = &actor.ID
	}

	switch feed {
	case "my":
		if actorID == nil {
			return listPublic(ctx, limit, offset)
		}
		rows, err := app.db.QueryContext(ctx, myQuery, *actorID, limit, offset)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()

		return scan(rows)
	case localFeedValue:
		rows, err := app.db.QueryContext(ctx, localQuery, limit, offset)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()

		return scan(rows)
	case federatedFeedValue:
		rows, err := app.db.QueryContext(ctx, federatedQuery, limit, offset)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()

		return scan(rows)
	default:
		return listPublic(ctx, limit, offset)
	}
}

func scanBlogPosts(rows *sql.Rows) ([]models.BlogPost, error) {
	return scanRows(rows, func(rows *sql.Rows, p *models.BlogPost) error {
		return rows.Scan(
			&p.ID,
			&p.ActorID,
			&p.Title,
			&p.Slug,
			&p.BodyMarkdown,
			&p.BodyHTML,
			&p.Visibility,
			&p.APObjectID,
			&p.PublishedAt,
			&p.UpdatedAt,
			&p.DeletedAt,
			&p.Version,
		)
	})
}

func scanRows[T any](rows *sql.Rows, scan func(*sql.Rows, *T) error) ([]T, error) {
	items := make([]T, 0)
	for rows.Next() {
		var item T
		if err := scan(rows, &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return items, nil
}

func slugifyTitle(title string) string {
	title = strings.ToLower(strings.TrimSpace(title))
	slug := nonAlphaNumRX.ReplaceAllString(title, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		return fmt.Sprintf("post-%d", time.Now().Unix())
	}
	if len(slug) > maxSlugLength {
		slug = strings.Trim(slug[:maxSlugLength], "-")
	}

	return slug
}

func htmlEscape(s string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&#39;",
	)

	return replacer.Replace(s)
}
