package api

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/liangbinchen2013/fast-oj/internal/store"
)

const sampleChecker = `#include <cstdio>
int main(int argc, char **argv){ return argc > 2 ? 0 : 1; }`

// 判题器是一项单独的权力：能改用例、能改题面，都不等于能把一段自己写的代码送上判机。
func TestJudgeProgramNeedsItsOwnPermission(t *testing.T) {
	h := newHarness(t)
	setter := h.author("setter2001", permTestcaseManage)
	h.readyProblem("P2200")

	code, body := h.postAs(setter, "/problems/P2200/judge_program", url.Values{
		"kind": {"checker"}, "language": {"cpp"}, "source": {sampleChecker},
	})
	if code != http.StatusForbidden {
		t.Fatalf("只有 testcase.manage 的人存判题器 = %d，要 403：\n%s", code, short(body))
	}
	if !strings.Contains(body, permJudgeProgram) {
		t.Errorf("拒绝时没说要哪一项权力：%s", short(body))
	}
	if _, err := h.db.GetJudgeProgram(ctx(), mustProblemID(t, h, "P2200"), store.ProgramChecker); err == nil {
		t.Error("被 403 挡掉了却还是写进了库")
	}

	// 看得见现在用哪种判定，只是不能改：把整块藏起来会让他以为这题不支持 SPJ。
	if code, body := h.getAs(setter, "/problems/P2200/cases"); code != http.StatusOK ||
		!strings.Contains(body, "判题方式") || !strings.Contains(body, "不能改") {
		t.Errorf("只读说明没到位 = %d：\n%s", code, short(body))
	}
}

func TestJudgeProgramSaveAndRemove(t *testing.T) {
	h := newHarness(t)
	keeper := h.author("keeper2002", permTestcaseManage, permJudgeProgram)
	h.readyProblem("P2201")
	pid := mustProblemID(t, h, "P2201")

	if code, body := h.postAs(keeper, "/problems/P2201/judge_program", url.Values{
		"kind": {"checker"}, "language": {"cpp"}, "source": {sampleChecker},
	}); code != http.StatusOK {
		t.Fatalf("存判题器 = %d：\n%s", code, short(body))
	}
	stored, err := h.db.GetJudgeProgram(ctx(), pid, store.ProgramChecker)
	if err != nil || !strings.Contains(stored.Source, "argv") {
		t.Fatalf("库里没读到判题器：%+v %v", stored, err)
	}
	_, body := h.getAs(keeper, "/problems/P2201/cases")
	if !strings.Contains(body, "SPJ 判题器") || !strings.Contains(body, "选手输出文件") {
		t.Errorf("用例页没说清这题现在由判题器判定，或没讲约定：\n%s", short(body))
	}

	// 留空保存 = 撤掉，回到逐字节比较。
	if code, _ := h.postAs(keeper, "/problems/P2201/judge_program", url.Values{
		"kind": {"checker"}, "language": {"cpp"}, "source": {"  "},
	}); code != http.StatusOK {
		t.Fatalf("撤掉判题器 = %d", code)
	}
	if _, err := h.db.GetJudgeProgram(ctx(), pid, store.ProgramChecker); err == nil {
		t.Error("源码留空保存却还留着一条判题器记录")
	}
	if _, body := h.getAs(keeper, "/problems/P2201/cases"); !strings.Contains(body, "宽松比较") {
		t.Errorf("撤掉之后用例页应当回到逐字节比较的说法：\n%s", short(body))
	}
}

// 判题器只能用判机真认识的语言写：允许写 pas 就是允许存一份永远编不出来的源码，
// 然后让下一个学生吃一条判机故障。
func TestJudgeProgramLanguageMustBeAJudgeToolchain(t *testing.T) {
	h := newHarness(t)
	keeper := h.author("keeper2003", permTestcaseManage, permJudgeProgram)
	h.readyProblem("P2202")
	pid := mustProblemID(t, h, "P2202")

	if code, body := h.postAs(keeper, "/problems/P2202/judge_program", url.Values{
		"kind": {"checker"}, "language": {"pas"}, "source": {"x"},
	}); code != http.StatusOK || !strings.Contains(body, "判机认识的语言") {
		t.Fatalf("不存在的语言 = %d：\n%s", code, short(body))
	}
	if _, err := h.db.GetJudgeProgram(ctx(), pid, store.ProgramChecker); err == nil {
		t.Error("被拒的语言还是写进了库")
	}

	// 空字节与学生提交同样的理由被拒。
	if _, body := h.postAs(keeper, "/problems/P2202/judge_program", url.Values{
		"kind": {"checker"}, "language": {"cpp"}, "source": {"int main(){ }\x00"},
	}); !strings.Contains(body, "空字节") {
		t.Errorf("空字节没被点名：\n%s", short(body))
	}
	// 认不出的种类也不许写。
	if _, body := h.postAs(keeper, "/problems/P2202/judge_program", url.Values{
		"kind": {"compiler"}, "language": {"cpp"}, "source": {"x"},
	}); !strings.Contains(body, "种类") {
		t.Errorf("不认识的种类没被点名：\n%s", short(body))
	}
}

// 判题器要跟着工单到判机手上，否则判机只能按逐字节比较——那正好是页面上写着"本题用
// SPJ 判定"的那道谎。
func TestJudgeProgramTravelsWithTheTask(t *testing.T) {
	h := newHarness(t)
	keeper := h.author("keeper2004", permTestcaseManage, permJudgeProgram)
	student := h.signIn("stu2004")
	h.readyProblem("P2203")
	if code, body := h.postAs(keeper, "/problems/P2203/judge_program", url.Values{
		"kind": {"checker"}, "language": {"cpp"}, "source": {sampleChecker},
	}); code != http.StatusOK {
		t.Fatalf("存判题器 = %d：\n%s", code, short(body))
	}
	if code, _ := h.submitCode(student, "P2203", "cpp", "int main(){}"); code != http.StatusOK {
		t.Fatalf("提交 = %d", code)
	}
	task := h.claimTask(t, "box-2203")
	if task.Checker == nil || !strings.Contains(task.Checker.Source, "argv") ||
		task.Checker.Language != "cpp" {
		t.Fatalf("工单没把判题器带到判机：%+v", task)
	}

	// 没配判题器的题目不该拖着一份空源码过网络。
	h.readyProblem("P2204")
	if code, _ := h.submitCode(student, "P2204", "cpp", "int main(){}"); code != http.StatusOK {
		t.Fatalf("提交 = %d", code)
	}
	if task := h.claimTask(t, "box-2204"); task.Checker != nil {
		t.Errorf("普通题也带了判题器：%+v", task.Checker)
	}
}

func mustProblemID(t *testing.T, h *harness, pid string) int64 {
	t.Helper()
	p, err := h.db.FindProblem(ctx(), pid)
	if err != nil {
		t.Fatalf("找不到 %s：%v", pid, err)
	}
	return p.ID
}
