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

func TestTokenListingAndRevocation(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	u, err := db.EnsureUser(ctx, "tokened", "user")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateToken(ctx, u.ID, "laptop", plain); err != nil {
		t.Fatal(err)
	}
	list, err := db.ListTokens(ctx, u.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("tokens = %+v %v", list, err)
	}
	if strings.Contains(list[0].Digest, plain) {
		t.Fatal("token digest resembles the plaintext")
	}
	// Revoking by digest works, and only for the owner.
	if _, err := db.DeleteTokenDigest(ctx, u.ID+1, list[0].Digest); err != nil {
		t.Fatal(err)
	}
	if still, err := db.ListTokens(ctx, u.ID); err != nil || len(still) != 1 {
		t.Fatalf("someone else revoked my token: %+v", still)
	}
	ok, err := db.DeleteTokenDigest(ctx, u.ID, list[0].Digest)
	if err != nil || !ok {
		t.Fatalf("revoke = %v %v", ok, err)
	}
	if _, err := db.UserByToken(ctx, plain); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked token still works: %v", err)
	}
}
