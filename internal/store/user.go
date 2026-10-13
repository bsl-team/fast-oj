package store

import (
	"context"
	"fmt"
)

// userColumns never selects passhash: a hash must not ride along in a struct
// that might get marshalled to JSON.
const userColumns = `id, handle, IFNULL(email, ''), display_name, bio, role, status, dm_policy,
		(passhash <> '') AS has_password, created_at, IFNULL(last_login_at, 0)`

func scanUser(row scanner) (User, error) {
	var (
		u       User
		hasPass int
	)
	err := row.Scan(&u.ID, &u.Handle, &u.Email, &u.DisplayName, &u.Bio, &u.Role, &u.Status,
		&u.DMPolicy, &hasPass, &u.CreatedAt, &u.LastLoginAt)
	u.HasPassword = hasPass != 0
	return u, notFound(err)
}

// EnsureUser returns the existing row or creates it, so the judge and CLI can
// refer to people by handle without a registration flow.
func (db *DB) EnsureUser(ctx context.Context, handle, role string) (User, error) {
	if handle == "" {
		return User{}, fmt.Errorf("handle must not be empty")
	}
	if role == "" {
		role = "user"
	}
	if role != "user" && role != "admin" {
		return User{}, fmt.Errorf("unknown role %q", role)
	}
	_, err := db.SQL.ExecContext(ctx,
		`INSERT INTO users (handle, role, created_at) VALUES (?, ?, ?)
		 ON CONFLICT(handle) DO NOTHING`, handle, role, nowMS())
	if err != nil {
		return User{}, err
	}
	return db.GetUserByHandle(ctx, handle)
}

func (db *DB) GetUserByHandle(ctx context.Context, handle string) (User, error) {
	return scanUser(db.SQL.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE handle=? COLLATE NOCASE`, handle))
}

func (db *DB) GetUserByID(ctx context.Context, id int64) (User, error) {
	return scanUser(db.SQL.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE id=?`, id))
}

func (db *DB) ListUsers(ctx context.Context, limit int) ([]User, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := db.SQL.QueryContext(ctx, `SELECT `+userColumns+` FROM users ORDER BY id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SetUserRole changes the tier. Sessions stay valid; the effective capability
// set is recomputed on the next request, which is what makes a demotion stick
// immediately.
func (db *DB) SetUserRole(ctx context.Context, userID int64, role string) error {
	if role != "user" && role != "admin" {
		return fmt.Errorf("unknown role %q", role)
	}
	res, err := db.SQL.ExecContext(ctx, `UPDATE users SET role=? WHERE id=?`, role, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetUserStatus activates or bans an account. Banning also drops its sessions.
func (db *DB) SetUserStatus(ctx context.Context, userID int64, status string) error {
	if status != "active" && status != "banned" {
		return fmt.Errorf("unknown status %q", status)
	}
	res, err := db.SQL.ExecContext(ctx, `UPDATE users SET status=? WHERE id=?`, status, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if status != "active" {
		return db.DropUserSessions(ctx, userID)
	}
	return nil
}
