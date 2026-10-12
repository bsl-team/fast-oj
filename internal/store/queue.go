package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ClaimedTask is everything a judge box needs to start work on one submission.
type ClaimedTask struct {
	SubmissionID int64
	ProblemID    int64
	PID          string
	Language     string
	Source       string
	TimeLimitMS  int
	MemLimitKB   int
}

// ClaimNext hands out the oldest queued submission to this judge, or
// sql.ErrNoRows when the queue is empty.
//
// The claim is one statement: the inner SELECT picks the row, the outer UPDATE
// stamps it, and the status guard inside the subquery means two boxes racing
// for the same row cannot both get a write. RETURNING gives the winner back
// without a second round trip.
func (db *DB) ClaimNext(ctx context.Context, judgeID int64) (ClaimedTask, error) {
	var t ClaimedTask
	var status string
	err := db.SQL.QueryRowContext(ctx,
		`UPDATE submissions
		 SET status='compiling', judge_id=?, started_at=?
		 WHERE id = (SELECT id FROM submissions WHERE status='queued' ORDER BY id LIMIT 1)
		 RETURNING id, problem_id, status`, judgeID, nowMS()).
		Scan(&t.SubmissionID, &t.ProblemID, &status)
	if err != nil {
		return t, notFound(err)
	}
	if status != "compiling" {
		return t, fmt.Errorf("claim returned status %q", status)
	}

	// Source can be large, so it is fetched only now, for the judge that owns
	// the row.
	err = db.SQL.QueryRowContext(ctx,
		`SELECT s.language, s.source, p.pid, p.time_limit_ms, p.mem_limit_kb
		 FROM submissions s JOIN problems p ON p.id=s.problem_id
		 WHERE s.id=? AND s.judge_id=?`, t.SubmissionID, judgeID).
		Scan(&t.Language, &t.Source, &t.PID, &t.TimeLimitMS, &t.MemLimitKB)
	if err != nil {
		return t, notFound(err)
	}
	return t, nil
}

// ReportStatus moves a claimed submission along the state machine. Only the
// judge that owns the row may report, and only from the state it is really in.
// An empty detail leaves the previous note alone.
// errStaleStatus is a control signal, not a failure: the row moved on while this
// judge was working, so its report no longer applies.
var errStaleStatus = errors.New("submission status moved on")

func (db *DB) ReportStatus(ctx context.Context, submissionID, judgeID int64, from, to, detail string) (bool, error) {
	return db.ReportProgress(ctx, submissionID, judgeID, from, to, detail, nil)
}

// ReportProgress moves the state and, when a finished case rides along, writes that
// row in the same transaction. Either both land or neither does, so the detail page
// can never show a case row belonging to a state the submission has moved past.
func (db *DB) ReportProgress(ctx context.Context, submissionID, judgeID int64, from, to, detail string, run *Run) (bool, error) {
	err := db.InTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE submissions
			 SET status=?, detail=CASE WHEN ?='' THEN detail ELSE ? END
			 WHERE id=? AND judge_id=? AND status=?`,
			to, detail, detail, submissionID, judgeID, from)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return errStaleStatus
		}
		if run != nil {
			run.SubmissionID = submissionID
			if err := writeRun(ctx, tx, *run); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, errStaleStatus) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// ReapStale marks in-flight submissions that no judge is working on any more as
// abandoned, so a dead judge box cannot wedge the queue forever.
func (db *DB) ReapStale(ctx context.Context, leaseMS int64) (int64, error) {
	res, err := db.SQL.ExecContext(ctx,
		`UPDATE submissions SET status='abandoned', verdict='IE', detail=?
		 WHERE status IN ('compiling','running') AND started_at IS NOT NULL
		   AND started_at < ?`,
		"judge lease expired", nowMS()-leaseMS)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// RequeueAbandoned puts an abandoned row back at the tail of the queue.
// ClearRuns drops the case rows of an attempt that is about to be judged again,
// so a re-run never shows rows from the attempt that died.
func (db *DB) ClearRuns(ctx context.Context, submissionID int64) error {
	_, err := db.SQL.ExecContext(ctx, `DELETE FROM runs WHERE submission_id=?`, submissionID)
	return err
}

// RequeueAbandoned puts a dead attempt back in the queue and drops its case rows,
// so a re-run never shows results from the attempt that died.
func (db *DB) RequeueAbandoned(ctx context.Context, submissionID int64) (bool, error) {
	requeued := false
	err := db.InTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE submissions SET status='queued', verdict='', judge_id=NULL, detail='',
			           time_ms=-1, mem_kb=-1, score=0, started_at=NULL, judged_at=NULL
			 WHERE id=? AND status='abandoned'`, submissionID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return errStaleStatus
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM runs WHERE submission_id=?`, submissionID); err != nil {
			return err
		}
		requeued = true
		return nil
	})
	if errors.Is(err, errStaleStatus) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return requeued, nil
}
