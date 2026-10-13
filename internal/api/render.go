package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

// The web UI is the only interface a person gets: no text/plain listings, no
// ?format=json, no tokens to script against. Two surfaces stay machine-shaped
// because their client is a program and not a person: /judge/*, which the judge
// boxes drive with the judge token, and /healthz, which a supervisor polls.

// isJudgeRoute is the whole test for that: the judge protocol answers JSON, and
// every other URL answers HTML.
func isJudgeRoute(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, "/judge/")
}

// writeJSON is the judge protocol's reply shape. Errors carry the same envelope
// so a box never has to guess whether a body is text or JSON.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		return
	}
}

func writeJudgeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg, "status": status})
}

// respondError sends a person the styled error page. A judge box gets JSON, and
// nothing in between: one request, one representation.
func (s *Server) respondError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	if isJudgeRoute(r) {
		writeJudgeError(w, status, msg)
		return
	}
	s.errorPage(w, r, status, msg)
}
