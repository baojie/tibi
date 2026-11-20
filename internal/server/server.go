// Package server implements tibi's HTTP API and public routes.
package server

import (
	"encoding/json"
	"errors"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"tibi/api"
	"tibi/internal/blob"
	"tibi/internal/config"
	"tibi/internal/ingest"
	"tibi/internal/render"
	"tibi/internal/store"
	"tibi/web"
)

// Server holds dependencies for the HTTP handlers.
type Server struct {
	cfg     config.Server
	store   *store.Store
	blobs   blob.Store
	ingest  *ingest.Pipeline
	pages   map[string]*template.Template
	tempDir string
	// renderCache holds rendered text views (content is immutable).
	renderCache *render.Cache
	chromaCSS   []byte
	log         *slog.Logger
	mux         *http.ServeMux
}

// New builds the HTTP handler.
func New(cfg config.Server, st *store.Store, bs blob.Store, tempDir string, log *slog.Logger) (*Server, error) {
	if log == nil {
		log = slog.Default()
	}
	pages := map[string]*template.Template{}
	for _, name := range []string{"file", "image", "text", "burn", "video", "audio", "collection"} {
		t, err := template.ParseFS(web.Templates, "templates/layout.html", "templates/"+name+".html")
		if err != nil {
			return nil, err
		}
		pages[name] = t
	}
	var heic *ingest.Converter
	if cfg.ConvertHEIC {
		c, err := ingest.DetectConverter(cfg.HEICConverter)
		if err != nil {
			log.Warn("HEIC conversion disabled", "err", err)
		} else if c == nil {
			log.Info("no HEIC converter found (vips, heif-convert or magick); HEIC photos are stored as-is")
		} else {
			log.Info("HEIC converter", "tool", c.Name)
		}
		heic = c
	}
	s := &Server{
		cfg:   cfg,
		store: st,
		blobs: bs,
		ingest: &ingest.Pipeline{Store: st, Blobs: bs, TempDir: tempDir, StripImageMeta: cfg.StripImageMeta,
			HEIC: heic, Log: log},
		tempDir:     tempDir,
		renderCache: render.NewCache(64 << 20),
		chromaCSS:   render.CSS(),
		pages:       pages,
		log:         log,
		mux:         http.NewServeMux(),
	}
	s.routes()
	return s, nil
}

func (s *Server) routes() {
	static, _ := fs.Sub(web.Static, "static")
	m := s.mux
	m.HandleFunc("GET /healthz", s.handleHealthz)
	m.HandleFunc("GET /robots.txt", s.handleRobots)
	m.HandleFunc("GET /static/chroma.css", s.handleChromaCSS)
	m.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))

	m.HandleFunc("GET /api/v1/openapi.yaml", s.handleOpenAPI)
	m.Handle("GET /api/v1/whoami", s.auth("", http.HandlerFunc(s.handleWhoami)))
	m.Handle("POST /api/v1/items", s.auth(store.ScopeUpload, http.HandlerFunc(s.handleUpload)))
	m.Handle("GET /api/v1/items", s.auth(store.ScopeUpload, http.HandlerFunc(s.handleList)))
	m.Handle("POST /api/v1/items/{id}/fork", s.auth(store.ScopeUpload, http.HandlerFunc(s.handleFork)))
	m.Handle("POST /api/v1/collections", s.auth(store.ScopeUpload, http.HandlerFunc(s.handleCreateCollection)))
	m.Handle("GET /api/v1/collections", s.auth(store.ScopeUpload, http.HandlerFunc(s.handleListCollections)))
	m.Handle("GET /api/v1/collections/{id}", s.auth(store.ScopeUpload, http.HandlerFunc(s.handleGetCollection)))
	m.Handle("PATCH /api/v1/collections/{id}", s.auth(store.ScopeUpload, http.HandlerFunc(s.handlePatchCollection)))
	m.Handle("DELETE /api/v1/collections/{id}", s.auth(store.ScopeDelete, http.HandlerFunc(s.handleDeleteCollection)))
	m.Handle("GET /api/v1/search", s.auth(store.ScopeUpload, http.HandlerFunc(s.handleSearch)))
	m.Handle("GET /api/v1/items/{id}", s.auth(store.ScopeUpload, http.HandlerFunc(s.handleGetItem)))
	m.Handle("PATCH /api/v1/items/{id}", s.auth(store.ScopeUpload, http.HandlerFunc(s.handlePatchItem)))
	m.Handle("DELETE /api/v1/items/{id}", s.auth(store.ScopeDelete, http.HandlerFunc(s.handleDeleteItem)))
	m.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		apiError(w, http.StatusNotFound, "not_found", "no such endpoint")
	})

	m.HandleFunc("GET /{$}", s.handleIndex)
	m.HandleFunc("GET /s/{slug}", s.handleSlug)
	// Item routes (/{id}, /{id}.{ext}, /{id}/raw, /{id}/dl) are dispatched by
	// hand: wildcard patterns would conflict with the /static/ and /api/ prefixes.
	m.HandleFunc("/", s.handleItemRoutes)
}

// ServeHTTP applies global middleware.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	h.Set("Referrer-Policy", "no-referrer")
	sw := &statusWriter{ResponseWriter: w, status: 200}
	s.mux.ServeHTTP(sw, r)
	s.log.Info("http", "method", r.Method, "path", r.URL.Path, "status", sw.status,
		"bytes", sw.n, "dur_ms", time.Since(start).Milliseconds())
}

type statusWriter struct {
	http.ResponseWriter
	status int
	n      int64
	wrote  bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wrote {
		w.status, w.wrote = code, true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	w.wrote = true
	n, err := w.ResponseWriter.Write(b)
	w.n += int64(n)
	return n, err
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DB().PingContext(r.Context()); err != nil {
		http.Error(w, "db unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte("ok\n"))
}

func (s *Server) handleRobots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte("User-agent: *\nDisallow: /\n"))
}

func (s *Server) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Write(api.OpenAPI)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte("tibi\n"))
}

// apiError writes the standard JSON error envelope.
func apiError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": msg}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

func notFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	w.Write([]byte("not found\n"))
}

func isNotFound(err error) bool {
	return errors.Is(err, store.ErrNotFound) || errors.Is(err, blob.ErrNotFound)
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}
