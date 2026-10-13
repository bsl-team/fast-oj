package api

import (
	"crypto/subtle"
	"net/http"
	"net/url"
	"time"

	"github.com/liangbinchen2013/fast-oj/internal/store"
)

// claimHandle is the account the bootstrap token already owns: authenticating with
// -admin-token resolves to this handle, so the password set here logs the same
// identity in, not a second one.
const claimHandle = "admin"

const (
	claimWindow   = 15 * time.Minute
	claimAttempts = 5
)

// handleAdminClaimPage is the first-run page. A deployment with no web account yet
// is otherwise unreachable: the operator token is a curl credential and a browser
// cannot present one.
func (s *Server) handleAdminClaimPage(w http.ResponseWriter, r *http.Request) {
	if a, err := s.authenticate(r); err == nil && a.User.ID != 0 {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.page(w, r, http.StatusOK, "接管后台", "admin_claim", nil, msgOf(r), map[string]any{
		"Handle":   claimHandle,
		"HasOwned": s.adminHasPassword(r),
	})
}

// adminHasPassword only drives a warning on the form: overwriting a password that
// someone already set is a different action from starting a fresh install.
func (s *Server) adminHasPassword(r *http.Request) bool {
	u, err := s.db.GetUserByHandle(r.Context(), claimHandle)
	return err == nil && u.HasPassword
}

func (s *Server) handleAdminClaim(w http.ResponseWriter, r *http.Request) {
	key := "claim:" + clientIP(r)
	allowed, err := s.db.LoginAllowed(r.Context(), key, claimWindow, claimAttempts)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if !allowed {
		s.claimFailed(w, r, "失败次数过多，请 15 分钟后再试", http.StatusTooManyRequests)
		return
	}

	// Constant time, like the bearer path: this form is the only place the token
	// crosses a network boundary as data rather than as a header.
	if subtle.ConstantTimeCompare([]byte(r.FormValue("token")), []byte(s.cfg.AdminToken)) != 1 {
		if _, ferr := s.db.RegisterLoginFailure(r.Context(), key, claimWindow); ferr != nil {
			s.log.Error("throttle", "err", ferr)
		}
		s.log.Warn("claim refused", "ip", clientIP(r))
		// The typed token is never echoed back into the page.
		s.claimFailed(w, r, "管理令牌不正确", http.StatusForbidden)
		return
	}

	password := r.FormValue("password")
	if bad := passwordProblem(password); bad != "" {
		s.claimFailed(w, r, bad, http.StatusBadRequest)
		return
	}

	u, err := s.db.EnsureUser(r.Context(), claimHandle, "admin")
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	// SetPassword drops this account's existing sessions, so a session minted
	// before the claim cannot outlive it.
	if err := s.db.SetPassword(r.Context(), u.ID, password, s.cfg.BCryptCost); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	sess, err := s.db.CreateSession(r.Context(), u.ID, clientIP(r), r.UserAgent(), store.SessionTTL)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if err := s.db.ClearLoginFailures(r.Context(), key); err != nil {
		s.log.Error("throttle reset", "err", err)
	}
	if err := s.db.TouchLogin(r.Context(), u.ID); err != nil {
		s.log.Error("touch login", "err", err)
	}
	s.setSession(w, sess.ID, store.SessionTTL)
	s.log.Info("admin claimed", "user", u.Handle, "ip", clientIP(r))
	http.Redirect(w, r, "/admin/perms?msg="+url.QueryEscape("后台已接管，权限按需要在控制台授予"), http.StatusSeeOther)
}

// claimFailed redraws the page with the reason. The token field stays empty on
// purpose: re-showing a secret in a response body is how secrets end up in logs.
func (s *Server) claimFailed(w http.ResponseWriter, r *http.Request, msg string, status int) {
	s.page(w, r, status, "接管后台", "admin_claim", nil, msg, map[string]any{
		"Handle":   claimHandle,
		"HasOwned": s.adminHasPassword(r),
	})
}
