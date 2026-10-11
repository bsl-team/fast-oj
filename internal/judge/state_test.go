package judge

import "testing"

func TestTransitionTable(t *testing.T) {
	legal := [][2]Status{
		{Queued, Compiling}, {Queued, Abandoned},
		{Compiling, Running}, {Compiling, Done}, {Compiling, Abandoned},
		{Running, Running}, {Running, Done}, {Running, Abandoned},
		{Abandoned, Queued},
	}
	for _, pair := range legal {
		if !Transition(pair[0], pair[1]) {
			t.Errorf("%s -> %s should be legal", pair[0], pair[1])
		}
	}

	illegal := [][2]Status{
		{Queued, Queued}, {Queued, Done}, {Queued, Running},
		{Compiling, Queued}, {Running, Compiling}, {Running, Queued},
		{Done, Queued}, {Done, Running}, {Done, Abandoned}, {Done, Done},
		{Abandoned, Done}, {Abandoned, Compiling},
	}
	for _, pair := range illegal {
		if Transition(pair[0], pair[1]) {
			t.Errorf("%s -> %s must be rejected", pair[0], pair[1])
		}
	}
}

func TestInFlightStatuses(t *testing.T) {
	for _, s := range []Status{Queued, Compiling, Running} {
		if !s.InFlight() {
			t.Errorf("%s should be in flight", s)
		}
	}
	for _, s := range []Status{Done, Abandoned, Status("bogus")} {
		if s.InFlight() {
			t.Errorf("%s should be settled", s)
		}
	}
}

func TestVerdictName(t *testing.T) {
	for _, v := range []Verdict{AC, WA, TLE, MLE, RE, CE, IE} {
		if got := VerdictName(string(v)); got != v {
			t.Errorf("VerdictName(%q) = %q", v, got)
		}
	}
	for _, raw := range []string{"", "ac", "OSError", "AC ", "MEMORY"} {
		if got := VerdictName(raw); got != IE {
			t.Errorf("VerdictName(%q) = %q, want IE", raw, got)
		}
	}
	if !AC.Accepted() || WA.Accepted() || IE.Accepted() {
		t.Fatal("only AC is accepted")
	}
}

func TestStatusStringsMatchSchemaCheck(t *testing.T) {
	// The schema CHECK constraint lists these exact values.
	for _, s := range []Status{Queued, Compiling, Running, Done, Abandoned} {
		switch string(s) {
		case "queued", "compiling", "running", "done", "abandoned":
		default:
			t.Errorf("status %q is not allowed by the schema", s)
		}
	}
}
