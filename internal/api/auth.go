package api

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/liangbinchen2013/fast-oj/internal/store"
)

type actor struct {
	User  store.User
	Admin bool
	Judge bool
}

var (
	errNoCredentials = errors.New("no credentials")
	errForbidden     = errors.New("admin token required")
	errJudgeOnly     = errors.New("judge token required")
)

// authenticate resolves a bearer token. The configured admin token bootstraps
// the first admin account and the judge token lets a judge box claim work;
// every other token is looked up by digest.
func (s *Server) authenticate(r *http.Request) (actor, error) {
	token := bearerToken(r)
	if token == "" {
		return actor{}, errNoCredentials
	}
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

// requireJudge accepts the judge token (and the admin token, so one operator
// can drive the queue by hand).
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

// requireAdmin authenticates and enforces the admin role.
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

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// writeAuthError maps the auth failures onto 401/403.
func (s *Server) writeAuthError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errForbidden):
		respondError(w, r, http.StatusForbidden, "admin access required")
	case errors.Is(err, errJudgeOnly):
		respondError(w, r, http.StatusForbidden, "judge token required")
	default:
		respondError(w, r, http.StatusUnauthorized, "invalid or missing bearer token")
	}
}
