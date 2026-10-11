package store

import (
	"context"
	"strings"
	"testing"
)

func TestMessagePolicyMatrix(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	me := mustUser(t, db, "dm_me")
	peer := mustUser(t, db, "dm_peer")

	set := func(policy string) {
		if err := db.SetDMPolicy(ctx, me.ID, policy); err != nil {
			t.Fatal(err)
		}
	}
	may := func(from int64) bool {
		ok, err := db.MayMessage(ctx, from, me.ID)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}

	set(DMPolicyEveryone)
	if !may(peer.ID) {
		t.Error("everyone refused a stranger")
	}
	set(DMPolicyOff)
	if may(peer.ID) {
		t.Error("off accepted a message")
	}
	if err := db.SetDMPolicy(ctx, me.ID, "nonsense"); err == nil {
		t.Error("an unknown policy was stored")
	}

	// I follow them: "following" allows them to write, "mutual" still does not.
	if err := db.Follow(ctx, me.ID, peer.ID); err != nil {
		t.Fatal(err)
	}
	set(DMPolicyFollowing)
	if !may(peer.ID) {
		t.Error("following refused someone I follow")
	}
	set(DMPolicyMutual)
	if may(peer.ID) {
		t.Error("mutual accepted a one-way relation")
	}
	if err := db.Follow(ctx, peer.ID, me.ID); err != nil {
		t.Fatal(err)
	}
	if !may(peer.ID) {
		t.Error("mutual refused a mutual friend")
	}
	// Someone I do not follow at all is still a stranger under "following".
	other := mustUser(t, db, "dm_other")
	if may(other.ID) {
		t.Error("following accepted someone I never followed")
	}
}

func TestMessageRoundTrip(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	a := mustUser(t, db, "dm_a")
	b := mustUser(t, db, "dm_b")
	if err := db.SetDMPolicy(ctx, b.ID, DMPolicyEveryone); err != nil {
		t.Fatal(err)
	}

	if _, err := db.SendDM(ctx, a.ID, b.ID, "   "); err == nil {
		t.Fatal("empty message accepted")
	}
	if _, err := db.SendDM(ctx, a.ID, b.ID, strings.Repeat("字", MaxDMBody+1)); err == nil {
		t.Fatal("oversized message accepted")
	}

	first, err := db.SendDM(ctx, a.ID, b.ID, "  在吗？  ")
	if err != nil {
		t.Fatal(err)
	}
	if first.Body != "在吗？" {
		t.Fatalf("body not trimmed: %q", first.Body)
	}
	if _, err := db.SendDM(ctx, b.ID, a.ID, "在的"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SendDM(ctx, a.ID, b.ID, "有个题想问"); err != nil {
		t.Fatal(err)
	}

	unread, err := db.CountDMUnread(ctx, b.ID)
	if err != nil || unread != 2 {
		t.Fatalf("b unread = %d %v, want 2", unread, err)
	}
	if n, _ := db.CountDMUnread(ctx, a.ID); n != 1 {
		t.Fatalf("a unread = %d, want 1", n)
	}

	threads, err := db.ListDMThreads(ctx, b.ID, 10)
	if err != nil || len(threads) != 1 {
		t.Fatalf("threads = %+v %v", threads, err)
	}
	th := threads[0]
	if th.Peer != a.Handle || th.Unread != 2 || th.FromMe {
		t.Fatalf("thread = %+v", th)
	}
	if !strings.Contains(th.Last, "想问") {
		t.Errorf("thread shows %q as the last message", th.Last)
	}

	conv, err := db.ListDMConversation(ctx, b.ID, a.ID, 10)
	if err != nil || len(conv) != 3 {
		t.Fatalf("conversation = %+v %v", conv, err)
	}
	if conv[0].Body != "在吗？" || conv[2].Body != "有个题想问" {
		t.Errorf("conversation is not in reading order: %+v", conv)
	}
	for _, m := range conv {
		if m.FromMe {
			t.Error("FromMe is the caller's business, not the store's")
		}
	}

	marked, err := db.MarkDMRead(ctx, b.ID, a.ID)
	if err != nil || marked != 2 {
		t.Fatalf("marked %d rows %v, want 2", marked, err)
	}
	if n, _ := db.CountDMUnread(ctx, b.ID); n != 0 {
		t.Fatalf("unread after marking = %d", n)
	}
	// Marking twice changes nothing, and only ever the reader's own messages.
	if n, err := db.MarkDMRead(ctx, b.ID, a.ID); err != nil || n != 0 {
		t.Fatalf("second mark = %d %v", n, err)
	}
	if n, _ := db.CountDMUnread(ctx, a.ID); n != 1 {
		t.Fatalf("marking as b changed a's unread: %d", n)
	}
	threads, _ = db.ListDMThreads(ctx, b.ID, 10)
	if len(threads) != 1 || threads[0].Unread != 0 {
		t.Errorf("thread still reports unread: %+v", threads)
	}
}

func TestThreadsSeparateConversations(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	me := mustUser(t, db, "dm_hub")
	x := mustUser(t, db, "dm_x")
	y := mustUser(t, db, "dm_y")

	if _, err := db.SendDM(ctx, me.ID, x.ID, "给 x"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SendDM(ctx, y.ID, me.ID, "y 给我"); err != nil {
		t.Fatal(err)
	}
	threads, err := db.ListDMThreads(ctx, me.ID, 10)
	if err != nil || len(threads) != 2 {
		t.Fatalf("threads = %+v %v", threads, err)
	}
	// Newest activity first: y's message came after mine to x.
	if threads[0].Peer != y.Handle || threads[1].Peer != x.Handle {
		t.Errorf("thread order = %s,%s", threads[0].Peer, threads[1].Peer)
	}
	// y wrote last, so that thread is not "from me"; the x thread is.
	if threads[0].FromMe || !threads[1].FromMe {
		t.Errorf("FromMe mislabelled which side sent the last message: %+v", threads)
	}
}
