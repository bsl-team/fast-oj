package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/liangbinchen2013/fast-oj/internal/judgeagent"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fast-judge:", err)
		os.Exit(1)
	}
}

func defaultName() string {
	if host, err := os.Hostname(); err == nil && host != "" {
		return host
	}
	return "judge-1"
}

func run() error {
	var (
		server  = flag.String("server", envOr("FASTOJ_SERVER", "http://127.0.0.1:8080"), "fast-oj base URL")
		token   = flag.String("token", os.Getenv("FASTOJ_JUDGE_TOKEN"), "judge token (required)")
		name    = flag.String("name", envOr("FASTOJ_JUDGE_NAME", defaultName()), "judge box name")
		dir     = flag.String("dir", os.Getenv("FASTOJ_JUDGE_DIR"), "work directory, default under the user cache dir")
		poll    = flag.Duration("poll", 500*time.Millisecond, "wait between empty queue polls")
		backoff = flag.Duration("backoff", 3*time.Second, "wait after a failed round")
		once    = flag.Bool("once", false, "process at most one submission, then exit")
		tools   = flag.String("tools", "", "comma list of languages to advertise (default: everything usable)")
		verbose = flag.Bool("v", false, "log each step")
	)
	flag.Parse()

	logf := func(format string, args ...any) {
		if *verbose {
			log.Printf(format, args...)
		}
	}
	langs := splitCSV(*tools)

	agent, err := judgeagent.New(judgeagent.Config{
		Server: *server, Token: *token, Name: *name, Dir: *dir,
		Poll: *poll, Backoff: *backoff, Languages: langs, Logf: logf,
	})
	if err != nil {
		return err
	}
	log.Printf("judge %s polling %s for [%v]", *name, *server, agent.Languages())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *once {
		worked, err := agent.ProcessOnce(ctx)
		if err != nil {
			return err
		}
		if !worked {
			log.Printf("queue empty")
		}
		return nil
	}

	if err := agent.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func splitCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
