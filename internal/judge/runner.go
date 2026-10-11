package judge

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

const (
	// MaxOutput is the per-stream cap. Past it whatever the program printed
	// already cannot match the expected answer, so it is scored as WA.
	MaxOutput = 1 << 20
	MaxStderr = 1 << 16
	// wallClockSlack absorbs process startup and scheduling jitter, and the
	// floor keeps very small limits usable.
	wallClockSlack = 2.0
	minWallClock   = 2 * time.Second
)

type Limits struct {
	TimeMS int
	MemKB  int
	// MaxProcs bounds the process tree. Zero means the judge's default for a
	// submission; compiling needs more headroom because the driver runs its back
	// ends (cc1plus, as, ld) as children of the same job.
	MaxProcs int
}

type Execution struct {
	Verdict Verdict
	TimeMS  int
	MemKB   int
	Stdout  []byte
	Stderr  []byte
	Exit    int
	Note    string
}

// Guard is the platform-specific resource enforcement around one process tree.
type Guard interface {
	// Attach binds a running process to the limits.
	Attach(p *os.Process) error
	// Kill stops the whole tree.
	Kill()
	// Measure reports accumulated CPU time in ms and peak memory in KB, or -1
	// for either when the platform cannot tell.
	Measure() (timeMS int, memKB int)
	Close() error
}

type limitedBuffer struct {
	buf  bytes.Buffer
	cap  int
	flew bool
}

func newCappedBuffer(capacity int) *limitedBuffer {
	return &limitedBuffer{cap: capacity}
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := b.cap - b.buf.Len(); room >= len(p) {
		b.buf.Write(p)
	} else {
		if room > 0 {
			b.buf.Write(p[:room])
		}
		b.flew = true
	}
	// Always report the full count so os/exec keeps draining the pipe instead of
	// blocking a chatty program forever.
	return len(p), nil
}

func (b *limitedBuffer) Bytes() []byte { return b.buf.Bytes() }

func (b *limitedBuffer) Overflowed() bool { return b.flew }

func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	if err != nil {
		return -1
	}
	return 0
}

// allowedEnvKeys is everything a judged program may inherit. SystemRoot/WINDIR
// are what the loader needs to find the system DLLs; PATH is what reaches the
// compiler. Nothing else survives, because none of it is the program's business.
var allowedEnvKeys = []string{"PATH", "SystemRoot", "WINDIR"}

// runEnv is the entire environment a judged program is allowed to see. Nothing
// is inherited on purpose: the judge process holds operator secrets (its token,
// the database path, whatever the service unit exports), and a submission that
// could call getenv would read them out of the very same process it was spawned
// from. Only what a native toolchain needs to start survives, and temp/home are
// aimed at the throwaway work directory.
func runEnv(dir string) []string {
	env := []string{
		"TEMP=" + dir, "TMP=" + dir, "TMPDIR=" + dir,
		"HOME=" + dir, "USERPROFILE=" + dir,
		"LANG=C", "LC_ALL=C",
	}
	for _, key := range allowedEnvKeys {
		if v := os.Getenv(key); v != "" {
			env = append(env, key+"="+v)
		}
	}
	return env
}

// Run executes argv in dir with stdin under lim. The verdict describes this one
// execution only; comparing output is the caller's job.
func Run(argv []string, dir string, stdin []byte, lim Limits) Execution {
	if len(argv) == 0 {
		return Execution{Verdict: IE, Note: "empty command line"}
	}
	bin, err := exec.LookPath(argv[0])
	if err != nil {
		return Execution{Verdict: IE, Note: "missing program: " + argv[0]}
	}
	if lim.TimeMS <= 0 {
		lim.TimeMS = 1000
	}

	cmd := exec.Command(bin, argv[1:]...)
	cmd.Dir = dir
	cmd.Env = runEnv(dir)
	stdout, stderr := newCappedBuffer(MaxOutput), newCappedBuffer(MaxStderr)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.Stdin = bytes.NewReader(stdin)

	g, err := newGuard(lim)
	if err != nil {
		return Execution{Verdict: IE, Note: "sandbox init failed: " + err.Error()}
	}
	defer g.Close()

	if err := cmd.Start(); err != nil {
		return Execution{Verdict: IE, Note: "start failed: " + err.Error()}
	}
	if err := g.Attach(cmd.Process); err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return Execution{Verdict: IE, Note: "attach failed: " + err.Error()}
	}

	done := make(chan waitResult, 1)
	go func() {
		err := cmd.Wait()
		done <- waitResult{err: err, state: cmd.ProcessState}
	}()

	select {
	case w := <-done:
		_, jobMemKB := g.Measure()
		timeMS := usageMS(w.state)
		if timeMS < 0 {
			timeMS, _ = g.Measure()
		}
		memKB := jobMemKB
		ex := Execution{
			TimeMS: timeMS, MemKB: memKB, Exit: exitCode(w.err),
			Stdout: stdout.Bytes(), Stderr: stderr.Bytes(),
		}
		switch {
		case stdout.Overflowed():
			ex.Verdict = WA
			ex.Note = "output over " + strconv.Itoa(MaxOutput) + " bytes"
		case lim.MemKB > 0 && memKB > lim.MemKB:
			ex.Verdict = MLE
		case ex.Exit != 0:
			ex.Verdict = RE
			if ex.Note == "" && w.err != nil {
				ex.Note = w.err.Error()
			}
		default:
			ex.Verdict = AC
		}
		return ex

	case <-time.After(wallClockFor(lim.TimeMS)):
		g.Kill()
		<-done
		_, memKB := g.Measure()
		return Execution{
			Verdict: TLE, TimeMS: lim.TimeMS, MemKB: memKB,
			Stdout: stdout.Bytes(), Stderr: stderr.Bytes(),
			Note: "wall clock limit " + strconv.Itoa(lim.TimeMS) + "ms exceeded",
		}
	}
}

// waitResult carries both halves of cmd.Wait: the error and the exit status the
// platform put in it.
type waitResult struct {
	err   error
	state *os.ProcessState
}

// usageMS reads per-process CPU time out of the wait status; -1 when the
// platform does not put it there.
func usageMS(state *os.ProcessState) int {
	if state == nil {
		return -1
	}
	ru, ok := state.SysUsage().(*syscall.Rusage)
	if !ok {
		return -1
	}
	return cpuMS(ru)
}

// wallClockFor turns a CPU limit into a hard deadline. Without one, a program
// that sleeps instead of computing would pin a judge slot forever.
func wallClockFor(timeMS int) time.Duration {
	d := time.Duration(float64(timeMS)*wallClockSlack) * time.Millisecond
	if d < minWallClock {
		d = minWallClock
	}
	return d
}
