package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// verify.go 是「伪人机验证」里唯一真的那部分。页面上转的圈和那个复选框都不作数——
// 浏览器说自己已经等了 5 秒，服务端没有任何理由相信；真正让脚本多花成本的是记在这里的
// ready_at：令牌由渲染表单那一次发出来，绑死来源地址和用途，时间没到就核销不掉。
// 脚本要绕过它，只能同样等那么久。

// 用途。注册和登录两条路的"这个人最近有多少次动作"来源不同（一个是成功注册次数，
// 一个是失败次数），所以令牌不能串用：拿登录页领的令牌去交注册表单不算数。
const (
	VerifyRegister = "register"
	VerifyLogin    = "login"
)

// verifyChallengeTTL 是领来不用的窗口的寿命。十分钟是给"页面开着去倒了杯水"的人留的；
// 再长就等于给未来的提交预留额度——一个地址把令牌攒着，哪天想注册就随时能注册。
const verifyChallengeTTL = 10 * time.Minute

// NewVerifyChallenge 给这个来源地址发一个"要等到 readyAt 之后才被放行"的窗口，返回令牌
// 和那个时刻（毫秒）。
//
// 同一 kind+ip 先删旧再写新：这张表是匿名请求能写东西的少数几张表之一，刷新一次注册页
// 就多一行。按来源地址收成一条待核销窗口，表的大小就被"同时在看表单的地址数"限制住，
// 而不是被"刷页的次数"限制住。
func (db *DB) NewVerifyChallenge(ctx context.Context, kind, ip string, wait time.Duration) (string, int64, error) {
	token, err := NewSecret(24)
	if err != nil {
		return "", 0, err
	}
	now := nowMS()
	ready := now + wait.Milliseconds()
	if _, err := db.SQL.ExecContext(ctx,
		`DELETE FROM verify_challenges WHERE kind=? AND ip=?`, kind, ip); err != nil {
		return "", 0, err
	}
	if _, err := db.SQL.ExecContext(ctx,
		`INSERT INTO verify_challenges (token, kind, ip, ready_at, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		token, kind, ip, ready, now+verifyChallengeTTL.Milliseconds(), now); err != nil {
		return "", 0, err
	}
	return token, ready, nil
}

// SpendVerifyChallenge 核销一个令牌：来源地址对、用途对、没过期、等待时间已经走完，
// 而且**那一下复选框真的点过**，五条同时成立才算过，并且用过一次就作废。
//
// 判断写在一条 DELETE 的 WHERE 里、靠 RowsAffected 定成败，而不是"先查再改"：并发提交同
// 一个令牌时两次请求都读到了"可用"，但只有一次删得掉那一行。用 `used` 标记反而要处理
// "标记写成功但请求失败"的中间态。
//
// 过期清理挂在这条路上：这里是提交，本来就被限流和核销管着；渲染表单那一次是无条件的，
// 不能拿它当清理的触发器。
func (db *DB) SpendVerifyChallenge(ctx context.Context, token, kind, ip string) (bool, error) {
	if token == "" {
		return false, nil
	}
	now := nowMS()
	res, err := db.SQL.ExecContext(ctx,
		`DELETE FROM verify_challenges
		 WHERE token=? AND kind=? AND ip=? AND ready_at<=? AND expires_at>? AND verified=1`,
		token, kind, ip, now, now)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if _, err := db.SQL.ExecContext(ctx,
		`DELETE FROM verify_challenges WHERE expires_at<=?`, now); err != nil {
		// 清不掉过期行不影响核销的结论，也不该把一次正常的登录判成失败。
		return n == 1, nil
	}
	return n == 1, nil
}

// MarkVerifyClick 把"复选框点过了"盖在窗口上：**只有等待时间已经走完**才盖得上。
//
// 这一条是 0029 存在的原因：0028 之后脚本睡够秒数就能直接交表单，复选框是纯装饰。现在
// 勾一下就是一次独立的 HTTP 请求（还要长得像浏览器，见 internal/api/human.go），提交那一步
// 只认盖过章的令牌。它仍能被刻意模仿——一次 POST 而已——但模仿不了 ready_at 那段已经过去的时间，
// 两道叠起来才是这道门的实际内容。
func (db *DB) MarkVerifyClick(ctx context.Context, token, kind, ip string) (bool, error) {
	if token == "" {
		return false, nil
	}
	now := nowMS()
	res, err := db.SQL.ExecContext(ctx,
		`UPDATE verify_challenges SET verified=1
		 WHERE token=? AND kind=? AND ip=? AND ready_at<=? AND expires_at>? AND verified=0`,
		token, kind, ip, now, now)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// VerifyState 看一眼这个令牌还挂着没有，返回它要求的时刻（毫秒）和有没有被点过。
//
// 存在的理由是不让人白等：提交早了被打回去重填时，表单要能把原来那个等待接着用，而不是
// 每次失败都发一个新的、从此刻重新计时的窗口——那样多点几次提交就永远等不完。
func (db *DB) VerifyState(ctx context.Context, token, kind, ip string) (int64, bool, bool, error) {
	if token == "" {
		return 0, false, false, nil
	}
	var (
		readyAt  int64
		verified int
	)
	err := db.SQL.QueryRowContext(ctx,
		`SELECT ready_at, verified FROM verify_challenges WHERE token=? AND kind=? AND ip=? AND expires_at>?`,
		token, kind, ip, nowMS()).Scan(&readyAt, &verified)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, false, nil
	}
	if err != nil {
		return 0, false, false, err
	}
	return readyAt, verified == 1, true, nil
}

// PendingVerifyChallenge 返回这个地址在这一用途上还没走完的那个窗口（如果有），连带
// 它要求的时刻和有没有被点过。
//
// 有了它，"同一个地址同一个用途只留一行"才不只是省地方：渲染表单时如果已经有窗口在计时，
// 就接着用它。否则两个标签页来回切、或者有人拿废令牌试一次，都会把正在计时的窗口顶掉，
// 等于让老实等待的人重新等一遍——那正好是这张表最不该造成的事。
func (db *DB) PendingVerifyChallenge(ctx context.Context, kind, ip string) (string, int64, bool, bool, error) {
	var (
		token    string
		readyAt  int64
		verified int
	)
	err := db.SQL.QueryRowContext(ctx,
		`SELECT token, ready_at, verified FROM verify_challenges
		 WHERE kind=? AND ip=? AND expires_at>? ORDER BY ready_at DESC LIMIT 1`,
		kind, ip, nowMS()).Scan(&token, &readyAt, &verified)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, false, false, nil
	}
	if err != nil {
		return "", 0, false, false, err
	}
	return token, readyAt, verified == 1, true, nil
}

// RateHits 报告一个键在滚动窗口里已经攒了几次动作。窗口已经过掉的按 0 算——那正是
// LoginAllowed 里"重新开始数"的那个判断，这里要给出同一个口径，否则渲染出来的等待秒数
// 和限流真正用的那个数字会打架。
func (db *DB) RateHits(ctx context.Context, key string, window time.Duration) (int, error) {
	var (
		hits  int
		start int64
	)
	err := db.SQL.QueryRowContext(ctx,
		`SELECT failures, window_start FROM login_attempts WHERE key=?`, key).Scan(&hits, &start)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if start < nowMS()-window.Milliseconds() {
		return 0, nil
	}
	return hits, nil
}
