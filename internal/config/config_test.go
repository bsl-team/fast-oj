package config

import (
	"path/filepath"
	"testing"
)

func TestLoadRequiresTokens(t *testing.T) {
	if _, err := Load(nil); err == nil {
		t.Fatal("expected error when tokens are missing")
	}
}

func TestLoadDefaultsAndDerivedDBPath(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load([]string{"-data", dir, "-admin-token", "a", "-judge-token", "j"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "127.0.0.1:8080" {
		t.Errorf("addr = %q", cfg.Addr)
	}
	if want := filepath.Join(dir, "fast-oj.db"); cfg.DBPath != want {
		t.Errorf("db = %q, want %q", cfg.DBPath, want)
	}
	if cfg.MaxSource != 256<<10 {
		t.Errorf("max-source = %d", cfg.MaxSource)
	}
}

func TestLoadRejectsZeroMaxSource(t *testing.T) {
	if _, err := Load([]string{"-data", t.TempDir(), "-admin-token", "a", "-judge-token", "j", "-max-source", "0"}); err == nil {
		t.Fatal("expected validation error")
	}
}
