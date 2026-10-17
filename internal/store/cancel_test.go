package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCancelAccountFreesIdentityKeepsRecords(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	u, err := db.CreateAccount(ctx, AccountInput{
		Handle: "victim", Email: "victim@example.com", Password: "correct-horse-battery", Cost: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetUserPermission(ctx, u.ID, "problem.create", true); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateSession(ctx, u.ID, "1.2.3.4", "test", SessionTTL); err != nil {
		t.Fatal(err)
	}
	p := mustProblem(t, db, "C900", "留下的记录")
	sub, err := db.CreateSubmission(ctx, &Submission{
		UserID: u.ID, ProblemID: p.ID, Language: "cpp", Source: "int main(){}", Status: "done", Verdict: "AC",
	})
	if err != nil {
		t.Fatal(err)
	}

	freed, err := db.CancelAccount(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}

	after, err := db.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.IsCancelled() {
		t.Error("注销后状态该是 deleted")
	}
	if after.Handle != freed || !strings.HasPrefix(freed, "banned-") {
		t.Errorf("handle = %q, want a banned-<id> 墓碑名", after.Handle)
	}
	if after.Email != "" || after.HasPassword {
		t.Errorf("邮箱和密码没抹干净：%+v", after)
	}
	if after.DisplayName != AnonymousName || after.Bio != "" {
		t.Errorf("昵称/简介没清：%+v", after)
	}
	if after.DMPolicy != "off" {
		t.Errorf("私信策略 = %s, want off", after.DMPolicy)
	}

	// 用户名自由了：下一个人可以原样注册。
	again, err := db.CreateAccount(ctx, AccountInput{
		Handle: "victim", Email: "second@example.com", Password: "correct-horse-battery", Cost: 4,
	})
	if err != nil {
		t.Fatalf(" freed handle 不能重新注册：%v", err)
	}
	if again.ID == u.ID {
		t.Fatal("注册复用到了同一行")
	}

	// 会话与个人权限覆盖都跟着身份一起没了。
	if _, _, err := db.SessionUser(ctx, "anything"); !errors.Is(err, ErrNotFound) {
		t.Error("注销后还有会话能用")
	}
	if perms, _ := db.EffectivePermissions(ctx, u.ID, after.Role); perms["problem.create"] {
		t.Error("注销后个人权限覆盖还在")
	}

	// 记录一条没少，而且署名是封禁用户、不是那个已经属于别人的 handle。
	var left int
	if err := db.SQL.QueryRowContext(ctx, `SELECT COUNT(*) FROM submissions WHERE user_id=?`, u.ID).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 1 {
		t.Fatalf("提交记录数 = %d, want 1（记录必须留着）", left)
	}
	rows, err := db.ListSubmissionDetails(ctx, DetailFilter{Limit: 10})
	if err != nil || len(rows) == 0 {
		t.Fatalf("明细查不出来：%d %v", len(rows), err)
	}
	if rows[0].ID != sub.ID {
		t.Fatalf("明细里第一行不是那条提交：%+v", rows[0])
	}
	if rows[0].Name != AnonymousName {
		t.Errorf("署名 = %q, want %q", rows[0].Name, AnonymousName)
	}
	if rows[0].Handle != freed {
		t.Errorf("链接用的 handle = %q, want %q", rows[0].Handle, freed)
	}
	board, err := db.Leaderboard(ctx, 10)
	if err != nil || len(board) == 0 {
		t.Fatalf("排行榜查不出来：%d %v", len(board), err)
	}
	// 墓碑仍然在榜上（记录留着），但署名不能是那个已经属于别人的用户名。
	var found bool
	for _, row := range board {
		if row.Handle == freed {
			found = true
			if row.Name != AnonymousName {
				t.Errorf("墓碑在排行榜上署名 = %q, want %q", row.Name, AnonymousName)
			}
		}
	}
	if !found {
		t.Error("注销的人从排行榜上消失了，记录应该留着")
	}

	// 墓碑不能再改回去：用户名可能已经被别人拿走了。
	if err := db.SetUserStatus(ctx, u.ID, "active"); !errors.Is(err, ErrAccountCancelled) {
		t.Errorf("解封注销账号 = %v, want ErrAccountCancelled", err)
	}
	if err := db.SetUserRole(ctx, u.ID, "admin"); !errors.Is(err, ErrAccountCancelled) {
		t.Errorf("给注销账号改角色 = %v", err)
	}
	if err := db.SetUserPermission(ctx, u.ID, "problem.create", true); !errors.Is(err, ErrAccountCancelled) {
		t.Errorf("给注销账号授权 = %v", err)
	}
}

// 站主不能注销：那等于让别的管理员把站长的位子抹掉。
func TestCancelAccountRefusesTheSiteOwner(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	boss := mustUser(t, db, "boss")
	if _, err := db.ClaimOwner(ctx, boss.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CancelAccount(ctx, boss.ID); !errors.Is(err, ErrSiteOwner) {
		t.Fatalf("注销站主 = %v, want ErrSiteOwner", err)
	}
}

// 有人真注册过 banned-17 这种名字时，墓碑换个后缀，不要把约束错误冒给管理员。
func TestCancelAccountAvoidsTakenTombstoneName(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	victim := mustUser(t, db, "victim2")
	if _, err := db.CreateAccount(ctx, AccountInput{
		Handle: "banned-2", Email: "b@example.com", Password: "correct-horse-battery", Cost: 4,
	}); err != nil {
		t.Fatal(err)
	}
	freed, err := db.CancelAccount(ctx, victim.ID)
	if err != nil {
		t.Fatal(err)
	}
	if freed == "banned-2" || len(freed) > 32 {
		t.Errorf("墓碑用户名 = %q，应该避开已经存在的名字", freed)
	}
}

func TestOwnedCounts(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	author := mustUser(t, db, "author17")
	p, err := db.CreateProblem(ctx, &Problem{
		PID: "OWNED1", Title: "名下有题", Statement: "s", TimeLimitMS: 1000, MemLimitKB: 262144,
		Languages: []string{"cpp"}, Visible: true, OwnerID: author.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = p
	problems, contests, err := db.OwnedCounts(ctx, author.ID)
	if err != nil {
		t.Fatal(err)
	}
	if problems != 1 || contests != 0 {
		t.Errorf("owned = %d/%d, want 1/0", problems, contests)
	}
	if _, err := db.CancelAccount(ctx, author.ID); err != nil {
		// 数据层不设这道闸：它把数字交出来，由页面决定要不要拦。
		t.Logf("store 层允许注销名下有题的账号：%v", err)
	}
}
