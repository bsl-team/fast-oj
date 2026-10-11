package api

import "testing"

// The columns are compared by eye across rows, so a list that mixes "15ms" with
// "1s", or prints 6.73046875M, is unusable.
func TestDurationsAndSizesReadConsistently(t *testing.T) {
	times := map[int]string{
		-1: "-", 0: "0ms", 15: "15ms", 999: "999ms", 1000: "1000ms", 1050: "1050ms",
	}
	for ms, want := range times {
		if got := formatMS(ms); got != want {
			t.Errorf("formatMS(%d) = %q, want %q", ms, got, want)
		}
	}

	sizes := map[int]string{
		-1: "-", 0: "0K", 948: "948K", 1023: "1023K", 1024: "1.00M",
		6892: "6.73M", 524940: "512.64M",
	}
	for kb, want := range sizes {
		if got := formatKB(kb); got != want {
			t.Errorf("formatKB(%d) = %q, want %q", kb, got, want)
		}
	}
}
