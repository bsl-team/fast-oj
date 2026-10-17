package api

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/liangbinchen2013/fast-oj/internal/config"
	"github.com/liangbinchen2013/fast-oj/internal/store"
)

// 注册限流：一台机器每分钟最多 -register-per-minute 个账号（默认 1），按 IP 和浏览器
// 设备号两把键一起算。
//
// 网卡 MAC 地址不在服务端能拿到的范围里：它只存在于同一个二层网络，请求一过路由器就被
// 换成路由器的地址，HTTP 里从头到尾没有它。所以"这台机器"用的是注册页生成、存在
// localStorage 里的随机号；清掉站点数据能换一个新的号，这正是 IP 那条键留着的原因。

const goodDevice = "k7x2m9q4t1w6e8r0p3s5v7b9n2m4q6k8"

func (h *harness) register(handle, email, device string) (int, string, http.Header) {
	h.t.Helper()
	return h.postNoRedirect("/register", url.Values{
		"handle": {handle}, "email": {email}, "password": {testPassword}, "device": {device},
	}, "")
}

func TestRegistrationRateLimitPerMinute(t *testing.T) {
	h := newHarnessCfg(t, func(c *config.Config) { c.RegisterPerMinute = 1 })

	code, _, hdr := h.register("first", "first@example.com", goodDevice)
	if code != http.StatusSeeOther {
		t.Fatalf("第一条注册 = %d", code)
	}
	// Location 里的 msg 是 URL 转义过的，要先解出来再看内容。
	query, _ := url.ParseQuery(strings.TrimPrefix(hdr.Get("Location"), "/?"))
	if !strings.Contains(query.Get("msg"), "欢迎，first") {
		t.Fatalf("第一条注册没有落到欢迎页：%q", hdr.Get("Location"))
	}

	// 换一个设备号、换一个邮箱都过不去：这个 IP 的一分钟额度已经用掉了。
	code, body, _ := h.register("second", "second@example.com", "z1a2b3c4d5e6f7g8h9i0j1k2l3m4n5o6")
	if code != http.StatusTooManyRequests {
		t.Fatalf("第二条注册 = %d, want 429:\n%s", code, short(body))
	}
	if !strings.Contains(body, "一分钟内只能注册 1 个账号") {
		t.Errorf("限流没给出原因：\n%s", short(body))
	}
	// 限流不该把已经填的东西丢掉。
	if !strings.Contains(body, `value="second"`) || !strings.Contains(body, `value="second@example.com"`) {
		t.Errorf("被限流的那次把表单内容丢了：\n%s", short(body))
	}
	// 而且没有偷偷建号。
	if _, err := h.db.GetUserByHandle(ctx(), "second"); err != store.ErrNotFound {
		t.Errorf("被限流的注册还是建了账号：%v", err)
	}
}

// 设备号来自客户端，什么都能编：格式不对就当没有——不给它建限流桶，也不影响别的判断。
// 否则一个攻击者只要每次都换一个假号，就能把桶名撑爆，或者反过来用别人的号替别人挡注册。
func TestRegistrationDeviceTokenIsNotTrusted(t *testing.T) {
	h := newHarnessCfg(t, func(c *config.Config) { c.RegisterPerMinute = 10 })

	bad := []string{"", "short", "../../etc/passwd", strings.Repeat("a", 200), "has space!", "带中文的号", "ok12_-"}
	for i, tok := range bad {
		handle := fmt.Sprintf("nodev%d", i)
		code, body, _ := h.register(handle, handle+"@example.com", tok)
		if code != http.StatusSeeOther {
			t.Fatalf("注册 %s（设备号 %q）= %d:\n%s", handle, short(tok), code, short(body))
		}
	}
	if n := h.countBuckets(`register:dev:%`); n != 0 {
		t.Errorf("假设备号也建了限流桶：n=%d", n)
	}
	// 合格的号才建桶。
	_, _, _ = h.register("withdev", "withdev@example.com", goodDevice)
	if n := h.countBuckets(`register:dev:%`); n != 1 {
		t.Errorf("合格的设备号该建一个桶，n=%d", n)
	}
	// 每次注册都按 IP 记账，这条不会因为有设备号就少记。
	if n := h.countBuckets(`register:ip:%`); n != 1 {
		t.Errorf("IP 桶该只有一个（同一个地址），n=%d", n)
	}
}

func (h *harness) countBuckets(like string) int {
	h.t.Helper()
	var n int
	if err := h.db.SQL.QueryRowContext(ctx(), `SELECT COUNT(*) FROM login_attempts WHERE key LIKE ?`, like).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}
