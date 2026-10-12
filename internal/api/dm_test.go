package api

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/liangbinchen2013/fast-oj/internal/store"
)

func TestMessagesNeedALogin(t *testing.T) {
	h := newHarness(t)
	// The test client follows redirects, so what matters is where we ended up:
	// a login form, not an inbox.
	code, body, _ := h.html(http.MethodGet, "/messages", "", nil, "")
	if code != http.StatusOK || !strings.Contains(body, `name="identifier"`) {
		t.Fatalf("anonymous inbox did not bounce to the login page: %d", code)
	}
	if strings.Contains(body, "<h1>私信</h1>") {
		t.Error("an anonymous visitor was shown the inbox")
	}

	cookie, _ := h.login("nosend", "nosend@example.com", testPassword)
	if code, _, _ := h.postForm("/messages/target", url.Values{"body": {"hi"}}, cookie, ""); code != http.StatusForbidden {
		t.Fatalf("CSRF-less send = %d, want 403", code)
	}
}

func TestMessageThreadAndUnreadBadge(t *testing.T) {
	h := newHarness(t)
	senderCookie, senderCSRF := h.login("sender13", "sender13@example.com", testPassword)
	recvCookie, _ := h.login("reader13", "reader13@example.com", testPassword)

	code, page := h.postForm2("/messages/reader13", url.Values{"body": {"在吗？有个题想问"}}, senderCookie, senderCSRF)
	if code != http.StatusOK || !strings.Contains(page, "已发送") {
		t.Fatalf("send = %d", code)
	}
	if !strings.Contains(page, "在吗？有个题想问") {
		t.Errorf("the thread does not show the message:\n%s", page)
	}

	// The badge counts the recipient's unread, not the sender's.
	if _, nav := h.htmlWithCookie(http.MethodGet, "/", senderCookie); strings.Contains(nav, `私信 <b class="badge bad"`) {
		t.Error("the sender was credited with an unread badge")
	}
	_, nav := h.htmlWithCookie(http.MethodGet, "/", recvCookie)
	if !strings.Contains(nav, `私信 <b class="badge bad">1</b>`) {
		t.Errorf("recipient nav missing the badge:\n%s", nav)
	}

	_, inbox := h.htmlWithCookie(http.MethodGet, "/messages", recvCookie)
	for _, want := range []string{"sender13", "在吗？有个题想问", `badge bad">1<`} {
		if !strings.Contains(inbox, want) {
			t.Errorf("inbox missing %q:\n%s", want, inbox)
		}
	}

	// Opening the conversation is the read receipt.
	if code, _ := h.htmlWithCookie(http.MethodGet, "/messages/sender13", recvCookie); code != http.StatusOK {
		t.Fatalf("recipient thread = %d", code)
	}
	if _, nav := h.htmlWithCookie(http.MethodGet, "/", recvCookie); strings.Contains(nav, "badge bad") {
		t.Error("the badge survived opening the conversation")
	}

	// Text view for scripts.
	if code, text := h.get("/messages", h.userToken("reader13")); code != http.StatusOK ||
		!strings.Contains(text, "sender13") {
		t.Fatalf("text inbox = %d %q", code, text)
	}
}

func TestMessagePolicyFromTheBrowser(t *testing.T) {
	h := newHarness(t)
	cookie, csrf := h.login("quiet13", "quiet13@example.com", testPassword)
	fanCookie, fanCSRF := h.login("fanboy13", "fanboy13@example.com", testPassword)

	if code, page := h.postForm2("/settings/privacy", url.Values{"dm_policy": {"mutual"}}, cookie, csrf); code != http.StatusOK ||
		!strings.Contains(page, "已保存隐私设置") {
		t.Fatalf("privacy save = %d", code)
	}
	if code, page := h.postForm2("/settings/privacy", url.Values{"dm_policy": {"nonsense"}}, cookie, csrf); code != http.StatusBadRequest {
		t.Fatalf("unknown policy = %d %s", code, page)
	}
	if _, page := h.htmlWithCookie(http.MethodGet, "/settings", cookie); !strings.Contains(page, `value="mutual" checked`) {
		t.Error("the settings page did not keep the chosen policy")
	}

	// A stranger is refused, and the refusal says why instead of silently dropping.
	if code, page := h.postForm2("/messages/quiet13", url.Values{"body": {"加我好友"}}, fanCookie, fanCSRF); code != http.StatusOK ||
		!strings.Contains(page, "不接受这条私信") {
		t.Fatalf("stranger send = %d %s", code, page)
	}
	if _, inbox := h.htmlWithCookie(http.MethodGet, "/messages", cookie); strings.Contains(inbox, "加我好友") {
		t.Error("a refused message still reached the inbox")
	}

	// quiet follows back, but that is still one-way for the policy: mutual means an
	// edge in each direction, so fanboy has to follow quiet too.
	if code, _ := h.postForm2("/u/fanboy13/follow", url.Values{}, cookie, csrf); code != http.StatusOK {
		t.Fatalf("follow back = %d", code)
	}
	if code, page := h.postForm2("/messages/quiet13", url.Values{"body": {"现在总行了吧"}}, fanCookie, fanCSRF); code != http.StatusOK ||
		!strings.Contains(page, "不接受这条私信") {
		t.Fatalf("one-way follow was accepted under mutual: %d %s", code, page)
	}
	if code, _ := h.postForm2("/u/quiet13/follow", url.Values{}, fanCookie, fanCSRF); code != http.StatusOK {
		t.Fatalf("fanboy follow = %d", code)
	}
	if code, page := h.postForm2("/messages/quiet13", url.Values{"body": {"现在可以了吧"}}, fanCookie, fanCSRF); code != http.StatusOK ||
		!strings.Contains(page, "已发送") {
		t.Fatalf("mutual send = %d %s", code, page)
	}

	// Off refuses everyone, even a mutual friend.
	if code, _ := h.postForm2("/settings/privacy", url.Values{"dm_policy": {"off"}}, cookie, csrf); code != http.StatusOK {
		t.Fatalf("close dm = %d", code)
	}
	if _, page := h.postForm2("/messages/quiet13", url.Values{"body": {"还在吗"}}, fanCookie, fanCSRF); !strings.Contains(page, "不接受这条私信") {
		t.Error("messages got through a closed inbox")
	}
	// Closing an inbox refuses what comes in; it does not stop the owner from
	// writing to someone else.
	if _, page := h.htmlWithCookie(http.MethodGet, "/messages/fanboy13", cookie); !strings.Contains(page, `name="body"`) {
		t.Error("the owner of a closed inbox could not write out")
	}
}

func TestMessageValidationAndSelf(t *testing.T) {
	h := newHarness(t)
	cookie, csrf := h.login("solo13", "solo13@example.com", testPassword)

	if _, page := h.postForm2("/messages/solo13", url.Values{"body": {"  "}}, cookie, csrf); !strings.Contains(page, "不能给自己发私信") {
		t.Error("self message was not refused")
	}
	if code, _ := h.postForm2("/messages/nobody13", url.Values{"body": {"hi"}}, cookie, csrf); code != http.StatusNotFound {
		t.Errorf("send to a missing user = %d, want 404", code)
	}
	h.login("penpal13", "penpal13@example.com", testPassword)
	if _, page := h.postForm2("/messages/penpal13", url.Values{"body": {strings.Repeat("字", store.MaxDMBody+1)}}, cookie, csrf); !strings.Contains(page, "太长") {
		t.Error("oversized message was not reported")
	}
}

func TestMessageRateLimit(t *testing.T) {
	h := newHarness(t)
	aCookie, aCSRF := h.login("rapid13", "rapid13@example.com", testPassword)
	h.login("target13", "target13@example.com", testPassword)

	if code, page := h.postForm2("/messages/target13", url.Values{"body": {"第一条"}}, aCookie, aCSRF); code != http.StatusOK ||
		!strings.Contains(page, "已发送") {
		t.Fatalf("first message = %d %s", code, page)
	}
	// One per two seconds: the very next attempt is refused.
	if _, page := h.postForm2("/messages/target13", url.Values{"body": {"第二条"}}, aCookie, aCSRF); !strings.Contains(page, "发得太快") {
		t.Error("a second message inside the window went through")
	}
	// A refused attempt does not use up the allowance, so the next one lands once
	// the window has passed.
	time.Sleep(2200 * time.Millisecond)
	if code, page := h.postForm2("/messages/target13", url.Values{"body": {"等过两秒"}}, aCookie, aCSRF); code != http.StatusOK ||
		!strings.Contains(page, "已发送") {
		t.Fatalf("after the window = %d %s", code, page)
	}

	// A different sender is unaffected.
	otherCookie, otherCSRF := h.login("bystander13", "bystander13@example.com", testPassword)
	if code, _ := h.postForm2("/messages/target13", url.Values{"body": {"我的第一条"}}, otherCookie, otherCSRF); code != http.StatusOK {
		t.Fatalf("other sender blocked: %d", code)
	}
}
