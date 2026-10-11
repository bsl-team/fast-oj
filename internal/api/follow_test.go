package api

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestFollowFromTheProfile(t *testing.T) {
	h := newHarness(t)
	fanCookie, fanCSRF := h.login("fan12", "fan12@example.com", testPassword)
	starCookie, starCSRF := h.login("star12", "star12@example.com", testPassword)

	// An anonymous toggle is a login prompt that returns to the same profile.
	if code, _, hdr := h.postNoRedirect("/u/star12/follow", url.Values{}, ""); code != http.StatusSeeOther ||
		!strings.Contains(hdr.Get("Location"), "/login?next=%2Fu%2Fstar12") {
		t.Fatalf("anonymous follow = %d %q", code, hdr.Get("Location"))
	}
	// A cookie-authenticated toggle still needs its CSRF token.
	if code, _, _ := h.postForm("/u/star12/follow", url.Values{}, fanCookie, ""); code != http.StatusForbidden {
		t.Fatalf("CSRF-less follow = %d, want 403", code)
	}

	code, page := h.postForm2("/u/star12/follow", url.Values{}, fanCookie, fanCSRF)
	if code != http.StatusOK || !strings.Contains(page, "已关注") {
		t.Fatalf("follow = %d", code)
	}
	// Repeating it is not an error, and the counts must not double.
	if code, _ := h.postForm2("/u/star12/follow", url.Values{}, fanCookie, fanCSRF); code != http.StatusOK {
		t.Fatalf("repeat follow = %d", code)
	}

	// The star sees their own count; the fan sees the button. Nobody gets a
	// toggle on their own profile.
	_, starPage := h.htmlWithCookie(http.MethodGet, "/u/star12", starCookie)
	if !strings.Contains(starPage, "粉丝 <b>1</b>") {
		t.Errorf("star's profile missing the follower count:\n%s", starPage)
	}
	if strings.Contains(starPage, "unfollow") {
		t.Error("the profile offered a toggle to its own owner")
	}
	_, fanView := h.htmlWithCookie(http.MethodGet, "/u/star12", fanCookie)
	if !strings.Contains(fanView, "取消关注") {
		t.Errorf("the follower's view missing the unfollow button:\n%s", fanView)
	}
	_, anon := h.htmlGet("/u/star12", "")
	if !strings.Contains(anon, "粉丝 <b>1</b>") {
		t.Error("anonymous visitor lost the follow counts")
	}
	if strings.Contains(anon, "/u/star12/unfollow") {
		t.Error("anonymous visitor was offered a toggle")
	}

	_, list := h.htmlGet("/u/star12/followers", "")
	if !strings.Contains(list, "/u/fan12") || !strings.Contains(list, "粉丝") {
		t.Errorf("followers list missing the fan:\n%s", list)
	}
	_, following := h.htmlGet("/u/fan12/following", "")
	if !strings.Contains(following, "/u/star12") {
		t.Error("following list missing the star")
	}
	if code, text := h.get("/u/star12/followers", ""); code != http.StatusOK ||
		!strings.Contains(text, "fan12") || !strings.Contains(text, "shown=1") {
		t.Errorf("text followers = %d %q", code, text)
	}

	// Mutual only after the second edge, and it shows on both sides.
	if code, _ := h.postForm2("/u/fan12/follow", url.Values{}, starCookie, starCSRF); code != http.StatusOK {
		t.Fatalf("star follow = %d", code)
	}
	if _, page := h.htmlWithCookie(http.MethodGet, "/u/star12", fanCookie); !strings.Contains(page, "互相关注") {
		t.Error("mutual badge missing for the follower")
	}

	if code, page := h.postForm2("/u/star12/unfollow", url.Values{}, fanCookie, fanCSRF); code != http.StatusOK ||
		!strings.Contains(page, "已取消关注") {
		t.Fatalf("unfollow = %d", code)
	}
	if _, page := h.htmlWithCookie(http.MethodGet, "/u/star12", fanCookie); strings.Contains(page, "取消关注") {
		t.Error("profile still offered to unfollow after the unfollow")
	}
	// Unfollowing again is a no-op the page says so about, not an error.
	if code, page := h.postForm2("/u/star12/unfollow", url.Values{}, fanCookie, fanCSRF); code != http.StatusOK ||
		!strings.Contains(page, "本来就没有关注") {
		t.Errorf("second unfollow = %d", code)
	}
}

func TestFollowRefusesSelfAndStrangers(t *testing.T) {
	h := newHarness(t)
	cookie, csrf := h.login("solo12", "solo12@example.com", testPassword)

	if code, page := h.postForm2("/u/solo12/follow", url.Values{}, cookie, csrf); code != http.StatusOK ||
		!strings.Contains(page, "不能关注自己") {
		t.Fatalf("self follow = %d", code)
	}
	if code, _, _ := h.postForm("/u/nobody-here/follow", url.Values{}, cookie, csrf); code != http.StatusNotFound {
		t.Fatalf("follow a missing user = %d, want 404", code)
	}
	if code, _ := h.htmlGet("/u/nobody-here/followers", ""); code != http.StatusNotFound {
		t.Fatalf("missing user's list = %d, want 404", code)
	}
}
