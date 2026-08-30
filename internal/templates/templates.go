package templates

import (
	"html/template"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/cfbevan/multiverse/templates"
)

type TemplateCache map[string]*template.Template

func humanDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02 at 15:04")
}

var functions = template.FuncMap{
	"humanDate": humanDate,
}

func NewTemplateCache() (TemplateCache, error) {
	cache := TemplateCache{}

	pages, err := fs.Glob(templates.Files, "html/pages/*.html")
	if err != nil {
		return nil, err
	}

	for _, page := range pages {
		name := filepath.Base(page)

		patterns := []string{
			"html/base.html",
			"html/partials/*.html",
			"html/forms/*.html",
			page,
		}

		ts, err := template.New(name).Funcs(functions).ParseFS(templates.Files, patterns...)
		if err != nil {
			return nil, err
		}

		cache[name] = ts
	}
	return cache, nil
}
