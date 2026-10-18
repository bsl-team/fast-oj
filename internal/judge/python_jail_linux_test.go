//go:build linux

package judge

import (
	"path/filepath"
	"testing"
)

// 闸门承诺的是"有牢房才收 Python"，那就得有一条测试真的在牢房里跑一次 Python：
// 解释器要在 chroot 里起得来（/usr 是只读 bind，标准库在那里面），护栏要在里面照样
// 咬得住，而学生的视野里不该有宿主文件系统。这三件事任何一件只在开了沙盒的机器上坏掉，
// 表征都是"只有生产判不了 Python"。
func TestPythonInsideTheJail(t *testing.T) {
	jailOn(t)

	if rep := judgePythonInJail(t, pySumSource, "3\n"); rep.Verdict != AC {
		t.Fatalf("牢房里的正确 Python = %s（%s），要 AC", rep.Verdict, rep.Detail)
	}

	// /etc 是故意没 bind 进牢房的（少一个目录就少一分把宿主账号列表端出去的可能）。
	blind := `import os
try:
    with open("/etc/passwd", "rb") as f:
        f.read(1)
    print("SEES_HOST")
except OSError:
    print("blind")`
	if rep := judgePythonInJail(t, blind, "blind\n"); rep.Verdict != AC {
		t.Fatalf("牢房里的 Python 看见了不该看见的东西或跑不起来：%s（%s）", rep.Verdict, rep.Detail)
	}

	// 护栏在牢房里也一样咬：越界的尝试要撞到那句"判机不允许"，而不是悄悄出去。
	escape := `import subprocess
subprocess.run(["true"])`
	rep := judgePythonInJail(t, escape, "3\n")
	if rep.Verdict != RE {
		t.Fatalf("牢房里的 subprocess = %s（%s），要 RE", rep.Verdict, rep.Detail)
	}
}

// judgePythonInJail 用当前沙盒配置跑一份 Python，期望输出由调用方给（判定就照它来）。
func judgePythonInJail(t *testing.T, source, expected string) Report {
	t.Helper()
	tool := requirePython(t)
	task := Task{
		SubmissionID: 1, PID: "P1", Language: PythonLanguage, Source: source,
		TimeLimitMS: 10_000, MemLimitKB: 512 << 10,
		Cases: []Case{{Name: "1", Ordinal: 1, Score: 100,
			Input: []byte("1 2\n"), Expected: []byte(expected)}},
	}
	return Judge(task, tool, filepath.Join(t.TempDir(), "work"), nil, nil)
}
