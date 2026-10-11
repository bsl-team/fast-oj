package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/liangbinchen2013/fast-oj/internal/store"
)

func zipOf(files map[string]string) []byte {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, name := range []string{"3.in", "3.out", "sample2.in", "sample2.out", "1.in", "1.out", "sample1.in", "sample1.out"} {
		f, err := w.Create(name)
		if err != nil {
			panic(err)
		}
		if _, err := f.Write([]byte(files[name])); err != nil {
			panic(err)
		}
	}
	if err := w.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func TestProblemAuthBoundaries(t *testing.T) {
	h := newHarness(t)

	if code, _ := h.json(http.MethodPost, "/problems", "", []byte(`{"pid":"P1","title":"t"}`)); code != http.StatusUnauthorized {
		t.Fatalf("anonymous create = %d, want 401", code)
	}
	if code, _ := h.json(http.MethodPost, "/problems", h.admin, []byte(`{"pid":"P1","title":"t"}`)); code != http.StatusCreated {
		t.Fatalf("admin create = %d", code)
	}

	// A real user token must not be able to author problems.
	user, err := h.db.EnsureUser(ctx(), "tester", "user")
	if err != nil {
		t.Fatal(err)
	}
	token, err := store.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := h.db.CreateToken(ctx(), user.ID, "cli", token); err != nil {
		t.Fatal(err)
	}
	if code, _ := h.json(http.MethodPost, "/problems", token, []byte(`{"pid":"P2","title":"t2"}`)); code != http.StatusForbidden {
		t.Fatalf("non-admin create = %d, want 403", code)
	}
	// ...but can read the public list.
	if code, body := h.get("/problems", token); code != http.StatusOK || !strings.Contains(body, "P1") {
		t.Fatalf("user list = %d %q", code, body)
	}
}

func TestProblemCrudAndTextRendering(t *testing.T) {
	h := newHarness(t)

	payload := `{"pid":"P1001","title":"A+B Problem","statement":"Give a and b, print a+b.\nHave 3 spaces.","time_limit_ms":1500,"mem_limit_kb":131072,"languages":["c","cpp"]}`
	code, body := h.json(http.MethodPost, "/problems", h.admin, []byte(payload))
	if code != http.StatusCreated {
		t.Fatalf("create = %d %q", code, body)
	}
	if !strings.Contains(body, "P1001") || !strings.Contains(body, "1.5s") || !strings.Contains(body, "128M") {
		t.Fatalf("create render = %q", body)
	}

	// Duplicate pid.
	if code, body := h.json(http.MethodPost, "/problems", h.admin, []byte(payload)); code != http.StatusConflict {
		t.Fatalf("duplicate = %d %q", code, body)
	}

	code, body = h.get("/problems/P1001", "")
	if code != http.StatusOK {
		t.Fatalf("get = %d %q", code, body)
	}
	for _, want := range []string{"A+B Problem", "time=1.5s", "Give a and b", "(no samples)"} {
		if !strings.Contains(body, want) {
			t.Fatalf("detail %q missing %q", body, want)
		}
	}

	// PUT only changes what it sends.
	code, body = h.json(http.MethodPut, "/problems/1", h.admin, []byte(`{"title":"A+B (revised)"}`))
	if code != http.StatusOK || !strings.Contains(body, "revised") {
		t.Fatalf("update = %d %q", code, body)
	}
	if _, detail := h.get("/problems/P1001", h.admin); !strings.Contains(detail, "time=1.5s") {
		t.Fatalf("update dropped untouched fields: %q", detail)
	}

	for _, bad := range []string{
		`{"pid":"","title":"x"}`,
		`{"pid":"P2","title":""}`,
		`{"pid":"P2","title":"x","time_limit_ms":0}`,
		`{"pid":"P2","title":"x","mem_limit_kb":-1}`,
		`{"pid":"P2","title":"x","languages":["c++!"]}`,
		`{"pid":"way-too-long-to-be-a-pid-aaaaaaaaaaaaaaaaaaaaaaaaaaaa","title":"x"}`,
		`not json`,
	} {
		if code, _ := h.json(http.MethodPost, "/problems", h.admin, []byte(bad)); code != http.StatusBadRequest {
			t.Fatalf("bad payload %s accepted with %d", bad, code)
		}
	}

	// Invalid UTF-8 must be refused, never rewritten into the database as
	// U+FFFD replacement characters.
	garbage := []byte(`{"pid":"PB","title":"` + "\xff\xfe" + `"}`)
	if code, body := h.json(http.MethodPost, "/problems", h.admin, garbage); code != http.StatusBadRequest || !strings.Contains(body, "UTF-8") {
		t.Fatalf("invalid utf-8 = %d %q", code, body)
	}

	if code, _ := h.json(http.MethodPost, "/problems", h.admin, []byte(strings.Repeat("x", 2<<20))); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body = %d, want 413", code)
	}

	code, body = h.do(http.MethodDelete, "/problems/P1001", h.admin, nil, "")
	if code != http.StatusOK || !strings.Contains(body, "deleted") {
		t.Fatalf("delete = %d %q", code, body)
	}
	if code, _ := h.get("/problems/1", h.admin); code != http.StatusNotFound {
		t.Fatalf("get after delete = %d", code)
	}
	if code, _ := h.do(http.MethodDelete, "/problems/P1001", h.admin, nil, ""); code != http.StatusNotFound {
		t.Fatalf("double delete = %d", code)
	}
}

func TestHiddenProblemVisibility(t *testing.T) {
	h := newHarness(t)
	h.json(http.MethodPost, "/problems", h.admin, []byte(`{"pid":"H1","title":"hidden one","visible":false}`))

	if code, _ := h.get("/problems/H1", ""); code != http.StatusNotFound {
		t.Fatalf("anon hidden detail = %d, want 404", code)
	}
	if code, body := h.get("/problems", ""); code != http.StatusOK || strings.Contains(body, "H1") {
		t.Fatalf("anon list leaked hidden: %d %q", code, body)
	}
	if code, body := h.get("/problems", h.admin); code != http.StatusOK || !strings.Contains(body, "H1") {
		t.Fatalf("admin list = %d %q", code, body)
	}
	if code, _ := h.get("/problems/H1", h.admin); code != http.StatusOK {
		t.Fatalf("admin hidden detail = %d", code)
	}
}

func TestCaseUploadAndAccess(t *testing.T) {
	h := newHarness(t)
	h.json(http.MethodPost, "/problems", h.admin, []byte(`{"pid":"P1","title":"Cases"}`))

	archive := zipOf(map[string]string{
		"sample1.in": "1 2\n", "sample1.out": "3\n",
		"sample2.in": "10 20\n", "sample2.out": "30\n",
		"1.in": "3 4\n", "1.out": "7\n",
		"3.in": "5 6\n", "3.out": "11\n",
	})

	// Cases are invisible until an admin uploads them.
	if code, _ := h.do(http.MethodPost, "/problems/P1/cases", "", archive, "application/zip"); code != http.StatusUnauthorized {
		t.Fatalf("anon upload = %d", code)
	}
	// Garbage body must be rejected as a bad archive, not a crash.
	if code, body := h.do(http.MethodPost, "/problems/P1/cases", h.admin, []byte("not a zip"), "application/zip"); code != http.StatusBadRequest {
		t.Fatalf("garbage upload = %d %q", code, body)
	}

	code, body := h.do(http.MethodPost, "/problems/P1/cases", h.admin, archive, "application/zip")
	if code != http.StatusOK {
		t.Fatalf("upload = %d %q", code, body)
	}
	if !strings.Contains(body, "total=4") || !strings.Contains(body, "added=4") {
		t.Fatalf("upload summary = %q", body)
	}

	// Re-uploading replaces by name instead of duplicating.
	code, body = h.do(http.MethodPost, "/problems/P1/cases", h.admin, archive, "application/zip")
	if code != http.StatusOK || !strings.Contains(body, "added=0") || !strings.Contains(body, "replaced=4") {
		t.Fatalf("re-upload = %d %q", code, body)
	}

	code, body = h.get("/problems/P1/cases", h.admin)
	if code != http.StatusOK {
		t.Fatalf("case list = %d %q", code, body)
	}
	if !strings.Contains(body, "sample1") || !strings.Contains(body, "judged") {
		t.Fatalf("case list content = %q", body)
	}
	if code, _ := h.get("/problems/P1/cases", ""); code != http.StatusUnauthorized {
		t.Fatalf("anon case list = %d", code)
	}

	// Samples show up in the public detail view, judged data does not.
	_, detail := h.get("/problems/P1", "")
	if !strings.Contains(detail, "~~~ input") || !strings.Contains(detail, "1 2") {
		t.Fatalf("public detail lost samples: %q", detail)
	}
	if strings.Contains(detail, "3 4") {
		t.Fatalf("public detail leaked judged case: %q", detail)
	}

	if code, body := h.get("/problems/P1/cases/sample1/in", ""); code != http.StatusOK || body != "1 2\n" {
		t.Fatalf("sample input = %d %q", code, body)
	}
	if code, body := h.get("/problems/P1/cases/1/out", ""); code != http.StatusForbidden {
		t.Fatalf("anon judged case = %d %q", code, body)
	}
	// Case "1" was uploaded second among judged cases, so it sits at ordinal 3
	// and its bytes live at <problem>/3.*.
	if code, body := h.get("/problems/P1/cases/1/out", h.admin); code != http.StatusOK || body != "7\n" {
		t.Fatalf("admin judged case = %d %q", code, body)
	}
	if code, _ := h.get("/problems/P1/cases/nope/in", h.admin); code != http.StatusNotFound {
		t.Fatalf("unknown case = %d", code)
	}

	// Ordinals are samples-first, then judged cases, and stable across re-upload.
	var caseList struct {
		Cases []store.Testcase `json:"cases"`
	}
	_, js := h.get("/problems/P1/cases?format=json", h.admin)
	if err := json.Unmarshal([]byte(js), &caseList); err != nil {
		t.Fatalf("case json: %v (%s)", err, js)
	}
	wantOrder := []string{"sample1", "sample2", "1", "3"}
	if len(caseList.Cases) != 4 {
		t.Fatalf("cases = %+v", caseList.Cases)
	}
	for i, tc := range caseList.Cases {
		if tc.Name != wantOrder[i] || tc.Ordinal != i+1 {
			t.Fatalf("case %d = %+v, want %s at ordinal %d", i, tc, wantOrder[i], i+1)
		}
		if tc.IsSample != (i < 2) {
			t.Fatalf("sample flag wrong for %+v", tc)
		}
	}

	code, body = h.do(http.MethodDelete, "/problems/P1/cases", h.admin, nil, "")
	if code != http.StatusOK || !strings.Contains(body, "removed 4") {
		t.Fatalf("wipe = %d %q", code, body)
	}
	if _, listed := h.get("/problems/P1/cases", h.admin); !strings.Contains(listed, "cases=0") {
		t.Fatalf("after wipe: %q", listed)
	}
}

func TestIncompleteCasePairsRejected(t *testing.T) {
	h := newHarness(t)
	h.json(http.MethodPost, "/problems", h.admin, []byte(`{"pid":"P9","title":"Broken set"}`))

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, _ := w.Create("lonely.in")
	f.Write([]byte("only input\n"))
	w.Close()

	code, body := h.do(http.MethodPost, "/problems/P9/cases", h.admin, buf.Bytes(), "application/zip")
	if code != http.StatusBadRequest || !strings.Contains(body, "lonely") {
		t.Fatalf("unpaired upload = %d %q", code, body)
	}
}

func TestListPagination(t *testing.T) {
	h := newHarness(t)
	for i := 1; i <= 7; i++ {
		h.json(http.MethodPost, "/problems", h.admin,
			[]byte(fmt.Sprintf(`{"pid":"P%02d","title":"problem %d"}`, i, i)))
	}

	code, body := h.get("/problems?limit=3", "")
	if code != http.StatusOK {
		t.Fatalf("list = %d", code)
	}
	type page struct {
		Items     []store.Problem `json:"items"`
		NextAfter int64           `json:"next_after"`
		Total     int             `json:"total"`
	}
	pageOf := func(q string) page {
		t.Helper()
		_, body := h.get("/problems?"+q+"&format=json", "")
		var p page
		if err := json.Unmarshal([]byte(body), &p); err != nil {
			t.Fatalf("json page %q: %v (%s)", q, err, body)
		}
		return p
	}

	p1 := pageOf("limit=3")
	if len(p1.Items) != 3 || p1.NextAfter != 3 || p1.Total != 7 {
		t.Fatalf("page1 = %+v", p1)
	}
	if !strings.Contains(body, "next_after=3") || !strings.Contains(body, "shown=3 total=7") {
		t.Fatalf("text page footer = %q", body)
	}

	p2 := pageOf("limit=3&after=3")
	if len(p2.Items) != 3 || p2.Items[0].PID != "P04" {
		t.Fatalf("page2 = %+v", p2.Items)
	}

	p3 := pageOf("limit=3&after=6")
	if len(p3.Items) != 1 || p3.NextAfter != 0 {
		t.Fatalf("last page = %+v", p3)
	}

	_, search := h.get("/problems?q=P05", "")
	if !strings.Contains(search, "P05") || strings.Contains(search, "P04") {
		t.Fatalf("search = %q", search)
	}
}
