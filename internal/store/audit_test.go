package store

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestAuditWritesOneLine(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	if err := db.Audit(ctx, AuditEvent{
		ActorID: 7, Actor: "operator", Action: "perm.grant",
		Target: "alice", Detail: "ticket.handle", IP: "203.0.113.9",
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := db.ListAudit(ctx, AuditFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	r := rows[0]
	if r.ActorID != 7 || r.Actor != "operator" || r.Action != "perm.grant" ||
		r.Target != "alice" || r.Detail != "ticket.handle" || r.IP != "203.0.113.9" {
		t.Errorf("row = %+v", r)
	}
	if r.At == 0 {
		t.Error("审计行要自己带时间，不能靠调用方记得填")
	}
	// 没有账号的凭据也要记得下来：判机令牌那种场合 actor_id 是 0。
	if err := db.Audit(ctx, AuditEvent{Action: "auth.denied", Target: "/judge/claim", Detail: "不是判机令牌"}); err != nil {
		t.Fatal(err)
	}
	rows, _ = db.ListAudit(ctx, AuditFilter{Action: "auth.denied"})
	if len(rows) != 1 || rows[0].ActorID != 0 || rows[0].Actor != "" || rows[0].IP != "" {
		t.Errorf("匿名一条 = %+v", rows[0])
	}
	// 没有动作的行不是审计，写它的人一定搞错了什么。
	if err := db.Audit(ctx, AuditEvent{Actor: "operator"}); err == nil {
		t.Error("空 action 应当报错")
	}
}

// 只追加：最近的在最前面，游标按 id 往回走，一页不多不少。
func TestAuditOrderAndPaging(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	for i := 0; i < 7; i++ {
		if err := db.Audit(ctx, AuditEvent{
			Actor: "operator", Action: "ip.ban", Target: "203.0.113." + string(rune('0'+i)),
		}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := db.ListAudit(ctx, AuditFilter{Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 3 {
		t.Fatalf("第一页 %d 条, want 3", len(first))
	}
	if first[0].Target != "203.0.113.6" || first[2].Target != "203.0.113.4" {
		t.Errorf("顺序应当是新的在前：%+v", first)
	}
	if first[0].ID <= first[1].ID || first[1].ID <= first[2].ID {
		t.Errorf("id 必须递减，游标才算得对：%+v", first)
	}
	second, err := db.ListAudit(ctx, AuditFilter{Limit: 3, AfterID: first[2].ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 3 || second[0].Target != "203.0.113.3" {
		t.Errorf("第二页 = %+v", second)
	}
	third, err := db.ListAudit(ctx, AuditFilter{Limit: 3, AfterID: second[2].ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(third) != 1 || third[0].Target != "203.0.113.0" {
		t.Errorf("第三页 = %+v", third)
	}
	if last, err := db.ListAudit(ctx, AuditFilter{AfterID: third[0].ID}); err != nil || len(last) != 0 {
		t.Errorf("走到底应当是空页 = %d %v", len(last), err)
	}

	// 按动作筛，外加"这个筛选下总共有多少条"。
	bans, err := db.ListAudit(ctx, AuditFilter{Action: "ip.ban", Limit: 100})
	if err != nil || len(bans) != 7 {
		t.Errorf("按动作筛 = %d %v", len(bans), err)
	}
	if n, err := db.CountAudit(ctx, AuditFilter{Action: "ip.ban"}); err != nil || n != 7 {
		t.Errorf("CountAudit = %d %v", n, err)
	}
	if n, err := db.CountAudit(ctx, AuditFilter{Action: "perm.grant"}); err != nil || n != 0 {
		t.Errorf("没发生过的动作 = %d %v", n, err)
	}
	// limit 两头都要收住：0 是"用默认"，超大值不能变成一次读完整张表。
	if rows, err := db.ListAudit(ctx, AuditFilter{Limit: -5}); err != nil || len(rows) != 7 {
		t.Errorf("负 limit = %d %v", len(rows), err)
	}
	if rows, err := db.ListAudit(ctx, AuditFilter{Limit: 10000}); err != nil {
		t.Errorf("超大 limit 报错：%v", err)
	} else if len(rows) > MaxAuditPage {
		t.Errorf("超大 limit 没收到 %d，实际 %d", MaxAuditPage, len(rows))
	}
}

// 攻击者能控制的文本必须有上限：这是一张只追加的表，一行写多大就是多大。
func TestAuditClipsEveryControlledField(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	long := strings.Repeat("删", 5000)
	if err := db.Audit(ctx, AuditEvent{
		Actor: long, Action: "problem.delete", Target: long, Detail: long + "\n\n伪造的一行", IP: long,
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := db.ListAudit(ctx, AuditFilter{Limit: 1})
	if err != nil || len(rows) != 1 {
		t.Fatalf("%d %v", len(rows), err)
	}
	r := rows[0]
	check := func(name, got string, bound int) {
		if n := utf8.RuneCountInString(got); n > bound {
			t.Errorf("%s 有 %d 个字，上限是 %d", name, n, bound)
		}
	}
	check("actor", r.Actor, MaxAuditActor)
	check("target", r.Target, MaxAuditTarget)
	check("detail", r.Detail, MaxAuditDetail)
	check("ip", r.IP, MaxAuditIP)
	if !strings.HasSuffix(r.Detail, "…") {
		t.Errorf("截断要留下痕迹，不能让读者以为那就是全部：%q", r.Detail)
	}
	if strings.ContainsAny(r.Detail, "\n\r\t") {
		t.Errorf("换行与制表要压成一行，否则页面上分不清谁是谁：%q", r.Detail)
	}
	// 没超线的文本原样保留，包括中文——按字节裁会把汉字切成半个。
	if err := db.Audit(ctx, AuditEvent{Action: "note", Detail: "这是一条中文的备注，没有超长"}); err != nil {
		t.Fatal(err)
	}
	rows, _ = db.ListAudit(ctx, AuditFilter{Action: "note"})
	if len(rows) != 1 || rows[0].Detail != "这是一条中文的备注，没有超长" {
		t.Errorf("短文本被改了：%+v", rows)
	}
}
