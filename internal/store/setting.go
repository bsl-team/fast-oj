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
	Links         []SiteLink `json:"links"`
	AllowRegister bool       `json:"allow_register"`
}

// Setting keys. They are data, so adding one needs no schema change.
const (
	setTitle         = "site.title"
	setTagline       = "site.tagline"
	setFooter        = "site.footer"
	setLinks         = "site.links"
	setAllowRegister = "site.allow_register"
)

func (db *DB) Site(ctx context.Context) (SiteConfig, error) {
	rows, err := db.SQL.QueryContext(ctx, `SELECT key, value FROM site_settings`)
	if err != nil {
		return SiteConfig{}, err
	}
	defer rows.Close()

	values := map[string]string{
		setTitle:         "fast-oj",
		setTagline:       "",
		setFooter:        "",
		setLinks:         "[]",
		setAllowRegister: "1",
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
		Links:   links,
		// Anything but an explicit "0" keeps registration open, so a missing row
		// cannot silently lock the site.
		AllowRegister: values[setAllowRegister] != "0",
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
	allow := "0"
	if in.AllowRegister {
		allow = "1"
	}
	writes := []struct{ key, value string }{
		{setTitle, in.Title},
		{setTagline, in.Tagline},
		{setFooter, in.Footer},
		{setLinks, string(raw)},
		{setAllowRegister, allow},
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
