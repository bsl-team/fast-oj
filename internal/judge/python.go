package judge

// Python 在这里是"解释型语言"这一类的第一份实现：判机不编译，直接把源码交给解释器，
// 交给之前先在同一个工作目录里落一份护栏（pyguard.py），由它装上审计钩子再跑学生的代码。
//
// 为什么护栏必须跟着这道闸门一起走：Python 的 `os.system("rm -rf ...")` 与 C++ 的
// `system("...")` 是同一种危险，而 C++ 那一侧的边界是牢房给的。护栏自己给不出边界——
// 它只是让越界这件事变得困难且看得见。所以**没开沙盒的判机不收 Python**：那台机器上
// 解释器和判机同一个身份、同一个文件系统，护栏拦不住一个决心足够的人。

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

//go:embed pyguard.py
var pyguardSource []byte

// PythonLanguage 是提交页与判机之间对这门语言的拼写。
const PythonLanguage = "python"

// guardFileName 是护栏落在工作目录里的名字。{dir} 会被展开成那次运行的目录，牢房里
// 那个目录就是 chroot 的根，所以牢房内看到的是 /pyguard.py。
const guardFileName = "pyguard.py"

// pythonInterpreter 挑解释器：Linux/容器里是 python3，Windows 上官方安装包叫 python。
func pythonInterpreter() string {
	if runtime.GOOS == "windows" {
		return "python"
	}
	return "python3"
}

// pythonTool 是 Python 的工单口径。没有 Compile：判机现编一份字节码交给解释器跑，
// 与直接跑源码在"谁来兜隔离"这件事上没有区别，只会多一个可能与解释器版本不符的 .pyc。
//
//	-I  忽略 PYTHONPATH 等环境变量（判机发出去的环境本来就只有一小撮，这是第二道）
//	-S  不跑 site：少一条把 site-packages 塞进 sys.path 的路，pip 之类也就更进不来
//	-B  不落 .pyc
//	-X utf8  学生打印中文时不跟着 LANG=C 一起变成乱码
var pythonTool = func() Tool {
	return Tool{
		Language:   PythonLanguage,
		SourceFile: "main.py",
		Run: []string{pythonInterpreter(), "-I", "-B", "-S", "-X", "utf8",
			"{dir}/" + guardFileName, "{src}"},
		// Support 是跑之前要顺便写进工作目录的文件——没有它，学生那份代码连解释器都
		// 进不去（护栏就是那一层）。
		Support: []SupportFile{{Name: guardFileName, Body: pyguardSource}},
	}
}()

// pythonUsable 报告这台机器上有没有解释器。Available() 用它，启动自测也用它。
func pythonUsable() bool {
	_, err := exec.LookPath(pythonInterpreter())
	return err == nil
}

// PythonProbe 是判机启动时对 Python 做的一次真跑：一次正常程序必须跑通，一次想逃逸的
// 必须被挡下来。两个条件缺一个就不 advertise Python——"页面说能交，判机判不了"与
// "页面说能交，判机却让它跑了"，后者更糟。
//
// 它必须在牢房里跑（Run 自己会开），因为这道闸门承诺的就是"有牢房才收 Python"。
func PythonProbe(dir string) (string, error) {
	if !pythonUsable() {
		return "", fmt.Errorf("这台机器上没有 %s，Python 提交判不了", pythonInterpreter())
	}
	if _, jailed := jailFor(Limits{TimeMS: 5000, MemKB: 65536}); !jailed {
		return "", fmt.Errorf("沙盒没开（%s）：Python 提交只能在牢房里跑，这台判机不收", SandboxNote())
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	// 自测的那份目录不留下来：里面躺着护栏与学生代码的样本，判完就该没有。
	defer os.RemoveAll(dir)
	tool := pythonTool
	if err := writeSupport(tool, dir); err != nil {
		return "", err
	}
	lim := Limits{TimeMS: 10_000, MemKB: 256 << 10, MaxProcs: 8}
	// 一次正常的：打印一行再加一次算术，同时确认 stdio 与 -I 的组合在这台机器上真能用。
	benign := `print("ok", sum(range(10)))`
	src := filepath.Join(dir, tool.SourceFile)
	if err := writeRunFile(src, []byte(benign)); err != nil {
		return "", err
	}
	ex := Run(tool.RunArgs(dir), dir, nil, lim)
	if ex.Exit != 0 || !strings.Contains(string(ex.Stdout), "ok 45") {
		return "", fmt.Errorf("Python 自测没跑通：退出码 %d，输出 %q，%s %s",
			ex.Exit, strings.TrimSpace(string(ex.Stdout)),
			strings.TrimSpace(string(ex.Stderr)), ex.Note)
	}
	// 一次想跑的：起子进程。护栏必须把它挡成一条 RuntimeError，而那句 echo 一个字都不能出现。
	hostile := `import os
print("CANARY-" + os.popen("echo LEAKED").read())`
	if err := writeRunFile(src, []byte(hostile)); err != nil {
		return "", err
	}
	blocked := Run(tool.RunArgs(dir), dir, nil, lim)
	if strings.Contains(string(blocked.Stdout), "LEAKED") || strings.Contains(string(blocked.Stdout), "CANARY") {
		return "", fmt.Errorf("Python 护栏没挡住 os.popen：stdout=%q", strings.TrimSpace(string(blocked.Stdout)))
	}
	if blocked.Exit == 0 {
		return "", fmt.Errorf("Python 护栏放跑了一次子进程调用却还正常退出：stdout=%q stderr=%q",
			strings.TrimSpace(string(blocked.Stdout)), strings.TrimSpace(string(blocked.Stderr)))
	}
	note := fmt.Sprintf("Python 可用（%s，护栏自测通过：子进程/网络/装包已被挡）", interpreterVersion())
	return note, nil
}

// interpreterVersion 只为那句话：操作员要能在 /judges 与提交详情上看到这台机器到底用的
// 哪个解释器，判出来的东西跟版本有关。
func interpreterVersion() string {
	out, err := exec.Command(pythonInterpreter(), "-c", "import sys;print('%d.%d'%sys.version_info[:2])").Output()
	if err != nil {
		return "版本未知"
	}
	return strings.TrimSpace(string(out))
}

// PythonVersion 是缓存下来的解释器版本：判机每份 Python 工单都要回报"实际用的口径"，
// 而为此每条提交去 fork 一次解释器不值得。
var PythonVersion = sync.OnceValue(interpreterVersion)

// writeSupport 把工具自带的文件落进这次运行的工作目录。
func writeSupport(tool Tool, dir string) error {
	for _, f := range tool.Support {
		if err := writeRunFile(filepath.Join(dir, f.Name), f.Body); err != nil {
			return fmt.Errorf("写 %s: %w", f.Name, err)
		}
	}
	return nil
}
