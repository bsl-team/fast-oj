package config

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	Addr       string
	DataDir    string
	DBPath     string
	AdminToken string
	JudgeToken string
	MaxSource  int64
	MaxUpload  int64
	MaxCase    int64
	BCryptCost int
	Secure     bool
	// AllowRegister gates self-service signup; M10 moves the switch into the
	// site settings the operator can edit in the browser.
	AllowRegister bool
	Verbose       bool
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func defaultDataDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return "data"
	}
	return filepath.Join(wd, "data")
}

func (c *Config) Validate() error {
	if c.Addr == "" {
		return fmt.Errorf("addr must not be empty")
	}
	if c.AdminToken == "" {
		return fmt.Errorf("admin token must not be empty (set -admin-token or FASTOJ_ADMIN_TOKEN)")
	}
	if c.JudgeToken == "" {
		return fmt.Errorf("judge token must not be empty (set -judge-token or FASTOJ_JUDGE_TOKEN)")
	}
	if c.MaxSource <= 0 {
		return fmt.Errorf("max source size must be positive")
	}
	if c.MaxUpload <= 0 || c.MaxCase <= 0 {
		return fmt.Errorf("max upload and max case sizes must be positive")
	}
	if c.MaxCase > c.MaxUpload {
		return fmt.Errorf("max-case must not exceed max-upload")
	}
	if c.BCryptCost < 10 || c.BCryptCost > 14 {
		return fmt.Errorf("bcrypt cost must be 10..14")
	}
	abs, err := filepath.Abs(c.DataDir)
	if err != nil {
		return err
	}
	c.DataDir = abs
	if c.DBPath == "" {
		c.DBPath = filepath.Join(c.DataDir, "fast-oj.db")
	}
	return os.MkdirAll(filepath.Dir(c.DBPath), 0o750)
}

func Load(args []string) (Config, error) {
	var c Config
	fs := flag.NewFlagSet("fast-oj", flag.ContinueOnError)
	fs.StringVar(&c.Addr, "addr", envOr("FASTOJ_ADDR", "127.0.0.1:8080"), "listen address")
	fs.StringVar(&c.DataDir, "data", envOr("FASTOJ_DATA", defaultDataDir()), "data directory")
	fs.StringVar(&c.DBPath, "db", os.Getenv("FASTOJ_DB"), "sqlite file (default <data>/fast-oj.db)")
	fs.StringVar(&c.AdminToken, "admin-token", os.Getenv("FASTOJ_ADMIN_TOKEN"), "token granting problem authoring")
	fs.StringVar(&c.JudgeToken, "judge-token", os.Getenv("FASTOJ_JUDGE_TOKEN"), "token granting judge access")
	fs.Int64Var(&c.MaxSource, "max-source", 256<<10, "maximum accepted source/json body bytes")
	fs.Int64Var(&c.MaxUpload, "max-upload", 64<<20, "maximum case archive upload bytes")
	fs.Int64Var(&c.MaxCase, "max-case", 16<<20, "maximum single case file bytes")
	fs.IntVar(&c.BCryptCost, "bcrypt-cost", 11, "password hash cost (10 is weak, 14 is slow on login)")
	fs.BoolVar(&c.Secure, "secure-cookies", false, "mark session cookies Secure (enable when serving over TLS)")
	noRegister := fs.Bool("no-register", false, "关闭自助注册")
	fs.BoolVar(&c.Verbose, "v", false, "verbose request logging")
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	c.AllowRegister = !*noRegister
	c.DataDir, _ = filepath.Abs(c.DataDir)
	if err := c.Validate(); err != nil {
		return c, err
	}
	return c, nil
}
