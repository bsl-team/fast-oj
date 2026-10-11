package api

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/liangbinchen2013/fast-oj/internal/store"
)

func (h *harness) user(handle string) store.User {
	h.t.Helper()
	u, err := h.db.GetUserByHandle(ctx(), handle)
	if err != nil {
		h.t.Fatal(err)
	}
	return u
}

func (h *harness) grant(handle, key string, allowed bool) {
	h.t.Helper()
	u := h.user(handle)
	if err := h.db.SetUserPermission(ctx(), u.ID, key, allowed); err != nil {
		h.t.Fatal(err)
	}
}

// author registers a user with the given capabilities and returns a bearer token.
func (h *harness) author(handle string, perms ...string) string {
	h.t.Helper()
	tok := h.userToken(handle)
	for _, p := range perms {
		h.grant(handle, p, true)
	}
	return tok
}

func TestAuthoringNeedsTheCreateRight(t *testing.T) {
	h := newHarness(t)
	plain := h.userToken("nobody-yet")

	code, body := h.json(http.MethodPost, "/problems", plain, []byte(`{"pid":"P6000","title":"想出题"}`))
	if code != http.StatusForbidden {
		t.Fatalf("unprivileged create = %d %q", code, body)
	}
	if !strings.Contains(body, permProblemCreate) {
		t.Errorf("403 should name the missing capability: %q", body)
	}

	writer := h.author("writer", permProblemCreate)
	code, body = h.json(http.MethodPost, "/problems", writer, []byte(`{"pid":"P6000","title":"我的题"}`))
	if code != http.StatusCreated {
		t.Fatalf("granted create = %d %q", code, body)
	}
	p, err := h.db.FindProblem(ctx(), "P6000")
	if err != nil {
		t.Fatal(err)
	}
	if p.OwnerID != h.user("writer").ID {
		t.Fatalf("owner not recorded: %+v", p)
	}
}

func TestOwnershipScopesEditing(t *testing.T) {
	h := newHarness(t)
	alice := h.author("alice2", permProblemCreate)
	bob := h.author("bob2", permProblemCreate)
	h.json(http.MethodPost, "/problems", alice, []byte(`{"pid":"P6001","title":"爱丽丝的题"}`))

	// Same right, different owner: still refused.
	if code, body := h.json(http.MethodPut, "/problems/P6001", bob, []byte(`{"title":"抢过来"}`)); code != http.StatusForbidden {
		t.Fatalf("non-owner update = %d %q", code, body)
	} else if !strings.Contains(body, permProblemEditAny) {
		t.Errorf("message should name the way out: %q", body)
	}
	if code, _ := h.do(http.MethodDelete, "/problems/P6001", bob, nil, ""); code != http.StatusForbidden {
		t.Fatalf("non-owner delete = %d", code)
	}
	if code, body := h.json(http.MethodPut, "/problems/P6001", alice, []byte(`{"title":"改自己的"}`)); code != http.StatusOK {
		t.Fatalf("owner update = %d %q", code, body)
	}

	// The global right is exactly what unlocks someone else's problem.
	h.grant("bob2", permProblemEditAny, true)
	if code, body := h.json(http.MethodPut, "/problems/P6001", bob, []byte(`{"title":"管理员代改"}`)); code != http.StatusOK {
		t.Fatalf("edit.any update = %d %q", code, body)
	}
}

func TestPublishRightIsSeparateFromAuthoring(t *testing.T) {
	h := newHarness(t)
	writer := h.author("writer3", permProblemCreate)
	h.json(http.MethodPost, "/problems", writer, []byte(`{"pid":"P6002","title":"待隐藏"}`))

	// Owning a problem does not let you hide it from the public.
	if code, body := h.json(http.MethodPut, "/problems/P6002", writer, []byte(`{"visible":false}`)); code != http.StatusForbidden {
		t.Fatalf("owner hid a problem without the right = %d %q", code, body)
	}
	h.grant("writer3", permProblemPublish, true)
	if code, body := h.json(http.MethodPut, "/problems/P6002", writer, []byte(`{"visible":false}`)); code != http.StatusOK {
		t.Fatalf("publish right update = %d %q", code, body)
	}
	// Title-only edits still work without touching visibility.
	if code, _ := h.json(http.MethodPut, "/problems/P6002", writer, []byte(`{"title":"改名"}`)); code != http.StatusOK {
		t.Fatalf("untouched visibility should not need the right: %d", code)
	}
}

func TestHiddenProblemsRespectRights(t *testing.T) {
	h := newHarness(t)
	owner := h.author("owner4", permProblemCreate, permProblemPublish)
	h.json(http.MethodPost, "/problems", owner, []byte(`{"pid":"P6003","title":"内部题","visible":false}`))
	outsider := h.userToken("outsider")

	if code, _ := h.get("/problems/P6003", outsider); code != http.StatusNotFound {
		t.Fatalf("hidden detail for outsider = %d", code)
	}
	if _, list := h.get("/problems", outsider); strings.Contains(list, "P6003") {
		t.Error("hidden problem leaked into the outsider list")
	}
	// The author can still see and work on it.
	if code, body := h.get("/problems/P6003", owner); code != http.StatusOK || !strings.Contains(body, "内部题") {
		t.Fatalf("author view = %d %q", code, body)
	}
	// Submitting to a hidden problem is allowed for the author, refused for others.
	h.uploadCasesAs(owner, "P6003")
	if code, _ := h.submit(owner, "P6003", "cpp", "int main(){}"); code != http.StatusCreated {
		t.Fatalf("author submit to own hidden problem = %d", code)
	}
	if code, _ := h.submit(outsider, "P6003", "cpp", "int main(){}"); code != http.StatusNotFound {
		t.Fatalf("outsider submit to hidden problem = %d", code)
	}
}

// uploadCasesAs posts a case zip with an arbitrary token.
func (h *harness) uploadCasesAs(token, pid string) {
	h.t.Helper()
	archive := zipOf(map[string]string{
		"sample1.in": "1 2\n", "sample1.out": "3\n",
		"1.in": "1 2\n", "1.out": "3\n",
	})
	if code, body := h.do(http.MethodPost, "/problems/"+pid+"/cases", token, archive, "application/zip"); code != http.StatusOK {
		h.t.Fatalf("upload as %s = %d %q", pid, code, body)
	}
}

func TestCaseRightsAreIndependentOfStatementEditing(t *testing.T) {
	h := newHarness(t)
	author := h.author("author5", permProblemCreate)
	h.json(http.MethodPost, "/problems", author, []byte(`{"pid":"P6004","title":"缺数据"}`))

	// The author may upload for their own problem.
	h.uploadCasesAs(author, "P6004")

	// A data maintainer with no create right can top up someone else's data...
	maintainer := h.author("maintainer5", permTestcaseManage)
	if code, body := h.do(http.MethodPost, "/problems/P6004/cases", maintainer,
		zipOf(map[string]string{"2.in": "4 5\n", "2.out": "9\n"}), "application/zip"); code != http.StatusOK {
		t.Fatalf("data maintainer upload = %d %q", code, body)
	}
	// ...but cannot rewrite the statement.
	if code, _ := h.json(http.MethodPut, "/problems/P6004", maintainer, []byte(`{"title":"越界改名"}`)); code != http.StatusForbidden {
		t.Fatalf("maintainer edited a statement = %d", code)
	}
	// Nobody else may even list the case set.
	stranger := h.userToken("stranger5")
	if code, _ := h.get("/problems/P6004/cases", stranger); code != http.StatusForbidden {
		t.Fatalf("stranger listed cases = %d", code)
	}
	// Judged case bodies stay shut; samples stay open.
	if code, _ := h.get("/problems/P6004/cases/2/in", stranger); code != http.StatusForbidden {
		t.Fatalf("stranger fetched judged case = %d", code)
	}
	if code, _ := h.get("/problems/P6004/cases/sample1/in", ""); code != http.StatusOK {
		t.Fatalf("anonymous sample fetch = %d", code)
	}
}

func TestViewingOthersSourceNeedsARight(t *testing.T) {
	h := newHarness(t)
	writer := h.author("writer6", permProblemCreate)
	h.readyProblemAs(writer, "P6005")
	owner := h.userToken("owner6")
	intruder := h.userToken("intruder6")

	h.submit(owner, "P6005", "cpp", "int main(){return 0;}")
	// The text view carries no source; the JSON view carries it for the owner.
	if _, mine := h.get("/submissions/1?format=json", owner); !strings.Contains(mine, "int main()") {
		t.Fatal("author of the submission could not see their own source")
	}
	if _, other := h.get("/submissions/1?format=json", intruder); strings.Contains(other, "int main()") {
		t.Fatal("another user read someone else's source")
	}
	h.grant("intruder6", permSubmissionViewAny, true)
	if _, audited := h.get("/submissions/1?format=json", intruder); !strings.Contains(audited, "int main()") {
		t.Fatal("submission.view.any had no effect")
	}
}

func (h *harness) readyProblemAs(token, pid string) {
	h.t.Helper()
	h.json(http.MethodPost, "/problems", token,
		[]byte(fmt.Sprintf(`{"pid":%q,"title":"别人的题","languages":["c","cpp"]}`, pid)))
	h.uploadCasesAs(token, pid)
}

func TestAdminConsolePermissions(t *testing.T) {
	h := newHarness(t)
	plainCookie, plainCSRF := h.login("plain", "plain@example.com", testPassword)

	// The console is closed without permission.manage.
	if code, _ := h.htmlWithCookie(http.MethodGet, "/admin/perms", plainCookie); code != http.StatusForbidden {
		t.Fatalf("plain user reached the console: %d", code)
	}

	// The bootstrap admin token has it, and can hand it out.
	if code, page := h.get("/admin/perms", h.admin); code != http.StatusOK || !strings.Contains(page, "plain") {
		t.Fatalf("admin console = %d", code)
	}
	if code, body := h.postBearer("/admin/perms/u/plain",
		url.Values{"key": {permProblemCreate}, "action": {"grant"}}, h.admin); code != http.StatusOK {
		// A bearer-authenticated POST needs no CSRF, and the console answers with
		// a redirect that the client follows back to the page.
		t.Fatalf("grant via console = %d %s", code, body)
	}
	u := h.user("plain")
	perms, err := h.db.EffectivePermissions(ctx(), u.ID, u.Role)
	if err != nil || !perms[permProblemCreate] {
		t.Fatalf("grant did not take effect: %+v %v", perms, err)
	}

	// Clearing restores the tier default, which for a plain user is nothing.
	if code, body := h.postBearer("/admin/perms/u/plain",
		url.Values{"key": {permProblemCreate}, "action": {"clear"}}, h.admin); code != http.StatusOK {
		t.Fatalf("clear = %d %s", code, body)
	}
	if perms, _ := h.db.EffectivePermissions(ctx(), u.ID, u.Role); perms[permProblemCreate] {
		t.Fatal("clear left the grant in place")
	}

	// A cookie session in the console still needs its CSRF token.
	if code, _, _ := h.postNoRedirect("/admin/perms/u/plain",
		url.Values{"key": {permProblemCreate}, "action": {"grant"}}, plainCookie); code != http.StatusForbidden {
		t.Fatalf("CSRF-less console POST = %d", code)
	}
	_ = plainCSRF
}

func TestAdminConsoleRefusesSelfEditAndBlocksLockout(t *testing.T) {
	h := newHarness(t)
	// The bootstrap admin token maps to the "admin" account, so editing that
	// account through the console is the self-edit case.
	code, body := h.json(http.MethodPost, "/admin/perms/u/admin", h.admin,
		[]byte(`{"key":"problem.create","action":"revoke"}`))
	if code != http.StatusBadRequest || !strings.Contains(body, "自己") {
		t.Fatalf("self-edit = %d %q", code, body)
	}
}

func TestRoleAndStatusChanges(t *testing.T) {
	h := newHarness(t)
	cookie, _ := h.login("victim", "victim@example.com", testPassword)

	// A promoted user gains the whole staff set through the tier.
	if code, body := h.postBearer("/admin/users/victim/role", url.Values{"role": {"admin"}}, h.admin); code != http.StatusOK {
		t.Fatalf("role change = %d %s", code, body)
	}
	if u := h.user("victim"); u.Role != "admin" {
		t.Fatalf("role = %s", u.Role)
	}
	if _, page := h.htmlWithCookie(http.MethodGet, "/admin/perms", cookie); !strings.Contains(page, "victim") {
		t.Error("promoted user still cannot reach the console")
	}

	// Banning kills live sessions immediately.
	if code, body := h.postBearer("/admin/users/victim/status", url.Values{"status": {"banned"}}, h.admin); code != http.StatusOK {
		t.Fatalf("ban = %d %s", code, body)
	}
	if _, page := h.htmlWithCookie(http.MethodGet, "/admin/perms", cookie); strings.Contains(page, "victim") {
		t.Error("banned session still authenticated")
	}
	// A banned account cannot log back in, and the reason is not spelled out.
	banned, bannedPage := h.formPage("/login", url.Values{"identifier": {"victim"}, "password": {testPassword}})
	if banned == http.StatusSeeOther {
		t.Fatal("banned account logged in")
	}
	if strings.Contains(bannedPage, "封禁") || strings.Contains(bannedPage, "banned") {
		t.Errorf("login should not reveal the ban reason:\n%s", bannedPage)
	}

	// Unbanning restores access without a password reset.
	if code, body := h.postBearer("/admin/users/victim/status", url.Values{"status": {"active"}}, h.admin); code != http.StatusOK {
		t.Fatalf("unban = %d %s", code, body)
	}
	code, _, hdr := h.postNoRedirect("/login", url.Values{"identifier": {"victim"}, "password": {testPassword}}, "")
	if code != http.StatusSeeOther || hdr.Get("Set-Cookie") == "" {
		t.Fatalf("reactivated login = %d %v", code, hdr.Values("Set-Cookie"))
	}
}

func TestJudgeViewRight(t *testing.T) {
	h := newHarness(t)
	if code, _ := h.get("/judges", h.userToken("watcher7")); code != http.StatusForbidden {
		t.Fatalf("plain user listed judges = %d", code)
	}
	auditor := h.author("auditor7", permJudgeView)
	if code, _ := h.get("/judges", auditor); code != http.StatusOK {
		t.Fatalf("judge.view holder = %d", code)
	}
	// The judge token itself is not a licence to browse the registry.
	if code, _ := h.get("/judges", h.judge); code != http.StatusForbidden {
		t.Fatalf("judge token listed judges = %d", code)
	}
}

func TestPermissionStoreRoundTrip(t *testing.T) {
	h := newHarness(t)
	ctx := ctx()
	// Both accounts have to exist first: the admin row is created lazily by the
	// first request that authenticates with the bootstrap token.
	h.get("/whoami", h.admin)
	h.login("plain", "plain@example.com", testPassword)

	perms, err := h.db.ListPermissions(ctx)
	if err != nil || len(perms) < 8 {
		t.Fatalf("permissions = %d %v", len(perms), err)
	}
	u := h.user("admin")
	if grants, err := h.db.UserGrants(ctx, u.ID, u.Role); err != nil || len(grants) != len(perms) {
		t.Fatalf("grants = %d want %d (%v)", len(grants), len(perms), err)
	} else {
		for _, g := range grants {
			if !g.Effective || !g.ByRole {
				t.Fatalf("admin tier should hold everything: %+v", g)
			}
		}
	}

	// An unknown key is refused rather than silently doing nothing.
	if err := h.db.SetUserPermission(ctx, u.ID, "not.a.permission", true); err != store.ErrNotFound {
		t.Fatalf("unknown key = %v", err)
	}

	// An explicit removal beats the tier default, and clearing restores it.
	plain := h.user("plain")
	if err := h.db.SetUserRole(ctx, plain.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if err := h.db.SetUserPermission(ctx, plain.ID, permProblemCreate, false); err != nil {
		t.Fatal(err)
	}
	eff, err := h.db.EffectivePermissions(ctx, plain.ID, "admin")
	if err != nil || eff[permProblemCreate] {
		t.Fatalf("explicit removal should beat the tier: %+v %v", eff[permProblemCreate], err)
	}
	// An explicit grant on a plain tier works the other way too.
	if err := h.db.SetUserPermission(ctx, plain.ID, permFeedModerate, true); err != nil {
		t.Fatal(err)
	}
	if eff, _ = h.db.EffectivePermissions(ctx, plain.ID, "user"); !eff[permFeedModerate] {
		t.Fatal("explicit grant on the plain tier had no effect")
	}
	if err := h.db.ClearUserPermission(ctx, plain.ID, permProblemCreate); err != nil {
		t.Fatal(err)
	}
	if eff, _ := h.db.EffectivePermissions(ctx, plain.ID, "admin"); !eff[permProblemCreate] {
		t.Fatal("clear did not restore the tier default")
	}
}
