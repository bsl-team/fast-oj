package judge

import (
	"os"
	"path/filepath"
	"runtime"
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

	// 换一套配置就把上一条命的判决带走：ECS 上就是这么把一次自测悄悄变成"没建牢房也
	// 算通过"的——旧的 probeUnavailable 让 jailFor 直接返回 false，/bin/true 在牢房外
	// 跑得好好的，于是日志写"沙盒已启用"。
	SetSandbox("/proc/self/exe", jail.Auto, true, 1000, 1000)
	if _, ok := jailFor(Limits{TimeMS: 100, MemKB: 1024}); !ok {
		t.Error("换了配置就该重新照计划建牢房，让新的自测测得出东西")
	}
	if SandboxNote() != "" {
		t.Errorf("换配置后那句旧结论不该还留着：%q", SandboxNote())
	}

	// -sandbox=off 永远不建牢房，与自测结论无关。
	SetSandbox("", jail.Off, false, 0, 0)
	if _, ok := jailFor(Limits{TimeMS: 100, MemKB: 1024}); ok {
		t.Error("off 模式下不该尝试牢房")
	}
}

// ECS 上真撞到的那件事：unshare 被内核拒掉时 exec 根本没发生，退出码是 0，判定却是
// IE。自测只盯着退出码就会把"开不了牢房"记成"沙盒已启用"，判机于是带着这句假话继续
// 把每一条提交判成 IE。IE 必须算失败，与退出码无关。
func TestProbeFailureCountsIEEvenWithExitZero(t *testing.T) {
	cases := []struct {
		name    string
		ex      Execution
		want    bool
		whyWant string
	}{
		{
			name: "unshare 被拒",
			ex: Execution{Verdict: IE, Exit: 0,
				Note: "沙盒未能启动（unshare 命名空间或 exec 被拒）: fork/exec /proc/self/exe: permission denied"},
			want:    true,
			whyWant: "exec 没发生，退出码 0 不代表牢房建起来了",
		},
		{
			name:    "jailer 自己声明失败",
			ex:      Execution{Verdict: IE, Exit: 0, Note: "沙盒未能建立: operation not permitted"},
			want:    true,
			whyWant: "牢房建不起来",
		},
		{
			name:    "没有前缀的 IE 也要认",
			ex:      Execution{Verdict: IE, Exit: 0, Note: "attach failed: no such process"},
			want:    true,
			whyWant: "任何一次 IE 都不是沙盒可用",
		},
		{
			name:    "非零退出码",
			ex:      Execution{Verdict: RE, Exit: 1, Stderr: []byte("boom")},
			want:    true,
			whyWant: "牢房里的 /bin/true 都不肯退 0，说明边界把它挡住了",
		},
		{name: "干净跑通", ex: Execution{Verdict: AC, Exit: 0}, want: false},
	}
	for _, c := range cases {
		note, failed := probeFailure(c.ex)
		if failed != c.want {
			t.Errorf("%s：probeFailure 判成 %v，要 %v（%s）", c.name, failed, c.want, c.whyWant)
		}
		if c.want && note == "" {
			t.Errorf("%s：失败了却没有可写进日志的话", c.name)
		}
	}
	// 那句自带"沙盒未能"的话原样留着，别在日志里叠两层前缀。
	if got, _ := probeFailure(Execution{Verdict: IE, Note: "沙盒未能建立: x"}); got != "沙盒未能建立: x" {
		t.Errorf("前缀叠加成了 %q", got)
	}
	if got, _ := probeFailure(Execution{Verdict: IE, Note: "attach failed: y"}); got != "沙盒未能建立: attach failed: y" {
		t.Errorf("补前缀补错了：%q", got)
	}
}

// 真跑一次自测：牢房的 exec 对象不存在，就必须报错并让后续工单不再尝试牢房。
func TestProbeSandboxFailsOnABrokenJailer(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("命名空间牢房只有 Linux 有")
	}
	want := sandbox
	t.Cleanup(func() { sandbox = want })

	SetSandbox("/definitely/not/an/executable", jail.Auto, true, 65534, 65534)
	note, err := ProbeSandbox(filepath.Join(t.TempDir(), "probe"))
	if err == nil {
		t.Fatalf("自测把开不了的牢房记成了成功：%q", note)
	}
	if !strings.Contains(note, "沙盒未能") {
		t.Errorf("日志里这句话应当直说牢房没建起来，got %q", note)
	}
	if _, ok := jailFor(Limits{TimeMS: 100, MemKB: 1024}); ok {
		t.Error("自测失败后 auto 不该再给每条工单建牢房")
	}
	if SandboxAvailable() {
		t.Error("/judges 上那行说明不该显示沙盒可用")
	}
}

// 自测必须在牢房里跑。jailFor 已经拒绝建牢房时还去跑一次 /bin/true，测的是牢房外的
// 世界，跑通了就会把"未隔离"写成"沙盒已启用"——上面那条 ECS 事故的另一半。
func TestProbeDoesNotPretendWithoutAJailer(t *testing.T) {
	want := sandbox
	t.Cleanup(func() { sandbox = want })

	SetSandbox("", jail.Auto, true, 65534, 65534) // mode 不是 off，却没有 jailer
	note, err := ProbeSandbox(filepath.Join(t.TempDir(), "probe"))
	if err == nil {
		t.Fatalf("连 jailer 都没有，自测却报了成功：%q", note)
	}
	if !strings.Contains(note, "沙盒未能建立") {
		t.Errorf("这句话应当直说牢房没建起来，got %q", note)
	}
	if SandboxAvailable() {
		t.Error("没建过牢房就不能声称建过")
	}
}
