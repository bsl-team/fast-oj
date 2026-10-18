//go:build linux

package jail

import (
	"syscall"
	"testing"
)

// 这条测试盯着 cBPF 的跳转方向。
//
// 跳转写反不会报错：内核照样收下过滤器，程序照跑，只是什么都没挡——实测就是这样让
// socket() 与 ptrace() 在"装了 seccomp 的牢房"里双双成功。所以这里不测"装上了没有"，
// 而是顺着指令流走一遍，检查"号命中"这条路真的走到 RET ERRNO。
func TestProgramRoutesDeniedSyscallsToErrno(t *testing.T) {
	instr := program()
	if len(instr) != 3+2*len(denied)+1 {
		t.Fatalf("指令流长度 %d，要 %d", len(instr), 3+2*len(denied)+1)
	}
	// 第一条是"载架构"，第二条是"架构不对就跳开整条链"。
	if instr[1].Jt != 0 {
		t.Errorf("架构命中时应当顺着往下挡（jt=%d，要 0）——写反了就是过滤器装了但什么都不挡", instr[1].Jt)
	}
	if want := uint8(len(denied)*2 + 1); instr[1].Jf != want {
		t.Errorf("架构不对时要跳到那条 ALLOW（jf=%d，要 %d）", instr[1].Jf, want)
	}
	// 每个被挡的号后面必须紧跟一条 RET ERRNO；这条链上任何一格串错，那个号就形同没挡。
	for i, want := range denied {
		cmp := instr[3+i*2]
		if cmp.K != want {
			t.Fatalf("第 %d 条比对的是 %d，要 %d", i, cmp.K, want)
		}
		if cmp.Jt != 0 || cmp.Jf != 1 {
			t.Errorf("%d 的跳转把命中与不命中接反了（jt=%d jf=%d）", want, cmp.Jt, cmp.Jf)
		}
		ret := instr[3+i*2+1]
		if ret.Code != syscall.BPF_RET|syscall.BPF_K || ret.K != seccompRetErrno|eperm {
			t.Errorf("%d 命中后返回 %#x，要 ERRNO|EPERM", want, ret.K)
		}
	}
	last := instr[len(instr)-1]
	if last.K != seccompRetAllow {
		t.Errorf("指令流最后一条应当是 ALLOW，得到 %#x", last.K)
	}
}

// 黑名单必须真含得下那几个：断网不靠它（CLONE_NEWNET 已经断了），靠它的是 ptrace 与
// "在牢房里再开一间牢房"这一类。少写一个就是一个洞，而且没人会注意到。
func TestDeniedListCoversTheOutwardPaths(t *testing.T) {
	for _, want := range []struct {
		name string
		nr   uint32
	}{
		{"socket", syscall.SYS_SOCKET}, {"connect", syscall.SYS_CONNECT},
		{"sendmsg", syscall.SYS_SENDMSG}, {"ptrace", syscall.SYS_PTRACE},
		{"process_vm_readv", sysProcessVMReadv}, {"unshare", syscall.SYS_UNSHARE},
		{"mount", syscall.SYS_MOUNT}, {"bpf", sysBpf},
	} {
		found := false
		for _, nr := range denied {
			if nr == want.nr {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("黑名单里没有 %s（号 %d）", want.name, want.nr)
		}
	}
}
