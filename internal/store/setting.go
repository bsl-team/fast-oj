package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
)

// SiteLink is one entry in the friendly-links list on the home page.
type SiteLink struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// SMTPConfig is how this site sends mail: the 图形化 settings an operator fills in on
// the same page as everything else, so a wrong password can be debugged from the
// browser instead of by editing startup flags.
//
// Password is the only secret in here. It never leaves the database and no template
// prints it: the page shows a mask and only replaces it when a new value is typed.
type SMTPConfig struct {
	Host     string `json:"smtp_host"`
	Port     int    `json:"smtp_port"`
	Username string `json:"smtp_username"`
	Password string `json:"-"`
	From     string `json:"smtp_from"`
	// TLSMode is "starttls" (the default), "ssl" (connect already wrapped) or "none".
	// 587 wants starttls and 465 wants ssl; getting it wrong is the single most common
	// reason a first test mail hangs for a minute and then times out.
	TLSMode string `json:"smtp_tls"`
}

// Configured is the question the mail layer asks before it tries to open a socket:
// a host and a return address are the minimum anything can be sent with.
func (c SMTPConfig) Configured() bool {
	return strings.TrimSpace(c.Host) != "" && strings.TrimSpace(c.From) != ""
}

// SiteConfig is the typed view over the settings table. Missing keys fall back
// to defaults, so a half-populated table still renders a usable site.
type SiteConfig struct {
	Title         string     `json:"title"`
	Tagline       string     `json:"tagline"`
	Footer        string     `json:"footer"`
	APIHint       string     `json:"api_hint"`
	Links         []SiteLink `json:"links"`
	AllowRegister bool       `json:"allow_register"`
	// The four feature switches a school site asks for: 关掉之后旧内容照样读得到，
	// 只是不再收新的。注册是第五个，见 AllowRegister。
	AllowFeed     bool `json:"allow_feed"`
	AllowTopics   bool `json:"allow_topics"`
	AllowContests bool `json:"allow_contests"`
	AllowDM       bool `json:"allow_dm"`
	// RequireEmailVerify is the sixth switch, and it works the other way round: the
	// others refuse to *take* something new, this one refuses to *let go* of a new
	// account until the mailbox answers. It is off unless someone ticks it on, because
	// a missing row must not silently put every future registrant behind a mail server
	// that may not be configured yet.
	RequireEmailVerify bool       `json:"require_email_verify"`
	SMTP               SMTPConfig `json:"smtp"`
}

// DefaultAPIHint is the footer line a fresh site shows. 纯文本 API 与 ?format=json
// 早就撤掉了（网页是唯一给人类看的界面），所以默认值不能再宣传它们——留着这句话
// 就是让每个新站点在页脚里教人去 curl 一个已经不存在的东西。
const DefaultAPIHint = ""

// Setting keys. They are data, so adding one needs no schema change.
const (
	setTitle         = "site.title"
	setTagline       = "site.tagline"
	setFooter        = "site.footer"
	setAPIHint       = "site.api_hint"
	setLinks         = "site.links"
	setAllowRegister = "site.allow_register"
	setAllowFeed     = "site.allow_feed"
	setAllowTopics   = "site.allow_topics"
	setAllowContests = "site.allow_contests"
	setAllowDM       = "site.allow_dm"
	// setRequireVerify is the 注册需要验证邮箱 switch (M25f).
	setRequireVerify = "site.require_email_verification"
	// The SMTP block. These are one group because a half-filled mailer is worse than
	// an absent one: the test-mail button is what tells the operator which field is wrong.
	setSMTPHost     = "smtp.host"
	setSMTPPort     = "smtp.port"
	setSMTPUsername = "smtp.username"
	setSMTPPassword = "smtp.password"
	setSMTPFrom     = "smtp.from"
	setSMTPTLS      = "smtp.tls"
	// setPublicClientSeen is not a setting anyone edits: it is one stamped row that
	// remembers "a visitor who was not this machine showed up", so the IP features can
	// hide themselves on a deployment where every request comes from the loopback.
	setPublicClientSeen = "ip.public_client_seen"
)

// openSwitch is the rule every feature switch follows: only an explicit "0" closes
// it. A missing or half-written row must not silently lock a whole site out.
func openSwitch(v string) bool { return v != "0" }

// optInSwitch is the rule for the one switch that *adds* a door instead of closing a
// gate: 注册需要验证邮箱 stays off unless it is explicitly ticked on, because a missing
// row that turned it on would hold every new registrant behind a mail server nobody has
// configured yet.
func optInSwitch(v string) bool { return v == "1" }

// boolSwitch is the same rule seen from the writing side.
func boolSwitch(on bool) string {
	if on {
		return "1"
	}
	return "0"
}

func (db *DB) Site(ctx context.Context) (SiteConfig, error) {
	rows, err := db.SQL.QueryContext(ctx, `SELECT key, value FROM site_settings`)
	if err != nil {
		return SiteConfig{}, err
	}
	defer rows.Close()

	values := map[string]string{
		setTitle:         "fast oj",
		setTagline:       "",
		setFooter:        "",
		setAPIHint:       DefaultAPIHint,
		setLinks:         "[]",
		setAllowRegister: "1",
		setAllowFeed:     "1",
		setAllowTopics:   "1",
		setAllowContests: "1",
		setAllowDM:       "1",
		setRequireVerify: "0",
		setSMTPHost:      "",
		setSMTPPort:      "587",
		setSMTPUsername:  "",
		setSMTPPassword:  "",
		setSMTPFrom:      "",
		setSMTPTLS:       "starttls",
	}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return SiteConfig{}, err
		}
		values[k] = v
	}
	if err := rows.Err(); err != nil {
		return SiteConfig{}, err
	}

	var links []SiteLink
	if err := json.Unmarshal([]byte(values[setLinks]), &links); err != nil {
		return SiteConfig{}, err
	}
	// A port that does not parse stays 0 rather than failing the whole settings read:
	// the operator who typed "587,25" by accident still has to be able to open this page
	// and fix it. The form re-validates on the way in.
	port, err := strconv.Atoi(strings.TrimSpace(values[setSMTPPort]))
	if err != nil {
		port = 0
	}
	return SiteConfig{
		Title:   values[setTitle],
		Tagline: values[setTagline],
		Footer:  values[setFooter],
		APIHint: values[setAPIHint],
		Links:   links,
		// 五个开关都只在显式写"0"时关闭：缺行或写坏一行都不该把整个站点锁住。
		AllowRegister: openSwitch(values[setAllowRegister]),
		AllowFeed:     openSwitch(values[setAllowFeed]),
		AllowTopics:   openSwitch(values[setAllowTopics]),
		AllowContests: openSwitch(values[setAllowContests]),
		AllowDM:       openSwitch(values[setAllowDM]),
		// 第六个反过来：缺行是不拦人。
		RequireEmailVerify: optInSwitch(values[setRequireVerify]),
		SMTP: SMTPConfig{
			Host:     values[setSMTPHost],
			Port:     port,
			Username: values[setSMTPUsername],
			Password: values[setSMTPPassword],
			From:     values[setSMTPFrom],
			TLSMode:  values[setSMTPTLS],
		},
	}, nil
}

// SetSiteAll writes the whole form in one transaction, so a half-saved settings
// page is never observable.
func (db *DB) SetSiteAll(ctx context.Context, byUserID int64, in SiteConfig) error {
	links := in.Links
	if links == nil {
		links = []SiteLink{}
	}
	raw, err := json.Marshal(links)
	if err != nil {
		return err
	}
	writes := []struct{ key, value string }{
		{setTitle, in.Title},
		{setTagline, in.Tagline},
		{setFooter, in.Footer},
		{setAPIHint, in.APIHint},
		{setLinks, string(raw)},
		{setAllowRegister, boolSwitch(in.AllowRegister)},
		{setAllowFeed, boolSwitch(in.AllowFeed)},
		{setAllowTopics, boolSwitch(in.AllowTopics)},
		{setAllowContests, boolSwitch(in.AllowContests)},
		{setAllowDM, boolSwitch(in.AllowDM)},
		{setRequireVerify, boolSwitch(in.RequireEmailVerify)},
		{setSMTPHost, strings.TrimSpace(in.SMTP.Host)},
		{setSMTPPort, strconv.Itoa(in.SMTP.Port)},
		{setSMTPUsername, strings.TrimSpace(in.SMTP.Username)},
		{setSMTPPassword, in.SMTP.Password},
		{setSMTPFrom, strings.TrimSpace(in.SMTP.From)},
		{setSMTPTLS, in.SMTP.TLSMode},
	}
	return db.InTx(ctx, func(tx *sql.Tx) error {
		for _, w := range writes {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO site_settings (key, value, updated_at, updated_by) VALUES (?, ?, ?, ?)
				 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at,
				                                   updated_by=excluded.updated_by`,
				w.key, w.value, nowMS(), nullInt64(byUserID)); err != nil {
				return err
			}
		}
		return nil
	})
}
