package store

import (
	"context"
	"errors"
	"testing"
)

func TestFollowRoundTrip(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	a := mustUser(t, db, "alice11")
	b := mustUser(t, db, "bob11")
	c := mustUser(t, db, "carol11")

	if err := db.Follow(ctx, a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	// A double-click or a retry after a lost response must not fail.
	if err := db.Follow(ctx, a.ID, b.ID); err != nil {
		t.Fatalf("repeat follow = %v", err)
	}
	if ok, err := db.IsFollowing(ctx, a.ID, b.ID); err != nil || !ok {
		t.Fatalf("is following = %v %v", ok, err)
	}
	if err := db.Follow(ctx, a.ID, a.ID); !errors.Is(err, ErrSelfFollow) {
		t.Fatalf("self follow = %v, want ErrSelfFollow", err)
	}
	if err := db.Follow(ctx, a.ID, 9999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("follow a stranger = %v, want ErrNotFound", err)
	}

	if err := db.Follow(ctx, b.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	if ok, err := db.AreMutual(ctx, a.ID, b.ID); err != nil || !ok {
		t.Fatalf("mutual = %v %v", ok, err)
	}
	if err := db.Follow(ctx, c.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	// One-way is not mutual, in either direction.
	if ok, _ := db.AreMutual(ctx, a.ID, c.ID); ok {
		t.Error("one-way relation reported as mutual")
	}
	if ok, _ := db.AreMutual(ctx, c.ID, a.ID); ok {
		t.Error("one-way relation reported as mutual in reverse")
	}

	following, err := db.ListFollowing(ctx, a.ID, 10)
	if err != nil || len(following) != 1 || following[0].Handle != b.Handle {
		t.Fatalf("following = %+v %v", following, err)
	}
	fans, err := db.ListFollowers(ctx, a.ID, 10)
	if err != nil || len(fans) != 2 {
		t.Fatalf("followers = %+v %v", fans, err)
	}
	if f, n, err := db.FollowCounts(ctx, a.ID); err != nil || f != 1 || n != 2 {
		t.Fatalf("counts = %d/%d %v", f, n, err)
	}

	// Unfollowing twice: the second one removed nothing, which the caller needs
	// to know so the UI can say so instead of pretending.
	if ok, err := db.Unfollow(ctx, c.ID, a.ID); err != nil || !ok {
		t.Fatalf("unfollow = %v %v", ok, err)
	}
	if ok, err := db.Unfollow(ctx, c.ID, a.ID); err != nil || ok {
		t.Fatalf("second unfollow = %v %v, want false", ok, err)
	}
	if f, n, _ := db.FollowCounts(ctx, a.ID); f != 1 || n != 1 {
		t.Fatalf("counts after unfollow = %d/%d", f, n)
	}
}

func TestFollowEdgesAreRemovedWithTheAccount(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	a := mustUser(t, db, "dave11")
	b := mustUser(t, db, "erin11")

	if err := db.Follow(ctx, a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.Follow(ctx, b.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.ExecContext(ctx, `DELETE FROM users WHERE id=?`, a.ID); err != nil {
		t.Fatal(err)
	}
	if f, n, err := db.FollowCounts(ctx, b.ID); err != nil || f != 0 || n != 0 {
		t.Fatalf("dangling edges left behind: %d/%d %v", f, n, err)
	}
}
