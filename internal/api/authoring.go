package api

import (
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/liangbinchen2013/fast-oj/internal/store"
)

// Authoring is a web-only surface: the problem statement, its limits and its
// test data are edited by a person in a browser, so there is no JSON shape to
// keep in sync with the form.

const (
	maxPIDLen     = 32
	maxTitleRunes = 200
	maxTimeMS     = 600_000
	maxMemKB      = 4_194_304
)

// problemForm is what the browser posts. Memory is entered in megabytes because
// that is how people think about a limit, and stored in kilobytes because that
// is what the judge measures against.
type problemForm struct {
	PID     string
	Title   string
	Detail  string
	MemMB   float64
	TimeMS  int
	Langs   []string
	Visible bool
	// Keep/Statement belong to the statement editor.
	Statement string
}

func (f problemForm) memKB() int { return int(f.MemMB*1024 + 0.5) }

// parseProblemForm reads the form and returns the values plus the problems found
// in one pass: a rejected save has to redraw the page with everything already
// typed, so validation cannot stop at the first field.
func parseProblemForm(r *http.Request) (problemForm, []string) {
	f := problemForm{
		PID:       strings.TrimSpace(r.FormValue("pid")),
		Detail:    r.FormValue("detail"),
		Title:     strings.TrimSpace(r.FormValue("title")),
		Statement: r.FormValue("statement"),
		Langs:     r.Form["languages"],
		Visible:   r.FormValue("visible") == "1",
	}
	var errs []string
	if ms, err := strconv.Atoi(strings.TrimSpace(r.FormValue("time_ms"))); err != nil {
		errs = append(errs, "时限要填整数毫秒")
	} else {
		f.TimeMS = ms
	}
	if mb, err := strconv.ParseFloat(strings.TrimSpace(r.FormValue("mem_mb")), 64); err != nil {
		errs = append(errs, "内存上限要填数字（MB）")
	} else {
		f.MemMB = mb
	}

	if f.PID == "" || len(f.PID) > maxPIDLen {
		errs = append(errs, fmt.Sprintf("编号要 1..%d 个字符", maxPIDLen))
	} else if strings.ContainsAny(f.PID, "/\\ \t") {
		errs = append(errs, "编号不能包含斜杠或空格")
	}
	if f.Title == "" || tooLong(f.Title, maxTitleRunes) {
		errs = append(errs, fmt.Sprintf("标题要 1..%d 个字符", maxTitleRunes))
	}
	if f.TimeMS <= 0 || f.TimeMS > maxTimeMS {
		errs = append(errs, fmt.Sprintf("时限要 1..%d 毫秒", maxTimeMS))
	}
	if f.memKB() <= 0 || f.memKB() > maxMemKB {
		errs = append(errs, fmt.Sprintf("内存要 0.001..%d MB", maxMemKB/1024))
	}
	for _, l := range f.Langs {
		if !validLanguageTag(l) {
			errs = append(errs, "语言标签不合法："+l)
		}
	}
	// A browser always posts the selector, so an empty value means the form came
	// from somewhere else: fall back to the default rather than reject.
	if f.Detail != "" && !store.ValidDetailLevel(f.Detail) {
		errs = append(errs, "详情级别不认识："+f.Detail)
	}
	return f, errs
}

// apply writes the form onto a problem row.
func (f problemForm) apply(p *store.Problem) {
	p.PID, p.Title, p.Statement = f.PID, f.Title, f.Statement
	p.DetailLevel = detailLevel(f.Detail)
	p.TimeLimitMS, p.MemLimitKB, p.Visible = f.TimeMS, f.memKB(), f.Visible
	p.Languages = f.Langs
}

type editRow struct {
	Value, Label string
	Checked      bool
}

// editData is the template payload for both the create and the edit page.
type editData struct {
	PID, Title, Statement string
	TimeMS                int
	MemMB                 string
	// Preview is Markdown already rendered through the app-wide pipeline, which is
	// the only reason it is safe to hand to the template unescaped.
	Preview        template.HTML
	Visible, IsNew bool
	// Detail is the chosen 详情级别 and DetailOptions the selector next to it.
	Detail               string
	DetailOptions        []detailOption
	Cases, Judged, Total int
	Langs                []editRow
	Errors               []string
	// Action is where the form posts and Key is the problem's current pid. Both
	// stay on the old value while the author is typing a new pid, so a rejected
	// rename cannot point the links and the post target at a problem that
	// does not exist yet.
	Action string
	Key    string
}

func newEditData(p store.Problem, isNew bool) editData {
	d := editData{
		PID: p.PID, Title: p.Title, Statement: p.Statement,
		TimeMS: p.TimeLimitMS, Visible: p.Visible, IsNew: isNew,
		MemMB: memMBText(p.MemLimitKB),
		Key:   p.PID, Detail: detailLevel(p.DetailLevel), DetailOptions: detailOptions,
	}
	if isNew {
		// A brand new problem starts from the limits most OI problems use, so the
		// common case is one field to fill rather than six.
		d.TimeMS, d.MemMB, d.Visible = 1000, "256", true
		d.Action = "/problems"
	} else {
		d.Action = "/problems/" + url.PathEscape(p.PID)
	}
	checked := map[string]bool{}
	for _, l := range p.Languages {
		checked[l] = true
	}
	for _, l := range defaultLanguages {
		d.Langs = append(d.Langs, editRow{Value: l, Label: l, Checked: checked[l]})
	}
	return d
}

// detailOption is one choice in the 详情级别 selector.
type detailOption struct {
	Value, Label string
	Current      bool
}

// detailLevels is the order the selector shows them in, least revealing first.
var detailLevels = []struct{ value, label string }{
	{store.DetailNone, "只给总判定（比赛进行中用这个）"},
	{store.DetailCase, "给逐用例判定"},
	{store.DetailDiff, "再给实测/期望差异（默认）"},
	{store.DetailFull, "全给，包含判机侧细节"},
}

var detailOptions = func() []detailOption {
	out := make([]detailOption, 0, len(detailLevels))
	for _, l := range detailLevels {
		out = append(out, detailOption{Value: l.value, Label: l.label})
	}
	return out
}()

// detailLevel keeps an unset value from rendering a blank option as selected.
func detailLevel(level string) string {
	if store.ValidDetailLevel(level) {
		return level
	}
	return store.DetailDiff
}

// memMBText prints the stored kilobyte budget back as the megabytes the form
// asked for, without the trailing ".000" that a fixed format would add.
func memMBText(kb int) string {
	mb := float64(kb) / 1024
	text := strconv.FormatFloat(mb, 'f', 3, 64)
	return strings.TrimRight(strings.TrimRight(text, "0"), ".")
}

// fillFromForm is what a rejected save does: keep exactly what was typed.
func (d *editData) fillFromForm(f problemForm, errs []string) {
	d.PID, d.Title, d.Statement = f.PID, f.Title, f.Statement
	d.Detail = detailLevel(f.Detail)
	d.TimeMS, d.Visible = f.TimeMS, f.Visible
	d.MemMB = strings.TrimRight(strings.TrimRight(strconv.FormatFloat(f.MemMB, 'f', 3, 64), "0"), ".")
	d.Errors = errs
	for i := range d.Langs {
		d.Langs[i].Checked = false
	}
	for _, l := range f.Langs {
		found := false
		for i := range d.Langs {
			if d.Langs[i].Value == l {
				d.Langs[i].Checked, found = true, true
			}
		}
		if !found {
			d.Langs = append(d.Langs, editRow{Value: l, Label: l, Checked: true})
		}
	}
}

// authoredBy checks the right to touch a problem and sends an anonymous browser
// to the login page first, because "403" is not an answer a person can act on.
func (s *Server) authoredBy(w http.ResponseWriter, r *http.Request, p store.Problem, next string) (actor, bool) {
	a, err := s.authenticate(r)
	if err != nil || a.User.ID == 0 {
		http.Redirect(w, r, "/login?next="+url.QueryEscape(next), http.StatusSeeOther)
		return a, false
	}
	if !a.canEditProblem(p) {
		s.respondError(w, r, http.StatusForbidden, "这道题由他人创建，需要 "+permProblemEditAny+" 才能修改")
		return a, false
	}
	return a, true
}

func (s *Server) handleProblemNew(w http.ResponseWriter, r *http.Request) {
	a, err := s.authenticate(r)
	if err != nil || a.User.ID == 0 {
		http.Redirect(w, r, "/login?next=/problems/new", http.StatusSeeOther)
		return
	}
	if !a.can(permProblemCreate) {
		s.respondError(w, r, http.StatusForbidden, "缺少权限："+permProblemCreate)
		return
	}
	d := newEditData(store.Problem{}, true)
	s.page(w, r, http.StatusOK, "新建题目", "problem_edit", a.viewer(), msgOf(r), d)
}

func (s *Server) handleProblemCreate(w http.ResponseWriter, r *http.Request) {
	a, err := s.authenticate(r)
	if err != nil || a.User.ID == 0 {
		http.Redirect(w, r, "/login?next=/problems/new", http.StatusSeeOther)
		return
	}
	if !a.can(permProblemCreate) {
		s.respondError(w, r, http.StatusForbidden, "缺少权限："+permProblemCreate)
		return
	}
	// 建题比提交更贵：每题都带出用例目录与文件。批量导入题库走的是 zip 与用例页的批量表，
	// 不需要在这里一秒建十几题。
	if s.cfg.ProblemPerMinute > 0 {
		key := "problem:user:" + strconv.FormatInt(a.User.ID, 10)
		if allowed, err := s.db.RateAllowed(r.Context(), key, rateMinute, s.cfg.ProblemPerMinute); err != nil {
			s.writeStoreError(w, r, err)
			return
		} else if !allowed {
			s.errorPage(w, r, http.StatusTooManyRequests, fmt.Sprintf(
				"建题太快了：一人每分钟最多 %d 题，等一分钟再试。批量导入请走用例页的 zip。",
				s.cfg.ProblemPerMinute))
			return
		}
		if _, err := s.db.RateHit(r.Context(), key, rateMinute); err != nil {
			s.writeStoreError(w, r, err)
			return
		}
	}
	f, errs := parseProblemForm(r)
	d := newEditData(store.Problem{}, true)
	d.fillFromForm(f, errs)
	if r.FormValue("preview") == "1" {
		d.Preview = renderMarkdownHTML(f.Statement)
		s.page(w, r, http.StatusOK, "新建题目", "problem_edit", a.viewer(), "", d)
		return
	}
	if len(errs) > 0 {
		s.page(w, r, http.StatusBadRequest, "新建题目", "problem_edit", a.viewer(), "", d)
		return
	}

	var p store.Problem
	f.apply(&p)
	p.OwnerID = a.User.ID
	created, err := s.db.CreateProblem(r.Context(), &p)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			d.fillFromForm(f, []string{"编号 " + f.PID + " 已经存在"})
			s.page(w, r, http.StatusConflict, "新建题目", "problem_edit", a.viewer(), "", d)
			return
		}
		s.writeStoreError(w, r, err)
		return
	}
	s.log.Info("problem created", "pid", created.PID, "id", created.ID, "by", a.User.Handle)
	http.Redirect(w, r, "/problems/"+url.PathEscape(created.PID)+"/cases?msg="+url.QueryEscape("题目已建好，现在加用例"), http.StatusSeeOther)
}

// problemForEdit resolves the {key} of an authoring URL. The numeric id is used
// for the form action so a pid rename cannot strand the browser on the old URL.
func (s *Server) problemForEdit(w http.ResponseWriter, r *http.Request) (actor, store.Problem, bool) {
	p, err := s.db.FindProblem(r.Context(), r.PathValue("key"))
	if err != nil {
		s.writeStoreError(w, r, err)
		return actor{}, p, false
	}
	a, ok := s.authoredBy(w, r, p, r.URL.Path)
	return a, p, ok
}

func (s *Server) handleProblemEdit(w http.ResponseWriter, r *http.Request) {
	a, p, ok := s.problemForEdit(w, r)
	if !ok {
		return
	}
	d, err := s.editDataFor(r, p, false)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	s.page(w, r, http.StatusOK, "编辑 "+p.PID, "problem_edit", a.viewer(), msgOf(r), d)
}

// editDataFor adds the case counts the editor shows next to the statement, so
// the author can see that a problem with no judged cases cannot be judged.
func (s *Server) editDataFor(r *http.Request, p store.Problem, isNew bool) (editData, error) {
	d := newEditData(p, isNew)
	cases, err := s.db.ListTestcases(r.Context(), p.ID, false)
	if err != nil {
		return d, err
	}
	d.Cases = len(cases)
	for _, tc := range cases {
		if !tc.IsSample {
			d.Judged++
		}
	}
	d.Total, err = s.db.ProblemScoreTotal(r.Context(), p.ID)
	return d, err
}

func (s *Server) handleProblemUpdate(w http.ResponseWriter, r *http.Request) {
	a, p, ok := s.problemForEdit(w, r)
	if !ok {
		return
	}
	f, errs := parseProblemForm(r)
	d, err := s.editDataFor(r, p, false)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	d.fillFromForm(f, errs)
	if r.FormValue("preview") == "1" {
		d.Preview = renderMarkdownHTML(f.Statement)
		s.page(w, r, http.StatusOK, "编辑 "+p.PID, "problem_edit", a.viewer(), "", d)
		return
	}
	// Flipping visibility is publishing, a separate capability from writing the
	// statement: an author cannot quietly put their problem in front of everyone.
	if p.Visible != f.Visible && !a.can(permProblemPublish) {
		d.fillFromForm(f, append(errs, "改可见性需要权限 "+permProblemPublish))
		s.page(w, r, http.StatusForbidden, "编辑 "+p.PID, "problem_edit", a.viewer(), "", d)
		return
	}
	if len(errs) > 0 {
		s.page(w, r, http.StatusBadRequest, "编辑 "+p.PID, "problem_edit", a.viewer(), "", d)
		return
	}

	f.apply(&p)
	updated, err := s.db.UpdateProblem(r.Context(), p)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			d.fillFromForm(f, []string{"编号 " + f.PID + " 已经存在"})
			s.page(w, r, http.StatusConflict, "编辑 "+p.PID, "problem_edit", a.viewer(), "", d)
			return
		}
		s.writeStoreError(w, r, err)
		return
	}
	s.log.Info("problem updated", "pid", updated.PID, "by", a.User.Handle)
	http.Redirect(w, r, "/problems/"+url.PathEscape(updated.PID)+"?msg="+url.QueryEscape("已保存"), http.StatusSeeOther)
}

func (s *Server) handleProblemDelete(w http.ResponseWriter, r *http.Request) {
	a, p, ok := s.problemForEdit(w, r)
	if !ok {
		return
	}
	if err := s.db.DeleteProblem(r.Context(), p.ID); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	// 跟清空用例一样，记在删文件之前：题这时候已经没了，文件系统后面出不出错都改不了
	// "谁删了这道题"。
	s.audit(r, a, "problem.delete", p.PID, "已删除题目：「"+p.Title+"」")
	// The row is gone first; if the files could not be removed the author has to
	// be told, because an orphaned directory is invisible in the UI.
	if err := s.cases.RemoveProblem(p.ID); err != nil {
		s.log.Error("case files after delete", "pid", p.PID, "err", err)
		http.Redirect(w, r, "/problems/"+url.PathEscape(p.PID)+"?msg="+url.QueryEscape("题目已删除，但用例文件没删掉："+err.Error()), http.StatusSeeOther)
		return
	}
	s.log.Info("problem deleted", "pid", p.PID, "id", p.ID, "by", a.User.Handle)
	http.Redirect(w, r, "/problems?msg="+url.QueryEscape("已删除 "+p.PID), http.StatusSeeOther)
}
