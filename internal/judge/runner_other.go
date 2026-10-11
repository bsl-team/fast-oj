//go:build !windows

package judge

import (
	"os"
	"os/exec"
	"runtime"
	"syscall"
)

// plainGuard is the Unix side of enforcement. It deliberately does not pretend to
// be a sandbox: what it guarantees is that the whole process tree dies on timeout
// (Setpgid + group kill) and that time and peak memory are measured for real.
//
// Hard caps belong to whoever started the judge. Go cannot hand a Linux child its
// rlimits at spawn (SysProcAttr.Rlimits only exists on the BSDs), and trying to
// wrap every run in a shell's ulimit would make the verdict depend on which shell
// the image happens to ship. Inside the container this is what actually enforces
// the limits:
//
//	--memory / --cpus / --pids-limit / --ulimit core=0 / --read-only + tmpfs
//
// With those in place a submission that allocates past its problem limit is
// OOM-killed by the cgroup and we still see the peak in ru_maxrss, so the verdict
// is MLE rather than an unexplained crash.
type plainGuard struct {
	lim  Limits
	proc *os.Process
}

func newGuard(lim Limits) (Guard, error) { return &plainGuard{lim: lim}, nil }

func (g *plainGuard) Configure(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true, // so Kill takes the whole tree, not just the leader
	}
	if g.lim.RunAs != nil {
		// Dropping the uid also means the program cannot read anything the judge
		// owns, and RLIMIT-style per-user budgets stop competing with the judge.
		cmd.SysProcAttr.Credential = &syscall.Credential{
			Uid: g.lim.RunAs.UID, Gid: g.lim.RunAs.GID, NoSetGroups: true,
		}
	}
	return nil
}

func (g *plainGuard) Attach(p *os.Process) error {
	g.proc = p
	return nil
}

// Kill stops the process group. A program that forked before hanging would
// otherwise orphan and keep burning the box while the judge moved on.
func (g *plainGuard) Kill() {
	if g.proc == nil {
		return
	}
	if err := syscall.Kill(-g.proc.Pid, syscall.SIGKILL); err != nil {
		_ = g.proc.Kill()
	}
}

// Measure reports nothing: time and peak RSS come out of the wait status.
func (g *plainGuard) Measure() (int, int) { return -1, -1 }

func (g *plainGuard) Close() error { return nil }

// cpuMS converts the child rusage from the wait status; timevals are seconds
// plus microseconds.
func cpuMS(ru *syscall.Rusage) int {
	user := ru.Utime.Sec*1000 + ru.Utime.Usec/1000
	sys := ru.Stime.Sec*1000 + ru.Stime.Usec/1000
	return int(user + sys)
}

// memKB converts ru_maxrss, which the kernel reports in kilobytes on Linux and in
// bytes on the BSDs.
func memKB(ru *syscall.Rusage) int {
	if runtime.GOOS == "linux" {
		return int(ru.Maxrss)
	}
	return int(ru.Maxrss / 1024)
}
