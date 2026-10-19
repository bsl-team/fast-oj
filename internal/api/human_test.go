package api

import (
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/liangbinchen2013/fast-oj/internal/config"
	"github.com/liangbinchen2013/fast-oj/internal/store"
)

// 伪人机验证：观感在页面上，强制的是服务端那段时间。这几条用例盯的是后者——
// 弹不弹按这个地址的频繁度，等没等够由数据库里的 ready_at 说话，浏览器怎么说都不算。

var verifyTokenRe = regexp.MustCompile(`name="verify" value="([^"]+)"`)
var verifySecondsRe = regexp.MustCompile(`data-seconds="(\d+)"`)

// humanToken 取出这一页发下来的令牌和它要求的秒数；页面没有那个窗时 ok 为 false。
func humanToken(body string) (string, int, bool) {
	m := verifyTokenRe.FindStringSubmatch(body)
	if m == nil {
		return "", 0, false
	}
	s := verifySecondsRe.FindStringSubmatch(body)
	if s == nil {
		return m[1], 0, true
	}
	n, _ := strconv.Atoi(s[1])
	return m[1], n, true
}

func TestHumanVerifyRegisterWaitsFromSecondAccount(t *testing.T) {
	// 上限设成 1 秒：等待区间收成"必定 1 秒"，用例要等的就是确定的 1 秒而不是随机几秒。
	h := newHarnessCfg(t, func(c *config.Config) {
		c.HumanVerifyMax = 1
		c.RegisterPerMinute = 50
	})

	// 第一次：这个地址本小时还没有成功注册，页面不该有那个窗。
	_, body := h.htmlGet("/register", "")
	if _, _, present := humanToken(body); present {
		t.Fatalf("第一条注册就弹了验证窗：\n%s", short(body))
	}
	if code, _, _ := h.register("alice", "alice@example.com", goodDevice); code != http.StatusSeeOther {
		t.Fatalf("第一条注册 = %d", code)
	}

	// 第二条起必弹，而且表单里带着服务端发的令牌。
	_, body = h.htmlGet("/register", "")
	token, seconds, present := humanToken(body)
	if !present {
		t.Fatalf("第二条注册没有验证窗：\n%s", short(body))
	}
	if seconds != 1 {
		t.Fatalf("等待秒数 = %d，上限是 1", seconds)
	}

	// 没等够就提交：拒绝，而且不许偷偷建号。
	values := url.Values{
		"handle": {"bob"}, "email": {"bob@example.com"}, "password": {testPassword},
		"device": {goodDevice}, "verify": {token},
	}
	code, rejected, _ := h.postNoRedirect("/register", values, "")
	if code != http.StatusForbidden {
		t.Fatalf("没等够就注册成功 = %d:\n%s", code, short(rejected))
	}
	if !strings.Contains(rejected, "请先完成页面里的验证") {
		t.Errorf("被拒的那一页没说为什么要拒绝：\n%s", short(rejected))
	}
	if _, err := h.db.GetUserByHandle(ctx(), "bob"); err != store.ErrNotFound {
		t.Errorf("被验证挡住的注册还是建了账号：%v", err)
	}
	// 被打回来重填不该让人重新等一遍：沿用同一个令牌，秒数只会更少。
	if again, still, _ := humanToken(rejected); again != token {
		t.Errorf("重填的那一页换了令牌（%q 而不是 %q），等于让提交者重新等满", again, token)
	} else if still > seconds {
		t.Errorf("重填的那一页等待变长了：%d > %d", still, seconds)
	}

	// 等够了，同一个令牌就能用。
	time.Sleep(1100 * time.Millisecond)
	code, okay, hdr := h.postNoRedirect("/register", values, "")
	if code != http.StatusSeeOther {
		t.Fatalf("等够时间后注册 = %d:\n%s", code, short(okay))
	}
	if !strings.Contains(hdr.Get("Location"), "msg=") {
		t.Fatalf("等够时间后没有落到带欢迎语的首页：%q", hdr.Get("Location"))
	}
	if q, _ := url.ParseQuery(strings.TrimPrefix(hdr.Get("Location"), "/?")); !strings.Contains(q.Get("msg"), "欢迎，bob") {
		t.Errorf("注册没有落到欢迎页：%q", hdr.Get("Location"))
	}

	// 令牌是一次性的：拿刚用过的那个再来一次，必须算没过。
	code, _, _ = h.postNoRedirect("/register", url.Values{
		"handle": {"carol"}, "email": {"carol@example.com"}, "password": {testPassword},
		"device": {goodDevice}, "verify": {token},
	}, "")
	if code != http.StatusForbidden {
		t.Fatalf("用过的令牌还能再核销一次 = %d", code)
	}
	if _, err := h.db.GetUserByHandle(ctx(), "carol"); err != store.ErrNotFound {
		t.Errorf("第二次用同一个令牌也建了号：%v", err)
	}
}

func TestHumanVerifyRegisterWaitsLongerAsItGoes(t *testing.T) {
	// 上限放到 8 秒，才看得出它在按频繁度往上长：第 2 次注册是 1..5，第 3 次 1..6。
	h := newHarnessCfg(t, func(c *config.Config) {
		c.HumanVerifyMax = 8
		c.RegisterPerMinute = 50
	})
	if code, _, _ := h.register("dave", "dave@example.com", goodDevice); code != http.StatusSeeOther {
		t.Fatalf("第一条注册 = %d", code)
	}
	_, body := h.htmlGet("/register", "")
	_, first, present := humanToken(body)
	if !present || first < 1 || first > 5 {
		t.Fatalf("第二条注册的等待秒数 = %d（窗存在 %v），应该在 1..5", first, present)
	}

	// 让第二条号真正注册成功，计数才会往前走（被拒的那些不算）。
	time.Sleep(time.Duration(first) * time.Second)
	token, _, _ := humanToken(body)
	code, out, _ := h.postNoRedirect("/register", url.Values{
		"handle": {"erin"}, "email": {"erin@example.com"}, "password": {testPassword},
		"device": {"z1a2b3c4d5e6f7g8h9i0j1k2l3m4n5o6"}, "verify": {token},
	}, "")
	if code != http.StatusSeeOther {
		t.Fatalf("第二条注册 = %d:\n%s", code, short(out))
	}
	_, body = h.htmlGet("/register", "")
	_, second, present := humanToken(body)
	if !present {
		t.Fatalf("第三条注册没有验证窗：\n%s", short(body))
	}
	if second < 1 || second > 6 {
		t.Fatalf("第三条注册的等待秒数 = %d，应该在 1..6（比上一条的上限多 1）", second)
	}
}

func TestHumanVerifyLoginStartsAtThreeFailures(t *testing.T) {
	h := newHarnessCfg(t, func(c *config.Config) { c.HumanVerifyMax = 1 })
	if _, err := h.db.CreateAccount(ctx(), store.AccountInput{
		Handle: "target2", Email: "target2@example.com", Password: testPassword,
	}); err != nil {
		t.Fatal(err)
	}

	// 打错两次密码还不该有窗：那是学生日常，不是机器。
	for i := 1; i <= 2; i++ {
		_, body := h.htmlGet("/login", "")
		if _, _, present := humanToken(body); present {
			t.Fatalf("第 %d 次失败之后就弹窗了：\n%s", i, short(body))
		}
		if _, out := h.formPage("/login", url.Values{"identifier": {"target2"}, "password": {"wrong-password-here"}}); !strings.Contains(out, "账号或密码不正确") {
			t.Fatalf("第 %d 次失败没有给出正常报错：\n%s", i, short(out))
		}
	}
	// 第三次失败才是门槛：攒到 3 次起，这一页就要先验证。
	if _, out := h.formPage("/login", url.Values{"identifier": {"target2"}, "password": {"wrong-password-here"}}); !strings.Contains(out, "账号或密码不正确") {
		t.Fatalf("第三次失败没有给出正常报错：\n%s", short(out))
	}
	_, body := h.htmlGet("/login", "")
	token, seconds, present := humanToken(body)
	if !present {
		t.Fatalf("三次失败之后登录页还是没有窗：\n%s", short(body))
	}
	if seconds != 1 {
		t.Fatalf("登录的等待秒数 = %d，上限是 1", seconds)
	}

	// 没等够就提交：密码是对的也不放行，而且这一次不算密码错误（不该把人推向那条真锁）。
	if code, out, _ := h.postNoRedirect("/login", url.Values{
		"identifier": {"target2"}, "password": {testPassword}, "verify": {"not-a-real-token"},
	}, ""); code != http.StatusForbidden || !strings.Contains(out, "请先完成页面里的验证") {
		t.Fatalf("假令牌登录 = %d:\n%s", code, short(out))
	}
	hits, err := h.db.RateHits(ctx(), "login:127.0.0.1", loginWindow)
	if err != nil {
		t.Fatal(err)
	}
	if hits != 3 {
		t.Fatalf("被验证挡住的登录也算了一次密码错误：失败次数 = %d，应该还是 3", hits)
	}

	time.Sleep(1100 * time.Millisecond)
	code, _, hdr := h.postNoRedirect("/login", url.Values{
		"identifier": {"target2"}, "password": {testPassword}, "verify": {token},
	}, "")
	if code != http.StatusSeeOther || cookieValue(hdr.Values("Set-Cookie"), "fastoj_session") == "" {
		t.Fatalf("等够时间后登录 = %d %v", code, hdr.Values("Set-Cookie"))
	}
	// 登录成功把那串失败清零：下一次登录页不该再有窗。
	if _, body := h.htmlGet("/login", ""); len(body) > 0 {
		if _, _, present := humanToken(body); present {
			t.Errorf("登录成功后登录页还带着验证窗：\n%s", short(body))
		}
	}
}

func TestHumanVerifyDisabledMeansJustThrottling(t *testing.T) {
	h := newHarnessCfg(t, func(c *config.Config) {
		c.HumanVerifyMax = 0
		c.RegisterPerMinute = 50
	})
	_, page := h.htmlGet("/register", "")
	if _, _, present := humanToken(page); present {
		t.Fatalf("关掉整套之后注册页还有验证窗：\n%s", short(page))
	}
	for i, name := range []string{"frank", "grace"} {
		code, out, _ := h.register(name, name+"@example.com", goodDevice)
		if code != http.StatusSeeOther {
			t.Fatalf("第 %d 条注册 = %d:\n%s", i+1, code, short(out))
		}
	}
	if n := h.userCount(); n != 3 {
		t.Errorf("注册了两个账号之后用户数 = %d，应该是 3（含 claim 那个管理员）", n)
	}
}
