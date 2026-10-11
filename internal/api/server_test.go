package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/liangbinchen2013/fast-oj/internal/config"
	"github.com/liangbinchen2013/fast-oj/internal/store"
)

func ctx() context.Context { return context.Background() }

// testConfig mirrors what Load would produce, without touching the process env.
func testConfig(t *testing.T) config.Config {
	t.Helper()
	dir := t.TempDir()
	return config.Config{
		Addr:       "127.0.0.1:0",
		DataDir:    dir,
		DBPath:     dir + "/test.db",
		AdminToken: "admin-secret",
		JudgeToken: "judge-secret",
		MaxSource:  1 << 20,
		MaxUpload:  1 << 20,
		MaxCase:    1 << 20,
	}
}

type harness struct {
	t     *testing.T
	srv   *httptest.Server
	db    *store.DB
	cfg   config.Config
	admin string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	cfg := testConfig(t)
	db, err := store.Open(ctx(), cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	handler := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), db).Handler()
	hs := httptest.NewServer(handler)
	t.Cleanup(func() {
		hs.Close()
		db.Close()
	})
	return &harness{t: t, srv: hs, db: db, cfg: cfg, admin: cfg.AdminToken}
}

// do issues a request and returns status plus body as text.
func (h *harness) do(method, path, token string, body []byte, contentType string) (int, string) {
	h.t.Helper()
	var reader io.Reader
	if body != nil {
		reader = strings.NewReader(string(body))
	}
	req, err := http.NewRequest(method, h.srv.URL+path, reader)
	if err != nil {
		h.t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := h.srv.Client().Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

func (h *harness) get(path, token string) (int, string) {
	return h.do(http.MethodGet, path, token, nil, "")
}

func (h *harness) json(method, path, token string, payload []byte) (int, string) {
	return h.do(method, path, token, payload, "application/json")
}

func TestHealthzIsPlainText(t *testing.T) {
	h := newHarness(t)
	code, body := h.get("/healthz", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if body != "ok\n" {
		t.Fatalf("body = %q, want %q", body, "ok\n")
	}
}

func TestAboutTextAndJSON(t *testing.T) {
	h := newHarness(t)
	code, body := h.get("/about", "")
	if code != http.StatusOK || !strings.Contains(body, "fast-oj") || !strings.Contains(body, "uptime") {
		t.Fatalf("text about = %d %q", code, body)
	}
	if !strings.Contains(body, "counters problems=0") || !strings.Contains(body, "(wal)") {
		t.Fatalf("about missing db status: %q", body)
	}
	if strings.Contains(body, "\"uptime\":") {
		t.Fatal("text response leaked json")
	}

	code, body = h.get("/about?format=json", "")
	if code != http.StatusOK || !strings.Contains(body, `"uptime"`) {
		t.Fatalf("json about = %d %q", code, body)
	}

	code, body = h.get("/about", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
}

func TestAboutNegotiatesJSONByAccept(t *testing.T) {
	h := newHarness(t)
	req, err := http.NewRequest(http.MethodGet, h.srv.URL+"/about", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := h.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(strings.TrimSpace(string(body)), "{") {
		t.Fatalf("negotiated json = %d %q", resp.StatusCode, body)
	}
}

func TestHealthzFailsWhenDatabaseIsGone(t *testing.T) {
	cfg := testConfig(t)
	db, err := store.Open(ctx(), cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	handler := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), db).Handler()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body %q", rec.Code, rec.Body.String())
	}
}

func TestUnknownRouteIs404(t *testing.T) {
	h := newHarness(t)
	code, body := h.get("/nope", "")
	if code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body %q", code, body)
	}
}

func TestMethodNotAllowedIs405(t *testing.T) {
	h := newHarness(t)
	code, _ := h.do(http.MethodPost, "/healthz", "", []byte("x"), "text/plain")
	if code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", code)
	}
}
