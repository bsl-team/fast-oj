package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// dayLayout is the calendar key a check-in is counted against. Local dates, so
// "today" follows whatever zone the server runs in.
const dayLayout = "2006-01-02"

// CheckInState is what a page shows about one person's streak.
type CheckInState struct {
	Streak  int    `json:"streak"`
	Total   int    `json:"total"`
	LastDay string `json:"last_day,omitempty"`
	Today   bool   `json:"checked_in_today"`
}

// pow3Penalty is 3^missed, capped at a billion: a long absence resets the streak
// regardless, and an uncapped exponent on a year-old gap would overflow.
func pow3Penalty(missed int) int {
	const ceiling = 1 << 30
	p := 1
	for i := 0; i < missed; i++ {
		p *= 3
		if p >= ceiling {
			return ceiling
		}
	}
	return p
}

// dayGap counts the days *missed* between two calendar days: consecutive days have
// a gap of 0, one skipped day has 1.
func dayGap(from, to string) (int, error) {
	f, err := time.Parse(dayLayout, from)
	if err != nil {
		return 0, err
	}
	t, err := time.Parse(dayLayout, to)
	if err != nil {
		return 0, err
	}
	gap := int(t.Sub(f).Hours()/24) - 1
	if gap < 0 {
		gap = 0
	}
	return gap, nil
}

// CheckIn records today's check-in and returns the resulting streak plus whether
// this call added one. Missing n days costs 3^n streak days, floored at 1 so today
// always starts a fresh run. Checking in twice in a day changes nothing.
func (db *DB) CheckIn(ctx context.Context, userID int64, now time.Time) (streak int, added bool, err error) {
	today := now.Format(dayLayout)

	var (
		lastDay string
		prev    int
	)
	err = db.SQL.QueryRowContext(ctx,
		`SELECT day, streak FROM checkins WHERE user_id=? ORDER BY day DESC LIMIT 1`, userID).
		Scan(&lastDay, &prev)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	if errors.Is(err, sql.ErrNoRows) {
		lastDay, prev = "", 0
	}
	if lastDay == today {
		return prev, false, nil
	}

	streak = prev + 1
	if lastDay != "" {
		missed, err := dayGap(lastDay, today)
		if err != nil {
			return 0, false, err
		}
		if missed > 0 {
			streak -= pow3Penalty(missed)
		}
	}
	if streak < 1 {
		streak = 1
	}

	res, err := db.SQL.ExecContext(ctx,
		`INSERT INTO checkins (user_id, day, streak, created_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(user_id, day) DO NOTHING`, userID, today, streak, now.UnixMilli())
	if err != nil {
		return 0, false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Someone else's request already wrote today between our read and write.
		return prev, false, nil
	}
	return streak, true, nil
}

// CheckInState reads the streak without writing anything.
func (db *DB) CheckInState(ctx context.Context, userID int64, now time.Time) (CheckInState, error) {
	var st CheckInState
	var lastDay sql.NullString
	var streak sql.NullInt64
	err := db.SQL.QueryRowContext(ctx,
		`SELECT day, streak FROM checkins WHERE user_id=? ORDER BY day DESC LIMIT 1`, userID).
		Scan(&lastDay, &streak)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return st, err
	}
	if err = db.SQL.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM checkins WHERE user_id=?`, userID).Scan(&st.Total); err != nil {
		return st, err
	}
	if lastDay.Valid {
		st.LastDay = lastDay.String
		st.Streak = int(streak.Int64)
		st.Today = lastDay.String == now.Format(dayLayout)
	}
	return st, nil
}
