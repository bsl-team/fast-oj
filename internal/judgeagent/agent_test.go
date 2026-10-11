package judgeagent

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/liangbinchen2013/fast-oj/internal/api"
	"github.com/liangbinchen2013/fast-oj/internal/casefs"
	"github.com/liangbinchen2013/fast-oj/internal/config"
	"github.com/liangbinchen2013/fast-oj/internal/judge"
	"github.com/liangbinchen2013/fast-oj/internal/store"
)

const (
	sumSource    = "#include <iostream>\nint main(){int a,b;std::cin>>a>>b;std::cout<<a+b<<std::endl;return 0;}"
	offByOne     = "#include <iostream>\nint main(){int a,b;std::cin>>a>>b;std::cout<<a+b+1<<std::endl;return 0;}"
	brokenSource = "int main(){ this is not c++ }"
)

type env struct {
	srv   *httptest.Server
	db    *store.DB
	cfg   config.Config
	agent *Agent
}

func newEnv(t *testing.T) *env {
	t.Helper()
	if _, err := exec.LookPath("g++"); err != nil {
		t.Skip("g++ not on PATH")
	}
	dir := t.TempDir()
	cfg := config.Config{
		Addr: "127.0.0.1:0", DataDir: dir, DBPath: filepath.Join(dir, "oj.db"),
		AdminToken: "admin-secret", JudgeToken: "judge-secret",
		MaxSource: 1 << 20, MaxUpload: 1 << 20, MaxCase: 1 << 20,
	}
	db, err := store.Open(context.Background(), cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	handler := api.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), db).Handler()
	srv := httptest.NewServer(handler)
	agent, err := New(Config{
		Server: srv.URL, Token: cfg.JudgeToken, Name: "e2e-box",
		Dir: filepath.Join(t.TempDir(), "work"), Logf: t.Logf,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		srv.Close()
		db.Close()
	})
	return &env{srv: srv, db: db, cfg: cfg, agent: agent}
}

// seedProblem writes a sample plus two judged cases to disk and returns a
// queued submission carrying the candidate source.
func (e *env) seedProblem(t *testing.T, source, language string) store.Submission {
	t.Helper()
	ctx := context.Background()
	user, err := e.db.EnsureUser(ctx, "alice", "user")
	if err != nil {
		t.Fatal(err)
	}
	prob, err := e.db.CreateProblem(ctx, &store.Problem{
		PID: "P1", Title: "sum", Statement: "read a b, print a+b",
		TimeLimitMS: 2000, MemLimitKB: 262144, Languages: []string{"cpp"}, Visible: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := casefs.New(e.cfg.DataDir)
	specs := []struct {
		name    string
		in, out string
		sample  bool
		score   int
	}{
		{"sample1", "1 2\n", "3\n", true, 0},
		{"1", "3 4\n", "7\n", false, 40},
		{"2", "100 200\n", "300\n", false, 60},
	}
	for i, s := range specs {
		relIn, relOut, err := cases.Put(prob.ID, i+1, []byte(s.in), []byte(s.out))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.db.AddTestcase(ctx, &store.Testcase{
			ProblemID: prob.ID, Ordinal: i + 1, Name: s.name, IsSample: s.sample,
			Score: s.score, InputRel: relIn, OutputRel: relOut,
		}); err != nil {
			t.Fatal(err)
		}
	}
	total, err := e.db.ProblemScoreTotal(ctx, prob.ID)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := e.db.CreateSubmission(ctx, &store.Submission{
		UserID: user.ID, ProblemID: prob.ID, Language: language, Source: source, TotalScore: total,
	})
	if err != nil {
		t.Fatal(err)
	}
	return sub
}

func (e *env) process(t *testing.T) (bool, error) {
	t.Helper()
	return e.agent.ProcessOnce(context.Background())
}

func TestEndToEndAccepted(t *testing.T) {
	e := newEnv(t)
	sub := e.seedProblem(t, sumSource, "cpp")

	worked, err := e.process(t)
	if err != nil || !worked {
		t.Fatalf("process = %v %v", worked, err)
	}

	row, err := e.db.GetSubmission(context.Background(), sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != "done" || row.Verdict != "AC" {
		t.Fatalf("row = %+v", row)
	}
	if row.TotalScore != 100 || row.Score != 100 {
		t.Fatalf("score = %d/%d, want 100/100", row.Score, row.TotalScore)
	}
	if row.TimeMS < 0 {
		t.Fatalf("cpu time was not measured: %+v", row)
	}
	if row.MemKB <= 0 {
		t.Fatalf("memory was not measured: %d KB", row.MemKB)
	}
	if row.StartedAt == 0 || row.JudgedAt == 0 || row.JudgeID == 0 {
		t.Fatalf("timestamps or owner missing: %+v", row)
	}
	runs, err := e.db.ListRuns(context.Background(), sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 3 {
		t.Fatalf("runs = %+v", runs)
	}
	for _, r := range runs {
		if r.Verdict != "AC" {
			t.Fatalf("run %s = %s", r.CaseName, r.Verdict)
		}
	}
	if !runs[0].IsSample || runs[1].IsSample {
		t.Fatalf("sample flags lost over the wire: %+v", runs)
	}
	// The judge box registered itself through the claim heartbeat.
	j, err := e.db.GetJudge(context.Background(), "e2e-box")
	if err != nil || j.LastSeenAt == 0 {
		t.Fatalf("box not registered: %+v %v", j, err)
	}
	// Work directories are per submission and removed afterwards.
	if entries, err := filepath.Glob(filepath.Join(e.agent.cfg.Dir, "*")); err != nil || len(entries) != 0 {
		t.Fatalf("work dir left behind: %v %v", entries, err)
	}
}

func TestEndToEndWrongAnswer(t *testing.T) {
	e := newEnv(t)
	sub := e.seedProblem(t, offByOne, "cpp")
	if _, err := e.process(t); err != nil {
		t.Fatal(err)
	}
	row, err := e.db.GetSubmission(context.Background(), sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The server names the first failing judged case; a sample mismatch alone
	// does not become the reported case.
	if row.Verdict != "WA" || row.Score != 0 || row.FailedCase != "1" {
		t.Fatalf("row = %+v", row)
	}
	// The failing case is named and the first difference is quoted back.
	if _, detail, err := e.db.SubmissionPayload(context.Background(), sub.ID); err != nil || detail == "" {
		t.Fatalf("WA carried no diff detail: %q %v", detail, err)
	}
}

func TestEndToEndCompileError(t *testing.T) {
	e := newEnv(t)
	sub := e.seedProblem(t, brokenSource, "cpp")
	if _, err := e.process(t); err != nil {
		t.Fatal(err)
	}
	row, err := e.db.GetSubmission(context.Background(), sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.Verdict != "CE" || row.Score != 0 || row.FailedCase != "compile" {
		t.Fatalf("row = %+v", row)
	}
	runs, err := e.db.ListRuns(context.Background(), sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Fatalf("a build failure recorded case runs: %+v", runs)
	}
	if _, detail, err := e.db.SubmissionPayload(context.Background(), sub.ID); err != nil || detail == "" {
		t.Fatalf("compiler message was dropped: %q %v", detail, err)
	}
}

func TestEndToEndUnknownLanguageIsInfrastructureError(t *testing.T) {
	e := newEnv(t)
	sub := e.seedProblem(t, sumSource, "pascal")
	worked, err := e.process(t)
	if err != nil || !worked {
		t.Fatalf("process = %v %v", worked, err)
	}
	row, err := e.db.GetSubmission(context.Background(), sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The box cannot run it, so it must say so instead of leaving it claimed.
	if row.Status != "done" || row.Verdict != "IE" {
		t.Fatalf("row = %+v", row)
	}
	_, detail, err := e.db.SubmissionPayload(context.Background(), sub.ID)
	if err != nil || detail == "" || row.Score != 0 {
		t.Fatalf("IE detail missing: %q %v row=%+v", detail, err, row)
	}
}

func TestEmptyQueueIsNotAnError(t *testing.T) {
	e := newEnv(t)
	worked, err := e.process(t)
	if worked || err != nil {
		t.Fatalf("empty queue = %v %v, want false nil", worked, err)
	}
}

func TestNewValidatesConfig(t *testing.T) {
	base := Config{Server: "http://x", Token: "t", Name: "n"}
	if _, err := New(Config{Token: "t", Name: "n"}); err == nil {
		t.Error("missing server accepted")
	}
	if _, err := New(Config{Server: "http://x", Name: "n"}); err == nil {
		t.Error("missing token accepted")
	}
	if _, err := New(Config{Server: "http://x", Token: "t"}); err == nil {
		t.Error("missing name accepted")
	}
	// A box that advertises nothing it can run is refused up front.
	if _, err := New(Config{Server: base.Server, Token: base.Token, Name: base.Name,
		Tools: map[string]judge.Tool{}}); err == nil {
		t.Error("a box with no usable toolchain was accepted")
	}
}
