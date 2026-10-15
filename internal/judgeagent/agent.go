// Package judgeagent is the other half of the wire protocol: it pulls work from
// the server, runs it locally and reports back. Keeping it importable means the
// whole loop can be tested without spawning a second process.
package judgeagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/liangbinchen2013/fast-oj/internal/judge"
	"github.com/liangbinchen2013/fast-oj/internal/protocol"
)

type Config struct {
	Server string
	Token  string
	Name   string
	Dir    string
	// Poll is how long to wait before asking again when the queue is empty.
	Poll time.Duration
	// Backoff caps the retry wait after a failure.
	Backoff time.Duration
	Tools   map[string]judge.Tool
	// RunAs drops each submission to this uid/gid (Linux and containers only).
	// The compile step keeps the judge's own identity so it can write the binary.
	RunAs *judge.DropTo
	// Languages to advertise; defaults to the keys of Tools.
	Languages []string
	// Note is what /judges shows about this box: whether judged runs are actually
	// jailed, which is the operator's first question about a judge.
	Note string
	Logf func(format string, args ...any)
}

type Agent struct {
	cfg  Config
	http *http.Client
}

func New(cfg Config) (*Agent, error) {
	switch {
	case cfg.Server == "":
		return nil, errors.New("server URL is required")
	case cfg.Token == "":
		return nil, errors.New("judge token is required")
	case cfg.Name == "":
		return nil, errors.New("judge name is required")
	}
	if cfg.Poll <= 0 {
		cfg.Poll = 500 * time.Millisecond
	}
	if cfg.Backoff <= 0 {
		cfg.Backoff = 3 * time.Second
	}
	if cfg.Tools == nil {
		cfg.Tools = judge.Available(judge.DefaultTools())
	}
	if len(cfg.Languages) == 0 {
		for name := range cfg.Tools {
			cfg.Languages = append(cfg.Languages, name)
		}
		sort.Strings(cfg.Languages)
	}
	if len(cfg.Tools) == 0 {
		return nil, errors.New("no usable toolchain found (need g++ on PATH)")
	}
	if cfg.Dir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			base = os.TempDir()
		}
		cfg.Dir = filepath.Join(base, "fast-oj-judge")
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	// No client-wide timeout: the deadline belongs to each request's context, so
	// a long compile is not cut off by a global default.
	return &Agent{cfg: cfg, http: &http.Client{}}, nil
}

func (a *Agent) Languages() []string { return a.cfg.Languages }

// Dir 是每次运行的工作目录所在，启动时的沙盒自测要用它。
func (a *Agent) Dir() string { return a.cfg.Dir }

// SetNote 在自测之后补一句给 /judges 看的话：沙盒到底开没开，只有真建过一次才知道。
func (a *Agent) SetNote(note string) { a.cfg.Note = note }

func (a *Agent) greeting() protocol.Greeting {
	return protocol.Greeting{Name: a.cfg.Name, Languages: a.cfg.Languages, Note: a.cfg.Note}
}

// request sends an authenticated call and returns the status plus raw body.
func (a *Agent) request(ctx context.Context, method, path string, in any, out any) (int, error) {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.cfg.Server+path, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+a.cfg.Token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	resp, err := a.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return resp.StatusCode, err
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, fmt.Errorf("%s %s: %w", method, path, err)
		}
	} else if out != nil {
		return resp.StatusCode, fmt.Errorf("%s %s: empty body", method, path)
	}
	if resp.StatusCode >= 400 {
		return resp.StatusCode, fmt.Errorf("%s %s: %s", method, path, firstLine(raw))
	}
	return resp.StatusCode, nil
}

func firstLine(raw []byte) string {
	s := string(raw)
	if i := indexOf(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

func indexOf(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// Claim takes the next submission, or nil when the queue is empty. The same call
// doubles as the heartbeat that keeps this box listed on the server.
func (a *Agent) Claim(ctx context.Context) (*protocol.Task, error) {
	var task protocol.Task
	code, err := a.request(ctx, http.MethodPost, "/judge/claim", a.greeting(), &task)
	if code == http.StatusNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if task.SubmissionID == 0 {
		return nil, errors.New("claim returned a task without an id")
	}
	return &task, nil
}

func (a *Agent) caseFile(ctx context.Context, caseID int64, which string) ([]byte, error) {
	path := "/judge/cases/" + strconv.FormatInt(caseID, 10) + "/" + which
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.cfg.Server+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.cfg.Token)
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// Case bodies are capped server side, and this is a second belt.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("case %d %s: %s", caseID, which, firstLine(body))
	}
	return body, nil
}

func (a *Agent) PostStatus(ctx context.Context, submissionID int64, state, detail string) error {
	_, err := a.request(ctx, http.MethodPost,
		"/judge/submissions/"+strconv.FormatInt(submissionID, 10)+"/status",
		protocol.Status{Name: a.cfg.Name, State: state, Detail: detail}, nil)
	return err
}

func (a *Agent) PostResult(ctx context.Context, submissionID int64, rep judge.Report) error {
	result := protocol.Result{
		Name:       a.cfg.Name,
		Verdict:    string(rep.Verdict),
		TimeMS:     rep.TimeMS,
		MemKB:      rep.MemKB,
		TimeMin:    rep.TimeMin,
		MemMin:     rep.MemMin,
		FailedCase: rep.FailedCase,
		Detail:     rep.Detail,
	}
	for _, row := range rep.Runs {
		result.Runs = append(result.Runs, protocol.RunRow{
			Ordinal: row.Ordinal, CaseName: row.CaseName, Verdict: string(row.Verdict),
			TimeMS: row.TimeMS, MemKB: row.MemKB, IsSample: row.IsSample,
			TimeMin: row.TimeMin, MemMin: row.MemMin,
		})
	}
	_, err := a.request(ctx, http.MethodPost,
		"/judge/submissions/"+strconv.FormatInt(submissionID, 10)+"/result", result, nil)
	return err
}

// PostRun reports one finished case while the rest of the submission is still being
// judged, which is what lets the detail page fill in as it goes. A failed progress
// report is not worth stopping for: the final result carries every row anyway.
func (a *Agent) PostRun(ctx context.Context, submissionID int64, detail string, row judge.RunRow) error {
	_, err := a.request(ctx, http.MethodPost,
		"/judge/submissions/"+strconv.FormatInt(submissionID, 10)+"/status",
		protocol.Status{
			Name: a.cfg.Name, State: string(judge.Running), Detail: detail,
			Run: &protocol.RunRow{
				Ordinal: row.Ordinal, CaseName: row.CaseName, Verdict: string(row.Verdict),
				TimeMS: row.TimeMS, MemKB: row.MemKB, IsSample: row.IsSample,
				TimeMin: row.TimeMin, MemMin: row.MemMin,
			},
		}, nil)
	return err
}

// ProcessOnce handles a single claimed submission. It reports true when work was
// found. Results are always reported, including "we could not do this at all",
// so a submission never sits in compiling without an answer.
func (a *Agent) ProcessOnce(ctx context.Context) (bool, error) {
	task, err := a.Claim(ctx)
	if err != nil || task == nil {
		return false, err
	}
	a.cfg.Logf("claimed sid=%d problem=%s lang=%s cases=%d", task.SubmissionID, task.PID, task.Language, len(task.Cases))

	tool, ok := a.cfg.Tools[task.Language]
	if !ok {
		rep := judge.Report{Verdict: judge.IE, TimeMS: -1, MemKB: -1,
			Detail: "this judge box has no toolchain for " + task.Language}
		return true, a.PostResult(ctx, task.SubmissionID, rep)
	}

	dir := filepath.Join(a.cfg.Dir, strconv.FormatInt(task.SubmissionID, 10))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		rep := judge.Report{Verdict: judge.IE, TimeMS: -1, MemKB: -1, Detail: "workdir: " + err.Error()}
		return true, a.PostResult(ctx, task.SubmissionID, rep)
	}
	// Each submission gets a private, freshly created directory, and it is gone
	// when the run ends.
	defer os.RemoveAll(dir)

	cases := make([]judge.Case, 0, len(task.Cases))
	for _, c := range task.Cases {
		input, err := a.caseFile(ctx, c.ID, "in")
		if err != nil {
			return true, a.reportInfra(ctx, task.SubmissionID, "fetch input: "+err.Error())
		}
		expected, err := a.caseFile(ctx, c.ID, "out")
		if err != nil {
			return true, a.reportInfra(ctx, task.SubmissionID, "fetch answer: "+err.Error())
		}
		cases = append(cases, judge.Case{
			ID: c.ID, Name: c.Name, Ordinal: c.Ordinal, IsSample: c.IsSample,
			Score: c.Score, Input: input, Expected: expected,
		})
	}

	if err := a.PostStatus(ctx, task.SubmissionID, string(judge.Running),
		fmt.Sprintf("%d cases", len(cases))); err != nil {
		a.cfg.Logf("status report failed (continuing): %v", err)
	}

	total := len(cases)
	progress := func(row judge.RunRow) {
		a.reportProgress(ctx, task.SubmissionID, row, total)
	}

	started := time.Now()
	rep := judge.Judge(judge.Task{
		SubmissionID: task.SubmissionID, PID: task.PID, Language: task.Language,
		Source: task.Source, TimeLimitMS: task.TimeLimitMS, MemLimitKB: task.MemLimitKB,
		Cases: cases,
	}, tool, dir, a.cfg.RunAs, judge.Progress(progress))
	a.cfg.Logf("sid=%d verdict=%s score=%d/%d time=%dms mem=%dKB wall=%s%s",
		task.SubmissionID, rep.Verdict, rep.Score, rep.TotalScore, rep.TimeMS, rep.MemKB,
		time.Since(started).Truncate(time.Millisecond), detailSuffix(rep.Detail))

	return true, a.PostResult(ctx, task.SubmissionID, rep)
}

// detailSuffix keeps the judge-side reason in the log line: an IE verdict on its
// own tells the operator nothing.
func detailSuffix(detail string) string {
	if detail == "" {
		return ""
	}
	return " detail=" + strings.ReplaceAll(detail, "\n", " ")
}

// reportProgress publishes one case row with a "3/10 · case AC" note, so the
// submission page and the queue both show how far the run got.
func (a *Agent) reportProgress(ctx context.Context, submissionID int64, row judge.RunRow, total int) {
	detail := fmt.Sprintf("%d/%d · %s %s", row.Ordinal, total, row.CaseName, row.Verdict)
	if err := a.PostRun(ctx, submissionID, detail, row); err != nil {
		a.cfg.Logf("progress report failed (continuing): %v", err)
	}
}

func (a *Agent) reportInfra(ctx context.Context, submissionID int64, detail string) error {
	return a.PostResult(ctx, submissionID, judge.Report{
		Verdict: judge.IE, TimeMS: -1, MemKB: -1, Detail: detail})
}

// Run polls until the context ends. A failed round waits Backoff rather than
// spinning, so an unreachable server does not turn into a hot loop.
func (a *Agent) Run(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		worked, err := a.ProcessOnce(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			a.cfg.Logf("round failed: %v", err)
			if !sleep(ctx, a.cfg.Backoff) {
				return ctx.Err()
			}
			continue
		}
		if !worked && !sleep(ctx, a.cfg.Poll) {
			return ctx.Err()
		}
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
