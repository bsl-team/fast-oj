package store

import (
	"context"
	"testing"
)

func TestSiteSettingsRoundTrip(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	admin := mustUser(t, db, "admin9")

	site, err := db.Site(ctx)
	if err != nil || site.Title != "fast oj" || !site.AllowRegister || len(site.Links) != 0 {
		t.Fatalf("defaults = %+v %v", site, err)
	}

	if err := db.SetSiteAll(ctx, admin.ID, SiteConfig{
		Title: "我的 OJ", Tagline: "欢迎", Footer: "出题请走管理台",
		Links: []SiteLink{{Title: "洛谷", URL: "https://luogu.com.cn"}},
	}); err != nil {
		t.Fatal(err)
	}
	site, err = db.Site(ctx)
	if err != nil || site.Title != "我的 OJ" || site.Footer == "" {
		t.Fatalf("after save = %+v %v", site, err)
	}
	if len(site.Links) != 1 || site.Links[0].Title != "洛谷" {
		t.Fatalf("links = %+v", site.Links)
	}

	// Only an explicit "0" locks registration, and absent links stay an empty
	// list rather than a null the UI has to special-case.
	if err := db.SetSiteAll(ctx, admin.ID, SiteConfig{Title: "我的 OJ"}); err != nil {
		t.Fatal(err)
	}
	site, err = db.Site(ctx)
	if err != nil || site.AllowRegister {
		t.Fatalf("allow_register = %v %v", site.AllowRegister, err)
	}
	if site.Links == nil || len(site.Links) != 0 {
		t.Errorf("links = %#v, want an empty list", site.Links)
	}
}
