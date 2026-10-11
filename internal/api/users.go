package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/liangbinchen2013/fast-oj/internal/store"
)

type userRequest struct {
	Handle string `json:"handle"`
	Role   string `json:"role"`
}

func validHandle(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '_', r == '-', r == '.':
		default:
			return false
		}
	}
	return true
}

func (s *Server) handleUserCreate(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireAdmin(r); err != nil {
		s.writeAuthError(w, r, err)
		return
	}
	var req userRequest
	if !s.decodeBody(w, r, &req) {
		return
	}
	req.Handle = strings.TrimSpace(req.Handle)
	if !validHandle(req.Handle) {
		s.respondError(w, r, http.StatusBadRequest, "handle must be 1..32 of [A-Za-z0-9_.-]")
		return
	}
	if req.Role == "" {
		req.Role = "user"
	}
	if req.Role != "user" && req.Role != "admin" {
		s.respondError(w, r, http.StatusBadRequest, "role must be user or admin")
		return
	}
	u, err := s.db.EnsureUser(r.Context(), req.Handle, req.Role)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	respond(w, r, http.StatusOK,
		fmt.Sprintf("user=%s id=%d role=%s\n", u.Handle, u.ID, u.Role), u)
}

func (s *Server) handleUserList(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireAdmin(r); err != nil {
		s.writeAuthError(w, r, err)
		return
	}
	users, err := s.db.ListUsers(r.Context(), 1000)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	var b strings.Builder
	b.WriteString("id     handle        role\n")
	for _, u := range users {
		fmt.Fprintf(&b, "%-6d %-13s %s\n", u.ID, u.Handle, u.Role)
	}
	fmt.Fprintf(&b, "users=%d\n", len(users))
	respond(w, r, http.StatusOK, b.String(), users)
}

type tokenRequest struct {
	Label string `json:"label"`
}

// handleTokenCreate issues an API token for a user. The plaintext is returned
// exactly once; only its digest is stored.
func (s *Server) handleTokenCreate(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireAdmin(r); err != nil {
		s.writeAuthError(w, r, err)
		return
	}
	handle := r.PathValue("handle")
	u, err := s.db.GetUserByHandle(r.Context(), handle)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	label := ""
	if r.ContentLength > 0 {
		var req tokenRequest
		if !s.decodeBody(w, r, &req) {
			return
		}
		label = strings.TrimSpace(req.Label)
	}

	token, err := store.NewToken()
	if err != nil {
		s.respondError(w, r, http.StatusInternalServerError, "token generation failed")
		return
	}
	if err := s.db.CreateToken(r.Context(), u.ID, label, token); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	s.log.Info("token issued", "user", u.Handle, "label", label)
	respond(w, r, http.StatusCreated,
		fmt.Sprintf("token=%s  user=%s  label=%s\n(copy now; only the digest is stored)\n",
			token, u.Handle, orDash(label)),
		map[string]any{"token": token, "user": u.Handle, "label": label})
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func (s *Server) handleWhoami(w http.ResponseWriter, r *http.Request) {
	a, err := s.authenticate(r)
	if err != nil {
		s.writeAuthError(w, r, err)
		return
	}
	respond(w, r, http.StatusOK,
		fmt.Sprintf("user=%s id=%d role=%s admin=%v\n", a.User.Handle, a.User.ID, a.User.Role, a.Admin),
		a.User)
}
