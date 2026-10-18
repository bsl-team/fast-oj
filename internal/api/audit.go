package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/liangbinchen2013/fast-oj/internal/store"
)

// audit.go is the durable half of 「安全加固与审计」: every privileged action already
// writes a slog line, and this writes the same fact into the database so it outlives
// the process. The slog line stays — an operator watching stderr while a change lands
// is worth more than a page they have to open afterwards.
//
// What gets recorded is the privileged surface only (permissions, roles, bans,
// cancellations, site settings, judge programs, deletions, ticket handling). Ordinary
// content writes a person makes about their own things are not audit material and
// would only drown the entries that are.

// auditTimeout bounds the detached write. Losing the trail line is worth waiting a
// moment for, but holding a database connection past the response is not.
const auditTimeout = 3 * time.Second

// audit appends one trail line for the acting identity.
//
// A failure here is logged and dropped: the action it describes has already changed
// real state, and refusing the request because the trail could not be written would
// hand anyone who can fill a disk (or stall SQLite) a switch for the whole site.
// An audit trail that can DoS the site is the wrong trade — the gap must be visible
// in the log, not felt as an outage.
func (s *Server) audit(r *http.Request, a actor, action, target, detail string) {
	e := store.AuditEvent{
		ActorID: a.User.ID,
		Actor:   a.User.Handle,
		Action:  action,
		Target:  target,
		Detail:  detail,
		IP:      clientIP(r),
	}
	// A credential that is not a person (the judge token) still has to leave a name
	// behind, otherwise the line reads as if nobody did anything. What it leaves is
	// the shape of the credential, never its value.
	if e.Actor == "" {
		e.Actor = credentialKind(r)
	}
	// WithoutCancel: an abandoned page must not take the trail line down with it.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), auditTimeout)
	defer cancel()
	if err := s.db.Audit(ctx, e); err != nil {
		s.log.Error("audit write failed", "action", action, "target", target, "actor", e.Actor, "err", err)
	}
}

// auditDenied records a refused judge-protocol call.
//
// 判机令牌能领工单、能写判定结果，所以"有人拿不对的凭据来敲门"是这一站最要紧的一条线索，
// 而它今天只混在通用请求日志里（只有方法、路径、状态码）。这里只记判机协议那一路：
// 网页上的 401 是普通访客没登录，量大且没有特权动作发生，记下来只会把真正的那几条冲淡。
func (s *Server) auditDenied(r *http.Request, reason string) {
	e := store.AuditEvent{
		Action: "judge.denied",
		Actor:  credentialKind(r),
		Target: r.URL.Path,
		Detail: reason,
		IP:     clientIP(r),
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), auditTimeout)
	defer cancel()
	if err := s.db.Audit(ctx, e); err != nil {
		s.log.Error("audit write failed", "action", e.Action, "target", e.Target, "err", err)
	}
}

// credentialKind says what shape the presented credential had, never its value: a
// token or a password must not end up in a row that staff pages read.
func credentialKind(r *http.Request) string {
	if bearerToken(r) != "" {
		return "bearer 令牌"
	}
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		return "会话 Cookie"
	}
	return "无凭据"
}

// userStatusLabel turns an account status into the words an operator reads: an audit
// line is evidence, and "banned" next to "封禁" is the difference between a line you
// can act on and one you have to decode.
func userStatusLabel(status string) string {
	switch status {
	case "active":
		return "启用"
	case "banned":
		return "封禁"
	case "deleted":
		return "已注销"
	}
	return status
}

// siteSwitches turns one save of the site settings into a readable summary. The audit
// line has to answer "what did that save change", not merely "someone saved": a site
// silently taken off 注册 is exactly the thing an operator asks about afterwards.
//
// The prose (首页语、页脚、API 提示) is deliberately left out — it can run to thousands
// of characters, and the row's value is the switches and the name.
func siteSwitches(c store.SiteConfig) string {
	state := func(name string, on bool) string {
		if on {
			return name + "=开"
		}
		return name + "=关"
	}
	return strings.Join([]string{
		state("注册", c.AllowRegister), state("动态", c.AllowFeed), state("帖子", c.AllowTopics),
		state("比赛", c.AllowContests), state("私信", c.AllowDM),
		fmt.Sprintf("友情链接 %d 条", len(c.Links)),
	}, " ")
}
