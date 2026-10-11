package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/liangbinchen2013/fast-oj/internal/casefs"
	"github.com/liangbinchen2013/fast-oj/internal/config"
	"github.com/liangbinchen2013/fast-oj/internal/store"
)

type Server struct {
	cfg     config.Config
	log     *slog.Logger
	db      *store.DB
	cases   *casefs.Store
	started time.Time
}

func New(cfg config.Config, log *slog.Logger, db *store.DB) *Server {
	return &Server{cfg: cfg, log: log, db: db, cases: casefs.New(cfg.DataDir), started: time.Now()}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /about", s.handleAbout)

	mux.HandleFunc("GET /problems", s.handleProblemList)
	mux.HandleFunc("POST /problems", s.handleProblemCreate)
	mux.HandleFunc("GET /problems/{key}", s.handleProblemGet)
	mux.HandleFunc("PUT /problems/{key}", s.handleProblemUpdate)
	mux.HandleFunc("DELETE /problems/{key}", s.handleProblemDelete)
	mux.HandleFunc("GET /problems/{key}/cases", s.handleCaseList)
	mux.HandleFunc("POST /problems/{key}/cases", s.handleCaseUpload)
	mux.HandleFunc("DELETE /problems/{key}/cases", s.handleCaseWipe)
	mux.HandleFunc("GET /problems/{key}/cases/{name}/{which}", s.handleCaseBody)

	mux.HandleFunc("GET /whoami", s.handleWhoami)
	mux.HandleFunc("GET /users", s.handleUserList)
	mux.HandleFunc("POST /users", s.handleUserCreate)
	mux.HandleFunc("POST /users/{handle}/tokens", s.handleTokenCreate)

	mux.HandleFunc("POST /submissions", s.handleSubmitCreate)
	mux.HandleFunc("GET /submissions", s.handleSubmitList)
	mux.HandleFunc("GET /submissions/{id}", s.handleSubmitGet)

	mux.HandleFunc("POST /judge/claim", s.handleJudgeClaim)
	mux.HandleFunc("GET /judge/cases/{id}/{which}", s.handleJudgeCaseFile)
	mux.HandleFunc("POST /judge/submissions/{id}/status", s.handleJudgeStatus)
	mux.HandleFunc("POST /judge/submissions/{id}/result", s.handleJudgeResult)
	mux.HandleFunc("GET /judges", s.handleJudgeList)

	// The same URLs answer a browser with HTML, so the text API and the web UI
	// never diverge on data or permissions.
	mux.HandleFunc("GET /", s.handleRoot)
	mux.HandleFunc("GET /rank", s.handleRank)
	mux.HandleFunc("GET /u/{handle}", s.handleUser)
	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("GET /register", s.handleRegisterPage)
	mux.HandleFunc("POST /register", s.handleRegister)
	mux.HandleFunc("POST /logout", s.handleLogout)

	mux.HandleFunc("GET /settings", s.handleSettings)
	mux.HandleFunc("POST /settings/profile", s.handleProfileSave)
	mux.HandleFunc("POST /settings/password", s.handlePasswordSave)
	mux.HandleFunc("POST /settings/tokens", s.handleTokenIssue)
	mux.HandleFunc("POST /settings/tokens/revoke", s.handleTokenRevoke)
	mux.HandleFunc("GET /account", s.handleAccountAlias)
	mux.HandleFunc("POST /problems/{key}/submit", s.handleSubmitForm)
	mux.HandleFunc("GET /static/{name}", s.handleStatic)
	return s.wrap(mux)
}

// handleRoot serves the problem list at / and turns every other unmatched GET
// into a 404 that a browser still sees as a page.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		s.notFound(w, r)
		return
	}
	s.handleProblemList(w, r)
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	if wantsHTML(r) {
		s.errorPage(w, r, http.StatusNotFound, "没有这个页面："+r.URL.Path)
		return
	}
	s.respondError(w, r, http.StatusNotFound, "no route for "+r.URL.Path)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 500*time.Millisecond)
	defer cancel()
	if err := s.db.PingContext(ctx); err != nil {
		s.respondError(w, r, http.StatusServiceUnavailable, "database unreachable")
		return
	}
	respond(w, r, http.StatusOK, "ok\n", map[string]any{"status": "ok"})
}

func (s *Server) handleAbout(w http.ResponseWriter, r *http.Request) {
	uptime := time.Since(s.started).Truncate(time.Second)
	mode, err := s.db.JournalMode(r.Context())
	if err != nil {
		s.respondError(w, r, http.StatusInternalServerError, "database: "+err.Error())
		return
	}
	counts, err := s.db.Counts(r.Context())
	if err != nil {
		s.respondError(w, r, http.StatusInternalServerError, "database: "+err.Error())
		return
	}

	text := strings.Join([]string{
		"fast-oj",
		"uptime   " + uptime.String(),
		fmt.Sprintf("go       %s %s/%s", runtime.Version(), runtime.GOOS, runtime.GOARCH),
		"listen   " + s.cfg.Addr,
		"data     " + s.cfg.DataDir,
		"db       " + s.db.Path + " (" + mode + ")",
		fmt.Sprintf("counters problems=%d submissions=%d pending=%d users=%d judges=%d",
			counts.Problems, counts.Submissions, counts.Pending, counts.Users, counts.Judges),
		"",
		"routes  (浏览器拿 HTML，curl 拿纯文本，?format=json 拿 JSON)",
		"  GET  /                        题库列表",
		"  GET  /problems                题库列表（?q= &limit= &after=）",
		"  GET  /problems/{key}          题面 + 样例 + 提交表单",
		"  POST /problems                建题（admin）",
		"  PUT  /problems/{key}          改题（admin）",
		"  DELETE /problems/{key}        删题（admin）",
		"  POST /problems/{key}/cases    上传用例 zip（admin）",
		"  GET  /problems/{key}/cases    用例清单（admin）",
		"  POST /problems/{key}/submit   网页表单提交代码",
		"  POST /submissions             提交（bearer）",
		"  GET  /submissions             提交列表（?user= &problem=）",
		"  GET  /submissions/{id}        结果（?wait=秒 长轮询）",
		"  GET  /rank                    排行榜",
		"  GET  /u/{handle}              个人主页",
		"  GET  /login /register         登录 / 注册（邮箱+密码，邮箱暂不验证）",
		"  GET  /settings                个人资料、密码、API 令牌",
		"  GET  /whoami                  当前身份",
		"  GET  /judges                  判机框列表（admin）",
		"  POST /judge/claim             判机：心跳 + 领取",
		"  GET  /judge/cases/{id}/{which} 判机：取用例正文",
		"  POST /judge/submissions/{id}/status|result  判机：回报",
		"  GET  /healthz  GET /about     运维",
	}, "\n") + "\n"

	respond(w, r, http.StatusOK, text, map[string]any{
		"uptime":   uptime.String(),
		"go":       runtime.Version(),
		"os":       runtime.GOOS,
		"arch":     runtime.GOARCH,
		"listen":   s.cfg.Addr,
		"data":     s.cfg.DataDir,
		"db":       s.db.Path,
		"journal":  mode,
		"counters": counts,
	})
}

type recorded struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *recorded) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorded) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(p)
	r.bytes += n
	return n, err
}

// Access logging is deliberately quiet: only failures and latency outliers are
// written out, so the log cannot dominate request cost.
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &recorded{ResponseWriter: w}
		defer func() {
			if p := recover(); p != nil {
				s.log.Error("panic", "path", r.URL.Path, "panic", p)
				if rec.status == 0 {
					s.respondError(rec.ResponseWriter, r, http.StatusInternalServerError, "internal error")
				}
				return
			}
			dur := time.Since(start)
			status := rec.status
			if status == 0 {
				status = http.StatusOK
			}
			if s.cfg.Verbose || status >= 400 || dur > 50*time.Millisecond {
				s.log.Info("req",
					"method", r.Method,
					"path", r.URL.Path,
					"status", status,
					"ms", float64(dur.Microseconds())/1000,
					"bytes", rec.bytes,
				)
			}
		}()
		next.ServeHTTP(rec, r)
	})
}

// wrap orders the chain: log first so a rejected request is still recorded,
// then the CSRF gate, then the handler.
func (s *Server) wrap(next http.Handler) http.Handler {
	return s.logRequests(s.guardCSRF(next))
}
