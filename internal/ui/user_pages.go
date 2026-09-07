package ui

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/cfbevan/multiverse/internal/models"
	"github.com/cfbevan/multiverse/internal/validator"
)

const (
	defaultRequestTimeout = 3 * time.Second
	oidcStateCookieName   = "mv_oidc_state"
	oidcStateCookieMaxAge = 300
	defaultMediaType      = "application/octet-stream"
	defaultUploadFilename = "upload.bin"
	oidcProviderGoogle    = "google"
	oidcProviderApple     = "apple"
	oidcProviderFacebook  = "facebook"
	jwtPartsCount         = 3
	oidcHandleMinLength   = 3
	oidcHandleSuffixMod   = 10000
	oidcHandleSuffixWidth = 4
	userHandleFallback    = "user"
	statusKey             = "status"
	themePresetKey        = "theme_preset"
	descriptionKey        = "description"
	countKey              = "count"
	handleKey             = "handle"
	userKey               = "user"
	emailKey              = "email"
	displayNameKey        = "display_name"
	bioKey                = "bio"
	isAdminKey            = "is_admin"
	actorKey              = "actor"
	contentKey            = "content"
	attributedToKey       = "attributedTo"
	activityContextKey    = "@context"
	feedKey               = "feed"
	feedLabelKey          = "Feed"
	postsKey              = "Posts"
	errorsKey             = "Errors"
	apObjectIDKey         = "ap_object_id"
	actorIDKey            = "actor_id"
	entityTypeKey         = "entity_type"
	entityIDKey           = "entity_id"
	typeKey               = "type"
	nameKey               = "name"
	publishedKey          = "published"
	titleKey              = "title"
	visibilityKey         = "visibility"
	mediaAssetIDKey       = "media_asset_id"
	publishedAtKey        = "published_at"
	objectKey             = "object"
	passwordKey           = "password"
	activityStreamsNS     = "https://www.w3.org/ns/activitystreams"
	createActivityType    = "Create"
	noteObjectType        = "Note"
	localFeedValue        = "local"
	federatedFeedValue    = "federated"
	minimumPasswordChars  = 8
	randomBytesLength     = 32
)

var handleSanitizeRegex = regexp.MustCompile(`[^a-z0-9]+`)

type oidcProvider struct {
	Key          string
	Name         string
	AuthURL      string
	TokenURL     string
	UserInfoURL  string
	ClientID     string
	ClientSecret string
	Scope        string
}

type oidcProviderStatus struct {
	Name             string
	ClientIDSet      bool
	SecretSet        bool
	RedirectURL      string
	AuthEndpoint     string
	TokenEndpoint    string
	UserInfoEndpoint string
}

func isAllowedThemePreset(preset string) bool {
	switch preset {
	case "default-dark", "default-light", "tokyo-night", "tokyo-night-storm",
		"catppuccin-latte", "catppuccin-frappe", "catppuccin-macchiato", "catppuccin-mocha":
		return true
	default:
		return false
	}
}

func (app *Application) userProfilePage(w http.ResponseWriter, r *http.Request) {
	app.render(w, r, "profile.html", nil, http.StatusOK)
}

func (app *Application) userSettingsPage(w http.ResponseWriter, r *http.Request) {
	app.render(w, r, "settings.html", nil, http.StatusOK)
}

func (app *Application) adminSettingsPage(w http.ResponseWriter, r *http.Request) {
	app.render(w, r, "site-settings.html", map[string]any{
		"OIDCProviders": app.oidcProviderStatuses(),
	}, http.StatusOK)
}

func (app *Application) passwordResetPage(w http.ResponseWriter, r *http.Request) {
	app.render(w, r, "password-reset.html", map[string]any{
		"ResetToken": strings.TrimSpace(r.URL.Query().Get("token")),
	}, http.StatusOK)
}

func (app *Application) currentAccount(w http.ResponseWriter, r *http.Request) {
	user := app.contextGetUser(r)
	if user == nil {
		app.authenticationRequiredResponse(w, r)

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), defaultRequestTimeout)
	defer cancel()

	payload, err := app.currentAccountPayload(ctx, user)
	if err != nil {
		app.serverErrorResponse(w, r, err)

		return
	}

	err = app.writeJSON(w, http.StatusOK, payload, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) updateThemeSettings(w http.ResponseWriter, r *http.Request) {
	user := app.contextGetUser(r)
	if user == nil {
		app.authenticationRequiredResponse(w, r)

		return
	}

	themePreset, err := readStringPayload(r, "theme_preset")
	if err != nil {
		app.badRequestResponse(w, r, err)

		return
	}
	themePreset = strings.TrimSpace(themePreset)
	if !isAllowedThemePreset(themePreset) {
		app.failedValidationResponse(
			w,
			r,
			map[string]string{themePresetKey: "must be a valid theme preset"},
		)

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), defaultRequestTimeout)
	defer cancel()

	if err := app.userSettings.UpsertThemePreset(ctx, user.ID, themePreset); err != nil {
		app.serverErrorResponse(w, r, err)

		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{
		statusKey:      "ok",
		themePresetKey: themePreset,
	}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) requestPasswordResetEmail(w http.ResponseWriter, r *http.Request) {
	user := app.contextGetUser(r)
	if user == nil {
		app.authenticationRequiredResponse(w, r)

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), defaultRequestTimeout)
	defer cancel()

	enabled, err := app.siteConfigEnabled(ctx, "password_reset_enabled")
	if err != nil {
		app.serverErrorResponse(w, r, err)

		return
	}
	if !enabled {
		app.notPermittedResponse(w, r)

		return
	}

	rawToken, tokenHash, err := newPasswordResetToken()
	if err != nil {
		app.serverErrorResponse(w, r, err)

		return
	}

	reset := &models.PasswordResetToken{
		TokenHash: tokenHash,
		UserID:    user.ID,
		ExpiresAt: time.Now().UTC().Add(1 * time.Hour),
	}
	if err := app.resetTokens.Insert(ctx, reset); err != nil {
		app.serverErrorResponse(w, r, err)

		return
	}

	resetURL := strings.TrimSuffix(
		app.config.ActivityPubBaseURL,
		"/",
	) + "/user/password-reset?token=" + rawToken
	body := fmt.Sprintf(
		"Hello %s,\n\nUse this link to reset your password:\n%s\n\nThis link expires in one hour.\n",
		user.Handle,
		resetURL,
	)
	if err := app.mailer.Send(user.Email, "Multiverse password reset", body); err != nil {
		app.serverErrorResponse(w, r, err)

		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{statusKey: "sent"}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) resetPassword(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		if err := app.readJSON(w, r, &input); err != nil {
			app.badRequestResponse(w, r, err)

			return
		}
	} else {
		if err := r.ParseForm(); err != nil {
			app.badRequestResponse(w, r, err)

			return
		}
		input.Token = strings.TrimSpace(r.FormValue("token"))
		input.Password = strings.TrimSpace(r.FormValue("password"))
	}

	v := validator.NewValidator()
	v.CheckField(validator.NotBlank(input.Token), "token", "must be provided")
	v.CheckField(validator.NotBlank(input.Password), "password", "must be provided")
	v.CheckField(
		validator.MinChars(input.Password, minimumPasswordChars),
		"password",
		"must be at least 8 characters long",
	)
	if !v.Valid() {
		app.failedValidationResponse(w, r, v.FieldErrors)

		return
	}

	tokenHash := hashPasswordResetToken(input.Token)
	ctx, cancel := context.WithTimeout(r.Context(), defaultRequestTimeout)
	defer cancel()

	resetToken, err := app.resetTokens.GetByTokenHash(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, models.ErrRecordNotFound) {
			app.invalidCredentialsResponse(w, r)

			return
		}
		app.serverErrorResponse(w, r, err)

		return
	}
	if resetToken.UsedAt != nil || time.Now().UTC().After(resetToken.ExpiresAt) {
		app.invalidCredentialsResponse(w, r)

		return
	}

	hash, err := hashPassword(input.Password)
	if err != nil {
		app.serverErrorResponse(w, r, err)

		return
	}

	if err := app.users.UpdatePasswordHash(ctx, resetToken.UserID, hash); err != nil {
		app.serverErrorResponse(w, r, err)

		return
	}
	if err := app.resetTokens.MarkUsed(ctx, tokenHash, time.Now().UTC()); err != nil {
		app.serverErrorResponse(w, r, err)

		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{statusKey: "password_reset"}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) listSiteConfigs(w http.ResponseWriter, r *http.Request) {
	user := app.contextGetUser(r)
	if user == nil || !user.IsAdmin {
		app.authenticationRequiredResponse(w, r)

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), defaultRequestTimeout)
	defer cancel()

	configs, err := app.siteConfigs.List(ctx)
	if err != nil {
		app.serverErrorResponse(w, r, err)

		return
	}

	items := make([]envelope, 0, len(configs))
	for _, config := range configs {
		items = append(items, envelope{
			"key":          config.Key,
			"enabled":      config.Enabled,
			descriptionKey: config.Description,
		})
	}

	err = app.writeJSON(
		w,
		http.StatusOK,
		envelope{"site_configs": items, countKey: len(items)},
		nil,
	)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) updateSiteConfig(w http.ResponseWriter, r *http.Request) {
	user := app.contextGetUser(r)
	if user == nil || !user.IsAdmin {
		app.authenticationRequiredResponse(w, r)

		return
	}

	key := strings.TrimSpace(r.PathValue("key"))
	if key == "" {
		app.badRequestResponse(w, r, errors.New("key must be provided"))

		return
	}

	enabledValue, err := readStringPayload(r, "enabled")
	if err != nil {
		app.badRequestResponse(w, r, err)

		return
	}
	enabled := strings.EqualFold(strings.TrimSpace(enabledValue), "true") || enabledValue == "1" ||
		strings.EqualFold(enabledValue, "on")

	ctx, cancel := context.WithTimeout(r.Context(), defaultRequestTimeout)
	defer cancel()

	if err := app.siteConfigs.UpdateEnabled(ctx, key, enabled); err != nil {
		if errors.Is(err, models.ErrRecordNotFound) {
			app.notFoundResponse(w, r)

			return
		}
		app.serverErrorResponse(w, r, err)

		return
	}

	err = app.writeJSON(
		w,
		http.StatusOK,
		envelope{statusKey: "ok", "key": key, "enabled": enabled},
		nil,
	)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) currentAccountPayload(
	ctx context.Context,
	user *models.User,
) (envelope, error) {
	actor, err := app.actors.GetByUserID(ctx, user.ID)
	if err != nil && !errors.Is(err, models.ErrRecordNotFound) {
		return nil, err
	}

	settings, err := app.userSettings.GetByUserID(ctx, user.ID)
	if err != nil && !errors.Is(err, models.ErrRecordNotFound) {
		return nil, err
	}

	actorEnvelope := envelope{}
	if actor != nil {
		followers, err := app.actors.CountFollowers(ctx, actor.ID)
		if err != nil {
			return nil, err
		}
		following, err := app.actors.CountFollowing(ctx, actor.ID)
		if err != nil {
			return nil, err
		}
		actorEnvelope = envelope{
			"id":              actor.ID,
			handleKey:         actor.Handle,
			"domain":          actor.Domain,
			"inbox_url":       actor.InboxURL,
			"outbox_url":      actor.OutboxURL,
			"followers_url":   actor.FollowersURL,
			"following_url":   actor.FollowingURL,
			"public_key_id":   actor.PublicKeyID,
			"is_local":        actor.IsLocal,
			"followers_count": followers,
			"following_count": following,
		}
	}

	themePreset := ""
	if settings != nil {
		themePreset = settings.ThemePreset
	}

	return envelope{
		userKey: envelope{
			"id":           user.ID,
			emailKey:       user.Email,
			handleKey:      user.Handle,
			displayNameKey: user.DisplayName,
			bioKey:         user.Bio,
			"activated":    user.Activated,
			isAdminKey:     user.IsAdmin,
		},
		actorKey: actorEnvelope,
		"settings": envelope{
			themePresetKey: themePreset,
		},
	}, nil
}

func (app *Application) siteConfigEnabled(
	ctx context.Context,
	key string,
) (bool, error) {
	config, err := app.siteConfigs.GetByKey(ctx, key)
	if err != nil {
		if errors.Is(err, models.ErrRecordNotFound) {
			return true, nil
		}

		return false, err
	}

	return config.Enabled, nil
}

func (app *Application) oidcLogin(w http.ResponseWriter, r *http.Request) {
	enabled, err := app.siteConfigEnabled(r.Context(), "oidc_enabled")
	if err != nil || !enabled {
		app.notPermittedResponse(w, r)

		return
	}
	providerKey := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("provider")))
	if providerKey == "" {
		providerKey = oidcProviderGoogle
	}

	provider, ok := app.oidcProviders()[providerKey]
	if !ok {
		app.badRequestResponse(w, r, fmt.Errorf("unsupported oidc provider %q", providerKey))

		return
	}
	if strings.TrimSpace(provider.ClientID) == "" ||
		strings.TrimSpace(provider.ClientSecret) == "" {
		app.badRequestResponse(
			w,
			r,
			fmt.Errorf("oidc provider %q is not configured", provider.Name),
		)

		return
	}

	state, err := generateOIDCState()
	if err != nil {
		app.serverErrorResponse(w, r, err)

		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     oidcStateCookieName,
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   oidcStateCookieMaxAge,
	})

	params := url.Values{}
	params.Set("client_id", provider.ClientID)
	params.Set("redirect_uri", app.oidcRedirectURL(provider.Key))
	params.Set("response_type", "code")
	params.Set("scope", provider.Scope)
	params.Set("state", state)

	http.Redirect(w, r, provider.AuthURL+"?"+params.Encode(), http.StatusTemporaryRedirect)
}

func (app *Application) oidcCallback(w http.ResponseWriter, r *http.Request) {
	enabled, err := app.siteConfigEnabled(r.Context(), "oidc_enabled")
	if err != nil || !enabled {
		app.notPermittedResponse(w, r)

		return
	}
	providerKey := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("provider")))
	provider, ok := app.oidcProviders()[providerKey]
	if !ok {
		app.badRequestResponse(w, r, fmt.Errorf("unsupported oidc provider %q", providerKey))

		return
	}

	state := strings.TrimSpace(r.FormValue("state"))
	if state == "" {
		state = strings.TrimSpace(r.URL.Query().Get("state"))
	}

	stateCookie, err := r.Cookie(oidcStateCookieName)
	if err != nil || state == "" || stateCookie.Value != state {
		app.badRequestResponse(w, r, errors.New("invalid oidc state"))

		return
	}

	code := strings.TrimSpace(r.FormValue("code"))
	if code == "" {
		code = strings.TrimSpace(r.URL.Query().Get("code"))
	}
	if code == "" {
		app.badRequestResponse(w, r, errors.New("missing oidc code"))

		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     oidcStateCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})

	ctx, cancel := context.WithTimeout(r.Context(), defaultRequestTimeout)
	defer cancel()

	accessToken, idToken, err := app.exchangeOIDCCode(ctx, provider, code)
	if err != nil {
		app.serverErrorResponse(w, r, err)

		return
	}

	email, displayName := "", ""
	if accessToken != "" {
		email, displayName = app.fetchOIDCProfile(ctx, provider, accessToken)
	}
	if email == "" {
		email, displayName = oidcIdentityFromIDToken(idToken)
	}
	if email == "" {
		app.badRequestResponse(w, r, errors.New("oidc provider did not return an email address"))

		return
	}

	user, err := app.findOrCreateOIDCUser(
		ctx,
		strings.ToLower(strings.TrimSpace(email)),
		displayName,
	)
	if err != nil {
		app.serverErrorResponse(w, r, err)

		return
	}

	token, err := app.tokenManager.Issue(user.ID, authTokenExpiry())
	if err != nil {
		app.serverErrorResponse(w, r, err)

		return
	}

	html, err := app.oidcCallbackHTML(token, user)
	if err != nil {
		app.serverErrorResponse(w, r, err)

		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(html))
}

func (app *Application) oidcProviders() map[string]oidcProvider {
	// #nosec G101 -- provider client secrets are sourced from environment variables.
	return map[string]oidcProvider{
		oidcProviderGoogle: {
			Key:          oidcProviderGoogle,
			Name:         "Google",
			AuthURL:      "https://accounts.google.com/o/oauth2/v2/auth",
			TokenURL:     "https://oauth2.googleapis.com/token",
			UserInfoURL:  "https://openidconnect.googleapis.com/v1/userinfo",
			ClientID:     strings.TrimSpace(app.config.OIDCGoogleClientID),
			ClientSecret: strings.TrimSpace(app.config.OIDCGoogleSecret),
			Scope:        "openid email profile",
		},
		oidcProviderApple: {
			Key:          oidcProviderApple,
			Name:         "Apple",
			AuthURL:      "https://appleid.apple.com/auth/authorize",
			TokenURL:     "https://appleid.apple.com/auth/token",
			UserInfoURL:  "",
			ClientID:     strings.TrimSpace(app.config.OIDCAppleClientID),
			ClientSecret: strings.TrimSpace(app.config.OIDCAppleSecret),
			Scope:        "name email",
		},
		oidcProviderFacebook: {
			Key:          oidcProviderFacebook,
			Name:         "Facebook",
			AuthURL:      "https://www.facebook.com/v20.0/dialog/oauth",
			TokenURL:     "https://graph.facebook.com/v20.0/oauth/access_token",
			UserInfoURL:  "https://graph.facebook.com/me?fields=id,name,email",
			ClientID:     strings.TrimSpace(app.config.OIDCFacebookAppID),
			ClientSecret: strings.TrimSpace(app.config.OIDCFacebookSecret),
			Scope:        "public_profile,email",
		},
	}
}

func (app *Application) oidcProviderStatuses() []oidcProviderStatus {
	providers := app.oidcProviders()
	ordered := []string{oidcProviderGoogle, oidcProviderApple, oidcProviderFacebook}
	statuses := make([]oidcProviderStatus, 0, len(ordered))
	for _, key := range ordered {
		provider := providers[key]
		statuses = append(statuses, oidcProviderStatus{
			Name:             provider.Name,
			ClientIDSet:      provider.ClientID != "",
			SecretSet:        provider.ClientSecret != "",
			RedirectURL:      app.oidcRedirectURL(provider.Key),
			AuthEndpoint:     provider.AuthURL,
			TokenEndpoint:    provider.TokenURL,
			UserInfoEndpoint: provider.UserInfoURL,
		})
	}

	return statuses
}

func (app *Application) oidcRedirectURL(provider string) string {
	redirect := strings.TrimSpace(app.config.OIDCRedirectURL)
	if redirect == "" {
		redirect = strings.TrimSuffix(app.config.ActivityPubBaseURL, "/") + "/v1/auth/oidc/callback"
	}
	sep := "?"
	if strings.Contains(redirect, "?") {
		sep = "&"
	}

	return redirect + sep + "provider=" + url.QueryEscape(provider)
}

func generateOIDCState() (string, error) {
	raw := make([]byte, randomBytesLength)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}

	return hex.EncodeToString(raw), nil
}

func (app *Application) exchangeOIDCCode(
	ctx context.Context,
	provider oidcProvider,
	code string,
) (string, string, error) {
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", app.oidcRedirectURL(provider.Key))
	data.Set("client_id", provider.ClientID)
	data.Set("client_secret", provider.ClientSecret)

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		provider.TokenURL,
		strings.NewReader(data.Encode()),
	)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	res, err := app.httpClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = res.Body.Close() }()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return "", "", err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", "", fmt.Errorf("oidc token exchange failed with status %d", res.StatusCode)
	}

	var payload struct {
		AccessToken string `json:"access_token"`
		IDToken     string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", "", err
	}

	return strings.TrimSpace(payload.AccessToken), strings.TrimSpace(payload.IDToken), nil
}

func (app *Application) fetchOIDCProfile(
	ctx context.Context,
	provider oidcProvider,
	accessToken string,
) (string, string) {
	if provider.UserInfoURL == "" {
		return "", ""
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, provider.UserInfoURL, nil)
	if err != nil {
		return "", ""
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	res, err := app.httpClient.Do(req)
	if err != nil {
		return "", ""
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", ""
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return "", ""
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", ""
	}

	email, _ := payload["email"].(string)
	name, _ := payload["name"].(string)

	return strings.TrimSpace(email), strings.TrimSpace(name)
}

func oidcIdentityFromIDToken(idToken string) (string, string) {
	parts := strings.Split(idToken, ".")
	if len(parts) != jwtPartsCount {
		return "", ""
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", ""
	}

	var payload map[string]any
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return "", ""
	}

	email, _ := payload["email"].(string)
	name, _ := payload["name"].(string)

	return strings.TrimSpace(email), strings.TrimSpace(name)
}

func (app *Application) findOrCreateOIDCUser(
	ctx context.Context,
	email string,
	displayName string,
) (*models.User, error) {
	user, err := app.users.GetByEmail(ctx, email)
	if err == nil {
		return user, nil
	}
	if !errors.Is(err, models.ErrRecordNotFound) {
		return nil, err
	}

	if err := app.ensureSignupsEnabled(ctx); err != nil {
		return nil, err
	}

	newUser, err := newOIDCUser(email, displayName)
	if err != nil {
		return nil, err
	}

	if err := app.insertOIDCUser(ctx, newUser); err != nil {
		return nil, err
	}

	if err := app.insertRegistrationActor(ctx, newUser); err != nil {
		return nil, err
	}
	if err := app.userSettings.UpsertThemePreset(ctx, newUser.ID, ""); err != nil {
		return nil, err
	}

	return newUser, nil
}

func (app *Application) ensureSignupsEnabled(ctx context.Context) error {
	signupsEnabled, err := app.siteConfigEnabled(ctx, "signup_enabled")
	if err != nil {
		return err
	}
	if !signupsEnabled {
		return errors.New("signups are disabled")
	}

	return nil
}

func newOIDCUser(email string, displayName string) (*models.User, error) {
	rawPassword, _, err := newPasswordResetToken()
	if err != nil {
		return nil, err
	}

	hash, err := hashPassword(rawPassword)
	if err != nil {
		return nil, err
	}

	handle := generateOIDCHandle(email)
	if displayName == "" {
		displayName = handle
	}

	return &models.User{
		Email:        email,
		Handle:       handle,
		DisplayName:  displayName,
		PasswordHash: hash,
		Bio:          "",
		Activated:    true,
	}, nil
}

func (app *Application) insertOIDCUser(ctx context.Context, user *models.User) error {
	if err := app.users.Insert(ctx, user); err == nil {
		return nil
	} else if !errors.Is(err, models.ErrDuplicateHandle) {
		return err
	}

	baseHandle := user.Handle
	user.Handle = fmt.Sprintf("%s%d", baseHandle, time.Now().Unix()%oidcHandleSuffixMod)

	return app.users.Insert(ctx, user)
}

func generateOIDCHandle(email string) string {
	base := strings.ToLower(strings.TrimSpace(email))
	if idx := strings.Index(base, "@"); idx > 0 {
		base = base[:idx]
	}
	base = handleSanitizeRegex.ReplaceAllString(base, "")
	if len(base) < oidcHandleMinLength {
		base = userHandleFallback
	}
	if len(base) > maxHandleLength-oidcHandleSuffixWidth-1 {
		base = base[:maxHandleLength-oidcHandleSuffixWidth-1]
	}

	return fmt.Sprintf("%s%0*d", base, oidcHandleSuffixWidth, time.Now().Unix()%oidcHandleSuffixMod)
}

func (app *Application) oidcCallbackHTML(token string, user *models.User) (string, error) {
	userJSON, err := json.Marshal(map[string]any{
		"id":           user.ID,
		"email":        user.Email,
		"handle":       user.Handle,
		"display_name": user.DisplayName,
		bioKey:         user.Bio,
		isAdminKey:     user.IsAdmin,
	})
	if err != nil {
		return "", err
	}

	return fmt.Sprintf(`<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Signing in...</title></head>
<body>
<script>
localStorage.setItem('multiverse-token', %q);
localStorage.setItem('multiverse-user', %q);
window.location.replace('/');
</script>
</body>
</html>`, token, string(userJSON)), nil
}

func readStringPayload(r *http.Request, key string) (string, error) {
	if strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			return "", err
		}
		value, _ := input[key].(string)

		return value, nil
	}

	if err := r.ParseForm(); err != nil {
		return "", err
	}

	return r.FormValue(key), nil
}

func newPasswordResetToken() (string, string, error) {
	raw := make([]byte, randomBytesLength)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	rawToken := hex.EncodeToString(raw)
	hash := hashPasswordResetToken(rawToken)

	return rawToken, hash, nil
}

func hashPasswordResetToken(token string) string {
	sum := sha256.Sum256([]byte(token))

	return hex.EncodeToString(sum[:])
}
