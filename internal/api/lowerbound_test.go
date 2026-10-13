package api

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/liangbinchen2013/fast-oj/internal/store"
)

// A run that was cut off cannot report its true cost, and printing the number
// without saying so reads as a measurement.
func TestKilledRunsAreShownAsLowerBounds(t *testing.T) {
	h := newHarness(t)
	h.readyProblem("P4010")
	cookie, csrf := h.login("cut", "cut@example.com", testPassword)

	code, _, hdr := h.postForm("/problems/P4010/submit",
		url.Values{"language": {"cpp"}, "source": {"int main(){while(1);}"}}, cookie, csrf)
	if code != http.StatusSeeOther {
		t.Fatalf("submit = %d", code)
	}
	loc := hdr.Get("Location")

	h.finishSubmission(1, store.Result{
		Verdict: "TLE", TimeMS: 1050, TimeMin: true, MemKB: 524940, MemMin: true,
		Score: 0, TotalScore: 2, FailedCase: "1",
	}, []store.Run{
		{Ordinal: 1, CaseName: "sample1", Verdict: "TLE", TimeMS: 1050, TimeMin: true, MemKB: 524940, MemMin: true, IsSample: true},
		{Ordinal: 2, CaseName: "1", Verdict: "TLE", TimeMS: 1050, TimeMin: true, MemKB: 1024},
		{Ordinal: 3, CaseName: "2", Verdict: "AC", TimeMS: 12, MemKB: 1024},
	})

	_, page := h.htmlWithCookie(http.MethodGet, loc, cookie)
	// The page escapes the marker, so it reads as &gt;1050ms in the source.
	for _, want := range []string{"&gt;1050ms", "&gt;512.64M"} {
		if !strings.Contains(page, want) {
			t.Errorf("judged page missing %q:\n%s", want, page)
		}
	}
	// The clean case in the same table stays exact.
	if !strings.Contains(page, "12ms") || strings.Contains(page, "&gt;12ms") {
		t.Error("a run that finished is marked as a lower bound")
	}

	if _, list := h.htmlGet("/submissions", ""); !strings.Contains(list, "&gt;1050ms") {
		t.Error("the list view lost the bound")
	}
}
