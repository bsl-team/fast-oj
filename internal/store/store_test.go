package store

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"testing"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func mustUser(t *testing.T, db *DB, handle string) User {
	t.Helper()
	u, err := db.EnsureUser(context.Background(), handle, "user")
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func mustProblem(t *testing.T, db *DB, pid, title string) Problem {
	t.Helper()
	p, err := db.CreateProblem(context.Background(), &Problem{
		PID: pid, Title: title, Statement: "read stdin, write stdout",
		TimeLimitMS: 1000, MemLimitKB: 262144, Languages: []string{"c", "cpp"}, Visible: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestOpenUsesWALAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oj.db")
	ctx := context.Background()

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	mode, err := db.JournalMode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
	mustProblem(t, db, "P100", "A+B")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopening must not re-apply or drop anything.
	db2, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if _, err := db2.FindProblem(ctx, "P100"); err != nil {
		t.Fatalf("data lost after reopen: %v", err)
	}
	var applied int
	if err := db2.SQL.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	// Compare against the embedded set so adding a migration never needs a test edit.
	files, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	if applied != len(files) {
		t.Fatalf("schema_migrations rows = %d, want %d", applied, len(files))
	}
}

func TestProblemLifecycle(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	p := mustProblem(t, db, "P100", "A+B Problem")
	if p.ID == 0 || p.CreatedAt == 0 {
		t.Fatalf("create did not fill id/timestamp: %+v", p)
	}

	if _, err := db.CreateProblem(ctx, &Problem{PID: "p100", Title: "case-insensitive pid", TimeLimitMS: 1000, MemLimitKB: 262144}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate pid err = %v, want ErrConflict", err)
	}

	byID, err := db.FindProblem(ctx, "1")
	if err != nil {
		t.Fatal(err)
	}
	byPID, err := db.FindProblem(ctx, "p100")
	if err != nil {
		t.Fatal(err)
	}
	if byID.ID != p.ID || byPID.ID != p.ID {
		t.Fatalf("lookup mismatch: id=%d pid=%d want %d", byID.ID, byPID.ID, p.ID)
	}

	if _, err := db.FindProblem(ctx, "P404"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing problem err = %v, want ErrNotFound", err)
	}

	p.Title = "A+B (updated)"
	p.MemLimitKB = 131072
	got, err := db.UpdateProblem(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != p.Title || got.TimeLimitMS != 1000 || len(got.Languages) != 2 {
		t.Fatalf("update round trip = %+v", got)
	}

	mustProblem(t, db, "P101", "Sum")
	mustProblem(t, db, "P102", "Difference")

	page, err := db.ListProblems(ctx, 0, 2, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.NextAfter != 2 || page.Total != 3 {
		t.Fatalf("first page = %+v", page)
	}
	second, err := db.ListProblems(ctx, page.NextAfter, 2, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.NextAfter != 0 {
		t.Fatalf("second page = %+v", second)
	}

	// A literal percent must not act as a wildcard.
	hits, err := db.ListProblems(ctx, 0, 50, "100", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits.Items) != 1 {
		t.Fatalf("pid search = %+v, want exactly 1", hits.Items)
	}
	esc, err := db.ListProblems(ctx, 0, 50, "%", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(esc.Items) != 0 {
		t.Fatalf("escaped search matched %d rows", len(esc.Items))
	}

	if err := db.DeleteProblem(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetProblemByID(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete err = %v", err)
	}
	if err := db.DeleteProblem(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double delete err = %v, want ErrNotFound", err)
	}
}

func TestTestcasesAreOrderedAndScored(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	p := mustProblem(t, db, "P1", "Ordering")

	for i, tc := range []Testcase{
		{ProblemID: p.ID, Name: "sample1", IsSample: true, InputRel: "1/sample1.in", OutputRel: "1/sample1.out"},
		{ProblemID: p.ID, Name: "case1", Score: 40, InputRel: "1/case1.in", OutputRel: "1/case1.out"},
		{ProblemID: p.ID, Name: "case2", Score: 60, InputRel: "1/case2.in", OutputRel: "1/case2.out"},
	} {
		want := i + 1
		created, err := db.AddTestcase(ctx, &tc)
		if err != nil {
			t.Fatal(err)
		}
		if created.Ordinal != want {
			t.Fatalf("ordinal = %d, want %d", created.Ordinal, want)
		}
	}

	all, err := db.ListTestcases(ctx, p.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("cases = %d, want 3", len(all))
	}
	samples, err := db.ListTestcases(ctx, p.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 1 || !samples[0].IsSample {
		t.Fatalf("samples = %+v", samples)
	}

	if total, err := db.ProblemScoreTotal(ctx, p.ID); err != nil || total != 100 {
		t.Fatalf("total score = %d, %v (samples must not count)", total, err)
	}

	if _, err := db.AddTestcase(ctx, &Testcase{ProblemID: p.ID, Name: "case1", InputRel: "a", OutputRel: "b"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate case name err = %v", err)
	}
	if n, err := db.DeleteTestcases(ctx, p.ID); err != nil || n != 3 {
		t.Fatalf("deleted = %d, %v", n, err)
	}
}

func TestUserHandlesAreCaseInsensitive(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	u := mustUser(t, db, "alice")
	again, err := db.EnsureUser(ctx, "Alice", "user")
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != u.ID {
		t.Fatalf("handle lookup is case sensitive: %d vs %d", again.ID, u.ID)
	}
	if _, err := db.EnsureUser(ctx, "bob", "root"); err == nil {
		t.Fatal("unknown role accepted")
	}

	users, err := db.ListUsers(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 {
		t.Fatalf("users = %+v", users)
	}
}

func TestSubmissionFlow(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	u := mustUser(t, db, "carol")
	p := mustProblem(t, db, "P1", "Flow")
	j, err := db.UpsertJudge(ctx, "box-1", []string{"cpp", "python3"}, nil, "local", nil)
	if err != nil {
		t.Fatal(err)
	}

	s, err := db.CreateSubmission(ctx, &Submission{
		UserID: u.ID, ProblemID: p.ID, Language: "cpp", Source: "int main(){}", TotalScore: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != "queued" || s.Verdict != "" {
		t.Fatalf("new submission = %+v", s)
	}
	if n, err := db.PendingCount(ctx); err != nil || n != 1 {
		t.Fatalf("pending = %d, %v", n, err)
	}

	src, detail, err := db.SubmissionPayload(ctx, s.ID)
	if err != nil || src != "int main(){}" || detail != "" {
		t.Fatalf("payload = %q/%q, %v", src, detail, err)
	}

	claimed, err := db.SetStatus(ctx, s.ID, "queued", "compiling", j.ID)
	if err != nil || !claimed {
		t.Fatalf("claim = %v, %v", claimed, err)
	}
	again, err := db.SetStatus(ctx, s.ID, "queued", "compiling", j.ID)
	if err != nil || again {
		t.Fatalf("double claim = %v, %v", again, err)
	}
	// Advancing from a status the row is not in must not touch it.
	if ok, err := db.SetStatus(ctx, s.ID, "queued", "running", 99); err != nil || ok {
		t.Fatalf("illegal transition accepted: %v %v", ok, err)
	}
	got, err := db.GetSubmission(ctx, s.ID)
	if err != nil || got.Status != "compiling" || got.JudgeID != j.ID || got.StartedAt == 0 {
		t.Fatalf("after claim = %+v, %v", got, err)
	}

	runs := []Run{
		{Ordinal: 1, CaseName: "case1", Verdict: "AC", TimeMS: 12, MemKB: 3072},
		{Ordinal: 2, CaseName: "case2", Verdict: "WA", TimeMS: 15, MemKB: 3100},
	}
	if err := db.RecordResult(ctx, s.ID, Result{
		Verdict: "WA", TimeMS: 15, MemKB: 3100, Score: 40, TotalScore: 100, FailedCase: "case2", Detail: "got 3 want 4",
	}, runs); err != nil {
		t.Fatal(err)
	}

	done, err := db.GetSubmission(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != "done" || done.Verdict != "WA" || done.Score != 40 || done.FailedCase != "case2" || done.JudgeID != j.ID {
		t.Fatalf("judged submission = %+v", done)
	}
	if done.StartedAt == 0 || done.JudgedAt == 0 {
		t.Fatalf("timestamps missing: %+v", done)
	}
	if n, err := db.PendingCount(ctx); err != nil || n != 0 {
		t.Fatalf("pending after finish = %d, %v", n, err)
	}

	gotRuns, err := db.ListRuns(ctx, s.ID)
	if err != nil || len(gotRuns) != 2 {
		t.Fatalf("runs = %+v, %v", gotRuns, err)
	}
	if gotRuns[0].Verdict != "AC" || gotRuns[1].Verdict != "WA" || gotRuns[0].Ordinal != 1 {
		t.Fatalf("run order/verdict = %+v", gotRuns)
	}

	list, err := db.ListSubmissions(ctx, SubmissionFilter{UserID: u.ID, ProblemID: p.ID})
	if err != nil || len(list) != 1 {
		t.Fatalf("list by user+problem = %+v, %v", list, err)
	}
	if list[0].Source != "" {
		t.Fatal("source leaked into listing")
	}
	if empty, err := db.ListSubmissions(ctx, SubmissionFilter{UserID: 999}); err != nil || len(empty) != 0 {
		t.Fatalf("unexpected rows %+v", empty)
	}

	if _, err := db.CreateSubmission(ctx, &Submission{UserID: 4242, ProblemID: p.ID, Language: "cpp"}); err == nil {
		t.Fatal("foreign key on user_id not enforced")
	}
}

func TestJudgeRegistry(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	j, err := db.UpsertJudge(ctx, "Box-1", []string{"cpp"}, nil, "first", nil)
	if err != nil {
		t.Fatal(err)
	}
	j2, err := db.UpsertJudge(ctx, "box-1", []string{"cpp", "go"}, []string{"options"}, "second", nil)
	if err != nil {
		t.Fatal(err)
	}
	if j2.ID != j.ID || j2.Note != "second" || len(j2.Languages) != 2 {
		t.Fatalf("upsert did not refresh: %+v vs %+v", j, j2)
	}
	if err := db.TouchJudge(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.TouchJudge(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("touch unknown judge = %v", err)
	}
	list, err := db.ListJudges(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("judges = %+v, %v", list, err)
	}
	got, err := db.GetJudge(ctx, "BOX-1")
	if err != nil || got.Name != "Box-1" {
		t.Fatalf("get judge = %+v, %v", got, err)
	}
}
