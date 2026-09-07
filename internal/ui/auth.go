package ui

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/cfbevan/multiverse/internal/models"
	"golang.org/x/crypto/bcrypt"
)

const (
	bearerPartsCount = 2
	authTokenTTL     = 24 * time.Hour
)

func (app *Application) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
		if authHeader == "" {
			next.ServeHTTP(w, r)

			return
		}

		parts := strings.SplitN(authHeader, " ", bearerPartsCount)
		if len(parts) != bearerPartsCount || parts[0] != "Bearer" {
			app.invalidAuthenticationTokenResponse(w, r)

			return
		}

		user, err := app.userFromBearerToken(r.Context(), parts[1])
		if err != nil {
			if errors.Is(err, models.ErrRecordNotFound) {
				app.invalidAuthenticationTokenResponse(w, r)

				return
			}
			app.serverErrorResponse(w, r, err)

			return
		}

		r = app.contextSetUser(r, user)
		next.ServeHTTP(w, r)
	})
}

func (app *Application) requireAuthenticated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if app.contextGetUser(r) == nil {
			app.authenticationRequiredResponse(w, r)

			return
		}
		next.ServeHTTP(w, r)
	})
}

func (app *Application) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := app.contextGetUser(r)
		if user == nil {
			app.authenticationRequiredResponse(w, r)

			return
		}
		if !user.IsAdmin {
			app.notPermittedResponse(w, r)

			return
		}

		next.ServeHTTP(w, r)
	})
}

func hashPassword(plaintext string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}

	return string(hash), nil
}

func verifyPassword(hash, plaintext string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plaintext))
}

func authTokenExpiry() time.Duration {
	return authTokenTTL
}

func (app *Application) userFromBearerToken(
	ctx context.Context,
	token string,
) (*models.User, error) {
	claims, err := app.tokenManager.Parse(token)
	if err != nil {
		return nil, models.ErrAuthenticationToken
	}

	user, err := app.users.GetByID(ctx, claims.UserID)
	if err != nil {
		return nil, err
	}

	return user, nil
}
