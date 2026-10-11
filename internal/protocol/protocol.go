// Package protocol holds the wire types shared by the server and the judge
// boxes, so the two ends cannot drift apart.
package protocol

// Greeting registers (and heartbeats) a judge box.
type Greeting struct {
	Name      string   `json:"name"`
	Languages []string `json:"languages,omitempty"`
	Note      string   `json:"note,omitempty"`
}

// Case describes one test case a box must fetch and run.
type Case struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Ordinal  int    `json:"ordinal"`
	IsSample bool   `json:"is_sample"`
	Score    int    `json:"score"`
}

// Task is the full work order for one submission.
type Task struct {
	SubmissionID int64  `json:"submission_id"`
	PID          string `json:"pid"`
	Language     string `json:"language"`
	Source       string `json:"source"`
	TimeLimitMS  int    `json:"time_limit_ms"`
	MemLimitKB   int    `json:"mem_limit_kb"`
	Cases        []Case `json:"cases"`
}

// Status is a progress report while a submission is in flight.
type Status struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// RunRow is one case result.
type RunRow struct {
	Ordinal  int    `json:"ordinal"`
	CaseName string `json:"case_name"`
	Verdict  string `json:"verdict"`
	TimeMS   int    `json:"time_ms"`
	MemKB    int    `json:"mem_kb"`
	IsSample bool   `json:"is_sample"`
}

// Result is the final report for a submission. The server recomputes the score
// from Runs, so Verdict alone is not trusted.
type Result struct {
	Name       string   `json:"name"`
	Verdict    string   `json:"verdict"`
	TimeMS     int      `json:"time_ms"`
	MemKB      int      `json:"mem_kb"`
	FailedCase string   `json:"failed_case,omitempty"`
	Detail     string   `json:"detail,omitempty"`
	Runs       []RunRow `json:"runs,omitempty"`
}
