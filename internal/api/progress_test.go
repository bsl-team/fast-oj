package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// The point of reporting per case is that the page fills in while the run is still
// going, and that a retried report cannot duplicate a row.
func TestProgressRowsShowBeforeTheRunFinishes(t *testing.T) {
	h := newHarness(t)
	alice := h.userToken("alice")
	h.readyProblem("P3010")
	if code, _ := h.submit(alice, "P3010", "cpp", "int main(){}"); code != http.StatusCreated {
		t.Fatalf("submit = %d", code)
	}
	task := h.claimTask(t, "box-p")
	status := "/judge/submissions/" + strconv.Itoa(int(task.SubmissionID)) + "/status"
	sid := strconv.FormatInt(task.SubmissionID, 10)

	if code, body := h.postJSON(status, h.judge, `{"name":"box-p","state":"running","detail":"3 cases"}`); code != http.StatusOK {
		t.Fatalf("running = %d %s", code, body)
	}
	row := func(ordinal int, name, verdict string) string {
		return `{"name":"box-p","state":"running","detail":"` + strconv.Itoa(ordinal) + `/3 · ` + name + ` ` + verdict +
			`","run":{"ordinal":` + strconv.Itoa(ordinal) + `,"case_name":"` + name + `","verdict":"` + verdict +
			`","time_ms":12,"mem_kb":1024,"is_sample":` + strconv.FormatBool(ordinal == 1) + `}}`
	}
	if code, body := h.postJSON(status, h.judge, row(1, "sample1", "AC")); code != http.StatusOK {
		t.Fatalf("first progress = %d %s", code, body)
	}

	code, page, _ := h.html(http.MethodGet, "/submissions/"+sid, alice, nil, "")
	if code != http.StatusOK {
		t.Fatalf("detail page = %d", code)
	}
	if !strings.Contains(page, "sample1") || !strings.Contains(page, "12ms") {
		t.Errorf("the finished case is not on the page yet:\n%s", page)
	}
	if !strings.Contains(page, "判题中") {
		t.Error("the submission stopped being reported as in progress")
	}

	// A retry of the same ordinal replaces the row instead of adding one.
	if code, _ := h.postJSON(status, h.judge, row(1, "sample1", "WA")); code != http.StatusOK {
		t.Fatalf("retried progress = %d", code)
	}
	_, runs := h.get("/submissions/"+sid+"?format=json", alice)
	var payload struct {
		Runs []struct {
			Ordinal int    `json:"ordinal"`
			Verdict string `json:"verdict"`
		} `json:"runs"`
	}
	if err := json.Unmarshal([]byte(runs), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Runs) != 1 || payload.Runs[0].Verdict != "WA" {
		t.Fatalf("runs after the retry = %s", runs)
	}

	// Garbage verdicts and ordinals are refused rather than folded into IE.
	if code, _ := h.postJSON(status, h.judge, row(2, "1", "BROKEN")); code != http.StatusBadRequest {
		t.Errorf("unknown verdict = %d, want 400", code)
	}
	if code, _ := h.postJSON(status, h.judge, row(0, "1", "AC")); code != http.StatusBadRequest {
		t.Errorf("ordinal 0 = %d, want 400", code)
	}

	// The final result is authoritative: it replaces the partial rows.
	result := `{"name":"box-p","verdict":"AC","time_ms":12,"mem_kb":1024,"runs":[
		{"ordinal":1,"case_name":"sample1","verdict":"AC","time_ms":12,"mem_kb":1024,"is_sample":true},
		{"ordinal":2,"case_name":"1","verdict":"AC","time_ms":12,"mem_kb":1024},
		{"ordinal":3,"case_name":"2","verdict":"AC","time_ms":12,"mem_kb":1024}]}`
	if code, body := h.postJSON("/judge/submissions/"+sid+"/result?format=json", h.judge, result); code != http.StatusOK {
		t.Fatalf("result = %d %s", code, body)
	}
	_, done, _ := h.html(http.MethodGet, "/submissions/"+sid, alice, nil, "")
	if strings.Contains(done, "判题中") || !strings.Contains(done, "通过") {
		t.Error("the finished submission did not settle")
	}
	if _, body := h.get("/submissions/"+sid+"?format=json", alice); strings.Count(body, `"ordinal":1`) != 1 {
		t.Errorf("partial rows survived the final result:\n%s", body)
	}
}
