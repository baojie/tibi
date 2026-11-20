package server

import (
	"encoding/json"
	"html/template"
	"net/http"

	"tibi/internal/i18n"
)

var pageNames = []string{"file", "image", "text", "burn", "video", "audio", "collection", "upload", "recent"}

// templateFuncs binds the catalog for one locale.
func templateFuncs(c *i18n.Catalog, loc string) template.FuncMap {
	return template.FuncMap{
		"t":    func(key string, args ...any) string { return c.T(loc, key, args...) },
		"tn":   func(key string, n int) string { return c.N(loc, key, n) },
		"lang": func() string { return loc },
		// Strings for page scripts, as JSON in a data attribute (no inline
		// scripts under the CSP).
		"jsI18n": func() string {
			b, _ := json.Marshal(c.Subset(loc, "action.", "fork.", "upload.", "token.", "recent.", "meta.", "label."))
			return string(b)
		},
	}
}

// locale picks the UI language for a request.
func (s *Server) locale(r *http.Request) string {
	return s.i18n.Match(r.Header.Get("Accept-Language"), s.cfg.DefaultLocale)
}

// handleOwnerPage serves the static upload and recent pages. They hold no
// data: their scripts call the API with the token kept in the browser.
func (s *Server) handleOwnerPage(page, titleKey string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.writePage(w, r, page, struct {
			OG    *ogMeta
			Title string
		}{nil, s.i18n.T(s.locale(r), titleKey)}, false)
	}
}
