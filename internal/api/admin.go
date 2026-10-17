package api

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/liangbinchen2013/fast-oj/internal/store"
)

// admin.go is the permission console: who may do what, down to single
// capabilities, without handing out an admin account.

type adminUserRow struct {
	ID     int64
	Handle string
	Email  string
	Role   string
	Status string
	// Owner marks the super admin: the one account the console cannot touch.
	Owner bool
}

func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	a, ok := s.requirePerm(w, r, permPermissionManage)
	if !ok {
		return
	}
	users, err := s.db.ListUsers(r.Context(), 1000)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	ownerID, err := s.db.OwnerID(r.Context())
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	rows := make([]adminUserRow, 0, len(users))
	for _, u := range users {
		rows = append(rows, adminUserRow{
			ID: u.ID, Handle: u.Handle, Email: u.Email, Role: u.Role, Status: u.Status,
			Owner: u.ID == ownerID,
		})
	}
	s.page(w, r, http.StatusOK, "权限管理", "admin_users", a.viewer(), msgOf(r), map[string]any{
		"Users": rows, "Self": a.User.ID,
	})
}

type grantRow struct {
	Key       string
	Label     string
	Staff     bool
	ByRole    bool
	Override  string // "", "granted", "removed"
	Effective bool
}

func (s *Server) handleAdminUser(w http.ResponseWriter, r *http.Request) {
	a, ok := s.requirePerm(w, r, permPermissionManage)
	if !ok {
		return
	}
	u, grants, rows, err := s.loadGrantView(r)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	owner, err := s.db.IsOwner(r.Context(), u.ID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	s.page(w, r, http.StatusOK, "权限 · "+u.Handle, "admin_user", a.viewer(), msgOf(r), map[string]any{
		"Target":   u,
		"Grants":   rows,
		"AllPerms": grants,
		"Self":     a.User.ID == u.ID,
		// Owner is the second reason this page has no buttons: the console can
		// show the super admin what he holds, but not change it.
		"Owner":   owner,
		"CanUser": a.can(permUserManage),
	})
}

// loadGrantView resolves the target user and merges the three sources of truth
// (tier default, individual override, effective result) into table rows.
func (s *Server) loadGrantView(r *http.Request) (store.User, []store.UserGrant, []grantRow, error) {
	u, err := s.db.GetUserByHandle(r.Context(), r.PathValue("handle"))
	if err != nil {
		return store.User{}, nil, nil, err
	}
	grants, err := s.db.UserGrants(r.Context(), u.ID, u.Role)
	if err != nil {
		return u, nil, nil, err
	}
	rows := make([]grantRow, 0, len(grants))
	for _, g := range grants {
		override := ""
		if g.Override != nil {
			if *g.Override {
				override = "granted"
			} else {
				override = "removed"
			}
		}
		rows = append(rows, grantRow{
			Key: g.Key, Label: g.Label, Staff: g.Staff, ByRole: g.ByRole,
			Override: override, Effective: g.Effective,
		})
	}
	return u, grants, rows, nil
}

// adminTarget loads the user and refuses two things: editing yourself, and editing
// the super admin. Locking yourself out of the permission console is a far more
// likely accident than an attack, so the console simply refuses to change the
// account you are signed in with. The site owner is immune to every other admin,
// which is the one rule that has to hold even when someone else holds
// permission.manage.
func (s *Server) adminTarget(w http.ResponseWriter, r *http.Request, a actor) (store.User, bool) {
	u, err := s.db.GetUserByHandle(r.Context(), r.PathValue("handle"))
	if err != nil {
		s.writeStoreError(w, r, err)
		return store.User{}, false
	}
	if u.ID == a.User.ID {
		s.respondError(w, r, http.StatusBadRequest, "不能修改自己的权限，请用另一个管理员账号")
		return store.User{}, false
	}
	owner, err := s.db.IsOwner(r.Context(), u.ID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return store.User{}, false
	}
	if owner {
		s.respondError(w, r, http.StatusForbidden, "超级管理员的权限、角色与状态只有他自己能动")
		return store.User{}, false
	}
	return u, true
}

func (s *Server) handleAdminPermChange(w http.ResponseWriter, r *http.Request) {
	a, ok := s.requirePerm(w, r, permPermissionManage)
	if !ok {
		return
	}
	target, ok := s.adminTarget(w, r, a)
	if !ok {
		return
	}
	key := strings.TrimSpace(r.FormValue("key"))
	action := strings.TrimSpace(r.FormValue("action"))
	back := "/admin/perms/u/" + url.PathEscape(target.Handle)

	switch action {
	case "grant", "revoke":
		if err := s.db.SetUserPermission(r.Context(), target.ID, key, action == "grant"); err != nil {
			if err == store.ErrNotFound {
				s.respondError(w, r, http.StatusBadRequest, "没有这个权限项："+key)
				return
			}
			s.writeStoreError(w, r, err)
			return
		}
	case "clear":
		if err := s.db.ClearUserPermission(r.Context(), target.ID, key); err != nil {
			s.writeStoreError(w, r, err)
			return
		}
	default:
		s.respondError(w, r, http.StatusBadRequest, "action 只能是 grant / revoke / clear")
		return
	}
	s.log.Info("permission changed", "by", a.User.Handle, "target", target.Handle, "key", key, "action", action)
	http.Redirect(w, r, back+"?msg="+url.QueryEscape(fmt.Sprintf("%s → %s", target.Handle, key)), http.StatusSeeOther)
}

func (s *Server) handleAdminRoleChange(w http.ResponseWriter, r *http.Request) {
	a, ok := s.requirePerm(w, r, permUserManage)
	if !ok {
		return
	}
	target, ok := s.adminTarget(w, r, a)
	if !ok {
		return
	}
	role := strings.TrimSpace(r.FormValue("role"))
	if err := s.db.SetUserRole(r.Context(), target.ID, role); err != nil {
		s.respondError(w, r, http.StatusBadRequest, "角色设置失败: "+err.Error())
		return
	}
	s.log.Info("role changed", "by", a.User.Handle, "target", target.Handle, "role", role)
	http.Redirect(w, r, "/admin/perms/u/"+url.PathEscape(target.Handle)+"?msg="+url.QueryEscape("角色已设为 "+role), http.StatusSeeOther)
}

func (s *Server) handleAdminStatusChange(w http.ResponseWriter, r *http.Request) {
	a, ok := s.requirePerm(w, r, permUserManage)
	if !ok {
		return
	}
	target, ok := s.adminTarget(w, r, a)
	if !ok {
		return
	}
	status := strings.TrimSpace(r.FormValue("status"))
	if err := s.db.SetUserStatus(r.Context(), target.ID, status); err != nil {
		s.respondError(w, r, http.StatusBadRequest, "状态设置失败: "+err.Error())
		return
	}
	s.log.Info("status changed", "by", a.User.Handle, "target", target.Handle, "status", status)
	http.Redirect(w, r, "/admin/perms/u/"+url.PathEscape(target.Handle)+"?msg="+url.QueryEscape("状态已设为 "+status), http.StatusSeeOther)
}
