package api

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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

// featureClosed is the one answer every switched-off feature gives: the write stops
// here with the reason, instead of a half-finished write or a bare 403.
//
// 403 rather than 503 keeps it consistent with the rest of the app's refusals (a
// ban and a closed registration are 403 too), and a 5xx would have a proxy or a
// monitoring box treating an operator's choice as an outage.
func (s *Server) featureClosed(w http.ResponseWriter, r *http.Request, what string) {
	s.respondError(w, r, http.StatusForbidden, "本站已关闭"+what+"。")
}

// feedNotice is why this reader cannot post to 动态 right now, or "" when they can.
// The composer and the write path ask the same question, so the page never shows a
// box that would only walk into a refusal.
func (s *Server) feedNotice(r *http.Request, a actor) string {
	if !s.site(r.Context()).AllowFeed {
		return "本站已关闭动态发布。"
	}
	if a.User.ID == 0 {
		return ""
	}
	msg, _ := a.bannedFrom(permFeedPost, "发动态")
	return msg
}

// topicNotice is the same question for 帖子：站级开关优先，然后是这个账号的禁止。
func (s *Server) topicNotice(r *http.Request, a actor) string {
	if !s.site(r.Context()).AllowTopics {
		return "本站已关闭发帖。"
	}
	msg, _ := a.bannedFrom(permTopicPost, "发帖")
	return msg
}

type linkRow struct{ Title, URL string }

func tooLong(s string, n int) bool { return utf8.RuneCountInString(s) > n }

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		s.notFound(w, r)
		return
	}
	a, _ := s.authenticate(r)
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
	// The home page shows the ten newest posts of both kinds: a 求助贴 left off the
	// front page is a question nobody sees.
	topics, err := s.db.ListTopics(r.Context(), store.TopicFilter{Limit: homeTopics})
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	counts, err := s.db.Counts(r.Context())
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}

	// The layout already appends "· <站点名>", so the page title stays 首页.
	checkIn, err := s.checkInFor(r, a)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	s.page(w, r, http.StatusOK, "首页", "home", a.viewer(), msgOf(r), map[string]any{
		"CheckIn":    checkIn,
		"Problems":   problems.Items,
		"Posts":      postRows(posts, a),
		"Topics":     topicRows(topics, a),
		"Counts":     counts,
		"SignedIn":   a.User.ID != 0,
		"Registered": s.registrationOpen(r),
		// 首页也有动态输入框，理由要给它同一个答案。
		"Banned": s.feedNotice(r, a),
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
		if title == "" || tooLong(title, 60) {
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
	rawLinks := r.FormValue("links")
	in := store.SiteConfig{
		Title:   strings.TrimSpace(r.FormValue("title")),
		Tagline: r.FormValue("tagline"),
		Footer:  r.FormValue("footer"),
		APIHint: r.FormValue("api_hint"),
		// The checkbox is only present when checked, so every switch reads the same way.
		AllowRegister: r.FormValue("allow_register") == "1",
		AllowFeed:     r.FormValue("allow_feed") == "1",
		AllowTopics:   r.FormValue("allow_topics") == "1",
		AllowContests: r.FormValue("allow_contests") == "1",
		AllowDM:       r.FormValue("allow_dm") == "1",
	}
	// A rejected save keeps what was typed: having to re-enter a whole home page
	// because one link line was malformed is not a fair trade.
	fail := func(msg string) {
		s.page(w, r, http.StatusBadRequest, "站点设置", "admin_site", a.viewer(), msg, map[string]any{
			"Site": in, "Links": rawLinks,
		})
	}
	// Counted in runes because that is what the form's maxlength counts: 60 bytes
	// would refuse a 20-character Chinese site name.
	if tooLong(in.Tagline, 2000) || tooLong(in.Footer, 2000) || tooLong(in.APIHint, 2000) {
		fail("首页语、页脚与 API 提示各最长 2000 字")
		return
	}
	if in.Title == "" || tooLong(in.Title, 60) {
		fail("站点标题要 1..60 个字符")
		return
	}
	links, err := parseLinkLines(rawLinks)
	if err != nil {
		fail(err.Error())
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
