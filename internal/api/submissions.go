package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/liangbinchen2013/fast-oj/internal/store"
)

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

// detailView is how much of the judging a particular reader gets to see on the
// submission page. The problem's own 详情级别 sets the ceiling for everyone who is
// neither the author nor the operator, so a contest can hide the failing case
// without hiding the score.
type detailView struct {
	Source bool // the submitted code itself
	Runs   bool // the per-case table
	Perf   bool // per-case time and memory
	Diff   bool // what the judge compared, and which case failed
	Judge  bool // which box judged it
}

var seeEverything = detailView{Source: true, Runs: true, Perf: true, Diff: true, Judge: true}

func submissionView(a actor, d store.SubmissionDetail) detailView {
	own := a.User.ID != 0 && a.User.ID == d.UserID
	if a.can(permSubmissionViewAny) || a.can(permProblemEditAny) ||
		(d.ProblemOwner != 0 && a.User.ID == d.ProblemOwner) {
		// Whoever owns the problem, or holds the audit right, needs the whole
		// picture to act on it.
		return seeEverything
	}
	// Somebody else's submission stays a summary: the per-case detail is the
	// author's and the operator's business, never the public's.
	if !own {
		return detailView{}
	}
	// Your own code and score are yours whatever the level says; what the level
	// grades is what the judging revealed about it.
	view := gradeDetail(d.DetailLevel)
	view.Source = true
	return view
}

func gradeDetail(level string) detailView {
	view := detailView{}
	switch level {
	case store.DetailCase:
		view.Runs = true
	case store.DetailDiff, store.DetailFull:
		view.Runs, view.Perf, view.Diff = true, true, true
		if level == store.DetailFull {
			view.Judge = true
		}
	case store.DetailNone:
	default:
		// An unknown value falls back to the middle of the ladder rather than to
		// showing everything.
		view.Runs, view.Perf, view.Diff = true, true, true
	}
	return view
}

func (s *Server) handleSubmitGet(w http.ResponseWriter, r *http.Request) {
	a, _ := s.authenticate(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.respondError(w, r, http.StatusBadRequest, "submission id must be a positive number")
		return
	}
	d, err := s.db.SubmissionDetail(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}

	view := submissionView(a, d)
	var runs []store.Run
	var source, detail string
	if view.Runs || view.Source {
		// Source and judge detail stay out of the joined list view; they are
		// loaded only for the one row someone is allowed to read.
		source, detail, err = s.db.SubmissionPayload(r.Context(), d.ID)
		if err != nil {
			s.writeStoreError(w, r, err)
			return
		}
		if !view.Source {
			source = ""
		}
		if !view.Diff {
			detail = ""
		}
		d.Source, d.Detail = source, detail
	}
	if view.Runs {
		runs, err = s.db.ListRuns(r.Context(), d.ID)
		if err != nil {
			s.writeStoreError(w, r, err)
			return
		}
	}
	s.submissionPage(w, r, a, d, view, source, detail, runs)
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
	s.submissionsPage(w, r, a.viewer(), items, f.Limit, q.Get("user"), q.Get("problem"))
}
