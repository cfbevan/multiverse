package ui

import (
	"fmt"
	"net/http"

	"github.com/cfbevan/multiverse/templates"
)

func (app *Application) Routes() http.Handler {
	router := http.NewServeMux()

	router.Handle("GET /static/", http.FileServerFS(templates.Files))

	router.HandleFunc(fmt.Sprintf("%s /{$}", http.MethodGet), app.home)

	return app.recoverPanic(router)
}

func (app *Application) home(w http.ResponseWriter, r *http.Request) {
	app.render(w, r, http.StatusOK, "home.html", nil)
}
