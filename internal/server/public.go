package server

import (
	"bytes"
	"encoding/base64"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"tibi/internal/blob"
	"tibi/internal/id"
	"tibi/internal/store"
	"tibi/internal/units"
)

const (
	rawCSP = "default-src 'none'; sandbox"
)

// handleItemRoutes dispatches the frozen item URL scheme (PLAN A3).
func (s *Server) handleItemRoutes(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) == 2 && parts[1] == "reveal" {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.handleReveal(w, r, parts[0])
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
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
	it, acc, err := s.lookup(r, itemID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if hasExt {
		if !strings.EqualFold(ext, it.Ext) {
			notFound(w)
			return
		}
		s.serveRaw(w, r, it, acc, false)
		return
	}
	if it.BurnAfterRead && !acc.privileged {
		s.renderBurnNotice(w, it)
		return
	}
	s.renderView(w, r, it, viewOpts{})
}

func (s *Server) handleRaw(w http.ResponseWriter, r *http.Request, itemID string) {
	it, acc, err := s.lookup(r, itemID)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.serveRaw(w, r, it, acc, false)
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request, itemID string) {
	it, acc, err := s.lookup(r, itemID)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.serveRaw(w, r, it, acc, true)
}

// serveRaw serves bytes, except that burn-after-read items are never served
// raw to unprivileged clients: chat-app link previewers prefetch raw URLs,
// and only the explicit reveal may consume the item.
func (s *Server) serveRaw(w http.ResponseWriter, r *http.Request, it *store.Item, acc access, download bool) {
	if it.BurnAfterRead && !acc.privileged {
		notFound(w)
		return
	}
	s.serveBlob(w, r, it, download)
}

// access describes what the requester may see.
type access struct {
	// privileged requests carry a token with read_private: they see private
	// items, and burn-after-read items without consuming them.
	privileged bool
}

func (s *Server) requestAccess(r *http.Request) access {
	secret := bearer(r)
	if secret == "" {
		return access{}
	}
	t, err := s.store.Authenticate(r.Context(), secret)
	if err != nil {
		return access{}
	}
	return access{privileged: t.Has(store.ScopeReadPrivate)}
}

// lookup returns a live item the requester may access; anything else is
// ErrNotFound, so private, expired and deleted items are indistinguishable
// from ones that never existed.
func (s *Server) lookup(r *http.Request, itemID string) (*store.Item, access, error) {
	if !id.Valid(itemID) {
		return nil, access{}, store.ErrNotFound
	}
	it, err := s.store.GetItem(r.Context(), itemID)
	if err != nil {
		return nil, access{}, err
	}
	var acc access
	if it.Visibility == store.Private || it.BurnAfterRead {
		acc = s.requestAccess(r)
	}
	if it.Visibility == store.Private && !acc.privileged {
		return nil, acc, store.ErrNotFound
	}
	return it, acc, nil
}

// handleSlug redirects /s/{slug} to the item it points to.
func (s *Server) handleSlug(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if !store.ValidSlug(slug) {
		notFound(w)
		return
	}
	it, err := s.store.ResolveSlug(r.Context(), slug)
	if err == nil && it.Visibility == store.Private && !s.requestAccess(r).privileged {
		err = store.ErrNotFound
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.Redirect(w, r, "/"+it.ID, http.StatusFound)
}

// handleReveal shows a burn-after-read item once and deletes it.
func (s *Server) handleReveal(w http.ResponseWriter, r *http.Request, itemID string) {
	it, _, err := s.lookup(r, itemID)
	if err == nil && !it.BurnAfterRead {
		err = store.ErrNotFound
	}
	if err == nil {
		// Claim it first: of concurrent reveals exactly one succeeds. The
		// blob stays until GC, so it can still be read below.
		err = s.store.Burn(r.Context(), itemID)
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	switch it.Kind {
	case store.KindText, store.KindImage:
		s.renderView(w, r, it, viewOpts{burned: true})
	default:
		s.serveBlob(w, r, it, true)
	}
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
	h.Set("Cache-Control", s.rawCacheControl(it))
	if download {
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", ContentDisposition(downloadName(it), it.ID+"."+it.Ext))
	} else {
		h.Set("Content-Type", RawContentType(it.Mime))
		h.Set("Content-Disposition", "inline")
	}
	http.ServeContent(w, r, "", time.Unix(it.CreatedAt, 0), f)
}

// ContentDisposition builds an attachment header carrying both an ASCII
// filename (for tools such as curl -J and wget that ignore RFC 5987) and the
// exact UTF-8 name in filename* (used by browsers).
func ContentDisposition(name, fallback string) string {
	ascii := asciiName(name)
	if ascii != name && alnum(strings.TrimSuffix(ascii, path.Ext(ascii))) < 2 {
		ascii = fallback // e.g. an all-Chinese name: "_ _(_).txt" helps nobody
	}
	v := `attachment; filename="` + ascii + `"`
	if ascii != name {
		v += "; filename*=UTF-8''" + url.PathEscape(name)
	}
	return v
}

// asciiName replaces characters that are not safe in a quoted ASCII
// filename, collapsing runs into one underscore and keeping the extension.
func asciiName(name string) string {
	var b strings.Builder
	under := false
	for _, r := range name {
		ok := r > 0x20 && r < 0x7f && r != '"' && r != '\\' && r != '/' && r != ';' && r != '%'
		if ok || r == ' ' {
			b.WriteRune(r)
			under = false
		} else if !under {
			b.WriteByte('_')
			under = true
		}
	}
	return strings.TrimSpace(b.String())
}

func alnum(s string) int {
	n := 0
	for _, r := range s {
		if r < 0x80 && (r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			n++
		}
	}
	return n
}

// rawCacheControl lets public, permanent content be cached forever, while
// private, burn-after-read and expiring content is not cached past its life.
func (s *Server) rawCacheControl(it *store.Item) string {
	if it.Visibility == store.Private || it.BurnAfterRead {
		return "private, no-store"
	}
	if it.ExpiresAt != nil {
		left := *it.ExpiresAt - s.store.Now().Unix()
		if left < 0 {
			left = 0
		}
		return "public, max-age=" + strconv.FormatInt(min(left, 31536000), 10)
	}
	return "public, max-age=31536000, immutable"
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
	URL         string
	RawURL      string
	EmbedURL    template.URL
	DownloadURL string
	Markdown    string
	Text        *textView
	Expires     string
	// Burned is set when a burn-after-read item is shown on reveal: it is
	// already deleted, so no raw/download links are offered.
	Burned bool
}

type viewOpts struct {
	burned bool
}

func (s *Server) renderView(w http.ResponseWriter, r *http.Request, it *store.Item, o viewOpts) {
	title := it.Title
	if title == "" {
		title = downloadName(it)
	}
	j := s.itemJSON(it)
	vm := viewModel{
		Title:       title,
		Item:        it,
		Size:        units.FormatBytes(it.Size),
		URL:         j.URL,
		RawURL:      s.cfg.RawBase() + "/" + it.ID + "/raw",
		EmbedURL:    template.URL(j.RawURL),
		DownloadURL: j.DownloadURL,
		Markdown:    j.Markdown,
		Burned:      o.burned,
	}
	if it.ExpiresAt != nil {
		vm.Expires = time.Unix(*it.ExpiresAt, 0).UTC().Format("2006-01-02 15:04 UTC")
	}
	page := "file"
	switch it.Kind {
	case store.KindImage:
		page = "image"
		if o.burned {
			uri, err := s.dataURI(r, it)
			if err != nil {
				s.fail(w, err)
				return
			}
			vm.EmbedURL = uri
		}
	case store.KindText:
		tv, err := s.loadTextView(r.Context(), it, !o.burned)
		if err != nil {
			s.fail(w, err)
			return
		}
		vm.Text = tv
		page = "text"
	}
	s.writePage(w, page, vm, it.Visibility == store.Private || it.BurnAfterRead)
}

func (s *Server) writePage(w http.ResponseWriter, page string, vm any, noStore bool) {
	var buf bytes.Buffer
	if err := s.pages[page].ExecuteTemplate(&buf, "layout", vm); err != nil {
		s.log.Error("render", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", s.viewCSP())
	h.Set("X-Frame-Options", "DENY")
	if noStore {
		h.Set("Cache-Control", "no-store")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	w.Write(buf.Bytes())
}

// dataURI inlines an image so it can be shown after its item is burned.
func (s *Server) dataURI(r *http.Request, it *store.Item) (template.URL, error) {
	f, err := s.blobs.Open(r.Context(), blob.Key(it.SHA256))
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}
	return template.URL("data:" + it.Mime + ";base64," + base64.StdEncoding.EncodeToString(b)), nil
}

// renderBurnNotice is the interstitial for burn-after-read items: link
// previewers that GET the page see only this; the reveal is a POST.
func (s *Server) renderBurnNotice(w http.ResponseWriter, it *store.Item) {
	s.writePage(w, "burn", struct {
		Title  string
		Action string
		Kind   string
	}{"Burn after reading", "/" + it.ID + "/reveal", it.Kind}, true)
}

// viewCSP allows images and media from the raw origin when it differs.
func (s *Server) viewCSP() string {
	src := "'self'"
	if raw := s.cfg.RawBaseURL; raw != "" && raw != s.cfg.BaseURL {
		src += " " + raw
	}
	return "default-src 'self'; img-src " + src + " data:; media-src " + src + "; connect-src " + src +
		"; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
}
