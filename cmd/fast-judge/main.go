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

	"github.com/liangbinchen2013/fast-oj/internal/jail"
	"github.com/liangbinchen2013/fast-oj/internal/judge"
	"github.com/liangbinchen2013/fast-oj/internal/judgeagent"
)

func main() {
	// 判机被自己以 jailer 身份拉起时，这一行负责建牢房并 exec 提交程序，永不返回。
	jail.Main()
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fast-judge:", err)
		os.Exit(1)
	}
}

// selfExe 是牢房要 exec 的那个"自己"。/proc/self/exe 在 chroot 之后仍然有效，
// 而且不受工作目录被删之类的破事影响，所以优先用它。
func selfExe() (string, error) {
	if _, err := os.Stat("/proc/self/exe"); err == nil {
		return "/proc/self/exe", nil
	}
	return os.Executable()
}

// jailIdentity 决定牢房里的 uid。非 root 判机走 userns：要挑一个**没有映射**的号，
// 宿主上才会变成 nobody，判机的库与别人的源码才读不到。
func jailIdentity(drop *judge.DropTo, userNS bool) (uint32, uint32) {
	if !userNS {
		if drop != nil {
			return drop.UID, drop.GID
		}
		return nobody, nobody
	}
	uid, gid := uint32(nobody), uint32(nobody)
	if os.Getuid() == int(uid) { // 判机本身就是 nobody：换一个没映射的号
		uid, gid = nobody-1, nobody-1
	}
	return uid, gid
}

const nobody = 65534

// setupSandbox 定下沙盒配置，并当场真建一次牢房。-sandbox=on 而建不起来就直接不启动：
// 明确拒绝比悄悄少一层防护好；auto 则带着"未隔离"这句话继续干活。
func setupSandbox(dir string, mode jail.Mode, drop *judge.DropTo) (string, error) {
	userNS := os.Getuid() != 0
	uid, gid := jailIdentity(drop, userNS)
	self := ""
	if mode != jail.Off {
		path, err := selfExe()
		if err != nil {
			return "", fmt.Errorf("找不到判机自己的可执行文件，沙盒无从 exec：%w", err)
		}
		self = path
	}
	judge.SetSandbox(self, mode, userNS, uid, gid)
	note, err := judge.ProbeSandbox(dir)
	if err != nil && mode == jail.On {
		return "", fmt.Errorf("-sandbox=on 但牢房建不起来：%w", err)
	}
	return note, nil
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
		sbox    = flag.String("sandbox", envOr("FASTOJ_JUDGE_SANDBOX", "auto"), "判题沙盒：auto=能开就开，on=开不起来就不判，off=不隔离")
		verbose = flag.Bool("v", false, "log each step")
	)
	flag.Parse()

	drop, err := parseRunAs(*runAs)
	if err != nil {
		return err
	}
	mode, err := jail.ParseMode(*sbox)
	if err != nil {
		return err
	}
	if drop != nil && runtime.GOOS == "windows" {
		// Refusing beats a silently ignored flag: on Windows there is no way to
		// hand the judged process a different identity here.
		return fmt.Errorf("-run-as 在 Windows 上不生效，判机只能以自己的身份跑；要么去掉它，要么把判机放进容器")
	}
	if mode == jail.On && runtime.GOOS != "linux" {
		return fmt.Errorf("-sandbox=on 需要 Linux 命名空间，%s 上没有；要么用 -sandbox=off，要么换 Linux/容器", runtime.GOOS)
	}
	if runtime.GOOS != "linux" {
		// 非 Linux 没有"降级版"沙盒可言：直接按关闭处理，日志与 /judges 上都会写明。
		mode = jail.Off
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
	// 沙盒配置要在任何一次判题之前定下来：agent.New 已经拿到了工作目录，自测就用它。
	note, sandboxErr := setupSandbox(agent.Dir(), mode, drop)
	if sandboxErr != nil {
		return sandboxErr
	}
	agent.SetNote(note)
	log.Printf("judge %s polling %s for [%v]", *name, *server, agent.Languages())
	log.Printf("隔离边界：%s。环境变量只发最小集，内存/CPU/进程数与超时由判机实测加内核 rlimit 双重兜住。", note)
	if mode == jail.Off {
		log.Printf("警告：沙盒已关闭。提交的代码能读写这个账号能访问的一切并能出网——只在你信得过的机器上这么用。")
	}

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
