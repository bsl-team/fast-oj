package store

import (
	"context"
	"fmt"
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

func (db *DB) ProblemScoreTotal(ctx context.Context, problemID int64) (int, error) {
	var total int
	err := db.SQL.QueryRowContext(ctx,
		`SELECT IFNULL(SUM(score), 0) FROM testcases WHERE problem_id=? AND is_sample=0`, problemID).Scan(&total)
	return total, err
}
