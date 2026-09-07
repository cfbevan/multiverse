package ui

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/cfbevan/multiverse/internal/models"
	ap "github.com/cfbevan/multiverse/internal/platform/activitypub"
	"github.com/cfbevan/multiverse/internal/validator"
)

const (
	maxHandleLength   = 50
	minPasswordLength = 8
)

type registrationInput struct {
	Email       string `json:"email"`
	Handle      string `json:"handle"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
	Bio         string `json:"bio"`
}

func (app *Application) registerAccount(w http.ResponseWriter, r *http.Request) {
	input, ok := app.parseRegistrationInput(w, r)
	if !ok {
		return
	}

	if fieldErrors := app.validateRegistrationInput(input); len(fieldErrors) > 0 {
		app.failedValidationResponse(w, r, fieldErrors)

		return
	}

	user, ok := app.createRegistrationUser(w, r, r.Context(), input)
	if !ok {
		return
	}

	if err := app.insertRegistrationActor(r.Context(), user); err != nil {
		app.handleRegistrationInsertError(w, r, err)

		return
	}

	if err := app.userSettings.UpsertThemePreset(r.Context(), user.ID, ""); err != nil {
		app.serverErrorResponse(w, r, err)

		return
	}

	token, err := app.tokenManager.Issue(user.ID, authTokenExpiry())
	if err != nil {
		app.serverErrorResponse(w, r, err)

		return
	}

	if err := app.writeJSON(w, http.StatusCreated, envelope{
		userKey: envelope{
			"id":           user.ID,
			emailKey:       user.Email,
			handleKey:      user.Handle,
			displayNameKey: user.DisplayName,
			bioKey:         user.Bio,
			isAdminKey:     user.IsAdmin,
		},
		"token": token,
	}, nil); err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) parseRegistrationInput(
	w http.ResponseWriter,
	r *http.Request,
) (registrationInput, bool) {
	var input registrationInput

	if strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		if err := app.readJSON(w, r, &input); err != nil {
			app.badRequestResponse(w, r, err)

			return input, false
		}

		return input, true
	}

	if err := r.ParseForm(); err != nil {
		app.badRequestResponse(w, r, err)

		return input, false
	}
	input.Email = strings.TrimSpace(r.PostFormValue("email"))
	input.Handle = strings.TrimSpace(r.PostFormValue("handle"))
	input.DisplayName = strings.TrimSpace(r.PostFormValue("display_name"))
	input.Password = r.PostFormValue("password")
	input.Bio = strings.TrimSpace(r.PostFormValue("bio"))

	return input, true
}

func (app *Application) validateRegistrationInput(input registrationInput) map[string]string {
	v := validator.NewValidator()
	v.CheckField(validator.NotBlank(input.Email), "email", "must be provided")
	v.CheckField(
		validator.Matches(input.Email, validator.EmailRX),
		"email",
		"must be a valid email address",
	)
	v.CheckField(validator.NotBlank(input.Handle), "handle", "must be provided")
	v.CheckField(
		validator.MaxChars(input.Handle, maxHandleLength),
		"handle",
		"must not be more than 50 characters long",
	)
	v.CheckField(validator.NotBlank(input.DisplayName), "display_name", "must be provided")
	v.CheckField(validator.NotBlank(input.Password), "password", "must be provided")
	v.CheckField(
		validator.MinChars(input.Password, minPasswordLength),
		"password",
		"must be at least 8 characters long",
	)
	if v.Valid() {
		return nil
	}

	return v.FieldErrors
}

func (app *Application) createRegistrationUser(
	w http.ResponseWriter,
	r *http.Request,
	ctx context.Context,
	input registrationInput,
) (*models.User, bool) {
	hash, err := hashPassword(input.Password)
	if err != nil {
		app.serverErrorResponse(w, r, err)

		return nil, false
	}

	user := &models.User{
		Email:        strings.ToLower(strings.TrimSpace(input.Email)),
		Handle:       strings.TrimSpace(input.Handle),
		DisplayName:  strings.TrimSpace(input.DisplayName),
		PasswordHash: hash,
		Bio:          strings.TrimSpace(input.Bio),
		Activated:    true,
	}

	ctx, cancel := context.WithTimeout(ctx, defaultRequestTimeout)
	defer cancel()

	signupsEnabled, err := app.siteConfigEnabled(ctx, "signup_enabled")
	if err != nil {
		app.serverErrorResponse(w, r, err)

		return nil, false
	}
	if !signupsEnabled {
		app.notPermittedResponse(w, r)

		return nil, false
	}

	if err := app.users.Insert(ctx, user); err != nil {
		app.handleRegistrationInsertError(w, r, err)

		return nil, false
	}

	return user, true
}

func (app *Application) insertRegistrationActor(ctx context.Context, user *models.User) error {
	baseURL := strings.TrimSuffix(app.config.ActivityPubBaseURL, "/")
	userID := user.ID
	publicKeyPEM, privateKeyPEM, err := ap.GenerateActorKeyPair()
	if err != nil {
		return err
	}

	actor := &models.Actor{
		UserID:        &userID,
		Handle:        user.Handle,
		Domain:        app.config.ActivityPubDomain,
		InboxURL:      baseURL + "/inbox",
		OutboxURL:     baseURL + "/actors/" + user.Handle + "/outbox",
		FollowersURL:  baseURL + "/actors/" + user.Handle + "/followers",
		FollowingURL:  baseURL + "/actors/" + user.Handle + "/following",
		PublicKeyID:   baseURL + "/actors/" + user.Handle + "#main-key",
		PublicKeyPEM:  publicKeyPEM,
		PrivateKeyPEM: privateKeyPEM,
	}

	return app.actors.InsertLocal(ctx, actor)
}

func (app *Application) handleRegistrationInsertError(
	w http.ResponseWriter,
	r *http.Request,
	err error,
) {
	switch {
	case errors.Is(err, models.ErrDuplicateEmail):
		app.failedValidationResponse(
			w,
			r,
			map[string]string{emailKey: "a user with this email already exists"},
		)
	case errors.Is(err, models.ErrDuplicateHandle):
		app.failedValidationResponse(
			w,
			r,
			map[string]string{handleKey: "a user with this handle already exists"},
		)
	default:
		app.serverErrorResponse(w, r, err)
	}
}

func (app *Application) loginAccount(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email    string `json:"email"`
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
		input.Email = strings.TrimSpace(r.PostFormValue("email"))
		input.Password = r.PostFormValue("password")
	}

	ctx, cancel := context.WithTimeout(r.Context(), defaultRequestTimeout)
	defer cancel()

	user, err := app.users.GetByEmail(ctx, strings.ToLower(strings.TrimSpace(input.Email)))
	if err != nil {
		if errors.Is(err, models.ErrRecordNotFound) {
			app.invalidCredentialsResponse(w, r)

			return
		}
		app.serverErrorResponse(w, r, err)

		return
	}

	if err := verifyPassword(user.PasswordHash, input.Password); err != nil {
		app.invalidCredentialsResponse(w, r)

		return
	}

	token, err := app.tokenManager.Issue(user.ID, authTokenExpiry())
	if err != nil {
		app.serverErrorResponse(w, r, err)

		return
	}

	err = app.writeJSON(w, http.StatusOK, envelope{
		"token": token,
		"user": envelope{
			"id":           user.ID,
			emailKey:       user.Email,
			handleKey:      user.Handle,
			displayNameKey: user.DisplayName,
			"bio":          user.Bio,
			"is_admin":     user.IsAdmin,
		},
	}, nil)
	if err != nil {
		app.serverErrorResponse(w, r, err)
	}
}
