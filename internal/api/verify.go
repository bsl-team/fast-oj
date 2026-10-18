package api

// Email verification (M25f): the link a new registrant has to click before the site lets
// them hand in code, and the two screens that explain where that link went.
//
// The gate is deliberately narrow. Reading the site, logging in, browsing problems,
// 动态, 帖子 and 比赛 all keep working, because none of those cost the site anything and a
// person who cannot read the 题面 cannot even decide whether to verify. What it withholds
// is the two things that cost something: 提交代码 (a judge run) and 参加比赛 (a roster slot
// in someone else's contest).

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/liangbinchen2013/fast-oj/internal/store"
)

func (s *Server) handleVerifyEmail(w http.ResponseWriter, r *http.Request) {
	// The token arrives in a query string, which is what a mail link has to do. Nothing
	// here needs CSRF (it changes only the visitor's own account, and only once), and the
	// token is never echoed back into a URL or a log line.
	id, err := s.db.VerifyEmailToken(r.Context(), r.URL.Query().Get("token"))
	if err != nil {
		switch {
		case errors.Is(err, store.ErrEmailTokenExpired):
			s.verifyDone(w, r, err.Error()+"。在设置页可以重新发送。")
		default:
			s.verifyDone(w, r, "这个链接用过了，或者是别处抄来的。要重新收一封，去设置页点「重新发送验证邮件」。")
		}
		return
	}
	u, err := s.db.GetUserByID(r.Context(), id)
	if err != nil {
		s.verifyDone(w, r, "邮箱已经验证过了。")
		return
	}
	s.log.Info("email verified", "user", u.Handle)
	// 验证成功的这一刻账号已经能提交了，所以这里不能再用"当前开关"来决定这句话：
	// 一个刚刚把开关关掉、或者注册时没勾但管理员后来代发的站点，同样要给人一个答案。
	s.verifyDone(w, r, "邮箱 "+u.Email+" 已经验证过了，现在可以提交代码和参加比赛了。")
}

// verifyDone is the one landing page for every outcome of /verify. The person following a
// mail link is not necessarily signed in, and they may well be on a phone, so this is
// plain text with two links rather than a redirect into a login wall.
func (s *Server) verifyDone(w http.ResponseWriter, r *http.Request, msg string) {
	a, _ := s.authenticate(r)
	s.page(w, r, http.StatusOK, "邮箱验证", "verify", a.viewer(), msg, map[string]any{
		"SignedIn": a.User.ID != 0,
	})
}

// handleResendEmail is the 重新发送验证邮件 button on the account page. It is signed-in
// only, posts, and can only ever mail the address that is already on this account: the
// form carries no address field, so nobody can use it to make the site send a mail to a
// third party.
func (s *Server) handleResendEmail(w http.ResponseWriter, r *http.Request) {
	a, err := s.requireSignedIn(w, r)
	if err != nil {
		return
	}
	back := "/settings?msg="
	if a.User.EmailVerified {
		http.Redirect(w, r, back+url.QueryEscape("这个邮箱已经验证过了，不用再发。"), http.StatusSeeOther)
		return
	}
	if a.User.Email == "" {
		http.Redirect(w, r, back+url.QueryEscape("这个账号没有留邮箱，验证不了。找管理员要一个邮箱，或者用有邮箱的账号注册。"), http.StatusSeeOther)
		return
	}
	msg, err := s.sendVerification(r, a.User)
	if err != nil {
		s.log.Warn("verification mail failed", "user", a.User.Handle, "err", err)
		http.Redirect(w, r, back+url.QueryEscape(msg), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, back+url.QueryEscape("验证邮件已经发到你账号上那个邮箱，几分钟内到；再点一次会作废上一条链接。"), http.StatusSeeOther)
}

// sendVerification mints a link and mails it. It returns the message to show either way:
// the same sentence shape for success and failure, because "邮件没发出去" and "发出去了"
// have to be distinguishable on the page.
//
// 一次重发就换一张令牌，旧链接当场作废（见 store.CreateEmailToken）。
func (s *Server) sendVerification(r *http.Request, u store.User) (string, error) {
	site := s.siteFresh(r.Context())
	token, err := s.db.CreateEmailToken(r.Context(), u.ID, u.Email, store.EmailVerifyTTL)
	if err != nil {
		return "验证邮件没发出去：存令牌这一步就失败了。", err
	}
	if err := s.sendVerificationMail(r.Context(), site, s.baseURL(r), u.Email, u.Handle, token); err != nil {
		return "验证邮件没发出去：" + err.Error(), err
	}
	return "", nil
}

// verifyNotice is why this reader cannot submit right now, or "" when they can. It is
// asked by the problem page (so the form is replaced by a sentence and a link) and by the
// write path (so the refusal cannot be talked past), which is the same shape 被禁用日常能力
// already uses: the page and the gate never disagree.
//
// 纯文本，不带 HTML：这句话有两处落脚地，一处是 flash（会被转义），一处是提交请求的返回值。
// 链接由模板自己加，跟「提交需要先登录」那一行同一个写法。
func (s *Server) verifyNotice(r *http.Request, a actor) string {
	if !s.requiresEmailVerify(r) {
		return ""
	}
	if a.User.ID == 0 || a.User.EmailVerified {
		return ""
	}
	return "你的邮箱还没验证，所以还不能提交代码，也不能参加比赛。" +
		"在「设置」页点「重新发送验证邮件」，点信里那条链接就解开了。"
}

// requiresEmailVerify is the site switch, read once per question: 注册需要验证邮箱.
func (s *Server) requiresEmailVerify(r *http.Request) bool {
	return s.site(r.Context()).RequireEmailVerify
}
