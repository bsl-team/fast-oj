//go:build windows

package judge

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The job object is the only thing standing between a submission and the machine,
// so the limits are read back out of the kernel rather than trusted from the
// struct we passed in.
func TestJobGuardCapsProcessTreeAndTotalMemory(t *testing.T) {
	g, err := newGuard(Limits{TimeMS: 1000, MemKB: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	job := g.(*jobGuard).job
	var got windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := queryJob(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&got)), uint32(unsafe.Sizeof(got))); err != nil {
		t.Fatal(err)
	}

	flags := got.BasicLimitInformation.LimitFlags
	for _, f := range []uint32{
		windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS,
		windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY,
		windows.JOB_OBJECT_LIMIT_JOB_MEMORY,
	} {
		if flags&f == 0 {
			t.Errorf("limit flag %#x is not set", f)
		}
	}
	if got.BasicLimitInformation.ActiveProcessLimit != maxTreeProcesses {
		t.Fatalf("active process limit = %d, want %d", got.BasicLimitInformation.ActiveProcessLimit, maxTreeProcesses)
	}
	if want := uintptr(64<<10) * 1024; got.ProcessMemoryLimit != want {
		t.Fatalf("per-process memory limit = %d, want %d", got.ProcessMemoryLimit, want)
	}
	if want := uintptr(64<<10) * 1024 * jobMemFactor; got.JobMemoryLimit != want {
		t.Fatalf("whole-tree memory limit = %d, want %d", got.JobMemoryLimit, want)
	}
}

// Without a memory cap the job must not invent one, otherwise an unlimited problem
// would fail programs that legitimately allocate a lot.
func TestJobGuardLeavesUnlimitedRunsUncapped(t *testing.T) {
	g, err := newGuard(Limits{TimeMS: 1000})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	job := g.(*jobGuard).job
	var got windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := queryJob(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&got)), uint32(unsafe.Sizeof(got))); err != nil {
		t.Fatal(err)
	}
	if got.BasicLimitInformation.LimitFlags&windows.JOB_OBJECT_LIMIT_JOB_MEMORY != 0 {
		t.Error("a run without a memory cap still got a job memory limit")
	}
	if got.BasicLimitInformation.LimitFlags&windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS == 0 {
		t.Error("fork bombs are unlimited")
	}
}

// Compiling is a driver plus its back ends in the same job, so the caller can
// widen the process budget for it.
func TestJobGuardHonoursWiderProcessBudget(t *testing.T) {
	g, err := newGuard(Limits{TimeMS: 1000, MaxProcs: CompileLimits.MaxProcs})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	job := g.(*jobGuard).job
	var got windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := queryJob(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&got)), uint32(unsafe.Sizeof(got))); err != nil {
		t.Fatal(err)
	}
	if got.BasicLimitInformation.ActiveProcessLimit != uint32(CompileLimits.MaxProcs) {
		t.Fatalf("active process limit = %d, want %d", got.BasicLimitInformation.ActiveProcessLimit, CompileLimits.MaxProcs)
	}
}
