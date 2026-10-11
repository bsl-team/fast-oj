package api

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// gzipMinBytes is the cutoff below which compression is not worth it: the header
// block of a gzip stream alone can exceed a small reply.
const gzipMinBytes = 1024

var gzipWriters = sync.Pool{New: func() any { return gzip.NewWriter(nil) }}

// gzipResponses compresses text replies. It sits outside the logging middleware so
// a recovered panic still reaches the client: the inner handler writes its 500 into
// this buffer, and the buffer is flushed once the call returns.
func gzipResponses(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The reply depends on this header, so say so unconditionally: a shared
		// cache must never hand a gzipped body to a client that cannot decode it,
		// nor the reverse.
		w.Header().Set("Vary", "Accept-Encoding")
		if r.Method == http.MethodHead || !wantsGzip(r) {
			next.ServeHTTP(w, r)
			return
		}
		b := &bufferedReply{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(b, r)
		b.finish()
	})
}

func wantsGzip(r *http.Request) bool {
	for _, token := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		if strings.EqualFold(strings.TrimSpace(token), "gzip") {
			return true
		}
	}
	return false
}

// compressibleType covers the text payloads this server produces. Binary case
// data is not served here, and already-compressed formats would only cost CPU.
func compressibleType(contentType string) bool {
	ct := strings.ToLower(contentType)
	if ct == "" {
		return false
	}
	if strings.HasPrefix(ct, "text/") {
		return true
	}
	for _, t := range []string{"application/json", "application/javascript", "application/xml"} {
		if strings.HasPrefix(ct, t) {
			return true
		}
	}
	return false
}

// bufferedReply collects one response so the compressed size can be compared
// against the original before anything is committed to the wire.
type bufferedReply struct {
	http.ResponseWriter
	body   bytes.Buffer
	status int
}

func (b *bufferedReply) WriteHeader(code int) { b.status = code }

func (b *bufferedReply) Write(p []byte) (int, error) { return b.body.Write(p) }

func (b *bufferedReply) finish() {
	body := b.body.Bytes()
	h := b.ResponseWriter.Header()
	out := body
	encoded := false

	if compressibleType(h.Get("Content-Type")) && len(body) >= gzipMinBytes {
		zw := gzipWriters.Get().(*gzip.Writer)
		var packed bytes.Buffer
		zw.Reset(&packed)
		// A write error here can only be the buffer, which cannot fail.
		_, _ = zw.Write(body)
		_ = zw.Close()
		gzipWriters.Put(zw)
		if packed.Len() > 0 && packed.Len() < len(body) {
			out = packed.Bytes()
			encoded = true
		}
	}

	if encoded {
		h.Set("Content-Encoding", "gzip")
	} else {
		h.Del("Content-Encoding")
	}
	h.Set("Content-Length", strconv.Itoa(len(out)))
	b.ResponseWriter.WriteHeader(b.status)
	_, _ = b.ResponseWriter.Write(out)
}
