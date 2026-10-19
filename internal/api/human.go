package api

import (
	"context"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/liangbinchen2013/fast-oj/internal/store"
)

// human.go 是「伪人机验证」的服务端那一半。
//
// 说清楚它拦得住什么、拦不住什么：页面上那个转圈和复选框都是观感，浏览器说自己等了五秒
// 没有任何效力；有效力的只有 internal/store/verify.go 里那一行 ready_at——令牌由渲染表单
// 那一次发出，绑死来源地址和用途，时间没到就核销不掉。脚本要绕过这道门，唯一的办法是
// 同样等那么久，所以它的成本是"时间"，不是"识别"。真正掐住刷号的仍然是那两条限流
// （每分钟几个、每小时几个），这个门只是让每次动作都先付出几秒。
//
// 为什么按 IP 而不是按账号：这两条路上都还没有账号。

const (
	// 注册第一次不弹：这个地址本小时还没有成功注册的记录，就直接放行。第二次起必弹。
	registerVerifyFrom = 1
	// 登录要失败攒到 3 次才弹。前两三次打错密码是学生日常，先怀疑人是机器只会让人在
	// 门口多等，而那条真正的锁（maxLoginFailures）照旧在 10 次处生效。
	loginVerifyFrom = 3

	// 等待秒数是 1..span，两条曲线的 span 都设计成"第一次触发时 1..5 秒，每多一次动作
	// 上限 +1 秒"：注册第 2 次 1..5、第 3 次 1..6；登录第 4 次 1..5、第 5 次 1..6。
	humanVerifyFloor = 1
)

// humanGate 是渲染给表单的那件事：要等，就给出令牌和还剩几秒。
type humanGate struct {
	Token string
	// Seconds 是倒计时给人看的秒数。复用旧窗口时可能比这次抽出来的短，也可能正好走完
	// ——走完就是 0，页面直接把复选框给出来。
	Seconds int
}

// humanSpan 报告这个来源地址现在要不要弹验证窗，以及等待上限（秒，实际是 1..span）。
// 不需要弹的时候 span 是 0。
func (s *Server) humanSpan(ctx context.Context, kind, ip string) (int, error) {
	if s.cfg.HumanVerifyMax <= 0 {
		// 上限设成 0 就是整套关掉：全班共用一个出口 IP 的那天，这是最快的降压阀，
		// 关掉之后剩下的还是原来那两条限流，不是没有防护。
		return 0, nil
	}
	switch kind {
	case store.VerifyRegister:
		hits, err := s.db.RateHits(ctx, "register:hour:"+ip, registerWindow)
		if err != nil {
			return 0, err
		}
		if hits < registerVerifyFrom {
			return 0, nil
		}
		return s.capSpan(hits + 4), nil
	case store.VerifyLogin:
		hits, err := s.db.RateHits(ctx, "login:"+ip, loginWindow)
		if err != nil {
			return 0, err
		}
		if hits < loginVerifyFrom {
			return 0, nil
		}
		return s.capSpan(hits + 2), nil
	}
	return 0, nil
}

// capSpan 把涨上去的等待收进 operator 给的上限里。频繁度只该让等待变长，不该让页面
// 变成一分钟的白屏——那会让人以为站点坏了。
func (s *Server) capSpan(span int) int {
	if span > s.cfg.HumanVerifyMax {
		return s.cfg.HumanVerifyMax
	}
	if span < humanVerifyFloor {
		return humanVerifyFloor
	}
	return span
}

// humanGateFor 决定这一页带不带验证窗。
//
// 一个地址在一个用途上只允许有一个正在计时的窗口，渲染时按这个顺序找：
// 表单里已经带着的令牌（presented）还挂着就沿用它；否则看这个地址还有没有别的窗口在计时
// （两个标签页来回切、或者有人拿废令牌来试，都不该把老实等待的那个人顶掉重等）；都没有才
// 发新的。少了这一步，"手快点了一次提交"的代价就变成重新等满一遍。
func (s *Server) humanGateFor(ctx context.Context, kind, ip, presented string) (*humanGate, error) {
	span, err := s.humanSpan(ctx, kind, ip)
	if err != nil || span == 0 {
		return nil, err
	}
	var (
		readyAt int64
		token   string
	)
	if presented != "" {
		at, pending, err := s.db.VerifyState(ctx, presented, kind, ip)
		if err != nil {
			return nil, err
		}
		if pending {
			token, readyAt = presented, at
		}
	}
	if token == "" {
		var pending bool
		token, readyAt, pending, err = s.db.PendingVerifyChallenge(ctx, kind, ip)
		if err != nil {
			return nil, err
		}
		if !pending {
			sec := humanVerifyFloor + rand.IntN(span)
			token, readyAt, err = s.db.NewVerifyChallenge(ctx, kind, ip, time.Duration(sec)*time.Second)
			if err != nil {
				return nil, err
			}
		}
	}
	left := readyAt - time.Now().UnixMilli()
	if left < 0 {
		left = 0
	}
	return &humanGate{Token: token, Seconds: int((left + 999) / 1000)}, nil
}

// humanAllowed 核销表单带回来的令牌。放行与否之外不返回原因：这一条路上失败的原因只有
// "没等够"和"令牌不是你的/已经用过"两种，而对提交者来说该看到的都是同一句话——把区别
// 说清楚对合法用户没有用，对脚本却是免费的探测反馈。
func (s *Server) humanAllowed(r *http.Request, kind string) (bool, error) {
	span, err := s.humanSpan(r.Context(), kind, clientIP(r))
	if err != nil {
		return false, err
	}
	if span == 0 {
		return true, nil
	}
	ok, err := s.db.SpendVerifyChallenge(r.Context(), r.FormValue("verify"), kind, clientIP(r))
	if err != nil {
		return false, err
	}
	return ok, nil
}
