package server

import (
	"bytes"
	"mime"
	"net/http"
	"strings"
	"time"

	"tibi/internal/blob"
	"tibi/internal/id"
	"tibi/internal/store"
	"tibi/internal/units"
)

const (
	rawCSP  = "default-src 'none'; sandbox"
	viewCSP = "default-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
)

// handleItemRoutes dispatches the frozen item URL scheme (PLAN A3).
func (s *Server) handleItemRoutes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	switch {
	case len(parts) == 1:
		s.handleItemOrRaw(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "raw":
		s.handleRaw(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "dl":
		s.handleDownload(w, r, parts[0])
	default:
		notFound(w)
	}
}

// handleItemOrRaw serves /{id} (view page) and /{id}.{ext} (raw bytes).
func (s *Server) handleItemOrRaw(w http.ResponseWriter, r *http.Request, name string) {
	itemID, ext, hasExt := strings.Cut(name, ".")
	if !id.Valid(itemID) {
		notFound(w)
		return
	}
	it, err := s.lookup(r, itemID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if hasExt {
		if !strings.EqualFold(ext, it.Ext) {
			notFound(w)
			return
		}
		s.serveBlob(w, r, it, false)
		return
	}
	s.renderView(w, it)
}

func (s *Server) handleRaw(w http.ResponseWriter, r *http.Request, itemID string) {
	it, err := s.lookup(r, itemID)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.serveBlob(w, r, it, false)
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request, itemID string) {
	it, err := s.lookup(r, itemID)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.serveBlob(w, r, it, true)
}

func (s *Server) lookup(r *http.Request, itemID string) (*store.Item, error) {
	if !id.Valid(itemID) {
		return nil, store.ErrNotFound
	}
	it, err := s.store.GetItem(r.Context(), itemID)
	if err != nil {
		return nil, err
	}
	if it.Visibility == "private" {
		// Private access (read_private tokens, signed links) arrives in 0.0.4/0.0.9.
		return nil, store.ErrNotFound
	}
	return it, nil
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	if isNotFound(err) {
		notFound(w)
		return
	}
	s.log.Error("lookup", "err", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

// activeTypes must never be served with their real type (PLAN A8.1).
var activeTypes = map[string]bool{
	"text/html":                true,
	"application/xhtml+xml":    true,
	"image/svg+xml":            true,
	"text/xml":                 true,
	"application/xml":          true,
	"text/javascript":          true,
	"application/javascript":   true,
	"application/ecmascript":   true,
	"text/ecmascript":          true,
	"application/x-javascript": true,
}

// passiveTypes are served inline with their real type.
var passiveTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true,
	"image/avif": true, "image/bmp": true, "image/x-icon": true,
	"video/mp4": true, "video/webm": true, "video/quicktime": true,
	"audio/mpeg": true, "audio/ogg": true, "audio/opus": true, "audio/mp4": true,
	"audio/wav": true, "audio/wave": true, "audio/flac": true, "audio/aac": true,
}

// RawContentType returns the Content-Type used on raw (inline) routes.
func RawContentType(mt string) string {
	mt = strings.ToLower(mt)
	switch {
	case activeTypes[mt], strings.HasPrefix(mt, "text/"), strings.HasSuffix(mt, "+xml"), strings.HasSuffix(mt, "+json"), mt == "application/json":
		return "text/plain; charset=utf-8"
	case passiveTypes[mt]:
		return mt
	}
	return "application/octet-stream"
}

func (s *Server) serveBlob(w http.ResponseWriter, r *http.Request, it *store.Item, download bool) {
	f, err := s.blobs.Open(r.Context(), blob.Key(it.SHA256))
	if err != nil {
		s.fail(w, err)
		return
	}
	defer f.Close()
	h := w.Header()
	h.Set("Content-Security-Policy", rawCSP)
	h.Set("Cross-Origin-Resource-Policy", "cross-origin")
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	if download {
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": downloadName(it)}))
	} else {
		h.Set("Content-Type", RawContentType(it.Mime))
		h.Set("Content-Disposition", "inline")
	}
	http.ServeContent(w, r, "", time.Unix(it.CreatedAt, 0), f)
}

func downloadName(it *store.Item) string {
	if it.Filename != "" {
		return it.Filename
	}
	return it.ID + "." + it.Ext
}

type viewModel struct {
	Title       string
	Item        *store.Item
	Size        string
	RawURL      string
	DownloadURL string
}

func (s *Server) renderView(w http.ResponseWriter, it *store.Item) {
	title := it.Title
	if title == "" {
		title = downloadName(it)
	}
	vm := viewModel{
		Title:       title,
		Item:        it,
		Size:        units.FormatBytes(it.Size),
		RawURL:      s.cfg.RawBase() + "/" + it.ID + "/raw",
		DownloadURL: s.cfg.RawBase() + "/" + it.ID + "/dl",
	}
	var buf bytes.Buffer
	if err := s.pages["file"].ExecuteTemplate(&buf, "layout", vm); err != nil {
		s.log.Error("render", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", viewCSP)
	h.Set("X-Frame-Options", "DENY")
	h.Set("Cache-Control", "no-cache")
	w.Write(buf.Bytes())
}
