package store

import (
	"context"
	"strings"
	"testing"
)

func TestParseThemeKeepsOnlyKnownKeysAndHex(t *testing.T) {
	// 这一串里一半是攻击面：CSS 上下文里的 `}`、`;`、函数式写法、未知键名。解析器的职责就是
	// 让它们掉出去，而不是让它们把页面样式改写掉。
	raw := `mode=dark;fg=#16181D;bg=#fff;muted=#6a7280;color=red;evil=;expression(alert(1));` +
		`line=red;code=%23abc;accent=#7fb0ff;ok=;bad=#1122334;warn=#000;mode=neon`

	got := ParseTheme(raw)
	if got.Mode != "dark" {
		t.Errorf("mode = %q，应该只认到第一个合法值（neon 不在名单里）", got.Mode)
	}
	for key, want := range map[string]string{
		"fg": "#16181d", "bg": "#ffffff", "accent": "#7fb0ff", "warn": "#000000",
	} {
		if got.Colors[key] != want {
			t.Errorf("%s = %q，应该是 %q", key, got.Colors[key], want)
		}
	}
	for _, key := range []string{"color", "evil", "line", "code", "ok", "bad"} {
		if v, seen := got.Colors[key]; seen {
			t.Errorf("%s = %q 这种值不该被收下来", key, v)
		}
	}
	// muted 是合法键 + 合法值，不该被上面那堆脏东西连坐丢掉。
	if got.Colors["muted"] != "#6a7280" {
		t.Errorf("muted = %q，合法值被丢了", got.Colors["muted"])
	}
	// 渲染出来的那串里不能出现没有配过的键，也不能出现任何非白名单的东西。
	style := got.Style()
	if strings.ContainsAny(style, "%{}()\"'\\") {
		t.Fatalf("Style 里出现了 CSS 上下文的危险字符：%q", style)
	}
	for _, key := range ThemeKeys {
		if _, want := got.Colors[key]; !want && strings.Contains(style, "--"+key) {
			t.Errorf("Style 带了没配过的键 %s：%q", key, style)
		}
	}
	if !strings.HasPrefix(style, "--") {
		t.Errorf("Style 不像自定义属性串：%q", style)
	}
}

func TestThemeRoundTripIsStable(t *testing.T) {
	var th Theme
	th.Set("mode", "light")
	th.Set("fg", "#ABCDEF")
	th.Set("bg", "#111")
	th.Set("not-a-key", "#ff0000")
	th.Set("accent", "red")

	raw := th.String()
	if raw != "mode=light;fg=#abcdef;bg=#111111" {
		t.Fatalf("String = %q", raw)
	}
	again := ParseTheme(raw)
	if again.String() != raw || again.Style() != th.Style() {
		t.Fatalf("读回来不一样：%q vs %q", again.String(), raw)
	}
	if th.Style() != "--fg:#abcdef;--bg:#111111" {
		t.Fatalf("Style = %q，应该只带调过的键、且按固定顺序", th.Style())
	}

	// 清空一个键：设成空串就是"跟着默认走"。
	th.Set("fg", "")
	if th.String() != "mode=light;bg=#111111" {
		t.Fatalf("清空 fg 之后 = %q", th.String())
	}
	empty := Theme{}
	if empty.Style() != "" || !empty.IsEmpty() {
		t.Error("空的 Theme 应该既没有 style 也算空")
	}
	// mode=空 就是把明暗底色交还给系统。
	var back Theme
	back.Set("mode", "")
	if !back.IsEmpty() {
		t.Errorf("mode 设回空之后还不算空：%q", back.String())
	}
}

func TestUpdateThemeReadsBackWithTheUserRow(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	u, err := db.EnsureUser(ctx, "palette", "user")
	if err != nil {
		t.Fatal(err)
	}
	if u.Theme != "" {
		t.Fatalf("新账号的配色 = %q，应该是空", u.Theme)
	}
	var th Theme
	th.Set("mode", "dark")
	th.Set("accent", "#ff8800")
	if err := db.UpdateTheme(ctx, u.ID, th); err != nil {
		t.Fatal(err)
	}
	// 配色的全部意义在这里：它随 authenticate 那一次读用户一起出来，渲染不再查第二遍。
	got, err := db.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Theme != th.String() {
		t.Fatalf("读回来的配色 = %q，应该是 %q", got.Theme, th.String())
	}
	if ParseTheme(got.Theme).Style() != "--accent:#ff8800" {
		t.Fatalf("渲染串 = %q", ParseTheme(got.Theme).Style())
	}

	// UpdateTheme 自己也要挡一遍：调用方拼错了不能把脏值写进库。
	var dirty Theme
	dirty.Colors = map[string]string{"fg": "}body{background:url(evil)"}
	if err := db.UpdateTheme(ctx, u.ID, dirty); err != nil {
		t.Fatal(err)
	}
	got, err = db.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Theme != "" {
		t.Fatalf("脏值被写进了库：%q", got.Theme)
	}
}

// 这两条是给"速度"那个要求留的证据：配色在每个页面都要解析一次，所以它得能被量出来。
// 九个键的解析与拼接是纳秒级，而它换来的是"不再多查一次库"（见 api 包那条查询计数用例）。
func BenchmarkParseTheme(b *testing.B) {
	const raw = "mode=dark;fg=#e6e8ee;muted=#98a1b3;line=#2a2f3a;bg=#12151b;code=#1a1e26;accent=#7fb0ff;ok=#6dd58c;bad=#ff8a80;warn=#e6c15c"
	b.ReportAllocs()
	for b.Loop() {
		if got := ParseTheme(raw); len(got.Colors) != 9 || got.Mode != "dark" {
			b.Fatalf("解析出来的东西不对：%q", got.String())
		}
	}
}

func BenchmarkThemeStyle(b *testing.B) {
	t := ParseTheme("mode=dark;fg=#e6e8ee;muted=#98a1b3;line=#2a2f3a;bg=#12151b;code=#1a1e26;accent=#7fb0ff;ok=#6dd58c;bad=#ff8a80;warn=#e6c15c")
	b.ReportAllocs()
	for b.Loop() {
		if s := t.Style(); len(s) == 0 {
			b.Fatal("空串")
		}
	}
}
