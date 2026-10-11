package judge

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Case is one test case with its data already fetched.
type Case struct {
	ID       int64
	Name     string
	Ordinal  int
	IsSample bool
	Score    int
	Input    []byte
	Expected []byte
}

// Task is the work order for one submission.
type Task struct {
	SubmissionID int64
	PID          string
	Language     string
	Source       string
	TimeLimitMS  int
	MemLimitKB   int
	Cases        []Case
}

type RunRow struct {
	Ordinal  int     `json:"ordinal"`
	CaseName string  `json:"case_name"`
	Verdict  Verdict `json:"verdict"`
	TimeMS   int     `json:"time_ms"`
	MemKB    int     `json:"mem_kb"`
	IsSample bool    `json:"is_sample"`
}

type Report struct {
	Verdict    Verdict
	TimeMS     int
	MemKB      int
	FailedCase string
	Detail     string
	Runs       []RunRow
	Score      int
	TotalScore int
}

// CompileLimits are deliberately loose: a slow link step is not the submitter's
// fault. The compiler is not the untrusted input here, but it still runs under a
// scrubbed environment and a wide process cap rather than unsandboxed, since it is
// the same execution path. TimeMS is a wall budget (limit + 50ms slack), so it
// carries the multi-process compiler's startup overhead rather than one CPU bound.
var CompileLimits = Limits{TimeMS: 120_000, MemKB: 2 << 20, MaxProcs: 16}

// maxDetail keeps judge-side text out of the database from growing unbounded.
const maxDetail = 4 << 10

// OutputsMatch compares program output the way an OJ is expected to: trailing
// whitespace on each line and trailing blank lines do not matter.
func OutputsMatch(got, want []byte) bool {
	return normalizeOutput(got) == normalizeOutput(want)
}

func normalizeOutput(body []byte) string {
	body = bytes.TrimRight(body, " \t\r\n")
	if len(body) == 0 {
		return ""
	}
	lines := strings.Split(string(body), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t\r")
	}
	return strings.Join(lines, "\n")
}

// Judge builds and runs one submission over all of its cases. It never talks to
// the network: the caller has already fetched the case data. drop is the uid a
// submission is run as, when the judge has one to give it.
func Judge(task Task, tool Tool, dir string, drop *DropTo) Report {
	rep := Report{Verdict: IE, TimeMS: -1, MemKB: -1}
	if len(task.Cases) == 0 {
		rep.Detail = "problem " + task.PID + " has no test cases"
		return rep
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		rep.Detail = "workdir: " + err.Error()
		return rep
	}

	source := filepath.Join(dir, tool.SourceFile)
	if err := os.WriteFile(source, []byte(task.Source), 0o644); err != nil {
		rep.Detail = "write source: " + err.Error()
		return rep
	}

	if tool.Compiles() {
		build := Run(tool.CompileArgs(dir), dir, nil, CompileLimits)
		if build.Exit != 0 {
			rep.Verdict = CE
			rep.Detail = clipText(string(build.Stderr), maxDetail)
			rep.FailedCase = "compile"
			return rep
		}
		if build.Verdict == TLE {
			rep.Verdict = IE
			rep.Detail = "compiler timed out"
			return rep
		}
	}

	runLimits := Limits{TimeMS: task.TimeLimitMS, MemKB: task.MemLimitKB, RunAs: drop}
	argv := tool.RunArgs(dir)
	if !tool.Compiles() {
		// Interpreters take the source file after the program name.
		argv = append(argv, source)
	}

	worst := AC
	firstFailure := -1
	for _, tc := range task.Cases {
		ex := Run(argv, dir, tc.Input, runLimits)
		v := ex.Verdict
		if v == AC && !OutputsMatch(ex.Stdout, tc.Expected) {
			v = WA
		}
		row := RunRow{Ordinal: tc.Ordinal, CaseName: tc.Name, Verdict: v,
			TimeMS: ex.TimeMS, MemKB: ex.MemKB, IsSample: tc.IsSample}
		rep.Runs = append(rep.Runs, row)

		if ex.TimeMS > rep.TimeMS {
			rep.TimeMS = ex.TimeMS
		}
		if ex.MemKB > rep.MemKB {
			rep.MemKB = ex.MemKB
		}
		if v == AC {
			if !tc.IsSample {
				rep.Score += tc.Score
			}
			continue
		}
		if firstFailure < 0 {
			firstFailure = len(rep.Runs) - 1
			rep.FailedCase = tc.Name
			detail := strings.TrimSpace(string(ex.Stderr))
			if v == WA {
				detail = diffExcerpt(ex.Stdout, tc.Expected)
			}
			rep.Detail = clipText(detail, maxDetail)
		}
		if severity(v) > severity(worst) {
			worst = v
		}
	}
	for _, tc := range task.Cases {
		if !tc.IsSample {
			rep.TotalScore += tc.Score
		}
	}

	switch {
	case firstFailure < 0:
		rep.Verdict = AC
	case worst == AC:
		rep.Verdict = WA
	default:
		rep.Verdict = worst
	}
	return rep
}

// severity orders verdicts so the most informative one wins when several cases
// fail for different reasons.
func severity(v Verdict) int {
	switch v {
	case AC:
		return 0
	case WA:
		return 1
	case RE:
		return 2
	case MLE:
		return 3
	case TLE:
		return 4
	case CE:
		return 5
	default:
		return 6
	}
}

func diffExcerpt(got, want []byte) string {
	g := normalizeOutput(got)
	w := normalizeOutput(want)
	gl, wl := strings.Split(g, "\n"), strings.Split(w, "\n")
	for i := 0; i < len(gl) && i < len(wl); i++ {
		if gl[i] != wl[i] {
			return "line " + strconv.Itoa(i+1) + ": got [" + clipText(gl[i], 200) + "] want [" + clipText(wl[i], 200) + "]"
		}
	}
	if len(gl) != len(wl) {
		return "line count: got " + strconv.Itoa(len(gl)) + " want " + strconv.Itoa(len(wl))
	}
	return "output differs"
}

func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n[... clipped]"
}
