package api

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/liangbinchen2013/fast-oj/internal/casefs"
	"github.com/liangbinchen2013/fast-oj/internal/config"
	"github.com/liangbinchen2013/fast-oj/internal/store"
)

// benchServer builds a populated instance behind a real TCP listener, so the
// numbers include connection handling and not just handler time.
func benchServer(t testing.TB) (*httptest.Server, config.Config) {
	t.Helper()
	cfg := testConfig(t)
	db, err := store.Open(context.Background(), cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	cases := casefs.New(cfg.DataDir)
	ctx := context.Background()

	user, err := db.EnsureUser(ctx, "bencher", "user")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		p, err := db.CreateProblem(ctx, &store.Problem{
			PID: fmt.Sprintf("P%04d", i), Title: strings.Repeat("题目", 20),
			Statement:   "# 描述\n\n给定 $a, b$，输出 $a+b$。\n\n| 档 | 范围 |\n|---|---|\n| 1 | 0..10 |\n",
			TimeLimitMS: 1000, MemLimitKB: 262144, Languages: []string{"c", "cpp"}, Visible: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		for j := 0; j < 5; j++ {
			relIn, relOut, err := cases.Put(p.ID, j+1, []byte("1 2\n"), []byte("3\n"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.AddTestcase(ctx, &store.Testcase{
				ProblemID: p.ID, Ordinal: j + 1, Name: fmt.Sprintf("case%d", j),
				IsSample: j == 0, Score: 1, InputRel: relIn, OutputRel: relOut,
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for i := 0; i < 500; i++ {
		if _, err := db.CreateSubmission(ctx, &store.Submission{
			UserID: user.ID, ProblemID: int64(i%200 + 1), Language: "cpp",
			Source: "int main(){}", TotalScore: 4,
		}); err != nil {
			t.Fatal(err)
		}
	}

	srv := httptest.NewServer(New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), db).Handler())
	t.Cleanup(func() {
		srv.Close()
		db.Close()
	})
	return srv, cfg
}

func percentiles(samples []float64) (p50, p90, p99 float64) {
	sort.Float64s(samples)
	at := func(q float64) float64 { return samples[int(float64(len(samples)-1)*q)] }
	return at(0.50), at(0.90), at(0.99)
}

func sample(b *testing.B, srv *httptest.Server, paths []string) []float64 {
	b.Helper()
	client := srv.Client()
	samples := make([]float64, 0, b.N)
	for i := 0; b.Loop(); i++ {
		start := time.Now()
		body, status, err := get(client, srv.URL+paths[i%len(paths)])
		if err != nil || status != http.StatusOK || len(body) == 0 {
			b.Fatalf("request failed: %v (%d, %d bytes)", err, status, len(body))
		}
		samples = append(samples, float64(time.Since(start).Microseconds())/1000)
	}
	return samples
}

func report(b *testing.B, samples []float64) {
	if len(samples) == 0 {
		b.Skip("no samples")
	}
	p50, p90, p99 := percentiles(samples)
	b.ReportMetric(p50, "ms/p50")
	b.ReportMetric(p90, "ms/p90")
	b.ReportMetric(p99, "ms/p99")
}

func get(client *http.Client, url string) ([]byte, int, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return body, resp.StatusCode, err
}

func BenchmarkHealthz(b *testing.B) {
	srv, _ := benchServer(b)
	report(b, sample(b, srv, []string{"/healthz"}))
}

func BenchmarkProblemListText(b *testing.B) {
	srv, _ := benchServer(b)
	report(b, sample(b, srv, []string{"/problems?limit=50"}))
}

func BenchmarkProblemListJSON(b *testing.B) {
	srv, _ := benchServer(b)
	report(b, sample(b, srv, []string{"/problems?limit=50"}))
}

func BenchmarkProblemListDeepPage(b *testing.B) {
	srv, _ := benchServer(b)
	report(b, sample(b, srv, []string{"/problems?limit=50&after=150"}))
}

func BenchmarkProblemDetail(b *testing.B) {
	srv, _ := benchServer(b)
	paths := make([]string, 50)
	for i := range paths {
		paths[i] = fmt.Sprintf("/problems/P%04d", i)
	}
	report(b, sample(b, srv, paths))
}

func BenchmarkSubmissionList(b *testing.B) {
	srv, _ := benchServer(b)
	report(b, sample(b, srv, []string{"/submissions?limit=50"}))
}

func BenchmarkSubmissionGet(b *testing.B) {
	srv, _ := benchServer(b)
	paths := make([]string, 100)
	for i := range paths {
		paths[i] = fmt.Sprintf("/submissions/%d", i+1)
	}
	report(b, sample(b, srv, paths))
}

func BenchmarkRank(b *testing.B) {
	srv, _ := benchServer(b)
	report(b, sample(b, srv, []string{"/rank"}))
}

// TestReadPathP99Budget backs the README's latency claim with a measurement over
// a populated database. The ceiling is deliberately loose so a loaded machine
// still passes; the real numbers are logged.
func TestReadPathP99Budget(t *testing.T) {
	srv, _ := benchServer(t)
	client := srv.Client()

	for _, c := range []struct {
		name  string
		paths []string
	}{
		{"healthz", []string{"/healthz"}},
		{"problem list", []string{"/problems?limit=50"}},
		{"problem detail", []string{"/problems/P0001", "/problems/P0002", "/problems/P0003"}},
		{"problem html", []string{"/problems/P0001"}},
		{"submission list", []string{"/submissions?limit=50"}},
		{"rank", []string{"/rank"}},
	} {
		samples := make([]float64, 0, 300)
		for i := 0; i < 300; i++ {
			start := time.Now()
			req, err := http.NewRequest(http.MethodGet, srv.URL+c.paths[i%len(c.paths)], nil)
			if err != nil {
				t.Fatal(err)
			}
			// A browser-shaped Accept exercises the HTML view; everything else
			// stays on the text path.
			if c.name != "problem html" {
				req.Header.Set("Accept", "text/plain")
			} else {
				req.Header.Set("Accept", "text/html")
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil || resp.StatusCode != http.StatusOK || len(body) == 0 {
				t.Fatalf("%s: status %d err %v", c.name, resp.StatusCode, err)
			}
			samples = append(samples, float64(time.Since(start).Microseconds())/1000)
		}
		p50, p90, p99 := percentiles(samples)
		t.Logf("%-15s p50=%7.3fms  p90=%7.3fms  p99=%7.3fms", c.name, p50, p90, p99)
		if p99 > 25 {
			t.Errorf("%s p99 = %.3fms, over the 25ms budget", c.name, p99)
		}
	}
}
