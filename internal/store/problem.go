package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var ErrConflict = errors.New("already exists")

func conflict(err error, what string) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return fmt.Errorf("%w: %s", ErrConflict, what)
	}
	return err
}

// escapeLike neutralises LIKE wildcards so a search term stays a literal.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

const problemColumns = `id, pid, title, statement, time_limit_ms, mem_limit_kb, languages, visible, created_at, updated_at`

func scanProblem(row scanner) (Problem, error) {
	var (
		p           Problem
		langs       string
		visibleFlag int
	)
	err := row.Scan(&p.ID, &p.PID, &p.Title, &p.Statement, &p.TimeLimitMS, &p.MemLimitKB,
		&langs, &visibleFlag, &p.CreatedAt, &p.UpdatedAt)
	p.Languages = splitList(langs)
	p.Visible = visibleFlag != 0
	return p, notFound(err)
}

func (db *DB) CreateProblem(ctx context.Context, p *Problem) (Problem, error) {
	now := nowMS()
	p.CreatedAt = now
	p.UpdatedAt = now
	res, err := db.SQL.ExecContext(ctx,
		`INSERT INTO problems (pid, title, statement, time_limit_ms, mem_limit_kb, languages, visible, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.PID, p.Title, p.Statement, p.TimeLimitMS, p.MemLimitKB,
		joinList(p.Languages), boolInt(p.Visible), p.CreatedAt, p.UpdatedAt)
	if err != nil {
		return Problem{}, conflict(err, "pid "+p.PID)
	}
	p.ID, _ = res.LastInsertId()
	return *p, nil
}

func (db *DB) UpdateProblem(ctx context.Context, p Problem) (Problem, error) {
	p.UpdatedAt = nowMS()
	res, err := db.SQL.ExecContext(ctx,
		`UPDATE problems SET pid=?, title=?, statement=?, time_limit_ms=?, mem_limit_kb=?, languages=?, visible=?, updated_at=?
		 WHERE id=?`,
		p.PID, p.Title, p.Statement, p.TimeLimitMS, p.MemLimitKB,
		joinList(p.Languages), boolInt(p.Visible), p.UpdatedAt, p.ID)
	if err != nil {
		return Problem{}, conflict(err, "pid "+p.PID)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Problem{}, ErrNotFound
	}
	return db.GetProblemByID(ctx, p.ID)
}

func (db *DB) GetProblemByID(ctx context.Context, id int64) (Problem, error) {
	return scanProblem(db.SQL.QueryRowContext(ctx,
		`SELECT `+problemColumns+` FROM problems WHERE id=?`, id))
}

// FindProblem accepts either the numeric row id or the human readable pid.
func (db *DB) FindProblem(ctx context.Context, key string) (Problem, error) {
	var id int64
	if _, err := fmt.Sscanf(key, "%d", &id); err == nil && strings.TrimLeft(key, "0123456789") == "" {
		return db.GetProblemByID(ctx, id)
	}
	return scanProblem(db.SQL.QueryRowContext(ctx,
		`SELECT `+problemColumns+` FROM problems WHERE pid=? COLLATE NOCASE`, key))
}

type ProblemPage struct {
	Items     []Problem `json:"items"`
	NextAfter int64     `json:"next_after,omitempty"`
	Total     int       `json:"total"`
}

// ListProblems pages over the primary key with a cursor instead of OFFSET, so
// deep pages keep the same cost as the first one.
func (db *DB) ListProblems(ctx context.Context, after int64, limit int, search string, visibleOnly bool) (ProblemPage, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	q := `SELECT ` + problemColumns + ` FROM problems WHERE id > ?`
	args := []any{after}
	if visibleOnly {
		q += ` AND visible=1`
	}
	if search != "" {
		like := "%" + escapeLike(search) + "%"
		q += ` AND (pid LIKE ? ESCAPE '\' OR title LIKE ? ESCAPE '\')`
		args = append(args, like, like)
	}
	q += ` ORDER BY id LIMIT ?`
	args = append(args, limit+1)

	rows, err := db.SQL.QueryContext(ctx, q, args...)
	if err != nil {
		return ProblemPage{}, err
	}
	defer rows.Close()

	var page ProblemPage
	for rows.Next() {
		p, err := scanProblem(rows)
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, p)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if len(page.Items) > limit {
		page.NextAfter = page.Items[limit-1].ID
		page.Items = page.Items[:limit]
	}

	where := "WHERE 1=1"
	if visibleOnly {
		where += " AND visible=1"
	}
	if err := db.SQL.QueryRowContext(ctx, `SELECT COUNT(*) FROM problems `+where).Scan(&page.Total); err != nil {
		return page, err
	}
	return page, nil
}

// CountProblems is the cheap total for page headers, where pulling a page of
// rows just to read .Total would be silly.
func (db *DB) CountProblems(ctx context.Context, visibleOnly bool) (int, error) {
	q := `SELECT COUNT(*) FROM problems`
	if visibleOnly {
		q += ` WHERE visible=1`
	}
	var n int
	err := db.SQL.QueryRowContext(ctx, q).Scan(&n)
	return n, err
}

// CountSolved counts distinct problems this user has AC'd.
func (db *DB) CountSolved(ctx context.Context, userID int64) (int, error) {
	var n int
	err := db.SQL.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT problem_id) FROM submissions WHERE user_id=? AND verdict='AC'`, userID).Scan(&n)
	return n, err
}

func (db *DB) DeleteProblem(ctx context.Context, id int64) error {
	res, err := db.SQL.ExecContext(ctx, `DELETE FROM problems WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
