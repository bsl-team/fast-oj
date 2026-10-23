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
	// ErrAccountCancelled refuses to re-edit an identity that has already been
	// wiped: its old login name may belong to the next registrant by now.
	ErrAccountCancelled = errors.New("该账号已注销，记录保留但身份不可恢复")
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
	// RequireEmailVerification is the site switch seen from the writing side: the row
	// lands unverified, and the caller is responsible for mailing the link. The store
	// does not read the switch itself, so a test or a bulk path that forgets it gets an
	// ordinary verified account instead of a locked one.
	RequireEmailVerification bool
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
	verified := 1
	if in.RequireEmailVerification {
		verified = 0
	}
	res, err := db.SQL.ExecContext(ctx,
		`INSERT INTO users (handle, email, passhash, display_name, role, status, email_verified, created_at)
		 VALUES (?, ?, ?, ?, 'user', 'active', ?, ?)`,
		in.Handle, in.Email, hash, strings.TrimSpace(in.DisplayName), verified, nowMS())
	if err != nil {
		return User{}, conflict(err, "handle or email")
	}
	id, _ := res.LastInsertId()
	return db.GetUserByID(ctx, id)
}

// BulkAccount is one row of the admin batch-signup form, and after the write also
// its result.
type BulkAccount struct {
	Handle   string `json:"handle"`
	Email    string `json:"email"`
	Password string `json:"password"`
	// Status is "" before the write, then "created" or "dup". It travels back out
	// to the page so a teacher can see which of the 40 names actually landed.
	Status string `json:"status"`
}

// CreateBulkAccounts writes a batch of accounts in one transaction and marks each
// row with what happened.
//
// 撞名的那条不牵连别人：ON CONFLICT DO NOTHING + 看影响行数，所以一个班里有两行写重了，
// 剩下的三十九个照样建好，页面上点名说哪两条没进。真正读写出错才整批回滚，不会出现
// "建到一半"的账号名单。
// 邮箱可以是空的：唯一索引是 partial（WHERE email IS NOT NULL），一批没有邮箱的班级账号
// 互相不冲突。
func (db *DB) CreateBulkAccounts(ctx context.Context, rows []BulkAccount, cost int) error {
	return db.InTx(ctx, func(tx *sql.Tx) error {
		for i := range rows {
			hash, err := HashPassword(rows[i].Password, cost)
			if err != nil {
				return err
			}
			// 没填邮箱要写成 NULL 而不是空串：那两条唯一索引都是 partial
			// （WHERE ... IS NOT NULL），空串会被算进去，第二个不填邮箱的号就会"撞名"。
			var email any
			if e := strings.TrimSpace(rows[i].Email); e != "" {
				email = e
			}
			res, err := tx.ExecContext(ctx,
				`INSERT INTO users (handle, email, passhash, display_name, role, status, created_at)
				 VALUES (?, ?, ?, '', 'user', 'active', ?)
				 ON CONFLICT DO NOTHING`,
				rows[i].Handle, email, hash, nowMS())
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				rows[i].Status = "dup"
				continue
			}
			rows[i].Status = "created"
		}
		return nil
	})
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
	// StepUpAt 是这个会话最近一次"把密码再输对"的时刻（毫秒），0 从没输过。权限控制台
	// 用它决定要不要再问一次密码；它属于会话，不属于账号，见 0031。
	StepUpAt int64
}

// StepUpFresh 报告那次确认还在有效期内。窗口多长是策略，由调用方给（api 那边的 2 分钟）。
//
// 带上"时刻不能在未来"这一条：这一列只会被 nowMS() 写入，但系统时钟倒退过一次，一个
// 未来的时间戳就能让这个会话永远不用再确认——多比一次整数减法换掉这种可能，值。
func (s Session) StepUpFresh(nowMS int64, window time.Duration) bool {
	return s.StepUpAt > 0 && s.StepUpAt <= nowMS && nowMS-s.StepUpAt <= window.Milliseconds()
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
		u        User
		userID   int64
		csrf     string
		expires  int64
		stepUpAt int64
	)
	err := db.SQL.QueryRowContext(ctx,
		`SELECT s.user_id, s.csrf, s.expires_at, s.step_up_at, u.handle, u.role, u.status
		 FROM sessions s JOIN users u ON u.id=s.user_id
		 WHERE s.id=? AND s.expires_at > ?`, id, nowMS()).
		Scan(&userID, &csrf, &expires, &stepUpAt, &u.Handle, &u.Role, &u.Status)
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
	return u, Session{ID: id, UserID: userID, CSRF: csrf, ExpiresAt: expires, StepUpAt: stepUpAt}, nil
}

// MarkStepUp 记下"这个会话刚刚把密码输对了"。调用方必须先自己比对过密码——这一行存在的意义
// 就是"非零当且仅当证明过"，所以它只接受比对成功的结果。
//
// 反过来不需要"收回"这条路：改密码与封号都会把会话整条删掉（SetPassword / SetUserStatus 里的
// DropUserSessions），会话没了，那次确认自然跟着没了。
func (db *DB) MarkStepUp(ctx context.Context, sessionID string) error {
	_, err := db.SQL.ExecContext(ctx, `UPDATE sessions SET step_up_at=? WHERE id=?`, nowMS(), sessionID)
	return err
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

// --- 通用速率限制 ---
//
// RateHit / RateAllowed 用的是登录失败那同一张计数表：它的语义本来就是"一个键在滚动窗口
// 里被记了几次"，再开一张表只会让运维多一张要清理的表。名字不叫 failure 是因为提交与建题
// 这些路径上，每一次尝试都要计数——限流挡的是刷屏，不是错。
//
// 注意调用顺序：先 RateAllowed 再 RateHit。反过来会把"被拒的那一次"也记进去，于是被
// 限住的人永远出不去（每一次试探都续上一格）。

// RateHit 记下这个键的一次动作。
func (db *DB) RateHit(ctx context.Context, key string, window time.Duration) (int, error) {
	return db.RegisterLoginFailure(ctx, key, window)
}

// RateAllowed 报告这个键在窗口里还能不能再做一次。
func (db *DB) RateAllowed(ctx context.Context, key string, window time.Duration, max int) (bool, error) {
	return db.LoginAllowed(ctx, key, window, max)
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
