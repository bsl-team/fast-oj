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
	return s.wrap(mux)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 500*time.Millisecond)
	defer cancel()
	if err := s.db.PingContext(ctx); err != nil {
		respondError(w, r, http.StatusServiceUnavailable, "database unreachable")
		return
	}
	respond(w, r, http.StatusOK, "ok\n", map[string]any{"status": "ok"})
}

func (s *Server) handleAbout(w http.ResponseWriter, r *http.Request) {
	uptime := time.Since(s.started).Truncate(time.Second)
	mode, err := s.db.JournalMode(r.Context())
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "database: "+err.Error())
		return
	}
	counts, err := s.db.Counts(r.Context())
	if err != nil {
		respondError(w, r, http.StatusInternalServerError, "database: "+err.Error())
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
		"endpoints",
		"  GET  /healthz            liveness",
		"  GET  /about              this summary",
		"",
		"add ?format=json (or Accept: application/json) for machine output",
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
					respondError(rec.ResponseWriter, r, http.StatusInternalServerError, "internal error")
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

func (s *Server) wrap(next http.Handler) http.Handler {
	return s.logRequests(next)
}
