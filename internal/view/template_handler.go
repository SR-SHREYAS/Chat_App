package view

import (
	"net/http"
	"path/filepath"
	"sync"
	"text/template"
)

type TemplateHandler struct {
	once     sync.Once
	filename string
	templ    *template.Template
}

func NewTemplateHandler(filename string) *TemplateHandler {
	return &TemplateHandler{filename: filename}
}

func (t *TemplateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t.once.Do(func() {
		t.templ = template.Must(template.ParseFiles(filepath.Join("templates", t.filename)))
	})
	t.templ.Execute(w, r)
}
