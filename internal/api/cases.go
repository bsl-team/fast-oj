package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/liangbinchen2013/fast-oj/internal/store"
)

// maxInlineCase bounds what the browser editor will load into a textarea. Beyond
// it the case is still listed and can be replaced by an archive import, but the
// page refuses to show half a file that saving would silently truncate.
const maxInlineCase = 256 << 10

type caseRow struct {
	ID              int64
	Ordinal         int
	Name            string
	Kind            string
	Group           string
	Score           int
	InSize, OutSize string
	Missing         bool
}

type casesData struct {
	PID, Title string
	Rows       []caseRow
	Groups     []groupRow
	Judged     int
	Total      int
}

// groupRow summarises one bundle: how many cases it holds and what the group is
// worth when all of them pass.
type groupRow struct {
	Name   string
	Cases  int
	Points int
}

// caseEdit is one case opened for editing.
type caseEdit struct {
	ID              int64
	Name            string
	Group           string
	Score           int
	IsSample        bool
	Input, Output   string
	Inline          bool
	InSize, OutSize string
	Ordinal         int
}

// loadCases reads the rows plus the two sizes each row needs, in one place so the
// list page and the edit page cannot disagree about what a case costs.
func (s *Server) loadCases(r *http.Request, p store.Problem) ([]store.Testcase, []caseRow, error) {
	cases, err := s.db.ListTestcases(r.Context(), p.ID, false)
	if err != nil {
		return nil, nil, err
	}
	rows := make([]caseRow, 0, len(cases))
	for _, tc := range cases {
		in, errIn := s.cases.Size(tc.InputRel)
		out, errOut := s.cases.Size(tc.OutputRel)
		kind := "判题"
		if tc.IsSample {
			kind = "样例"
		}
		rows = append(rows, caseRow{
			ID: tc.ID, Ordinal: tc.Ordinal, Name: tc.Name, Score: tc.Score, Group: tc.GroupName,
			Kind:    kind,
			InSize:  formatBytes(in, errIn),
			OutSize: formatBytes(out, errOut),
			Missing: errIn != nil || errOut != nil,
		})
	}
	return cases, rows, nil
}

func formatBytes(n int64, err error) string {
	if err != nil {
		return "缺文件"
	}
	switch {
	case n >= 1<<20:
		return strconv.FormatFloat(float64(n)/(1<<20), 'f', 2, 64) + " MB"
	case n >= 1024:
		return strconv.FormatFloat(float64(n)/1024, 'f', 1, 64) + " KB"
	}
	return strconv.FormatInt(n, 10) + " B"
}

func (s *Server) handleCasesPage(w http.ResponseWriter, r *http.Request) {
	a, p, ok := s.requireCaseAccess(w, r, r.PathValue("key"))
	if !ok {
		return
	}
	cases, rows, err := s.loadCases(r, p)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	total, err := s.db.ProblemScoreTotal(r.Context(), p.ID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	judged := 0
	// Groups keep the order their cases are declared in, so the summary does not
	// reshuffle between two visits to the same page.
	var groups []groupRow
	index := map[string]int{}
	for _, tc := range cases {
		if tc.IsSample {
			continue
		}
		judged++
		if tc.GroupName == "" {
			continue
		}
		at, seen := index[tc.GroupName]
		if !seen {
			at = len(groups)
			index[tc.GroupName] = at
			groups = append(groups, groupRow{Name: tc.GroupName})
		}
		groups[at].Cases++
		groups[at].Points += tc.Score
	}
	s.page(w, r, http.StatusOK, "用例 "+p.PID, "problem_cases", a.viewer(), msgOf(r), casesData{
		PID: p.PID, Title: p.Title, Rows: rows, Groups: groups, Judged: judged, Total: total,
	})
}

// caseForm is one case as the editor posts it.
type caseForm struct {
	Name, Group   string
	Score         int
	IsSample      bool
	Input, Output string
	HasContent    bool
}

// parseCaseForm reads the case editor. A sample carries no points by definition,
// so the score field is ignored rather than left for the author to notice.
func parseCaseForm(r *http.Request) (caseForm, string) {
	f := caseForm{
		Name:     strings.TrimSpace(r.FormValue("name")),
		Group:    strings.TrimSpace(r.FormValue("group")),
		IsSample: r.FormValue("sample") == "1",
	}
	if score, err := strconv.Atoi(strings.TrimSpace(r.FormValue("score"))); err != nil {
		return f, "分值要填整数"
	} else if score < 0 || score > 10000 {
		return f, "分值要 0..10000"
	} else {
		f.Score = score
	}
	if f.Name == "" || len(f.Name) > 64 || strings.ContainsAny(f.Name, "/\\ \t") || f.Name == "." || f.Name == ".." {
		return f, "用例名要 1..64 个字符，不能含空格或斜杠"
	}
	f.Input, f.Output = r.FormValue("input"), r.FormValue("output")
	// The textareas are only rendered for small files, so the fields being absent
	// is how "keep the bytes that are already on disk" arrives.
	_, hasIn := r.Form["input"]
	_, hasOut := r.Form["output"]
	f.HasContent = hasIn || hasOut
	if len(f.Group) > 40 || strings.ContainsAny(f.Group, "/\\") {
		return f, "组名最多 40 个字符，不能含斜杠"
	}
	if len(f.Input) > maxInlineCase || len(f.Output) > maxInlineCase {
		return f, "这一条太大，网页里改不了；用 zip 导入同名覆盖"
	}
	if f.IsSample {
		f.Score = 0
	}
	return f, ""
}

func (s *Server) handleCaseCreate(w http.ResponseWriter, r *http.Request) {
	a, p, ok := s.requireCaseAccess(w, r, r.PathValue("key"))
	if !ok {
		return
	}
	f, bad := parseCaseForm(r)
	if bad != "" {
		s.backToCases(w, r, p, bad)
		return
	}
	if !f.HasContent {
		s.backToCases(w, r, p, "输入与输出都要写点什么")
		return
	}
	cases, err := s.db.ListTestcases(r.Context(), p.ID, false)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if _, dup := findCase(cases, f.Name); dup {
		s.backToCases(w, r, p, "已经有同名用例："+f.Name)
		return
	}
	ordinal := 0
	for _, tc := range cases {
		if tc.Ordinal > ordinal {
			ordinal = tc.Ordinal
		}
	}
	ordinal++
	relIn, relOut, err := s.cases.Put(p.ID, ordinal, []byte(f.Input), []byte(f.Output))
	if err != nil {
		s.backToCases(w, r, p, "写入失败："+err.Error())
		return
	}
	if _, err := s.db.AddTestcase(r.Context(), &store.Testcase{
		ProblemID: p.ID, Ordinal: ordinal, Name: f.Name, IsSample: f.IsSample,
		Score: f.Score, GroupName: f.Group, InputRel: relIn, OutputRel: relOut,
	}); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	s.log.Info("case added", "pid", p.PID, "name", f.Name, "by", a.User.Handle)
	s.casesDone(w, r, p, "已加一条用例 "+f.Name)
}

func findCase(cases []store.Testcase, name string) (store.Testcase, bool) {
	for _, tc := range cases {
		if tc.Name == name {
			return tc, true
		}
	}
	return store.Testcase{}, false
}

// caseForEdit resolves /problems/{key}/cases/{id} against the problem it belongs
// to, so an id from another problem cannot be edited through this URL.
func (s *Server) caseForEdit(w http.ResponseWriter, r *http.Request) (actor, store.Problem, store.Testcase, bool) {
	a, p, ok := s.requireCaseAccess(w, r, r.PathValue("key"))
	if !ok {
		return a, p, store.Testcase{}, false
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.backToCases(w, r, p, "用例编号要是数字")
		return a, p, store.Testcase{}, false
	}
	tc, err := s.db.GetTestcaseByID(r.Context(), id)
	if err != nil || tc.ProblemID != p.ID {
		// An id from another problem must not be reachable through this URL.
		s.backToCases(w, r, p, "没有这条用例，或者它不属于这道题")
		return a, p, tc, false
	}
	return a, p, tc, true
}

func (s *Server) handleCaseEditPage(w http.ResponseWriter, r *http.Request) {
	a, p, tc, ok := s.caseForEdit(w, r)
	if !ok {
		return
	}
	in, errIn := s.cases.Read(tc.InputRel)
	out, errOut := s.cases.Read(tc.OutputRel)
	edit := caseEdit{
		ID: tc.ID, Name: tc.Name, Group: tc.GroupName, Score: tc.Score, IsSample: tc.IsSample, Ordinal: tc.Ordinal,
		InSize:  formatBytes(int64(len(in)), errIn),
		OutSize: formatBytes(int64(len(out)), errOut),
		Inline:  errIn == nil && errOut == nil && len(in) <= maxInlineCase && len(out) <= maxInlineCase,
	}
	if edit.Inline {
		edit.Input, edit.Output = string(in), string(out)
	}
	s.page(w, r, http.StatusOK, "改用例 "+tc.Name, "case_edit", a.viewer(), msgOf(r), map[string]any{
		"PID": p.PID, "Title": p.Title, "Edit": edit,
	})
}

func (s *Server) handleCaseSave(w http.ResponseWriter, r *http.Request) {
	a, p, tc, ok := s.caseForEdit(w, r)
	if !ok {
		return
	}
	f, bad := parseCaseForm(r)
	if bad != "" {
		s.backToCases(w, r, p, bad)
		return
	}
	next := tc
	next.Name, next.Score, next.IsSample, next.GroupName = f.Name, f.Score, f.IsSample, f.Group
	if f.HasContent {
		relIn, relOut, err := s.cases.Put(p.ID, tc.Ordinal, []byte(f.Input), []byte(f.Output))
		if err != nil {
			s.backToCases(w, r, p, "写入失败："+err.Error())
			return
		}
		next.InputRel, next.OutputRel = relIn, relOut
	}
	if _, err := s.db.UpdateTestcase(r.Context(), &next); err != nil {
		if errors.Is(err, store.ErrConflict) {
			s.backToCases(w, r, p, "已经有同名用例："+f.Name)
			return
		}
		s.writeStoreError(w, r, err)
		return
	}
	s.log.Info("case updated", "pid", p.PID, "name", f.Name, "by", a.User.Handle)
	s.casesDone(w, r, p, "已保存用例 "+f.Name)
}

func (s *Server) handleCaseDelete(w http.ResponseWriter, r *http.Request) {
	a, p, tc, ok := s.caseForEdit(w, r)
	if !ok {
		return
	}
	if _, err := s.db.DeleteTestcase(r.Context(), tc.ID); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	// The files are shared by ordinal, so they only go away once no remaining row
	// points at them.
	rest, err := s.db.ListTestcases(r.Context(), p.ID, false)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	for _, rel := range []string{tc.InputRel, tc.OutputRel} {
		shared := false
		for _, other := range rest {
			if other.InputRel == rel || other.OutputRel == rel {
				shared = true
			}
		}
		if !shared {
			if err := s.cases.Remove(rel); err != nil {
				s.log.Error("case file after delete", "pid", p.PID, "rel", rel, "err", err)
			}
		}
	}
	s.log.Info("case deleted", "pid", p.PID, "name", tc.Name, "by", a.User.Handle)
	s.casesDone(w, r, p, "已删除用例 "+tc.Name)
}

// handleCaseWipe drops every case of a problem, rows and files together. It is a
// POST with its own button because the alternative is a stray link prefetch
// deleting an author's test set.
func (s *Server) handleCaseWipe(w http.ResponseWriter, r *http.Request) {
	a, p, ok := s.requireCaseAccess(w, r, r.PathValue("key"))
	if !ok {
		return
	}
	n, err := s.db.DeleteTestcases(r.Context(), p.ID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if err := s.cases.RemoveProblem(p.ID); err != nil {
		s.log.Error("case files after wipe", "pid", p.PID, "err", err)
		s.casesDone(w, r, p, fmt.Sprintf("清掉了 %d 条记录，但用例文件没删掉：%s", n, err))
		return
	}
	s.log.Info("cases wiped", "pid", p.PID, "removed", n, "by", a.User.Handle)
	s.casesDone(w, r, p, fmt.Sprintf("已清空用例（%d 条）", n))
}

// casesDone is the one redirect every case write ends with, so the flash always
// lands on the page the author was working on.
func (s *Server) casesDone(w http.ResponseWriter, r *http.Request, p store.Problem, msg string) {
	http.Redirect(w, r, "/problems/"+url.PathEscape(p.PID)+"/cases?msg="+url.QueryEscape(msg), http.StatusSeeOther)
}

// backToCases is the rejected-write half of casesDone.
func (s *Server) backToCases(w http.ResponseWriter, r *http.Request, p store.Problem, msg string) {
	s.casesDone(w, r, p, msg)
}
