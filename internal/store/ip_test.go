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
	if _, err := db.BanIP(ctx, "192.168.1.50", "", op.ID); err != nil {
		t.Errorf("校园内网地址应当可以封：%v", err)
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
