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

// sandboxProbe 是自测结论的三态。必须有"还没测过"这一档：自测本身就是**通过一次带
// 牢房的运行**来完成的，如果只有开了/关了两种，启动时默认"关"就会让自测永远不去建
// 牢房，于是永远"测得过"。
type sandboxProbe int8

const (
	probeUnknown     sandboxProbe = iota // 还没自测：该测，所以照计划关牢房
	probeOK                              // 自测过了，牢房建得起来
	probeUnavailable                     // 自测过了，这台机器开不了
)

// sandbox 是判机启动时定下来的一次性配置，不是每条工单一个值：牢房能不能开、
// 以谁的身份开，取决于这台机器的内核与判机的 uid，跟具体哪道题无关。
var sandbox struct {
	mode   jail.Mode
	self   string // 判机自己的可执行文件路径，牢房靠 exec 它实现
	userNS bool
	uid    uint32
	gid    uint32
	// probed 是启动自测的结论；note 用来写日志和 /judges 上那行说明。
	probed sandboxProbe
	note   string
}

// SetSandbox 打开（或关掉）判题沙盒。self 是判机可执行文件路径；userNS 为真表示
// 判机不是 root，牢房要靠非特权 user namespace 撑起来。
//
// 换配置必须同时把上一次自测的结论丢掉：probed 回答的是"按**当前**这套配置，这台机器
// 开不开得了牢房"。模式、jailer 路径、身份任一项变了，旧结论就不再成立——留着它，
// 下一份配置会继承上一条命的判决（实测就是这样让一次自测悄悄跳过牢房，然后报"已启用"）。
func SetSandbox(self string, mode jail.Mode, userNS bool, uid, gid uint32) {
	sandbox.mode, sandbox.self, sandbox.userNS = mode, self, userNS
	sandbox.uid, sandbox.gid = uid, gid
	sandbox.probed, sandbox.note = probeUnknown, ""
}

// SandboxMode 与 SandboxAvailable 给状态展示用。
func SandboxMode() jail.Mode { return sandbox.mode }

func SandboxAvailable() bool { return sandbox.probed == probeOK }

func SandboxNote() string { return sandbox.note }

// Jailed 回答"这台判机现在会把一次运行关进牢房吗"。收 Python 之前要先问它：护栏
// （internal/judge/pyguard.py）拦的是"离开这次运行的目录"，而那个边界是 chroot 给的；
// 没有牢房的判机上，解释器和判机是同一个身份、同一个文件系统。
func Jailed() bool {
	_, ok := jailFor(Limits{})
	return ok
}

// reportSandbox 记下自测结论。
//
// -sandbox=on 时开不起来根本不启动（setupSandbox 直接报错退出），所以这里记下的
// "开不了"只属于 auto：那条路从此不再尝试牢房，判机继续干活，并把"未隔离"这句话
// 留在日志、/judges 和每次判题的 note 里。悄悄少一层防护与悄悄判死每一条，都不是答案。
func reportSandbox(ok bool, note string) {
	if ok {
		sandbox.probed = probeOK
	} else {
		sandbox.probed = probeUnavailable
	}
	sandbox.note = note
}

// jailFor 决定这一次运行要不要关进牢房，以及牢房长什么样。
//
// 自测已经判定"这台开不了"之后就不再试：-sandbox=auto 的语义是"能开就开"，不是
// "开不了就让每条提交撞上建不起来的牢房"。后者正是 ECS 上撞到的那件事——判机一边在
// 日志里写着"沙盒未启用"，一边把每条工单都判成 IE。
func jailFor(lim Limits) (jail.Spec, bool) {
	if sandbox.mode == jail.Off || sandbox.self == "" || sandbox.probed == probeUnavailable {
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

// probeFailure 把一次自测运行翻译成"这台机器开不了牢房"的那句话。
//
// IE 就是失败，即使退出码是 0：unshare 被内核拒掉时 exec 根本没有发生，Go 报的是
// fork/exec 那一步的 errno，而进程压根没存在过——按退出码判成败会骗人。ECS 上就是
// 判机一边在日志里写"沙盒已启用"，一边把每条提交都判成 IE。
func probeFailure(ex Execution) (string, bool) {
	switch {
	case ex.Verdict == IE:
		note := ex.Note
		if !strings.HasPrefix(note, "沙盒未能") {
			note = "沙盒未能建立: " + note
		}
		return note, true
	case ex.Exit != 0:
		return fmt.Sprintf("沙盒自测失败：退出码 %d %s", ex.Exit, strings.TrimSpace(string(ex.Stderr))), true
	}
	return "", false
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
	lim := Limits{TimeMS: 5000, MemKB: 64 << 10}
	// 自测必须在牢房里跑，否则它测的是别的东西。jailFor 在这里返回 false 只有两种可能：
	// self 是空的（mode 不是 off 却连 jailer 都没有），或者上一份配置的结论还留着。
	// 两种都不能让"/bin/true 在牢房外跑通了"冒充"沙盒已启用"。
	if _, jailed := jailFor(lim); !jailed {
		note := "沙盒未能建立: 配置要求建牢房，可 jailFor 拒绝了一次——自测没法在牢房里跑"
		reportSandbox(false, note)
		return note, errors.New(note)
	}
	ex := Run([]string{"/bin/true"}, dir, nil, lim)
	if note, failed := probeFailure(ex); failed {
		reportSandbox(false, note)
		return note, errors.New(note)
	}
	note := fmt.Sprintf("沙盒已启用（%s，提交以 uid %d 运行）", sandboxKind(), sandbox.uid)
	reportSandbox(true, note)
	return note, nil
}

func sandboxKind() string {
	if sandbox.userNS {
		return "非特权命名空间 + chroot"
	}
	return "命名空间 + chroot"
}
