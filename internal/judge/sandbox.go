package judge

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/liangbinchen2013/fast-oj/internal/jail"
)

// jailFailure 认出 jailer 打出来的失败声明：牢房建不起来时必须报 IE，既不能判成 RE
// 让学生去查自己没跑起来的代码，也不能悄悄按不隔离的老路子重跑一遍。
func jailFailure(stderr []byte) (string, bool) {
	for _, line := range bytes.Split(stderr, []byte("\n")) {
		if note, ok := bytes.CutPrefix(line, []byte(jail.ErrPrefix)); ok {
			return "沙盒未能建立: " + string(bytes.TrimSpace(note)), true
		}
	}
	return "", false
}

// sandbox 是判机启动时定下来的一次性配置，不是每条工单一个值：牢房能不能开、
// 以谁的身份开，取决于这台机器的内核与判机的 uid，跟具体哪道题无关。
var sandbox struct {
	mode   jail.Mode
	self   string // 判机自己的可执行文件路径，牢房靠 exec 它实现
	userNS bool
	uid    uint32
	gid    uint32
	// Available 是启动自测的结论，用来写日志和 /judges 上那行说明。
	available bool
	note      string
}

// SetSandbox 打开（或关掉）判题沙盒。self 是判机可执行文件路径；userNS 为真表示
// 判机不是 root，牢房要靠非特权 user namespace 撑起来。
func SetSandbox(self string, mode jail.Mode, userNS bool, uid, gid uint32) {
	sandbox.mode, sandbox.self, sandbox.userNS = mode, self, userNS
	sandbox.uid, sandbox.gid = uid, gid
}

// SandboxMode 与 SandboxAvailable 给状态展示用。
func SandboxMode() jail.Mode { return sandbox.mode }

func SandboxAvailable() bool { return sandbox.available }

func SandboxNote() string { return sandbox.note }

// reportSandbox 记下自测结论。开不起来时 -sandbox=auto 只是标注，-sandbox=on 会让
// 每条判题都报 IE，两者都不该悄悄当成"没事"。
func reportSandbox(ok bool, note string) {
	sandbox.available, sandbox.note = ok, note
}

// jailFor 决定这一次运行要不要关进牢房，以及牢房长什么样。
func jailFor(lim Limits) (jail.Spec, bool) {
	if sandbox.mode == jail.Off || sandbox.self == "" {
		return jail.Spec{}, false
	}
	procs := lim.MaxProcs
	if procs <= 0 {
		procs = 64 // 正常提交用不到几十个进程；fork 炸弹用不到第 65 个
	}
	return jail.Spec{
		TimeMS: lim.TimeMS, MemKB: lim.MemKB, Procs: procs,
		UserNS: sandbox.userNS, UID: sandbox.uid, GID: sandbox.gid,
		Binds: jail.Binds(), Dev: jail.Dev(),
	}, true
}

// ProbeSandbox 在启动时真真切切地建一次牢房跑一个 /bin/true，把结论记下来。
// 等第一条判题失败再说"这台开不了沙盒"是不诚实的：操作者应该在日志里第一眼看到它。
func ProbeSandbox(dir string) (string, error) {
	if sandbox.mode == jail.Off {
		reportSandbox(false, "沙盒关闭")
		return "沙盒关闭", nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	ex := Run([]string{"/bin/true"}, dir, nil, Limits{TimeMS: 5000, MemKB: 64 << 10})
	note := ""
	switch {
	case strings.HasPrefix(ex.Note, "沙盒未能建立"):
		reportSandbox(false, ex.Note)
		return ex.Note, errors.New(ex.Note)
	case ex.Exit != 0:
		note = fmt.Sprintf("沙盒自测失败：退出码 %d %s", ex.Exit, strings.TrimSpace(string(ex.Stderr)))
		reportSandbox(false, note)
		return note, errors.New(note)
	}
	note = fmt.Sprintf("沙盒已启用（%s，提交以 uid %d 运行）", sandboxKind(), sandbox.uid)
	reportSandbox(true, note)
	return note, nil
}

func sandboxKind() string {
	if sandbox.userNS {
		return "非特权命名空间 + chroot"
	}
	return "命名空间 + chroot"
}
