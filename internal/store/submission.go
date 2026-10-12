package store

import (
	"context"
	"database/sql"
	"errors"
)

const submissionColumns = `id, user_id, problem_id, language, status, verdict,
		time_ms, mem_kb, time_min, mem_min, score, total_score, failed_case, IFNULL(judge_id, 0),
		created_at, IFNULL(started_at, 0), IFNULL(judged_at, 0)`

func scanSubmission(row scanner) (Submission, error) {
	var s Submission
	var timeMin, memMin int
	err := row.Scan(&s.ID, &s.UserID, &s.ProblemID, &s.Language, &s.Status, &s.Verdict,
		&s.TimeMS, &s.MemKB, &timeMin, &memMin, &s.Score, &s.TotalScore, &s.FailedCase, &s.JudgeID,
		&s.CreatedAt, &s.StartedAt, &s.JudgedAt)
	s.TimeMin, s.MemMin = timeMin != 0, memMin != 0
	return s, notFound(err)
}

func (db *DB) CreateSubmission(ctx context.Context, s *Submission) (Submission, error) {
	res, err := db.SQL.ExecContext(ctx,
		`INSERT INTO submissions (user_id, problem_id, language, source, status, total_score, created_at)
		 VALUES (?, ?, ?, ?, 'queued', ?, ?)`,
		s.UserID, s.ProblemID, s.Language, s.Source, s.TotalScore, nowMS())
	if err != nil {
		return Submission{}, err
	}
	s.ID, _ = res.LastInsertId()
	s.Status = "queued"
	s.CreatedAt = nowMS()
	return *s, nil
}

func (db *DB) GetSubmission(ctx context.Context, id int64) (Submission, error) {
	return scanSubmission(db.SQL.QueryRowContext(ctx,
		`SELECT `+submissionColumns+` FROM submissions WHERE id=?`, id))
}

// Source is kept out of the default column set: listing submissions must not
// drag megabytes of code through the response path.
func (db *DB) SubmissionPayload(ctx context.Context, id int64) (source, detail string, err error) {
	err = db.SQL.QueryRowContext(ctx,
		`SELECT source, detail FROM submissions WHERE id=?`, id).Scan(&source, &detail)
	return source, detail, notFound(err)
}

type SubmissionFilter struct {
	UserID    int64
	ProblemID int64
	AfterID   int64
	Limit     int
}

func (db *DB) ListSubmissions(ctx context.Context, f SubmissionFilter) ([]Submission, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 50
	}
	q := `SELECT ` + submissionColumns + ` FROM submissions WHERE 1=1`
	args := []any{}
	if f.UserID != 0 {
		q += ` AND user_id=?`
		args = append(args, f.UserID)
	}
	if f.ProblemID != 0 {
		q += ` AND problem_id=?`
		args = append(args, f.ProblemID)
	}
	if f.AfterID != 0 {
		q += ` AND id<?`
		args = append(args, f.AfterID)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, f.Limit)

	rows, err := db.SQL.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Submission, 0, f.Limit)
	for rows.Next() {
		s, err := scanSubmission(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SetStatus performs a compare-and-set on the status column. The guard is what
// makes a duplicate judge claim a no-op instead of a reassignment.
func (db *DB) SetStatus(ctx context.Context, id int64, from, to string, judgeID int64) (bool, error) {
	res, err := db.SQL.ExecContext(ctx,
		`UPDATE submissions
		 SET status=?, judge_id=?, started_at=IFNULL(started_at, ?)
		 WHERE id=? AND status=?`,
		to, judgeID, nowMS(), id, from)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

type Result struct {
	Verdict    string
	TimeMS     int
	MemKB      int
	TimeMin    bool
	MemMin     bool
	Score      int
	TotalScore int
	FailedCase string
	Detail     string
}

// RecordResult stores the verdict and per-case runs in one transaction, so a
// submission is never visible as done without its runs.
func (db *DB) RecordResult(ctx context.Context, id int64, r Result, runs []Run) error {
	return db.InTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE submissions SET status='done', verdict=?, time_ms=?, mem_kb=?, time_min=?, mem_min=?,
			           score=?, total_score=?, failed_case=?, detail=?, judged_at=?
			 WHERE id=?`,
			r.Verdict, r.TimeMS, r.MemKB, boolInt(r.TimeMin), boolInt(r.MemMin),
			r.Score, r.TotalScore, r.FailedCase, r.Detail, nowMS(), id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM runs WHERE submission_id=?`, id); err != nil {
			return err
		}
		for _, run := range runs {
			run.SubmissionID = id
			if err := insertRun(ctx, tx, run); err != nil {
				return err
			}
		}
		return nil
	})
}

func insertRun(ctx context.Context, tx *sql.Tx, run Run) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO runs (submission_id, ordinal, case_name, verdict, time_ms, mem_kb, is_sample,
		                   time_min, mem_min)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.SubmissionID, run.Ordinal, run.CaseName, run.Verdict, run.TimeMS, run.MemKB, boolInt(run.IsSample),
		boolInt(run.TimeMin), boolInt(run.MemMin))
	return err
}

func (db *DB) AddRun(ctx context.Context, run Run) (Run, error) {
	res, err := db.SQL.ExecContext(ctx,
		`INSERT INTO runs (submission_id, ordinal, case_name, verdict, time_ms, mem_kb, is_sample,
		                   time_min, mem_min)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.SubmissionID, run.Ordinal, run.CaseName, run.Verdict, run.TimeMS, run.MemKB, boolInt(run.IsSample),
		boolInt(run.TimeMin), boolInt(run.MemMin))
	if err != nil {
		return Run{}, err
	}
	run.ID, _ = res.LastInsertId()
	return run, nil
}

func (db *DB) ListRuns(ctx context.Context, submissionID int64) ([]Run, error) {
	rows, err := db.SQL.QueryContext(ctx,
		`SELECT id, submission_id, ordinal, case_name, verdict, time_ms, mem_kb, is_sample, time_min, mem_min
		 FROM runs WHERE submission_id=? ORDER BY ordinal`, submissionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Run
	for rows.Next() {
		var (
			r                       Run
			sample, timeMin, memMin int
		)
		if err := rows.Scan(&r.ID, &r.SubmissionID, &r.Ordinal, &r.CaseName, &r.Verdict,
			&r.TimeMS, &r.MemKB, &sample, &timeMin, &memMin); err != nil {
			return nil, err
		}
		r.IsSample = sample != 0
		r.TimeMin, r.MemMin = timeMin != 0, memMin != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// PendingCount is what the CLI polls while waiting for a verdict.
func (db *DB) PendingCount(ctx context.Context) (int, error) {
	var n int
	err := db.SQL.QueryRowContext(ctx, `SELECT COUNT(*) FROM submissions WHERE status='queued'`).Scan(&n)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	return n, err
}
