package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func mustTopic(t *testing.T, db *DB, kind, title string, authorID, problemID int64) Topic {
	t.Helper()
	tp, err := db.CreateTopic(context.Background(), kind, title, "正文 "+title, authorID, problemID)
	if err != nil {
		t.Fatalf("create topic %q: %v", title, err)
	}
	return tp
}

func TestTopicKindsAndTheProblemLink(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	author := mustUser(t, db, "asker")
	p := mustProblem(t, db, "PT1", "A+B 到底哪里错")

	help := mustTopic(t, db, TopicHelp, "求助", author.ID, p.ID)
	if help.ProblemID != p.ID || help.ProblemPID != "PT1" || help.ProblemTitle != p.Title {
		t.Errorf("the help topic lost its problem: %+v", help)
	}
	if TopicKindLabel(help.Kind) != "求助" || TopicKindLabel(TopicCommunity) != "社区" {
		t.Errorf("kind labels = %q / %q", TopicKindLabel(help.Kind), TopicKindLabel(TopicCommunity))
	}
	// A 社区贴 has no problem, and a 求助贴 must have one: the table says so.
	if _, err := db.CreateTopic(ctx, TopicCommunity, "闲聊", "", author.ID, p.ID); err == nil ||
		!strings.Contains(err.Error(), "社区贴不挂题目") {
		t.Errorf("community topic with a problem = %v", err)
	}
	if _, err := db.CreateTopic(ctx, TopicHelp, "没题的求助", "", author.ID, 0); err == nil ||
		!strings.Contains(err.Error(), "求助贴要挂在一道题上") {
		t.Errorf("help topic without a problem = %v", err)
	}
	if _, err := db.CreateTopic(ctx, "poll", "不认识的类型", "", author.ID, 0); err == nil {
		t.Error("an unknown kind slipped through")
	}
	// A problem that does not exist cannot carry a help topic either.
	if _, err := db.CreateTopic(ctx, TopicHelp, "挂空题", "", author.ID, 4242); err == nil {
		t.Error("a help topic pointed at a problem that is not there")
	}
}

func TestTopicValidation(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	author := mustUser(t, db, "writer")

	if _, err := db.CreateTopic(ctx, TopicCommunity, "   ", "正文", author.ID, 0); err == nil ||
		!strings.Contains(err.Error(), "标题不能是空的") {
		t.Errorf("blank title = %v", err)
	}
	long := strings.Repeat("字", MaxTopicTitle+1)
	if _, err := db.CreateTopic(ctx, TopicCommunity, long, "", author.ID, 0); err == nil ||
		!strings.Contains(err.Error(), "标题最长") {
		t.Errorf("over-long title = %v", err)
	}
	if _, err := db.CreateTopic(ctx, TopicCommunity, "太长", strings.Repeat("a", MaxTopicBody+1), author.ID, 0); err == nil {
		t.Error("over-long body accepted")
	}
	// Trailing whitespace is trimmed, a Chinese title is counted in characters not
	// bytes.
	tp, err := db.CreateTopic(ctx, TopicCommunity, "标题", "正文\r\n\r\n", author.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasSuffix(tp.Body, "\n") || tp.Body != "正文" {
		t.Errorf("body not trimmed: %q", tp.Body)
	}
}

func TestListTopicsFiltersAndPages(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	a := mustUser(t, db, "alice")
	b := mustUser(t, db, "bob")
	p := mustProblem(t, db, "PT2", "越界")

	for i := 0; i < 3; i++ {
		mustTopic(t, db, TopicCommunity, fmt.Sprintf("社区 %d", i), a.ID, 0)
	}
	for i := 0; i < 2; i++ {
		mustTopic(t, db, TopicHelp, fmt.Sprintf("求助 %d", i), b.ID, p.ID)
	}

	all, err := db.ListTopics(ctx, TopicFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 5 {
		t.Fatalf("all = %d", len(all))
	}
	if all[0].Title != "求助 1" {
		t.Errorf("newest first: %+v", all[0])
	}
	// The kind filter is what splits 社区贴's page from a problem's list.
	help, err := db.ListTopics(ctx, TopicFilter{Kind: TopicHelp})
	if err != nil {
		t.Fatal(err)
	}
	if len(help) != 2 || help[0].Author != "bob" {
		t.Errorf("help list = %+v", help)
	}
	onProblem, err := db.ListTopics(ctx, TopicFilter{ProblemID: p.ID})
	if err != nil || len(onProblem) != 2 {
		t.Errorf("by problem = %d %v", len(onProblem), err)
	}
	byAuthor, err := db.ListTopics(ctx, TopicFilter{AuthorID: a.ID})
	if err != nil || len(byAuthor) != 3 {
		t.Errorf("by author = %d %v", len(byAuthor), err)
	}
	found, err := db.ListTopics(ctx, TopicFilter{Search: "社区 2"})
	if err != nil || len(found) != 1 {
		t.Errorf("search = %+v %v", found, err)
	}
	// LIKE wildcards in a search term stay literal text.
	wild, err := db.ListTopics(ctx, TopicFilter{Search: "%"})
	if err != nil {
		t.Fatal(err)
	}
	if len(wild) != 0 {
		t.Errorf("a %% matched %d topics, it should match none", len(wild))
	}
	// The cursor pages without repeating or skipping.
	first, err := db.ListTopics(ctx, TopicFilter{Limit: 2})
	if err != nil || len(first) != 2 {
		t.Fatalf("first page = %d %v", len(first), err)
	}
	second, err := db.ListTopics(ctx, TopicFilter{Limit: 2, After: first[1].ID})
	if err != nil || len(second) != 2 {
		t.Fatalf("second page = %d %v", len(second), err)
	}
	if second[0].ID == first[1].ID || second[1].ID == first[0].ID {
		t.Errorf("pages overlap: %d %d | %d %d", first[0].ID, first[1].ID, second[0].ID, second[1].ID)
	}
	// A limit above the ceiling is clamped rather than obeyed.
	if big, err := db.ListTopics(ctx, TopicFilter{Limit: 9999}); err != nil || len(big) != 5 {
		t.Errorf("clamp = %d %v", len(big), err)
	}
}

func TestTopicComments(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	asker := mustUser(t, db, "asker2")
	helper := mustUser(t, db, "helper2")
	p := mustProblem(t, db, "PT3", "为什么 WA")
	tp := mustTopic(t, db, TopicHelp, "求助一题", asker.ID, p.ID)

	first, err := db.CreateTopicComment(ctx, tp.ID, helper.ID, "把你的取模写全")
	if err != nil {
		t.Fatal(err)
	}
	if first.Author != "helper2" || first.TopicID != tp.ID {
		t.Errorf("comment = %+v", first)
	}
	if _, err := db.CreateTopicComment(ctx, tp.ID, asker.ID, "**已经解决了**，谢谢"); err != nil {
		t.Fatal(err)
	}
	list, err := db.ListTopicComments(ctx, tp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Author != "helper2" {
		t.Errorf("comments read oldest first: %+v", list)
	}
	// The count rides along on the list page, in one grouped query.
	page, err := db.ListTopics(ctx, TopicFilter{ProblemID: p.ID})
	if err != nil || len(page) != 1 || page[0].Comments != 2 {
		t.Errorf("comment count = %+v %v", page, err)
	}
	got, err := db.GetTopic(ctx, tp.ID)
	if err != nil || got.Comments != 2 {
		t.Errorf("GetTopic count = %+v %v", got, err)
	}

	if _, err := db.CreateTopicComment(ctx, tp.ID, helper.ID, "   "); err == nil {
		t.Error("an empty comment was accepted")
	}
	if _, err := db.CreateTopicComment(ctx, tp.ID, helper.ID, strings.Repeat("字", MaxTopicComment+1)); err == nil {
		t.Error("an over-long comment was accepted")
	}
	// A comment cannot be attached to a topic that is not there.
	if _, err := db.CreateTopicComment(ctx, 777, helper.ID, "有人吗"); !errors.Is(err, ErrNotFound) {
		t.Errorf("orphan comment = %v", err)
	}

	// Only the author may delete their own comment; the moderator path is separate.
	if ok, err := db.DeleteTopicComment(ctx, first.ID, asker.ID); err != nil || ok {
		t.Errorf("someone else deleted a comment: %v %v", ok, err)
	}
	if ok, err := db.DeleteTopicComment(ctx, first.ID, helper.ID); err != nil || !ok {
		t.Errorf("author delete = %v %v", ok, err)
	}
	if rest, _ := db.ListTopicComments(ctx, tp.ID); len(rest) != 1 {
		t.Errorf("comment survived: %+v", rest)
	}
	if _, err := db.GetTopicComment(ctx, first.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetTopicComment after delete = %v", err)
	}
}

func TestDeleteTopicTakesItsComments(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	author := mustUser(t, db, "owner")
	other := mustUser(t, db, "passer")
	tp := mustTopic(t, db, TopicCommunity, "要删掉的帖子", author.ID, 0)
	if _, err := db.CreateTopicComment(ctx, tp.ID, other.ID, "一楼"); err != nil {
		t.Fatal(err)
	}

	if ok, err := db.DeleteTopic(ctx, tp.ID, other.ID); err != nil || ok {
		t.Errorf("a stranger deleted someone else's topic: %v %v", ok, err)
	}
	if ok, err := db.DeleteTopic(ctx, tp.ID, author.ID); err != nil || !ok {
		t.Fatalf("author delete = %v %v", ok, err)
	}
	if _, err := db.GetTopic(ctx, tp.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("topic still there: %v", err)
	}
	var n int
	if err := db.SQL.QueryRowContext(ctx, `SELECT COUNT(*) FROM topic_comments WHERE topic_id=?`, tp.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d comments outlived their topic", n)
	}
	// The moderator path does not need the author id.
	second := mustTopic(t, db, TopicCommunity, "违规帖", author.ID, 0)
	if ok, err := db.DeleteTopicAny(ctx, second.ID); err != nil || !ok {
		t.Errorf("moderator delete = %v %v", ok, err)
	}
	if ok, err := db.DeleteTopicAny(ctx, 9999); err != nil || ok {
		t.Errorf("deleting nothing reported success: %v %v", ok, err)
	}
}

func TestDeletingAProblemTakesItsHelpTopics(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	author := mustUser(t, db, "author")
	p := mustProblem(t, db, "PT4", "删题测试")
	tp := mustTopic(t, db, TopicHelp, "这题怎么做", author.ID, p.ID)
	if _, err := db.CreateTopicComment(ctx, tp.ID, author.ID, "顶"); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteProblem(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetTopic(ctx, tp.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("the topic outlived its problem: %v", err)
	}
	// 社区贴 are not attached to any problem, so they stay.
	survivor := mustTopic(t, db, TopicCommunity, "闲聊", author.ID, 0)
	if _, err := db.GetTopic(ctx, survivor.ID); err != nil {
		t.Errorf("a community topic disappeared with the problem: %v", err)
	}
}

func TestDeletingAUserTakesTheirTopics(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	author := mustUser(t, db, "leaver")
	tp := mustTopic(t, db, TopicCommunity, "我要走了", author.ID, 0)
	if _, err := db.SQL.ExecContext(ctx, `DELETE FROM users WHERE id=?`, author.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetTopic(ctx, tp.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("the topic outlived its author: %v", err)
	}
}
