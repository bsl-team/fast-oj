package api

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/liangbinchen2013/fast-oj/internal/store"
)

const (
	loginWindow      = 15 * time.Minute
	maxLoginFailures = 10
	registerWindow   = time.Hour
	maxRegistrations = 20
	// 一台机器一分钟最多注册几个账号由 -register-per-minute 决定，默认 1。机房里一个出口
	// IP 后面站着几十个学生，光看 IP 会误伤，光看设备号又能被清空存储绕过——所以两把键
	// 一起看，任何一把超了就拒。
	registerMinute = time.Minute
)

// deviceToken is the browser-side machine handle: a random id the register page
// generates once and keeps in localStorage.
//
// 网卡 MAC 地址拿不到：它只存在于同一个二层网络里，HTTP 请求从头到尾不会带上它，
// 路由器已经把它换成了自己的。能做到的是"这台机器的浏览器"这个级别的标识。
// 校验格式而不是信任内容：这个值来自客户端，什么都能编，所以它只能用来做限流键，
// 不能当身份、不能当授权依据。
func deviceToken(raw string) string {
	tok := strings.TrimSpace(raw)
	if len(tok) < 16 || len(tok) > 64 {
		return ""
	}
	for _, r := range tok {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return ""
		}
	}
	return tok
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// validEmail is deliberately shallow: the mailbox is not verified yet, so this
// only rejects shapes that could never be delivered, not throwaway domains.
// validHandle keeps URLs safe: a handle travels in a path, so it may not contain
// a slash, a space or any other character that would change what the URL means.
func validHandle(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '_', r == '-', r == '.':
		default:
			return false
		}
	}
	return true
}

func validEmail(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 3 || len(s) > 120 {
		return false
	}
	at := strings.IndexByte(s, '@')
	if at < 1 || at != strings.LastIndexByte(s, '@') || at == len(s)-1 {
		return false
	}
	domain := s[at+1:]
	if !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return false
	}
	for _, r := range s {
		if r > 127 || r <= ' ' || strings.ContainsRune(`()<>[];:,"`, r) {
			return false
		}
	}
	return true
}

// passwordPolicy is the minimum we will accept; length matters more than
// composition rules that push people into "Password1!".
func passwordProblem(pw string) string {
	switch {
	case len(pw) < store.MinPasswordLen:
		return "密码至少 " + strconv.Itoa(store.MinPasswordLen) + " 位"
	case len(pw) > store.MaxPasswordLen:
		return "密码最长 " + strconv.Itoa(store.MaxPasswordLen) + " 字节"
	case strings.Trim(pw, "0123456789") == "":
		return "密码不能全是数字"
	}
	return ""
}

// safeNext only accepts an in-site path, so /login?next= cannot become an open
// redirect to an attacker's page. Backslashes are rejected because some user
// agents normalise them into a scheme-relative URL.
func safeNext(raw string) string {
	if raw == "" || strings.ContainsRune(raw, '\\') {
		return "/"
	}
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return "/"
	}
	if u, err := url.Parse(raw); err != nil || u.IsAbs() || u.Host != "" {
		return "/"
	}
	return raw
}

func (s *Server) setSession(w http.ResponseWriter, id string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.Secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(ttl.Seconds()),
	})
}

func (s *Server) clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.Secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if a, err := s.authenticate(r); err == nil && a.User.ID != 0 {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.page(w, r, http.StatusOK, "登录", "login", nil, msgOf(r), map[string]any{"Next": r.URL.Query().Get("next")})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	key := "login:" + clientIP(r)
	allowed, err := s.db.LoginAllowed(r.Context(), key, loginWindow, maxLoginFailures)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if !allowed {
		s.loginFailed(w, r, "失败次数过多，请 15 分钟后再试", http.StatusTooManyRequests)
		return
	}

	identifier := r.FormValue("identifier")
	password := r.FormValue("password")
	u, hash, err := s.db.FindForLogin(r.Context(), identifier)
	if err != nil || !store.CheckPassword(hash, password) {
		if _, ferr := s.db.RegisterLoginFailure(r.Context(), key, loginWindow); ferr != nil {
			s.log.Error("throttle", "err", ferr)
		}
		s.log.Warn("login failed", "identifier", identifier, "ip", clientIP(r))
		// One message for unknown account and wrong password: no enumeration.
		s.loginFailed(w, r, "账号或密码不正确", http.StatusUnauthorized)
		return
	}

	sess, err := s.db.CreateSession(r.Context(), u.ID, clientIP(r), r.UserAgent(), store.SessionTTL)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if err := s.db.ClearLoginFailures(r.Context(), key); err != nil {
		s.log.Error("throttle reset", "err", err)
	}
	if err := s.db.TouchLogin(r.Context(), u.ID); err != nil {
		s.log.Error("touch login", "err", err)
	}
	s.setSession(w, sess.ID, store.SessionTTL)
	s.log.Info("login", "user", u.Handle, "ip", clientIP(r))
	http.Redirect(w, r, safeNext(r.FormValue("next")), http.StatusSeeOther)
}

// loginFailed re-renders the form instead of redirecting, so the typed handle
// survives and the status code stays meaningful.
func (s *Server) loginFailed(w http.ResponseWriter, r *http.Request, msg string, status int) {
	s.page(w, r, status, "登录", "login", nil, msg, map[string]any{
		"Next":       r.FormValue("next"),
		"Identifier": r.FormValue("identifier"),
	})
}

func (s *Server) handleRegisterPage(w http.ResponseWriter, r *http.Request) {
	// 已经登录就别给看注册表单：这个页面以前带着 nil viewer 渲染，导航栏骗你说没登录，
	// 而浏览器其实带着 session cookie——你一提交就撞上 CSRF 闸门，得到一句莫名其妙的
	// 「表单已过期」。/login 早就这么挡了，注册没道理例外。
	if a, err := s.authenticate(r); err == nil && a.User.ID != 0 {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if !s.registrationOpen(r) {
		s.errorPage(w, r, http.StatusForbidden, "当前未开放注册")
		return
	}
	s.page(w, r, http.StatusOK, "注册", "register", nil, msgOf(r), map[string]any{
		"Handle": "", "Email": "", "DisplayName": "",
	})
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if !s.registrationOpen(r) {
		s.errorPage(w, r, http.StatusForbidden, "当前未开放注册")
		return
	}
	handle := strings.TrimSpace(r.FormValue("handle"))
	email := strings.TrimSpace(r.FormValue("email"))
	display := strings.TrimSpace(r.FormValue("display_name"))
	password := r.FormValue("password")

	ipKey := "register:ip:" + clientIP(r)
	hourKey := "register:hour:" + clientIP(r)
	// 一分钟几次的键：IP 加上浏览器设备号。两把键各自记一条计数，所以不能跟按小时那条
	// 共用同一个键名——login_attempts 里一个键只有一行。
	minuteKeys := []string{ipKey}
	if dev := deviceToken(r.FormValue("device")); dev != "" {
		minuteKeys = append(minuteKeys, "register:dev:"+dev)
	}
	for _, key := range minuteKeys {
		allowed, err := s.db.LoginAllowed(r.Context(), key, registerMinute, s.cfg.RegisterPerMinute)
		if err != nil {
			s.writeStoreError(w, r, err)
			return
		}
		if !allowed {
			s.renderRegisterForm(w, r, http.StatusTooManyRequests,
				"这台机器一分钟内只能注册 "+strconv.Itoa(s.cfg.RegisterPerMinute)+" 个账号，等一分钟再试。",
				handle, email, display)
			return
		}
	}
	// 然后才是每小时的总量上限。
	allowed, err := s.db.LoginAllowed(r.Context(), hourKey, registerWindow, maxRegistrations)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if !allowed {
		s.renderRegisterForm(w, r, http.StatusTooManyRequests,
			"这台机器这一个小时里的注册次数已达上限，请稍后再试", handle, email, display)
		return
	}

	var problem string
	switch {
	case !validHandle(handle):
		problem = "用户名只能是 1..32 个字母、数字、下划线、点或短横线"
	case handle == "admin" || strings.EqualFold(handle, "system"):
		problem = "这个用户名被保留了"
	case !validEmail(email):
		problem = "邮箱格式不正确"
	case passwordProblem(password) != "":
		problem = passwordProblem(password)
	case display != "" && len(display) > 40:
		problem = "昵称最长 40 个字符"
	}
	if problem != "" {
		s.renderRegisterForm(w, r, http.StatusBadRequest, problem, handle, email, display)
		return
	}

	u, err := s.db.CreateAccount(r.Context(), store.AccountInput{
		Handle: handle, Email: email, Password: password, DisplayName: display, Cost: s.cfg.BCryptCost,
	})
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			s.renderRegisterForm(w, r, http.StatusConflict, "这个用户名或邮箱已经注册过了", handle, email, display)
			return
		}
		if errors.Is(err, store.ErrWeakPassword) {
			s.renderRegisterForm(w, r, http.StatusBadRequest, passwordProblem(password), handle, email, display)
			return
		}
		s.writeStoreError(w, r, err)
		return
	}
	// 注册成功也要计数：这些桶记的是"成功注册"，不是失败尝试，所以一条都不能漏。
	count := func(key string, window time.Duration) {
		if _, err := s.db.RegisterLoginFailure(r.Context(), key, window); err != nil {
			s.log.Error("register counter", "err", err)
		}
	}
	for _, key := range minuteKeys {
		count(key, registerMinute)
	}
	count(hourKey, registerWindow)
	// 站主先到先得：站点还没人认领时，第一个注册的人就是超级管理员。
	claimed := s.claimSiteOwner(r, u)

	sess, err := s.db.CreateSession(r.Context(), u.ID, clientIP(r), r.UserAgent(), store.SessionTTL)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	s.setSession(w, sess.ID, store.SessionTTL)
	s.traffic.noteRegister(clientIP(r))
	s.log.Info("registered", "user", u.Handle, "ip", clientIP(r))
	welcome := "邮箱还没验证，可以先正常使用。"
	if claimed {
		// 这个人必须知道自己刚刚拿到了什么：站主只有第一个账号能做，之后再也补不上。
		welcome = "你是这个站的第一个账号，已经把你设为超级管理员：别的管理员改不动你的权限、角色和状态。"
	}
	http.Redirect(w, r, "/?msg="+url.QueryEscape("欢迎，"+u.Handle+"。"+welcome), http.StatusSeeOther)
}

// claimSiteOwner seats the first person to register a site that has no super admin
// yet, and returns whether this account is now it.
//
// Only an unpeopled site may be claimed this way. A school that pre-creates its
// student accounts — bulk registration, before the teacher ever signs up — would
// otherwise hand the top of the tree to whichever student clicks 注册 first. Sites
// that already have people get their owner from the token takeover page instead.
func (s *Server) claimSiteOwner(r *http.Request, u store.User) bool {
	owner, err := s.db.OwnerID(r.Context())
	if err != nil {
		s.log.Error("owner lookup", "err", err)
		return false
	}
	if owner != 0 {
		return false
	}
	human, err := s.db.HasOtherHumanAccount(r.Context(), u.ID)
	if err != nil {
		s.log.Error("owner claim", "err", err)
		return false
	}
	if human {
		return false
	}
	won, err := s.db.ClaimOwner(r.Context(), u.ID)
	if err != nil {
		s.log.Error("owner claim", "err", err)
		return false
	}
	if won {
		s.log.Info("site owner claimed", "user", u.Handle)
	}
	return won
}

func (s *Server) renderRegisterForm(w http.ResponseWriter, r *http.Request, status int, msg, handle, email, display string) {
	s.page(w, r, status, "注册", "register", nil, msg, map[string]any{
		"Handle": handle, "Email": email, "DisplayName": display,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		if err := s.db.DropSession(r.Context(), c.Value); err != nil {
			s.log.Error("drop session", "err", err)
		}
	}
	s.clearSession(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// --- settings: profile and password ---

type settingsData struct {
	Email       string
	DisplayName string
	Bio         string
	HasPassword bool
	Joined      string
	// DMPolicy is who may send this person a private message.
	DMPolicy string
}

func (s *Server) buildSettings(a actor) settingsData {
	return settingsData{
		Email: a.User.Email, DisplayName: a.User.DisplayName, Bio: a.User.Bio,
		HasPassword: a.User.HasPassword, Joined: agoMS(a.User.CreatedAt),
		DMPolicy: a.User.DMPolicy,
	}
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	a, err := s.authenticate(r)
	if err != nil || a.User.ID == 0 {
		http.Redirect(w, r, "/login?next=/settings", http.StatusSeeOther)
		return
	}
	s.page(w, r, http.StatusOK, "设置", "settings", a.viewer(), msgOf(r), s.buildSettings(a))
}

func (s *Server) handleProfileSave(w http.ResponseWriter, r *http.Request) {
	a, err := s.requireSignedIn(w, r)
	if err != nil {
		return
	}
	display := strings.TrimSpace(r.FormValue("display_name"))
	if len(display) > 40 {
		http.Redirect(w, r, "/settings?msg="+url.QueryEscape("昵称最长 40 个字符"), http.StatusSeeOther)
		return
	}
	if err := s.db.UpdateProfile(r.Context(), a.User.ID, display, r.FormValue("bio")); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings?msg="+url.QueryEscape("已保存"), http.StatusSeeOther)
}

func (s *Server) handlePasswordSave(w http.ResponseWriter, r *http.Request) {
	a, err := s.requireSignedIn(w, r)
	if err != nil {
		return
	}
	back := "/settings?msg="
	if a.User.HasPassword {
		current, err := s.db.UserPasswordHash(r.Context(), a.User.ID)
		if err != nil {
			s.writeStoreError(w, r, err)
			return
		}
		if !store.CheckPassword(current, r.FormValue("current")) {
			http.Redirect(w, r, back+url.QueryEscape("当前密码不正确"), http.StatusSeeOther)
			return
		}
	}
	pw := r.FormValue("password")
	if problem := passwordProblem(pw); problem != "" {
		http.Redirect(w, r, back+url.QueryEscape(problem), http.StatusSeeOther)
		return
	}
	if err := s.db.SetPassword(r.Context(), a.User.ID, pw, s.cfg.BCryptCost); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	// Every session is gone, including this one, so start a fresh login.
	s.clearSession(w)
	s.log.Info("password changed", "user", a.User.Handle)
	http.Redirect(w, r, "/login?msg="+url.QueryEscape("密码已修改，请重新登录"), http.StatusSeeOther)
}

// requireSignedIn is the shared gate for the settings pages.
func (s *Server) requireSignedIn(w http.ResponseWriter, r *http.Request) (actor, error) {
	a, err := s.authenticate(r)
	if err != nil || a.User.ID == 0 {
		http.Redirect(w, r, "/login?next=/settings", http.StatusSeeOther)
		return actor{}, errNoCredentials
	}
	return a, nil
}
