package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestJudgeProgramRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	p := mustProblem(t, db, "P700", "SPJ 题")

	// 没配判题器读不到就是 ErrNotFound，与其他按主键取的表一个口径；领工单那条路走的
	// 是 JudgePrograms（空 map），不必在这里造一个零值语义。
	if _, err := db.GetJudgeProgram(ctx, p.ID, ProgramChecker); !errors.Is(err, ErrNotFound) {
		t.Fatalf("没配判题器 = %v, want ErrNotFound", err)
	}
	if list, err := db.JudgePrograms(ctx, p.ID); err != nil || len(list) != 0 {
		t.Fatalf("没配任何程序时 = %+v, %v，要空表", list, err)
	}

	if err := db.PutJudgeProgram(ctx, JudgeProgram{
		ProblemID: p.ID, Kind: ProgramChecker, Language: "CPP",
		Source: "#include <cstdio>\nint main(){return 0;}",
	}); err != nil {
		t.Fatal(err)
	}
	one, err := db.GetJudgeProgram(ctx, p.ID, ProgramChecker)
	if err != nil {
		t.Fatal(err)
	}
	// 语言写成小写：判机那边的工具表按小写键查，作者写 CPP 不该让他撞"没有这套工具链"。
	if one.Language != "cpp" || !strings.Contains(one.Source, "cstdio") {
		t.Errorf("读回 = %+v", one)
	}
	// 再写一次是换内容，不是多一条记录。
	if err := db.PutJudgeProgram(ctx, JudgeProgram{
		ProblemID: p.ID, Kind: ProgramChecker, Language: "cpp", Source: "int main(){return 1;}"}); err != nil {
		t.Fatal(err)
	}
	again, err := db.GetJudgeProgram(ctx, p.ID, ProgramChecker)
	if err != nil || !strings.Contains(again.Source, "return 1") {
		t.Fatalf("覆盖没生效：%+v %v", again, err)
	}

	// 两种程序各占一行，互不干扰。
	if err := db.PutJudgeProgram(ctx, JudgeProgram{
		ProblemID: p.ID, Kind: ProgramInteractor, Language: "cpp", Source: "int main(){return 0;}"}); err != nil {
		t.Fatal(err)
	}
	all, err := db.JudgePrograms(ctx, p.ID)
	if err != nil || len(all) != 2 {
		t.Fatalf("按题目取全部 = %+v, %v，要两条", all, err)
	}
	if all[ProgramChecker].Kind != ProgramChecker || all[ProgramInteractor].Kind != ProgramInteractor {
		t.Errorf("种类错位：%+v", all)
	}

	// 空源码等于撤掉这一类：出题页用一个表单同时表达"改"和"不用了"。
	if err := db.PutJudgeProgram(ctx, JudgeProgram{
		ProblemID: p.ID, Kind: ProgramChecker, Language: "cpp", Source: "   "}); err != nil {
		t.Fatal(err)
	}
	after, err := db.JudgePrograms(ctx, p.ID)
	if err != nil || len(after) != 1 || after[ProgramInteractor].Source == "" {
		t.Errorf("撤掉判题器后 = %+v, %v，要只剩交互器", after, err)
	}

	// 题删了，程序跟着删：判题器源码不属于任何别人。
	if err := db.DeleteProblem(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	rows, err := db.SQL.ExecContext(ctx, `SELECT COUNT(*) FROM judge_programs`)
	if err != nil {
		t.Fatal(err)
	}
	_ = rows
	var n int
	if err := db.SQL.QueryRowContext(ctx, `SELECT COUNT(*) FROM judge_programs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("题删完还剩 %d 份判机程序，外键的 CASCADE 没生效", n)
	}
}

func TestJudgeProgramValidatesAtTheDoor(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	p := mustProblem(t, db, "P701", "校验")

	// 种类走白名单，而不是让 SQLite 的 CHECK 文本冒到页面上。
	if err := db.PutJudgeProgram(ctx, JudgeProgram{
		ProblemID: p.ID, Kind: "compiler", Language: "cpp", Source: "x"}); err == nil {
		t.Error("没被认识的种类竟然写进去了")
	}
	if err := db.PutJudgeProgram(ctx, JudgeProgram{
		ProblemID: p.ID, Kind: ProgramChecker, Language: "cpp",
		Source: strings.Repeat("a", MaxProgramSource+1)}); err == nil {
		t.Error("超过上限的源码没被拦")
	}
	// 默认语言：表单里没填也不能写出一个空语言，判机那边查不到工具链。
	if err := db.PutJudgeProgram(ctx, JudgeProgram{
		ProblemID: p.ID, Kind: ProgramChecker, Source: "int main(){}"}); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetJudgeProgram(ctx, p.ID, ProgramChecker)
	if err != nil || got.Language != "cpp" {
		t.Errorf("没填语言 = %+v, %v，要落到 cpp", got, err)
	}
}

// 判题器是"别人写的代码在判机上跑"，所以它是一项要单独发的权限，不能跟着 problem.edit
// 顺手流给每个被委托改题面的人。
func TestJudgeProgramPermissionIsStaffOnly(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	boss, err := db.EnsureUser(ctx, "spjboss", "admin")
	if err != nil {
		t.Fatal(err)
	}

	list, err := db.ListPermissions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var found *Permission
	for i := range list {
		if list[i].Key == "judge_program.edit" {
			found = &list[i]
		}
	}
	if found == nil {
		t.Fatal("0022 没把判题器权限建进 permissions")
	}
	if !found.Staff {
		t.Error("判题器权限必须是管理侧权限：它等于把在判机上执行代码的权力发出去")
	}
	perms, err := db.EffectivePermissions(ctx, boss.ID, boss.Role)
	if err != nil {
		t.Fatal(err)
	}
	if !perms["judge_program.edit"] {
		t.Error("管理员默认就该有这一项，否则升级完没人能配 SPJ")
	}
}
