package store

import (
	"context"
	"fmt"
	"strings"
)

const testcaseColumns = `id, problem_id, ordinal, name, is_sample, score, input_rel, output_rel`

func scanTestcase(row scanner) (Testcase, error) {
	var (
		tc     Testcase
		sample int
	)
	err := row.Scan(&tc.ID, &tc.ProblemID, &tc.Ordinal, &tc.Name, &sample, &tc.Score, &tc.InputRel, &tc.OutputRel)
	tc.IsSample = sample != 0
	return tc, notFound(err)
}

// AddTestcase appends to the problem's ordered case list when Ordinal is zero.
func (db *DB) AddTestcase(ctx context.Context, tc *Testcase) (Testcase, error) {
	if tc.Ordinal == 0 {
		var max int
		err := db.SQL.QueryRowContext(ctx,
			`SELECT IFNULL(MAX(ordinal), 0) FROM testcases WHERE problem_id=?`, tc.ProblemID).Scan(&max)
		if err != nil {
			return Testcase{}, err
		}
		tc.Ordinal = max + 1
	}
	res, err := db.SQL.ExecContext(ctx,
		`INSERT INTO testcases (problem_id, ordinal, name, is_sample, score, input_rel, output_rel)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		tc.ProblemID, tc.Ordinal, tc.Name, boolInt(tc.IsSample), tc.Score, tc.InputRel, tc.OutputRel)
	if err != nil {
		return Testcase{}, fmt.Errorf("%w: case %q", conflict(err, tc.Name), tc.Name)
	}
	tc.ID, _ = res.LastInsertId()
	return *tc, nil
}

func (db *DB) ListTestcases(ctx context.Context, problemID int64, samplesOnly bool) ([]Testcase, error) {
	q := `SELECT ` + testcaseColumns + ` FROM testcases WHERE problem_id=?`
	if samplesOnly {
		q += ` AND is_sample=1`
	}
	q += ` ORDER BY ordinal`
	rows, err := db.SQL.QueryContext(ctx, q, problemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Testcase
	for rows.Next() {
		tc, err := scanTestcase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, tc)
	}
	return out, rows.Err()
}

func (db *DB) GetTestcaseByID(ctx context.Context, id int64) (Testcase, error) {
	return scanTestcase(db.SQL.QueryRowContext(ctx,
		`SELECT `+testcaseColumns+` FROM testcases WHERE id=?`, id))
}

func (db *DB) GetTestcase(ctx context.Context, problemID int64, name string) (Testcase, error) {
	return scanTestcase(db.SQL.QueryRowContext(ctx,
		`SELECT `+testcaseColumns+` FROM testcases WHERE problem_id=? AND name=?`, problemID, name))
}

func (db *DB) DeleteTestcases(ctx context.Context, problemID int64) (int64, error) {
	res, err := db.SQL.ExecContext(ctx, `DELETE FROM testcases WHERE problem_id=?`, problemID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// UpdateTestcase writes the editable columns of one case by id. Renaming is
// deliberately not an upsert by name: the row keeps its ordinal and its files,
// and only the label changes.
func (db *DB) UpdateTestcase(ctx context.Context, tc *Testcase) (Testcase, error) {
	res, err := db.SQL.ExecContext(ctx,
		`UPDATE testcases SET name=?, ordinal=?, is_sample=?, score=?, input_rel=?, output_rel=? WHERE id=?`,
		tc.Name, tc.Ordinal, boolInt(tc.IsSample), tc.Score, tc.InputRel, tc.OutputRel, tc.ID)
	if err != nil {
		return Testcase{}, conflict(err, tc.Name)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Testcase{}, ErrNotFound
	}
	return db.GetTestcaseByID(ctx, tc.ID)
}

// DeleteTestcase removes one case row and returns it, because the caller has to
// delete the files it points at.
func (db *DB) DeleteTestcase(ctx context.Context, id int64) (Testcase, error) {
	tc, err := db.GetTestcaseByID(ctx, id)
	if err != nil {
		return Testcase{}, err
	}
	if _, err := db.SQL.ExecContext(ctx, `DELETE FROM testcases WHERE id=?`, id); err != nil {
		return Testcase{}, err
	}
	return tc, nil
}

func (db *DB) ProblemScoreTotal(ctx context.Context, problemID int64) (int, error) {
	var total int
	err := db.SQL.QueryRowContext(ctx,
		`SELECT IFNULL(SUM(score), 0) FROM testcases WHERE problem_id=? AND is_sample=0`, problemID).Scan(&total)
	return total, err
}

// CaseCounts returns judged-case counts for many problems in one query, so the
// problem list never turns into one query per row.
func (db *DB) CaseCounts(ctx context.Context, problemIDs []int64) (map[int64]int, error) {
	counts := make(map[int64]int, len(problemIDs))
	if len(problemIDs) == 0 {
		return counts, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(problemIDs)), ",")
	args := make([]any, len(problemIDs))
	for i, id := range problemIDs {
		args[i] = id
	}
	rows, err := db.SQL.QueryContext(ctx,
		`SELECT problem_id, COUNT(*) FROM testcases WHERE problem_id IN (`+placeholders+`) GROUP BY problem_id`,
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id int64
			n  int
		)
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		counts[id] = n
	}
	return counts, rows.Err()
}

func (db *DB) CountTestcases(ctx context.Context, problemID int64, samplesOnly bool) (int, error) {
	q := `SELECT COUNT(*) FROM testcases WHERE problem_id=?`
	if samplesOnly {
		q += ` AND is_sample=1`
	}
	var n int
	err := db.SQL.QueryRowContext(ctx, q, problemID).Scan(&n)
	return n, err
}

// PutTestcase inserts or replaces a case by (problem, name). The id is re-read
// because an upsert branch leaves last_insert_rowid() untouched.
func (db *DB) PutTestcase(ctx context.Context, tc *Testcase) (Testcase, error) {
	_, err := db.SQL.ExecContext(ctx,
		`INSERT INTO testcases (problem_id, ordinal, name, is_sample, score, input_rel, output_rel)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(problem_id, name) DO UPDATE SET
			ordinal=excluded.ordinal, is_sample=excluded.is_sample, score=excluded.score,
			input_rel=excluded.input_rel, output_rel=excluded.output_rel`,
		tc.ProblemID, tc.Ordinal, tc.Name, boolInt(tc.IsSample), tc.Score, tc.InputRel, tc.OutputRel)
	if err != nil {
		return Testcase{}, err
	}
	return db.GetTestcase(ctx, tc.ProblemID, tc.Name)
}
