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
	Verbose    bool
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
	fs.Int64Var(&c.MaxSource, "max-source", 256<<10, "maximum accepted source bytes")
	fs.BoolVar(&c.Verbose, "v", false, "verbose request logging")
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	if err := c.Validate(); err != nil {
		return c, err
	}
	return c, nil
}
