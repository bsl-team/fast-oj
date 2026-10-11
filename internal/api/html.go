package api

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/liangbinchen2013/fast-oj/internal/store"
)

//go:embed templates/*.html static/*
var assetFS embed.FS

// assetVersions fingerprints the embedded assets. The binary is the release, so a
// changed file always means a changed hash, which is what lets /static be cached
// for a year without a rebuild ever serving a stale stylesheet.
var assetVersions = func() map[string]string {
	out := map[string]string{}
	entries, err := fs.ReadDir(assetFS, "static")
	if err != nil {
		return out
	}
	for _, e := range entries {
		body, err := assetFS.ReadFile("static/" + e.Name())
		if err != nil {
			continue
		}
		sum := sha256.Sum256(body)
		out[e.Name()] = hex.EncodeToString(sum[:])[:8]
	}
	return out
}()

// staticURL is the template helper behind {{static "style.css"}}.
func staticURL(name string) string {
	v, ok := assetVersions[name]
	if !ok {
		return "/static/" + url.PathEscape(name)
	}
	return "/static/" + url.PathEscape(name) + "?v=" + v
}

// Templates ship inside the binary, so a parse failure is a build-time bug and
// is allowed to stop the process rather than degrade one page at runtime.
var views = mustLoadViews()

// viewer is the nav-bar identity. Handle is the URL slug, Name what a human
// reads; CSRF travels with it because every form a signed-in browser sees must
// carry its token.
type viewer struct {
	Handle string
	Name   string
	Admin  bool
	// Manage and Site are the two capabilities the nav bar renders links for, so
	// a delegate with only one of the consoles sees only that one.
	Manage bool
	Site   bool
	// Unread is the private-message badge in the nav bar.
	Unread int
	CSRF   string
}

// shell is the envelope every page template sees: layout fields plus the page
// payload under .Data.
type shell struct {
	Title string
	// Site carries the operator-editable title, tagline and footer.
	Site  store.SiteConfig
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
	// Templates render operator/user prose (tagline, footer, feed) through the
	// same Markdown+KaTeX pipeline as problem statements, so there is exactly one
	// escaping policy in the app.
	funcs := template.FuncMap{"markdown": renderMarkdownHTML, "static": staticURL}
	base, err := template.New("base").Funcs(funcs).ParseFS(assetFS, "templates/layout.html", "templates/partials.html")
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
	err := views.render(w, status, name, shell{Title: title, Site: s.site(r.Context()), Me: me, Flash: flash, Data: data})
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
	// The URL carries the content hash, so this can be cached for a year: a new
	// binary produces a new ?v=, and an old page never asks for the old name.
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
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
