package api

import (
	"context"
	"log/slog"
	"net/http"
	"runtime"
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
	cache   *siteCache
	started time.Time
}

func New(cfg config.Config, log *slog.Logger, db *store.DB) *Server {
	return &Server{cfg: cfg, log: log, db: db, cases: casefs.New(cfg.DataDir), cache: &siteCache{}, started: time.Now()}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /about", s.handleAbout)

	mux.HandleFunc("GET /problems", s.handleProblemList)
	mux.HandleFunc("GET /problems/new", s.handleProblemNew)
	mux.HandleFunc("POST /problems", s.handleProblemCreate)
	mux.HandleFunc("GET /problems/{key}", s.handleProblemGet)
	mux.HandleFunc("GET /problems/{key}/edit", s.handleProblemEdit)
	mux.HandleFunc("POST /problems/{key}", s.handleProblemUpdate)
	mux.HandleFunc("POST /problems/{key}/delete", s.handleProblemDelete)
	mux.HandleFunc("GET /problems/{key}/cases", s.handleCasesPage)
	mux.HandleFunc("POST /problems/{key}/cases", s.handleCaseCreate)
	mux.HandleFunc("POST /problems/{key}/cases/upload", s.handleCaseUpload)
	mux.HandleFunc("POST /problems/{key}/cases/wipe", s.handleCaseWipe)
	mux.HandleFunc("GET /problems/{key}/cases/{id}/edit", s.handleCaseEditPage)
	mux.HandleFunc("POST /problems/{key}/cases/{id}/edit", s.handleCaseSave)
	mux.HandleFunc("POST /problems/{key}/cases/{id}/delete", s.handleCaseDelete)
	mux.HandleFunc("GET /problems/{key}/cases/{name}/{which}", s.handleCaseBody)
	mux.HandleFunc("GET /problems/{key}/topics", s.handleProblemTopics)

	mux.HandleFunc("GET /topics", s.handleTopicList)
	mux.HandleFunc("GET /topics/new", s.handleTopicNew)
	mux.HandleFunc("POST /topics", s.handleTopicCreate)
	mux.HandleFunc("GET /topics/{id}", s.handleTopicGet)
	mux.HandleFunc("POST /topics/{id}/delete", s.handleTopicDelete)
	mux.HandleFunc("POST /topics/{id}/comment", s.handleTopicComment)
	mux.HandleFunc("POST /topics/comments/{id}/delete", s.handleTopicCommentDelete)

	mux.HandleFunc("GET /submissions", s.handleSubmitList)
	mux.HandleFunc("GET /submissions/{id}", s.handleSubmitGet)

	mux.HandleFunc("GET /contests", s.handleContestList)
	mux.HandleFunc("GET /contests/new", s.handleContestNew)
	mux.HandleFunc("POST /contests", s.handleContestCreate)
	mux.HandleFunc("GET /contests/{key}", s.handleContestHome)
	mux.HandleFunc("GET /contests/{key}/edit", s.handleContestEdit)
	mux.HandleFunc("POST /contests/{key}", s.handleContestUpdate)
	mux.HandleFunc("POST /contests/{key}/delete", s.handleContestDelete)
	mux.HandleFunc("GET /contests/{key}/standings", s.handleContestStandings)
	mux.HandleFunc("GET /contests/{key}/p/{label}", s.handleContestProblemEntry)
	mux.HandleFunc("POST /contests/{key}/join", s.handleContestJoin)
	mux.HandleFunc("POST /contests/{key}/leave", s.handleContestLeave)
	mux.HandleFunc("POST /contests/{key}/teams", s.handleContestTeam)
	mux.HandleFunc("POST /contests/{key}/problems", s.handleContestProblemSave)
	mux.HandleFunc("POST /contests/{key}/problems/{problemKey}/remove", s.handleContestProblemRemove)
	mux.HandleFunc("POST /contests/{key}/problems/{problemKey}/move", s.handleContestProblemMove)

	mux.HandleFunc("POST /judge/claim", s.handleJudgeClaim)
	mux.HandleFunc("GET /judge/cases/{id}/{which}", s.handleJudgeCaseFile)
	mux.HandleFunc("POST /judge/submissions/{id}/status", s.handleJudgeStatus)
	mux.HandleFunc("POST /judge/submissions/{id}/result", s.handleJudgeResult)
	mux.HandleFunc("GET /judges", s.handleJudgeList)

	// The same URLs answer a browser with HTML, so the text API and the web UI
	// never diverge on data or permissions.
	mux.HandleFunc("GET /", s.handleHome)

	mux.HandleFunc("GET /feed", s.handleFeed)
	mux.HandleFunc("POST /feed", s.handlePostCreate)
	mux.HandleFunc("POST /feed/{id}/delete", s.handlePostDelete)
	mux.HandleFunc("POST /feed/{id}/hide", s.handlePostHide)

	mux.HandleFunc("GET /admin/site", s.handleSiteAdmin)
	mux.HandleFunc("POST /admin/site", s.handleSiteSave)

	mux.HandleFunc("GET /messages", s.handleInbox)
	mux.HandleFunc("GET /messages/{handle}", s.handleThread)
	mux.HandleFunc("POST /messages/{handle}", s.handleSend)
	mux.HandleFunc("POST /settings/privacy", s.handlePrivacy)
	mux.HandleFunc("POST /checkin", s.handleCheckIn)

	mux.HandleFunc("GET /rank", s.handleRank)
	mux.HandleFunc("GET /u/{handle}", s.handleUser)
	mux.HandleFunc("GET /u/{handle}/following", s.followListHandler("following"))
	mux.HandleFunc("GET /u/{handle}/followers", s.followListHandler("followers"))
	mux.HandleFunc("POST /u/{handle}/follow", s.followHandler(true))
	mux.HandleFunc("POST /u/{handle}/unfollow", s.followHandler(false))
	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("GET /register", s.handleRegisterPage)
	mux.HandleFunc("POST /register", s.handleRegister)
	mux.HandleFunc("POST /logout", s.handleLogout)

	mux.HandleFunc("GET /settings", s.handleSettings)
	mux.HandleFunc("POST /settings/profile", s.handleProfileSave)
	mux.HandleFunc("POST /settings/password", s.handlePasswordSave)

	// First-run bootstrap: the operator token cannot be presented by a browser, so
	// this pair turns it into a real web login for the admin account.
	mux.HandleFunc("GET /admin/claim", s.handleAdminClaimPage)
	mux.HandleFunc("POST /admin/claim", s.handleAdminClaim)

	mux.HandleFunc("GET /admin/perms", s.handleAdminUsers)
	mux.HandleFunc("GET /admin/perms/u/{handle}", s.handleAdminUser)
	mux.HandleFunc("POST /admin/perms/u/{handle}", s.handleAdminPermChange)
	mux.HandleFunc("POST /admin/users/{handle}/role", s.handleAdminRoleChange)
	mux.HandleFunc("POST /admin/users/{handle}/status", s.handleAdminStatusChange)
	mux.HandleFunc("POST /problems/{key}/submit", s.handleSubmitForm)
	mux.HandleFunc("GET /static/{name}", s.handleStatic)
	return s.wrap(mux)
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	s.errorPage(w, r, http.StatusNotFound, "没有这个页面："+r.URL.Path)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 500*time.Millisecond)
	defer cancel()
	if err := s.db.PingContext(ctx); err != nil {
		http.Error(w, "database unreachable\n", http.StatusServiceUnavailable)
		return
	}
	// A health check is read by a supervisor, not by a person, so it stays one
	// line of text. Everything a human reads is HTML.
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
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

	// The inventory is what an operator reads when they forget a route, so it
	// lives here rather than in a document that can drift.
	routes := []string{
		"  GET  /                        首页：站名 + 首页语 + 动态 + 最新题目",
		"  GET  /problems                题库列表（?q= &limit= &after=）",
		"  GET  /problems/{key}          题面 + 样例 + 提交表单",
		"  GET  /problems/new            出题表单（problem.create）",
		"  POST /problems                建题（problem.create）",
		"  GET  /problems/{key}/edit     改题表单（owner 或 problem.edit.any）",
		"  POST /problems/{key}          保存题目（owner 或 problem.edit.any）",
		"  POST /problems/{key}/delete   删题（owner 或 problem.edit.any）",
		"  GET  /problems/{key}/cases    用例管理页（owner 或 testcase.manage）",
		"  POST /problems/{key}/cases    加一条用例",
		"  POST /problems/{key}/cases/upload  批量导入用例 zip",
		"  POST /problems/{key}/cases/{id}/edit|delete  改用例 / 删用例",
		"  POST /problems/{key}/cases/wipe    清空用例",
		"  GET  /problems/{key}/cases/{name}/{in|out}  用例正文（样例公开）",
		"  GET  /problems/{key}/topics  这道题的求助贴；从这里发帖会自动带上题号",
		"  POST /problems/{key}/submit   提交代码",
		"  GET  /contests                比赛列表（?q= &after= &limit=）",
		"  GET  /contests/{key}          比赛主页：时间、赛制、题目（成员看得到）",
		"  GET  /contests/new            建比赛（contest.create）",
		"  POST /contests                创建比赛",
		"  GET  /contests/{key}/edit     改比赛与挂题（owner 或 contest.manage.any）",
		"  POST /contests/{key}          保存比赛",
		"  POST /contests/{key}/delete   删除比赛（名单与挂题一起没，历史提交保留）",
		"  POST /contests/{key}/problems 挂题 / 改比赛内分值与题号",
		"  POST /contests/{key}/problems/{problemKey}/remove|move  移下 / 调顺序",
		"  POST /contests/{key}/join         加入（私有赛要码）；/leave 退出",
		"  POST /contests/{key}/teams        建队 / 入队 / 退队",
		"  GET  /contests/{key}/standings    该比赛独立榜（OI 取最后一次且赛中不出榜，IOI 取最高分，ACM 首过+罚时）",
		"  GET  /contests/{key}/p/{题号}      比赛内题号入口，带上比赛归属",
		"  GET  /submissions             提交列表（?user= &problem=）",
		"  GET  /submissions/{id}        结果；判题中每 2 秒自动刷新，逐用例行一条条冒出来",
		"  GET  /feed                    动态（?user= &after= &limit=）；POST /feed 发一条",
		"  GET  /topics                  社区贴列表（?q= &after= &limit=）；/topics/new 发帖",
		"  GET  /topics/{id}             帖子正文与评论；POST /topics/{id}/delete 删帖（本人或 topic.moderate）",
		"  POST /topics/{id}/comment     发表评论（登录用户，一小时 60 条）",
		"  POST /topics/comments/{id}/delete  删评论（本人或 topic.moderate）",
		"  POST /feed/{id}/delete        删动态（本人，或持 feed.moderate 删别人的）",
		"  POST /feed/{id}/hide          隐藏/恢复动态（feed.moderate）",
		"  GET  /rank                    排行榜",
		"  GET  /u/{handle}              个人主页：简介 + 动态 + 战绩 + 连续打卡",
		"  GET  /u/{handle}/following    关注列表；/followers 粉丝",
		"  POST /u/{handle}/follow       关注；/unfollow 取消",
		"  GET  /messages                私信收件箱；/messages/{handle} 一对一对话",
		"  POST /messages/{handle}       发一条私信（受收信策略限制）",
		"  GET  /login /register         登录 / 注册",
		"  GET  /settings                个人资料、密码、私信策略",
		"  POST /checkin                 每日打卡（漏卡 n 天扣 3^n 连续天数）",
		"  GET  /admin/site              站点设置（site.settings）",
		"  GET  /admin/claim             首次接管：用管理令牌给 admin 账号设网页密码",
		"  GET  /admin/perms             权限控制台（permission.manage）",
		"  POST /admin/perms/u/{handle}  授予/撤销/清除单项能力",
		"  POST /admin/users/{handle}/role|status  改角色 / 封禁解禁",
		"  GET  /judges                  判机框列表（judge.view）",
		"  GET  /healthz                 探活",
		"",
		"  判机内部协议（只有判机令牌能用，网页上没有入口）：",
		"  POST /judge/claim             心跳 + 领取工单",
		"  GET  /judge/cases/{id}/{which} 取用例正文",
		"  POST /judge/submissions/{id}/status|result  逐用例回报 / 终局结果",
	}

	s.page(w, r, http.StatusOK, "运行概览", "about", s.viewerOf(r), msgOf(r), map[string]any{
		"Uptime":   uptime.String(),
		"Go":       runtime.Version(),
		"Platform": runtime.GOOS + "/" + runtime.GOARCH,
		"Listen":   s.cfg.Addr,
		"Data":     s.cfg.DataDir,
		"DB":       s.db.Path,
		"Journal":  mode,
		"Counts":   counts,
		"Routes":   routes,
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
	return gzipResponses(s.logRequests(s.guardCSRF(next)))
}
