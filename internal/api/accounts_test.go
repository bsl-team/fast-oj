package api

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// 批量建号：一个班四十个学生，不该用注册表单填四十遍。建出来的是普通账号，
// 这一页不给任何人管理员身份。

func TestBulkAccountsFromAList(t *testing.T) {
	h := newHarness(t)
	teacher := h.author("teacher10", permUserManage)

	// 撞名的那一行不该连累整批：先把 stu01 建成，最后一节再拿它试。
	values := url.Values{
		"mode": {"list"}, "password": {"welcome-2026"},
		"list": {"stu01\nstu02,stu02@school.cn\n"},
	}
	code, body := h.postAs(teacher, "/admin/accounts", values)
	if code != http.StatusOK {
		t.Fatalf("建号 = %d:\n%s", code, short(body))
	}
	for _, want := range []string{"stu01", "stu02", "stu02@school.cn", "welcome-2026", "建好 2 个"} {
		if !strings.Contains(body, want) {
			t.Errorf("结果页缺 %q：\n%s", want, short(body))
		}
	}
	u := h.user("stu01")
	if u.Role != "user" || u.Status != "active" {
		t.Errorf("批量建出来的该是普通可用账号：%+v", u)
	}
	// 密码是真的：拿它登录一次。
	if code, _, hdr := h.postNoRedirect("/login", url.Values{"identifier": {"stu01"}, "password": {"welcome-2026"}}, ""); code != http.StatusSeeOther ||
		hdr.Get("Set-Cookie") == "" {
		t.Fatalf("用初始密码登录 = %d", code)
	}
	// 没有权限的人进不来这一页，也提交不了。
	outsider := h.author("outsider10")
	if code, _ := h.getAs(outsider, "/admin/accounts/new"); code != http.StatusForbidden {
		t.Errorf("没有 user.manage 的人看到了建号页 = %d", code)
	}

	// 撞名只跳过那一行，其余照样建好。
	dup := url.Values{"mode": {"list"}, "password": {"welcome-2026"}, "list": {"stu01\nstu03\n"}}
	if code, body := h.postAs(teacher, "/admin/accounts", dup); code != http.StatusOK ||
		!strings.Contains(body, "撞名，跳过") || !strings.Contains(body, "建好 1 个") {
		t.Fatalf("撞名的那次 = %d:\n%s", code, short(body))
	}
}

func TestBulkAccountsByCount(t *testing.T) {
	h := newHarness(t)
	teacher := h.author("teacher11", permUserManage)

	values := url.Values{"mode": {"count"}, "prefix": {"kid"}, "from": {"1"}, "count": {"4"}}
	code, body := h.postAs(teacher, "/admin/accounts", values)
	if code != http.StatusOK {
		t.Fatalf("按个数建号 = %d:\n%s", code, short(body))
	}
	// 起始号补零到两位，跟名单里的写法一致。
	for _, want := range []string{"kid01", "kid02", "kid03", "kid04", "建好 4 个"} {
		if !strings.Contains(body, want) {
			t.Errorf("结果页缺 %q：\n%s", want, short(body))
		}
	}
	seen := map[string]bool{}
	for _, name := range []string{"kid01", "kid02", "kid03", "kid04"} {
		u, err := h.db.GetUserByHandle(ctx(), name)
		if err != nil {
			t.Fatalf("%s 没建出来：%v", name, err)
		}
		if u.Email != "" {
			t.Errorf("%s 不该有邮箱：%q", name, u.Email)
		}
	}
	// 每个号的初始密码都不一样，而且过密码策略。
	for _, pw := range bulkPasswords(body) {
		if passwordProblem(pw) != "" {
			t.Errorf("随机密码 %q 过不了策略", pw)
		}
		if seen[pw] {
			t.Errorf("两个号拿到了同一个随机密码 %q", pw)
		}
		seen[pw] = true
	}
	if len(seen) != 4 {
		t.Fatalf("页面上只认出 %d 个密码", len(seen))
	}
}

// bulkPasswords pulls the initial passwords out of the result table's <code> cells.
// The line must also contain </td>: the footer's API hint renders <code> too, and
// counting that as a student password is how a test starts passing for the wrong reason.
func bulkPasswords(body string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		if !strings.Contains(line, "</td>") {
			continue
		}
		a := strings.Index(line, "<code>")
		if a < 0 {
			continue
		}
		a += len("<code>")
		b := strings.Index(line[a:], "</code>")
		if b > 0 {
			out = append(out, line[a:a+b])
		}
	}
	return out
}

// userCount is how many accounts exist, so a refused batch can be proven to have
// written nothing.
func (h *harness) userCount() int {
	h.t.Helper()
	var n int
	if err := h.db.SQL.QueryRowContext(ctx(), `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}

// 批量入口不是绕过策略的后门：坏行为第几行要说清楚，而且一个都不建。
func TestBulkAccountsRefuseBadRows(t *testing.T) {
	h := newHarness(t)
	teacher := h.author("teacher12", permUserManage)

	before := h.userCount()
	for _, bad := range []struct {
		values url.Values
		why    string
	}{
		{url.Values{"mode": {"list"}, "password": {"welcome-2026"}, "list": {"has space\n"}}, "用户名"},
		{url.Values{"mode": {"list"}, "password": {"welcome-2026"}, "list": {"admin\n"}}, "保留"},
		{url.Values{"mode": {"list"}, "password": {"short1"}, "list": {"ok01\n"}}, "至少"},
		{url.Values{"mode": {"list"}, "password": {"welcome-2026"}, "list": {"ok01,not-an-email\n"}}, "邮箱"},
		{url.Values{"mode": {"count"}, "prefix": {"x"}, "from": {"1"}, "count": {"0"}}, "个数"},
		{url.Values{"mode": {"list"}, "password": {"welcome-2026"}, "list": {"\n\n"}}, "至少得给一行"},
	} {
		code, body := h.postAs(teacher, "/admin/accounts", bad.values)
		if code != http.StatusBadRequest || !strings.Contains(body, bad.why) {
			t.Errorf("%s：= %d，页面里没有 %q:\n%s", bad.why, code, bad.why, short(body))
		}
	}
	if n := h.userCount(); n != before {
		t.Errorf("被拒绝的批量请求还是建了账号：%d → %d", before, n)
	}
}

// 超级管理员那一票也不能被批量入口绕过：这一页只建 user 层的账号。
func TestBulkAccountsNeverCreateAdmins(t *testing.T) {
	h := newHarness(t)
	teacher := h.author("teacher13", permUserManage)
	if _, body := h.postAs(teacher, "/admin/accounts",
		url.Values{"mode": {"count"}, "prefix": {"plain"}, "from": {"1"}, "count": {"2"}}); !strings.Contains(body, "建好 2 个") {
		t.Fatalf("建号失败：\n%s", short(body))
	}
	for _, name := range []string{"plain01", "plain02"} {
		if u, err := h.db.GetUserByHandle(ctx(), name); err != nil || u.Role != "user" {
			t.Errorf("%s 角色 = %+v", name, u)
		}
		perms, _ := h.db.EffectivePermissions(ctx(), h.user(name).ID, "user")
		for _, key := range []string{permUserManage, permPermissionManage, permProblemCreate} {
			if perms[key] {
				t.Errorf("%s 拿到了 %s", name, key)
			}
		}
	}
}
