package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"runtime/debug"
	"strings"

	"github.com/cfbevan/multiverse/internal/models"
)

type envelope map[string]any

const maxJSONBodyBytes = 1_048_576

// writeJSON is a helper for sending JSON responses. It takes in a status code, data to be sent to
// the client, and any custom headers. It then converts the data to JSON and sends it to the client
// along with the provided headers.
func (app *Application) writeJSON(
	w http.ResponseWriter,
	status int,
	data envelope,
	headers http.Header,
) error {
	js, err := json.MarshalIndent(data, "", "\t")
	if err != nil {
		return err
	}

	js = append(js, '\n')

	maps.Copy(w.Header(), headers)

	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(status)
	if _, err := w.Write(js); err != nil {
		return err
	}

	return nil
}

func (app *Application) readJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	err := dec.Decode(dst)
	if err != nil {
		var syntaxError *json.SyntaxError
		var unmarshalTypeError *json.UnmarshalTypeError
		var invalidUnmarshalError *json.InvalidUnmarshalError
		var maxBytesError *http.MaxBytesError

		switch {
		case errors.As(err, &syntaxError):
			return fmt.Errorf(
				"body contains badly-formed JSON (at character %d)",
				syntaxError.Offset,
			)

		case errors.Is(err, io.ErrUnexpectedEOF):
			return errors.New("body contains badly-formed JSON")

		case errors.As(err, &unmarshalTypeError):
			if unmarshalTypeError.Field != "" {
				return fmt.Errorf(
					"body contains incorrect JSON type for field %q",
					unmarshalTypeError.Field,
				)
			}

			return fmt.Errorf(
				"body contains incorrect JSON type (at character %d)",
				unmarshalTypeError.Offset,
			)

		case errors.Is(err, io.EOF):
			return errors.New("body must not be empty")

		case strings.HasPrefix(err.Error(), "json: unknown field "):
			fieldName := strings.TrimPrefix(err.Error(), "json: unknown field ")

			return fmt.Errorf("body contains unknown key %s", fieldName)

		case errors.As(err, &maxBytesError):
			return fmt.Errorf("body must not be larger than %d bytes", maxBytesError.Limit)

		case errors.As(err, &invalidUnmarshalError):
			panic(err)

		default:
			return err
		}
	}

	err = dec.Decode(&struct{}{})
	if !errors.Is(err, io.EOF) {
		return errors.New("body must only contain a single JSON value")
	}

	return nil
}

// serverError helper writes a log entry at Error level (including the request
// method and URI as attributes), then sends a generic 500 Internal Server Error
// response to the user.
func (app *Application) serverError(w http.ResponseWriter, r *http.Request, err error) {
	var (
		method = r.Method
		uri    = r.URL.RequestURI()
		trace  = string(debug.Stack())
	)

	app.logger.Error(err.Error(), "method", method, "uri", uri, "trace", trace)
	http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
}

// clientError helper sends a specific status code and corresponding description
// to the user. We'll use this later in the book to send responses like 400 "Bad
// Request" when there's a problem with the request that the user sent.
func (app *Application) clientError(w http.ResponseWriter, status int) {
	http.Error(w, http.StatusText(status), status)
}

// render is a helper that renders templates. We pass in the http.ResponseWriter, request, name of the template file,
// and any dynamic data that we want to pass in. If there's an error, we use the serverError helper to
// send a 500 Internal Server Error response to the user.
const (
	currentPathKey      = "CurrentPath"
	isAuthenticatedKey  = "IsAuthenticated"
	currentUserKey      = "CurrentUser"
	signupEnabledKey    = "SignupEnabled"
	blogEnabledKey      = "BlogEnabled"
	microBlogEnabledKey = "MicroBlogEnabled"
	picturesEnabledKey  = "PicturesEnabled"
	videosEnabledKey    = "VideosEnabled"
	audioEnabledKey     = "AudioEnabled"
)

func (app *Application) render(
	w http.ResponseWriter,
	r *http.Request,
	page string,
	data any,
	status ...int,
) {
	statusCode := http.StatusOK
	if len(status) > 0 {
		statusCode = status[0]
	}
	ts, ok := app.tmpl[page]
	if !ok {
		err := fmt.Errorf("the template %s does not exist", page)
		app.serverError(w, r, err)

		return
	}

	pageData := map[string]any{
		currentPathKey:      r.URL.Path,
		isAuthenticatedKey:  false,
		currentUserKey:      (*models.User)(nil),
		signupEnabledKey:    true,
		blogEnabledKey:      true,
		microBlogEnabledKey: true,
		picturesEnabledKey:  true,
		videosEnabledKey:    true,
		audioEnabledKey:     true,
	}
	if data != nil {
		if values, ok := data.(map[string]any); ok {
			maps.Copy(pageData, values)
		}
	}
	for key, enabled := range app.siteSectionFlags(r.Context()) {
		pageData[key] = enabled
	}
	if user := app.contextGetUser(r); user != nil {
		pageData[isAuthenticatedKey] = true
		pageData[currentUserKey] = user
	}

	buf := new(bytes.Buffer)
	err := ts.ExecuteTemplate(buf, "base", pageData)
	if err != nil {
		app.serverError(w, r, err)

		return
	}

	w.WriteHeader(statusCode)
	if _, err := buf.WriteTo(w); err != nil {
		app.serverError(w, r, err)
	}
}

func (app *Application) renderFragment(
	w http.ResponseWriter,
	r *http.Request,
	status int,
	page string,
	fragment string,
	data any,
) {
	ts, ok := app.tmpl[page]
	if !ok {
		err := fmt.Errorf("the template %s does not exist", page)
		app.serverError(w, r, err)

		return
	}

	buf := new(bytes.Buffer)
	err := ts.ExecuteTemplate(buf, fragment, data)
	if err != nil {
		app.serverError(w, r, err)

		return
	}

	w.WriteHeader(status)
	if _, err := buf.WriteTo(w); err != nil {
		app.serverError(w, r, err)
	}
}

func (app *Application) siteSectionFlags(ctx context.Context) map[string]bool {
	flags := map[string]bool{
		"SignupEnabled":    true,
		"BlogEnabled":      true,
		"MicroBlogEnabled": true,
		"PicturesEnabled":  true,
		"VideosEnabled":    true,
		"AudioEnabled":     true,
	}

	configs, err := app.siteConfigs.List(ctx)
	if err != nil {
		return flags
	}

	for _, config := range configs {
		switch config.Key {
		case "signup_enabled":
			flags["SignupEnabled"] = config.Enabled
		case "blog_enabled":
			flags["BlogEnabled"] = config.Enabled
		case "micro_blog_enabled":
			flags["MicroBlogEnabled"] = config.Enabled
		case "pictures_enabled":
			flags["PicturesEnabled"] = config.Enabled
		case "videos_enabled":
			flags["VideosEnabled"] = config.Enabled
		case "audio_enabled":
			flags["AudioEnabled"] = config.Enabled
		}
	}

	return flags
}

func (app *Application) requireSiteSectionEnabled(
	w http.ResponseWriter,
	r *http.Request,
	key string,
) bool {
	enabled, err := app.siteConfigEnabled(r.Context(), key, true)
	if err != nil {
		app.serverErrorResponse(w, r, err)

		return false
	}
	if !enabled {
		app.notFoundResponse(w, r)

		return false
	}

	return true
}
