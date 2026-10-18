package api

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/liangbinchen2013/fast-oj/internal/store"
)

// Python 是站点收的语言，不是判机自己决定的事：题面页要能选到它。
func TestPythonIsOfferedOnTheProblemPage(t *testing.T) {
	h := newHarness(t)
	student := h.signIn("stu2100")
	if code, body := h.createProblem(url.Values{
		"pid": {"P2100"}, "title": {"会说 Python"}, "time_ms": {"1000"}, "mem_mb": {"256"},
		"languages": {"c", "cpp", "python"}, "visible": {"1"},
	}); code != http.StatusOK {
		t.Fatalf("建题 = %d：\n%s", code, short(body))
	}
	h.uploadCases("P2100")
	_, page := h.getAs(student, "/problems/P2100")
	if !strings.Contains(page, `value="python"`) {
		t.Errorf("提交页没有 Python 这个选项：\n%s", short(page))
	}
	if code, _ := h.submitCode(student, "P2100", "python", "print(3)"); code != http.StatusOK {
		t.Fatalf("交 Python = %d", code)
	}
}

// 内存下限这条不是格式检查，是"判定量的到底是什么"：解释器空跑就要 ~9MB 常驻，
// 题面只给 8MB 时每条 Python 提交都会 MLE——那是在判解释器，不是在判学生。
func TestPythonRefusedWhenMemoryLimitIsTooSmall(t *testing.T) {
	h := newHarness(t)
	student := h.signIn("stu2101")
	for _, c := range []struct {
		pid    string
		memMB  string
		wantOK bool
	}{
		{"P2101", "8", false},
		{"P2102", "128", true},
	} {
		if code, body := h.createProblem(url.Values{
			"pid": {c.pid}, "title": {"内存各异的题"}, "time_ms": {"1000"}, "mem_mb": {c.memMB},
			"languages": {"python"}, "visible": {"1"},
		}); code != http.StatusOK {
			t.Fatalf("建题 %s = %d：\n%s", c.pid, code, short(body))
		}
		h.uploadCases(c.pid)
		code, page := h.submitCode(student, c.pid, "python", "print(3)")
		if code != http.StatusOK {
			t.Fatalf("提交 = %d：\n%s", code, short(page))
		}
		p, err := h.db.FindProblem(ctx(), c.pid)
		if err != nil {
			t.Fatal(err)
		}
		subs, err := h.db.ListSubmissions(ctx(), store.SubmissionFilter{ProblemID: p.ID, Limit: 5})
		if err != nil {
			t.Fatal(err)
		}
		if c.wantOK && len(subs) != 1 {
			t.Fatalf("%s 的 Python 提交应当收下来，却只有 %d 条", c.pid, len(subs))
		}
		if !c.wantOK {
			if len(subs) != 0 {
				t.Fatalf("%s 内存只有 %sMB，却还是写下了提交", c.pid, c.memMB)
			}
			if !strings.Contains(page, "解释器") || !strings.Contains(page, "内存") {
				t.Errorf("%s 被拒却没说清原因（解释器自己要占内存）：\n%s", c.pid, short(page))
			}
		}
	}
}
