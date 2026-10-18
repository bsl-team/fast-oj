package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// Email verification tokens (M25f). A registrant gets a link whose token stands for
// "this address received this message"; the row is written once, read once and then
// retired, so the same link clicked twice cannot verify anything twice.
const EmailVerifyTTL = 24 * time.Hour

var (
	// ErrEmailTokenInvalid is the one answer for "no such token", "already used" and
	// "the address moved": the page must not tell a caller which of those it is, or a
	// guessed token could be probed for which half of it exists.
	ErrEmailTokenInvalid = errors.New("验证链接无效或已经用过")
	// ErrEmailTokenExpired says the link existed but its window closed. It is safe to
	// say out loud — the person holding the link already holds the token — and it is
	// the difference between "点重发" and "再试一次同一个链接".
	ErrEmailTokenExpired = errors.New("验证链接已过期，请重新发送")
)

// hashToken maps a link token to the value the database stores. SHA-256 is enough
// here because the token itself is 32 random bytes: there is no dictionary to run at.
func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// CreateEmailToken issues a single-use verification link for one account and returns
// the raw token, which is the only place it exists. The caller puts it in the mail.
//
// 重发为什么先把旧令牌删掉：一个人在页上点三次"重新发送"，不该手上有三条都能验证的链接。
// 删掉未用过的旧行、只留最新一条，点旧链接的人得到的就是 ErrEmailTokenInvalid，
// 页面上那句话（"重新发送过，旧的链接就作废了"）也就不用解释哪种链接还活着。
// 已经用掉的行留着（used_at 有值），它们不再能验证任何东西，留着只为了排查时看得见。
func (db *DB) CreateEmailToken(ctx context.Context, userID int64, email string, ttl time.Duration) (string, error) {
	raw, err := NewSecret(32)
	if err != nil {
		return "", err
	}
	if ttl <= 0 {
		ttl = EmailVerifyTTL
	}
	now := nowMS()
	err = db.InTx(ctx, func(tx *sql.Tx) error {
		// 一个用户一行未用的：先清掉上一次的，再插新的。
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM email_verify_tokens WHERE user_id=? AND used_at IS NULL`, userID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO email_verify_tokens (token_hash, user_id, email, created_at, expires_at)
			 VALUES (?, ?, ?, ?, ?)`,
			hashToken(raw), userID, strings.TrimSpace(email), now, now+ttl.Milliseconds())
		return err
	})
	if err != nil {
		return "", err
	}
	return raw, nil
}

// VerifyEmailToken consumes a token: it marks the account's address verified and
// retires the link. Everything happens in one transaction, so two simultaneous clicks
// cannot both walk away believing they were the one that verified the account.
func (db *DB) VerifyEmailToken(ctx context.Context, raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, ErrEmailTokenInvalid
	}
	var (
		userID  int64
		email   string
		expires int64
	)
	err := db.InTx(ctx, func(tx *sql.Tx) error {
		// 令牌只查哈希，原文一个字都不入库。
		err := tx.QueryRowContext(ctx,
			`SELECT t.user_id, t.email, t.expires_at FROM email_verify_tokens t
			 WHERE t.token_hash=? AND t.used_at IS NULL`, hashToken(raw)).
			Scan(&userID, &email, &expires)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrEmailTokenInvalid
		}
		if err != nil {
			return err
		}
		if expires <= nowMS() {
			return ErrEmailTokenExpired
		}
		// 邮件寄给的那个地址还得是账号现在的地址：改过邮箱之后，旧地址收到的链接
		// 不该替新地址作证。注销过的账号（status='deleted'）也不再接受验证。
		var current string
		var status string
		err = tx.QueryRowContext(ctx, `SELECT IFNULL(email,''), status FROM users WHERE id=?`, userID).
			Scan(&current, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrEmailTokenInvalid
		}
		if err != nil {
			return err
		}
		if status == "deleted" || !strings.EqualFold(strings.TrimSpace(current), strings.TrimSpace(email)) {
			return ErrEmailTokenInvalid
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET email_verified=1 WHERE id=?`, userID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE email_verify_tokens SET used_at=? WHERE token_hash=?`,
			nowMS(), hashToken(raw))
		return err
	})
	if err != nil {
		if errors.Is(err, ErrEmailTokenExpired) {
			// 过期这一行再没人能用得上了，顺手清掉。删除必须在事务外：事务里返回错误
			// 就是回滚，写在里面的 DELETE 也会跟着被抹掉。删失败不往上抛——那会把
			// 「链接过期了，请重新发送」这句有用的话换成一句 database error。
			_, _ = db.SQL.ExecContext(ctx, `DELETE FROM email_verify_tokens WHERE token_hash=?`, hashToken(raw))
		}
		return 0, err
	}
	return userID, nil
}
