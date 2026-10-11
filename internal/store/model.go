package store

import "strings"

type scanner interface{ Scan(dest ...any) error }

type User struct {
	ID          int64  `json:"id"`
	Handle      string `json:"handle"`
	Email       string `json:"email,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Bio         string `json:"bio,omitempty"`
	Role        string `json:"role"`
	Status      string `json:"status"`
	HasPassword bool   `json:"has_password"`
	CreatedAt   int64  `json:"created_at"`
	LastLoginAt int64  `json:"last_login_at"`
}

type Problem struct {
	ID          int64    `json:"id"`
	PID         string   `json:"pid"`
	Title       string   `json:"title"`
	Statement   string   `json:"statement,omitempty"`
	TimeLimitMS int      `json:"time_limit_ms"`
	MemLimitKB  int      `json:"mem_limit_kb"`
	Languages   []string `json:"languages,omitempty"`
	Visible     bool     `json:"visible"`
	OwnerID     int64    `json:"owner_id"`
	CreatedAt   int64    `json:"created_at"`
	UpdatedAt   int64    `json:"updated_at"`
}

type Testcase struct {
	ID        int64  `json:"id"`
	ProblemID int64  `json:"problem_id"`
	Ordinal   int    `json:"ordinal"`
	Name      string `json:"name"`
	IsSample  bool   `json:"is_sample"`
	Score     int    `json:"score"`
	InputRel  string `json:"input_rel"`
	OutputRel string `json:"output_rel"`
}

type Submission struct {
	ID         int64  `json:"id"`
	UserID     int64  `json:"user_id"`
	ProblemID  int64  `json:"problem_id"`
	Language   string `json:"language"`
	Source     string `json:"source,omitempty"`
	Status     string `json:"status"`
	Verdict    string `json:"verdict"`
	TimeMS     int    `json:"time_ms"`
	MemKB      int    `json:"mem_kb"`
	Score      int    `json:"score"`
	TotalScore int    `json:"total_score"`
	FailedCase string `json:"failed_case"`
	Detail     string `json:"detail,omitempty"`
	JudgeID    int64  `json:"judge_id"`
	CreatedAt  int64  `json:"created_at"`
	StartedAt  int64  `json:"started_at"`
	JudgedAt   int64  `json:"judged_at"`
}

type Run struct {
	ID           int64  `json:"id"`
	SubmissionID int64  `json:"submission_id"`
	Ordinal      int    `json:"ordinal"`
	CaseName     string `json:"case_name"`
	Verdict      string `json:"verdict"`
	TimeMS       int    `json:"time_ms"`
	MemKB        int    `json:"mem_kb"`
	IsSample     bool   `json:"is_sample"`
}

type Judge struct {
	ID         int64    `json:"id"`
	Name       string   `json:"name"`
	Languages  []string `json:"languages,omitempty"`
	Note       string   `json:"note"`
	LastSeenAt int64    `json:"last_seen_at"`
}

func joinList(items []string) string { return strings.Join(items, ",") }

func splitList(raw string) []string {
	if raw == "" {
		return nil
	}
	out := make([]string, 0, strings.Count(raw, ",")+1)
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
