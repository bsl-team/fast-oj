//go:build linux

package judge

import "testing"

// 牢房开着的时候，交互对话必须照样跑得通。两条管道是判机自己持有的文件描述符，交给
// 子进程后要穿过 chroot 与新命名空间——这件事不该靠推理，要有一条测试盯着：牢房改一行
// 就把对话断掉，而断掉的表征是"所有交互题集体 WA/IE"，很难往沙盒上想。
func TestInteractiveInsideTheJail(t *testing.T) {
	jailOn(t)
	tool := requireToolchain(t)

	if rep := judgeInteractive(t, tool, itxCandidate, itxGood, itxCases(), 5000); rep.Verdict != AC {
		t.Fatalf("牢房里的正确对话 = %s（%s），要 AC", rep.Verdict, rep.Detail)
	}
	// 判定口径不能因为开没开牢房而变：同样这份答错的代码，牢房里也得是 WA 并带上评语。
	bad := judgeInteractive(t, tool, itxCandidate, itxWrong, itxCases(), 5000)
	if bad.Verdict != WA || bad.Detail == "" {
		t.Errorf("牢房里的答错对话 = %s（%s），要 WA 且有评语", bad.Verdict, bad.Detail)
	}
}

// 判题器同样要能穿过牢房：它编在只读的宿主工具链上、跑在 chroot 里，三个数据文件写在
// 这次的目录里。少一条测试，就会出现"只有开了沙盒的机器判不了 SPJ"这种只在生产炸的事。
func TestSPJInsideTheJail(t *testing.T) {
	jailOn(t)
	tool := requireToolchain(t)
	ac := checker("0")
	if rep := judgeWithSPJ(t, tool, ac, messySum, oneSumCase); rep.Verdict != AC {
		t.Fatalf("牢房里的判题器 = %s（%s），要 AC", rep.Verdict, rep.Detail)
	}
	wa := checker("1")
	if rep := judgeWithSPJ(t, tool, wa, messySum, oneSumCase); rep.Verdict != WA {
		t.Errorf("牢房里的判题器说错 = %s（%s），要 WA", rep.Verdict, rep.Detail)
	}
}
