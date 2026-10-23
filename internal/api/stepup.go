package api

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/liangbinchen2013/fast-oj/internal/store"
)

// stepup.go 是给"谁能做什么"这一扇门加的那道身份再确认。
//
// CSRF 挡的是"别人替你点按钮"，挡不住拿到你 session cookie 的人——而权限控制台改的正是
// 这套站点最值钱的东西。所以这里要求：动权限之前把密码再输一遍，确认后 2 分钟内这一路的
// 改动不再问。判断记在服务端的会话行上（sessions.step_up_at），跟着 authenticate 本来就要
// 读的那一次出来，放行一侧不多一次查询。
//
// 说清它挡什么：拿到 cookie 但不知道密码的人，到这里就停了。知道密码的人挡不住——那种情况
// 与"他把账号给了你"没有区别，任何会话保护都给不出更强的保证。

const (
	// stepUpWindow 是那一次确认的有效期：够连改十几项权限，又短到人起身离开座位这件事
	// 来不及发生。
	stepUpWindow = 2 * time.Minute
	// maxStepUpFailures 是一个会话连着输错几次就当场踢掉。会来猜这道确认密码的，
	// 多半已经不是这个管理员本人——留着他的会话只会让下一次猜得更准。
	maxStepUpFailures = 5
)

// stepUpKey 把会话 id 折成一个能进 login_attempts 的键。会话 id 本身就是凭据，而那张表的
// 内容会被别的管理员在统计页看到——原样存等于把凭据抄进一张可读的表里。
func stepUpKey(sessionID string) string {
	sum := sha256.Sum256([]byte(sessionID))
	return "step_up:" + hex.EncodeToString(sum[:])[:16]
}

// stepUpOK 报告这一次改动是否已经证明过身份。令牌（bearer）那一路不需要：它每一次请求都
// 自带那个秘密，也就不存在"cookie 被人拿走"这回事。
func stepUpOK(a actor) bool {
	if !a.Cookie {
		return true
	}
	return a.Session.StepUpFresh(time.Now().UnixMilli(), stepUpWindow)
}

// requireStepUp 站在权限控制台四个写动作的前面：先过权限、先过"不许动自己/动站主"，
// 才轮到这道确认——那两条拒绝已经够清楚，不该被一句"请先输密码"盖过去。
//
// 没证明过就 303 送回原来那一页并留下一句话：那一页按同一个判断渲染，此刻摆着的是密码框，
// 不是那排改权限的按钮。
func (s *Server) requireStepUp(w http.ResponseWriter, r *http.Request, a actor, back, what string) bool {
	if stepUpOK(a) {
		return true
	}
	s.log.Info("step up required", "user", a.User.Handle, "action", what, "ip", clientIP(r))
	http.Redirect(w, r, back+"?msg="+url.QueryEscape(
		"改"+what+"之前要再证明一次身份：请输入你的密码，之后 2 分钟之内不再问。"), http.StatusSeeOther)
	return false
}

// handleStepUpUnlock 收那一次密码。它只把"证明过"写在**当前这个会话**上，不动任何权限——
// 所以就算被人拿着用，他能得到的也只是"再输一次他自己已经知道的密码"。
func (s *Server) handleStepUpUnlock(w http.ResponseWriter, r *http.Request) {
	a, ok := s.requireAnyPerm(w, r, permPermissionManage, permUserManage)
	if !ok {
		return
	}
	back := safeNext(r.FormValue("next"))
	if back == "/" {
		back = "/admin/perms"
	}
	key := stepUpKey(a.Session.ID)
	allowed, err := s.db.LoginAllowed(r.Context(), key, loginWindow, maxStepUpFailures)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if !allowed {
		s.dropStepUpSession(w, r, a, "这个会话确认失败的次数太多了，已经退出登录。")
		return
	}
	hash, err := s.db.UserPasswordHash(r.Context(), a.User.ID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if !store.CheckPassword(hash, r.FormValue("step_up")) {
		hits, err := s.db.RegisterLoginFailure(r.Context(), key, loginWindow)
		if err != nil {
			s.log.Error("step up throttle", "err", err)
		}
		// 密码不进日志、不进审计、不回显：这句提示里只有还剩几次。
		s.log.Warn("step up failed", "user", a.User.Handle, "ip", clientIP(r), "hits", hits)
		if hits >= maxStepUpFailures {
			s.dropStepUpSession(w, r, a, "密码连续输错 5 次，这个会话已经退出登录。")
			return
		}
		http.Redirect(w, r, back+"?msg="+url.QueryEscape(
			"密码不正确（这次确认还剩 "+strconv.Itoa(maxStepUpFailures-hits)+" 次机会）。"), http.StatusSeeOther)
		return
	}
	if err := s.db.MarkStepUp(r.Context(), a.Session.ID); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if err := s.db.ClearLoginFailures(r.Context(), key); err != nil {
		s.log.Error("step up throttle reset", "err", err)
	}
	s.log.Info("step up confirmed", "user", a.User.Handle, "ip", clientIP(r))
	http.Redirect(w, r, back+"?msg="+url.QueryEscape("身份已确认，接下来 2 分钟之内改权限不再问密码。"), http.StatusSeeOther)
}

// dropStepUpSession 是这个门最硬的一条：会话没保住就别继续用。只踢这一个会话，
// 本人另一台设备上的登录不受影响——要防的是手里这个会话，不是把管理员全站赶出去。
func (s *Server) dropStepUpSession(w http.ResponseWriter, r *http.Request, a actor, msg string) {
	if err := s.db.DropSession(r.Context(), a.Session.ID); err != nil {
		s.log.Error("drop session after step up lockout", "err", err)
	}
	s.audit(r, a, "step_up.denied", a.User.Handle, msg+"（只踢掉这一个会话）")
	s.clearSession(w)
	http.Redirect(w, r, "/login?msg="+url.QueryEscape(msg), http.StatusSeeOther)
}
