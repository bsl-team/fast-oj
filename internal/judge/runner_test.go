package judge

import (
	"testing"
	"time"
)

func TestVerdictForUsesTheMeasuredTime(t *testing.T) {
	lim := Limits{TimeMS: 1000, MemKB: 262144}

	cases := []struct {
		name       string
		timeMS     int
		memKB      int
		exit       int
		overflowed bool
		want       Verdict
	}{
		{"under both limits", 15, 948, 0, false, AC},
		{"exactly at the limit is not TLE", 1000, 948, 0, false, AC},
		{"one millisecond over still counts", 1001, 948, 0, false, TLE},
		{"clean exit over the limit is not AC", 1038, 948, 0, false, TLE},
		{"over the memory cap", 15, 524940, 0, false, MLE},
		{"memory beats a nonzero exit", 15, 524940, 137, false, MLE},
		{"time beats a nonzero exit", 1050, 948, 137, false, TLE},
		{"crash within limits", 15, 948, 1, false, RE},
		{"overflowed output cannot match", 15, 948, 0, true, WA},
		{"overflow beats time and memory", 9999, 9999999, 1, true, WA},
	}
	for _, c := range cases {
		got, _ := verdictFor(lim, c.timeMS, c.memKB, c.exit, c.overflowed)
		if got != c.want {
			t.Errorf("%s: verdict = %s, want %s", c.name, got, c.want)
		}
	}

	// A problem without limits must not invent them.
	unlimited := Limits{TimeMS: 0, MemKB: 0}
	if got, _ := verdictFor(unlimited, 1<<20, 1<<20, 0, false); got != AC {
		t.Errorf("unlimited problem = %s, want AC", got)
	}
}

func TestWallClockGivesEveryRunTheSlackToBeMeasured(t *testing.T) {
	if got, want := wallClockFor(1000), 1050*time.Millisecond; got != want {
		t.Errorf("wall clock for 1000ms = %s, want %s", got, want)
	}
	// Tiny limits still leave room for process creation.
	if got := wallClockFor(10); got < minWallClock {
		t.Errorf("wall clock for 10ms = %s, want at least %s", got, minWallClock)
	}
}
