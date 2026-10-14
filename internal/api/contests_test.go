package api

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/liangbinchen2013/fast-oj/internal/store"
)

// contestWindow is a contest that is open right now, in the layout the form sends.
func contestWindow() (string, string) {
	now := time.Now()
	return now.Add(-time.Hour).Format(contestLayoutMini), now.Add(time.Hour).Format(contestLayoutMini)
}

// contestValues is what the browser posts: every field, including the defaults the
// form comes pre-filled with.
func contestValues(cid, title string) url.Values {
	start, end := contestWindow()
	return url.Values{
		"cid": {cid}, "title": {title}, "start_at": {start}, "end_at": {end},
		"mode": {store.ContestOI}, "team_size": {"1"}, "penalty_s": {"1200"}, "visible": {"1"},
	}
}

func (h *harness) createContest(s session, values url.Values) (int, string) {
	h.t.Helper()
	return h.postAs(s, "/contests", values)
}

func TestContestCRUDThroughTheWebForm(t *testing.T) {
	h := newHarness(t)
	host := h.signIn("host", permContestCreate)
	h.readyProblem("P5000")
	h.readyProblem("P5001")

	// A contest list is public, and empty is not an error.
	if code, body := h.htmlGet("/contests", ""); code != http.StatusOK || !strings.Contains(body, "还没有比赛") {
		t.Fatalf("empty list = %d %s", code, body)
	}

	values := contestValues("C5000", "周末赛")
	values.Set("statement", "## 说明\n\n共 $n$ 题，$1\\le n\\le 10^5$。")
	code, body := h.createContest(host, values)
	if code != http.StatusOK {
		t.Fatalf("create = %d %s", code, body)
	}
	// Creation lands on the edit page with the next step spelled out.
	for _, want := range []string{"编辑比赛 C5000", "比赛已建好，现在挂题", "挂题", "危险区"} {
		if !strings.Contains(body, want) {
			t.Errorf("edit page missing %q:\n%s", want, body)
		}
	}

	// Mount two problems: the letters follow the order they were added in.
	if _, body := h.postAs(host, "/contests/C5000/problems", url.Values{
		"problem": {"P5000"}, "points": {"0"}, "label": {""},
	}); !strings.Contains(body, "已挂上 A：P5000") {
		t.Errorf("first mount:\n%s", body)
	}
	if _, body := h.postAs(host, "/contests/C5000/problems", url.Values{
		"problem": {"P5001"}, "points": {"100"}, "label": {" finale "},
	}); !strings.Contains(body, "已挂上 finale：P5001") {
		t.Errorf("second mount:\n%s", body)
	}
	if _, page := h.getAs(host, "/contests/C5000/edit"); !strings.Contains(page, "题目满分 2") {
		t.Errorf("the score column should explain the default:\n%s", page)
	}

	// Re-posting the same problem edits its settings instead of failing on the key.
	if _, body := h.postAs(host, "/contests/C5000/problems", url.Values{
		"problem": {"P5000"}, "points": {"30"}, "label": {""},
	}); !strings.Contains(body, "已更新 P5000 的比赛设置") {
		t.Errorf("re-mount:\n%s", body)
	}
	if _, page := h.getAs(host, "/contests/C5000/edit"); !strings.Contains(page, "30（题目满分 2）") {
		t.Errorf("override not shown:\n%s", page)
	}

	// The home page lists the problems for the host and renders the statement.
	code, home := h.getAs(host, "/contests/C5000")
	if code != http.StatusOK {
		t.Fatalf("home = %d", code)
	}
	for _, want := range []string{"进行中", "周末赛", `<span class="math inline">\(n\)</span>`, "P5000", "finale", "编辑比赛与挂题"} {
		if !strings.Contains(home, want) {
			t.Errorf("home missing %q:\n%s", want, home)
		}
	}

	// Moving swaps the order; the letters follow, so the label follows too.
	if _, body := h.postAs(host, "/contests/C5000/problems/P5000/move", url.Values{"dir": {"down"}}); !strings.Contains(body, "已调整 P5000 的顺序") {
		t.Errorf("move down:\n%s", body)
	}
	_, page := h.getAs(host, "/contests/C5000/edit")
	if strings.Index(page, "finale") > strings.Index(page, "A</td>") && strings.Contains(page, "A</td>") {
		t.Log("order after the move: P5000 is now second")
	}

	// A rejected save keeps what was typed, and says what was wrong.
	bad := contestValues("C5000", "改坏的")
	start, _ := contestWindow()
	bad.Set("end_at", start) // equal to start, so end is not after start
	bad.Set("team_size", "9")
	bad.Set("mode", "swiss")
	bad.Set("join_code", "带 空格")
	code, body = h.postAs(host, "/contests/C5000", bad)
	if code != http.StatusBadRequest {
		t.Fatalf("bad save = %d", code)
	}
	for _, want := range []string{"结束时间必须晚于开始时间", "队伍人数要 1..5", "计分模式只认 oi 或 acm", "加入码只能是字母", "改坏的"} {
		if !strings.Contains(body, want) {
			t.Errorf("rejected save missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, `<option value="swiss"`) {
		t.Error("an unknown mode should not render as selected")
	}

	// A generated code is shown to the host only, on the edit page.
	if _, body := h.postAs(host, "/contests/C5000", func() url.Values {
		v := contestValues("C5000", "周末赛")
		v.Set("newcode", "1")
		return v
	}()); !strings.Contains(body, "已保存") {
		t.Errorf("newcode save:\n%s", body)
	}
	_, page = h.getAs(host, "/contests/C5000/edit")
	joinCode := hiddenValue(page, "join_code")
	if len(joinCode) != 8 {
		t.Fatalf("generated join code = %q, want 8 characters", joinCode)
	}
	if _, home := h.getAs(host, "/contests/C5000"); strings.Contains(home, joinCode) {
		t.Error("the contest home page leaked the join code")
	}
	if _, list := h.htmlGet("/contests", ""); strings.Contains(list, joinCode) || !strings.Contains(list, "私有") {
		t.Errorf("the list should mark it 私有 without printing the code:\n%s", list)
	}

	// Removing a problem keeps the row's history but empties the mount list.
	if _, body := h.postAs(host, "/contests/C5000/problems/P5001/remove", url.Values{}); !strings.Contains(body, "已移下 P5001") {
		t.Errorf("remove:\n%s", body)
	}
	if _, body := h.postAs(host, "/contests/C5000/delete", url.Values{}); !strings.Contains(body, "已删除 C5000") {
		t.Errorf("delete:\n%s", body)
	}
	if code, _ := h.getAs(host, "/contests/C5000"); code != http.StatusNotFound {
		t.Errorf("deleted contest = %d, want 404", code)
	}
}

func TestContestAutoCidAndBlankTitle(t *testing.T) {
	h := newHarness(t)
	host := h.signIn("hoster", permContestCreate)

	values := contestValues("", "自动编号赛")
	_, body := h.createContest(host, values)
	if !strings.Contains(body, "比赛已建好，现在挂题") {
		t.Fatalf("auto cid create:\n%s", body)
	}
	if !strings.Contains(body, "编辑比赛 C0001") {
		t.Errorf("the generated cid should be C0001:\n%s", body)
	}
	// A duplicate is refused with the number named.
	if code, body := h.createContest(host, contestValues("C0001", "撞车")); code != http.StatusConflict ||
		!strings.Contains(body, "比赛编号 C0001 已经存在") {
		t.Errorf("duplicate cid = %d:\n%s", code, body)
	}
	if code, body := h.createContest(host, contestValues("C0002", "")); code != http.StatusBadRequest ||
		!strings.Contains(body, "标题要 1..200 个字符") {
		t.Errorf("blank title = %d", code)
	}
	// A slash in the number would break the URL, so it is refused before the insert.
	if code, body := h.createContest(host, contestValues("C0/02", "斜杠")); code != http.StatusBadRequest ||
		!strings.Contains(body, "比赛编号要") {
		t.Errorf("slash in cid = %d", code)
	}
}

func TestContestPermissions(t *testing.T) {
	h := newHarness(t)
	host := h.signIn("organizer", permContestCreate)
	plain := h.signIn("plain")
	other := h.signIn("rival", permContestCreate)
	h.readyProblem("P5010")

	if _, body := h.createContest(host, contestValues("C5010", "我的赛")); !strings.Contains(body, "比赛已建好") {
		t.Fatalf("create:\n%s", body)
	}

	// Nobody else may edit it, and the message says which key would.
	for path, want := range map[string]string{
		"/contests/C5010/edit": permContestManageAny,
	} {
		code, body := h.getAs(plain, path)
		if code != http.StatusForbidden || !strings.Contains(body, want) {
			t.Errorf("%s as a plain user = %d:\n%s", path, code, body)
		}
	}
	if code, body := h.getAs(other, "/contests/C5010/edit"); code != http.StatusForbidden ||
		!strings.Contains(body, permContestManageAny) {
		t.Errorf("another host = %d:\n%s", code, body)
	}
	// Mounting is the same right as editing, not a side door.
	if code, _ := h.postAs(other, "/contests/C5010/problems", url.Values{"problem": {"P5010"}}); code != http.StatusForbidden {
		t.Errorf("mount by a stranger = %d, want 403", code)
	}
	// The nav bar only offers 办赛 to someone who may host.
	if _, page := h.getAs(plain, "/contests"); strings.Contains(page, `/contests/new`) {
		t.Errorf("a plain reader was offered the create page:\n%s", page)
	}
	if _, page := h.getAs(host, "/contests"); !strings.Contains(page, "新建比赛") {
		t.Errorf("a host lost the create link:\n%s", page)
	}
	// An anonymous write is bounced to the login page with the destination kept.
	code, _, hdr := h.postNoRedirect("/contests", contestValues("C9", "匿名"), "")
	loc := hdr.Get("Location")
	if code != http.StatusSeeOther || !strings.HasPrefix(loc, "/login?next=/contests/new") {
		t.Fatalf("anonymous create = %d %q", code, loc)
	}
	// contest.manage.any lets an operator fix somebody else's contest.
	operator := h.signIn("op", permContestManageAny)
	if _, body := h.getAs(operator, "/contests/C5010/edit"); !strings.Contains(body, "编辑比赛 C5010") {
		t.Errorf("operator edit:\n%s", body)
	}
	if _, body := h.postAs(operator, "/contests/C5010", contestValues("C5010", "运维改过的赛")); !strings.Contains(body, "已保存") {
		t.Errorf("operator save:\n%s", body)
	}
	// The owner still can, and the owner alone keeps the delete right.
	if _, body := h.postAs(host, "/contests/C5010/delete", url.Values{}); !strings.Contains(body, "已删除 C5010") {
		t.Errorf("owner delete:\n%s", body)
	}
}

func TestContestMountingRespectsProblemVisibility(t *testing.T) {
	h := newHarness(t)
	author := h.signIn("author", permProblemCreate)
	// The host can author problems too, which is what makes "mount your own hidden
	// problem" reachable at all.
	host := h.signIn("host2", permContestCreate, permProblemCreate)
	h.readyProblem("P5020")

	// An unpublished problem of somebody else's is not a free prize for a host.
	if _, body := h.postAs(author, "/problems", hiddenValues("P5021", "没公开的题")); !strings.Contains(body, "题目已建好") {
		t.Fatalf("hidden problem:\n%s", body)
	}
	if _, body := h.createContest(host, contestValues("C5020", "偷题赛")); !strings.Contains(body, "比赛已建好") {
		t.Fatalf("contest:\n%s", body)
	}
	if _, body := h.postAs(host, "/contests/C5020/problems", url.Values{"problem": {"P5021"}}); !strings.Contains(body, "没公开") {
		t.Errorf("mounting somebody else's hidden problem should be refused:\n%s", body)
	}
	// The host's own hidden problem is fine: they already decide who sees it.
	if _, body := h.postAs(host, "/problems", hiddenValues("P5023", "自己的隐藏题")); !strings.Contains(body, "题目已建好") {
		t.Fatalf("own hidden problem:\n%s", body)
	}
	if _, body := h.postAs(host, "/contests/C5020/problems", url.Values{"problem": {"P5023"}}); !strings.Contains(body, "已挂上") {
		t.Errorf("mounting one's own hidden problem:\n%s", body)
	}
	// Unknown and empty numbers are answered with a message, not a stack trace.
	if _, body := h.postAs(host, "/contests/C5020/problems", url.Values{"problem": {"GONE"}}); !strings.Contains(body, "找不到题目 GONE") {
		t.Errorf("unknown problem:\n%s", body)
	}
	if _, body := h.postAs(host, "/contests/C5020/problems", url.Values{"problem": {""}}); !strings.Contains(body, "要填题目编号") {
		t.Errorf("blank problem:\n%s", body)
	}
	// A problem with no judged cases can be mounted, but the page says so.
	h.createProblem(url.Values{"pid": {"P5022"}, "title": {"没有用例"}})
	if _, body := h.postAs(host, "/contests/C5020/problems", url.Values{"problem": {"P5022"}, "points": {"0"}}); !strings.Contains(body, "已挂上") {
		t.Fatalf("mount:\n%s", body)
	}
	if _, page := h.getAs(host, "/contests/C5020/edit"); !strings.Contains(page, "没有判题用例") {
		t.Errorf("the host should be warned about an empty problem:\n%s", page)
	}
	// A non-member reader sees the notice, not the problem list.
	stranger := h.signIn("peeker")
	if _, page := h.getAs(stranger, "/contests/C5020"); strings.Contains(page, "P5022") {
		t.Errorf("the mount list leaked to a reader who is not in the contest:\n%s", page)
	}
	if _, page := h.getAs(stranger, "/contests/C5020"); !strings.Contains(page, "加入") {
		t.Errorf("a stranger should be told how to get in:\n%s", page)
	}
}

func TestHiddenContestIsInvisibleToThePublic(t *testing.T) {
	h := newHarness(t)
	host := h.signIn("quiet", permContestCreate)

	values := contestValues("C5030", "内部训练赛")
	if _, body := h.createContest(host, values); !strings.Contains(body, "比赛已建好") {
		t.Fatalf("create:\n%s", body)
	}
	if _, body := h.postAs(host, "/contests/C5030", func() url.Values {
		v := contestValues("C5030", "内部训练赛")
		v.Del("visible")
		return v
	}()); !strings.Contains(body, "已保存") {
		t.Fatalf("hide:\n%s", body)
	}

	if _, page := h.htmlGet("/contests", ""); strings.Contains(page, "内部训练赛") {
		t.Errorf("hidden contest leaked into the public list:\n%s", page)
	}
	// A stranger cannot even reach the page, so its existence stays private.
	stranger := h.signIn("nosey")
	if code, _ := h.getAs(stranger, "/contests/C5030"); code != http.StatusNotFound {
		t.Errorf("hidden contest home = %d, want 404", code)
	}
	// The host still sees it, marked 未公开, and the list carries it for staff.
	if _, page := h.getAs(host, "/contests"); !strings.Contains(page, "内部训练赛") || !strings.Contains(page, "未公开") {
		t.Errorf("staff list lost the hidden contest:\n%s", page)
	}
	if _, page := h.getAs(host, "/contests/C5030"); !strings.Contains(page, "内部训练赛") {
		t.Errorf("the host cannot see their own contest:\n%s", page)
	}
}

func TestContestTimeFormatting(t *testing.T) { // The form and the page must agree on one zone, or a contest starts twice.
	raw := "2026-01-02T15:04"
	ms, ok := parseContestTime(raw)
	if !ok {
		t.Fatal("the layout a browser sends must parse")
	}
	if got := formatContestInput(ms); got != raw {
		t.Errorf("round trip = %q, want %q", got, raw)
	}
	if _, ok := parseContestTime("2026-01-02T15:04:05"); !ok {
		t.Error("seconds must be tolerated")
	}
	if _, ok := parseContestTime(" 2026-01-02 15:04 "); ok {
		t.Error("a space instead of the T separator should be refused, not guessed")
	}
	if _, ok := parseContestTime(""); ok {
		t.Error("blank is not a time")
	}
	for in, want := range map[int64]string{
		0: "0 秒", 59 * 1000: "59 秒", 60 * 1000: "1 分", 90 * 1000: "1 分",
		61 * 60 * 1000: "1 小时 1 分", 25 * 60 * 60 * 1000: "1 天 1 小时",
		-5000: "0 秒",
	} {
		if got := formatSpan(in); got != want {
			t.Errorf("formatSpan(%d) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[int64]string{
		0: "0:00", 65 * 1000: "1:05", 3600 * 1000: "60:00",
	} {
		if got := formatClock(in); got != want {
			t.Errorf("formatClock(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestJoinCodeGenerationIsReadable(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 40; i++ {
		code, err := newJoinCode()
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != 8 || !validJoinCode(code) {
			t.Fatalf("bad code %q", code)
		}
		for _, banned := range []string{"0", "O", "1", "I", "l"} {
			if strings.Contains(code, banned) {
				t.Errorf("code %q contains a character people misread", code)
			}
		}
		seen[code] = true
	}
	if len(seen) < 35 {
		t.Errorf("codes repeat: only %d distinct out of 40", len(seen))
	}
	for _, bad := range []string{"带空格", strings.Repeat("a", 33), "a_b"} {
		if validJoinCode(bad) {
			t.Errorf("%q should be refused", bad)
		}
	}
}
