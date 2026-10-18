package api

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/liangbinchen2013/fast-oj/internal/store"
)

// 审计：特权动作要留下一行活得比进程久的记录。这些用例断言的是"确实入库了、入库的是
// 谁做的、对谁做的"，不是页面上那句话。

// auditRows 读某个动作的全部审计行。
func (h *harness) auditRows(action string) []store.AuditRow {
	h.t.Helper()
	rows, err := h.db.ListAudit(context.Background(), store.AuditFilter{Action: action, Limit: 50})
	if err != nil {
		h.t.Fatal(err)
	}
	return rows
}

func TestAuditPermissionGrant(t *testing.T) {
	h := newHarness(t)
	h.author("auditee")

	if code, body := h.postBearer("/admin/perms/u/auditee",
		url.Values{"key": {permStatsView}, "action": {"grant"}}, h.admin); code != http.StatusOK {
		t.Fatalf("授予 = %d %s", code, short(body))
	}
	rows := h.auditRows("perm.grant")
	if len(rows) != 1 {
		t.Fatalf("perm.grant 行数 = %d, want 1：%+v", len(rows), rows)
	}
	r := rows[0]
	// 令牌进来的是 admin 这个账号：操作者要写下当时的名字，而不是"某个人"。
	if r.Actor != store.BootstrapHandle || r.ActorID == 0 {
		t.Errorf("操作者 = %q/%d", r.Actor, r.ActorID)
	}
	if r.Target != "auditee" || r.Detail != permStatsView {
		t.Errorf("授予的那一行 = %+v", r)
	}
	if r.IP == "" {
		t.Error("审计行要带来源地址，不然同一台机器上两个人分不清")
	}

	// 撤销与清除同样是特权动作。
	if code, _ := h.postBearer("/admin/perms/u/auditee",
		url.Values{"key": {permStatsView}, "action": {"revoke"}}, h.admin); code != http.StatusOK {
		t.Fatalf("撤销 = %d", code)
	}
	if rows := h.auditRows("perm.revoke"); len(rows) != 1 || rows[0].Detail != permStatsView {
		t.Errorf("perm.revoke = %+v", rows)
	}
	// 没做成的动作不算：不认识的能力键被拒之后不该多出一行。
	if code, _ := h.postBearer("/admin/perms/u/auditee",
		url.Values{"key": {"no.such.key"}, "action": {"grant"}}, h.admin); code == http.StatusOK {
		t.Fatalf("不存在的权限键居然成功了：%d", code)
	}
	if rows := h.auditRows("perm.grant"); len(rows) != 1 {
		t.Errorf("被拒的授予也记了一行：%+v", rows)
	}
}

// 封号、改角色、注销、封 IP、存判题器、删题：每一条都要留下行。
func TestAuditPrivilegedSites(t *testing.T) {
	h := newHarness(t)
	h.author("victim")
	if code, _ := h.postBearer("/admin/users/victim/status", url.Values{"status": {"banned"}}, h.admin); code != http.StatusOK {
		t.Fatalf("封号 = %d", code)
	}
	if code, _ := h.postBearer("/admin/users/victim/role", url.Values{"role": {"admin"}}, h.admin); code != http.StatusOK {
		t.Fatalf("改角色 = %d", code)
	}
	rows := h.auditRows("user.status")
	if len(rows) != 1 || rows[0].Target != "victim" || !strings.Contains(rows[0].Detail, "封禁") {
		t.Errorf("user.status = %+v", rows)
	}
	if got := h.auditRows("role.change"); len(got) != 1 || !strings.Contains(got[0].Detail, "user") {
		t.Errorf("role.change 要写明从哪个角色改来：%+v", got)
	}

	// 封 IP：原因是有文本的，也要在审计里。
	h.openSite("203.0.113.60")
	if code, body := h.from(http.MethodPost, "/admin/ips/ban", "127.0.0.1:1", h.admin,
		url.Values{"ip": {"203.0.113.60"}, "note": {"脚本刷注册"}}); code != http.StatusOK || !strings.Contains(body, "已封禁") {
		t.Fatalf("封 IP = %d", code)
	}
	if got := h.auditRows("ip.ban"); len(got) != 1 || got[0].Target != "203.0.113.60" ||
		!strings.Contains(got[0].Detail, "脚本刷注册") {
		t.Errorf("ip.ban = %+v", got)
	}
	if code, _ := h.from(http.MethodPost, "/admin/ips/unban/203.0.113.60", "127.0.0.1:1", h.admin, url.Values{}); code != http.StatusOK {
		t.Fatalf("解封 = %d", code)
	}
	if got := h.auditRows("ip.unban"); len(got) != 1 || got[0].Target != "203.0.113.60" {
		t.Errorf("ip.unban = %+v", got)
	}

	// 站点设置：审计要看得出改了哪些开关，而不是只看到"有人保存过设置"。
	if code, _ := h.postBearer("/admin/site", url.Values{
		"title": {"测试站"}, "tagline": {"首页语"}, "footer": {""}, "api_hint": {""},
		"links": {""}, "allow_register": {"1"}, "allow_topics": {"1"},
	}, h.admin); code != http.StatusOK {
		t.Fatalf("保存站点设置 = %d", code)
	}
	saved := h.auditRows("site.save")
	if len(saved) != 1 || saved[0].Target != "测试站" {
		t.Fatalf("site.save = %+v", saved)
	}
	if !strings.Contains(saved[0].Detail, "帖子=开") || !strings.Contains(saved[0].Detail, "注册=开") ||
		!strings.Contains(saved[0].Detail, "比赛=关") {
		t.Errorf("站点设置那一行没写清开关：%q", saved[0].Detail)
	}

	// 判题器：这是"把别人写的代码送上判机"，最该留痕的一项。
	writer := h.author("spjwriter", permProblemCreate, permTestcaseManage, permJudgeProgram)
	if code, body := h.postAs(writer, "/problems", problemValues("P7700", "判题器题")); code != http.StatusOK {
		t.Fatalf("建题 = %d %s", code, short(body))
	}
	spj := url.Values{"kind": {"checker"}, "language": {"cpp"}, "source": {"int main(){return 0;}"}}
	if code, body := h.postAs(writer, "/problems/P7700/judge_program", spj); code != http.StatusOK {
		t.Fatalf("存判题器 = %d %s", code, short(body))
	}
	savedProgram := h.auditRows("judge_program.save")
	if len(savedProgram) != 1 || savedProgram[0].Target != "P7700" ||
		!strings.Contains(savedProgram[0].Detail, "checker") {
		t.Fatalf("judge_program.save = %+v", savedProgram)
	}
	if code, _ := h.postAs(writer, "/problems/P7700/judge_program",
		url.Values{"kind": {"checker"}, "language": {"cpp"}, "source": {""}}); code != http.StatusOK {
		t.Fatalf("撤判题器 = %d", code)
	}
	if got := h.auditRows("judge_program.remove"); len(got) != 1 || got[0].Target != "P7700" {
		t.Errorf("judge_program.remove = %+v", got)
	}

	// 删题：题目没了以后，这一行是唯一还说得出"谁删的、删的是哪道题"的地方。
	if code, _ := h.postAs(writer, "/problems/P7700/delete", url.Values{}); code != http.StatusOK {
		t.Fatalf("删题 = %d", code)
	}
	deleted := h.auditRows("problem.delete")
	if len(deleted) != 1 || deleted[0].Target != "P7700" || !strings.Contains(deleted[0].Detail, "判题器题") {
		t.Errorf("problem.delete = %+v", deleted)
	}
}

// 判机协议上的鉴权失败：今天它只混在通用请求日志里（方法、路径、状态码），既没有
// 操作者也没有原因。
func TestAuditJudgeAuthDenials(t *testing.T) {
	h := newHarness(t)

	// 拿一个不对的 bearer 来领工单：401，而且要记得下来。
	if code, _ := h.from(http.MethodPost, "/judge/claim", "203.0.113.77:51234", "not-the-judge-token",
		url.Values{"name": {"box"}}); code != http.StatusUnauthorized {
		t.Fatalf("错令牌的 claim = %d, want 401", code)
	}
	rows := h.auditRows("judge.denied")
	if len(rows) != 1 {
		t.Fatalf("judge.denied 行数 = %d, want 1：%+v", len(rows), rows)
	}
	r := rows[0]
	if r.Target != "/judge/claim" {
		t.Errorf("被拒的路径 = %q", r.Target)
	}
	if r.IP != "203.0.113.77" {
		t.Errorf("来源地址 = %q", r.IP)
	}
	if !strings.Contains(r.Detail, "令牌") {
		t.Errorf("被拒的原因要写进审计行：%q", r.Detail)
	}
	// 令牌的值绝不进库：那一行只说凭据的形态。
	if strings.Contains(r.Actor+r.Detail, "not-the-judge-token") || strings.Contains(r.Actor+r.Detail, h.judge) {
		t.Errorf("审计行泄露了令牌：%+v", r)
	}
	if r.Actor != "bearer 令牌" || r.ActorID != 0 {
		t.Errorf("操作者 = %q/%d", r.Actor, r.ActorID)
	}

	// 登录了但没有判机权力，用 Cookie 打判机接口：403，同样要记。
	visitor := h.author("nojudge")
	if code, _ := h.postAs(visitor, "/judge/claim", url.Values{"name": {"box"}}); code != http.StatusForbidden {
		t.Fatalf("普通人打判机接口 = %d, want 403", code)
	}
	rows = h.auditRows("judge.denied")
	if len(rows) != 2 {
		t.Fatalf("judge.denied 行数 = %d, want 2", len(rows))
	}
	// 新的在前。
	if !strings.Contains(rows[0].Detail, "判机令牌") || rows[0].Actor != "会话 Cookie" {
		t.Errorf("Cookie 那一条 = %+v", rows[0])
	}
	if !strings.Contains(rows[1].Detail, "令牌") {
		t.Errorf("第一条被改写了：%+v", rows[1])
	}

	// 判机令牌正常领单不该记：那是每天的流量，不是被拒的敲门。（队列是空的，所以 404，
	// 但鉴权是过了的。）
	if code, _ := h.json(http.MethodPost, "/judge/claim", h.judge,
		[]byte(`{"name":"box","languages":["cpp"]}`)); code != http.StatusNotFound {
		t.Fatalf("空队列的 claim = %d, want 404", code)
	}
	if n := len(h.auditRows("judge.denied")); n != 2 {
		t.Errorf("成功的判机调用也记了一行：%+v", h.auditRows("judge.denied"))
	}
}

// 记不上账不能挡住正事：一张写不进去的审计表不该变成整站的开关。
func TestAuditFailureDoesNotBlockAction(t *testing.T) {
	h := newHarness(t)
	h.author("auditee2")
	// 把表抽掉，等于审计每次都写失败（磁盘满、库被锁）。
	if _, err := h.db.SQL.ExecContext(context.Background(), `DROP TABLE audit`); err != nil {
		t.Fatal(err)
	}
	if code, body := h.postBearer("/admin/perms/u/auditee2",
		url.Values{"key": {permStatsView}, "action": {"grant"}}, h.admin); code != http.StatusOK {
		t.Fatalf("审计写不进去时动作被挡了：%d %s", code, short(body))
	}
	u := h.user("auditee2")
	perms, err := h.db.EffectivePermissions(context.Background(), u.ID, u.Role)
	if err != nil || !perms[permStatsView] {
		t.Fatalf("授予本身没落库：%v %+v", err, perms)
	}
}
