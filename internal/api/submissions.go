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
	s.submissionPage(w, r, a, d, full, source, detail, runs)
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



