package store

import (
	"context"
	"strings"
	"testing"
)

func TestOwnerClaimIsFirstComeOnly(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	// 全新库：还没有人认领这个位子。
	if id, err := db.OwnerID(ctx); err != nil || id != 0 {
		t.Fatalf("fresh database owner = %d (%v), want none", id, err)
	}
	first, second := mustUser(t, db, "first"), mustUser(t, db, "second")

	if won, err := db.ClaimOwner(ctx, first.ID); err != nil || !won {
		t.Fatalf("first claim = %v (%v)", won, err)
	}
	// 第二个来的人只能干看着：这个位子不可转让。
	if won, err := db.ClaimOwner(ctx, second.ID); err != nil || won {
		t.Fatalf("second claim = %v (%v), want refused", won, err)
	}
	if id, _ := db.OwnerID(ctx); id != first.ID {
		t.Fatalf("owner = %d, want %d", id, first.ID)
	}
	// 坐上这个位子同时就是管理员：别的路径都读 users.role 判管理员。
	if u, _ := db.GetUserByID(ctx, first.ID); u.Role != "admin" {
		t.Errorf("站主角色 = %s, want admin", u.Role)
	}
	if u, _ := db.GetUserByID(ctx, second.ID); u.Role != "user" {
		t.Errorf("后来者的角色 = %s, want user", u.Role)
	}
	if ok, _ := db.IsOwner(ctx, first.ID); !ok {
		t.Error("the founder should be the owner")
	}
	if ok, _ := db.IsOwner(ctx, second.ID); ok {
		t.Error("a later account is not the owner")
	}
	// 没有站主时不认错人。
	if ok, err := db.IsOwner(ctx, 0); err != nil || ok {
		t.Errorf("IsOwner(0) = %v (%v)", ok, err)
	}
}

// 站主的免疫是数据层的事实，不是某个页面的礼貌：正常入口拒绝，绕过入口直接写库
// 也收不走他的任何一项权限。
func TestOwnerKeepsEveryPermission(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	boss := mustUser(t, db, "boss")
	if _, err := db.ClaimOwner(ctx, boss.ID); err != nil {
		t.Fatal(err)
	}

	for _, tried := range []struct {
		what string
		err  error
	}{
		{"收回权限", db.SetUserPermission(ctx, boss.ID, "problem.create", false)},
		{"清除单项", db.ClearUserPermission(ctx, boss.ID, "problem.create")},
		{"改角色", db.SetUserRole(ctx, boss.ID, "user")},
		{"封号", db.SetUserStatus(ctx, boss.ID, "banned")},
	} {
		if tried.err == nil || !strings.Contains(tried.err.Error(), "超级管理员") {
			t.Errorf("%s 应该被拒绝，实际 = %v", tried.what, tried.err)
		}
	}

	// 绕过上面的入口，直接往那张表里插一条收回：仍然收不走任何一项权限。
	if _, err := db.SQL.ExecContext(ctx,
		`INSERT INTO user_permissions (user_id, key, granted) VALUES (?, 'problem.create', 0)`, boss.ID); err != nil {
		t.Fatal(err)
	}
	perms, err := db.EffectivePermissions(ctx, boss.ID, "user")
	if err != nil {
		t.Fatal(err)
	}
	listed, err := db.ListPermissions(ctx)
	if err != nil || len(listed) == 0 {
		t.Fatalf("permissions = %d (%v)", len(listed), err)
	}
	for _, p := range listed {
		if !perms[p.Key] {
			t.Errorf("站主少了 %s", p.Key)
		}
	}

	// 权限编辑页也一样：站主那一行永远显示"有"。
	grants, err := db.UserGrants(ctx, boss.ID, "user")
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range grants {
		if !g.Effective {
			t.Errorf("编辑页把 %s 显示成了没有", g.Key)
		}
	}
}

// 已经跑了几个月的站点升级时，站主归最早的那个管理员账号。
// 不预置的话，升级后的第一个注册者会凭空拿到最高权限。
func TestOwnerMigrationSeedsTheEarliestAdmin(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	veteran := mustUser(t, db, "veteran")
	if err := db.SetUserRole(ctx, veteran.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	mustUser(t, db, "rookie")

	// 重放这条迁移：把表删掉、抹掉它的记账行，假装这个库是从 0016 升上来的。
	if _, err := db.SQL.ExecContext(ctx, `DROP TABLE site_owner`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.ExecContext(ctx,
		`DELETE FROM schema_migrations WHERE name='0017_site_owner.sql'`); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if id, _ := db.OwnerID(ctx); id != veteran.ID {
		t.Fatalf("升级后站主 = %d, want %d（最早的管理员）", id, veteran.ID)
	}
}

func mustPermissions(t *testing.T, db *DB, ctx context.Context) []Permission {
	t.Helper()
	perms, err := db.ListPermissions(ctx)
	if err != nil || len(perms) == 0 {
		t.Fatalf("permissions = %d (%v)", len(perms), err)
	}
	return perms
}
