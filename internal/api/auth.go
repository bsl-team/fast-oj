package api

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/liangbinchen2013/fast-oj/internal/store"
)

const (
	sessionCookie = "fastoj_session"
	csrfField     = "csrf"
	csrfHeader    = "X-CSRF-Token"
)

type actor struct {
	User store.User
	// Admin and Judge are capabilities, not identities: the bootstrap admin
	// token grants the former without being a user account.
	Admin   bool
	Judge   bool
	Session store.Session
	// Cookie records that the credentials came from a cookie, which is exactly
	// the case where a cross-site request could ride along.
	Cookie bool
}

var (
	errNoCredentials = errors.New("no credentials")
	errForbidden     = errors.New("admin token required")
	errJudgeOnly     = errors.New("judge token required")
	errCSRF          = errors.New("csrf token missing or incorrect")
)

// authenticate resolves credentials from either the Authorization header (API,
// CLI, judge boxes) or the session cookie (browser). There is deliberately no
// "API token in a cookie" path: a cookie-based bearer would need its own CSRF
// secret, and real sessions make it redundant.
func (s *Server) authenticate(r *http.Request) (actor, error) {
	if token := bearerToken(r); token != "" {
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.AdminToken)) == 1 {
			u, err := s.db.EnsureUser(r.Context(), "admin", "admin")
			if err != nil {
				return actor{}, err
			}
			return actor{User: u, Admin: true}, nil
		}
		if s.cfg.JudgeToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.JudgeToken)) == 1 {
			return actor{Judge: true}, nil
		}
		u, err := s.db.UserByToken(r.Context(), token)
		if err != nil {
			return actor{}, err
		}
		return actor{User: u, Admin: u.Role == "admin"}, nil
	}

	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		u, sess, err := s.db.SessionUser(r.Context(), c.Value)
		if err != nil {
			return actor{}, err
		}
		return actor{User: u, Admin: u.Role == "admin", Session: sess, Cookie: true}, nil
	}
	return actor{}, errNoCredentials
}

func (s *Server) requireAdmin(r *http.Request) (actor, error) {
	a, err := s.authenticate(r)
	if err != nil {
		return a, err
	}
	if !a.Admin {
		return a, errForbidden
	}
	return a, nil
}

// requireJudge accepts the judge token, and the admin token so one operator can
// drive the queue by hand.
func (s *Server) requireJudge(r *http.Request) (actor, error) {
	a, err := s.authenticate(r)
	if err != nil {
		return a, err
	}
	if !a.Judge && !a.Admin {
		return a, errJudgeOnly
	}
	return a, nil
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func (s *Server) writeAuthError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errForbidden):
		s.respondError(w, r, http.StatusForbidden, "没有这个权限")
	case errors.Is(err, errJudgeOnly):
		s.respondError(w, r, http.StatusForbidden, "需要判机令牌")
	case errors.Is(err, errCSRF):
		s.respondError(w, r, http.StatusForbidden, "表单已过期，请刷新页面重试")
	default:
		s.respondError(w, r, http.StatusUnauthorized, "请先登录或提供有效的令牌")
	}
}

// csrfOK compares the submitted token against the session's, in constant time.
// Bearer-authenticated calls have no session and are immune to CSRF, so they pass.
func csrfOK(r *http.Request, a actor) bool {
	if !a.Cookie {
		return true
	}
	got := r.Header.Get(csrfHeader)
	if got == "" {
		got = r.FormValue(csrfField)
	}
	if got == "" || a.Session.CSRF == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(a.Session.CSRF)) == 1
}

// guardCSRF is the single choke point for state-changing browser requests. It
// parses the form first so both the field and the header are visible, and fails
// closed for any cookie-authenticated write without a matching token.
func (s *Server) guardCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if isForm(r) {
			if err := r.ParseForm(); err != nil {
				s.respondError(w, r, http.StatusBadRequest, "表单解析失败: "+err.Error())
				return
			}
		}
		a, err := s.authenticate(r)
		if err == nil && a.Cookie && !csrfOK(r, a) {
			s.log.Warn("csrf rejected", "path", r.URL.Path, "user", a.User.Handle)
			s.writeAuthError(w, r, errCSRF)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isForm(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") ||
		strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data")
}

// viewerOf describes the logged-in identity for the nav bar.
func (s *Server) viewerOf(r *http.Request) *viewer {
	a, err := s.authenticate(r)
	if err != nil || a.User.ID == 0 {
		return nil
	}
	return a.viewer()
}

// viewer turns an actor into what the templates need.
func (a actor) viewer() *viewer {
	if a.User.ID == 0 {
		return nil
	}
	name := a.User.Handle
	if a.User.DisplayName != "" {
		name = a.User.DisplayName
	}
	return &viewer{Handle: a.User.Handle, Name: name, Admin: a.Admin, CSRF: a.Session.CSRF}
}
