package store

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func queueFixture(t *testing.T) (*DB, User, Problem, Judge) {
	t.Helper()
	db := openTestDB(t)
	ctx := context.Background()
	u := mustUser(t, db, "alice")
	p := mustProblem(t, db, "P1", "Queue problem")
	j, err := db.UpsertJudge(ctx, "box-1", []string{"cpp"}, "")
	if err != nil {
		t.Fatal(err)
	}
	return db, u, p, j
}

func submitSomething(t *testing.T, db *DB, u User, p Problem, source string) Submission {
	t.Helper()
	s, err := db.CreateSubmission(context.Background(), &Submission{
		UserID: u.ID, ProblemID: p.ID, Language: "cpp", Source: source, TotalScore: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestClaimNextIsFifoAndStampsOwner(t *testing.T) {
	ctx := context.Background()
	db, u, p, j := queueFixture(t)
	j2, err := db.UpsertJudge(ctx, "box-2", nil, "")
	if err != nil {
		t.Fatal(err)
	}

	first := submitSomething(t, db, u, p, "int main(){}")
	second := submitSomething(t, db, u, p, "int main(){return 1;}")

	task, err := db.ClaimNext(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task.SubmissionID != first.ID || task.ProblemID != p.ID || task.PID != "P1" {
		t.Fatalf("first claim = %+v", task)
	}
	if task.Language != "cpp" || task.Source != "int main(){}" || task.TimeLimitMS != 1000 || task.MemLimitKB != 262144 {
		t.Fatalf("claim payload = %+v", task)
	}
	row, err := db.GetSubmission(ctx, first.ID)
	if err != nil || row.Status != "compiling" || row.JudgeID != j.ID || row.StartedAt == 0 {
		t.Fatalf("claimed row = %+v, %v", row, err)
	}

	// A second box takes the next row, not the one already owned.
	other, err := db.ClaimNext(ctx, j2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if other.SubmissionID != second.ID {
		t.Fatalf("second claim = %+v, want %d", other, second.ID)
	}

	if _, err := db.ClaimNext(ctx, j.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty queue err = %v, want ErrNotFound", err)
	}
}

func TestConcurrentClaimsNeverDuplicate(t *testing.T) {
	ctx := context.Background()
	db, u, p, _ := queueFixture(t)

	const jobs = 24
	for i := 0; i < jobs; i++ {
		submitSomething(t, db, u, p, "x")
	}
	boxes := make([]int64, 4)
	for i := range boxes {
		j, err := db.UpsertJudge(ctx, string(rune('a'+i))+":box", nil, "")
		if err != nil {
			t.Fatal(err)
		}
		boxes[i] = j.ID
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		seen = map[int64]int{}
		errs []error
	)
	for _, id := range boxes {
		wg.Add(1)
		go func(judgeID int64) {
			defer wg.Done()
			for {
				task, err := db.ClaimNext(ctx, judgeID)
				if errors.Is(err, ErrNotFound) {
					return
				}
				if err != nil {
					mu.Lock()
					errs = append(errs, err)
					mu.Unlock()
					return
				}
				mu.Lock()
				seen[task.SubmissionID]++
				mu.Unlock()
			}
		}(id)
	}
	wg.Wait()

	if len(errs) > 0 {
		t.Fatalf("claim errors: %v", errs)
	}
	if len(seen) != jobs {
		t.Fatalf("claimed %d distinct rows, queued %d", len(seen), jobs)
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("submission %d claimed %d times", id, n)
		}
	}
}

func TestReportStatusOnlyByOwnerAndLegalStep(t *testing.T) {
	ctx := context.Background()
	db, u, p, j := queueFixture(t)
	j2, err := db.UpsertJudge(ctx, "box-2", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	s := submitSomething(t, db, u, p, "x")
	if _, err := db.ClaimNext(ctx, j.ID); err != nil {
		t.Fatal(err)
	}

	// Another box may not touch it.
	if ok, err := db.ReportStatus(ctx, s.ID, j2.ID, "compiling", "running", ""); err != nil || ok {
		t.Fatalf("non-owner report = %v %v", ok, err)
	}
	// Wrong source state.
	if ok, err := db.ReportStatus(ctx, s.ID, j.ID, "queued", "running", ""); err != nil || ok {
		t.Fatalf("wrong from-state = %v %v", ok, err)
	}
	if ok, err := db.ReportStatus(ctx, s.ID, j.ID, "compiling", "running", "on case 3"); err != nil || !ok {
		t.Fatalf("owner report = %v %v", ok, err)
	}
	row, err := db.GetSubmission(ctx, s.ID)
	if err != nil || row.Status != "running" {
		t.Fatalf("row after progress = %+v %v", row, err)
	}
	// detail is deliberately absent from the default column set; it comes back
	// through the payload read.
	if _, detail, err := db.SubmissionPayload(ctx, s.ID); err != nil || detail != "on case 3" {
		t.Fatalf("detail = %q %v", detail, err)
	}
	// An empty detail must not erase what is already there.
	if ok, err := db.ReportStatus(ctx, s.ID, j.ID, "running", "running", ""); err != nil || !ok {
		t.Fatalf("self transition = %v %v", ok, err)
	}
	if _, detail, err := db.SubmissionPayload(ctx, s.ID); err != nil || detail != "on case 3" {
		t.Fatalf("detail was cleared: %q %v", detail, err)
	}
}

func TestReapStaleAndRequeue(t *testing.T) {
	ctx := context.Background()
	db, u, p, j := queueFixture(t)
	s := submitSomething(t, db, u, p, "x")
	if _, err := db.ClaimNext(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	// Simulate a judge box that died an hour ago.
	if _, err := db.SQL.ExecContext(ctx,
		`UPDATE submissions SET started_at=? WHERE id=?`, nowMS()-3600_000, s.ID); err != nil {
		t.Fatal(err)
	}

	n, err := db.ReapStale(ctx, 60_000)
	if err != nil || n != 1 {
		t.Fatalf("reaped = %d %v", n, err)
	}
	row, err := db.GetSubmission(ctx, s.ID)
	if err != nil || row.Status != "abandoned" || row.Verdict != "IE" {
		t.Fatalf("reaped row = %+v %v", row, err)
	}
	// A reaped row is out of the pending queue.
	if pending, err := db.PendingCount(ctx); err != nil || pending != 0 {
		t.Fatalf("pending after reap = %d %v", pending, err)
	}

	if ok, err := db.RequeueAbandoned(ctx, s.ID); err != nil || !ok {
		t.Fatalf("requeue = %v %v", ok, err)
	}
	row, err = db.GetSubmission(ctx, s.ID)
	if err != nil || row.Status != "queued" || row.Verdict != "" || row.JudgeID != 0 || row.StartedAt != 0 {
		t.Fatalf("requeued row = %+v %v", row, err)
	}
	// Requeueing a settled row must fail; only abandoned rows go back.
	if err := db.RecordResult(ctx, s.ID, Result{Verdict: "AC", TimeMS: 1, MemKB: 1, Score: 1, TotalScore: 1}, nil); err != nil {
		t.Fatal(err)
	}
	if ok, err := db.RequeueAbandoned(ctx, s.ID); err != nil || ok {
		t.Fatalf("done row requeued: %v %v", ok, err)
	}
}

func TestReapLeavesFreshWorkAlone(t *testing.T) {
	ctx := context.Background()
	db, u, p, j := queueFixture(t)
	s := submitSomething(t, db, u, p, "x")
	if _, err := db.ClaimNext(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	n, err := db.ReapStale(ctx, 60_000)
	if err != nil || n != 0 {
		t.Fatalf("fresh claim reaped: %d %v", n, err)
	}
	if row, _ := db.GetSubmission(ctx, s.ID); row.Status != "compiling" {
		t.Fatalf("row = %+v", row)
	}
}

// 提交选的版本要一路走到判机手里：这一列在写库、读列表、领取工单任何一环被丢掉，
// 页面上写的"C++14"就和实际编译用的东西不是一回事了。
func TestClaimNextCarriesVersionOptions(t *testing.T) {
	ctx := context.Background()
	db, u, p, j := queueFixture(t)

	chosen, err := db.CreateSubmission(ctx, &Submission{
		UserID: u.ID, ProblemID: p.ID, Language: "cpp", Options: "std=c++14,O=0",
		Source: "int main(){}", TotalScore: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := db.ClaimNext(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Options != "std=c++14,O=0" {
		t.Errorf("领到的工单带的版本选项 = %q，要 std=c++14,O=0", task.Options)
	}
	if row, err := db.GetSubmission(ctx, chosen.ID); err != nil || row.Options != "std=c++14,O=0" {
		t.Fatalf("写库后读回 = %+v, %v", row, err)
	}

	// 不选的老路：空串一路是空串，判定基准交给判机的默认。
	plain := submitSomething(t, db, u, p, "int main(){}")
	list, err := db.ListSubmissions(ctx, SubmissionFilter{UserID: u.ID})
	if err != nil || len(list) != 2 {
		t.Fatalf("列表 = %d 条, %v", len(list), err)
	}
	options := map[int64]string{}
	for _, s := range list {
		options[s.ID] = s.Options
	}
	if options[chosen.ID] != "std=c++14,O=0" {
		t.Errorf("列表里 #%d 的版本选项 = %q", chosen.ID, options[chosen.ID])
	}
	if options[plain.ID] != "" {
		t.Errorf("列表里 #%d 没选版本却带上了 %q", plain.ID, options[plain.ID])
	}
	box2, err := db.UpsertJudge(ctx, "box-2", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	next, err := db.ClaimNext(ctx, box2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if next.SubmissionID != plain.ID || next.Options != "" {
		t.Errorf("领取到的工单 = %+v，要那条没选版本的", next)
	}
}
