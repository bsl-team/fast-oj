//go:build linux

package judge

import (
	"os"
	"syscall"
	"testing"
)

// 判机自己的 umask 不能决定"学生能不能读到自己那份代码"。
//
// systemd 单元里 `UMask=0027` 是好默认（服务不该到处留人可读可写的东西），但它会把请求的
// 0644 磨成 0640。判机以 root 跑、exec 前再把身份降到 nobody 之后，那份 root 所有、别人
// 不可读的文件连**提交自己**都打不开：g++ 读不了 main.cpp，解释器读不了 pyguard.py，
// 判题器与交互器读不了输入与答案文件 —— 全在门口一个 EACCES。
//
// 生产上真就是这么炸的：`can't open file '/pyguard.py': [Errno 13] Permission denied`。
// 这条测试要在 root 上跑，因为非 root 的判机走 user namespace，文件属主在牢房里看着像
// nobody，恰好把问题遮掉。
func TestRunFilesStayReadableUnderTheJudgeUmask(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("要 root：只有降权到 nobody 之后，「读不到自己的源码」这件事才会显形")
	}
	jailOn(t)
	tool := requireToolchain(t)

	old := syscall.Umask(0o027)
	defer syscall.Umask(old)

	if rep := judgeSource(t, tool, sumSource, sumCases(), 5000, 262144); rep.Verdict != AC {
		t.Fatalf("umask 027 下的 C++ 提交 = %s（%s），要 AC", rep.Verdict, rep.Detail)
	}
	if rep := judgePythonInJail(t, pySumSource, "3\n"); rep.Verdict != AC {
		t.Fatalf("umask 027 下的 Python 提交 = %s（%s），要 AC —— 护栏文件八成又不可读了", rep.Verdict, rep.Detail)
	}
}

// 落盘模式写死，不靠调用点的运气：0o077 里只看"其他"那三位是不是可读。
func TestWriteRunFileIgnoresTheUmask(t *testing.T) {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	path := t.TempDir() + "/main.cpp"
	if err := writeRunFile(path, []byte("int main(){}")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o004 == 0 {
		t.Fatalf("落盘模式 %v：其他位不可读，降到 nobody 的提交读不到自己的文件", info.Mode().Perm())
	}
}
