package store

import (
	"context"
	"testing"
	"time"
)

func day(n int) time.Time {
	return time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC).AddDate(0, 0, n)
}

func seedStreak(t *testing.T, db *DB, userID int64, at time.Time, streak int) {
	t.Helper()
	if _, err := db.SQL.ExecContext(context.Background(),
		`INSERT INTO checkins (user_id, day, streak, created_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(user_id, day) DO UPDATE SET streak=excluded.streak`,
		userID, at.Format(dayLayout), streak, at.UnixMilli()); err != nil {
		t.Fatal(err)
	}
}

func TestCheckInStreakAndPenalty(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	u := mustUser(t, db, "punch21")

	// First ever check-in starts at one, and repeating it the same day changes
	// nothing at all.
	streak, added, err := db.CheckIn(ctx, u.ID, day(0))
	if err != nil || streak != 1 || !added {
		t.Fatalf("first check-in = %d/%v %v", streak, added, err)
	}
	if streak, added, err = db.CheckIn(ctx, u.ID, day(0)); err != nil || streak != 1 || added {
		t.Fatalf("same day again = %d/%v %v", streak, added, err)
	}
	if streak, _, err = db.CheckIn(ctx, u.ID, day(1)); err != nil || streak != 2 {
		t.Fatalf("next day = %d %v, want 2", streak, err)
	}

	cases := []struct {
		name    string
		have    int
		lastDay int
		comeDay int
		want    int
	}{
		{"consecutive keeps adding", 30, 10, 11, 31},
		{"one missed day costs 3", 30, 10, 12, 28},
		{"two missed days cost 9", 30, 10, 13, 22},
		{"three missed days cost 27", 30, 10, 14, 4},
		{"a long absence floors at one", 30, 10, 40, 1},
	}
	for i, c := range cases {
		// Each case needs its own history: CheckIn reads the most recent row, so
		// the previous case's check-in would be the one it sees.
		pc := mustUser(t, db, "case"+string(rune('a'+i)))
		seedStreak(t, db, pc.ID, day(c.lastDay), c.have)
		streak, added, err := db.CheckIn(ctx, pc.ID, day(c.comeDay))
		if err != nil {
			t.Fatal(err)
		}
		if !added {
			t.Errorf("%s: the check-in was not recorded", c.name)
		}
		if streak != c.want {
			t.Errorf("%s: streak = %d, want %d", c.name, streak, c.want)
		}
	}

	// u's own history is just day 0 and day 1 at this point.
	st, err := db.CheckInState(ctx, u.ID, day(1))
	if err != nil || st.Streak != 2 || st.Total != 2 || !st.Today {
		t.Fatalf("state = %+v %v, want streak 2, total 2, today", st, err)
	}
	if st, _ = db.CheckInState(ctx, u.ID, day(40)); st.Today || st.Streak != 2 {
		t.Errorf("state on an untouched day = %+v, want streak 2 and not today", st)
	}
	// Coming back after 38 missed days costs more than the streak holds.
	if streak, added, _ := db.CheckIn(ctx, u.ID, day(40)); !added || streak != 1 {
		t.Fatalf("return after a long absence = %d/%v, want 1/true", streak, added)
	}

	// A person who never checked in has a zero state, not an error.
	quiet := mustUser(t, db, "quiet21")
	if st, err := db.CheckInState(ctx, quiet.ID, day(40)); err != nil || st.Streak != 0 || st.Today || st.Total != 0 {
		t.Errorf("empty state = %+v %v", st, err)
	}
}

func TestCheckInCountsDaysNotRows(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	u := mustUser(t, db, "daily21")

	for i := 0; i < 5; i++ {
		if _, _, err := db.CheckIn(ctx, u.ID, day(i)); err != nil {
			t.Fatal(err)
		}
	}
	st, err := db.CheckInState(ctx, u.ID, day(4))
	if err != nil || st.Streak != 5 || st.Total != 5 {
		t.Fatalf("state = %+v %v, want 5/5", st, err)
	}
	// The primary key is (user, day), so a duplicate write cannot inflate the
	// total even if a retry lands twice.
	if _, added, _ := db.CheckIn(ctx, u.ID, day(4)); added {
		t.Error("a repeat on the same day was counted again")
	}
}
