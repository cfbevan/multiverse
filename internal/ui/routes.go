package ui

import (
	"context"
	"net/http"
	"time"

	"github.com/cfbevan/multiverse/internal/models"
	"github.com/cfbevan/multiverse/templates"
)

// Routes returns the application's HTTP handler tree.
func (app *Application) Routes() http.Handler {
	router := http.NewServeMux()

	router.Handle("GET /static/", http.FileServerFS(templates.Files))

	router.HandleFunc(http.MethodGet+" /{$}", app.home)
	router.HandleFunc(http.MethodGet+" /healthz", app.healthcheck)

	router.HandleFunc(http.MethodGet+" /blog", app.blog)
	router.HandleFunc(http.MethodGet+" /blog/{id}", app.blogItemPage)
	router.HandleFunc(http.MethodGet+" /blog/partials/list", app.blogPostListPartial)
	router.HandleFunc(http.MethodPost+" /blog/partials/create", app.createBlogPostHTMX)
	router.HandleFunc(http.MethodGet+" /micro-blog", app.microBlog)
	router.HandleFunc(http.MethodGet+" /micro-blog/partials/list", app.microBlogListPartial)
	router.HandleFunc(http.MethodPost+" /micro-blog/partials/create", app.createMicroPostHTMX)
	router.HandleFunc(http.MethodGet+" /micro-blog/{id}", app.microBlogItemPage)
	router.HandleFunc(http.MethodGet+" /user/profile", app.userProfilePage)
	router.HandleFunc(http.MethodGet+" /user/settings", app.userSettingsPage)
	router.HandleFunc(http.MethodGet+" /admin/settings", app.adminSettingsPage)
	router.HandleFunc(http.MethodGet+" /user/password-reset", app.passwordResetPage)
	router.HandleFunc(http.MethodGet+" /user/login", app.loginPage)
	router.HandleFunc(http.MethodGet+" /user/signup", app.signupPage)
	router.HandleFunc(http.MethodGet+" /audio", app.audio)
	router.HandleFunc(http.MethodGet+" /audio/partials/list", app.audioListPartial)
	router.HandleFunc(http.MethodPost+" /audio/partials/create", app.createAudioPostHTMX)
	router.HandleFunc(http.MethodGet+" /audio/{id}/preview", app.audioPreview)
	router.HandleFunc(http.MethodGet+" /audio/{id}", app.audioItemPage)
	router.HandleFunc(http.MethodGet+" /pictures", app.pictures)
	router.HandleFunc(http.MethodGet+" /pictures/partials/list", app.pictureListPartial)
	router.HandleFunc(http.MethodPost+" /pictures/partials/create", app.createPicturePostHTMX)
	router.HandleFunc(http.MethodGet+" /pictures/{id}/preview", app.picturePreview)
	router.HandleFunc(http.MethodGet+" /pictures/{id}", app.pictureItemPage)
	router.HandleFunc(http.MethodGet+" /videos", app.videos)
	router.HandleFunc(http.MethodGet+" /videos/partials/list", app.videoListPartial)
	router.HandleFunc(http.MethodPost+" /videos/partials/create", app.createVideoPostHTMX)
	router.HandleFunc(http.MethodGet+" /videos/{id}/preview", app.videoPreview)
	router.HandleFunc(http.MethodGet+" /videos/{id}", app.videoItemPage)
	router.HandleFunc(http.MethodPost+" /v1/accounts/signup", app.registerAccount)
	router.HandleFunc(http.MethodPost+" /v1/accounts/login", app.loginAccount)
	router.HandleFunc(http.MethodGet+" /v1/auth/oidc/login", app.oidcLogin)
	router.HandleFunc(http.MethodGet+" /v1/auth/oidc/callback", app.oidcCallback)
	router.HandleFunc(http.MethodPost+" /v1/auth/oidc/callback", app.oidcCallback)
	router.Handle(
		http.MethodGet+" /v1/accounts/me",
		app.requireAuthenticated(http.HandlerFunc(app.currentAccount)),
	)
	router.Handle(
		http.MethodPost+" /v1/accounts/me/settings/theme",
		app.requireAuthenticated(http.HandlerFunc(app.updateThemeSettings)),
	)
	router.Handle(
		http.MethodPost+" /v1/accounts/me/password-reset-email",
		app.requireAuthenticated(http.HandlerFunc(app.requestPasswordResetEmail)),
	)
	router.HandleFunc(http.MethodPost+" /v1/accounts/password-reset", app.resetPassword)
	router.Handle(
		http.MethodGet+" /v1/site-configs",
		app.requireAdmin(http.HandlerFunc(app.listSiteConfigs)),
	)
	router.Handle(
		http.MethodPost+" /v1/site-configs/{key}",
		app.requireAdmin(http.HandlerFunc(app.updateSiteConfig)),
	)

	router.HandleFunc(http.MethodGet+" /v1/micro-posts", app.listMicroPosts)
	router.Handle(
		http.MethodPost+" /v1/micro-posts",
		app.requireAuthenticated(http.HandlerFunc(app.createMicroPost)),
	)
	router.HandleFunc(http.MethodGet+" /v1/comments", app.listComments)
	router.Handle(
		http.MethodPost+" /v1/comments",
		app.requireAuthenticated(http.HandlerFunc(app.createComment)),
	)
	router.HandleFunc(http.MethodGet+" /v1/blog-posts", app.listBlogPosts)
	router.Handle(
		http.MethodPost+" /v1/blog-posts",
		app.requireAuthenticated(http.HandlerFunc(app.createBlogPost)),
	)
	router.HandleFunc(http.MethodGet+" /v1/audio-posts", app.listAudioPosts)
	router.Handle(
		http.MethodPost+" /v1/audio-posts",
		app.requireAuthenticated(http.HandlerFunc(app.createAudioPost)),
	)
	router.Handle(
		http.MethodPost+" /v1/picture-posts",
		app.requireAuthenticated(http.HandlerFunc(app.createPicturePost)),
	)
	router.Handle(
		http.MethodPost+" /v1/video-posts",
		app.requireAuthenticated(http.HandlerFunc(app.createVideoPost)),
	)

	router.HandleFunc(http.MethodGet+" /.well-known/webfinger", app.webfinger)
	router.HandleFunc(http.MethodGet+" /actors/{name}", app.actor)
	router.HandleFunc(http.MethodGet+" /actors/{name}/outbox", app.actorOutbox)
	router.HandleFunc(http.MethodGet+" /actors/{name}/followers", app.actorFollowers)
	router.HandleFunc(http.MethodGet+" /actors/{name}/following", app.actorFollowing)
	router.HandleFunc(http.MethodPost+" /inbox", app.sharedInbox)

	return app.recoverPanic(app.authenticate(router))
}

func (app *Application) home(w http.ResponseWriter, r *http.Request) {
	app.render(w, r, "home.html", map[string]any{
		"ReleaseNotes": []map[string]string{
			{
				"Date":    "2026-09-07",
				"Version": "v0.1.0",
				"Summary": "Initial MVP release of Multiverse, a federated social media platform built with Go and PostgreSQL.",
			},
		},
	}, http.StatusOK)
}

const healthcheckTimeout = 2 * time.Second

func (app *Application) healthcheck(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), healthcheckTimeout)
	defer cancel()

	status := "available"
	dbStatus := "available"
	if err := app.db.PingContext(ctx); err != nil {
		status = "degraded"
		dbStatus = err.Error()
	}

	err := app.writeJSON(w, http.StatusOK, envelope{
		statusKey: status,
		"system_info": envelope{
			"environment": app.config.Env,
			"version":     "dev",
		},
		"database": envelope{
			statusKey: dbStatus,
		},
	}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) blog(w http.ResponseWriter, r *http.Request) {
	if !app.requireSiteSectionEnabled(w, r, "blog_enabled") {
		return
	}

	app.render(w, r, "blog.html", map[string]any{
		feedLabelKey:       localFeedValue,
		isAuthenticatedKey: app.contextGetUser(r) != nil,
	}, http.StatusOK)
}

func (app *Application) loginPage(w http.ResponseWriter, r *http.Request) {
	oidcEnabled, err := app.siteConfigEnabled(r.Context(), "oidc_enabled", true)
	if err != nil {
		oidcEnabled = true
	}
	oidcLoginEnabled, err := app.siteConfigEnabled(r.Context(), "oidc_login_enabled", true)
	if err != nil {
		oidcLoginEnabled = true
	}
	providers := app.oidcProviders()
	googleConfigured := providers["google"].ClientID != "" && providers["google"].Secret != ""
	appleConfigured := providers["apple"].ClientID != "" && providers["apple"].Secret != ""
	facebookConfigured := providers["facebook"].ClientID != "" && providers["facebook"].Secret != ""
	anyProviderConfigured := googleConfigured || appleConfigured || facebookConfigured

	app.render(w, r, "login.html", map[string]any{
		isAuthenticatedKey:          app.contextGetUser(r) != nil,
		"OIDCEnabled":               oidcEnabled,
		"OIDCLoginEnabled":          oidcLoginEnabled,
		"OIDCLoginType":             "oidc",
		"OIDCGoogleConfigured":      googleConfigured,
		"OIDCAppleConfigured":       appleConfigured,
		"OIDCFacebookConfigured":    facebookConfigured,
		"OIDCAnyProviderConfigured": anyProviderConfigured,
	}, http.StatusOK)
}

func (app *Application) signupPage(w http.ResponseWriter, r *http.Request) {
	oidcEnabled, err := app.siteConfigEnabled(r.Context(), "oidc_enabled", true)
	if err != nil {
		oidcEnabled = true
	}
	oidcLoginEnabled, err := app.siteConfigEnabled(r.Context(), "oidc_login_enabled", true)
	if err != nil {
		oidcLoginEnabled = true
	}
	providers := app.oidcProviders()
	googleConfigured := providers["google"].ClientID != "" && providers["google"].Secret != ""
	appleConfigured := providers["apple"].ClientID != "" && providers["apple"].Secret != ""
	facebookConfigured := providers["facebook"].ClientID != "" && providers["facebook"].Secret != ""
	anyProviderConfigured := googleConfigured || appleConfigured || facebookConfigured

	app.render(w, r, "signup.html", map[string]any{
		isAuthenticatedKey:          app.contextGetUser(r) != nil,
		"OIDCEnabled":               oidcEnabled,
		"OIDCLoginEnabled":          oidcLoginEnabled,
		"OIDCLoginType":             "oidc",
		"OIDCGoogleConfigured":      googleConfigured,
		"OIDCAppleConfigured":       appleConfigured,
		"OIDCFacebookConfigured":    facebookConfigured,
		"OIDCAnyProviderConfigured": anyProviderConfigured,
	}, http.StatusOK)
}

func (app *Application) microBlog(w http.ResponseWriter, r *http.Request) {
	if !app.requireSiteSectionEnabled(w, r, "micro_blog_enabled") {
		return
	}

	feed, user, err := app.resolveMicroFeed(r)
	if err != nil {
		app.serverErrorResponse(w, r, err)

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), defaultRequestTimeout)
	defer cancel()

	posts, err := app.loadMicroFeedPosts(ctx, feed, user, actorOutboxPageSize, 0)
	if err != nil {
		app.serverErrorResponse(w, r, err)

		return
	}

	app.render(w, r, "micro-blog.html", map[string]any{
		feedLabelKey:       feed,
		postsKey:           posts,
		isAuthenticatedKey: user != nil,
	}, http.StatusOK)
}

func (app *Application) pictures(w http.ResponseWriter, r *http.Request) {
	if !app.requireSiteSectionEnabled(w, r, "pictures_enabled") {
		return
	}

	feed, user, err := app.resolveSectionFeed(r, localFeedValue)
	if err != nil {
		app.serverErrorResponse(w, r, err)

		return
	}

	app.render(w, r, "pictures.html", map[string]any{
		feedLabelKey:       feed,
		isAuthenticatedKey: user != nil,
	}, http.StatusOK)
}

func (app *Application) pictureListPartial(w http.ResponseWriter, r *http.Request) {
	app.renderSectionListPartial(
		w,
		r,
		"pictures_enabled",
		"pictures.html",
		"pictureList",
		func(req *http.Request) (string, *models.User, error) {
			return app.resolveSectionFeed(req, localFeedValue)
		},
		func(ctx context.Context, feed string, user *models.User, limit, offset int) ([]models.PicturePost, error) {
			return app.loadPictureFeedPosts(ctx, feed, user, limit, offset)
		},
	)
}

func (app *Application) videos(w http.ResponseWriter, r *http.Request) {
	if !app.requireSiteSectionEnabled(w, r, "videos_enabled") {
		return
	}

	feed, user, err := app.resolveSectionFeed(r, localFeedValue)
	if err != nil {
		app.serverErrorResponse(w, r, err)

		return
	}

	app.render(w, r, "videos.html", map[string]any{
		feedLabelKey:       feed,
		isAuthenticatedKey: user != nil,
	}, http.StatusOK)
}

func (app *Application) videoListPartial(w http.ResponseWriter, r *http.Request) {
	app.renderSectionListPartial(
		w,
		r,
		"videos_enabled",
		"videos.html",
		"videoList",
		func(req *http.Request) (string, *models.User, error) {
			return app.resolveSectionFeed(req, localFeedValue)
		},
		func(ctx context.Context, feed string, user *models.User, limit, offset int) ([]models.VideoPost, error) {
			return app.loadVideoFeedPosts(ctx, feed, user, limit, offset)
		},
	)
}
