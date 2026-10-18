package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// verifiedNow reads the column straight off the row, which is how these tests tell
// "the link did nothing" apart from "the struct was stale".
func verifiedNow(t *testing.T, db *DB, id int64) bool {
	t.Helper()
	var n int
	err := db.SQL.QueryRowContext(context.Background(),
		`SELECT email_verified FROM users WHERE id=?`, id).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n != 0
}

// account is a registrant with an address, the way 注册 writes one.
func emailAccount(t *testing.T, db *DB, handle, email string, requireVerify bool) User {
	t.Helper()
	u, err := db.CreateAccount(context.Background(), AccountInput{
		Handle: handle, Email: email, Password: "correct-horse", Cost: 4,
		RequireEmailVerification: requireVerify,
	})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestCreateAccountHonoursTheVerificationSwitch(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	plain := emailAccount(t, db, "plain", "plain@example.com", false)
	if !plain.EmailVerified {
		t.Fatal("a registration on a site that does not ask got an unverified account")
	}
	gated := emailAccount(t, db, "gated", "gated@example.com", true)
	if gated.EmailVerified {
		t.Fatal("the switch was ignored: the account landed verified")
	}

	// 批量建号那条路根本不提这一列，所以它拿到的还是 0024 的默认值：验证过的。
	// 老师建的班级账号不该因为站长哪天顺手打开了开关就交不了作业。
	bulk := []BulkAccount{{Handle: "class1", Email: "class1@example.com", Password: "correct-horse"}}
	if err := db.CreateBulkAccounts(ctx, bulk, 4); err != nil {
		t.Fatal(err)
	}
	student, err := db.GetUserByHandle(ctx, "class1")
	if err != nil {
		t.Fatal(err)
	}
	if !student.EmailVerified {
		t.Fatal("a bulk-created account reads as unverified")
	}
}

func TestEmailTokenLifecycle(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	u := emailAccount(t, db, "alice", "alice@example.com", true)

	token, err := db.CreateEmailToken(ctx, u.ID, u.Email, EmailVerifyTTL)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) < 20 {
		t.Fatalf("token looks too short to be a secret: %q", token)
	}
	// 库里存的是哈希：令牌原文不该在任何一行里能读出来。
	var stored string
	if err := db.SQL.QueryRowContext(ctx, `SELECT token_hash FROM email_verify_tokens`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == token {
		t.Fatal("the raw token was stored, not its hash")
	}

	id, err := db.VerifyEmailToken(ctx, token)
	if err != nil {
		t.Fatalf("verify = %v", err)
	}
	if id != u.ID {
		t.Fatalf("verified user = %d, want %d", id, u.ID)
	}
	now, err := db.GetUserByID(ctx, u.ID)
	if err != nil || !now.EmailVerified {
		t.Fatalf("account still unverified after the link worked: %+v %v", now, err)
	}

	// 同一个链接再点一次：无效，而且不能把已经验证过的状态改回去或报错成 500。
	if _, err := db.VerifyEmailToken(ctx, token); !errors.Is(err, ErrEmailTokenInvalid) {
		t.Fatalf("reuse err = %v, want ErrEmailTokenInvalid", err)
	}
	if _, err := db.VerifyEmailToken(ctx, "not-a-token"); !errors.Is(err, ErrEmailTokenInvalid) {
		t.Fatalf("unknown token err = %v", err)
	}
	if _, err := db.VerifyEmailToken(ctx, ""); !errors.Is(err, ErrEmailTokenInvalid) {
		t.Fatalf("empty token err = %v", err)
	}
}

func TestEmailTokenExpires(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	u := emailAccount(t, db, "bob", "bob@example.com", true)

	token, err := db.CreateEmailToken(ctx, u.ID, u.Email, EmailVerifyTTL)
	if err != nil {
		t.Fatal(err)
	}
	// 把窗口改到过去，比 sleep 可靠：过期的判据是 expires_at 对当前时间，不是等了多久。
	if _, err := db.SQL.ExecContext(ctx,
		`UPDATE email_verify_tokens SET expires_at=? WHERE user_id=?`,
		time.Now().Add(-time.Second).UnixMilli(), u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.VerifyEmailToken(ctx, token); !errors.Is(err, ErrEmailTokenExpired) {
		t.Fatalf("err = %v, want ErrEmailTokenExpired", err)
	}
	if ok := verifiedNow(t, db, u.ID); ok {
		t.Fatal("an expired link verified the account")
	}
	// 过期那条行已经被清掉：一条没人点的链接不该在表里长住。
	var left int
	if err := db.SQL.QueryRowContext(ctx, `SELECT COUNT(*) FROM email_verify_tokens`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatalf("expired rows left behind = %d", left)
	}
}

func TestEmailTokenResendRetiresTheOldLink(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	u := emailAccount(t, db, "carol", "carol@example.com", true)

	first, err := db.CreateEmailToken(ctx, u.ID, u.Email, EmailVerifyTTL)
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.CreateEmailToken(ctx, u.ID, u.Email, EmailVerifyTTL)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("resend handed back the same token")
	}
	if _, err := db.VerifyEmailToken(ctx, first); !errors.Is(err, ErrEmailTokenInvalid) {
		t.Fatalf("the superseded link still works: %v", err)
	}
	if _, err := db.VerifyEmailToken(ctx, second); err != nil {
		t.Fatalf("the newest link must verify: %v", err)
	}
	// 一个人点三次重发，表里留着的只有一条已用掉的。
	var rows int
	if err := db.SQL.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM email_verify_tokens WHERE used_at IS NULL`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("live tokens after verifying = %d, want 0", rows)
	}
}

func TestEmailTokenCannotVerifyAnotherAddress(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	u := emailAccount(t, db, "dave", "dave@example.com", true)

	token, err := db.CreateEmailToken(ctx, u.ID, "old@example.com", EmailVerifyTTL)
	if err != nil {
		t.Fatal(err)
	}
	// 令牌上写的地址和账号现在的地址不是同一个（改了邮箱、或者链接被人改寄到别处），
	// 这条链接就不能作证。
	if _, err := db.VerifyEmailToken(ctx, token); !errors.Is(err, ErrEmailTokenInvalid) {
		t.Fatalf("err = %v, want ErrEmailTokenInvalid", err)
	}
	if ok := verifiedNow(t, db, u.ID); ok {
		t.Fatal("a token for a different address verified the account")
	}
}
