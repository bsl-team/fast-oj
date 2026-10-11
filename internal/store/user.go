package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// userColumns never selects passhash: a hash must not ride along in a struct
// that might get marshalled to JSON.
const userColumns = `id, handle, IFNULL(email, ''), display_name, bio, role, status,
		(passhash <> '') AS has_password, created_at, IFNULL(last_login_at, 0)`

func scanUser(row scanner) (User, error) {
	var (
		u       User
		hasPass int
	)
	err := row.Scan(&u.ID, &u.Handle, &u.Email, &u.DisplayName, &u.Bio, &u.Role, &u.Status,
		&hasPass, &u.CreatedAt, &u.LastLoginAt)
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

// NewToken makes an unguessable opaque bearer token.
func NewToken() (string, error) {
	var buf [24]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf[:]), nil
}

// Tokens are stored as a SHA-256 digest so a stolen database file cannot be
// replayed as credentials.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (db *DB) CreateToken(ctx context.Context, userID int64, label, token string) error {
	_, err := db.SQL.ExecContext(ctx,
		`INSERT INTO tokens (token, user_id, label, created_at) VALUES (?, ?, ?, ?)`,
		hashToken(token), userID, label, nowMS())
	return conflict(err, "token")
}

func (db *DB) UserByToken(ctx context.Context, token string) (User, error) {
	if token == "" {
		return User{}, ErrNotFound
	}
	var userID int64
	err := db.SQL.QueryRowContext(ctx,
		`SELECT user_id FROM tokens WHERE token=?`, hashToken(token)).Scan(&userID)
	if err != nil {
		return User{}, notFound(err)
	}
	return db.GetUserByID(ctx, userID)
}

// TokenInfo is what a user may see about their own tokens: the digest prefix is
// enough to tell two apart, and the digest itself is not a secret.
type TokenInfo struct {
	Digest    string `json:"digest"`
	Label     string `json:"label"`
	CreatedAt int64  `json:"created_at"`
}

func (db *DB) ListTokens(ctx context.Context, userID int64) ([]TokenInfo, error) {
	rows, err := db.SQL.QueryContext(ctx,
		`SELECT token, label, created_at FROM tokens WHERE user_id=? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TokenInfo
	for rows.Next() {
		var t TokenInfo
		if err := rows.Scan(&t.Digest, &t.Label, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteTokenDigest revokes by stored digest, which is what the settings page
// can show without ever having the plaintext in hand.
func (db *DB) DeleteTokenDigest(ctx context.Context, userID int64, digest string) (bool, error) {
	res, err := db.SQL.ExecContext(ctx, `DELETE FROM tokens WHERE user_id=? AND token=?`, userID, digest)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (db *DB) DeleteToken(ctx context.Context, token string) (int64, error) {
	res, err := db.SQL.ExecContext(ctx, `DELETE FROM tokens WHERE token=?`, hashToken(token))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
