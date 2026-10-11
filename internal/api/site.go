package api

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/liangbinchen2013/fast-oj/internal/store"
)

// siteCache keeps the settings lookup off the hot path. The window is short on
// purpose: an operator editing the home page expects to see it on refresh.
type siteCache struct {
	mu     sync.RWMutex
	value  store.SiteConfig
	loaded time.Time
}

const siteTTL = 2 * time.Second

func (c *siteCache) invalidate() {
	c.mu.Lock()
	c.loaded = time.Time{}
	c.mu.Unlock()
}

func (c *siteCache) get(ctx context.Context, db *store.DB) (store.SiteConfig, error) {
	c.mu.RLock()
	fresh := time.Since(c.loaded) < siteTTL
	value := c.value
	c.mu.RUnlock()
	if fresh {
		return value, nil
	}

	site, err := db.Site(ctx)
	if err != nil {
		return value, err
	}
	c.mu.Lock()
	c.value, c.loaded = site, time.Now()
	c.mu.Unlock()
	return site, nil
}

func (s *Server) site(ctx context.Context) store.SiteConfig {
	site, err := s.cache.get(ctx, s.db)
	if err != nil {
		// A stale-but-usable last good value beats failing the whole page.
		return site
	}
	return site
}

// registrationOpen combines the operator's startup flag with the switch they can
// flip in the browser.
func (s *Server) registrationOpen(r *http.Request) bool {
	return s.cfg.AllowRegister && s.site(r.Context()).AllowRegister
}

type linkRow struct{ Title, URL string }

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		s.notFound(w, r)
		return
	}
	if !wantsHTML(r) {
		s.handleProblemList(w, r)
		return
	}

	a, _ := s.authenticate(r)
	site := s.site(r.Context())
	problems, err := s.db.ListProblems(r.Context(), 0, 6, "", !a.can(permProblemEditAny))
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	posts, err := s.db.ListPosts(r.Context(), 8, 0, 0, false)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	counts, err := s.db.Counts(r.Context())
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}

	s.page(w, r, http.StatusOK, site.Title, "home", a.viewer(), msgOf(r), map[string]any{
		"Problems":   problems.Items,
		"Posts":      postRows(posts, a),
		"Counts":     counts,
		"SignedIn":   a.User.ID != 0,
		"CanPost":    a.User.ID != 0,
		"Registered": s.registrationOpen(r),
	})
}

// --- site settings console ---

func (s *Server) handleSiteAdmin(w http.ResponseWriter, r *http.Request) {
	a, ok := s.requirePerm(w, r, permSiteSettings)
	if !ok {
		return
	}
	site := s.siteFresh(r.Context())
	s.page(w, r, http.StatusOK, "站点设置", "admin_site", a.viewer(), msgOf(r), map[string]any{
		"Site": site, "Links": linkLines(site.Links),
	})
}

// siteFresh bypasses the cache so the editor never shows a stale value.
func (s *Server) siteFresh(ctx context.Context) store.SiteConfig {
	site, err := s.db.Site(ctx)
	if err != nil {
		return site
	}
	return site
}

// linkLines renders the link list as the "标题 | https://…" lines the form uses,
// which is far friendlier than asking people to type JSON.
func linkLines(links []store.SiteLink) string {
	var b strings.Builder
	for _, l := range links {
		b.WriteString(l.Title + " | " + l.URL + "\n")
	}
	return b.String()
}

func parseLinkLines(raw string) ([]store.SiteLink, error) {
	var links []store.SiteLink
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		title, target, found := strings.Cut(line, "|")
		if !found {
			return nil, errBadLink
		}
		title = strings.TrimSpace(title)
		target = strings.TrimSpace(target)
		if title == "" || len(title) > 60 {
			return nil, errBadLink
		}
		if !safeExternalURL(target) {
			return nil, errBadLink
		}
		links = append(links, store.SiteLink{Title: title, URL: target})
	}
	if len(links) > 20 {
		return nil, errTooManyLinks
	}
	return links, nil
}

var (
	errBadLink      = &linkError{"每行要写成「标题 | https://…」，链接只允许 http/https/邮件"}
	errTooManyLinks = &linkError{"友情链接最多 20 条"}
)

type linkError struct{ msg string }

func (e *linkError) Error() string { return e.msg }

// safeExternalURL refuses javascript: and friends in a link that will be
// rendered as an <a href>.
func safeExternalURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.String() == "" {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "mailto":
		return true
	}
	return false
}

func (s *Server) handleSiteSave(w http.ResponseWriter, r *http.Request) {
	a, ok := s.requirePerm(w, r, permSiteSettings)
	if !ok {
		return
	}
	in := store.SiteConfig{
		Title:   strings.TrimSpace(r.FormValue("title")),
		Tagline: r.FormValue("tagline"),
		Footer:  r.FormValue("footer"),
		// The checkbox is only present when checked.
		AllowRegister: r.FormValue("allow_register") == "1",
	}
	if in.Title == "" || len(in.Title) > 60 {
		s.respondError(w, r, http.StatusBadRequest, "站点标题要 1..60 个字符")
		return
	}
	if len(in.Tagline) > 2000 || len(in.Footer) > 2000 {
		s.respondError(w, r, http.StatusBadRequest, "首页语与页脚各最长 2000 字")
		return
	}
	links, err := parseLinkLines(r.FormValue("links"))
	if err != nil {
		s.respondError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	in.Links = links

	if err := s.db.SetSiteAll(r.Context(), a.User.ID, in); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	// Drop the cache so the change is visible on the very next request.
	s.cache.invalidate()
	s.log.Info("site settings saved", "by", a.User.Handle)
	http.Redirect(w, r, "/admin/site?msg="+url.QueryEscape("已保存"), http.StatusSeeOther)
}
