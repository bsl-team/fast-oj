package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/liangbinchen2013/fast-oj/internal/store"
)

// htmlGet fetches a page the way a browser would.
func (h *harness) htmlGet(path, token string) (int, string) {
	h.t.Helper()
	code, body, _ := h.html(http.MethodGet, path, token, nil, "")
	return code, body
}

func (h *harness) html(method, path, token string, body []byte, contentType string) (int, string, http.Header) {
	h.t.Helper()
	var raw []byte
	if body != nil {
		raw = body
	}
	var reader *strings.Reader
	if raw != nil {
		reader = strings.NewReader(string(raw))
	} else {
		reader = strings.NewReader("")
	}
	req, err := http.NewRequest(method, h.srv.URL+path, reader)
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := h.srv.Client().Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	out := readAll(h.t, resp)
	return resp.StatusCode, out, resp.Header
}

// htmlForm posts an application/x-www-form-urlencoded body, like a browser form.
func (h *harness) htmlForm(path string, values url.Values, cookie string) (int, string, http.Header) {
	h.t.Helper()
	req, err := http.NewRequest(http.MethodPost, h.srv.URL+path, strings.NewReader(values.Encode()))
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := h.srv.Client().Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode, readAll(h.t, resp), resp.Header
}

// noRedirectClient follows nothing, so a 303 target can be asserted.
func (h *harness) postNoRedirect(path string, values url.Values, cookie string) (int, string, http.Header) {
	h.t.Helper()
	req, err := http.NewRequest(http.MethodPost, h.srv.URL+path, strings.NewReader(values.Encode()))
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode, readAll(h.t, resp), resp.Header
}

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func (h *harness) bind(handle string) string {
	h.t.Helper()
	tok := h.userToken(handle)
	// The bind reply is a 303 that carries the cookie, so the redirect must not
	// be followed or the Set-Cookie header is lost.
	code, body, hdr := h.postNoRedirect("/account/bind", url.Values{"token": {tok}}, "")
	if code != http.StatusSeeOther {
		h.t.Fatalf("bind = %d %q", code, body)
	}
	cookies := hdr.Values("Set-Cookie")
	if len(cookies) == 0 {
		h.t.Fatal("bind set no cookie")
	}
	if !strings.Contains(cookies[0], "HttpOnly") || !strings.Contains(cookies[0], "SameSite=Lax") {
		h.t.Fatalf("token cookie is not HttpOnly+Lax: %q", cookies[0])
	}
	if !strings.Contains(cookies[0], tok) {
		h.t.Fatalf("cookie does not carry the token: %q", cookies[0])
	}
	return "fastoj_token=" + tok
}

func TestProblemListViewForBrowsers(t *testing.T) {
	h := newHarness(t)
	h.readyProblem("P4000")
	h.json(http.MethodPost, "/problems", h.admin, []byte(`{"pid":"P4001","title":"隐藏题","visible":false}`))

	code, body := h.htmlGet("/", "")
	if code != http.StatusOK {
		t.Fatalf("GET / = %d", code)
	}
	for _, want := range []string{
		"<!doctype html>", "<th>编号</th>", `/problems/P4000`, "case ready", "题库",
		`href="/static/style.css"`, `katex@0.16.11`, `/static/math.js`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("list page missing %q", want)
		}
	}
	// A hidden problem is invisible to an anonymous browser, visible to admin.
	if strings.Contains(body, "P4001") {
		t.Error("hidden problem leaked into the anonymous list")
	}
	if _, mine := h.htmlGet("/", h.admin); !strings.Contains(mine, "P4001") {
		t.Error("admin browser list lost the hidden problem")
	}
	// The same URL still serves text to curl.
	if code, text := h.get("/problems", ""); code != http.StatusOK || strings.Contains(text, "<html") {
		t.Fatalf("text negotiation broke: %d %q", code, text)
	}
}

func TestProblemDetailViewRendersMarkdownAndMath(t *testing.T) {
	h := newHarness(t)
	fence := "```"
	statement := `# 描述

输入 $a, b$，输出 $a+b$。平均值写成块级公式：

$$ \frac{a+b}{2} $$

` + fence + "cpp\nint main(){}\n" + fence + `

<script>alert(1)</script>

[坑](javascript:alert(2))
`
	payload, err := json.Marshal(map[string]any{"pid": "P4002", "title": "公式题", "statement": statement})
	if err != nil {
		t.Fatal(err)
	}
	h.json(http.MethodPost, "/problems", h.admin, payload)
	h.uploadCases("P4002")

	code, body := h.htmlGet("/problems/P4002", "")
	if code != http.StatusOK {
		t.Fatalf("detail = %d", code)
	}
	for _, want := range []string{
		"<h1>描述</h1>",
		`<span class="math inline">\(a, b\)</span>`,
		`<span class="math display">\[ \frac{a+b}{2} \]</span>`,
		`<pre><code class="language-cpp">`,
		"样例", "1 2",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("detail page missing %q\n%s", want, body)
		}
	}
	// Scope the injection check to the statement block: the page chrome itself
	// legitimately carries <script> tags for KaTeX.
	start, end := strings.Index(body, "<article>"), strings.Index(body, "</article>")
	if start < 0 || end < 0 || end < start {
		t.Fatalf("no statement block found in:\n%s", body)
	}
	article := body[start:end]
	for _, banned := range []string{"<script", "</script>", "javascript:alert", "onerror", "<img"} {
		if strings.Contains(article, banned) {
			t.Errorf("statement leaked %q:\n%s", banned, article)
		}
	}
	// uploadCases puts "4 5" in judged case 2; it must not reach the page.
	if strings.Contains(body, "4 5") {
		t.Errorf("judged case leaked: %s", body)
	}
	// Without a bound identity the form is replaced by a pointer to /account.
	if strings.Contains(body, `<form method="post" action="/problems/P4002/submit"`) {
		t.Error("submit form shown to an anonymous visitor")
	}
	bound := h.bind("webuser")
	code, page := h.htmlWithCookie(http.MethodGet, "/problems/P4002", bound)
	if code != http.StatusOK || !strings.Contains(page, `<textarea name="source"`) {
		t.Fatalf("bound browser should see the editor: %d", code)
	}
	if !strings.Contains(page, "webuser") {
		t.Errorf("nav lost the bound handle: %s", page)
	}
	// Text mode must not gain a form.
	if _, text := h.get("/problems/P4002", ""); strings.Contains(text, "<textarea") {
		t.Error("text view started emitting HTML")
	}
}

func (h *harness) htmlWithCookie(method, path, cookie string) (int, string) {
	h.t.Helper()
	req, err := http.NewRequest(method, h.srv.URL+path, strings.NewReader(""))
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("Accept", "text/html")
	req.Header.Set("Cookie", cookie)
	resp, err := h.srv.Client().Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode, readAll(h.t, resp)
}

func TestWebSubmitEndToEndThroughTheForm(t *testing.T) {
	h := newHarness(t)
	h.readyProblem("P4003")
	cookie := h.bind("solver")

	source := "#include <iostream>\nint main(){int a,b;std::cin>>a>>b;std::cout<<a+b;}\n"
	code, body, hdr := h.postNoRedirect("/problems/P4003/submit",
		url.Values{"language": {"cpp"}, "source": {source}}, cookie)
	if code != http.StatusSeeOther {
		t.Fatalf("submit = %d %q", code, body)
	}
	loc := hdr.Get("Location")
	if loc != "/submissions/1" {
		t.Fatalf("redirect target = %q", loc)
	}

	// The queued row is readable as a page.
	got, page := h.htmlWithCookie(http.MethodGet, loc, cookie)
	if got != http.StatusOK || !strings.Contains(page, "提交 #1") || !strings.Contains(page, "排队中") {
		t.Fatalf("submission page = %d %s", got, page)
	}
	// The source is HTML-escaped on the page, so match an inert fragment plus the
	// escaped form of the include line.
	if !strings.Contains(page, "int main()") || !strings.Contains(page, "&lt;iostream&gt;") {
		t.Errorf("owner cannot see their own source:\n%s", page)
	}

	// The judge box finishes it and the page shows the verdict.
	h.finishSubmission(1, store.Result{Verdict: "AC", TimeMS: 12, MemKB: 3072, Score: 2, TotalScore: 2},
		[]store.Run{{Ordinal: 2, CaseName: "1", Verdict: "AC", TimeMS: 12, MemKB: 3072}})
	_, judged := h.htmlWithCookie(http.MethodGet, loc, cookie)
	for _, want := range []string{"badge AC", "通过", "得分", "12ms", "逐用例"} {
		if !strings.Contains(judged, want) {
			t.Errorf("judged page missing %q\n%s", want, judged)
		}
	}

	// A stranger browsing the same page gets no source and no case table — but
	// the notice that explains why is allowed to mention them.
	_, anon := h.htmlGet(loc, "")
	if strings.Contains(anon, "std::cin") || strings.Contains(anon, "<th>类型</th>") {
		t.Errorf("anonymous visitor saw private detail:\n%s", anon)
	}
	if !strings.Contains(anon, "badge AC") {
		t.Error("verdict itself should stay public")
	}
	if !strings.Contains(anon, "只有本人或管理员可见") {
		t.Error("anonymous page should explain the restriction")
	}

	// Anonymous form posts bounce to the binding page.
	code, _, hdr = h.postNoRedirect("/problems/P4003/submit",
		url.Values{"language": {"cpp"}, "source": {"x"}}, "")
	if code != http.StatusSeeOther || !strings.Contains(hdr.Get("Location"), "/account?msg=") {
		t.Fatalf("anonymous form = %d %q", code, hdr.Get("Location"))
	}
}

// finishSubmission plays the judge side: claim, then record a verdict.
func (h *harness) finishSubmission(id int64, res store.Result, runs []store.Run) {
	h.t.Helper()
	if _, err := h.db.UpsertJudge(ctx(), "box-web", []string{"cpp"}, ""); err != nil {
		h.t.Fatal(err)
	}
	if ok, err := h.db.SetStatus(ctx(), id, "queued", "compiling", 1); err != nil || !ok {
		h.t.Fatalf("claim = %v %v", ok, err)
	}
	if err := h.db.RecordResult(ctx(), id, res, runs); err != nil {
		h.t.Fatal(err)
	}
}

func TestWebFormValidationMirrorsTheAPI(t *testing.T) {
	h := newHarness(t)
	h.readyProblem("P4004")
	h.json(http.MethodPost, "/problems", h.admin, []byte(`{"pid":"P4005","title":"no cases"}`))
	cookie := h.bind("checker")

	cases := []struct{ pid, lang, source string }{
		{"P4004", "pascal", "int main(){}"}, // language not accepted
		{"P4005", "cpp", "int main(){}"},    // no judged cases yet
		{"P4004", "", "int main(){}"},       // missing language
		{"P4004", "cpp", "   "},             // empty source
		{"MISSING", "cpp", "int main(){}"},  // unknown problem
	}
	for _, c := range cases {
		code, _, hdr := h.postNoRedirect("/problems/"+c.pid+"/submit",
			url.Values{"language": {c.lang}, "source": {c.source}}, cookie)
		if code != http.StatusSeeOther {
			t.Fatalf("%+v = %d", c, code)
		}
		target, err := url.QueryUnescape(hdr.Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(target, "/problems/"+c.pid+"?msg=") {
			t.Fatalf("%+v bounced to %q", c, target)
		}
		if strings.Contains(target, "还没有判题用例") == false && c.pid == "P4005" {
			t.Fatalf("empty problem message lost: %q", target)
		}
	}

	// Nothing was created.
	if _, list := h.get("/submissions", ""); !strings.Contains(list, "shown=0") {
		t.Fatalf("rejected forms created rows: %s", list)
	}
}

func TestUnrestrictedProblemFormStillOffersLanguages(t *testing.T) {
	h := newHarness(t)
	h.json(http.MethodPost, "/problems", h.admin, []byte(`{"pid":"P4010","title":"语言不限"}`))
	h.uploadCases("P4010")
	cookie := h.bind("opener")

	_, page := h.htmlWithCookie(http.MethodGet, "/problems/P4010", cookie)
	for _, want := range []string{`<option value="c">`, `<option value="cpp">`, "语言 <b>不限</b>"} {
		if !strings.Contains(page, want) {
			t.Errorf("form missing %q:\n%s", want, page)
		}
	}
	// And the form actually works for an unrestricted problem.
	code, _, hdr := h.postNoRedirect("/problems/P4010/submit",
		url.Values{"language": {"Cpp"}, "source": {"int main(){}"}}, cookie)
	if code != http.StatusSeeOther || !strings.HasPrefix(hdr.Get("Location"), "/submissions/") {
		t.Fatalf("submit to unrestricted problem = %d %q", code, hdr.Get("Location"))
	}
}

func TestRankAndUserPages(t *testing.T) {
	h := newHarness(t)
	h.readyProblem("P4006")
	tok := h.userToken("champion")

	h.submit(tok, "P4006", "cpp", "int main(){}")
	h.submit(tok, "P4006", "cpp", "int main(){return 1;}")
	h.finishSubmission(1, store.Result{Verdict: "AC", TimeMS: 9, MemKB: 2048, Score: 2, TotalScore: 2}, nil)
	h.finishSubmission(2, store.Result{Verdict: "RE", TimeMS: 5, MemKB: 2048, Score: 0, TotalScore: 2, FailedCase: "1"}, nil)

	code, body := h.htmlGet("/rank", "")
	if code != http.StatusOK || !strings.Contains(body, "排行榜") || !strings.Contains(body, "champion") {
		t.Fatalf("rank page = %d %s", code, body)
	}
	if !strings.Contains(body, "<td class=\"num\">1</td>") {
		t.Errorf("rank number missing:\n%s", body)
	}
	// JSON mode stays available for tooling.
	if code, text := h.get("/rank", ""); code != http.StatusOK || strings.Contains(text, "<table") {
		t.Fatalf("rank text mode = %d %q", code, text)
	}

	code, body = h.htmlGet("/u/champion", "")
	if code != http.StatusOK {
		t.Fatalf("user page = %d", code)
	}
	for _, want := range []string{"champion", "已解出题", "判定分布", "P4006", "AC", "RE"} {
		if !strings.Contains(body, want) {
			t.Errorf("user page missing %q", want)
		}
	}
	if code, _ := h.htmlGet("/u/nobody", ""); code != http.StatusNotFound {
		t.Fatalf("unknown user = %d", code)
	}
	// An anonymous profile page still works (no private data on it).
	if code, _ := h.htmlGet("/u/champion", ""); code != http.StatusOK {
		t.Fatalf("anon profile = %d", code)
	}
}

func TestHTMLNotFoundAndStaticAssets(t *testing.T) {
	h := newHarness(t)

	code, body := h.htmlGet("/definitely/not/here", "")
	if code != http.StatusNotFound {
		t.Fatalf("404 = %d", code)
	}
	if !strings.Contains(body, "404 Not Found") || !strings.Contains(body, "/definitely/not/here") {
		t.Errorf("error page content: %s", body)
	}

	for path, want := range map[string]string{
		"/static/style.css": "body {",
		"/static/math.js":   "span.math",
	} {
		code, body := h.htmlGet(path, "")
		if code != http.StatusOK || !strings.Contains(body, want) {
			t.Fatalf("%s = %d", path, code)
		}
	}
	if code, _ := h.htmlGet("/static/style.css.exe", ""); code != http.StatusNotFound {
		t.Fatalf("odd asset suffix = %d", code)
	}
	// Only the two known files are served; no directory listing or traversal.
	if code, _ := h.htmlGet("/static/", ""); code != http.StatusNotFound {
		t.Fatalf("static root = %d", code)
	}
}

func TestAccountPageAndUnbind(t *testing.T) {
	h := newHarness(t)
	cookie := h.bind("wallet")

	// The bind is per cookie: an anonymous browser still sees the empty form.
	if _, anonPage := h.htmlGet("/account", ""); strings.Contains(anonPage, "wallet") {
		t.Fatal("anonymous visitor appeared bound")
	}
	code, bound := h.htmlWithCookie(http.MethodGet, "/account", cookie)
	if code != http.StatusOK || !strings.Contains(bound, "wallet") {
		t.Fatalf("account page = %d %s", code, bound)
	}
	if !strings.Contains(bound, "退出这台机器上的绑定") {
		t.Fatalf("no unbind button for a bound browser:\n%s", bound)
	}

	// A bound cookie authenticates the same way the header does.
	if _, who := h.htmlWithCookie(http.MethodGet, "/whoami", ""); who == "" {
		t.Fatal("whoami replied empty")
	}
	code, boundPage := h.htmlWithCookie(http.MethodGet, "/problems", cookie)
	if code != http.StatusOK || !strings.Contains(boundPage, "wallet") {
		t.Fatalf("cookie auth did not reach the page: %d", code)
	}

	unboundCode, _, hdr := h.postNoRedirect("/account/unbind", url.Values{}, cookie)
	if unboundCode != http.StatusSeeOther {
		t.Fatalf("unbind = %d", unboundCode)
	}
	cleared := hdr.Get("Set-Cookie")
	if !strings.Contains(cleared, "Max-Age=0") || !strings.Contains(cleared, "fastoj_token=;") {
		t.Fatalf("unbind did not expire the cookie: %q", cleared)
	}

	// A bad or empty token is refused and nothing is stored.
	if _, _, bad := h.postNoRedirect("/account/bind", url.Values{"token": {"wrong"}}, ""); !strings.Contains(bad.Get("Location"), "msg=") {
		t.Fatal("bad token should bounce with a message")
	}
	if _, _, empty := h.postNoRedirect("/account/bind", url.Values{"token": {""}}, ""); !strings.Contains(empty.Get("Location"), "msg=") {
		t.Fatal("empty token should bounce with a message")
	}
}

func TestSubmissionListViewForBrowsers(t *testing.T) {
	h := newHarness(t)
	h.readyProblem("P4007")
	tok := h.userToken("watcher")
	for i := 0; i < 3; i++ {
		h.submit(tok, "P4007", "cpp", fmt.Sprintf("int main(){return %d;}", i))
	}
	code, body := h.htmlGet("/submissions", "")
	if code != http.StatusOK {
		t.Fatalf("list = %d", code)
	}
	for _, want := range []string{"提交记录", "P4007", "watcher", "/submissions/3", "显示 3 条"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in\n%s", want, body)
		}
	}
	_, filtered := h.htmlGet("/submissions?user=watcher&problem=P4007", "")
	if !strings.Contains(filtered, "watcher") {
		t.Errorf("filter lost: %s", filtered)
	}
	_, unknown := h.htmlGet("/submissions?user=ghost", "")
	if !strings.Contains(unknown, "404") {
		t.Errorf("unknown user filter should 404:\n%s", unknown)
	}
}
