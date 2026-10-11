package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

// Text is the default representation; JSON is opt-in for tooling.
func wantsJSON(r *http.Request) bool {
	if f := r.URL.Query().Get("format"); f != "" {
		return f == "json"
	}
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

func respond(w http.ResponseWriter, r *http.Request, status int, text string, payload any) {
	if wantsJSON(r) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			return
		}
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	if text == "" {
		text = strings.ToLower(http.StatusText(status))
	}
	_, _ = w.Write([]byte(text))
}

// respondError answers with text or JSON as usual, but a browser gets the
// styled error page instead of a bare line of text.
func (s *Server) respondError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	if wantsHTML(r) {
		s.errorPage(w, r, status, msg)
		return
	}
	respond(w, r, status, msg+"\n", map[string]any{"error": msg, "status": status})
}
