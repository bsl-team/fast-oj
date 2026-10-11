package api

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestCheckInFromTheHomePage(t *testing.T) {
	h := newHarness(t)

	if code, _, hdr := h.postNoRedirect("/checkin", url.Values{}, ""); code != http.StatusSeeOther ||
		!strings.Contains(hdr.Get("Location"), "/login?next=/") {
		t.Fatalf("anonymous check-in = %d %q", code, hdr.Get("Location"))
	}

	cookie, csrf := h.login("puncher22", "puncher22@example.com", testPassword)
	if code, _, _ := h.postForm("/checkin", url.Values{}, cookie, ""); code != http.StatusForbidden {
		t.Fatalf("CSRF-less check-in = %d, want 403", code)
	}

	// The home page offers the button and reports the streak as it grows.
	_, home := h.htmlWithCookie(http.MethodGet, "/", cookie)
	if !strings.Contains(home, `action="/checkin"`) || !strings.Contains(home, "打卡</input>") &&
		!strings.Contains(home, "value=\"打卡\"") {
		t.Errorf("home missing the check-in button:\n%s", home)
	}

	code, page := h.postForm2("/checkin", url.Values{}, cookie, csrf)
	if code != http.StatusOK || !strings.Contains(page, "您已连续打卡 1 天") {
		t.Fatalf("first check-in = %d", code)
	}
	if !strings.Contains(page, "今天已打卡") {
		t.Error("the button did not switch to the already-done label")
	}
	if !strings.Contains(page, "连续 <b>1</b> 天") {
		t.Error("the home widget missing the streak")
	}

	// A second click the same day adds nothing and says so.
	if _, page := h.postForm2("/checkin", url.Values{}, cookie, csrf); !strings.Contains(page, "今天已经打过卡了，连续 1 天") {
		t.Errorf("repeat check-in message missing:\n%s", page)
	}
	if _, page := h.htmlGet("/u/puncher22", ""); !strings.Contains(page, "连续打卡 <b>1</b> 天") {
		t.Error("the profile did not show the streak")
	}

	// An anonymous visitor sees no button and no one else's streak widget.
	if _, anon := h.htmlGet("/", ""); strings.Contains(anon, `action="/checkin"`) {
		t.Error("anonymous visitor was offered a check-in button")
	}
}
