package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/liangbinchen2013/fast-oj/internal/store"
)

// userToken bootstraps a normal account through the admin API and returns a
// bearer token for it, which is how a CLI gets its credentials.
func (h *harness) userToken(handle string) string {
	h.t.Helper()
	if code, body := h.json(http.MethodPost, "/users", h.admin,
		[]byte(fmt.Sprintf(`{"handle":%q}`, handle))); code != http.StatusOK {
		h.t.Fatalf("create user = %d %q", code, body)
	}
	code, body := h.json(http.MethodPost, "/users/"+handle+"/tokens", h.admin, []byte(`{"label":"cli"}`))
	if code != http.StatusCreated {
		h.t.Fatalf("issue token = %d %q", code, body)
	}
	// The plaintext is only in the text body; the JSON view repeats it once.
	if !strings.Contains(body, "token=") || !strings.Contains(body, "copy now") {
		h.t.Fatalf("token was not shown once: %q", body)
	}
	var resp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err == nil && resp.Token != "" {
		return resp.Token
	}
	// text/plain reply: pull the value out of the first line
	line := strings.SplitN(body, "\n", 2)[0]
	fields := strings.Fields(line)
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "token=") {
		h.t.Fatalf("cannot parse token from %q", body)
	}
	return strings.TrimPrefix(fields[0], "token=")
}

// readyProblem creates a problem that accepts c/cpp and uploads two judged
// cases plus one sample.
func (h *harness) readyProblem(pid string) {
	h.t.Helper()
	if code, body := h.createProblem(url.Values{"pid": {pid}, "title": {"case ready"}, "languages": {"c", "cpp"}}); code != http.StatusOK {
		h.t.Fatalf("create problem = %d %q", code, body)
	}
	h.uploadCases(pid)
}

func (h *harness) submit(token, problem, lang, source string) (int, string) {
	body := fmt.Sprintf(`{"problem":%q,"language":%q,"source":%q}`, problem, lang, source)
	return h.json(http.MethodPost, "/submissions", token, []byte(body))
}

func (h *harness) uploadCases(pid string) {
	h.t.Helper()
	archive := zipOf(map[string]string{
		"sample1.in": "1 2\n", "sample1.out": "3\n",
		"1.in": "1 2\n", "1.out": "3\n",
		"2.in": "4 5\n", "2.out": "9\n",
	})
	if code, body := h.do(http.MethodPost, "/problems/"+pid+"/cases", h.admin, archive, "application/zip"); code != http.StatusOK {
		h.t.Fatalf("upload cases = %d %q", code, body)
	}
}

func TestSubmissionInputValidation(t *testing.T) {
	h := newHarness(t)
	tok := h.userToken("submitter")
	h.readyProblem("P2000")

	if code, _ := h.submit("", "P2000", "cpp", "int main(){}"); code != http.StatusUnauthorized {
		t.Fatalf("anonymous submit = %d, want 401", code)
	}
	if code, _ := h.submit("bogus", "P2000", "cpp", "x"); code != http.StatusUnauthorized {
		t.Fatalf("bad token submit = %d", code)
	}

	cases := []struct {
		problem, lang, source string
		want                  int
	}{
		{"NOPE", "cpp", "x", http.StatusNotFound},
		{"P2000", "pascal", "x", http.StatusBadRequest},
		{"P2000", "", "x", http.StatusBadRequest},
		{"", "cpp", "x", http.StatusBadRequest},
		{"P2000", "cpp", "   ", http.StatusBadRequest},
	}
	for _, c := range cases {
		if code, body := h.submit(tok, c.problem, c.lang, c.source); code != c.want {
			t.Fatalf("submit %+v = %d %q, want %d", c, code, body, c.want)
		}
	}

	// A JSON \u0000 escape decodes to a real NUL, which must not be stored.
	if code, body := h.json(http.MethodPost, "/submissions", tok,
		[]byte(`{"problem":"P2000","language":"cpp","source":"a\u0000b"}`)); code != http.StatusBadRequest {
		t.Fatalf("NUL in source = %d %q, want 400", code, body)
	}

	// A problem with no judged cases cannot be graded.
	if code, body := h.submit(tok, "P9999", "cpp", "int main(){}"); code != http.StatusNotFound {
		t.Fatalf("unknown problem = %d %q", code, body)
	}
	h.createProblem(url.Values{"pid": {"EMPTY"}, "title": {"no cases"}})
	if code, body := h.submit(tok, "EMPTY", "cpp", "int main(){}"); code != http.StatusConflict {
		t.Fatalf("empty problem submit = %d %q, want 409", code, body)
	}

	// Hidden problems are invisible to regular users but testable by admins.
	h.createProblem(url.Values{"pid": {"HIDE"}, "title": {"hidden"}, "visible": {"0"}})
	h.uploadCases("HIDE")
	if code, _ := h.submit(tok, "HIDE", "cpp", "int main(){}"); code != http.StatusNotFound {
		t.Fatalf("hidden submit as user = %d, want 404", code)
	}
	if code, _ := h.submit(h.admin, "HIDE", "cpp", "int main(){}"); code != http.StatusCreated {
		t.Fatalf("hidden submit as admin = %d", code)
	}
}

func TestSubmissionRoundTripAndVisibility(t *testing.T) {
	h := newHarness(t)
	alice := h.userToken("alice")
	bob := h.userToken("bob")
	h.readyProblem("P2001")

	code, body := h.submit(alice, "P2001", "CPP", "#include <bits/stdc++.h>\nint main(){int a,b;std::cin>>a>>b;std::cout<<a+b;}")
	if code != http.StatusCreated {
		t.Fatalf("submit = %d %q", code, body)
	}
	for _, want := range []string{"sid=1", "problem=P2001", "lang=cpp", "status=queued"} {
		if !strings.Contains(body, want) {
			t.Fatalf("submit reply missing %q: %q", want, body)
		}
	}

	// The owner sees the queued state with unknown measurements as dashes.
	code, body = h.get("/submissions/1", alice)
	if code != http.StatusOK {
		t.Fatalf("get = %d %q", code, body)
	}
	for _, want := range []string{"sid=1", "problem=P2001", "user=alice", "status=queued", "time=-", "mem=-", "score=0/2"} {
		if !strings.Contains(body, want) {
			t.Fatalf("owner view missing %q: %q", want, body)
		}
	}

	// Another user may see the state but never the source or the case table.
	_, foreign := h.get("/submissions/1", bob)
	if strings.Contains(foreign, "std::cin") || strings.Contains(foreign, "verdict     time") {
		t.Fatalf("foreign user leaked submission body: %q", foreign)
	}
	if !strings.Contains(foreign, "status=queued") {
		t.Fatalf("state should stay public: %q", foreign)
	}

	// A judge box registers, claims the row and reports the verdict.
	j, err := h.db.UpsertJudge(ctx(), "box-1", []string{"cpp"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := h.db.SetStatus(ctx(), 1, "queued", "compiling", j.ID); err != nil || !ok {
		t.Fatalf("claim = %v %v", ok, err)
	}
	if err := h.db.RecordResult(ctx(), 1, store.Result{
		Verdict: "AC", TimeMS: 12, MemKB: 3072, Score: 2, TotalScore: 2,
	}, []store.Run{
		{Ordinal: 2, CaseName: "1", Verdict: "AC", TimeMS: 12, MemKB: 3072},
		{Ordinal: 3, CaseName: "2", Verdict: "AC", TimeMS: 9, MemKB: 3000},
	}); err != nil {
		t.Fatal(err)
	}

	code, body = h.get("/submissions/1?wait=2", alice)
	if code != http.StatusOK {
		t.Fatalf("waited get = %d", code)
	}
	for _, want := range []string{"status=done", "verdict=AC", "time=12ms", "mem=3.00M", "score=2/2", "judge=1"} {
		if !strings.Contains(body, want) {
			t.Fatalf("judged view missing %q: %q", want, body)
		}
	}
	if !strings.Contains(body, "verdict") || !strings.Contains(body, " AC ") {
		t.Fatalf("owner should see the per-case table: %q", body)
	}
	// Bob still cannot see it.
	if _, foreign = h.get("/submissions/1", bob); strings.Contains(foreign, "std::cin") {
		t.Fatalf("leaked source after judging: %q", foreign)
	}

	// Listings.
	code, body = h.get("/submissions?user=me", alice)
	if code != http.StatusOK || !strings.Contains(body, "P2001") || strings.Contains(body, "sid=9999") {
		t.Fatalf("my list = %d %q", code, body)
	}
	if code, _ := h.get("/submissions?user=me", ""); code != http.StatusUnauthorized {
		t.Fatalf("anon user=me = %d", code)
	}
	_, byProblem := h.get("/submissions?problem=P2001", "")
	if !strings.Contains(byProblem, "alice") || !strings.Contains(byProblem, "sid") {
		t.Fatalf("problem list = %q", byProblem)
	}
	if _, byBob := h.get("/submissions?user=bob", ""); !strings.Contains(byBob, "shown=0") {
		t.Fatalf("bob list = %q", byBob)
	}
	if code, _ := h.get("/submissions?user=nobody", ""); code != http.StatusNotFound {
		t.Fatalf("unknown handle filter = %d", code)
	}
	if code, body := h.get("/submissions/abc", ""); code != http.StatusBadRequest {
		t.Fatalf("bad id = %d %q", code, body)
	}
	if code, _ := h.get("/submissions/9999", ""); code != http.StatusNotFound {
		t.Fatalf("unknown id = %d", code)
	}
}

func TestWaitBlocksUntilDeadline(t *testing.T) {
	h := newHarness(t)
	alice := h.userToken("carol")
	h.readyProblem("P2002")
	if code, body := h.submit(alice, "P2002", "cpp", "x"); code != http.StatusCreated {
		t.Fatalf("submit = %d %q", code, body)
	}

	// Nothing completes the row, so the long poll must burn most of its budget.
	start := time.Now()
	code, body := h.get("/submissions/1?wait=1", alice)
	elapsed := time.Since(start)
	if code != http.StatusOK || !strings.Contains(body, "status=queued") {
		t.Fatalf("waited response = %d %q", code, body)
	}
	if elapsed < 500*time.Millisecond || elapsed > 3*time.Second {
		t.Fatalf("wait=1 returned in %v", elapsed)
	}

	// A terminal row answers immediately, even with a large budget.
	j, err := h.db.UpsertJudge(ctx(), "box-1", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.SetStatus(ctx(), 1, "queued", "compiling", j.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.db.RecordResult(ctx(), 1, store.Result{
		Verdict: "WA", TimeMS: 5, MemKB: 1024, Score: 1, TotalScore: 2, FailedCase: "1",
		Detail: "got 4\nwant 3",
	}, nil); err != nil {
		t.Fatal(err)
	}
	start = time.Now()
	_, body = h.get("/submissions/1?wait=5", alice)
	if took := time.Since(start); took > 400*time.Millisecond {
		t.Fatalf("terminal submission should answer at once, took %v", took)
	}
	for _, want := range []string{"verdict=WA", "failed_case=1", "~~~ detail", "got 4"} {
		if !strings.Contains(body, want) {
			t.Fatalf("WA view missing %q: %q", want, body)
		}
	}
	// Non-owners do not get the compiler/diff detail.
	if _, other := h.get("/submissions/1", ""); strings.Contains(other, "got 4") {
		t.Fatalf("detail leaked to anonymous: %q", other)
	}
}

func TestWhoamiAndUserAdmin(t *testing.T) {
	h := newHarness(t)
	tok := h.userToken("dave")

	code, body := h.get("/whoami", tok)
	if code != http.StatusOK || !strings.Contains(body, "user=dave") || !strings.Contains(body, "admin=false") {
		t.Fatalf("whoami = %d %q", code, body)
	}
	if code, body := h.get("/whoami", h.admin); code != http.StatusOK || !strings.Contains(body, "admin=true") {
		t.Fatalf("admin whoami = %d %q", code, body)
	}
	if code, _ := h.get("/whoami", ""); code != http.StatusUnauthorized {
		t.Fatalf("anon whoami = %d", code)
	}
	if code, _ := h.get("/users", ""); code != http.StatusUnauthorized {
		t.Fatalf("anon user list = %d", code)
	}
	_, listed := h.get("/users", h.admin)
	for _, want := range []string{"dave", "admin", "users="} {
		if !strings.Contains(listed, want) {
			t.Fatalf("user list missing %q: %q", want, listed)
		}
	}

	for _, bad := range []string{
		`{"handle":"../etc"}`,
		`{"handle":""}`,
		`{"handle":"has space"}`,
		`{"handle":"ok_name","role":"root"}`,
	} {
		if code, _ := h.json(http.MethodPost, "/users", h.admin, []byte(bad)); code != http.StatusBadRequest {
			t.Fatalf("bad user payload %s = %d", bad, code)
		}
	}
	// Creating the same handle twice is idempotent, not a conflict.
	if code, _ := h.json(http.MethodPost, "/users", h.admin, []byte(`{"handle":"dave"}`)); code != http.StatusOK {
		t.Fatalf("repeat create = %d", code)
	}
	if code, _ := h.json(http.MethodPost, "/users/nope/tokens", h.admin, []byte(`{}`)); code != http.StatusNotFound {
		t.Fatalf("token for unknown user = %d", code)
	}
}

func TestParseWaitClamps(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"":      0,
		"0":     0,
		"-3":    0,
		"45":    45 * time.Second,
		"45s":   45 * time.Second,
		"500s":  5 * time.Minute,
		"1h":    5 * time.Minute,
		"2m30s": 150 * time.Second,
		"junk":  0,
	} {
		if got := parseWait(in); got != want {
			t.Errorf("parseWait(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestPageLimitClamps(t *testing.T) {
	for in, want := range map[string]int{"": 50, "0": 50, "-1": 50, "10": 10, "9999": 500} {
		if got := pageLimit(in); got != want {
			t.Errorf("pageLimit(%q) = %d, want %d", in, got, want)
		}
	}
}
