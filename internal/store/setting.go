package store

import (
	"context"
	"database/sql"
	"encoding/json"
)

// SiteLink is one entry in the friendly-links list on the home page.
type SiteLink struct {
	Title string `json:"title"`
	URL   string `json:"url"`
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
	// setPublicClientSeen is not a setting anyone edits: it is one stamped row that
	// remembers "a visitor who was not this machine showed up", so the IP features can
	// hide themselves on a deployment where every request comes from the loopback.
	setPublicClientSeen = "ip.public_client_seen"
)

// openSwitch is the rule every feature switch follows: only an explicit "0" closes
// it. A missing or half-written row must not silently lock a whole site out.
func openSwitch(v string) bool { return v != "0" }

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
