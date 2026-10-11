//go:build !windows

package judge

import (
	"os"
	"syscall"
)

// plainGuard only knows how to kill the direct child. On Linux the real
// isolation comes from an external sandbox binary, which is what a production
// judge box should run; this fallback keeps the pipeline working for
// development on a machine you control.
type plainGuard struct {
	proc *os.Process
}

func newGuard(lim Limits) (Guard, error) { return &plainGuard{}, nil }

func (g *plainGuard) Attach(p *os.Process) error {
	g.proc = p
	return nil
}

func (g *plainGuard) Kill() {
	if g.proc != nil {
		_ = g.proc.Kill()
	}
}

func (g *plainGuard) Measure() (int, int) { return -1, -1 }

func (g *plainGuard) Close() error { return nil }

// cpuMS converts the child rusage from the wait status; timevals are seconds
// plus microseconds.
func cpuMS(ru *syscall.Rusage) int {
	user := ru.Ru_utime.Sec*1000 + ru.Ru_utime.Usec/1000
	sys := ru.Ru_stime.Sec*1000 + ru.Ru_stime.Usec/1000
	return int(user + sys)
}
