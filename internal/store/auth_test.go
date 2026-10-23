package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPasswordHashingAndPolicy(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery", 4)
	if err != nil {
		t.Fatal(err)
	}
	if hash == "correct-horse-battery" || !strings.HasPrefix(hash, "$2") {
		t.Fatalf("not a bcrypt digest: %q", hash)
	}
	if !CheckPassword(hash, "correct-horse-battery") {
		t.Fatal("correct password rejected")
	}
	if CheckPassword(hash, "Correct-horse-battery") {
		t.Fatal("wrong password accepted")
	}
	// An account without a password can never be logged into.
	if CheckPassword("", "anything") {
		t.Fatal("empty hash matched")
	}
	for _, weak := range []string{"short1", strings.Repeat("x", 80), ""} {
		if _, err := HashPassword(weak, 4); !errors.Is(err, ErrWeakPassword) {
			t.Errorf("HashPassword(%q) = %v, want ErrWeakPassword", weak, err)
		}
	}
}

func TestAccountUniquenessAndLookup(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	u, err := db.CreateAccount(ctx, AccountInput{
		Handle: "alice", Email: "Alice@Example.COM", Password: "correct-horse-battery", DisplayName: "爱丽丝", Cost: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "Alice@Example.COM" || !u.HasPassword || u.Role != "user" || u.Status != "active" {
		t.Fatalf("account = %+v", u)
	}
	// Case-insensitive on both sides.
	if _, err := db.GetUserByEmail(ctx, "alice@example.com"); err != nil {
		t.Fatalf("email lookup: %v", err)
	}
	for _, dup := range []AccountInput{
		{Handle: "alice", Email: "other@example.com", Password: "correct-horse-battery", Cost: 4},
		{Handle: "other", Email: "ALICE@example.com", Password: "correct-horse-battery", Cost: 4},
	} {
		if _, err := db.CreateAccount(ctx, dup); !errors.Is(err, ErrConflict) {
			t.Errorf("duplicate %+v = %v, want ErrConflict", dup, err)
		}
	}

	byHandle, hash, err := db.FindForLogin(ctx, "alice")
	if err != nil || !CheckPassword(hash, "correct-horse-battery") {
		t.Fatalf("login by handle = %+v %v", byHandle, err)
	}
	byEmail, _, err := db.FindForLogin(ctx, " alice@example.com ")
	if err != nil || byEmail.ID != u.ID {
		t.Fatalf("login by email = %+v %v", byEmail, err)
	}
	if _, _, err := db.FindForLogin(ctx, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown login = %v", err)
	}
}

func TestSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	u, err := db.CreateAccount(ctx, AccountInput{Handle: "bob", Email: "bob@example.com", Password: "correct-horse-battery", Cost: 4})
	if err != nil {
		t.Fatal(err)
	}

	sess, err := db.CreateSession(ctx, u.ID, "10.0.0.1", "test/1.0", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.ID) < 32 || len(sess.CSRF) < 16 {
		t.Fatalf("weak session material: %+v", sess)
	}
	got, back, err := db.SessionUser(ctx, sess.ID)
	if err != nil || got.ID != u.ID || back.CSRF != sess.CSRF {
		t.Fatalf("session lookup = %+v %+v %v", got, back, err)
	}
	// 身份再确认的时刻跟着同一行读出来：新会话是 0（登录成功不等于证明过密码），
	// MarkStepUp 之后它带着一个非零值，而 SessionUser 已经在读它了。
	if back.StepUpAt != 0 {
		t.Errorf("新建会话的 step_up_at = %d，应该是 0", back.StepUpAt)
	}
	if back.StepUpFresh(time.Now().UnixMilli(), 2*time.Minute) {
		t.Error("没确认过的会话被当成已确认")
	}
	if err := db.MarkStepUp(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	_, after, err := db.SessionUser(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.StepUpAt == 0 || !after.StepUpFresh(time.Now().UnixMilli(), 2*time.Minute) {
		t.Fatalf("确认之后还是没过：step_up_at = %d", after.StepUpAt)
	}
	// 窗口之外必须重新问：把"现在"推到那次确认之后 2 分 1 秒。
	if after.StepUpFresh(after.StepUpAt+(2*time.Minute+time.Second).Milliseconds(), 2*time.Minute) {
		t.Error("窗口之外的确认还算数")
	}
	// 正好 2 分钟还算数：这条边界要说清是含还是不含，否则以后没人敢改。
	if !after.StepUpFresh(after.StepUpAt+(2*time.Minute).Milliseconds(), 2*time.Minute) {
		t.Error("正好 2 分钟时被当成过期了")
	}
	// 未来的时刻不算数：时钟倒退过一次，也不该让一个会话永远不用再确认。
	if after.StepUpFresh(after.StepUpAt-time.Hour.Milliseconds(), 2*time.Minute) {
		t.Error("那次确认还在未来，却算已通过")
	}
	if _, _, err := db.SessionUser(ctx, sess.ID+"x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown session = %v", err)
	}

	// Expired sessions are refused.
	if _, err := db.SQL.ExecContext(ctx, `UPDATE sessions SET expires_at=? WHERE id=?`, nowMS()-1, sess.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.SessionUser(ctx, sess.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired session = %v", err)
	}

	second, err := db.CreateSession(ctx, u.ID, "10.0.0.2", "test/1.0", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.DropSession(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.SessionUser(ctx, second.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("dropped session still live: %v", err)
	}

	// A password change kills every session for that user.
	third, err := db.CreateSession(ctx, u.ID, "10.0.0.3", "test/1.0", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetPassword(ctx, u.ID, "another-good-password", 4); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.SessionUser(ctx, third.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("session survived SetPassword: %v", err)
	}
	if _, hash, err := db.FindForLogin(ctx, "bob"); err != nil || !CheckPassword(hash, "another-good-password") {
		t.Fatalf("new password does not verify: %v", err)
	}

	// Pruning removes exactly the rows that ran out their clock.
	expiring, err := db.CreateSession(ctx, u.ID, "10.0.0.4", "ua", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.ExecContext(ctx,
		`UPDATE sessions SET expires_at=? WHERE id=?`, nowMS()-1, expiring.ID); err != nil {
		t.Fatal(err)
	}
	n, err := db.PruneExpired(ctx)
	if err != nil || n != 1 {
		t.Fatalf("prune = %d %v, want 1", n, err)
	}
}

func TestDisabledAccountCannotUseSessionOrLogin(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	u, err := db.CreateAccount(ctx, AccountInput{Handle: "carol", Email: "c@example.com", Password: "correct-horse-battery", Cost: 4})
	if err != nil {
		t.Fatal(err)
	}
	sess, err := db.CreateSession(ctx, u.ID, "10.0.0.9", "ua", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.ExecContext(ctx, `UPDATE users SET status='banned' WHERE id=?`, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.SessionUser(ctx, sess.ID); !errors.Is(err, ErrAccountDisabled) {
		t.Fatalf("banned session = %v, want ErrAccountDisabled", err)
	}
	if _, _, err := db.FindForLogin(ctx, "carol"); !errors.Is(err, ErrAccountDisabled) {
		t.Fatalf("banned login = %v", err)
	}
}

func TestLoginThrottleWindow(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	const window = 15 * time.Minute

	for i := 1; i <= 3; i++ {
		n, err := db.RegisterLoginFailure(ctx, "login:1.2.3.4", window)
		if err != nil || n != i {
			t.Fatalf("failure %d = %d %v", i, n, err)
		}
	}
	if ok, err := db.LoginAllowed(ctx, "login:1.2.3.4", window, 3); err != nil || ok {
		t.Fatalf("still allowed after hitting the cap: %v %v", ok, err)
	}
	// A different key is unaffected.
	if ok, err := db.LoginAllowed(ctx, "login:5.6.7.8", window, 3); err != nil || !ok {
		t.Fatalf("other key blocked: %v %v", ok, err)
	}
	// Once the window has elapsed the counter restarts instead of staying stuck.
	if _, err := db.SQL.ExecContext(ctx,
		`UPDATE login_attempts SET window_start=? WHERE key='login:1.2.3.4'`,
		nowMS()-window.Milliseconds()-1000); err != nil {
		t.Fatal(err)
	}
	if ok, err := db.LoginAllowed(ctx, "login:1.2.3.4", window, 3); err != nil || !ok {
		t.Fatalf("stale window still blocking: %v %v", ok, err)
	}
	if n, err := db.RegisterLoginFailure(ctx, "login:1.2.3.4", window); err != nil || n != 1 {
		t.Fatalf("restart count = %d %v, want 1", n, err)
	}
	if err := db.ClearLoginFailures(ctx, "login:1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	if n, err := db.RegisterLoginFailure(ctx, "login:1.2.3.4", window); err != nil || n != 1 {
		t.Fatalf("after clear = %d %v", n, err)
	}
}
