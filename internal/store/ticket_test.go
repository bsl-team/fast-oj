package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func mustTicket(t *testing.T, db *DB, kind, title string, authorID int64) Ticket {
	t.Helper()
	tk, err := db.CreateTicket(hctx, &Ticket{
		Kind: kind, Title: title, Body: "正文 " + title, AuthorID: authorID,
	})
	if err != nil {
		t.Fatalf("create ticket %q: %v", title, err)
	}
	return tk
}

// mustSubmission writes a judged submission with no contest — 0 is what a practice
// submission carries, since submissions.contest_id is NOT NULL DEFAULT 0.
func mustSubmission(t *testing.T, db *DB, userID, problemID int64) int64 {
	t.Helper()
	res, err := db.SQL.ExecContext(hctx,
		`INSERT INTO submissions (user_id, problem_id, contest_id, language, source, status, verdict,
		                         score, total_score, created_at, judged_at)
		 VALUES (?, ?, 0, 'cpp', 'int main(){}', 'done', 'WA', 0, 0, 1, 1)`, userID, problemID)
	if err != nil {
		t.Fatalf("insert submission: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("submission id: %v", err)
	}
	return id
}

func TestTicketCreateAndRead(t *testing.T) {
	db := openTestDB(t)
	author := mustUser(t, db, "tk_author")
	other := mustUser(t, db, "tk_other")
	p := mustProblem(t, db, "TK1", "哪道题判错了")
	sub := mustSubmission(t, db, author.ID, p.ID)

	tk, err := db.CreateTicket(hctx, &Ticket{
		Kind: TicketAppeal, Title: "  数据弱  ", Body: "第 3 组数据我认为不对。",
		AuthorID: author.ID, ProblemID: p.ID, SubmissionID: sub,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if tk.Status != TicketOpen || tk.ID == 0 {
		t.Errorf("a new ticket = %+v, want open with an id", tk)
	}
	got, err := db.FindTicket(hctx, tk.ID)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got.Title != "数据弱" {
		t.Errorf("title = %q, want trimmed", got.Title)
	}
	if got.ProblemPID != "TK1" || got.SubmissionID != sub || got.Author != "tk_author" {
		t.Errorf("the links or the author did not come back: %+v", got)
	}
	if got.HandlerID != 0 || got.Handler != "" {
		t.Errorf("a fresh ticket should be unassigned: %+v", got)
	}
	if TicketKindLabel(TicketAppeal) != "申诉" || TicketStatusLabel(TicketOpen) != "待处理" {
		t.Error("labels drifted")
	}
	if TicketStatusClass(TicketOpen) != "warn" || TicketStatusClass(TicketDone) != "ok" {
		t.Error("status classes drifted")
	}
	// 别人列不出我的工单：author 这一道过滤是在 SQL 里的，不是靠页面自觉。
	mine, err := db.ListTickets(hctx, TicketFilter{AuthorID: author.ID})
	if err != nil || len(mine) != 1 {
		t.Fatalf("mine = %d (%v)", len(mine), err)
	}
	if theirs, err := db.ListTickets(hctx, TicketFilter{AuthorID: other.ID}); err != nil || len(theirs) != 0 {
		t.Errorf("someone else's list = %d rows (%v)", len(theirs), err)
	}
}

func TestTicketRefusesBadInput(t *testing.T) {
	db := openTestDB(t)
	author := mustUser(t, db, "tk_bad")

	if _, err := db.CreateTicket(hctx, &Ticket{Kind: "spam", Title: "x", AuthorID: author.ID}); err == nil ||
		!strings.Contains(err.Error(), "不认识的工单类型") {
		t.Errorf("bad kind = %v", err)
	}
	if _, err := db.CreateTicket(hctx, &Ticket{Kind: TicketOther, Title: "   ", AuthorID: author.ID}); err == nil ||
		!strings.Contains(err.Error(), "标题不能是空的") {
		t.Errorf("empty title = %v", err)
	}
	if _, err := db.CreateTicket(hctx, &Ticket{Kind: TicketOther, Title: "x", AuthorID: 0}); err == nil ||
		!strings.Contains(err.Error(), "工单要有作者") {
		t.Errorf("no author = %v", err)
	}
	// 挂一个不存在的提交：外键挡住，工单不会带着假引用进去。
	if _, err := db.CreateTicket(hctx, &Ticket{
		Kind: TicketOther, Title: "x", AuthorID: author.ID, SubmissionID: 9999,
	}); err == nil {
		t.Error("a ticket pointing at a missing submission was accepted")
	}
	tk := mustTicket(t, db, TicketBug, "状态", author.ID)
	if _, err := db.SetTicketStatus(hctx, tk.ID, "resolved", author.ID, ""); err == nil ||
		!strings.Contains(err.Error(), "不认识的工单状态") {
		t.Errorf("bad status = %v", err)
	}
	if _, err := db.SetTicketStatus(hctx, 9999, TicketDone, author.ID, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("status on a missing ticket = %v", err)
	}
}

func TestTicketThreadAndStatusFlow(t *testing.T) {
	db := openTestDB(t)
	author := mustUser(t, db, "tk_t_author")
	staff := mustUser(t, db, "tk_t_staff")
	tk := mustTicket(t, db, TicketAppeal, "复核", author.ID)

	if _, err := db.AddTicketReply(hctx, &TicketReply{TicketID: tk.ID, AuthorID: author.ID, Body: "补充一句"}); err != nil {
		t.Fatalf("reply: %v", err)
	}
	// 空的两种都不行：人写的话要有正文，系统流水要有 event。
	if _, err := db.AddTicketReply(hctx, &TicketReply{TicketID: tk.ID, AuthorID: author.ID, Body: "  "}); err == nil {
		t.Error("an empty reply was accepted")
	}
	if _, err := db.AddTicketReply(hctx, &TicketReply{TicketID: tk.ID, AuthorID: author.ID, Event: "nope"}); err == nil {
		t.Error("an unknown event row was accepted")
	}
	// 挂在不存在的工单上：外键挡。
	if _, err := db.AddTicketReply(hctx, &TicketReply{TicketID: 9999, AuthorID: author.ID, Body: "x"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("reply on a missing ticket = %v", err)
	}

	moved, err := db.SetTicketStatus(hctx, tk.ID, TicketDoing, staff.ID, "我来看看")
	if err != nil {
		t.Fatalf("set status: %v", err)
	}
	if moved.Status != TicketDoing || moved.HandlerID != staff.ID || moved.Handler != "tk_t_staff" {
		t.Errorf("after the move = %+v", moved)
	}

	thread, err := db.ListTicketReplies(hctx, tk.ID)
	if err != nil {
		t.Fatalf("thread: %v", err)
	}
	if len(thread) != 2 {
		t.Fatalf("thread = %d rows, want 2: %+v", len(thread), thread)
	}
	if thread[0].Body != "补充一句" || thread[0].Event != "" {
		t.Errorf("the human reply came out wrong: %+v", thread[0])
	}
	if thread[1].Event != "status" || !strings.Contains(thread[1].Body, "处理中") ||
		!strings.Contains(thread[1].Body, "我来看看") {
		t.Errorf("the status move should read as a line of its own: %+v", thread[1])
	}

	// 未处理队列的计数跟着状态走。
	if n, err := db.CountOpenTickets(hctx); err != nil || n != 1 {
		t.Errorf("open count = %d (%v), want 1", n, err)
	}
	if _, err := db.SetTicketStatus(hctx, tk.ID, TicketDone, staff.ID, ""); err != nil {
		t.Fatalf("close: %v", err)
	}
	if n, err := db.CountOpenTickets(hctx); err != nil || n != 0 {
		t.Errorf("open count after done = %d (%v), want 0", n, err)
	}
	if list, err := db.ListTickets(hctx, TicketFilter{OpenOnly: true}); err != nil || len(list) != 0 {
		t.Errorf("the open queue still has it: %d (%v)", len(list), err)
	}
	// 回复数在列表那一趟查询里就带回来了，页面不用一行一次查询。
	if list, err := db.ListTickets(hctx, TicketFilter{}); err != nil || len(list) != 1 || list[0].Replies != 3 {
		t.Errorf("list row reply count = %+v (%v)", list, err)
	}
}

func TestTicketSurvivesItsProblemAndDiesWithItsAuthor(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	author := mustUser(t, db, "tk_keep")
	p := mustProblem(t, db, "TK2", "会被删掉的题")
	tk, err := db.CreateTicket(ctx, &Ticket{Kind: TicketBug, Title: "题有问题", AuthorID: author.ID, ProblemID: p.ID})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.AddTicketReply(ctx, &TicketReply{TicketID: tk.ID, AuthorID: author.ID, Body: "一句"}); err != nil {
		t.Fatalf("reply: %v", err)
	}
	if _, err := db.SQL.ExecContext(ctx, `DELETE FROM problems WHERE id = ?`, p.ID); err != nil {
		t.Fatalf("delete problem: %v", err)
	}
	got, err := db.FindTicket(ctx, tk.ID)
	if err != nil {
		t.Fatalf("the ticket went with its problem: %v", err)
	}
	if got.ProblemID != 0 || got.ProblemPID != "" {
		t.Errorf("the dead problem link should be cleared, not printed: %+v", got)
	}
	// 作者被删时工单跟着没：这不是历史，这是别人的隐私。
	if _, err := db.SQL.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, author.ID); err != nil {
		t.Fatalf("delete author: %v", err)
	}
	if _, err := db.FindTicket(ctx, tk.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("ticket after the author is gone = %v", err)
	}
}

func TestTicketPagingIsTheSortKey(t *testing.T) {
	db := openTestDB(t)
	author := mustUser(t, db, "tk_page")
	for i := 0; i < 5; i++ {
		mustTicket(t, db, TicketOther, "第"+string(rune('A'+i))+"单", author.ID)
	}
	first, err := db.ListTickets(hctx, TicketFilter{AuthorID: author.ID, Limit: 2})
	if err != nil || len(first) != 2 {
		t.Fatalf("first page = %d (%v)", len(first), err)
	}
	second, err := db.ListTickets(hctx, TicketFilter{AuthorID: author.ID, Limit: 2, After: first[1].ID})
	if err != nil || len(second) != 2 {
		t.Fatalf("second page = %d (%v)", len(second), err)
	}
	if second[0].ID >= first[1].ID {
		t.Errorf("the cursor did not move backwards: %d then %d", first[1].ID, second[0].ID)
	}
	if first[0].ID <= first[1].ID {
		t.Errorf("newest should come first: %+v", first)
	}
}

func TestTicketDeleteTakesTheThread(t *testing.T) {
	db := openTestDB(t)
	author := mustUser(t, db, "tk_del")
	tk := mustTicket(t, db, TicketReport, "举报某人", author.ID)
	if _, err := db.AddTicketReply(hctx, &TicketReply{TicketID: tk.ID, AuthorID: author.ID, Body: "细节"}); err != nil {
		t.Fatalf("reply: %v", err)
	}
	if ok, err := db.DeleteTicket(hctx, tk.ID); err != nil || !ok {
		t.Fatalf("delete = %v (%v)", ok, err)
	}
	var n int
	if err := db.SQL.QueryRowContext(hctx, `SELECT COUNT(*) FROM ticket_replies WHERE ticket_id = ?`, tk.ID).Scan(&n); err != nil || n != 0 {
		t.Errorf("orphan replies = %d (%v)", n, err)
	}
	if ok, _ := db.DeleteTicket(hctx, tk.ID); ok {
		t.Error("deleting twice reported success")
	}
}
