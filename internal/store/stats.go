package store

import (
	"context"
	"fmt"
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
