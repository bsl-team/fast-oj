package api

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestGzipResponsesCompressesOnlyWhatIsWorthIt(t *testing.T) {
	big := strings.Repeat("题库列表的一行 ", 200)

	cases := []struct {
		name        string
		contentType string
		method      string
		accept      string
		size        int
		wantEncoded bool
	}{
		{"large html", "text/html; charset=utf-8", http.MethodGet, "gzip", len(big), true},
		{"large json", "application/json", http.MethodGet, "gzip", len(big), true},
		{"large text", "text/plain; charset=utf-8", http.MethodGet, "gzip, deflate", len(big), true},
		{"small html", "text/html", http.MethodGet, "gzip", 20, false},
		{"no offer", "text/html", http.MethodGet, "", len(big), false},
		{"identity only", "text/html", http.MethodGet, "br", len(big), false},
		{"head is never encoded", "text/html", http.MethodHead, "gzip", len(big), false},
		{"binary stays binary", "application/octet-stream", http.MethodGet, "gzip", len(big), false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := big[:c.size]
			h := gzipResponses(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", c.contentType)
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, body)
			}))
			req := httptest.NewRequest(c.method, "/", nil)
			req.Header.Set("Accept-Encoding", c.accept)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			resp := rec.Result()
			defer resp.Body.Close()
			raw, _ := io.ReadAll(resp.Body)

			if got := resp.Header.Get("Content-Encoding") == "gzip"; got != c.wantEncoded {
				t.Fatalf("encoded = %v, want %v (%d bytes out)", got, c.wantEncoded, len(raw))
			}
			if cl := resp.Header.Get("Content-Length"); cl != "" && cl != strconv.Itoa(len(raw)) {
				t.Errorf("Content-Length %s does not match the %d bytes on the wire", cl, len(raw))
			}
			if !strings.Contains(resp.Header.Get("Vary"), "Accept-Encoding") {
				t.Error("no Vary: Accept-Encoding, so a shared cache can serve gzip to a client that cannot decode it")
			}
			if c.wantEncoded {
				zr, err := gzip.NewReader(bytes.NewReader(raw))
				if err != nil {
					t.Fatal(err)
				}
				defer zr.Close()
				out, _ := io.ReadAll(zr)
				if string(out) != body {
					t.Errorf("round trip changed the body: %d bytes back, sent %d", len(out), len(body))
				}
				if len(raw) >= len(body) {
					t.Errorf("compressed %d >= original %d", len(raw), len(body))
				}
				return
			}
			if string(raw) != body {
				t.Errorf("body was rewritten by a reply that should have passed through")
			}
		})
	}
}

func TestStaticAssetsAreVersionedAndCachedForever(t *testing.T) {
	h := newHarness(t)

	url := staticURL("style.css")
	if !strings.HasPrefix(url, "/static/style.css?v=") {
		t.Fatalf("staticURL = %q, want a versioned /static path", url)
	}
	if !regexp.MustCompile(`\?v=[0-9a-f]{8}$`).MatchString(url) {
		t.Errorf("version %q is not a short hex digest", url)
	}
	if got := staticURL("missing.css"); strings.Contains(got, "?v=") {
		t.Errorf("unknown asset got a version anyway: %q", got)
	}

	code, body, hdr := h.html(http.MethodGet, url, "", nil, "")
	if code != http.StatusOK || !strings.Contains(body, "--bg") {
		t.Fatalf("versioned asset = %d (%d bytes)", code, len(body))
	}
	if cc := hdr.Get("Cache-Control"); !strings.Contains(cc, "immutable") || !strings.Contains(cc, "max-age=31536000") {
		t.Errorf("Cache-Control = %q, want a year plus immutable", cc)
	}
}
