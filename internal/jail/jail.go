// Package jail 把一次判题运行关进 Linux 命名空间：看不到宿主文件系统、没有网络、
// 没有可写的地方（除了这次运行的工作目录），并由内核而不是判机来兜住 CPU、内存、
// 进程数与文件大小。
//
// 为什么要"exec 自己"：Go 不能在 fork 与 exec 之间插东西，所以 setrlimit、mount、
// chroot 这些必须在子进程里、exec 之前完成（internal/judge/runner_other.go 里那句
// "Go cannot hand a Linux child its rlimits at spawn" 就是这个坑）。于是判机把真正的
// 命令行交给自己的另一个入口，由那个子进程先把牢房钉好，再 exec 提交程序。
//
// 建不起来的牢房必须明说，不能假装存在：任何一步失败都以 ErrPrefix + FailExit 退出，
// 判机把它报成 IE 而不是 RE——学生不该为内核不给权限去改自己的代码。
package jail

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

const (
	// jailFlag 是隐藏入口：值里带着整份 Spec，不该出现在 -h，也不该有人手工敲。
	jailFlag = "--oj-jail"
	// FailExit 区分"沙盒没建起来"与"程序自己挂了"。
	FailExit = 70
	// ErrPrefix 是 jailer 失败时打在 stderr 上的前缀。
	ErrPrefix = "OJ-JAIL: "
)

// Mode 是操作者选的沙盒强度。Auto 是默认：能开就开，开不起来就在启动日志和 /judges
// 上写明"未隔离"，但不影响判题。On 是硬要求：开不起来就每条判题都报 IE。
type Mode int

const (
	Off Mode = iota
	Auto
	On
)

func (m Mode) String() string {
	switch m {
	case Off:
		return "off"
	case On:
		return "on"
	}
	return "auto"
}

// ParseMode 只认 off/auto/on：拼错的沙盒开关比没有开关更危险。
func ParseMode(raw string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "auto":
		return Auto, nil
	case "off", "none", "0":
		return Off, nil
	case "on", "1":
		return On, nil
	}
	return Off, fmt.Errorf("-sandbox 只认 auto/on/off，给了 %q", raw)
}

// Spec 是父进程交给 jailer 的全部内容，JSON 之后走命令行参数。
type Spec struct {
	// Dir 是这次运行的工作目录，也是牢房里唯一可写的地方。
	Dir string `json:"dir"`
	// Argv 是真正要跑的程序。
	Argv []string `json:"argv"`
	Env  []string `json:"env"`

	TimeMS int `json:"time_ms"`
	MemKB  int `json:"mem_kb"`
	Procs  int `json:"procs"`

	// UserNS 为真表示这个牢房靠非特权 user namespace 撑起来（判机不是 root）。
	// 两种形态最后都把身份换成一个在宿主上什么都不是的 uid。
	UserNS bool `json:"user_ns"`
	// UID/GID 是牢房内的身份。UserNS 下它是个未映射的号，宿主看到的是 nobody。
	UID uint32 `json:"uid"`
	GID uint32 `json:"gid"`

	// Binds 是牢房里要按原路径只读出现的系统目录：编译要工具链，跑要动态库。
	Binds []string `json:"binds"`
	// Dev 是牢房里要 bind 进去的设备文件（新 mount 命名空间里没有现成的 /dev）。
	Dev []string `json:"dev"`
}

// encode 把 Spec 变成 --oj-jail= 的参数值。
func (s Spec) encode() (string, error) {
	blob, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return jailFlag + "=" + string(blob), nil
}

// Wrap 把真正的命令行包成"跑这一个牢房"的命令行。self 是判机自己的可执行文件
// 路径（Linux 上用 /proc/self/exe，它不受后续 chroot 影响）。
func Wrap(self string, spec Spec) ([]string, error) {
	arg, err := spec.encode()
	if err != nil {
		return nil, err
	}
	return []string{self, arg}, nil
}

// IsJailer 报告这条进程命令行是不是 jailer 入口。
func IsJailer(args []string) bool {
	return len(args) > 1 && strings.HasPrefix(args[1], jailFlag+"=")
}

// Strip 把 jailer 参数从命令行里摘掉，留给 flag 解析的是人看的那几个。
func Strip(args []string) []string {
	out := make([]string, 0, len(args))
	for i, a := range args {
		if i == 1 && strings.HasPrefix(a, jailFlag+"=") {
			continue
		}
		out = append(out, a)
	}
	return out
}

// Main 是 cmd/fast-judge 在 main 最开头调的入口：如果自己是 jailer，就建牢房、
// exec 提交程序，永不返回；建不起来就带着 ErrPrefix 退出。
func Main() {
	if !IsJailer(os.Args) {
		return
	}
	spec, err := decodeSpec(os.Args[1])
	if err == nil {
		err = Enter(spec)
	}
	if err == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "%s%v\n", ErrPrefix, err)
	os.Exit(FailExit)
}

func decodeSpec(arg string) (Spec, error) {
	var s Spec
	raw, ok := strings.CutPrefix(arg, jailFlag+"=")
	if !ok {
		return s, errors.New("not a jail spec")
	}
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return s, fmt.Errorf("spec: %w", err)
	}
	if s.Dir == "" || len(s.Argv) == 0 {
		return s, errors.New("spec needs dir and argv")
	}
	return s, nil
}
