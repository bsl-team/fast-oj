package api

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// 提交页上那两根下拉得真能把版本带进库里：这一串以后是判机拼命令行的唯一依据，
// 也是页面上"C++14"这句话的出处。
func TestSubmitCarriesVersionOptions(t *testing.T) {
	h := newHarness(t)
	author := h.signIn("versub")
	h.readyProblem("P2100")

	cases := []struct {
		name, std, optimize, want string
	}{
		{"不碰下拉", "", "", ""},
		{"只换标准", "c++14", "", "std=c++14"},
		{"只关优化", "", "0", "O=0"},
		{"两个都选，顺序按判机的轴", "c++11", "0", "std=c++11,O=0"},
		// 选中的正是默认值也照样写下来：记录说的是当时用了什么，而不是"跟着以后
		// 的默认走"。默认口径哪天换了，老提交仍然知道自己用的是 C++17。
		{"显式选默认值", "c++17", "2", "std=c++17,O=2"},
	}
	for _, c := range cases {
		code, _, hdr := h.submitRaw("P2100", url.Values{
			"language": {"cpp"}, "source": {"int main(){}"},
			"std": {c.std}, "O": {c.optimize},
		}, author)
		if code != http.StatusSeeOther || !strings.HasPrefix(hdr.Get("Location"), "/submissions/") {
			t.Fatalf("%s: submit = %d %q", c.name, code, hdr.Get("Location"))
		}
		id := strings.TrimPrefix(hdr.Get("Location"), "/submissions/")
		sid, err := strconv.Atoi(id)
		if err != nil {
			t.Fatalf("%s：id %q 不是数字", c.name, id)
		}
		sub, err := h.db.GetSubmission(ctx(), int64(sid))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if sub.Options != c.want {
			t.Errorf("%s：库里的版本选项 = %q，要 %q", c.name, sub.Options, c.want)
		}
	}
}

// 选了这门语言没有的标准要当面拒绝，并说出能选哪些；不能悄悄换成另一个版本编——
// 那等于用一条谁都没选过的命令行判了这份代码，页面上却留着提交时选的那个。
func TestSubmitRefusesAVersionTheLanguageDoesNotHave(t *testing.T) {
	h := newHarness(t)
	author := h.signIn("verbad")
	h.readyProblem("P2101")

	before, err := h.db.PendingCount(ctx())
	if err != nil {
		t.Fatal(err)
	}
	// C 没有 c++14 这种写法。
	code, _, hdr := h.submitRaw("P2101", url.Values{
		"language": {"c"}, "source": {"int main(){}"}, "std": {"c++14"},
	}, author)
	if code != http.StatusSeeOther {
		t.Fatalf("拒绝应当把人送回题目页，got %d", code)
	}
	flash, _ := url.QueryUnescape(hdr.Get("Location"))
	if !strings.Contains(flash, "不支持") || !strings.Contains(flash, "c99") {
		t.Errorf("拒绝的话应当说出能选哪些：%q", flash)
	}
	after, err := h.db.PendingCount(ctx())
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Errorf("被拒的提交还是排进了队列：%d → %d", before, after)
	}

	// 表单里塞一个判机根本没有的轴，同样进不了命令行。
	if _, _, hdr := h.submitRaw("P2101", url.Values{
		"language": {"cpp"}, "source": {"int main(){}"}, "std": {"-o /dev/null"},
	}, author); !strings.Contains(hdr.Get("Location"), url.QueryEscape("不支持")) {
		t.Errorf("把参数塞进 argv 的写法没被挡住：%q", hdr.Get("Location"))
	}
}

// 详情页要说全称，列表行只在真与默认不同时才补一句：每行都挂"C++17 · O2 开"会把
// 选过版本的那几条埋掉。
func TestVersionLabelOnPages(t *testing.T) {
	h := newHarness(t)
	author := h.signIn("verview")
	h.readyProblem("P2102")

	plain := h.submitID(t, author, "P2102", url.Values{})
	chosen := h.submitID(t, author, "P2102", url.Values{"std": {"c++14"}, "O": {"0"}})

	// html/template 把 C++ 里的 + 转成 &#43;，浏览器里显示正常，所以断言先还原一次。
	code, body := h.getAs(author, "/submissions/"+plain)
	if code != http.StatusOK {
		t.Fatalf("默认那条 = %d", code)
	}
	body = unplus(body)
	if !strings.Contains(body, "C++17") || !strings.Contains(body, "O2 开") {
		t.Errorf("详情页没写出默认口径：\n%s", short(body))
	}
	if _, body := h.getAs(author, "/submissions/"+chosen); !strings.Contains(unplus(body), "C++14 · O2 关") {
		t.Errorf("详情页没写出选过的版本：\n%s", short(body))
	}

	code, feed := h.getAs(author, "/problems/P2102")
	if code != http.StatusOK {
		t.Fatalf("题目页 = %d", code)
	}
	feed = unplus(feed)
	if !strings.Contains(feed, "cpp · C++14") {
		t.Errorf("最近提交那格没补版本：\n%s", short(feed))
	}
	if strings.Contains(feed, "cpp · C++17") {
		t.Error("列表把没选版本的行也标成了默认口径，选过版本的反而看不出来")
	}
}

func unplus(body string) string { return strings.ReplaceAll(body, "&#43;", "+") }

// 提交页必须把候选值渲染出来，且第一个选项是"判机默认"：不选的时候判的是这台机器的
// 口径，页面不能替它假装选了什么。
func TestProblemPageRendersVersionSelectors(t *testing.T) {
	h := newHarness(t)
	author := h.signIn("verform")
	h.readyProblem("P2103")

	code, body := h.getAs(author, "/problems/P2103")
	if code != http.StatusOK {
		t.Fatalf("题目页 = %d", code)
	}
	body = unplus(body)
	for _, want := range []string{`name="std"`, `name="O"`, "判机默认", "C++17", "C++14", "C11", "C99", "O2 关", "（默认）"} {
		if !strings.Contains(body, want) {
			t.Errorf("提交页缺 %q：\n%s", want, short(body))
		}
	}
}

// submitID 提交一次并把新记录的 id 拿回来。
func (h *harness) submitID(t *testing.T, s session, pid string, extra url.Values) string {
	t.Helper()
	form := url.Values{"language": {"cpp"}, "source": {"int main(){}"}}
	for k, vs := range extra {
		form[k] = vs
	}
	code, body, hdr := h.submitRaw(pid, form, s)
	if code != http.StatusSeeOther || !strings.HasPrefix(hdr.Get("Location"), "/submissions/") {
		t.Fatalf("提交 = %d %q loc=%q", code, short(body), hdr.Get("Location"))
	}
	return strings.TrimPrefix(hdr.Get("Location"), "/submissions/")
}
