package server

import (
	"net/http"
	"strings"
)

// CORS for /api/ (PLAN A12): browser clients on other origins — the Chrome
// extension (chrome-extension://<id>), a web app — may call the API when
// their origin is listed in cors_origins ("*" allows any origin; that is
// safe because the API authenticates with bearer tokens, not cookies).

const (
	corsAllowHeaders  = "Authorization, Content-Type, Tus-Resumable, Upload-Length, Upload-Metadata, Upload-Offset, Upload-Defer-Length, X-Tibi-Filename, X-Tibi-Title, X-Tibi-Kind, X-Tibi-Lang, X-Tibi-Expires, X-Tibi-Visibility, X-Tibi-Slug, X-Tibi-Burn-After-Read, X-Tibi-Keep-Meta, X-Tibi-Source, X-Tibi-Source-Url, X-Tibi-Meta"
	corsExposeHeaders = "Location, Upload-Offset, Upload-Length, Tus-Resumable, Tibi-Item-Id, Tibi-Item-Url"
	corsAllowMethods  = "GET, POST, PATCH, DELETE, HEAD, OPTIONS"
)

func (s *Server) corsAllowed(origin string) bool {
	for _, o := range s.cfg.CORSOrigins {
		if o == "*" || strings.EqualFold(strings.TrimRight(o, "/"), origin) {
			return true
		}
	}
	return false
}

// cors adds CORS headers for allowed origins and answers preflights. It
// reports true when the request was a preflight and has been answered.
func (s *Server) cors(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		return false
	}
	origin := r.Header.Get("Origin")
	h := w.Header()
	h.Add("Vary", "Origin")
	if origin == "" || !s.corsAllowed(origin) {
		return false
	}
	h.Set("Access-Control-Allow-Origin", origin)
	h.Set("Access-Control-Expose-Headers", corsExposeHeaders)
	if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
		h.Set("Access-Control-Allow-Methods", corsAllowMethods)
		h.Set("Access-Control-Allow-Headers", corsAllowHeaders)
		h.Set("Access-Control-Max-Age", "600")
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	return false
}
