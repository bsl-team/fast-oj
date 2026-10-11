package store

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

func TestPostLengthCountsCharacters(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	u := mustUser(t, db, "cjk10")

	// The browser's textarea counts characters, so storage must not charge three
	// bytes for each Chinese one.
	if _, err := db.CreatePost(ctx, u.ID, strings.Repeat("长", MaxPostBody)); err != nil {
		t.Fatalf("4000 Chinese characters refused: %v", err)
	}
	if _, err := db.CreatePost(ctx, u.ID, strings.Repeat("长", MaxPostBody+1)); err == nil {
		t.Fatal("4001 characters accepted")
	}
}

func TestFeedRoundTrip(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	u := mustUser(t, db, "writer9")

	post, err := db.CreatePost(ctx, u.ID, "  多行\n内容  \n\n")
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasSuffix(post.Body, " ") || !strings.Contains(post.Body, "多行") {
		t.Fatalf("body not trimmed: %q", post.Body)
	}
	if _, err := db.CreatePost(ctx, u.ID, "   "); err == nil {
		t.Fatal("empty post accepted")
	}
	if _, err := db.CreatePost(ctx, u.ID, strings.Repeat("x", MaxPostBody+1)); err == nil {
		t.Fatal("oversized post accepted")
	}

	// Newest first, and the cursor walks backwards from the oldest row shown.
	for i := 0; i < 3; i++ {
		if _, err := db.CreatePost(ctx, u.ID, "p"+strconv.Itoa(i)); err != nil {
			t.Fatal(err)
		}
	}
	page, err := db.ListPosts(ctx, 2, 0, 0, false)
	if err != nil || len(page) != 2 || !strings.HasPrefix(page[0].Body, "p2") {
		t.Fatalf("first page = %+v %v", page, err)
	}
	older, err := db.ListPosts(ctx, 2, page[1].ID, 0, false)
	if err != nil || len(older) != 2 || !strings.HasPrefix(older[0].Body, "p0") {
		t.Fatalf("second page = %+v %v", older, err)
	}
	if n, err := db.CountPosts(ctx); err != nil || n != 4 {
		t.Fatalf("count = %d %v", n, err)
	}

	// Filtering by author, and hiding as a moderator.
	other := mustUser(t, db, "other9")
	if _, err := db.CreatePost(ctx, other.ID, "别人的"); err != nil {
		t.Fatal(err)
	}
	mine, err := db.ListPosts(ctx, 10, 0, u.ID, false)
	if err != nil || len(mine) != 4 {
		t.Fatalf("author filter = %d %v", len(mine), err)
	}
	if ok, err := db.SetPostHidden(ctx, mine[0].ID, other.ID, true); err != nil || !ok {
		t.Fatalf("hide = %v %v", ok, err)
	}
	if ok, _ := db.SetPostHidden(ctx, mine[0].ID, other.ID, true); ok {
		t.Error("second hide should affect no rows")
	}
	visible, _ := db.ListPosts(ctx, 10, 0, u.ID, false)
	if len(visible) != 3 {
		t.Fatalf("hidden post still listed: %d", len(visible))
	}
	withHidden, _ := db.ListPosts(ctx, 10, 0, u.ID, true)
	if len(withHidden) != 4 {
		t.Fatalf("moderator view = %d, want 4", len(withHidden))
	}
	// The public count follows visibility, not the row count.
	if n, _ := db.CountPosts(ctx); n != 4 {
		t.Fatalf("count after hide = %d, want 4 of 5", n)
	}

	// Ownership-scoped delete, then the moderation delete.
	if ok, _ := db.DeletePost(ctx, mine[1].ID, other.ID); ok {
		t.Error("someone else deleted my post")
	}
	if ok, _ := db.DeletePost(ctx, mine[1].ID, u.ID); !ok {
		t.Error("author delete failed")
	}
	if ok, _ := db.DeletePostAny(ctx, mine[2].ID); !ok {
		t.Error("moderator delete failed")
	}
}
