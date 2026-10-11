package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liangbinchen2013/fast-oj/internal/config"
	"github.com/liangbinchen2013/fast-oj/internal/store"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Config{Addr: "127.0.0.1:0", DataDir: dir, AdminToken: "a", JudgeToken: "j", MaxSource: 1024}
	db, err := store.Open(context.Background(), filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), db).Handler())
	t.Cleanup(func() {
		hs.Close()
		db.Close()
	})
	return hs
}

func get(t *testing.T, url string, header map[string]string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestHealthzIsPlainText(t *testing.T) {
	srv := newTestServer(t)
	code, body := get(t, srv.URL+"/healthz", nil)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if body != "ok\n" {
		t.Fatalf("body = %q, want %q", body, "ok\n")
	}
}

func TestAboutTextAndJSON(t *testing.T) {
	srv := newTestServer(t)
	code, body := get(t, srv.URL+"/about", nil)
	if code != http.StatusOK || !strings.Contains(body, "fast-oj") || !strings.Contains(body, "uptime") {
		t.Fatalf("text about = %d %q", code, body)
	}
	if !strings.Contains(body, "counters problems=0") || !strings.Contains(body, "(wal)") {
		t.Fatalf("about missing db status: %q", body)
	}
	if strings.Contains(body, "\"uptime\":") {
		t.Fatalf("text response leaked json")
	}

	code, body = get(t, srv.URL+"/about?format=json", nil)
	if code != http.StatusOK || !strings.Contains(body, `"uptime"`) {
		t.Fatalf("json about = %d %q", code, body)
	}

	code, body = get(t, srv.URL+"/about", map[string]string{"Accept": "application/json"})
	if code != http.StatusOK || !strings.HasPrefix(strings.TrimSpace(body), "{") {
		t.Fatalf("negotiated json = %d %q", code, body)
	}
}

func TestHealthzFailsWhenDatabaseIsGone(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{Addr: "127.0.0.1:0", DataDir: dir, AdminToken: "a", JudgeToken: "j", MaxSource: 1024}
	db, err := store.Open(context.Background(), filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	h := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), db).Handler()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body %q", rec.Code, rec.Body.String())
	}
}

func TestUnknownRouteIs404(t *testing.T) {
	srv := newTestServer(t)
	code, body := get(t, srv.URL+"/nope", nil)
	if code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body %q", code, body)
	}
}

func TestMethodNotAllowedIs405(t *testing.T) {
	srv := newTestServer(t)
	resp, err := http.Post(srv.URL+"/healthz", "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}
