package judge

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/liangbinchen2013/fast-oj/internal/jail"
)

// spinSource burns CPU forever, which is what the timeout guard has to stop.
const spinSource = `#include <cstdio>
int main(){ volatile double x=1; for(;;) x*=1.0000001; printf("%f\n",x); return 0;}`

// The uid drop only does anything when the judge runs as root (that is the
// container's setup), so this is opt-in. It guards the failure that showed up in
// the field: a dropped child that times out has to be killed *and* reaped, or the
// judge hangs on the stdout pipe and stops serving the queue.
func TestDroppedSubmissionStillTimesOut(t *testing.T) {
	tool := requireToolchain(t)
	raw := os.Getenv("FASTOJ_TEST_RUN_AS_UID")
	if raw == "" {
		t.Skip("set FASTOJ_TEST_RUN_AS_UID to exercise the uid drop (needs a root judge)")
	}
	uid, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatal(err)
	}

	// Not t.TempDir: that parent is 0700, which a dropped child cannot traverse,
	// and the failure would look like an execution problem instead.
	dir := filepath.Join(os.TempDir(), "fastoj-drop-work")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	rep := Judge(Task{
		SubmissionID: 1, PID: "P1", Language: "cpp", Source: spinSource,
		TimeLimitMS: 300, MemLimitKB: 262144,
		Cases: []Case{{Name: "1", Ordinal: 1, Expected: []byte("never\n")}},
	}, tool, dir, &DropTo{UID: uint32(uid), GID: uint32(uid)}, nil)
	if rep.Verdict != TLE {
		t.Fatalf("verdict = %s (%s), want TLE", rep.Verdict, rep.Detail)
	}
}

// The probe only prints "clean" when all three conditions hold at once, so it
// cannot pass by accident: a scrubbed-away PATH or a leaked operator variable
// both turn it into a WA. USERPROFILE and TMP are compared against each other
// rather than against the real home directory, because runEnv aims both at the
// work dir and that is the property worth asserting.
const scrubProbeSource = `#include <cstdio>
#include <cstdlib>
#include <cstring>
int main(){
  const char* secret = getenv("FASTOJ_TEST_SECRET");
  const char* path = getenv("PATH");
  const char* profile = getenv("USERPROFILE");
  const char* tmp = getenv("TMP");
  bool ok = secret == NULL && path != NULL && profile != NULL && tmp != NULL &&
            strcmp(profile, tmp) == 0;
  puts(ok ? "clean" : "leaked");
  return 0;
}`

func TestJudgedProgramGetsAScrubbedEnvironment(t *testing.T) {
	tool := requireToolchain(t)
	t.Setenv("FASTOJ_TEST_SECRET", "must-not-reach-the-submission")

	rep := judgeSource(t, tool, scrubProbeSource,
		[]Case{{Name: "1", Ordinal: 1, Expected: []byte("clean\n")}}, 2000, 262144)
	if rep.Verdict != AC {
		t.Fatalf("verdict = %s (%s): the judged program did not see exactly the scrubbed environment",
			rep.Verdict, rep.Detail)
	}
}

func TestRunEnvKeepsOnlyWhatAToolchainNeeds(t *testing.T) {
	t.Setenv("FASTOJ_TEST_SECRET", "admin-token-lookalike")
	t.Setenv("PATH", "C:/mingw64/bin")
	t.Setenv("SystemRoot", "C:/Windows")
	dir := "C:/oj/work"

	got := runEnv(dir)
	joined := strings.Join(got, "\n")

	for _, want := range []string{"TEMP=" + dir, "TMP=" + dir, "HOME=" + dir, "USERPROFILE=" + dir, "PATH=C:/mingw64/bin", "SystemRoot=C:/Windows"} {
		if !strings.Contains(joined, want) {
			t.Errorf("runEnv dropped %q:\n%s", want, joined)
		}
	}
	for _, bad := range []string{"FASTOJ_TEST_SECRET", "admin-token-lookalike"} {
		if strings.Contains(joined, bad) {
			t.Errorf("runEnv leaked %q into the judged program:\n%s", bad, joined)
		}
	}
	// Nothing is inherited, so every entry is either one runEnv aims at the work
	// dir itself or one copied through an allow-listed key.
	for _, kv := range got {
		key, _, found := strings.Cut(kv, "=")
		if !found {
			t.Fatalf("malformed env entry %q", kv)
		}
		if !isJudgeSetEnv(key) && !isAllowedEnvKey(key) {
			t.Errorf("runEnv passes through %q, which is neither set by runEnv nor allow-listed", key)
		}
	}
}

// keys runEnv always writes itself, with the work dir or a fixed value as target.
var judgeSetKeys = []string{"TEMP", "TMP", "TMPDIR", "HOME", "USERPROFILE", "LANG", "LC_ALL"}

func isJudgeSetEnv(key string) bool {
	return containsFold(judgeSetKeys, key)
}

func isAllowedEnvKey(key string) bool {
	return containsFold(allowedEnvKeys, key)
}

func containsFold(list []string, want string) bool {
	for _, k := range list {
		if strings.EqualFold(k, want) {
			return true
		}
	}
	return false
}

// ECS 上撞到的那件事：启动自测判定"这台开不了牢房"之后，判机仍然给每条工单去建牢房，
// 于是每条提交都变成 IE。auto 的语义是"能开就开"，不是"开不了就每条都判死"。
// 三态是必需的：自测本身要靠一次带牢房的运行来完成，只有开/关两档的话它永远测得过。
func TestJailStopsAfterAFailedProbe(t *testing.T) {
	want := sandbox
	t.Cleanup(func() { sandbox = want })

	SetSandbox("/proc/self/exe", jail.Auto, true, 1000, 1000)
	if _, ok := jailFor(Limits{TimeMS: 100, MemKB: 1024}); !ok {
		t.Fatal("还没自测就该照计划建牢房，否则自测测不出任何东西")
	}
	reportSandbox(true, "沙盒已启用")
	if _, ok := jailFor(Limits{TimeMS: 100, MemKB: 1024}); !ok {
		t.Error("自测通过后应当继续关牢房")
	}
	reportSandbox(false, "沙盒未能建立: operation not permitted")
	if _, ok := jailFor(Limits{TimeMS: 100, MemKB: 1024}); ok {
		t.Error("自测说开不了，就不要再让每条工单去撞一次")
	}
	if SandboxAvailable() {
		t.Error("SandboxAvailable 应当跟着自测结论走，/judges 上那行说明靠它")
	}

	// -sandbox=off 永远不建牢房，与自测结论无关。
	SetSandbox("", jail.Off, false, 0, 0)
	if _, ok := jailFor(Limits{TimeMS: 100, MemKB: 1024}); ok {
		t.Error("off 模式下不该尝试牢房")
	}
}
