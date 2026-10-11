package judge

import (
	"strings"
	"testing"
)

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
