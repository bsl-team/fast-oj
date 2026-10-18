package judge

import (
	"strings"
	"testing"
)

// 默认口径必须还是把 -std=c++17 与 -O2 写死的那一条命令行：把版本拆成选项不能顺手
// 改掉所有历史提交的判定基准，否则"加了个下拉框"会变成"整站重判一遍"。
func TestToolDefaultsKeepTheOldCommandLine(t *testing.T) {
	tools := DefaultTools()
	want := []string{"g++", "-x", "c++", "-std=c++17", "-O2", "-w", "-static", "-o", "{exe}", "{src}"}
	got := tools["cpp"].Compile
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("cpp 默认命令行 = %v，要 %v", got, want)
	}
	if _, label, err := tools["cpp"].WithOptions(""); err != nil {
		t.Fatal(err)
	} else if label != "C++17 · O2 开" {
		t.Errorf("默认的人类说法 = %q", label)
	}
	cDefault := strings.Join(tools["c"].Compile, " ")
	if !strings.Contains(cDefault, "-std=c11") || !strings.Contains(cDefault, "-O2") {
		t.Errorf("c 默认命令行 = %q，要带 -std=c11 与 -O2", cDefault)
	}
}

func TestWithOptionsSplicesIntoTheCommandLine(t *testing.T) {
	tool := DefaultTools()["cpp"]
	cases := []struct {
		raw       string
		wantArgs  []string
		wantLabel string
	}{
		{"std=c++14", []string{"-std=c++14", "-O2"}, "C++14 · O2 开"},
		{"O=0", []string{"-std=c++17", "-O0"}, "C++17 · O2 关"},
		{"std=c++11,O=0", []string{"-std=c++11", "-O0"}, "C++11 · O2 关"},
		{"", []string{"-std=c++17", "-O2"}, "C++17 · O2 开"},
		// 重复给同一根轴：后面那个为准，而不是悄悄拼出两个 -std 让编译器去猜。
		{"std=c++11,std=c++20", []string{"-std=c++20"}, "C++20 · O2 开"},
	}
	for _, c := range cases {
		got, label, err := tool.WithOptions(c.raw)
		if err != nil {
			t.Fatalf("%q: %v", c.raw, err)
		}
		line := strings.Join(got.Compile, " ")
		for _, want := range c.wantArgs {
			if !strings.Contains(line, want) {
				t.Errorf("%q 展开成 %q，缺 %s", c.raw, line, want)
			}
		}
		if strings.Contains(line, "{opts}") {
			t.Errorf("%q 展开后还留着占位符：%q", c.raw, line)
		}
		if label != c.wantLabel {
			t.Errorf("%q 的说法 = %q，要 %q", c.raw, label, c.wantLabel)
		}
	}
	// 关掉 O2 时命令行上不能有 -O2。
	off, _, err := tool.WithOptions("O=0")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(off.Compile, " "), "-O2") {
		t.Errorf("O=0 之后还有 -O2：%v", off.Compile)
	}
}

// 只有候选表里的值能进命令行：这一条既是"不注入"的保证，也是判机对着一份来路不明的
// 工单该说的话。
func TestWithOptionsRefusesAnythingOffTheList(t *testing.T) {
	tool := DefaultTools()["cpp"]
	for _, raw := range []string{
		"std=c++98",              // 没这个标准
		"O=3",                    // 只给开关，不给第三个优化级别
		"foo=1",                  // 没有这根轴
		"std",                    // 缺了 =
		"std=-Wall -o /dev/null", // 想把参数塞进 argv
		"std=$(rm -rf /)",        // 想走 shell
		"std=c++14,std=",         // 空值
		"interp=3.11",            // cpp 没有解释器轴
		"std=c++20,O=2,unused=x",
	} {
		if _, _, err := tool.WithOptions(raw); err == nil {
			t.Errorf("%q 竟然被接受了", raw)
		}
	}
	if _, _, err := tool.WithOptions("std=c++98"); err != nil &&
		!strings.Contains(err.Error(), "只认") {
		t.Errorf("拒绝时应当报出这台机器认哪些值：%v", err)
	}
}

// 没有版本轴的语言收到选项就是错，不能当成"忽略就好了"——用户以为选了 c++11，
// 实际编出来的却是别的东西。
func TestToolsWithoutAxesRejectOptions(t *testing.T) {
	plain := Tool{Language: "txt", SourceFile: "main.txt", Run: []string{"cat", "{src}"}}
	if _, _, err := plain.WithOptions(""); err != nil {
		t.Errorf("空选项不该报错：%v", err)
	}
	if _, _, err := plain.WithOptions("std=c++17"); err == nil {
		t.Error("txt 没有版本轴，收到 std 应当报错")
	}
	// 有轴却没有占位符：选项无处可插，这比报错更值得警惕，所以也必须报。
	broken := Tool{Language: "cpp", SourceFile: "main.cpp",
		Compile: []string{"g++", "{src}"}, Base: []string{"g++", "{src}"}, Axes: cxxAxes()}
	if _, _, err := broken.WithOptions("std=c++14"); err == nil {
		t.Error("没有 {opts} 占位符时应当报错")
	}
}

const genericLambda = `#include <iostream>
int main(){ auto f=[](auto x){return x*2;}; std::cout<<f(21)<<std::endl; }`

// genericLambda 对任何输入都输出 42：这一条只关心"编不编得过"，用例给一个就够。
func oneCase() []Case {
	return []Case{{Name: "s", Ordinal: 1, IsSample: true, Input: []byte("1 2\n"), Expected: []byte("42\n")}}
}

// 选了版本就得真按版本编：泛型 lambda 是 C++14 才有的东西，c++11 必须编不过，
// 否则下拉框只是个装饰。
func TestSelectedStandardActuallyChangesTheBuild(t *testing.T) {
	tool := requireToolchain(t)
	for _, c := range []struct {
		raw  string
		want Verdict
	}{
		{"std=c++11", CE},
		{"std=c++14", AC},
		{"", AC},
	} {
		picked, _, err := tool.WithOptions(c.raw)
		if err != nil {
			t.Fatalf("%q: %v", c.raw, err)
		}
		rep := judgeSource(t, picked, genericLambda, oneCase(), 2000, 262144)
		if rep.Verdict != c.want {
			t.Errorf("选 %q 编出来是 %s（%s），要 %s；命令行 %v",
				c.raw, rep.Verdict, rep.Detail, c.want, picked.Compile)
		}
	}
}

// -O2 关掉之后仍然要能判对：优化开关换的是命令行，不是判定口径。
func TestOptimizeOffStillJudgesTheSame(t *testing.T) {
	tool := requireToolchain(t)
	picked, label, err := tool.WithOptions("O=0")
	if err != nil {
		t.Fatal(err)
	}
	rep := judgeSource(t, picked, sumSource, sumCases(), 2000, 262144)
	if rep.Verdict != AC {
		t.Fatalf("O=0（%s）= %s（%s），要 AC", label, rep.Verdict, rep.Detail)
	}
	if !strings.Contains(strings.Join(picked.CompileArgs("work"), " "), "-O0") {
		t.Errorf("命令行里没有 -O0：%v", picked.Compile)
	}
}
