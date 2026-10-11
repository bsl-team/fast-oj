package store

import (
	"context"
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
// judge that owns the row may report, and only legal transitions are written.
func (db *DB) ReportStatus(ctx context.Context, submissionID, judgeID int64, from, to string) (bool, error) {
	res, err := db.SQL.ExecContext(ctx,
		`UPDATE submissions SET status=? WHERE id=? AND judge_id=? AND status=?`,
		to, submissionID, judgeID, from)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
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
func (db *DB) RequeueAbandoned(ctx context.Context, submissionID int64) (bool, error) {
	res, err := db.SQL.ExecContext(ctx,
		`UPDATE submissions SET status='queued', verdict='', judge_id=NULL, detail='',
		           started_at=NULL, judged_at=NULL
		 WHERE id=? AND status='abandoned'`, submissionID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}
