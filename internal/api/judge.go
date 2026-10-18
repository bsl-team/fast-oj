package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/liangbinchen2013/fast-oj/internal/judge"
	"github.com/liangbinchen2013/fast-oj/internal/protocol"
	"github.com/liangbinchen2013/fast-oj/internal/store"
)

const maxJudgeRuns = 4096

func validJudgeName(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '_', r == '-', r == '.':
		default:
			return false
		}
	}
	return true
}

// registerJudge folds the heartbeat into every claim, so a box that only knows
// how to poll still shows up in the box list.
func (s *Server) registerJudge(r *http.Request, g protocol.Greeting) (store.Judge, error) {
	g.Name = strings.TrimSpace(g.Name)
	if !validJudgeName(g.Name) {
		return store.Judge{}, fmt.Errorf("judge name must be 1..64 of [A-Za-z0-9_.-]")
	}
	for _, l := range g.Languages {
		if !validLanguageTag(l) {
			return store.Judge{}, fmt.Errorf("bad language tag %q", l)
		}
	}
	return s.db.UpsertJudge(r.Context(), g.Name, g.Languages, g.Note)
}

func (s *Server) handleJudgeClaim(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireJudge(r); err != nil {
		s.writeAuthError(w, r, err)
		return
	}
	var g protocol.Greeting
	if !s.decodeBody(w, r, &g) {
		return
	}
	j, err := s.registerJudge(r, g)
	if err != nil {
		s.respondError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	task, err := s.db.ClaimNext(r.Context(), j.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"queue": "empty", "judge": j.Name})
		return
	}
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}

	cases, err := s.db.ListTestcases(r.Context(), task.ProblemID, false)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	programs, err := s.db.JudgePrograms(r.Context(), task.ProblemID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	work := protocol.Task{
		SubmissionID: task.SubmissionID, PID: task.PID, Language: task.Language,
		Options: task.Options,
		Source:  task.Source, TimeLimitMS: task.TimeLimitMS, MemLimitKB: task.MemLimitKB,
	}
	// 判题器与交互器跟着工单走：判机必须用**自己那套工具链**现编它们，别处编好的二进制
	// 带着另一套 libc，判出来的东西和判这台机器上学生程序的不是同一件事。
	if p, ok := programs[store.ProgramChecker]; ok {
		work.Checker = &protocol.AuxProgram{Language: p.Language, Source: p.Source}
	}
	if p, ok := programs[store.ProgramInteractor]; ok {
		work.Interactor = &protocol.AuxProgram{Language: p.Language, Source: p.Source}
	}
	for _, tc := range cases {
		work.Cases = append(work.Cases, protocol.Case{
			ID: tc.ID, Name: tc.Name, Ordinal: tc.Ordinal, IsSample: tc.IsSample, Score: tc.Score,
		})
	}

	s.log.Info("claimed", "sid", task.SubmissionID, "judge", j.Name, "pid", task.PID)
	writeJSON(w, http.StatusOK, work)
}

// handleJudgeCaseFile streams case data to a judge box. Unlike the public case
// route this also serves judged (non-sample) cases.
func (s *Server) handleJudgeCaseFile(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireJudge(r); err != nil {
		s.writeAuthError(w, r, err)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.respondError(w, r, http.StatusBadRequest, "case id must be a positive number")
		return
	}
	tc, err := s.db.GetTestcaseByID(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	which := r.PathValue("which")
	rel := tc.OutputRel
	switch which {
	case "in":
		rel = tc.InputRel
	case "out":
	default:
		s.respondError(w, r, http.StatusBadRequest, "which must be in or out")
		return
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

// resolveJudgeOwner looks up the box that must own the row before it may report.
func (s *Server) resolveJudgeOwner(w http.ResponseWriter, r *http.Request, name string, sub store.Submission) (store.Judge, bool) {
	j, err := s.db.GetJudge(r.Context(), strings.TrimSpace(name))
	if err != nil {
		s.respondError(w, r, http.StatusForbidden, "unknown judge box, claim the submission first")
		return store.Judge{}, false
	}
	if sub.JudgeID != j.ID {
		s.respondError(w, r, http.StatusForbidden,
			"judge "+j.Name+" does not own submission "+strconv.FormatInt(sub.ID, 10))
		return store.Judge{}, false
	}
	return j, true
}

func (s *Server) handleJudgeStatus(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireJudge(r); err != nil {
		s.writeAuthError(w, r, err)
		return
	}
	var req protocol.Status
	if !s.decodeBody(w, r, &req) {
		return
	}
	id, err := submissionID(r)
	if err != nil {
		s.respondError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	sub, err := s.db.GetSubmission(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	j, ok := s.resolveJudgeOwner(w, r, req.Name, sub)
	if !ok {
		return
	}

	to := judge.Status(req.State)
	if to != judge.Compiling && to != judge.Running {
		s.respondError(w, r, http.StatusBadRequest, "state must be compiling or running")
		return
	}
	if !judge.Transition(judge.Status(sub.Status), to) {
		s.respondError(w, r, http.StatusConflict,
			fmt.Sprintf("cannot move submission %d from %s to %s", id, sub.Status, to))
		return
	}
	var progress *store.Run
	if req.Run != nil {
		v := judge.VerdictName(req.Run.Verdict)
		// VerdictName folds the unknown into IE, so compare against the raw string
		// to tell "the judge sent IE" apart from "the judge sent garbage".
		if string(v) != req.Run.Verdict || req.Run.Ordinal <= 0 {
			s.respondError(w, r, http.StatusBadRequest, "progress row needs a known verdict and a positive ordinal")
			return
		}
		progress = &store.Run{
			Ordinal: req.Run.Ordinal, CaseName: clip(req.Run.CaseName, 64), Verdict: string(v),
			TimeMS: req.Run.TimeMS, MemKB: req.Run.MemKB, IsSample: req.Run.IsSample,
			TimeMin: req.Run.TimeMin, MemMin: req.Run.MemMin,
		}
	}
	ok, err = s.db.ReportProgress(r.Context(), id, j.ID, sub.Status, string(to), req.Detail, progress)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if !ok {
		s.respondError(w, r, http.StatusConflict, "submission state changed under the judge")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sid": id, "state": string(to)})
}

func (s *Server) handleJudgeResult(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireJudge(r); err != nil {
		s.writeAuthError(w, r, err)
		return
	}
	var req protocol.Result
	// A report carries a row per case, so it gets a larger budget than a normal
	// JSON body.
	if !s.decodeBodyLimit(w, r, &req, s.cfg.MaxUpload) {
		return
	}
	if len(req.Runs) > maxJudgeRuns {
		s.respondError(w, r, http.StatusBadRequest, "too many case rows")
		return
	}
	id, err := submissionID(r)
	if err != nil {
		s.respondError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	sub, err := s.db.GetSubmission(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	j, ok := s.resolveJudgeOwner(w, r, req.Name, sub)
	if !ok {
		return
	}
	if !judge.Transition(judge.Status(sub.Status), judge.Done) {
		s.respondError(w, r, http.StatusConflict,
			fmt.Sprintf("submission %d is already %s", id, sub.Status))
		return
	}

	cases, err := s.db.ListTestcases(r.Context(), sub.ProblemID, false)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	verdict, runs, score, total, failed := tallyResult(judge.VerdictName(req.Verdict), req.Runs, cases)
	// CE and IE have no failing case to name, so keep whatever the box labelled.
	if failed == "" {
		failed = req.FailedCase
	}

	if err := s.db.RecordResult(r.Context(), id, store.Result{
		Verdict: string(verdict), TimeMS: req.TimeMS, MemKB: req.MemKB,
		TimeMin: req.TimeMin, MemMin: req.MemMin, Score: score,
		TotalScore: total, FailedCase: failed, Detail: req.Detail,
	}, runs); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	s.log.Info("judged", "sid", id, "judge", j.Name, "verdict", string(verdict),
		"score", fmt.Sprintf("%d/%d", score, total), "time_ms", req.TimeMS)
	writeJSON(w, http.StatusOK, map[string]any{"sid": id, "verdict": string(verdict),
		"score": score, "total_score": total, "failed_case": failed, "cases": len(runs)})
}

// tallyResult treats the judge as untrusted arithmetic: the score is recomputed
// from the per-case rows against the stored case weights, and an AC claim whose
// rows contain a failure is downgraded to that failure.
//
// Bundling is applied here rather than in the judge: a group pays only when every
// case declared in it came back AC, so a box that skips the hard case of a group
// earns nothing from the easy ones. Each row also records what it actually earned,
// which is how the page can say "这条过了，但这组没给分".
func tallyResult(claimed judge.Verdict, reqRuns []protocol.RunRow, cases []store.Testcase) (judge.Verdict, []store.Run, int, int, string) {
	byName := make(map[string]store.Testcase, len(cases))
	// judgedByName holds only the scored cases, in the order they were declared.
	var judged []store.Testcase
	groupMembers := map[string][]string{}
	total := 0
	for _, tc := range cases {
		byName[tc.Name] = tc
		if tc.IsSample {
			continue
		}
		judged = append(judged, tc)
		total += tc.Score
		if tc.GroupName != "" {
			groupMembers[tc.GroupName] = append(groupMembers[tc.GroupName], tc.Name)
		}
	}

	ac := make(map[string]bool, len(reqRuns))
	clean := make(map[string]bool, len(reqRuns))
	for _, rr := range reqRuns {
		if _, known := byName[rr.CaseName]; !known {
			continue
		}
		clean[rr.CaseName] = true
		if judge.VerdictName(rr.Verdict) == judge.AC {
			ac[rr.CaseName] = true
		}
	}
	// A group pays only when all of its cases were reported *and* all of them are
	// AC; a missing row is a failure, not a pass.
	groupOK := make(map[string]bool, len(groupMembers))
	for name, members := range groupMembers {
		groupOK[name] = true
		for _, m := range members {
			if !ac[m] {
				groupOK[name] = false
				break
			}
		}
	}

	// earned is the points a case contributes given the bundle rules.
	earned := func(tc store.Testcase) int {
		if !ac[tc.Name] {
			return 0
		}
		if tc.GroupName != "" && !groupOK[tc.GroupName] {
			return 0
		}
		return tc.Score
	}

	verdict := claimed
	score := 0
	runs := make([]store.Run, 0, len(reqRuns))
	for i, rr := range reqRuns {
		v := judge.VerdictName(rr.Verdict)
		tc, known := byName[rr.CaseName]
		sample := rr.IsSample
		if known {
			sample = tc.IsSample
		}
		ordinal := rr.Ordinal
		if ordinal == 0 {
			ordinal = i + 1
		}
		points := 0
		if known && !sample {
			points = earned(tc)
			score += points
		}
		runs = append(runs, store.Run{
			Ordinal: ordinal, CaseName: rr.CaseName, Verdict: string(v), Score: points,
			TimeMS: rr.TimeMS, MemKB: rr.MemKB, IsSample: sample,
			TimeMin: rr.TimeMin, MemMin: rr.MemMin,
		})
	}

	// The first judged case that did not pass is the one worth naming, whatever the
	// box claimed: a sample mismatch alone does not become the reported case, and
	// the passing rows of a broken bundle are guilty of nothing but being in it.
	failed := ""
	for _, rr := range reqRuns {
		v := judge.VerdictName(rr.Verdict)
		if v == judge.AC {
			continue
		}
		if claimed == judge.AC && verdict == judge.AC {
			verdict = v
		}
		tc, known := byName[rr.CaseName]
		switch {
		case known && !tc.IsSample:
			failed = tc.Name
		case !known && failed == "" && claimed == judge.AC:
			// A row for a case that is gone cannot certify AC either, though a
			// judged row further down would be the better name.
			failed = rr.CaseName
		}
		if failed != "" {
			break
		}
	}
	if claimed == judge.AC && failed == "" && score != total {
		// Every reported row passed, so the gap is a skipped case or a bundle
		// broken by a case that was never reported.
		for _, tc := range judged {
			if !clean[tc.Name] {
				verdict, failed = judge.WA, tc.Name
				break
			}
		}
		if failed == "" {
			for _, tc := range judged {
				if earned(tc) != tc.Score {
					verdict, failed = judge.WA, tc.Name
					break
				}
			}
		}
	}
	// CE/IE fail the whole run without a judged case being implicated, so fall
	// back to the first row that did not pass.
	if failed == "" && verdict != judge.AC {
		for _, rr := range reqRuns {
			if judge.VerdictName(rr.Verdict) != judge.AC {
				failed = rr.CaseName
				break
			}
		}
	}
	return verdict, runs, score, total, failed
}

func submissionID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("submission id must be a positive number")
	}
	return id, nil
}

// judgeRow is one box in the registry the operator watches.
type judgeRow struct {
	ID        int64
	Name      string
	Languages string
	Seen      string
	Note      string
}

func (s *Server) handleJudgeList(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePerm(w, r, permJudgeView); !ok {
		return
	}
	judges, err := s.db.ListJudges(r.Context())
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	rows := make([]judgeRow, 0, len(judges))
	for _, j := range judges {
		rows = append(rows, judgeRow{
			ID: j.ID, Name: j.Name, Languages: joinOr(j.Languages, "不限"), Seen: ago(j.LastSeenAt), Note: j.Note,
		})
	}
	s.page(w, r, http.StatusOK, "判机", "judges", s.viewerOf(r), msgOf(r), map[string]any{"Rows": rows})
}
