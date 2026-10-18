package judge

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Option 是版本轴上的一个候选值。只有 Args 会被拼进命令行：提交带上来的值必须命中
// 这张表，所以用户写的任何文本都进不了 argv——这既是不注入的保证，也是"这台机器只
// 认这几个版本"的唯一说法。
type Option struct {
	Value string
	// Label 是给提交页和判题详情看的人类说法，比如"C++17"、"O2 开"。
	Label string
	Args  []string
	// Default 标记判机在这根轴上的默认选择。老提交和不选的人走的就是它，所以
	// "没选"和"这台机器的口径"必须是同一件事，而不是交给编译器自己的心情。
	Default bool
}

// OptionAxis 是一门语言上可以被提交选择的一根轴：C++ 标准、要不要优化、哪个解释器。
type OptionAxis struct {
	Name    string
	Label   string
	Options []Option
}

func (a OptionAxis) find(v string) (Option, bool) {
	for _, o := range a.Options {
		if o.Value == v {
			return o, true
		}
	}
	return Option{}, false
}

// Default 是判机在这根轴上的默认口径。
func (a OptionAxis) Default() Option {
	for _, o := range a.Options {
		if o.Default {
			return o
		}
	}
	return Option{}
}

// Tool describes how to build and run one language.
type Tool struct {
	Language   string
	SourceFile string
	Compile    []string
	Run        []string
	// Base 是带 "{opts}" 占位符的编译命令行骨架，Axes 是版本轴。Compile 是
	// WithOptions("") 的结果，也就是默认版本已经展开的那一份。
	Base []string
	Axes []OptionAxis
	// Support 是开跑之前要顺便写进工作目录的文件：解释型语言的护栏就是这一份。
	// 它跟着工单现落，不是判机上事先摆好的东西——换一道题就换一份，跑完即删。
	Support []SupportFile
}

// SupportFile 是一份由判机写进工作目录、供 Run 引用的文件。
type SupportFile struct {
	Name string
	Body []byte
}

// expand substitutes {src}, {exe} and {dir} in an argv template.
func (t Tool) expand(template []string, dir string) []string {
	if len(template) == 0 {
		return nil
	}
	src := filepath.Join(dir, t.SourceFile)
	exe := filepath.Join(dir, "program")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	out := make([]string, len(template))
	for i, arg := range template {
		out[i] = strings.NewReplacer("{src}", src, "{exe}", exe, "{dir}", dir).Replace(arg)
	}
	return out
}

func (t Tool) CompileArgs(dir string) []string { return t.expand(t.Compile, dir) }
func (t Tool) RunArgs(dir string) []string     { return t.expand(t.Run, dir) }
func (t Tool) Compiles() bool                  { return len(t.Compile) > 0 }

// Normalize 把提交表单带来的版本选择写成规范串：轴按判机的顺序排，同一根轴重复给的
// 以最后一个为准，没给的轴留空（也就是这台判机的默认）。
//
// 库里存的是这个结果而不是表单原文，因为这一串以后会被拼进命令行——它必须只由判机
// 候选表里的原话组成。命中不了表就报错，不降级成默认：用另一条命令行判了这份代码，
// 却把提交时选的版本留在页面上，是不能接受的。
func (t Tool) Normalize(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if len(t.Axes) == 0 {
		return "", fmt.Errorf("%s 没有可选的版本，却收到了 %q", t.Language, raw)
	}
	given := map[string]string{}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		name, value, ok := strings.Cut(pair, "=")
		if !ok {
			return "", fmt.Errorf("版本选项 %q 缺了 =", pair)
		}
		axis, err := t.axis(name)
		if err != nil {
			return "", err
		}
		if _, ok := axis.find(value); !ok {
			return "", fmt.Errorf("%s 的 %s 不支持 %q（这台机器只认：%s）",
				t.Language, axis.Label, value, axisValues(axis))
		}
		given[name] = value
	}
	parts := make([]string, 0, len(given))
	for _, a := range t.Axes {
		if v, ok := given[a.Name]; ok {
			parts = append(parts, a.Name+"="+v)
		}
	}
	return strings.Join(parts, ","), nil
}

// axis 按名字找一根轴；找不到就是把表单塞了别的东西进来。
func (t Tool) axis(name string) (OptionAxis, error) {
	for _, a := range t.Axes {
		if a.Name == name {
			return a, nil
		}
	}
	return OptionAxis{}, fmt.Errorf("%s 没有 %q 这个版本选项", t.Language, name)
}

// Resolved 报出这次选择里每根轴实际生效的候选值，顺序与 Axes 一致：没选的轴就是
// 这台判机的默认。页面要写"C++14 · O2 关"或者"只有哪几根轴被人改过"，都从这里来，
// 于是显示的话和拼进命令行的话不会是两套。
func (t Tool) Resolved(raw string) ([]Option, error) {
	raw = strings.TrimSpace(raw)
	if len(t.Axes) == 0 {
		if raw != "" {
			return nil, fmt.Errorf("%s 没有可选的版本，却收到了 %q", t.Language, raw)
		}
		return nil, nil
	}
	chosen := make([]Option, len(t.Axes))
	for i, axis := range t.Axes {
		chosen[i] = axis.Default()
	}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		name, value, ok := strings.Cut(pair, "=")
		if !ok {
			return nil, fmt.Errorf("版本选项 %q 缺了 =", pair)
		}
		axis, err := t.axis(name)
		if err != nil {
			return nil, err
		}
		opt, ok := axis.find(value)
		if !ok {
			return nil, fmt.Errorf("%s 的 %s 不支持 %q（这台机器只认：%s）",
				t.Language, axis.Label, value, axisValues(axis))
		}
		for i := range t.Axes {
			if t.Axes[i].Name == axis.Name {
				chosen[i] = opt
			}
		}
	}
	return chosen, nil
}

// WithOptions 按提交带上来的版本选择重拼一次命令行。raw 形如 "std=c++17,O=0"，
// 空串就是这台判机的默认口径。
//
// 返回值里第二个人类说法（"C++17 · O2 开"）是给判题详情用的：同一段代码用
// c++11 编出来和用 c++20 编出来不是一回事，出了争议要能看出当时用的是哪个。
func (t Tool) WithOptions(raw string) (Tool, string, error) {
	chosen, err := t.Resolved(raw)
	if err != nil {
		return t, "", err
	}
	if len(t.Axes) > 0 {
		// 版本轴必须真有一个地方落进命令行，否则"选了 c++11"和"没选"会是同一次编译，
		// 那比报错更糟。
		if !strings.Contains(strings.Join(t.Base, " "), "{opts}") &&
			!strings.Contains(strings.Join(t.Run, " "), "{opts}") {
			return t, "", fmt.Errorf("%s 的工具没有 {opts} 占位符，版本选项无处可插", t.Language)
		}
	}

	args := make([]string, 0, len(chosen)*2)
	labels := make([]string, 0, len(chosen))
	for _, o := range chosen {
		if o.Label != "" {
			labels = append(labels, o.Label)
		}
		args = append(args, o.Args...)
	}
	out := t
	out.Compile = splice(t.Base, args)
	if len(t.Run) > 0 {
		out.Run = splice(t.Run, args)
	}
	return out, strings.Join(labels, " · "), nil
}

// splice 把版本轴参数插进 "{opts}" 所在的位置，让最终命令行读起来是正常的
// "g++ -x c++ -std=c++17 -O2 -w -static -o program main.cpp"。
func splice(base, opts []string) []string {
	out := make([]string, 0, len(base)+len(opts))
	for _, arg := range base {
		if arg == "{opts}" {
			out = append(out, opts...)
			continue
		}
		out = append(out, arg)
	}
	return out
}

func axisValues(axis OptionAxis) string {
	parts := make([]string, 0, len(axis.Options))
	for _, o := range axis.Options {
		parts = append(parts, o.Value)
	}
	return strings.Join(parts, "/")
}

// cxxAxes 是 C++ 的两根轴：标准与要不要 -O2。默认是 c++17 加 O2，也就是这份代码
// 之前硬编码的那一套——把优化改成默认关，等于把所有历史提交的用时重判一遍。
func cxxAxes() []OptionAxis {
	return []OptionAxis{
		{Name: "std", Label: "标准", Options: []Option{
			{Value: "c++11", Label: "C++11", Args: []string{"-std=c++11"}},
			{Value: "c++14", Label: "C++14", Args: []string{"-std=c++14"}},
			{Value: "c++17", Label: "C++17", Args: []string{"-std=c++17"}, Default: true},
			{Value: "c++20", Label: "C++20", Args: []string{"-std=c++20"}},
		}},
		{Name: "O", Label: "优化", Options: []Option{
			// -O0 写出来而不是省掉：命令行里看得见"这次没优化"，比靠编译器默认好。
			{Value: "0", Label: "O2 关", Args: []string{"-O0"}},
			{Value: "2", Label: "O2 开", Args: []string{"-O2"}, Default: true},
		}},
	}
}

func cAxes() []OptionAxis {
	return []OptionAxis{
		{Name: "std", Label: "标准", Options: []Option{
			{Value: "c99", Label: "C99", Args: []string{"-std=c99"}},
			{Value: "c11", Label: "C11", Args: []string{"-std=c11"}, Default: true},
			{Value: "c17", Label: "C17", Args: []string{"-std=c17"}},
		}},
		{Name: "O", Label: "优化", Options: []Option{
			{Value: "0", Label: "O2 关", Args: []string{"-O0"}},
			{Value: "2", Label: "O2 开", Args: []string{"-O2"}, Default: true},
		}},
	}
}

// DefaultTools covers the languages the judge knows out of the box. Templates
// are argv lists, never shell strings, so nothing here is interpolated into a
// shell command line.
func DefaultTools() map[string]Tool {
	cxxBase := []string{"g++", "-x", "c++", "{opts}", "-w", "-static", "-o", "{exe}", "{src}"}
	cBase := []string{"gcc", "-x", "c", "{opts}", "-w", "-static", "-o", "{exe}", "{src}"}
	cxx := Tool{
		Language: "cpp", SourceFile: "main.cpp", Base: cxxBase, Run: []string{"{exe}"}, Axes: cxxAxes(),
	}
	c := Tool{
		Language: "c", SourceFile: "main.c", Base: cBase, Run: []string{"{exe}"}, Axes: cAxes(),
	}
	// Compile 存的是"默认版本已经展开"的那一份：不选、老数据、以及没接选项的调用方
	// 拿到的都必须是这台机器的口径，不能是一份带占位符的骨架。
	cxx.Compile = splice(cxxBase, defaultArgs(cxx.Axes))
	c.Compile = splice(cBase, defaultArgs(c.Axes))
	return map[string]Tool{"cpp": cxx, "c": c, PythonLanguage: pythonTool}
}

func defaultArgs(axes []OptionAxis) []string {
	var out []string
	for _, axis := range axes {
		out = append(out, axis.Default().Args...)
	}
	return out
}

// Available drops tools this box cannot actually use: a compiler that is not on
// PATH, and an interpreter that is not on PATH either.
//
// 解释型那一支以前是"无条件通过"的，那等于在没装 Python 的机器上也把 Python 报给
// 站点——页面说能交，判机却只能回一条判机故障。
func Available(tools map[string]Tool) map[string]Tool {
	out := map[string]Tool{}
	for name, t := range tools {
		bin := ""
		if t.Compiles() {
			bin = t.Compile[0]
		} else if len(t.Run) > 0 {
			bin = t.Run[0]
		}
		if bin == "" {
			continue
		}
		if _, err := exec.LookPath(bin); err == nil {
			out[name] = t
		}
	}
	return out
}
