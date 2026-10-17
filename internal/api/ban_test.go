package api

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// 三项可以单独收回的能力。有人刷动态、灌水、或者拿 AI 在比赛里一条条试，正确的处置
// 是堵住这一扇门，而不是封掉整个账号：历史成绩、私信、题目页都还得能用。
// 禁止落在 user_permissions 的一条"收回"上，恢复默认就是把它清掉。

func TestFeedPostCanBeBanned(t *testing.T) {
	h := newHarness(t)
	student := h.author("kid1")

	if code, body := h.postAs(student, "/feed", url.Values{"body": {"今天调了一道题"}}); code != http.StatusOK ||
		!strings.Contains(body, "今天调了一道题") {
		t.Fatalf("发动态（默认该能用）= %d\n%s", code, short(body))
	}
	h.grant("kid1", permFeedPost, false)

	code, body := h.postAs(student, "/feed", url.Values{"body": {"还要发"}})
	if code != http.StatusForbidden || !strings.Contains(body, "你被禁止发动态") {
		t.Fatalf("被禁止之后 = %d\n%s", code, short(body))
	}
	// 禁止的是"发"：看别人的动态照旧。
	if code, page := h.getAs(student, "/feed"); code != http.StatusOK ||
		!strings.Contains(page, "今天调了一道题") {
		t.Fatalf("被禁止的人读时间线 = %d", code)
	}
	// 页面上不该再留一个只会 403 的输入框。
	if _, page := h.getAs(student, "/feed"); strings.Contains(page, `name="body"`) ||
		!strings.Contains(page, "你被禁止发动态") {
		t.Errorf("动态页没说明原因，或者还留着输入框：\n%s", short(page))
	}

	h.grant("kid1", permFeedPost, true)
	if code, body := h.postAs(student, "/feed", url.Values{"body": {"恢复之后又能发了"}}); code != http.StatusOK {
		t.Fatalf("收回之后 = %d\n%s", code, short(body))
	}
}

func TestTopicPostCanBeBanned(t *testing.T) {
	h := newHarness(t)
	kid := h.author("kid2")

	if code, body := h.createTopic(kid, url.Values{"title": {"正常贴"}, "body": {"正文"}}); code != http.StatusOK {
		t.Fatalf("发帖（默认该能用）= %d\n%s", code, short(body))
	}
	h.grant("kid2", permTopicPost, false)

	if code, body := h.createTopic(kid, url.Values{"title": {"灌水"}, "body": {"再灌"}}); code != http.StatusForbidden ||
		!strings.Contains(body, "你被禁止发帖") {
		t.Fatalf("被禁止之后 = %d\n%s", code, short(body))
	}
	if _, page := h.getAs(kid, "/topics/new"); !strings.Contains(page, "你被禁止发帖") {
		t.Errorf("发帖页没说明原因：\n%s", short(page))
	}
	// 读帖子这件事不受影响，被禁止的人也得能看到别人怎么说。
	if code, _ := h.getAs(kid, "/topics"); code != http.StatusOK {
		t.Errorf("被禁止的人读社区贴列表 = %d", code)
	}
}

func TestContestJoinCanBeBanned(t *testing.T) {
	h := newHarness(t)
	host := h.author("host3", permContestCreate)
	h.readyProblem("P7009")
	if _, body := h.createContest(host, contestValues("C7009", "禁赛测试")); !strings.Contains(body, "比赛已建好") {
		t.Fatalf("建赛:\n%s", short(body))
	}
	h.mountContestProblems(host, "C7009", "P7009")
	cid := h.contestID("C7009")

	kid := h.author("kid3")
	if code, body := h.joinContest(kid, "C7009", ""); code != http.StatusOK || !strings.Contains(body, "已加入") {
		t.Fatalf("加入比赛（默认该能用）= %d\n%s", code, short(body))
	}
	h.grant("kid3", permContestJoin, false)

	// 人已经在名单里也交不了：只挡住"加入"这一扇门是不够的。
	if code, body := h.submitContest(kid, "P7009", cid, "int main(){return 0;}"); code != http.StatusOK ||
		!strings.Contains(body, "你被禁止参加比赛") {
		t.Fatalf("赛内提交 = %d\n%s", code, short(body))
	}
	if code, body := h.joinContest(kid, "C7009", ""); code != http.StatusOK ||
		!strings.Contains(body, "你被禁止参加比赛") {
		t.Fatalf("重新加入 = %d\n%s", code, short(body))
	}
	// 练习提交照旧：禁止的是比赛，不是做题。
	if code, body := h.submitCode(kid, "P7009", "cpp", "int main(){return 0;}"); code != http.StatusOK {
		t.Fatalf("练习提交 = %d\n%s", code, short(body))
	}
	// 比赛主页仍然读得到。
	if code, _ := h.getAs(kid, "/contests/C7009"); code != http.StatusOK {
		t.Errorf("被禁止的人看比赛主页 = %d", code)
	}
}

// 站主不该被这三条收回——那本来就是 M25a 的免疫，这里只确认这三个键在权限表里
// 是"人人可用"而不是管理侧：否则它们一出现就会把普通人全禁了。
func TestBanCapabilitiesArePublicByDefault(t *testing.T) {
	h := newHarness(t)
	h.author("kid4")
	kid := h.user("kid4")

	perms, err := h.db.EffectivePermissions(ctx(), kid.ID, kid.Role)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{permFeedPost, permTopicPost, permContestJoin} {
		if !perms[key] {
			t.Errorf("%s 默认就该有，普通人拿不到", key)
		}
	}
	listed, err := h.db.ListPermissions(ctx())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range listed {
		switch p.Key {
		case permFeedPost, permTopicPost, permContestJoin:
			if p.Staff {
				t.Errorf("%s 不该是管理侧权限", p.Key)
			}
		}
	}
}
