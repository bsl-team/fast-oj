package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/liangbinchen2013/fast-oj/internal/protocol"
	"github.com/liangbinchen2013/fast-oj/internal/store"
)

func (h *harness) claim(name string) (int, string) {
	return h.json(http.MethodPost, "/judge/claim?format=json", h.judge,
		[]byte(`{"name":"`+name+`","languages":["cpp"],"note":"test box"}`))
}

func (h *harness) claimTask(t *testing.T, name string) protocol.Task {
	t.Helper()
	code, body := h.claim(name)
	if code != http.StatusOK {
		t.Fatalf("claim %s = %d %q", name, code, body)
	}
	var task protocol.Task
	if err := json.Unmarshal([]byte(body), &task); err != nil {
		t.Fatalf("claim payload: %v (%s)", err, body)
	}
	return task
}

func (h *harness) postJSON(path, token string, body string) (int, string) {
	return h.json(http.MethodPost, path, token, []byte(body))
}

func TestJudgeClaimWorkOrder(t *testing.T) {
	h := newHarness(t)
	alice := h.userToken("alice")
	h.readyProblem("P3000")

	// An empty queue is a 404, not an error.
	if code, body := h.claim("box-1"); code != http.StatusNotFound || !strings.Contains(body, `"queue":"empty"`) {
		t.Fatalf("empty claim = %d %q", code, body)
	}
	if code, _ := h.postJSON("/judge/claim?format=json", "", `{"name":"box-1"}`); code != http.StatusUnauthorized {
		t.Fatalf("anonymous claim = %d", code)
	}
	if code, _ := h.postJSON("/judge/claim?format=json", alice, `{"name":"box-1"}`); code != http.StatusForbidden {
		t.Fatalf("user token claim = %d, want 403", code)
	}
	// The admin token may drive the queue by hand.
	if code, _ := h.json(http.MethodPost, "/judge/claim", h.admin, []byte(`{"name":""}`)); code != http.StatusBadRequest {
		t.Fatalf("blank judge name = %d, want 400", code)
	}

	if code, body := h.submit(alice, "P3000", "cpp", "int main(){}"); code != http.StatusCreated {
		t.Fatalf("submit = %d %q", code, body)
	}
	task := h.claimTask(t, "box-1")
	if task.SubmissionID == 0 || task.PID != "P3000" || task.Language != "cpp" || task.Source != "int main(){}" {
		t.Fatalf("task = %+v", task)
	}
	if task.TimeLimitMS != 1000 || task.MemLimitKB != 262144 {
		t.Fatalf("limits = %+v", task)
	}
	// sample1 (public) + 1,2 (judged), samples first.
	if len(task.Cases) != 3 {
		t.Fatalf("cases = %+v", task.Cases)
	}
	if !task.Cases[0].IsSample || task.Cases[0].Score != 0 {
		t.Fatalf("sample row = %+v", task.Cases[0])
	}
	if task.Cases[1].IsSample || task.Cases[1].Score != 1 {
		t.Fatalf("judged row = %+v", task.Cases[1])
	}

	// The box can pull every case body, including judged ones.
	for _, tc := range task.Cases {
		if code, body := h.get("/judge/cases/"+itoa(int(tc.ID))+"/in", h.judge); code != http.StatusOK {
			t.Fatalf("case %d in = %d %q", tc.ID, code, body)
		}
		if code, body := h.get("/judge/cases/"+itoa(int(tc.ID))+"/out", h.judge); code != http.StatusOK {
			t.Fatalf("case %d out = %d %q", tc.ID, code, body)
		}
	}
	if code, _ := h.get("/judge/cases/1/in", ""); code != http.StatusUnauthorized {
		t.Fatalf("anonymous case fetch = %d", code)
	}
	if code, _ := h.get("/judge/cases/999/in", h.judge); code != http.StatusNotFound {
		t.Fatalf("unknown case = %d", code)
	}
	if code, body := h.get("/judge/cases/1/other", h.judge); code != http.StatusBadRequest {
		t.Fatalf("bad which = %d %q", code, body)
	}

	// The box shows up in the registry.
	code, listed := h.get("/judges", h.admin)
	if code != http.StatusOK || !strings.Contains(listed, "box-1") || !strings.Contains(listed, "cpp") {
		t.Fatalf("judges = %d %q", code, listed)
	}
	if code, _ := h.get("/judges", alice); code != http.StatusForbidden {
		t.Fatalf("user may not list judges = %d", code)
	}
	// Nothing else is queued.
	if code, _ := h.claim("box-1"); code != http.StatusNotFound {
		t.Fatalf("second claim = %d", code)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestJudgeStatusTransitions(t *testing.T) {
	h := newHarness(t)
	alice := h.userToken("alice")
	h.readyProblem("P3001")
	h.submit(alice, "P3001", "cpp", "x")
	task := h.claimTask(t, "box-1")
	path := "/judge/submissions/" + itoa(int(task.SubmissionID)) + "/status"

	if code, body := h.postJSON(path, h.judge, `{"name":"box-9","state":"running"}`); code != http.StatusForbidden {
		t.Fatalf("unknown box = %d %q", code, body)
	}
	if code, body := h.postJSON(path, h.judge, `{"name":"box-1","state":"done"}`); code != http.StatusBadRequest {
		t.Fatalf("terminal state via status = %d %q", code, body)
	}
	// claim stamped compiling, so compiling -> running is the legal next step.
	if code, body := h.postJSON(path, h.judge, `{"name":"box-1","state":"running","detail":"on case 2"}`); code != http.StatusOK {
		t.Fatalf("progress = %d %q", code, body)
	}
	// Repeating it is now an illegal transition.
	if code, body := h.postJSON(path, h.judge, `{"name":"box-1","state":"compiling"}`); code != http.StatusConflict {
		t.Fatalf("running -> compiling = %d %q", code, body)
	}
	_, view := h.get("/submissions/"+itoa(int(task.SubmissionID)), alice)
	if !strings.Contains(view, "status=running") {
		t.Fatalf("progress not visible to the owner: %q", view)
	}
	if code, _ := h.postJSON(path, alice, `{"name":"box-1","state":"running"}`); code != http.StatusForbidden {
		t.Fatalf("user token may not report = %d", code)
	}
}

func TestJudgeResultAndScoring(t *testing.T) {
	h := newHarness(t)
	alice := h.userToken("alice")
	h.readyProblem("P3002")
	h.submit(alice, "P3002", "cpp", "int main(){}")
	task := h.claimTask(t, "box-1")
	sid := itoa(int(task.SubmissionID))
	result := "/judge/submissions/" + sid + "/result"

	judged := task.Cases[1].Name
	other := task.Cases[2].Name
	sample := task.Cases[0].Name

	body := `{"name":"box-1","verdict":"AC","time_ms":12,"mem_kb":3072,"runs":[` +
		`{"ordinal":1,"case_name":"` + sample + `","verdict":"AC","time_ms":1,"mem_kb":1024},` +
		`{"ordinal":2,"case_name":"` + judged + `","verdict":"AC","time_ms":12,"mem_kb":3072},` +
		`{"ordinal":3,"case_name":"` + other + `","verdict":"AC","time_ms":9,"mem_kb":3000}]}`
	code, out := h.postJSON(result, h.judge, body)
	if code != http.StatusOK || !strings.Contains(out, "verdict=AC") || !strings.Contains(out, "score=2/2") {
		t.Fatalf("AC result = %d %q", code, out)
	}
	_, view := h.get("/submissions/"+sid, alice)
	for _, want := range []string{"status=done", "verdict=AC", "score=2/2", "time=12ms", "mem=3M"} {
		if !strings.Contains(view, want) {
			t.Fatalf("owner view missing %q: %q", want, view)
		}
	}

	// A judge cannot report twice.
	if code, out := h.postJSON(result, h.judge, body); code != http.StatusConflict {
		t.Fatalf("repeat result = %d %q", code, out)
	}

	// An AC claim with a failing judged row is downgraded by the server.
	h.submit(alice, "P3002", "cpp", "int main(){return 0;}")
	task2 := h.claimTask(t, "box-1")
	sid2 := itoa(int(task2.SubmissionID))
	bad := `{"name":"box-1","verdict":"AC","time_ms":30,"mem_kb":2048,"runs":[` +
		`{"case_name":"` + sample + `","verdict":"AC"},` +
		`{"case_name":"` + judged + `","verdict":"WA"},` +
		`{"case_name":"` + other + `","verdict":"AC"}]}`
	code, out = h.postJSON("/judge/submissions/"+sid2+"/result", h.judge, bad)
	if code != http.StatusOK || !strings.Contains(out, "verdict=WA") || !strings.Contains(out, "score=1/2") ||
		!strings.Contains(out, "failed="+judged) {
		t.Fatalf("downgrade = %d %q", code, out)
	}

	// Unknown verdict strings become IE.
	h.submit(alice, "P3002", "cpp", "x")
	task3 := h.claimTask(t, "box-2")
	sid3 := itoa(int(task3.SubmissionID))
	code, out = h.postJSON("/judge/submissions/"+sid3+"/result", h.judge,
		`{"name":"box-2","verdict":"MEMORY_EXPLODED","time_ms":-1,"mem_kb":-1}`)
	if code != http.StatusOK || !strings.Contains(out, "verdict=IE") {
		t.Fatalf("unknown verdict = %d %q", code, out)
	}
	// box-2 owns that row, so box-1 must not be able to report on it.
	h.submit(alice, "P3002", "cpp", "x")
	task4 := h.claimTask(t, "box-3")
	if code, out := h.postJSON("/judge/submissions/"+itoa(int(task4.SubmissionID))+"/result", h.judge,
		`{"name":"box-1","verdict":"AC"}`); code != http.StatusForbidden {
		t.Fatalf("wrong owner result = %d %q", code, out)
	}
	if code, _ := h.postJSON("/judge/submissions/99999/result", h.judge, `{"name":"box-1","verdict":"AC"}`); code != http.StatusNotFound {
		t.Fatalf("unknown submission result = %d", code)
	}
	if code, body := h.postJSON("/judge/submissions/abc/result", h.judge, `{"name":"box-1"}`); code != http.StatusBadRequest {
		t.Fatalf("bad id = %d %q", code, body)
	}
}

func TestTallyResultMath(t *testing.T) {
	cases := []store.Testcase{
		{Name: "sample1", IsSample: true, Score: 0},
		{Name: "1", Score: 40},
		{Name: "2", Score: 60},
	}
	ac := protocol.RunRow{CaseName: "1", Verdict: "AC"}
	wa := protocol.RunRow{CaseName: "2", Verdict: "WA"}

	// Honest AC.
	v, runs, score, total, failed := tallyResult("AC", []protocol.RunRow{
		{CaseName: "sample1", Verdict: "AC"}, ac, {CaseName: "2", Verdict: "AC"},
	}, cases)
	if v != "AC" || score != 100 || total != 100 || failed != "" || len(runs) != 3 {
		t.Fatalf("honest ac: %v %d %d %q", v, score, total, failed)
	}

	// Lying AC.
	v, _, score, total, failed = tallyResult("AC", []protocol.RunRow{ac, wa}, cases)
	if v != "WA" || score != 40 || total != 100 || failed != "2" {
		t.Fatalf("lying ac: %v %d %d %q", v, score, total, failed)
	}

	// Compile error: no rows, no score, nothing failed by case.
	v, _, score, _, failed = tallyResult("CE", nil, cases)
	if v != "CE" || score != 0 || failed != "" {
		t.Fatalf("ce: %v %d %q", v, score, failed)
	}

	// Garbage verdict string collapses to IE.
	v, _, _, _, _ = tallyResult("AC", []protocol.RunRow{{CaseName: "1", Verdict: "boom"}}, cases)
	if v != "IE" {
		t.Fatalf("garbage verdict = %v", v)
	}

	// A row for a deleted case cannot certify AC.
	v, _, _, _, failed = tallyResult("AC", []protocol.RunRow{
		{CaseName: "1", Verdict: "AC"}, {CaseName: "ghost", Verdict: "RE"},
	}, cases)
	if v != "RE" || failed != "ghost" {
		t.Fatalf("ghost row: %v %q", v, failed)
	}

	// Sample rows never add points.
	v, _, score, _, _ = tallyResult("AC", []protocol.RunRow{{CaseName: "sample1", Verdict: "AC"}}, cases)
	if v != "AC" || score != 0 {
		t.Fatalf("sample only: %v %d", v, score)
	}
}
