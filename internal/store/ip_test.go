package store

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
)

func TestIPBanRoundTrip(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	// created_by 是真外键：封人的那个人得是个存在的账号（页面上要显示"谁封的"）。
	op := mustUser(t, db, "operator")

	b, err := db.BanIP(ctx, "203.0.113.7", "脚本刷注册", op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if b.IP != "203.0.113.7" || b.Note != "脚本刷注册" {
		t.Fatalf("ban = %+v", b)
	}
	if b.CreatorTag != "operator" {
		t.Errorf("谁封的要显示出来，creator = %q", b.CreatorTag)
	}
	// 再封一次同一个地址是刷新，不是报错也不是第二条。
	if _, err := db.BanIP(ctx, " 203.0.113.7 ", "换了一条原因", op.ID); err != nil {
		t.Fatal(err)
	}
	list, err := db.ListIPBans(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Note != "换了一条原因" {
		t.Fatalf("list = %+v", list)
	}

	removed, err := db.UnbanIP(ctx, "203.0.113.7")
	if err != nil || !removed {
		t.Fatalf("unban = %v %v", removed, err)
	}
	if again, err := db.UnbanIP(ctx, "203.0.113.7"); err != nil || again {
		t.Errorf("重复解封 = %v %v, want false 且不算错", again, err)
	}
	if _, err := db.BanIP(ctx, "example.com", "", op.ID); !errors.Is(err, ErrNotAnAddress) {
		t.Errorf("域名 = %v, want ErrNotAnAddress", err)
	}
	if _, err := db.BanIP(ctx, "127.0.0.1", "", op.ID); !errors.Is(err, ErrLoopbackBanned) {
		t.Errorf("环回 = %v, want ErrLoopbackBanned", err)
	}
	// 封全世界那一条挡在写库前：按下去的人自己也一起被 403，而这一页之后再也打不开，
	// 没有任何人 able 再解封它。
	for _, selfLockout := range []string{"0.0.0.0/0", "::/0"} {
		if _, err := db.BanIP(ctx, selfLockout, "", op.ID); !errors.Is(err, ErrBanEverything) {
			t.Errorf("%s = %v, want ErrBanEverything", selfLockout, err)
		}
	}
	if _, err := db.BanIP(ctx, "192.168.1.50", "", op.ID); err != nil {
		t.Errorf("校园内网地址应当可以封：%v", err)
	}
}

// 历史里躺着一条禁令不让封的规则时（这条禁令是后加的），列表要还能把它读出来、删掉它。
// 如果读与删也走禁令，那条记录就成了永远拆不掉的锁——封禁功能自己把钥匙吞了。
func TestLegacyBanStaysListableAndRemovable(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	op := mustUser(t, db, "operator")

	for _, raw := range []string{"0.0.0.0/0", "127.0.0.1"} {
		if _, err := db.SQL.ExecContext(ctx,
			`INSERT INTO ip_bans (ip, note, created_at, created_by) VALUES (?, '升级前留下的', 1, ?)`,
			raw, op.ID); err != nil {
			t.Fatal(err)
		}
	}
	list, err := db.ListIPBans(ctx)
	if err != nil {
		t.Fatalf("旧记录让列表报错了：%v", err)
	}
	if len(list) != 2 {
		t.Fatalf("列表 = %+v，要两条", list)
	}
	if !list[0].Contains(netip.MustParseAddr("203.0.113.9")) {
		t.Error("0.0.0.0/0 那条仍然要能匹配，别因为加了禁令就让旧规则变成哑弹")
	}
	for _, raw := range []string{"0.0.0.0/0", "127.0.0.1"} {
		removed, err := db.UnbanIP(ctx, raw)
		if err != nil || !removed {
			t.Errorf("解封 %s = %v %v，旧记录必须删得掉", raw, removed, err)
		}
	}
}

func TestIPBanMatching(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	if _, err := db.BanIP(ctx, "203.0.113.0/24", "机房", mustUser(t, db, "op2").ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.BanIP(ctx, "2001:db8::42", "v6 单地址", mustUser(t, db, "op2").ID); err != nil {
		t.Fatal(err)
	}
	list, err := db.ListIPBans(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{
		"203.0.113.9":   true,
		"203.0.113.255": true,
		"203.0.114.1":   false,
		"2001:db8::42":  true,
		"2001:db8::43":  false,
	}
	for ip, want := range cases {
		var got bool
		for _, b := range list {
			if b.Contains(parseAddr(t, ip)) {
				got = true
			}
		}
		if got != want {
			t.Errorf("%s 命中 = %v, want %v", ip, got, want)
		}
	}
}

// 封禁存的是"这一分钟谁来了"之外的另一件事：真实访客出现过没有。
// 全是环回地址时这个位不该被点亮，否则 IP 页会在一个只会自伤的场景里亮着。
func TestPublicClientGate(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	window := int64(24 * 3600 * 1000)

	if on, err := db.HasPublicClient(ctx, window); err != nil || on {
		t.Fatalf("全新库该是关的：%v %v", on, err)
	}
	if err := db.MarkPublicClientSeen(ctx); err != nil {
		t.Fatal(err)
	}
	if on, err := db.HasPublicClient(ctx, window); err != nil || !on {
		t.Fatalf("见过真实访客之后该是开的：%v %v", on, err)
	}
	// 窗口过一天就自己收起来。
	if on, err := db.HasPublicClient(ctx, -1); err != nil || on {
		t.Errorf("超出窗口还开着：%v %v", on, err)
	}
	// 这一位不是站点设置，不该混进设置页读出来的那些键里。
	site, err := db.Site(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(site.Title, "public") || !site.AllowRegister {
		t.Errorf("站点设置被这一位污染了： %+v", site)
	}
}

func parseAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	a, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("bad test address %q: %v", s, err)
	}
	return a.Unmap()
}
