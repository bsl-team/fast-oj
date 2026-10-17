package store

import (
	"context"
)

// Permission is one capability the system can grant.
type Permission struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Staff bool   `json:"staff"`
}

func (db *DB) ListPermissions(ctx context.Context) ([]Permission, error) {
	rows, err := db.SQL.QueryContext(ctx, `SELECT key, label, staff FROM permissions ORDER BY staff DESC, key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Permission
	for rows.Next() {
		var (
			p     Permission
			staff int
		)
		if err := rows.Scan(&p.Key, &p.Label, &staff); err != nil {
			return nil, err
		}
		p.Staff = staff != 0
		out = append(out, p)
	}
	return out, rows.Err()
}

// EffectivePermissions is the role baseline with that user's individual grants
// and removals applied on top. It is one query per authenticated request.
//
// The super admin is the exception: every capability is on for them no matter what
// the two tables above say. A removal row cannot be created for them through the
// console, but the immunity lives here rather than only in the screen that writes
// those rows, so a hand-edited database cannot strip it either.
func (db *DB) EffectivePermissions(ctx context.Context, userID int64, role string) (map[string]bool, error) {
	rows, err := db.SQL.QueryContext(ctx,
		`SELECT key, 1 AS src, 1 AS granted FROM role_permissions WHERE role=?
		 UNION ALL
		 SELECT key, 2 AS src, granted FROM user_permissions WHERE user_id=?
		 UNION ALL
		 SELECT p.key, 3 AS src, 1 AS granted
		   FROM permissions p JOIN site_owner o ON o.id=1 AND o.user_id=?`, role, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Row order from a UNION ALL is not guaranteed, so precedence is resolved by
	// source number rather than by arrival order: the super admin row (3) beats a
	// per-user override (2), which in turn beats the tier default (1).
	type stamped struct {
		granted bool
		src     int
	}
	best := map[string]stamped{}
	for rows.Next() {
		var (
			key        string
			src, grant int
		)
		if err := rows.Scan(&key, &src, &grant); err != nil {
			return nil, err
		}
		if cur, seen := best[key]; !seen || src >= cur.src {
			best[key] = stamped{granted: grant != 0, src: src}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make(map[string]bool, len(best))
	for key, v := range best {
		out[key] = v.granted
	}
	return out, nil
}

// UserGrant is one row of the permission editor: what the tier gives, what the
// user's own row says, and what is effective.
type UserGrant struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	Staff     bool   `json:"staff"`
	ByRole    bool   `json:"by_role"`
	Override  *bool  `json:"override,omitempty"`
	Effective bool   `json:"effective"`
}

func (db *DB) UserGrants(ctx context.Context, userID int64, role string) ([]UserGrant, error) {
	perms, err := db.ListPermissions(ctx)
	if err != nil {
		return nil, err
	}
	roleSet, err := db.permissionsByRole(ctx, role)
	if err != nil {
		return nil, err
	}
	overrides, err := db.userOverrides(ctx, userID)
	if err != nil {
		return nil, err
	}
	// The editor has to show what the site actually does, and for the super admin
	// that is "everything on", whatever these two tables say.
	owner, err := db.IsOwner(ctx, userID)
	if err != nil {
		return nil, err
	}

	out := make([]UserGrant, 0, len(perms))
	for _, p := range perms {
		g := UserGrant{Key: p.Key, Label: p.Label, Staff: p.Staff, ByRole: roleSet[p.Key]}
		if v, ok := overrides[p.Key]; ok {
			val := v
			g.Override = &val
			g.Effective = v
		} else {
			g.Effective = g.ByRole
		}
		if owner {
			g.Effective = true
		}
		out = append(out, g)
	}
	return out, nil
}

func (db *DB) permissionsByRole(ctx context.Context, role string) (map[string]bool, error) {
	rows, err := db.SQL.QueryContext(ctx, `SELECT key FROM role_permissions WHERE role=?`, role)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		out[key] = true
	}
	return out, rows.Err()
}

func (db *DB) userOverrides(ctx context.Context, userID int64) (map[string]bool, error) {
	rows, err := db.SQL.QueryContext(ctx, `SELECT key, granted FROM user_permissions WHERE user_id=?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var (
			key     string
			granted int
		)
		if err := rows.Scan(&key, &granted); err != nil {
			return nil, err
		}
		out[key] = granted != 0
	}
	return out, rows.Err()
}

// SetUserPermission records an explicit grant or removal. Unknown keys fail
// rather than silently doing nothing, so a typo cannot look like a successful
// privilege change.
func (db *DB) SetUserPermission(ctx context.Context, userID int64, key string, granted bool) error {
	if err := db.guardOwner(ctx, userID); err != nil {
		return err
	}
	var n int
	if err := db.SQL.QueryRowContext(ctx, `SELECT COUNT(*) FROM permissions WHERE key=?`, key).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	_, err := db.SQL.ExecContext(ctx,
		`INSERT INTO user_permissions (user_id, key, granted) VALUES (?, ?, ?)
		 ON CONFLICT(user_id, key) DO UPDATE SET granted=excluded.granted`,
		userID, key, boolInt(granted))
	return err
}

// ClearUserPermission drops the override, returning the user to their tier default.
func (db *DB) ClearUserPermission(ctx context.Context, userID int64, key string) error {
	if err := db.guardOwner(ctx, userID); err != nil {
		return err
	}
	_, err := db.SQL.ExecContext(ctx, `DELETE FROM user_permissions WHERE user_id=? AND key=?`, userID, key)
	return err
}
