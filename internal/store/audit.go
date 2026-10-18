package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// audit.go is the durable trail of privileged actions. It mirrors the append-only
// row already used by ticket_replies.event (migration 0016): one line per thing
// that happened, written next to the action rather than instead of it.
//
// There is deliberately no UpdateAudit/DeleteAudit. The value of an audit line is
// that it says what was true at the time, and the people with the reach to change
// such a row are exactly the people it describes.

const (
	// The bounds on every attacker- or operator-controlled string. This table is
	// only ever appended to, so an unbounded column would turn "someone can act
	// once" into "someone can fill the disk": the limit is on the row, not on the
	// request rate.
	MaxAuditAction = 64
	MaxAuditActor  = 64
	MaxAuditTarget = 200
	MaxAuditDetail = 500
	MaxAuditIP     = 64

	DefaultAuditPage = 50
	MaxAuditPage     = 200
)

// AuditEvent is one line of the trail. Everything except Action may be empty:
// a judge-token refusal has no account and often no target, and that is still a
// thing that happened.
type AuditEvent struct {
	// ActorID is the account behind the action, 0 when the credential was a token
	// or nothing at all.
	ActorID int64
	// Actor is the handle as it read at the moment of the action. It is stored as
	// text because the account can be cancelled and its name re-registered by a
	// different person; the line must keep naming the one who did it.
	Actor  string
	Action string
	Target string
	Detail string
	IP     string
}

type AuditRow struct {
	ID      int64  `json:"id"`
	At      int64  `json:"at"`
	ActorID int64  `json:"actor_id"`
	Actor   string `json:"actor"`
	Action  string `json:"action"`
	Target  string `json:"target"`
	Detail  string `json:"detail"`
	IP      string `json:"ip"`
}

// Audit appends one row.
//
// A write failure here must never block or roll back the action being recorded:
// an audit trail that can take the site down by making one column unhappy is the
// wrong trade, and the caller has already changed real state by the time it logs.
// The error is therefore returned for the caller to *log*, and the API layer's
// helper reduces it to a stderr line — the action still happens, and the gap is
// visible to whoever reads the log.
func (db *DB) Audit(ctx context.Context, e AuditEvent) error {
	e.Action = clip(e.Action, MaxAuditAction)
	if e.Action == "" {
		return errors.New("审计要有动作")
	}
	e.Actor = clip(e.Actor, MaxAuditActor)
	e.Target = clip(e.Target, MaxAuditTarget)
	e.Detail = clip(e.Detail, MaxAuditDetail)
	e.IP = clip(e.IP, MaxAuditIP)
	_, err := db.SQL.ExecContext(ctx,
		`INSERT INTO audit (at, actor_id, actor, action, target, detail, ip)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		nowMS(), e.ActorID, e.Actor, e.Action, e.Target, e.Detail, e.IP)
	return err
}

// clip bounds a value in runes (that is what the reader counts, and what a Chinese
// sentence runs to) and flattens it onto one line, so a note typed with newlines
// cannot make the table unreadable or smuggle a fake entry into a log line.
func clip(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', '\t', 0:
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	// The ellipsis is part of the budget, so the stored value is never longer
	// than the documented bound.
	return string([]rune(s)[:n-1]) + "…"
}

// AuditFilter narrows the walk. The zero value is "the newest lines of everything",
// which is what the operator's page shows.
type AuditFilter struct {
	Action string
	Actor  string
	// AfterID is the paging cursor: rows with a smaller id, which is also the sort
	// key. A cursor that disagrees with the ORDER BY would repeat or drop rows.
	AfterID int64
	Limit   int
}

// ListAudit reads the trail newest first: an operator opening this page is asking
// "what just happened", not "what happened at the beginning".
func (db *DB) ListAudit(ctx context.Context, f AuditFilter) ([]AuditRow, error) {
	limit := f.Limit
	switch {
	case limit <= 0:
		limit = DefaultAuditPage
	case limit > MaxAuditPage:
		limit = MaxAuditPage
	}
	where := []string{"1=1"}
	var args []any
	if f.Action != "" {
		where = append(where, "action = ?")
		args = append(args, f.Action)
	}
	if f.Actor != "" {
		where = append(where, "actor = ?")
		args = append(args, f.Actor)
	}
	if f.AfterID > 0 {
		where = append(where, "id < ?")
		args = append(args, f.AfterID)
	}
	q := `SELECT id, at, actor_id, actor, action, target, detail, ip FROM audit
	      WHERE ` + strings.Join(where, " AND ") + `
	      ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := db.SQL.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]AuditRow, 0, limit)
	for rows.Next() {
		var r AuditRow
		if err := rows.Scan(&r.ID, &r.At, &r.ActorID, &r.Actor, &r.Action, &r.Target, &r.Detail, &r.IP); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountAudit backs the one number the page can state honestly: rows this filter
// matched in total, not just this page.
func (db *DB) CountAudit(ctx context.Context, f AuditFilter) (int, error) {
	where := []string{"1=1"}
	var args []any
	if f.Action != "" {
		where = append(where, "action = ?")
		args = append(args, f.Action)
	}
	if f.Actor != "" {
		where = append(where, "actor = ?")
		args = append(args, f.Actor)
	}
	var n int
	err := db.SQL.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM audit WHERE `+strings.Join(where, " AND "), args...).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count audit: %w", err)
	}
	return n, nil
}
