package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/liangbinchen2013/fast-oj/internal/judge"
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
		runAs   = flag.String("run-as", envOr("FASTOJ_JUDGE_RUN_AS", ""), "run submissions as this uid:gid (Linux/containers; empty keeps the judge's own user)")
		verbose = flag.Bool("v", false, "log each step")
	)
	flag.Parse()

	drop, err := parseRunAs(*runAs)
	if err != nil {
		return err
	}
	if drop != nil && runtime.GOOS == "windows" {
		// Refusing beats a silently ignored flag: on Windows there is no way to
		// hand the judged process a different identity here.
		return fmt.Errorf("-run-as 在 Windows 上不生效，判机只能以自己的身份跑；要么去掉它，要么把判机放进容器")
	}

	logf := func(format string, args ...any) {
		if *verbose {
			log.Printf(format, args...)
		}
	}
	langs := splitCSV(*tools)

	agent, err := judgeagent.New(judgeagent.Config{
		Server: *server, Token: *token, Name: *name, Dir: *dir,
		Poll: *poll, Backoff: *backoff, Languages: langs, Logf: logf, RunAs: drop,
	})
	if err != nil {
		return err
	}
	log.Printf("judge %s polling %s for [%v]", *name, *server, agent.Languages())
	// Said out loud once at startup, because the default is not a sandbox: the
	// judged program runs as this user, with this user's file access.
	log.Printf("隔离边界：判题进程继承 fast-judge 的用户身份，仅限内存/CPU/进程数并只发最小环境变量；文件系统与网络不设边界。请用专用账号、容器或虚拟机运行判机。")

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

// parseRunAs turns the operator's "uid:gid" into a drop target. A malformed value
// fails at startup rather than on the first submission; empty means the judged
// program keeps the judge's own user, which is what running without a container
// looks like.
func parseRunAs(raw string) (*judge.DropTo, error) {
	if raw == "" {
		return nil, nil
	}
	uidPart, gidPart, found := strings.Cut(raw, ":")
	if !found {
		return nil, fmt.Errorf("-run-as wants uid:gid, got %q", raw)
	}
	uid, err := strconv.ParseUint(strings.TrimSpace(uidPart), 10, 32)
	if err != nil {
		return nil, fmt.Errorf("-run-as uid: %w", err)
	}
	gid, err := strconv.ParseUint(strings.TrimSpace(gidPart), 10, 32)
	if err != nil {
		return nil, fmt.Errorf("-run-as gid: %w", err)
	}
	return &judge.DropTo{UID: uint32(uid), GID: uint32(gid)}, nil
}
