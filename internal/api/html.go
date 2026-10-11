package api

import (
	"bytes"
	"embed"
	"errors"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed templates/*.html static/*
var assetFS embed.FS

// Templates ship inside the binary, so a parse failure is a build-time bug and
// is allowed to stop the process rather than degrade one page at runtime.
var views = mustLoadViews()

type viewer struct {
	Handle string
	Admin  bool
}

// shell is the envelope every page template sees: layout fields plus the page
// payload under .Data.
type shell struct {
	Title string
	Me    *viewer
	Flash string
	Data  any
}

type viewEngine struct {
	pages map[string]*template.Template
}

func mustLoadViews() *viewEngine {
	v, err := loadViews()
	if err != nil {
		panic("templates: " + err.Error())
	}
	return v
}

func loadViews() (*viewEngine, error) {
	base, err := template.New("base").ParseFS(assetFS, "templates/layout.html")
	if err != nil {
		return nil, err
	}
	v := &viewEngine{pages: map[string]*template.Template{}}
	err = fs.WalkDir(assetFS, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path == "templates/layout.html" || !strings.HasSuffix(path, ".html") {
			return err
		}
		t, err := base.Clone()
		if err != nil {
			return err
		}
		t, err = t.ParseFS(assetFS, path)
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(strings.TrimPrefix(path, "templates/"), ".html")
		v.pages[name] = t
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(v.pages) == 0 {
		return nil, errors.New("no page templates found")
	}
	return v, nil
}

func (v *viewEngine) render(w http.ResponseWriter, status int, page string, data shell) error {
	t, ok := v.pages[page]
	if !ok {
		return errors.New("unknown page template " + page)
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout", data); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, err := w.Write(buf.Bytes())
	return err
}

// wantsHTML is the browser-facing half of content negotiation: the same URL
// serves text/plain to curl, JSON to ?format=json, and HTML to a browser.
func wantsHTML(r *http.Request) bool {
	if r.URL.Query().Get("format") != "" {
		return false
	}
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "text/html") || strings.Contains(accept, "application/xhtml+xml")
}

// page renders an HTML view, falling back to the text error shape if the
// template blows up, so a rendering bug cannot turn into a blank 200.
func (s *Server) page(w http.ResponseWriter, r *http.Request, status int, title, name string, me *viewer, flash string, data any) {
	err := views.render(w, status, name, shell{Title: title, Me: me, Flash: flash, Data: data})
	if err != nil {
		s.log.Error("template", "page", name, "err", err)
		s.respondError(w, r, http.StatusInternalServerError, "page rendering failed")
	}
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" || !strings.HasSuffix(name, ".css") && !strings.HasSuffix(name, ".js") {
		s.respondError(w, r, http.StatusNotFound, "no such asset")
		return
	}
	body, err := assetFS.ReadFile("static/" + name)
	if err != nil {
		s.respondError(w, r, http.StatusNotFound, "no such asset")
		return
	}
	switch {
	case strings.HasSuffix(name, ".css"):
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case strings.HasSuffix(name, ".js"):
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	default:
		s.respondError(w, r, http.StatusNotFound, "no such asset")
		return
	}
	// Assets are embedded in the binary, so a new executable is a new version.
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// errorPage is the HTML form of an error reply, used only for browsers.
func (s *Server) errorPage(w http.ResponseWriter, r *http.Request, status int, msg string) {
	s.page(w, r, status, http.StatusText(status), "error", s.viewerOf(r), "", map[string]any{
		"Status":  statusLine(status),
		"Class":   statusClass(status),
		"Message": msg,
	})
}
