package api

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/liangbinchen2013/fast-oj/internal/store"
)

// fileTicket posts the submit form the way a browser does and returns the id from
// the 303 target, because that redirect is the only place the new id is announced.
func (h *harness) fileTicket(s session, v url.Values) (int64, int, string) {
	h.t.Helper()
	if v.Get("kind") == "" {
		v.Set("kind", store.TicketAppeal)
	}
	if v.Get("title") == "" {
		v.Set("title", "判题申诉")
	}
	code, body, head := h.submitForm(s, "/tickets", v)
	if code != http.StatusSeeOther {
		return 0, code, body
	}
	u, err := url.Parse(head.Get("Location"))
	if err != nil || !strings.HasPrefix(u.Path, "/tickets/") {
		h.t.Fatalf("the create redirect is not a ticket page: %q", head.Get("Location"))
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(u.Path, "/tickets/"), 10, 64)
	if err != nil {
		h.t.Fatalf("the redirect path %q has no ticket id", u.Path)
	}
	return id, code, body
}

// submitForm posts as this browser — CSRF attached, the 303 not followed — so the
// Location header is still there to read the new id off.
func (h *harness) submitForm(s session, path string, v url.Values) (int, string, http.Header) {
	h.t.Helper()
	return h.postForm(path, v, s.cookie, s.csrf)
}

func TestTicketNeedsLogin(t *testing.T) {
	h := newHarness(t)
	if code, _, head := h.postNoRedirect("/tickets", url.Values{"title": {"匿名"}}, ""); code != http.StatusSeeOther ||
		!strings.Contains(head.Get("Location"), "/login") {
		t.Errorf("anonymous create = %d -> %q, want a login redirect", code, head.Get("Location"))
	}
	// 客户端会跟着 303 走到登录页，所以断言"最后落在登录页"而不是状态码。
	for _, path := range []string{"/tickets", "/tickets/new"} {
		code, body := h.htmlGet(path, "")
		if code != http.StatusOK || !strings.Contains(body, "登录") || strings.Contains(body, "提交工单") {
			t.Errorf("anonymous %s = %d, and the page is not the login form:\n%s", path, code, short(body))
		}
	}
}

func TestTicketIsPrivateToItsAuthorAndTheHandlers(t *testing.T) {
	h := newHarness(t)
	user := h.signIn("tk_user")
	other := h.signIn("tk_other")
	staff := h.signIn("tk_staff", permTicketHandle)
	h.readyProblem("P8100")

	id, code, body := h.fileTicket(user, url.Values{"problem": {"P8100"}, "body": {"第 3 组数据我认为不对"}})
	if code != http.StatusSeeOther {
		t.Fatalf("create = %d:\n%s", code, body)
	}
	path := fmt.Sprintf("/tickets/%d", id)

	page := h.getPage(user, path)
	for _, want := range []string{"判题申诉", "待处理", "P8100", "tk_user"} {
		if !strings.Contains(page, want) {
			t.Errorf("the author's view is missing %q:\n%s", want, page)
		}
	}
	// 陌生人读到的是 404 而不是 403：工单的存在本身就是不该泄漏的信息。
	if code, _ := h.getAs(other, path); code != http.StatusNotFound {
		t.Errorf("a stranger read a private ticket: %d", code)
	}
	if page := h.getPage(staff, path); !strings.Contains(page, "第 3 组数据") {
		t.Errorf("the handler could not read the ticket:\n%s", page)
	}
	if page := h.getPage(other, "/tickets"); strings.Contains(page, "判题申诉") {
		t.Errorf("someone else's list leaked a ticket:\n%s", page)
	}
	if page := h.getPage(staff, "/tickets?scope=queue"); !strings.Contains(page, "判题申诉") {
		t.Errorf("the handler's queue is missing it:\n%s", page)
	}
	// 未处理角标只给处理人算。
	if page := h.getPage(staff, "/"); !strings.Contains(page, `工单 <b class="badge bad">1</b>`) {
		t.Errorf("the handler's nav bar has no open-ticket badge:\n%s", page)
	}
	if page := h.getPage(user, "/"); strings.Contains(page, `工单 <b class="badge bad">`) {
		t.Errorf("a plain user was shown the queue size:\n%s", page)
	}
	// 普通用户的 scope=queue 会被折回自己的列表，不会变成别人的队列。
	if page := h.getPage(user, "/tickets?scope=queue"); !strings.Contains(page, "我的工单") {
		t.Errorf("a plain user got a handler scope:\n%s", page)
	}
}

func TestTicketStatusMovesAndPermissions(t *testing.T) {
	h := newHarness(t)
	user := h.signIn("tk_move")
	staff := h.signIn("tk_mstaff", permTicketHandle)
	id, _, _ := h.fileTicket(user, url.Values{"title": {"帮我看看"}, "body": {"在的"}})
	path := fmt.Sprintf("/tickets/%d", id)

	// 普通用户既不能改状态也不能删。
	if code, page := h.postAs(user, path+"/status", url.Values{"status": {store.TicketDone}}); code != http.StatusForbidden ||
		!strings.Contains(page, permTicketHandle) {
		t.Errorf("author forced the status: %d\n%s", code, page)
	}
	if code, _ := h.postAs(user, path+"/delete", url.Values{}); code != http.StatusForbidden {
		t.Errorf("author deleted a ticket: %d", code)
	}
	// 没有 csrf 的 POST 进不来（这条与 multipart 那次修复同源，值得再钉一次）。
	if code, _, _ := h.postNoRedirect(path+"/reply", url.Values{"body": {"跨站"}}, user.cookie); code != http.StatusForbidden {
		t.Errorf("reply without csrf = %d, want 403", code)
	}
	// 处理人接手：改状态、记名，并留下一条流水。
	if _, body := h.postAs(staff, path+"/status", url.Values{
		"status": {store.TicketDoing}, "note": {"我来复核"},
	}); !strings.Contains(body, "已改为") {
		t.Errorf("handler move:\n%s", body)
	}
	page := h.getPage(staff, path)
	for _, want := range []string{"处理中", "tk_mstaff", "我来复核", "状态改为「处理中」"} {
		if !strings.Contains(page, want) {
			t.Errorf("after the move the page is missing %q:\n%s", want, page)
		}
	}
	// 作者只能关自己这一张，不需要 ticket.handle。
	if _, body := h.postAs(user, path+"/status", url.Values{"status": {store.TicketClosed}}); !strings.Contains(body, "已关闭") {
		t.Errorf("author closed their own ticket:\n%s", body)
	}
	if page := h.getPage(staff, "/tickets?scope=queue"); strings.Contains(page, "帮我看看") {
		t.Errorf("a closed ticket stayed in the queue:\n%s", page)
	}
	if _, body := h.postAs(staff, path+"/delete", url.Values{}); !strings.Contains(body, "已删除工单") {
		t.Errorf("handler delete:\n%s", body)
	}
	if code, _ := h.getAs(staff, path); code != http.StatusNotFound {
		t.Errorf("ticket after delete = %d, want 404", code)
	}
}

func TestTicketRepliesAndRateLimit(t *testing.T) {
	h := newHarness(t)
	user := h.signIn("tk_rep")
	staff := h.signIn("tk_rstaff", permTicketHandle)
	id, _, _ := h.fileTicket(user, url.Values{"title": {"追问测试"}})
	path := fmt.Sprintf("/tickets/%d", id)

	if _, body := h.postAs(user, path+"/reply", url.Values{"body": {"补一句"}}); !strings.Contains(body, "已回复") {
		t.Errorf("reply:\n%s", body)
	}
	if page := h.getPage(staff, path); !strings.Contains(page, "补一句") {
		t.Errorf("the handler did not see the reply:\n%s", page)
	}
	if _, body := h.postAs(staff, path+"/reply", url.Values{"body": {"   "}}); !strings.Contains(body, "回复不能是空的") {
		t.Errorf("empty reply:\n%s", body)
	}
	// 一小时 5 张：开头那张已占了一个名额，再刷满就该撞墙，而不是悄悄收下。
	for i := 1; i < maxTicketsHour; i++ {
		if _, code, _ := h.fileTicket(user, url.Values{"title": {fmt.Sprintf("刷第%d张", i)}}); code != http.StatusSeeOther {
			t.Fatalf("ticket %d was rejected early", i+1)
		}
	}
	code, body, _ := h.submitForm(user, "/tickets", url.Values{
		"kind": {store.TicketOther}, "title": {"超额的第六张"},
	})
	if code != http.StatusTooManyRequests || !strings.Contains(body, "一小时最多") {
		t.Errorf("the 6th ticket = %d, want 429:\n%s", code, body)
	}
}

func TestTicketCannotAttachSomeoneElsesSubmission(t *testing.T) {
	h := newHarness(t)
	victim := h.signIn("tk_victim")
	thief := h.signIn("tk_thief")
	h.readyProblem("P8101")
	_, body := h.submitCode(victim, "P8101", "cpp", "int main(){return 0;}")
	sub := h.submissionID(body)
	// 挂别人的提交：拒绝，并且不说那是什么。
	if _, code, body := h.fileTicket(thief, url.Values{
		"title": {"偷挂别人的提交"}, "submission": {strconv.FormatInt(sub, 10)},
	}); code != http.StatusBadRequest || !strings.Contains(body, "不是你的提交") {
		t.Errorf("attaching someone else's submission = %d:\n%s", code, body)
	}
	// 挂自己的可以。
	if id, code, _ := h.fileTicket(victim, url.Values{
		"title": {"我的申诉"}, "submission": {strconv.FormatInt(sub, 10)},
	}); code != http.StatusSeeOther {
		t.Errorf("attaching your own submission was refused")
	} else if page := h.getPage(victim, fmt.Sprintf("/tickets/%d", id)); !strings.Contains(page, strconv.FormatInt(sub, 10)) {
		t.Errorf("the ticket does not point at the submission:\n%s", page)
	}
}
