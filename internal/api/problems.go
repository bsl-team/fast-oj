package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/liangbinchen2013/fast-oj/internal/store"
)

// problemRequest uses pointers so an omitted field means "leave alone" on PUT
// and "use the default" on POST.
type problemRequest struct {
	PID         string   `json:"pid"`
	Title       string   `json:"title"`
	Statement   string   `json:"statement"`
	TimeLimitMS *int     `json:"time_limit_ms"`
	MemLimitKB  *int     `json:"mem_limit_kb"`
	Languages   []string `json:"languages"`
	Visible     *bool    `json:"visible"`
}

func validLanguageTag(s string) bool {
	if s == "" || len(s) > 16 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '+', r == '#', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

func (req *problemRequest) apply(p *store.Problem, isCreate bool) error {
	if isCreate {
		p.TimeLimitMS, p.MemLimitKB, p.Visible = 1000, 262144, true
	}
	if req.PID != "" {
		p.PID = strings.TrimSpace(req.PID)
	}
	if req.Title != "" {
		p.Title = strings.TrimSpace(req.Title)
	}
	if req.Statement != "" || isCreate {
		p.Statement = req.Statement
	}
	if req.TimeLimitMS != nil {
		p.TimeLimitMS = *req.TimeLimitMS
	}
	if req.MemLimitKB != nil {
		p.MemLimitKB = *req.MemLimitKB
	}
	if req.Languages != nil {
		p.Languages = req.Languages
	}
	if req.Visible != nil {
		p.Visible = *req.Visible
	}

	if p.PID == "" || len(p.PID) > 32 {
		return errors.New("pid must be 1..32 characters")
	}
	if p.Title == "" || utf8.RuneCountInString(p.Title) > 200 {
		return errors.New("title must be 1..200 characters")
	}
	if p.TimeLimitMS <= 0 || p.TimeLimitMS > 600_000 {
		return errors.New("time_limit_ms must be 1..600000")
	}
	if p.MemLimitKB <= 0 || p.MemLimitKB > 4_194_304 {
		return errors.New("mem_limit_kb must be 1..4194304")
	}
	for _, l := range p.Languages {
		if !validLanguageTag(l) {
			return fmt.Errorf("bad language tag %q", l)
		}
	}
	return nil
}

func (s *Server) handleProblemCreate(w http.ResponseWriter, r *http.Request) {
	a, ok := s.requirePerm(w, r, permProblemCreate)
	if !ok {
		return
	}
	var req problemRequest
	if !s.decodeBody(w, r, &req) {
		return
	}
	var p store.Problem
	if err := req.apply(&p, true); err != nil {
		s.respondError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	// Authorship is what lets a problem setter keep editing their own problems
	// without holding a global edit right.
	p.OwnerID = a.User.ID
	created, err := s.db.CreateProblem(r.Context(), &p)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	s.log.Info("problem created", "pid", created.PID, "id", created.ID, "by", a.User.Handle)
	respond(w, r, http.StatusCreated, "created  "+problemRow(created, "-")+"\n", created)
}

func (s *Server) handleProblemUpdate(w http.ResponseWriter, r *http.Request) {
	a, p, ok := s.requireProblemEdit(w, r, r.PathValue("key"))
	if !ok {
		return
	}
	var req problemRequest
	if !s.decodeBody(w, r, &req) {
		return
	}
	if req.Visible != nil && *req.Visible != p.Visible && !a.can(permProblemPublish) {
		s.respondError(w, r, http.StatusForbidden, "缺少权限："+permProblemPublish)
		return
	}
	if err := req.apply(&p, false); err != nil {
		s.respondError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := s.db.UpdateProblem(r.Context(), p)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	s.log.Info("problem updated", "pid", updated.PID, "by", a.User.Handle)
	respond(w, r, http.StatusOK, "updated  "+problemRow(updated, "-")+"\n", updated)
}

func (s *Server) handleProblemDelete(w http.ResponseWriter, r *http.Request) {
	a, p, ok := s.requireProblemEdit(w, r, r.PathValue("key"))
	if !ok {
		return
	}
	if err := s.db.DeleteProblem(r.Context(), p.ID); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if err := s.cases.RemoveProblem(p.ID); err != nil {
		s.respondError(w, r, http.StatusInternalServerError, "deleted row but case files remain: "+err.Error())
		return
	}
	s.log.Info("problem deleted", "pid", p.PID, "by", a.User.Handle)
	respond(w, r, http.StatusOK, fmt.Sprintf("deleted %s (id=%d)\n", p.PID, p.ID),
		map[string]any{"deleted": p.PID, "id": p.ID})
}

func (s *Server) handleProblemList(w http.ResponseWriter, r *http.Request) {
	a, _ := s.authenticate(r)
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	limit := pageLimit(r.URL.Query().Get("limit"))
	search := r.URL.Query().Get("q")
	page, err := s.db.ListProblems(r.Context(), after, limit, search, !a.can(permProblemEditAny))
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if wantsHTML(r) {
		s.problemsPage(w, r, a.viewer(), page, limit, search)
		return
	}

	var b strings.Builder
	b.WriteString(problemHeader)
	ids := make([]int64, len(page.Items))
	for i, p := range page.Items {
		ids[i] = p.ID
	}
	counts, err := s.db.CaseCounts(r.Context(), ids)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	for _, p := range page.Items {
		b.WriteString(problemRow(p, strconv.Itoa(counts[p.ID])) + "\n")
	}
	fmt.Fprintf(&b, "shown=%d total=%d", len(page.Items), page.Total)
	if page.NextAfter != 0 {
		fmt.Fprintf(&b, " next_after=%d\n", page.NextAfter)
	} else {
		b.WriteString("\n")
	}
	respond(w, r, http.StatusOK, b.String(), page)
}

func (s *Server) handleProblemGet(w http.ResponseWriter, r *http.Request) {
	a, _ := s.authenticate(r)
	p, ok := s.problemByKey(w, r)
	if !ok {
		return
	}
	if !p.Visible && !a.canEditProblem(p) && !a.can(permProblemPublish) {
		s.respondError(w, r, http.StatusNotFound, "problem "+p.PID+" not found")
		return
	}
	if wantsHTML(r) {
		s.problemPage(w, r, a, p)
		return
	}

	cases, err := s.db.ListTestcases(r.Context(), p.ID, false)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	total, err := s.db.ProblemScoreTotal(r.Context(), p.ID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s  %s\n", p.PID, p.Title)
	fmt.Fprintf(&b, "id=%d  time=%s  mem=%s  languages=%s  visible=%v  score=%d\n",
		p.ID, formatMS(p.TimeLimitMS, false), formatKB(p.MemLimitKB, false),
		joinOr(p.Languages, "*"), p.Visible, total)
	b.WriteString("\n" + strings.TrimRight(p.Statement, "\n") + "\n")

	// Only sample data is public: hidden case sizes would leak the test set.
	samples := []store.Testcase{}
	for _, tc := range cases {
		if tc.IsSample {
			samples = append(samples, tc)
		}
	}
	for _, tc := range samples {
		input, errIn := s.cases.Read(tc.InputRel)
		output, errOut := s.cases.Read(tc.OutputRel)
		fmt.Fprintf(&b, "\n--- sample %s ---\n", tc.Name)
		if errIn != nil || errOut != nil {
			b.WriteString("(case file missing)\n")
			continue
		}
		b.WriteString(tildeBlock("input", input) + tildeBlock("output", output))
	}
	if len(samples) == 0 {
		b.WriteString("\n(no samples)\n")
	}
	respond(w, r, http.StatusOK, b.String(), map[string]any{
		"problem": p, "samples": samples, "score_total": total,
		"case_count": len(cases),
	})
}

const problemHeader = "pid        title                                    time     mem  cases\n"

func problemRow(p store.Problem, cases string) string {
	status := "pub"
	if !p.Visible {
		status = "hid"
	}
	return fmt.Sprintf("%-10s %-40s %8s %8s %5s  %s",
		p.PID, clip(p.Title, 40), formatMS(p.TimeLimitMS, false), formatKB(p.MemLimitKB, false), cases, status)
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// formatMS keeps every duration in milliseconds: a list that mixes "15ms" with
// "1s" makes the numbers impossible to scan side by side. A lower bound (the run
// was killed before it finished) is printed as ">1050ms" rather than passed off as
// a measurement.
func formatMS(ms int, min bool) string {
	if ms < 0 {
		return "-"
	}
	return prefixMin(strconv.Itoa(ms)+"ms", min)
}

// formatKB shows memory in K below a megabyte and in M above it, always to two
// decimals so 6.73046875M never sprawls across the column.
func formatKB(kb int, min bool) string {
	if kb < 0 {
		return "-"
	}
	if kb < 1024 {
		return prefixMin(strconv.Itoa(kb)+"K", min)
	}
	return prefixMin(strconv.FormatFloat(float64(kb)/1024, 'f', 2, 64)+"M", min)
}

func prefixMin(text string, min bool) string {
	if min {
		return ">" + text
	}
	return text
}

func joinOr(items []string, empty string) string {
	if len(items) == 0 {
		return empty
	}
	return strings.Join(items, ",")
}

func tildeBlock(label string, body []byte) string {
	var b strings.Builder
	fmt.Fprintf(&b, "~~~ %s\n", label)
	lines := strings.Split(strings.TrimRight(string(body), "\r\n"), "\n")
	for _, l := range lines {
		b.WriteString("  " + l + "\n")
	}
	b.WriteString("~~~ end\n")
	return b.String()
}

// problemByKey resolves /problems/{key} into a row or writes the error.
func (s *Server) problemByKey(w http.ResponseWriter, r *http.Request) (store.Problem, bool) {
	key := r.PathValue("key")
	p, err := s.db.FindProblem(r.Context(), key)
	if err != nil {
		s.writeStoreError(w, r, err)
		return store.Problem{}, false
	}
	return p, true
}

func (s *Server) decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	return s.decodeBodyLimit(w, r, dst, s.cfg.MaxSource)
}

func (s *Server) decodeBodyLimit(w http.ResponseWriter, r *http.Request, dst any, limit int64) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		s.respondError(w, r, http.StatusBadRequest, "unreadable body")
		return false
	}
	if int64(len(body)) > limit {
		s.respondError(w, r, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("body over %d bytes", limit))
		return false
	}
	if len(bytes.TrimSpace(body)) == 0 {
		s.respondError(w, r, http.StatusBadRequest, "empty body")
		return false
	}
	// Reject invalid UTF-8 at the boundary: json.Unmarshal would silently turn
	// each bad byte into U+FFFD and permanently corrupt statements on write.
	if !utf8.Valid(body) {
		s.respondError(w, r, http.StatusBadRequest, "body must be valid UTF-8 (Windows cmd: chcp 65001)")
		return false
	}
	if err := json.Unmarshal(body, dst); err != nil {
		s.respondError(w, r, http.StatusBadRequest, "bad json: "+err.Error())
		return false
	}
	return true
}

func (s *Server) writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.respondError(w, r, http.StatusNotFound, "not found")
	case errors.Is(err, store.ErrConflict):
		s.respondError(w, r, http.StatusConflict, err.Error())
	default:
		s.log.Error("store", "err", err, "path", r.URL.Path)
		s.respondError(w, r, http.StatusInternalServerError, "database error")
	}
}

// casePair is one zip-sourced test case.
type casePair struct {
	Name          string
	Input, Output []byte
}

// collectCases pairs <stem>.in with <stem>.out inside a zip archive. Entries are
// flattened to their base name, so a nested path cannot escape anywhere.
func (s *Server) collectCases(zipBody []byte) ([]casePair, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipBody), int64(len(zipBody)))
	if err != nil {
		return nil, fmt.Errorf("not a zip archive: %w", err)
	}
	byName := map[string]*casePair{}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		base := path.Base(f.Name)
		ext := strings.ToLower(path.Ext(base))
		stem := strings.TrimSuffix(base, path.Ext(base))
		if stem == "" || (ext != ".in" && ext != ".out") {
			continue
		}
		if f.UncompressedSize64 > uint64(s.cfg.MaxCase) {
			return nil, fmt.Errorf("case %s over %d bytes", base, s.cfg.MaxCase)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(io.LimitReader(rc, s.cfg.MaxCase+1))
		rc.Close()
		if err != nil {
			return nil, err
		}
		if int64(len(body)) > s.cfg.MaxCase {
			return nil, fmt.Errorf("case %s over %d bytes", base, s.cfg.MaxCase)
		}
		pair := byName[stem]
		if pair == nil {
			pair = &casePair{Name: stem}
			byName[stem] = pair
		}
		if ext == ".in" {
			pair.Input = body
		} else {
			pair.Output = body
		}
	}

	var missing, out []casePair
	for _, pair := range byName {
		if pair.Input == nil || pair.Output == nil {
			missing = append(missing, *pair)
			continue
		}
		out = append(out, *pair)
	}
	if len(missing) > 0 {
		names := make([]string, len(missing))
		for i, m := range missing {
			names[i] = m.Name
		}
		sort.Strings(names)
		return nil, fmt.Errorf("incomplete pairs (need both .in and .out): %s", strings.Join(names, ", "))
	}
	// Samples sort first so a sample-only re-run stays cheap.
	sort.Slice(out, func(i, j int) bool {
		si, sj := isSampleName(out[i].Name), isSampleName(out[j].Name)
		if si != sj {
			return si
		}
		return out[i].Name < out[j].Name
	})
	if len(out) == 0 {
		return nil, errors.New("no <name>.in / <name>.out pairs found")
	}
	return out, nil
}

func isSampleName(name string) bool {
	return strings.HasPrefix(strings.ToLower(name), "sample")
}

func (s *Server) handleCaseUpload(w http.ResponseWriter, r *http.Request) {
	_, p, ok := s.requireCaseAccess(w, r, r.PathValue("key"))
	if !ok {
		return
	}
	zipBody, ok := s.readUpload(w, r)
	if !ok {
		return
	}
	pairs, err := s.collectCases(zipBody)
	if err != nil {
		s.respondError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	existing, err := s.db.ListTestcases(r.Context(), p.ID, false)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	byName := map[string]store.Testcase{}
	for _, tc := range existing {
		byName[tc.Name] = tc
	}
	next := 0
	for _, tc := range existing {
		if tc.Ordinal > next {
			next = tc.Ordinal
		}
	}

	var added, replaced int
	for _, pair := range pairs {
		ordinal := next + 1
		score := 1
		if old, found := byName[pair.Name]; found {
			ordinal = old.Ordinal
			score = old.Score
			replaced++
		} else {
			next = ordinal
			added++
			if isSampleName(pair.Name) {
				score = 0
			}
		}
		relIn, relOut, err := s.cases.Put(p.ID, ordinal, pair.Input, pair.Output)
		if err != nil {
			s.respondError(w, r, http.StatusInternalServerError, "case write failed: "+err.Error())
			return
		}
		_, err = s.db.PutTestcase(r.Context(), &store.Testcase{
			ProblemID: p.ID, Ordinal: ordinal, Name: pair.Name,
			IsSample: isSampleName(pair.Name), Score: score,
			InputRel: relIn, OutputRel: relOut,
		})
		if err != nil {
			s.writeStoreError(w, r, err)
			return
		}
	}

	total, err := s.db.CountTestcases(r.Context(), p.ID, false)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	respond(w, r, http.StatusOK,
		fmt.Sprintf("%s: added=%d replaced=%d total=%d\n", p.PID, added, replaced, total),
		map[string]any{"pid": p.PID, "added": added, "replaced": replaced, "total": total})
}

// readUpload accepts a raw zip body, or a zip inside a multipart file part.
func (s *Server) readUpload(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	defer r.Body.Close()
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(s.cfg.MaxUpload); err != nil {
			s.respondError(w, r, http.StatusBadRequest, "bad multipart upload: "+err.Error())
			return nil, false
		}
		file, _, err := r.FormFile("cases")
		if err != nil {
			file, _, err = r.FormFile("file")
		}
		if err != nil {
			s.respondError(w, r, http.StatusBadRequest, "no file part named cases")
			return nil, false
		}
		defer file.Close()
		body, err := io.ReadAll(io.LimitReader(file, s.cfg.MaxUpload+1))
		if err != nil || int64(len(body)) > s.cfg.MaxUpload {
			s.respondError(w, r, http.StatusRequestEntityTooLarge, "upload too large")
			return nil, false
		}
		return body, true
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, s.cfg.MaxUpload+1))
	if err != nil {
		s.respondError(w, r, http.StatusBadRequest, "unreadable body")
		return nil, false
	}
	if int64(len(body)) > s.cfg.MaxUpload {
		s.respondError(w, r, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("upload over %d bytes", s.cfg.MaxUpload))
		return nil, false
	}
	if len(body) == 0 {
		s.respondError(w, r, http.StatusBadRequest, "empty upload")
		return nil, false
	}
	return body, true
}

func (s *Server) handleCaseList(w http.ResponseWriter, r *http.Request) {
	_, p, ok := s.requireCaseAccess(w, r, r.PathValue("key"))
	if !ok {
		return
	}
	cases, err := s.db.ListTestcases(r.Context(), p.ID, false)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	total, err := s.db.ProblemScoreTotal(r.Context(), p.ID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}

	var b strings.Builder
	b.WriteString("ord  name                  kind    score      in     out\n")
	for _, tc := range cases {
		inSize, _ := s.cases.Size(tc.InputRel)
		outSize, _ := s.cases.Size(tc.OutputRel)
		kind := "judged"
		if tc.IsSample {
			kind = "sample"
		}
		fmt.Fprintf(&b, "%-4d %-20s %-8s %5d %7d %7d\n",
			tc.Ordinal, clip(tc.Name, 20), kind, tc.Score, inSize, outSize)
	}
	fmt.Fprintf(&b, "cases=%d judged_points=%d\n", len(cases), total)
	respond(w, r, http.StatusOK, b.String(),
		map[string]any{"pid": p.PID, "cases": cases, "judged_points": total})
}

func (s *Server) handleCaseWipe(w http.ResponseWriter, r *http.Request) {
	_, p, ok := s.requireCaseAccess(w, r, r.PathValue("key"))
	if !ok {
		return
	}
	n, err := s.db.DeleteTestcases(r.Context(), p.ID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if err := s.cases.RemoveProblem(p.ID); err != nil {
		s.respondError(w, r, http.StatusInternalServerError, "metadata removed but files remain: "+err.Error())
		return
	}
	respond(w, r, http.StatusOK, fmt.Sprintf("%s: removed %d cases\n", p.PID, n),
		map[string]any{"pid": p.PID, "removed": n})
}

// handleCaseBody streams one case file. Sample data is public; judged data needs
// an admin token.
func (s *Server) handleCaseBody(w http.ResponseWriter, r *http.Request) {
	a, _ := s.authenticate(r)
	p, ok := s.problemByKey(w, r)
	if !ok {
		return
	}
	tc, err := s.db.GetTestcase(r.Context(), p.ID, r.PathValue("name"))
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if !tc.IsSample && !a.canSeeTestcases(p) {
		s.respondError(w, r, http.StatusForbidden, "case "+tc.Name+" is not a sample")
		return
	}
	rel := tc.OutputRel
	if r.PathValue("which") == "in" {
		rel = tc.InputRel
	}
	body, err := s.cases.Read(rel)
	if err != nil {
		s.respondError(w, r, http.StatusNotFound, "case file missing")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
