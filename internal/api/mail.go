package api

// SMTP delivery for the two mails this site sends: a verification link (M25f) and the
// operator's own 发送测试邮件 button. The settings live in the site settings table so
// they are edited on the same page as everything else; internal/config owns none of them
// because a mail server that takes a restart to reconfigure is a mail server nobody
// debugges from the browser.

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/liangbinchen2013/fast-oj/internal/store"
)

const (
	// A mail server that is down must not hang a request for the platform's default
	// (which is no timeout at all). Two dials plus one hello is the budget; an operator
	// pressing 发送测试邮件 gets an answer in seconds instead of a spinning tab.
	mailDialTimeout = 8 * time.Second
	mailTotalBudget = 25 * time.Second
	// 一封测试邮件最多能写多少正文：这个正文是人手打的，不该变成上传通道。
	mailBodyLimit = 4000
)

// tlsModes are the three ways an operator can wrap the connection, and the reason the
// page asks instead of guessing: 587 wants starttls, 465 wants ssl, and picking the
// other one is a silent hang rather than an error message.
var tlsModes = map[string]string{
	"starttls": "STARTTLS（587 常用，默认）",
	"ssl":      "SSL/TLS（465 常用，连上就是加密的）",
	"none":     "不加密（只在本机或内网用）",
}

func validTLSMode(mode string) bool {
	_, ok := tlsModes[mode]
	return ok
}

// smtpFromEnv is the fallback for a deployment that already injects its mail password
// through the environment and does not want it typed into a web form. Values in the
// database win, field by field, so the page is the truth once anyone edits it.
//
// 这一步不打印、不回显任何一项：密码只能待在这个函数返回的结构里。
func smtpFromEnv(c store.SMTPConfig) store.SMTPConfig {
	if strings.TrimSpace(c.Host) == "" {
		c.Host = os.Getenv("FASTOJ_SMTP_HOST")
	}
	if c.Port == 0 {
		if p, err := strconv.Atoi(strings.TrimSpace(os.Getenv("FASTOJ_SMTP_PORT"))); err == nil {
			c.Port = p
		}
	}
	if strings.TrimSpace(c.Username) == "" {
		c.Username = os.Getenv("FASTOJ_SMTP_USERNAME")
	}
	if c.Password == "" {
		c.Password = os.Getenv("FASTOJ_SMTP_PASSWORD")
	}
	if strings.TrimSpace(c.From) == "" {
		c.From = os.Getenv("FASTOJ_SMTP_FROM")
	}
	if strings.TrimSpace(c.TLSMode) == "" {
		c.TLSMode = os.Getenv("FASTOJ_SMTP_TLS")
	}
	return c
}

// smtpFromForm reads the SMTP block out of the settings form.
//
// saved supplies the one field the page does not echo back: an empty password box means
// "keep the stored secret", because the alternative is that saving the site title wipes
// the mail password, or that the operator has to retype it every single time.
//
// The second return is what the operator reads when it refuses: a port that is not a
// number and an encryption mode that is not one of the three are the two mistakes that
// otherwise turn into a minute of spinning.
func smtpFromForm(r *http.Request, saved store.SMTPConfig) (store.SMTPConfig, string) {
	c := store.SMTPConfig{
		Host:     strings.TrimSpace(r.FormValue("smtp_host")),
		Username: strings.TrimSpace(r.FormValue("smtp_username")),
		From:     strings.TrimSpace(r.FormValue("smtp_from")),
		// 留空 = 保留已经存的那条：这个页面从不显示密码，所以它也不该被"清空输入框"
		// 顺手删掉——那是让人重打一遍的捷径，不是删除的入口。
		Password: saved.Password,
		TLSMode:  strings.ToLower(strings.TrimSpace(r.FormValue("smtp_tls"))),
	}
	if pw := r.FormValue("smtp_password"); pw != "" {
		c.Password = pw
	}
	portText := strings.TrimSpace(r.FormValue("smtp_port"))
	switch {
	case portText == "":
		c.Port = saved.Port
	default:
		port, err := strconv.Atoi(portText)
		if err != nil || port < 1 || port > 65535 {
			return c, "SMTP 端口要填 1..65535 的数字"
		}
		c.Port = port
	}
	if tooLong(c.Host, 200) || tooLong(c.Username, 200) || tooLong(c.From, 200) || len(c.Password) > 512 {
		return c, "SMTP 的每一项都太长了：地址、用户名、发件地址各 200 字，密码 512 字节"
	}
	if c.TLSMode == "" {
		c.TLSMode = "starttls"
	}
	if !validTLSMode(c.TLSMode) {
		return c, "加密方式只认 starttls / ssl / none"
	}
	if c.From != "" && !validEmail(c.From) {
		return c, "发件地址（from）看起来不是一个邮箱"
	}
	return c, ""
}

// sendVerificationMail is the registration path's one job: put a link in a mail. It is
// separate from the mail body builder so the resend form and the registration form send
// byte for byte the same message.
func (s *Server) sendVerificationMail(ctx context.Context, site store.SiteConfig, baseURL, to, handle, token string) error {
	cfg := smtpFromEnv(site.SMTP)
	subject := "验证你的 " + site.Title + " 邮箱"
	return sendMail(cfg, to, subject, verificationBody(baseURL, handle, token))
}

// verificationBody is the mail a new registrant gets. Plain text, one link, and the two
// things they would otherwise ask in a 工单: how long it is good for, and what happens to
// the old link when they press 重新发送.
func verificationBody(baseURL, handle, token string) string {
	link := strings.TrimSuffix(baseURL, "/") + "/verify?token=" + url.QueryEscape(token)
	return "你好，" + handle + "。\n\n" +
		"这一步只是确认这个邮箱是你的，点下面这个链接就够了：\n\n" +
		link + "\n\n" +
		"链接 24 小时内有效，只能点一次；在设置页重新发送之后，旧的那条就作废了。\n" +
		"验证之前你可以登录、看题目、读帖子，但还不能提交代码或参加比赛。\n" +
		"没有申请过这个账号的话，忽略这封邮件就行。\n"
}

// baseURL is where a person reading this mail should be sent back to. The Host header is
// what they used to reach the site, so a school that serves both an internal name and a
// domain gets the link they can actually open; -secure-cookies decides the scheme, which
// is the same signal the app already uses for "this deployment is on TLS".
func (s *Server) baseURL(r *http.Request) string {
	scheme := "http"
	if s.cfg.Secure || r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := strings.TrimSpace(r.Host)
	if host == "" {
		host = s.cfg.Addr
	}
	return scheme + "://" + host
}

// smtpAddr is host:port, refusing a port that could not be dialed.
func smtpAddr(host string, port int) (string, error) {
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("端口 %d 不是一个可用的端口（要 1..65535）", port)
	}
	return net.JoinHostPort(strings.TrimSpace(host), strconv.Itoa(port)), nil
}

// sendMail hands one plain-text mail to the configured server.
//
// The returned error is what the operator sees, verbatim: the server's own line
// ("535 5.7.8 Username and Password not accepted") is the useful part, and rewriting it
// into 「发送邮件失败」 would send them back to guessing. Nothing that comes back from the
// server is ever logged — a SMTP greeting or an auth challenge can carry what was typed
// into it.
func sendMail(c store.SMTPConfig, to, subject, body string) error {
	if strings.TrimSpace(c.Host) == "" {
		return errors.New("还没填 SMTP 服务器地址；填好之后再点一次「发送测试邮件」")
	}
	if strings.TrimSpace(c.From) == "" {
		return errors.New("还没填发件地址（from）")
	}
	if !validEmail(to) {
		return errors.New("收件邮箱格式不正确")
	}
	mode := strings.ToLower(strings.TrimSpace(c.TLSMode))
	if mode == "" {
		mode = "starttls"
	}
	if !validTLSMode(mode) {
		return fmt.Errorf("加密方式只认 starttls / ssl / none，填的是 %q", c.TLSMode)
	}
	addr, err := smtpAddr(c.Host, c.Port)
	if err != nil {
		return err
	}
	host := strings.TrimSpace(c.Host)

	dialer := &net.Dialer{Timeout: mailDialTimeout}
	var conn net.Conn
	if mode == "ssl" {
		// 465：整条连接从第一字节起就是 TLS，没有 STARTTLS 可谈。
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: host})
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("连不上 %s：%w", addr, err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(mailTotalBudget)); err != nil {
		return err
	}

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("和 %s 打招呼失败：%w", addr, err)
	}
	defer client.Quit()

	if mode == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("%s 不支持 STARTTLS，但它被填成了「starttls」；465 端口请选「SSL/TLS」", addr)
		}
		if err := client.StartTLS(&tls.Config{ServerName: host}); err != nil {
			return fmt.Errorf("STARTTLS 握手失败：%w", err)
		}
	}
	if c.Username != "" || c.Password != "" {
		if ok, _ := client.Extension("AUTH"); !ok {
			return fmt.Errorf("%s 说不支持 AUTH，但这里填了用户名或密码", addr)
		}
		// PlainAuth refuses to send a password over a connection it does not consider
		// encrypted, so 不加密 plus a password fails here with a clear reason instead of
		// quietly leaking it.
		if err := client.Auth(smtp.PlainAuth("", c.Username, c.Password, host)); err != nil {
			return fmt.Errorf("登录 %s 失败：%w", host, err)
		}
	}
	if err := client.Mail(strings.TrimSpace(c.From)); err != nil {
		return fmt.Errorf("发件地址 %s 被服务器拒绝：%w", c.From, err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("收件地址 %s 被服务器拒绝：%w", to, err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("服务器不让写正文：%w", err)
	}
	if _, err := io.WriteString(w, mailMessage(c.From, to, subject, body)); err != nil {
		_ = w.Close()
		return fmt.Errorf("投递正文失败：%w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("投递正文失败：%w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("邮件已经交出去了，但挂断时服务器回了：%w", err)
	}
	return nil
}

// mailMessage builds the RFC822 body. The subject is encoded because a Chinese subject
// in a raw header is not legal ASCII and plenty of servers drop the message for it.
func mailMessage(from, to, subject, body string) string {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(body, "\n", "\r\n"))
	return b.String()
}

// smtpTestInput is whatever the operator typed on the 发送测试邮件 form, kept so a
// failure does not make them re-enter the address.
type smtpTestInput struct{ To, Body string }

// handleSiteTestEmail is the 「发送测试邮件」 button: it connects to the server that is
// saved in the settings, sends one mail, and shows whatever came back on the page.
//
// POST only, and it re-renders instead of redirecting: an error string is not something
// to put in a URL (the address bar, the referrer and the access log all keep URLs), and
// the reason a test mail failed is the one thing this screen exists to show.
func (s *Server) handleSiteTestEmail(w http.ResponseWriter, r *http.Request) {
	a, ok := s.requirePerm(w, r, permSiteSettings)
	if !ok {
		return
	}
	site := s.siteFresh(r.Context())
	test := smtpTestInput{
		To:   strings.TrimSpace(r.FormValue("to")),
		Body: strings.TrimSpace(r.FormValue("body")),
	}
	// 测的是表单里刚填的那一套，不是"保存过的那一套"：改完密码想先试一发再存，
	// 是这个按钮存在的唯一理由。空着的密码框仍然表示沿用库里那条。
	cfg, bad := smtpFromForm(r, site.SMTP)
	msg := bad
	switch {
	case msg != "":
	case len(test.Body) > mailBodyLimit:
		msg = "测试正文最长 " + strconv.Itoa(mailBodyLimit) + " 字"
	default:
		if err := sendMail(cfg, test.To, testMailSubject(site.Title), testMailBody(cfg, test.To, test.Body)); err != nil {
			// 原样贴出服务器回的那一行，但只贴进页面：日志里只留一句 err，
			// 因为 SMTP 的挑战文本可能带着刚被敲进去的密码片段。
			msg = "测试邮件没发出去：" + err.Error()
			s.log.Warn("smtp test failed", "by", a.User.Handle, "err", err)
		} else {
			msg = "已经发到 " + test.To + "：去收件箱看一眼，能收到就说明这套配置是对的。"
			s.log.Info("smtp test sent", "by", a.User.Handle)
		}
	}
	// 回填表单：地址、端口、用户名这些重新显示出来，密码那一栏永远是掩码。
	shown := site
	shown.SMTP = cfg
	s.renderSite(w, r, a, http.StatusOK, shown, linkLines(site.Links), msg, test)
}

func testMailSubject(title string) string {
	if strings.TrimSpace(title) == "" {
		title = "fast oj"
	}
	return "【" + title + "】SMTP 测试邮件"
}

// testMailBody is what the operator reads in the inbox: enough to prove the mail came
// from this site and this button, nothing that has to stay secret.
func testMailBody(cfg store.SMTPConfig, to, extra string) string {
	var b strings.Builder
	b.WriteString("这是一封测试邮件，用来确认站上填的发信配置真的能用。\n\n")
	b.WriteString("发件服务器：" + cfg.Host + ":" + strconv.Itoa(cfg.Port) + "（" + strings.ToLower(cfg.TLSMode) + "）\n")
	b.WriteString("发件地址：" + cfg.From + "\n")
	b.WriteString("收件地址：" + to + "\n")
	if strings.TrimSpace(extra) != "" {
		b.WriteString("\n附言：" + strings.TrimSpace(extra) + "\n")
	}
	b.WriteString("\n收到这封邮件就说明 SMTP 配置是对的，注册时发的验证邮件也会走到同一个服务器。\n")
	return b.String()
}
