package config

import "testing"

// 令牌写在命令行上就等于发给了整台机器：判机上跑的是别人的代码，/proc/<pid>/cmdline 谁都读
// 得到。所以启动前要把这种写法点出来，而点名时不能把值再打印一遍。
func TestArgvSecretsNamesTheFlagNotTheValue(t *testing.T) {
	const token = "a733f0c9deadbeef"
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"-token 值", []string{"-server", "http://x", "-token", token}, []string{"-token"}},
		{"--token=值", []string{"--token=" + token}, []string{"-token"}},
		{"只给了名字没给值", []string{"-name", "box1"}, nil},
		{"值没出现在 argv 就是好的", []string{"-name", "box1", "-dir", "/tmp"}, nil},
		{"空值不算泄露", []string{"-token", ""}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ArgvSecrets(c.args, map[string]string{"-token": token, "-name": ""})
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("got %v, want %v", got, c.want)
				}
			}
			for _, g := range got {
				if g == token {
					t.Errorf("返回值本身了：%q", g)
				}
			}
		})
	}
}

// 两个令牌同时写在 argv 上时要都点出来，且顺序稳定（测试与人都要能读）。
func TestArgvSecretsSortsAndCoversBothTokens(t *testing.T) {
	got := ArgvSecrets([]string{"-judge-token", "JDG", "-admin-token", "ADM"},
		map[string]string{"-judge-token": "JDG", "-admin-token": "ADM"})
	if len(got) != 2 || got[0] != "-admin-token" || got[1] != "-judge-token" {
		t.Fatalf("got %v，要按名字排序的两个", got)
	}
}
