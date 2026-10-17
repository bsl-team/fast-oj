package api

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/liangbinchen2013/fast-oj/internal/store"
)

// accounts.go is batch sign-up: a teacher facing a class of 40 students, not a
// registration form to fill in 40 times. Only user.manage reaches it, and what it
// makes are ordinary accounts — a batch of usernames is not a path to a role.

const (
	// maxBulkAccounts 一次最多建多少个号。全班注册绰绰有余，同时挡住拿这个页面当轰炸机。
	maxBulkAccounts = 200
	bulkPassLen     = 10
	// bulkAlphabet 去掉了容易看错的字符（0/O、1/I/l）：手抄在纸上的初始密码不该靠字体分辨。
	bulkAlphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	bulkLetters  = 23 // 前 23 个字符全是字母
)

// bulkPassword returns a random initial password whose first character is a letter,
// so a batch of them can never come out as the one shape the password policy
// refuses ("全是数字").
func bulkPassword() (string, error) {
	var b [bulkPassLen]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	var sb strings.Builder
	for i, x := range b {
		if i == 0 {
			sb.WriteByte(bulkAlphabet[int(x)%bulkLetters])
			continue
		}
		sb.WriteByte(bulkAlphabet[int(x)%len(bulkAlphabet)])
	}
	return sb.String(), nil
}

type bulkRow struct {
	Ordinal  int
	Handle   string
	Email    string
	Password string
	Status   string // created / dup
}

type accountsData struct {
	Mode     string
	Prefix   string
	From     string
	Count    string
	List     string
	Password string
	Errors   []string
	Rows     []bulkRow
	Summary  string
	Copyable string
}

func (s *Server) handleAccountsNew(w http.ResponseWriter, r *http.Request) {
	a, ok := s.requirePerm(w, r, permUserManage)
	if !ok {
		return
	}
	s.page(w, r, http.StatusOK, "批量建号", "admin_accounts", a.viewer(), msgOf(r), accountsData{Mode: "list"})
}

// handleAccountsCreate validates every row before writing any of them, then reports
// per row what happened.
func (s *Server) handleAccountsCreate(w http.ResponseWriter, r *http.Request) {
	a, ok := s.requirePerm(w, r, permUserManage)
	if !ok {
		return
	}
	d := accountsData{
		Mode:     r.FormValue("mode"),
		Prefix:   strings.TrimSpace(r.FormValue("prefix")),
		From:     strings.TrimSpace(r.FormValue("from")),
		Count:    strings.TrimSpace(r.FormValue("count")),
		List:     r.FormValue("list"),
		Password: r.FormValue("password"),
	}
	if d.Mode != "count" {
		d.Mode = "list"
	}

	rows, errs := s.parseBulkAccounts(r, d)
	if len(errs) == 0 && len(rows) == 0 {
		errs = append(errs, "要建号至少得给一行：名单里写用户名，或者用「前缀 + 个数」那种方式。")
	}
	if len(errs) > 0 {
		d.Errors = errs
		s.page(w, r, http.StatusBadRequest, "批量建号", "admin_accounts", a.viewer(), "", d)
		return
	}

	if err := s.db.CreateBulkAccounts(r.Context(), rows, s.cfg.BCryptCost); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	created := 0
	var copyable strings.Builder
	for i, row := range rows {
		if row.Status == "created" {
			created++
			copyable.WriteString(fmt.Sprintf("%s\t%s\n", row.Handle, row.Password))
		}
		d.Rows = append(d.Rows, bulkRow{
			Ordinal: i + 1, Handle: row.Handle, Email: row.Email, Password: row.Password, Status: row.Status,
		})
	}
	d.Copyable = copyable.String()
	d.Summary = fmt.Sprintf("建好 %d 个，撞名跳过 %d 个。", created, len(rows)-created)
	s.log.Info("accounts created", "by", a.User.Handle, "created", created, "skipped", len(rows)-created)
	// 初始密码只在这一个页面上出现一次：库里存的是哈希，刷新就再也看不到了。
	s.page(w, r, http.StatusOK, "批量建号", "admin_accounts", a.viewer(), "", d)
}

// parseBulkAccounts turns the form into store rows. 两种模式：
//   - list：一行一个账号，写成「用户名」或「用户名,邮箱」，初始密码用上面那个统一密码；
//   - count：前缀 + 起始号 + 个数，每个号一个随机密码。
//
// 用户名和密码都要过注册页同一套规则——批量入口不该是一个能绕过策略的后门。
func (s *Server) parseBulkAccounts(r *http.Request, d accountsData) ([]store.BulkAccount, []string) {
	var errs []string
	var rows []store.BulkAccount

	add := func(handle, email, password string) {
		problem := validHandleErr(handle)
		if problem == "" && email != "" && !validEmail(email) {
			problem = "邮箱格式不正确：" + email
		}
		if problem == "" {
			problem = passwordProblem(password)
		}
		if problem != "" {
			errs = append(errs, fmt.Sprintf("第 %d 行（%s）：%s", len(rows)+len(errs)+1, handle, problem))
			return
		}
		rows = append(rows, store.BulkAccount{Handle: handle, Email: email, Password: password})
	}

	switch d.Mode {
	case "count":
		prefix := d.Prefix
		if prefix == "" || len(prefix) > 20 || strings.ContainsAny(prefix, " ,") {
			errs = append(errs, "前缀要 1..20 个字符，不能有空格")
		}
		from, err1 := strconv.Atoi(d.From)
		count, err2 := strconv.Atoi(d.Count)
		if err1 != nil || err2 != nil || from < 0 || count < 1 || count > maxBulkAccounts {
			errs = append(errs, fmt.Sprintf("起始号要 ≥0，个数要在 1..%d", maxBulkAccounts))
			break
		}
		width := 2
		if last := from + count - 1; last >= 100 {
			width = len(strconv.Itoa(last))
		}
		for i := 0; i < count; i++ {
			pw, err := bulkPassword()
			if err != nil {
				errs = append(errs, "随机密码生成失败："+err.Error())
				break
			}
			add(fmt.Sprintf("%s%0*d", prefix, width, from+i), "", pw)
		}
	default:
		lines := 0
		for _, line := range strings.Split(d.List, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			lines++
			if lines > maxBulkAccounts {
				errs = append(errs, fmt.Sprintf("一次最多 %d 个账号，多出来的没有处理", maxBulkAccounts))
				break
			}
			handle, email, _ := strings.Cut(line, ",")
			handle = strings.TrimSpace(handle)
			email = strings.TrimSpace(email)
			add(handle, email, d.Password)
		}
	}
	return rows, errs
}

// validHandleErr is validHandle with the reason spelled out, because a batch form
// that only says "不对" leaves 40 rows to guess over.
func validHandleErr(handle string) string {
	if handle == "admin" || strings.EqualFold(handle, "system") {
		return "这个用户名被保留了"
	}
	if !validHandle(handle) {
		return "用户名只能是 1..32 个字母、数字、下划线、点或短横线"
	}
	return ""
}
