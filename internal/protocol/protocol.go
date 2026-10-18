// Package protocol holds the wire types shared by the server and the judge
// boxes, so the two ends cannot drift apart.
package protocol

// 判机自报能力的词表。放在协议里是因为它是两端之间的一句话：判机说"我会这个"，
// 服务器照这句话派单。新增一项就要在判机那边真做得到才行——写在这一行里不等于会。
const (
	CapOptions    = "options"    // 认 submissions.options，按选的那条命令行编
	CapChecker    = "checker"    // 会现编并跑题目自带的判题器（SPJ）
	CapInteractor = "interactor" // 会跑交互器（交互题）
)

// Capabilities 是**这一版**判机会做的事。它写在代码里而不是配置里：能让操作员调的就
// 不是"我会不会"，而是谎。
var Capabilities = []string{CapOptions, CapChecker, CapInteractor}

// KnownCapability 报告这个词在词表里。服务器拿它校验自报的名单，不让一个拼错的词
// 把整台判机的能力描述改掉。
func KnownCapability(s string) bool {
	for _, c := range Capabilities {
		if c == s {
			return true
		}
	}
	return false
}

// Greeting is what a judge box sends to register itself and heartbeat.
type Greeting struct {
	Name      string   `json:"name"`
	Languages []string `json:"languages,omitempty"`
	// Capabilities 是这台判机**自报**它会做什么：options / checker / interactor。
	// 服务器照它派单——老判机不认识工单里这些字段，会照自己那套编、按逐字节比较判，
	// 然后正常回报成功，于是页面上「C++14 · O2 关」「本题由 SPJ 判定」就成了谎。
	// 不送这一列（老二进制）就是空名单，需要新能力的工单不会派给它，而是排队等。
	Capabilities []string `json:"capabilities,omitempty"`
	Note         string   `json:"note,omitempty"`
}

// Case describes one test case a box must fetch and run.
// Case 是一条用例在**这份工单里**的样子。没有数据库 id：判机取正文是按
// 「这条提交的第 N 条用例」取的，把主键发给它只是给了它一个可以用来枚举别的题目的把手。
type Case struct {
	Name     string `json:"name"`
	Ordinal  int    `json:"ordinal"`
	IsSample bool   `json:"is_sample"`
	Score    int    `json:"score"`
}

// AuxProgram is a program the problem author wrote and the judge box runs: the SPJ
// checker and the interactive problem's interactor. It travels as source because the
// box has to compile it with its own toolchain — a binary built elsewhere would carry
// a different libc than the one that then judges the student.
type AuxProgram struct {
	Language string `json:"language"`
	Source   string `json:"source"`
}

// Task is the full work order for one submission.
type Task struct {
	SubmissionID int64  `json:"submission_id"`
	PID          string `json:"pid"`
	Language     string `json:"language"`
	// Options is the version selection written in the judge's own allowlist
	// notation ("std=c++14,O=0"); empty means the box should use its default.
	Options string `json:"options,omitempty"`
	// Checker 与 Interactor 只在题目真配了才出现：一份没人用的源码会跟着每一次领单
	// 过一遍网络。
	Checker     *AuxProgram `json:"checker,omitempty"`
	Interactor  *AuxProgram `json:"interactor,omitempty"`
	Source      string      `json:"source"`
	TimeLimitMS int         `json:"time_limit_ms"`
	MemLimitKB  int         `json:"mem_limit_kb"`
	Cases       []Case      `json:"cases"`
}

// Status is a progress report while a submission is in flight.
type Status struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
	// Run carries one finished case so the detail page can show results while the
	// rest of the submission is still being judged. It is advisory: the server
	// re-derives the verdict and score from the full set when the result arrives.
	Run *RunRow `json:"run,omitempty"`
}

// RunRow is one case result.
type RunRow struct {
	Ordinal  int    `json:"ordinal"`
	CaseName string `json:"case_name"`
	Verdict  string `json:"verdict"`
	TimeMS   int    `json:"time_ms"`
	MemKB    int    `json:"mem_kb"`
	IsSample bool   `json:"is_sample"`
	// TimeMin/MemMin say the number is a lower bound: the run was killed before it
	// finished, so it cost at least that much.
	TimeMin bool `json:"time_min,omitempty"`
	MemMin  bool `json:"mem_min,omitempty"`
}

// Result is the final report for a submission. The server recomputes the score
// from Runs, so Verdict alone is not trusted.
type Result struct {
	Name       string `json:"name"`
	Verdict    string `json:"verdict"`
	TimeMS     int    `json:"time_ms"`
	MemKB      int    `json:"mem_kb"`
	TimeMin    bool   `json:"time_min,omitempty"`
	MemMin     bool   `json:"mem_min,omitempty"`
	FailedCase string `json:"failed_case,omitempty"`
	Detail     string `json:"detail,omitempty"`
	Runs       []RunRow `json:"runs,omitempty"`
	// Toolchain 是这台判机回报的"我这次实际用的口径"（"C++14 · O2 关"、"python3.14"）。
	// 页面显示的是这一列而不是提交时选的那份：选了 c++14 却被一台旧判机按 c++17 判了，
	// 差别只有判机自己知道。老判机不送 → 空串 → 页面写「未回报」，不替它编一句。
	Toolchain string `json:"toolchain,omitempty"`
}
