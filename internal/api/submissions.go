package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/liangbinchen2013/fast-oj/internal/judge"
	"github.com/liangbinchen2013/fast-oj/internal/store"
)

type submissionRequest struct {
	Problem  string `json:"problem"`
	Language string `json:"language"`
	Source   string `json:"source"`
}

func languageAllowed(list []string, lang string) bool {
	if len(list) == 0 {
		return true
	}
	for _, l := range list {
		if strings.EqualFold(l, lang) {
			return true
		}
	}
	return false
}

func (s *Server) handleSubmitCreate(w http.ResponseWriter, r *http.Request) {
	a, err := s.authenticate(r)
	if err != nil {
		s.writeAuthError(w, r, err)
		return
	}
	var req submissionRequest
	if !s.decodeBody(w, r, &req) {
		return
	}
	key := strings.TrimSpace(req.Problem)
	lang := strings.ToLower(strings.TrimSpace(req.Language))
	if key == "" || lang == "" {
		s.respondError(w, r, http.StatusBadRequest, "problem and language are required")
		return
	}
	if strings.ContainsRune(req.Source, 0) {
		s.respondError(w, r, http.StatusBadRequest, "source must not contain NUL bytes")
		return
	}
	if len(strings.TrimSpace(req.Source)) == 0 {
		s.respondError(w, r, http.StatusBadRequest, "source is empty")
		return
	}

	p, err := s.db.FindProblem(r.Context(), key)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if !p.Visible && !a.canEditProblem(p) && !a.can(permProblemPublish) {
		s.respondError(w, r, http.StatusNotFound, "problem "+p.PID+" not found")
		return
	}
	if !languageAllowed(p.Languages, lang) {
		s.respondError(w, r, http.StatusBadRequest,
			fmt.Sprintf("problem %s accepts %s, not %s", p.PID, strings.Join(p.Languages, "/"), lang))
		return
	}
	total, err := s.db.ProblemScoreTotal(r.Context(), p.ID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if total == 0 {
		s.respondError(w, r, http.StatusConflict, "problem "+p.PID+" has no judged test cases yet")
		return
	}

	sub, err := s.db.CreateSubmission(r.Context(), &store.Submission{
		UserID: a.User.ID, ProblemID: p.ID, Language: lang, Source: req.Source, TotalScore: total,
	})
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	s.log.Info("submitted", "sid", sub.ID, "user", a.User.Handle, "pid", p.PID, "lang", lang)
	respond(w, r, http.StatusCreated,
		fmt.Sprintf("submitted sid=%d problem=%s lang=%s status=queued\n", sub.ID, p.PID, lang),
		map[string]any{"submission": sub, "pid": p.PID})
}

// parseWait turns ?wait=45 / ?wait=45s into a budget, clamped so one polling
// client cannot pin a connection forever.
func parseWait(raw string) time.Duration {
	if raw == "" {
		return 0
	}
	const max = 5 * time.Minute
	if d, err := time.ParseDuration(raw); err == nil {
		switch {
		case d <= 0:
			return 0
		case d > max:
			return max
		default:
			return d
		}
	}
	if secs, err := strconv.Atoi(raw); err == nil {
		return parseWait(strconv.Itoa(secs) + "s")
	}
	return 0
}

func (s *Server) awaitDetail(r *http.Request, id int64, wait time.Duration) (store.SubmissionDetail, error) {
	deadline := time.Now().Add(wait)
	for {
		d, err := s.db.SubmissionDetail(r.Context(), id)
		if err != nil {
			return d, err
		}
		if !judge.Status(d.Status).InFlight() || !time.Now().Before(deadline) {
			return d, nil
		}
		select {
		case <-r.Context().Done():
			return d, r.Context().Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (s *Server) handleSubmitGet(w http.ResponseWriter, r *http.Request) {
	a, _ := s.authenticate(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.respondError(w, r, http.StatusBadRequest, "submission id must be a positive number")
		return
	}
	d, err := s.awaitDetail(r, id, parseWait(r.URL.Query().Get("wait")))
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}

	full := a.can(permSubmissionViewAny) || (a.User.ID != 0 && a.User.ID == d.UserID)
	var runs []store.Run
	var source, detail string
	if full {
		// Source and judge detail stay out of the joined list view; they are
		// loaded only for the one row someone is allowed to read.
		source, detail, err = s.db.SubmissionPayload(r.Context(), d.ID)
		if err != nil {
			s.writeStoreError(w, r, err)
			return
		}
		d.Source, d.Detail = source, detail
		runs, err = s.db.ListRuns(r.Context(), d.ID)
		if err != nil {
			s.writeStoreError(w, r, err)
			return
		}
	}
	if wantsHTML(r) {
		s.submissionPage(w, r, a, d, full, source, detail, runs)
		return
	}
	respond(w, r, http.StatusOK, submissionText(d, runs, full),
		map[string]any{"submission": d, "runs": runs})
}

func submissionText(d store.SubmissionDetail, runs []store.Run, full bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "sid=%d  problem=%s  %s\n", d.ID, d.PID, clip(d.Title, 60))
	fmt.Fprintf(&b, "user=%s  lang=%s  status=%s", d.Handle, d.Language, d.Status)
	if d.Verdict != "" {
		fmt.Fprintf(&b, "  verdict=%s", d.Verdict)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "time=%s  mem=%s  score=%d/%d  submitted=%s ago",
		formatMS(d.TimeMS), formatKB(d.MemKB), d.Score, d.TotalScore, ago(d.CreatedAt))
	if d.FailedCase != "" {
		fmt.Fprintf(&b, "  failed_case=%s", d.FailedCase)
	}
	if d.JudgeID != 0 {
		fmt.Fprintf(&b, "  judge=%d", d.JudgeID)
	}
	b.WriteString("\n")

	if !full {
		if d.Status != string(judge.Queued) {
			b.WriteString("(case details hidden: not your submission)\n")
		}
		return b.String()
	}
	if len(runs) > 0 {
		b.WriteString("ord kind   case                 verdict     time      mem\n")
		for _, run := range runs {
			kind := "judged"
			if run.IsSample {
				kind = "sample"
			}
			fmt.Fprintf(&b, "%-4d %-8s %-18s %-7s %8s %8s\n",
				run.Ordinal, kind, clip(run.CaseName, 18), run.Verdict,
				formatMS(run.TimeMS), formatKB(run.MemKB))
		}
	}
	if d.Detail != "" {
		b.WriteString("\n~~~ detail\n")
		for _, line := range strings.Split(strings.TrimRight(d.Detail, "\r\n"), "\n") {
			b.WriteString("  " + line + "\n")
		}
		b.WriteString("~~~ end\n")
	}
	return b.String()
}

func ago(ms int64) string {
	if ms == 0 {
		return "-"
	}
	d := time.Since(time.UnixMilli(ms))
	if d < 0 {
		return "0s"
	}
	return d.Truncate(time.Second).String()
}

// pageLimit mirrors the store-side clamp so the next_after footer stays exact.
func pageLimit(raw string) int {
	n, _ := strconv.Atoi(raw)
	switch {
	case n <= 0:
		return 50
	case n > 500:
		return 500
	}
	return n
}

func (s *Server) handleSubmitList(w http.ResponseWriter, r *http.Request) {
	a, _ := s.authenticate(r)
	q := r.URL.Query()
	var f store.DetailFilter
	f.Limit = pageLimit(q.Get("limit"))
	f.AfterID, _ = strconv.ParseInt(q.Get("after"), 10, 64)

	switch who := q.Get("user"); who {
	case "":
	case "me":
		if a.User.ID == 0 {
			s.writeAuthError(w, r, errNoCredentials)
			return
		}
		f.UserID = a.User.ID
	default:
		u, err := s.db.GetUserByHandle(r.Context(), who)
		if err != nil {
			s.writeStoreError(w, r, err)
			return
		}
		f.UserID = u.ID
	}
	if key := q.Get("problem"); key != "" {
		p, err := s.db.FindProblem(r.Context(), key)
		if err != nil {
			s.writeStoreError(w, r, err)
			return
		}
		f.ProblemID = p.ID
	}

	items, err := s.db.ListSubmissionDetails(r.Context(), f)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if wantsHTML(r) {
		s.submissionsPage(w, r, a.viewer(), items, f.Limit, q.Get("user"), q.Get("problem"))
		return
	}

	var b strings.Builder
	b.WriteString(submissionListHeader)
	for _, d := range items {
		b.WriteString(submissionListRow(d) + "\n")
	}
	fmt.Fprintf(&b, "shown=%d", len(items))
	if len(items) == f.Limit && len(items) > 0 {
		fmt.Fprintf(&b, " next_after=%d", items[len(items)-1].ID)
	}
	b.WriteString("\n")
	respond(w, r, http.StatusOK, b.String(), items)
}

const submissionListHeader = "sid    user        problem       lang   status     verdict   time      mem   score\n"

func submissionListRow(d store.SubmissionDetail) string {
	verdict := d.Verdict
	if verdict == "" {
		verdict = "-"
	}
	return fmt.Sprintf("%-6d %-11s %-13s %-6s %-10s %-9s %8s %6s %d/%d",
		d.ID, d.Handle, d.PID, d.Language, d.Status, verdict,
		formatMS(d.TimeMS), formatKB(d.MemKB), d.Score, d.TotalScore)
}
