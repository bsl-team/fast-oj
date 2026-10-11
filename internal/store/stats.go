package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

type Counts struct {
	Problems    int `json:"problems"`
	Users       int `json:"users"`
	Submissions int `json:"submissions"`
	Pending     int `json:"pending"`
	Judges      int `json:"judges"`
}

func (db *DB) Counts(ctx context.Context) (Counts, error) {
	var c Counts
	for _, q := range []struct {
		dst *int
		sql string
	}{
		{&c.Problems, `SELECT COUNT(*) FROM problems`},
		{&c.Users, `SELECT COUNT(*) FROM users`},
		{&c.Submissions, `SELECT COUNT(*) FROM submissions`},
		{&c.Pending, `SELECT COUNT(*) FROM submissions WHERE status='queued'`},
		{&c.Judges, `SELECT COUNT(*) FROM judges`},
	} {
		if err := db.SQL.QueryRowContext(ctx, q.sql).Scan(q.dst); err != nil {
			return c, fmt.Errorf("count %s: %w", q.sql, err)
		}
	}
	return c, nil
}

// ProblemCounts backs the web problem list: how many submissions a problem has
// drawn and how many distinct people have solved it.
type ProblemCounts struct {
	Submits int `json:"submits"`
	Solved  int `json:"solved"`
}

func (db *DB) ProblemCounts(ctx context.Context, problemIDs []int64) (map[int64]ProblemCounts, error) {
	out := make(map[int64]ProblemCounts, len(problemIDs))
	if len(problemIDs) == 0 {
		return out, nil
	}
	args := make([]any, len(problemIDs))
	for i, id := range problemIDs {
		args[i] = id
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(problemIDs)), ",")

	rows, err := db.SQL.QueryContext(ctx,
		`SELECT problem_id, COUNT(*), COUNT(DISTINCT user_id)
		 FROM submissions WHERE problem_id IN (`+placeholders+`) GROUP BY problem_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id          int64
			submits     int
			distinctAll int
		)
		if err := rows.Scan(&id, &submits, &distinctAll); err != nil {
			return nil, err
		}
		out[id] = ProblemCounts{Submits: submits}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Accepted solvers are counted separately so a user with ten AC rows on one
	// problem still counts once.
	rows, err = db.SQL.QueryContext(ctx,
		`SELECT problem_id, COUNT(DISTINCT user_id)
		 FROM submissions WHERE verdict='AC' AND problem_id IN (`+placeholders+`) GROUP BY problem_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var solved int
		if err := rows.Scan(&id, &solved); err != nil {
			return nil, err
		}
		c := out[id]
		c.Solved = solved
		out[id] = c
	}
	return out, rows.Err()
}

// RankRow is one leaderboard entry.
type RankRow struct {
	UserID  int64  `json:"user_id"`
	Handle  string `json:"handle"`
	Solved  int    `json:"solved"`
	Submits int    `json:"submits"`
	LastAC  int64  `json:"last_ac"`
}

// Leaderboard ranks people by the number of distinct problems they have AC'd.
func (db *DB) Leaderboard(ctx context.Context, limit int) ([]RankRow, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	rows, err := db.SQL.QueryContext(ctx,
		`SELECT u.id, u.handle,
		        (SELECT COUNT(DISTINCT s.problem_id) FROM submissions s WHERE s.user_id=u.id AND s.verdict='AC'),
		        (SELECT COUNT(*) FROM submissions s WHERE s.user_id=u.id),
		        (SELECT MAX(s.judged_at) FROM submissions s WHERE s.user_id=u.id AND s.verdict='AC')
		 FROM users u
		 WHERE u.role <> 'judge'
		 ORDER BY 3 DESC, 4 ASC, u.id ASC
		 LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []RankRow
	for rows.Next() {
		var (
			r      RankRow
			lastAC sql.NullInt64
		)
		if err := rows.Scan(&r.UserID, &r.Handle, &r.Solved, &r.Submits, &lastAC); err != nil {
			return nil, err
		}
		r.LastAC = nullTime(lastAC)
		out = append(out, r)
	}
	return out, rows.Err()
}

// SolvedProblem is one entry on a user's solved list.
type SolvedProblem struct {
	PID      string `json:"pid"`
	Title    string `json:"title"`
	BestTime int    `json:"best_time_ms"`
	SolvedAt int64  `json:"solved_at"`
}

func (db *DB) SolvedProblems(ctx context.Context, userID int64) ([]SolvedProblem, error) {
	rows, err := db.SQL.QueryContext(ctx,
		`SELECT p.pid, p.title, MIN(s.time_ms), MAX(s.judged_at)
		 FROM submissions s JOIN problems p ON p.id=s.problem_id
		 WHERE s.user_id=? AND s.verdict='AC'
		 GROUP BY s.problem_id, p.pid, p.title
		 ORDER BY MAX(s.judged_at) DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SolvedProblem
	for rows.Next() {
		var sp SolvedProblem
		if err := rows.Scan(&sp.PID, &sp.Title, &sp.BestTime, &sp.SolvedAt); err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

// VerdictTally counts a user's submissions by verdict, for the profile summary.
func (db *DB) VerdictTally(ctx context.Context, userID int64) (map[string]int, error) {
	rows, err := db.SQL.QueryContext(ctx,
		`SELECT CASE WHEN verdict='' THEN status ELSE verdict END, COUNT(*)
		 FROM submissions WHERE user_id=? GROUP BY 1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]int{}
	for rows.Next() {
		var key string
		var n int
		if err := rows.Scan(&key, &n); err != nil {
			return nil, err
		}
		out[key] = n
	}
	return out, rows.Err()
}

// nullTime turns a nullable column into zero so view structs stay plain.
func nullTime(v sql.NullInt64) int64 {
	if v.Valid {
		return v.Int64
	}
	return 0
}
