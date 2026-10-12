package api

import "testing"

// The columns are compared by eye across rows, so a list that mixes "15ms" with
// "1s", or prints 6.73046875M, is unusable. A number from a run that was killed
// mid-flight is a lower bound and has to look like one.
func TestDurationsAndSizesReadConsistently(t *testing.T) {
	times := []struct {
		ms   int
		min  bool
		want string
	}{
		{-1, false, "-"},
		{-1, true, "-"},
		{0, false, "0ms"},
		{15, false, "15ms"},
		{999, false, "999ms"},
		{1000, false, "1000ms"},
		{1050, false, "1050ms"},
		{1050, true, ">1050ms"},
	}
	for _, c := range times {
		if got := formatMS(c.ms, c.min); got != c.want {
			t.Errorf("formatMS(%d, %v) = %q, want %q", c.ms, c.min, got, c.want)
		}
	}

	sizes := []struct {
		kb   int
		min  bool
		want string
	}{
		{-1, false, "-"},
		{0, false, "0K"},
		{948, false, "948K"},
		{1023, false, "1023K"},
		{1024, false, "1.00M"},
		{6892, false, "6.73M"},
		{524940, false, "512.64M"},
		{524940, true, ">512.64M"},
		{900, true, ">900K"},
	}
	for _, c := range sizes {
		if got := formatKB(c.kb, c.min); got != c.want {
			t.Errorf("formatKB(%d, %v) = %q, want %q", c.kb, c.min, got, c.want)
		}
	}
}
