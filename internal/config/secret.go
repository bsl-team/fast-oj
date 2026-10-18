package config

import (
	"sort"
	"strings"
)

// ArgvSecrets 报告哪些密钥的值是**写在命令行上**传进来的，返回 flag 名，不返回值本身
// （把值再打印一遍只会让它多一个出现在日志里的机会）。
//
// 为什么这算泄露：Linux 上 /proc/<pid>/cmdline 是 0444、所有本地账号都读得到（hidepid
// 默认没开），而判机上跑的正是别人提交的代码——一次逃出沙盒的运行就能把判机令牌抄走。
// 拿着它可以去领任意一道题的源码、拉全部隐藏用例与答案、回报假判定。
//
// systemd 的 ExecStart 里写 `-token $FASTOJ_JUDGE_TOKEN` 会先展开再 exec，所以那种写法
// 等于把密钥贴给整台机器，哪怕它来自一个 0600 的 EnvironmentFile。两个二进制的默认值本来
// 就是环境变量（FASTOJ_JUDGE_TOKEN / FASTOJ_ADMIN_TOKEN），别再在命令行里传一遍。
func ArgvSecrets(args []string, secrets map[string]string) []string {
	var out []string
	for name, value := range secrets {
		if value == "" {
			continue
		}
		for _, a := range args {
			// 认 `-flag 值` 与 `-flag=值` 两种写法；顺带也认值单独出现，因为命令行上
			// 一个跟密钥一模一样的串只可能是这么来的。
			if a == value || strings.HasSuffix(a, "="+value) {
				out = append(out, name)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}
