package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrWeakPassword    = errors.New("password is too short or too long")
	ErrAccountDisabled = errors.New("account is not active")
)

const (
	// Passwords are bcrypt-hashed; bcrypt silently truncates past 72 bytes, so
	// refusing long input is better than hashing a prefix.
	MinPasswordLen = 10
	MaxPasswordLen = 72
	SessionTTL     = 30 * 24 * time.Hour
)

// NewSecret returns a URL-safe random token of n bytes.
func NewSecret(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func HashPassword(password string, cost int) (string, error) {
	if len(password) < MinPasswordLen || len(password) > MaxPasswordLen {
		return "", ErrWeakPassword
	}
	if cost < bcrypt.MinCost {
		cost = 11
	}
	if cost > bcrypt.MaxCost {
		cost = bcrypt.MaxCost
	}
	sum, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return "", err
	}
	return string(sum), nil
}

// CheckPassword compares a candidate against a bcrypt digest. An empty hash
// never matches, so accounts without a password cannot be logged into.
func CheckPassword(hash, password string) bool {
	if hash == "" || password == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

type AccountInput struct {
	Handle      string
	Email       string
	Password    string
	DisplayName string
	Cost        int
}

// CreateAccount registers a normal user. Email uniqueness is enforced by a
// partial unique index, so a duplicate surfaces as ErrConflict.
func (db *DB) CreateAccount(ctx context.Context, in AccountInput) (User, error) {
	in.Handle = strings.TrimSpace(in.Handle)
	in.Email = strings.TrimSpace(in.Email)
	if in.Handle == "" || in.Email == "" {
		return User{}, errors.New("handle and email are required")
	}
	hash, err := HashPassword(in.Password, in.Cost)
	if err != nil {
		return User{}, err
	}
	res, err := db.SQL.ExecContext(ctx,
		`INSERT INTO users (handle, email, passhash, display_name, role, status, created_at)
		 VALUES (?, ?, ?, ?, 'user', 'active', ?)`,
		in.Handle, in.Email, hash, strings.TrimSpace(in.DisplayName), nowMS())
	if err != nil {
		return User{}, conflict(err, "handle or email")
	}
	id, _ := res.LastInsertId()
	return db.GetUserByID(ctx, id)
}

func (db *DB) GetUserByEmail(ctx context.Context, email string) (User, error) {
	return scanUser(db.SQL.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE email=? COLLATE NOCASE`, strings.TrimSpace(email)))
}

// FindForLogin looks a user up by email first, then by handle, and returns the
// password hash separately so it never rides inside the User struct.
func (db *DB) FindForLogin(ctx context.Context, identifier string) (User, string, error) {
	identifier = strings.TrimSpace(identifier)
	var (
		userID int64
		hash   string
		status string
	)
	err := db.SQL.QueryRowContext(ctx,
		`SELECT id, passhash, status FROM users
		 WHERE email=? COLLATE NOCASE OR handle=? COLLATE NOCASE LIMIT 1`, identifier, identifier).
		Scan(&userID, &hash, &status)
	if err != nil {
		return User{}, "", notFound(err)
	}
	if status != "active" {
		return User{}, "", ErrAccountDisabled
	}
	u, err := db.GetUserByID(ctx, userID)
	return u, hash, err
}

// UserPasswordHash fetches just the digest, for verifying the current password
// before a change. It is never part of a User.
func (db *DB) UserPasswordHash(ctx context.Context, userID int64) (string, error) {
	var hash string
	err := db.SQL.QueryRowContext(ctx, `SELECT passhash FROM users WHERE id=?`, userID).Scan(&hash)
	return hash, notFound(err)
}

func (db *DB) SetPassword(ctx context.Context, userID int64, password string, cost int) error {
	hash, err := HashPassword(password, cost)
	if err != nil {
		return err
	}
	if _, err := db.SQL.ExecContext(ctx, `UPDATE users SET passhash=? WHERE id=?`, hash, userID); err != nil {
		return err
	}
	// Changing a password invalidates every existing session.
	return db.DropUserSessions(ctx, userID)
}

func (db *DB) UpdateProfile(ctx context.Context, userID int64, displayName, bio string) error {
	_, err := db.SQL.ExecContext(ctx, `UPDATE users SET display_name=?, bio=? WHERE id=?`,
		strings.TrimSpace(displayName), bio, userID)
	return err
}

func (db *DB) TouchLogin(ctx context.Context, userID int64) error {
	_, err := db.SQL.ExecContext(ctx, `UPDATE users SET last_login_at=? WHERE id=?`, nowMS(), userID)
	return err
}

// --- sessions ---

type Session struct {
	ID        string
	UserID    int64
	CSRF      string
	ExpiresAt int64
}

func (db *DB) CreateSession(ctx context.Context, userID int64, ip, userAgent string, ttl time.Duration) (Session, error) {
	id, err := NewSecret(32)
	if err != nil {
		return Session{}, err
	}
	csrf, err := NewSecret(16)
	if err != nil {
		return Session{}, err
	}
	now := nowMS()
	expiry := now + ttl.Milliseconds()
	_, err = db.SQL.ExecContext(ctx,
		`INSERT INTO sessions (id, user_id, csrf, created_at, last_seen_at, expires_at, ip, user_agent)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, userID, csrf, now, now, expiry, ip, truncate(userAgent, 200))
	if err != nil {
		return Session{}, err
	}
	return Session{ID: id, UserID: userID, CSRF: csrf, ExpiresAt: expiry}, nil
}

// SessionUser resolves a session id to its owner, refusing expired sessions and
// disabled accounts, and refreshes the last-seen stamp.
func (db *DB) SessionUser(ctx context.Context, id string) (User, Session, error) {
	var (
		u       User
		userID  int64
		csrf    string
		expires int64
	)
	err := db.SQL.QueryRowContext(ctx,
		`SELECT s.user_id, s.csrf, s.expires_at, u.handle, u.role, u.status
		 FROM sessions s JOIN users u ON u.id=s.user_id
		 WHERE s.id=? AND s.expires_at > ?`, id, nowMS()).
		Scan(&userID, &csrf, &expires, &u.Handle, &u.Role, &u.Status)
	if err != nil {
		return User{}, Session{}, notFound(err)
	}
	if u.Status != "active" {
		return User{}, Session{}, ErrAccountDisabled
	}
	u, err = db.GetUserByID(ctx, userID)
	if err != nil {
		return User{}, Session{}, err
	}
	if _, err := db.SQL.ExecContext(ctx, `UPDATE sessions SET last_seen_at=? WHERE id=?`, nowMS(), id); err != nil {
		return User{}, Session{}, err
	}
	return u, Session{ID: id, UserID: userID, CSRF: csrf, ExpiresAt: expires}, nil
}

func (db *DB) DropSession(ctx context.Context, id string) error {
	_, err := db.SQL.ExecContext(ctx, `DELETE FROM sessions WHERE id=?`, id)
	return err
}

// DropUserSessions is how a password change or a ban logs a user out everywhere.
func (db *DB) DropUserSessions(ctx context.Context, userID int64) error {
	_, err := db.SQL.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, userID)
	return err
}

func (db *DB) PruneExpired(ctx context.Context) (int64, error) {
	res, err := db.SQL.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, nowMS())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// --- login throttling ---

// RegisterLoginFailure adds a strike for a key inside a rolling window and
// reports how many are on the clock. A window that has already elapsed restarts
// at 1 rather than accumulating forever.
func (db *DB) RegisterLoginFailure(ctx context.Context, key string, window time.Duration) (int, error) {
	now := nowMS()
	expired := now - window.Milliseconds()
	_, err := db.SQL.ExecContext(ctx,
		`INSERT INTO login_attempts (key, failures, window_start, updated_at) VALUES (?, 1, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET
			failures = CASE WHEN login_attempts.window_start < ? THEN 1 ELSE login_attempts.failures + 1 END,
			window_start = CASE WHEN login_attempts.window_start < ? THEN ? ELSE login_attempts.window_start END,
			updated_at = excluded.updated_at`,
		key, now, now, expired, expired, now)
	if err != nil {
		return 0, err
	}
	var failures int
	err = db.SQL.QueryRowContext(ctx, `SELECT failures FROM login_attempts WHERE key=?`, key).Scan(&failures)
	return failures, err
}

// LoginAllowed reports whether a key is still under the failure budget.
func (db *DB) LoginAllowed(ctx context.Context, key string, window time.Duration, max int) (bool, error) {
	var (
		failures int
		start    int64
	)
	err := db.SQL.QueryRowContext(ctx,
		`SELECT failures, window_start FROM login_attempts WHERE key=?`, key).Scan(&failures, &start)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if start < nowMS()-window.Milliseconds() {
		return true, nil
	}
	return failures < max, nil
}

func (db *DB) ClearLoginFailures(ctx context.Context, key string) error {
	_, err := db.SQL.ExecContext(ctx, `DELETE FROM login_attempts WHERE key=?`, key)
	return err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
