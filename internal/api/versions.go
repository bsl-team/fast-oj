package api

// 版本选择（C++ 标准、要不要 -O2、以后 Python 用哪个解释器）的词汇表只有一份，就是
// 判机那边的工具表 internal/judge。这里照抄它来渲染下拉框、写库前规范化，判机拿到工单
// 后再按自己那一份校验一次。
//
// 必须用不带 PATH 过滤的 judge.DefaultTools：站点可能跑在一台没有 g++ 的机器上而判机在
// 另一台。按本机有没有编译器来筛下拉框，会把"能选哪些版本"变成"这个 web 进程装了什么"，
// 那是两件事。真正决定编不编得动的是判机，它命中不了自己的候选表就直接拒绝。

import (
	"net/url"
	"strings"

	"github.com/liangbinchen2013/fast-oj/internal/judge"
)

var versionTools = judge.DefaultTools()

func versionTool(lang string) (judge.Tool, bool) {
	t, ok := versionTools[strings.ToLower(strings.TrimSpace(lang))]
	return t, ok
}

// versionAxis 是提交页上一根下拉框的数据。
type versionAxis struct {
	Name    string
	Label   string
	Options []versionChoice
}

type versionChoice struct {
	Value, Label string
	// Default 标出判机自己的默认口径，页面用它写"默认（C++17）"这种提示。
	Default bool
}

// versionAxesFor 把这道题允许的语言的版本轴合成提交页要渲染的选项。
//
// 值是编译器写法（c++14 与 c11 互不撞名），所以两种语言可以共用一根"标准"下拉框：
// 选了 C 又挑 C++14 会被服务端按判机的话说"不支持"并列出能选的值，而不是编出一个
// 谁都没选的命令行。
func versionAxesFor(langs []string) []versionAxis {
	if len(langs) == 0 {
		langs = defaultLanguages
	}
	var order []string
	merged := map[string]*versionAxis{}
	add := func(axis judge.OptionAxis) {
		taken := merged[axis.Name]
		if taken == nil {
			taken = &versionAxis{Name: axis.Name, Label: axis.Label}
			merged[axis.Name] = taken
			order = append(order, axis.Name)
		}
		for _, o := range axis.Options {
			// 同一根轴上同名值只留一份：C 与 C++ 的"c11"写法不同，天然不会撞。
			dup := false
			for _, has := range taken.Options {
				if has.Value == o.Value {
					dup = true
				}
			}
			if !dup {
				taken.Options = append(taken.Options, versionChoice{
					Value: o.Value, Label: o.Label, Default: o.Default,
				})
			}
		}
	}
	for _, lang := range langs {
		if tool, ok := versionTool(lang); ok {
			for _, axis := range tool.Axes {
				add(axis)
			}
		}
	}
	out := make([]versionAxis, 0, len(order))
	for _, name := range order {
		out = append(out, *merged[name])
	}
	return out
}

// submitVersion 把提交表单里的版本下拉写成库里那串规范形式。表单里没选（空值）的轴
// 留空，意思是"用这台判机的默认口径"。
func submitVersion(tool judge.Tool, form url.Values) (string, error) {
	parts := make([]string, 0, len(tool.Axes))
	for _, axis := range tool.Axes {
		if v := strings.TrimSpace(form.Get(axis.Name)); v != "" {
			parts = append(parts, axis.Name+"="+v)
		}
	}
	return tool.Normalize(strings.Join(parts, ","))
}

// versionLabel 是给判题详情看的全称："C++17 · O2 开"。老数据（空串）也有一句实话：
// 它用的就是判机当时的默认，而默认正是把版本拆成选项之前硬编码的那一条命令行。
func versionLabel(lang, options string) string {
	tool, ok := versionTool(lang)
	if !ok {
		return ""
	}
	chosen, err := tool.Resolved(options)
	if err != nil {
		// 词表换过而记录还在：把库里那句话原样显示出来，总比编一个可能错的说法好。
		return options
	}
	parts := make([]string, 0, len(chosen))
	for _, o := range chosen {
		if o.Label != "" {
			parts = append(parts, o.Label)
		}
	}
	return strings.Join(parts, " · ")
}

// versionNote 只列与判机默认不同的部分，给最近提交那种一行一条的列表用：列表是拿来
// 扫的，"cpp" 后面跟一长串默认值只会让真正选过版本的那几条看不出来。
func versionNote(lang, options string) string {
	tool, ok := versionTool(lang)
	if !ok {
		return ""
	}
	chosen, err := tool.Resolved(options)
	if err != nil {
		return ""
	}
	var parts []string
	for i, o := range chosen {
		if o.Value != tool.Axes[i].Default().Value {
			parts = append(parts, o.Label)
		}
	}
	return strings.Join(parts, " · ")
}
