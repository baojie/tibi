package server

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/baojie/tibi/internal/blob"
	"github.com/baojie/tibi/internal/disposition"
	"github.com/baojie/tibi/internal/id"
	"github.com/baojie/tibi/internal/ingest"
	"github.com/baojie/tibi/internal/metrics"
	"github.com/baojie/tibi/internal/store"
	"github.com/baojie/tibi/internal/units"
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
		if !s.sameOrigin(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
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
	case len(parts) == 2 && parts[0] == "c":
		s.handleCollectionPage(w, r, parts[1])
	case len(parts) == 1:
		s.handleItemOrRaw(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "raw":
		s.handleRaw(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "dl":
		s.handleDownload(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "thumb":
		s.handleThumb(w, r, parts[0])
	case len(parts) == 3 && parts[1] == "burned":
		s.handleBurned(w, r, parts[0], parts[2])
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
		s.renderBurnNotice(w, r, it, acc.signQuery)
		return
	}
	s.renderView(w, r, it, viewOpts{signQuery: acc.signQuery})
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

// handleThumb serves the WebP thumbnail of an image (an extension of the
// frozen URL scheme; used by collections and link previews).
func (s *Server) handleThumb(w http.ResponseWriter, r *http.Request, itemID string) {
	it, acc, err := s.lookup(r, itemID)
	if err == nil && (!it.Thumb || (it.BurnAfterRead && !acc.privileged)) {
		err = store.ErrNotFound
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	if s.hotlinkBlocked(w, r, it) || s.overCap(w, it) {
		return
	}
	f, err := s.blobs.Open(r.Context(), blob.ThumbKey(it.SHA256, ingest.ThumbWidth))
	if err != nil {
		s.fail(w, err)
		return
	}
	defer f.Close()
	cw := &countingWriter{ResponseWriter: w}
	defer func() { s.usage.Add(it.ID, cw.n, false); metrics.ServedBytes.Add(cw.n) }()
	w = cw
	h := w.Header()
	h.Set("Content-Type", "image/webp")
	h.Set("Content-Security-Policy", rawCSP)
	h.Set("Cross-Origin-Resource-Policy", "cross-origin")
	h.Set("Cache-Control", s.rawCacheControl(it))
	http.ServeContent(w, r, "", time.Unix(it.CreatedAt, 0), f)
}

// serveRaw serves bytes, except that burn-after-read items are never served
// raw to unprivileged clients: chat-app link previewers prefetch raw URLs,
// and only the explicit reveal may consume the item.
func (s *Server) serveRaw(w http.ResponseWriter, r *http.Request, it *store.Item, acc access, download bool) {
	if it.BurnAfterRead && !acc.privileged {
		notFound(w)
		return
	}
	// Downloads too: <img src=/{id}/dl> embeds as well as the raw URL.
	if s.hotlinkBlocked(w, r, it) || s.overCap(w, it) {
		return
	}
	s.serveBlob(w, r, it, download)
}

// access describes what the requester may see.
type access struct {
	// privileged requests carry a token with read_private: they see private
	// items, and burn-after-read items without consuming them.
	privileged bool
	// signQuery is "exp=…&sig=…" when the request carries a valid share
	// link signature for the item: it may see that (private) item, and the
	// page's raw/download links carry the same signature.
	signQuery string
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
	if q := r.URL.Query(); q.Get("sig") != "" && s.signer.Valid(it.ID, q.Get("exp"), q.Get("sig"), s.store.Now().Unix()) {
		acc.signQuery = "exp=" + q.Get("exp") + "&sig=" + q.Get("sig")
	}
	if it.Visibility == store.Private && !acc.privileged && acc.signQuery == "" {
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
	case store.KindText, store.KindImage, store.KindEncrypted:
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
	if s.cfg.BlobDelivery == "redirect" && redirectable(it, download) && s.redirectBlob(w, r, it, download) {
		// The object store sends the bytes (and bills the egress): count
		// the whole object once per hit.
		if countHit(r) {
			s.usage.Add(it.ID, it.Size, true)
		}
		return
	}
	// Reserve the whole blob: what a Range request actually sends is up to
	// ServeContent (overlapping ranges or a stale If-Range get everything).
	release, ok := s.reserveCap(w, it, it.Size)
	if !ok {
		return
	}
	cw := &countingWriter{ResponseWriter: w}
	defer func() { release(); s.usage.Add(it.ID, cw.n, countHit(r)); metrics.ServedBytes.Add(cw.n) }()
	w = cw
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
		h.Set("Content-Disposition", disposition.Attachment(downloadName(it), it.ID+"."+it.Ext))
	} else {
		h.Set("Content-Type", RawContentType(it.Mime))
		h.Set("Content-Disposition", "inline")
	}
	http.ServeContent(w, r, "", time.Unix(it.CreatedAt, 0), f)
}

// redirectable reports whether a blob may be handed to the object store via
// a presigned URL. The store's response lacks tibi's security headers, so
// only passive media inline, or non-text non-active types as attachments,
// qualify; text and anything that could execute are always proxied
// (PLAN A7). Private and burn-after-read items are always proxied too, since
// a presigned URL is a bearer link that bypasses access checks.
func redirectable(it *store.Item, download bool) bool {
	if it.Visibility == store.Private || it.BurnAfterRead {
		return false
	}
	if download {
		return it.Kind != store.KindText && it.Kind != store.KindEncrypted && !activeTypes[strings.ToLower(it.Mime)]
	}
	switch it.Kind {
	case store.KindImage, store.KindVideo, store.KindAudio:
		return passiveTypes[strings.ToLower(it.Mime)]
	}
	return false
}

// presignTTL is how long redirect URLs stay valid. A presigned URL is a
// bearer link straight to the bucket, outside tibi's caps and hotlink
// checks, so keep it short — but long enough for a video player, which
// keeps fetching ranges from the same URL while it plays.
const presignTTL = 15 * time.Minute

// redirectBlob sends a 302 to a presigned URL; false means "proxy instead"
// (backend cannot presign, or presigning failed).
func (s *Server) redirectBlob(w http.ResponseWriter, r *http.Request, it *store.Item, download bool) bool {
	ttl := presignTTL
	if it.ExpiresAt != nil {
		left := time.Until(time.Unix(*it.ExpiresAt, 0))
		if left <= 0 {
			return false
		}
		ttl = min(ttl, left)
	}
	filename, mt := "", RawContentType(it.Mime)
	if download {
		filename, mt = downloadName(it), "application/octet-stream"
	}
	u, ok, err := s.blobs.PresignGet(r.Context(), blob.Key(it.SHA256), ttl, filename, mt)
	if err != nil {
		s.log.Warn("presign failed; proxying", "id", it.ID, "err", err)
		return false
	}
	if !ok {
		return false
	}
	// Each redirect is counted as the whole object; let the requesting
	// browser reuse it briefly, but no shared cache hand it to others.
	w.Header().Set("Cache-Control", "private, max-age="+strconv.Itoa(int(min(60, ttl.Seconds()/6))))
	http.Redirect(w, r, u, http.StatusFound)
	return true
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
	LocalRaw    string // same-origin raw path, for scripts (copy, fork)
	Text        *textView
	Expires     string
	Duration    string
	OverCap     bool // today's bandwidth allowance is used up
	// Burned is set when a burn-after-read item is shown on reveal: it is
	// already deleted, so no raw/download links are offered.
	Burned bool
	// OG holds link-preview (OpenGraph/Twitter) tags; nil when the item
	// must not be previewed.
	OG *ogMeta
}

// ogMeta is what chat apps show when a link is pasted.
type ogMeta struct {
	Type        string // website | video.other | music.song
	Title       string
	Description string
	URL         string
	Image       string
	ImageWidth  int
	ImageHeight int
	Video       string
	VideoType   string
	Card        string // summary | summary_large_image
}

// ogImageMax is the largest original image offered as og:image; bigger
// ones (and WebP/AVIF, which some unfurlers reject) use the thumbnail.
const ogImageMax = 5 << 20

// openGraph builds preview tags. Private, burn-after-read and encrypted
// items never get any (PLAN A6): a preview would leak their content.
func (s *Server) openGraph(it *store.Item, j ItemJSON, title string, tv *textView) *ogMeta {
	if it.Visibility != store.Unlisted || it.BurnAfterRead || it.Kind == store.KindEncrypted {
		return nil
	}
	og := &ogMeta{Type: "website", Title: title, URL: j.URL, Card: "summary"}
	switch it.Kind {
	case store.KindImage:
		og.Card = "summary_large_image"
		switch {
		case it.Size <= ogImageMax && (it.Mime == "image/png" || it.Mime == "image/jpeg" || it.Mime == "image/gif"):
			og.Image = j.RawURL
			if it.Width != nil {
				og.ImageWidth, og.ImageHeight = *it.Width, *it.Height
			}
		case j.ThumbnailURL != "":
			og.Image = j.ThumbnailURL
		}
	case store.KindVideo:
		og.Type, og.Video, og.VideoType = "video.other", j.RawURL, it.Mime
	case store.KindAudio:
		og.Type = "music.song"
	case store.KindText:
		if tv != nil {
			og.Description = tv.Preview
		}
	default:
		og.Description = it.Mime + ", " + units.FormatBytes(it.Size)
	}
	return og
}

type viewOpts struct {
	burned    bool
	signQuery string // appended to raw/download/thumb links (signed share links)
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
		LocalRaw:    "/" + it.ID + "/raw",
		EmbedURL:    template.URL(j.RawURL),
		DownloadURL: j.DownloadURL,
		Markdown:    j.Markdown,
		Burned:      o.burned,
	}
	if it.DurationMS != nil {
		vm.Duration = formatDuration(*it.DurationMS)
	}
	if !o.burned { // a burn's view is counted when it is consumed
		s.usage.Add(it.ID, 0, true)
	}
	if itemOver, totalOver := s.usage.Over(it.ID, s.cfg.MaxItemBytesDay, s.cfg.MaxTotalBytesDay); itemOver || totalOver {
		vm.OverCap = true
	}
	if q := o.signQuery; q != "" {
		vm.RawURL += "?" + q
		vm.LocalRaw += "?" + q
		vm.DownloadURL += "?" + q
		vm.EmbedURL = template.URL(string(vm.EmbedURL) + "?" + q)
		vm.URL += "?" + q
	}
	if it.ExpiresAt != nil {
		vm.Expires = time.Unix(*it.ExpiresAt, 0).UTC().Format("2006-01-02 15:04 UTC")
	}
	page := "file"
	switch it.Kind {
	case store.KindEncrypted:
		page = "encrypted"
		if o.burned { // its raw URL is gone: e2e.js fetches the grant instead
			vm.LocalRaw = "/" + it.ID + "/burned/" + s.burned.issue(it, s.store.Now())
		}
	case store.KindVideo:
		page = "video"
	case store.KindAudio:
		page = "audio"
	case store.KindImage:
		page = "image"
		if o.burned {
			vm.EmbedURL = template.URL("/" + it.ID + "/burned/" + s.burned.issue(it, s.store.Now()))
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
	if !o.burned && o.signQuery == "" {
		vm.OG = s.openGraph(it, j, title, vm.Text)
	}
	s.writePage(w, r, page, vm, it.Visibility == store.Private || it.BurnAfterRead)
}

func (s *Server) writePage(w http.ResponseWriter, r *http.Request, page string, vm any, noStore bool) {
	var buf bytes.Buffer
	loc := s.locale(r)
	if err := s.pages[loc][page].ExecuteTemplate(&buf, "layout", vm); err != nil {
		s.log.Error("render", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Language", loc)
	h.Add("Vary", "Accept-Language")
	h.Set("Content-Security-Policy", s.viewCSP())
	h.Set("X-Frame-Options", "DENY")
	if noStore {
		h.Set("Cache-Control", "no-store")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	w.Write(buf.Bytes())
}

// renderBurnNotice is the interstitial for burn-after-read items: link
// previewers that GET the page see only this; the reveal is a POST.
// signQuery carries a share link's signature into the reveal, which a
// private item needs to be found at all.
func (s *Server) renderBurnNotice(w http.ResponseWriter, r *http.Request, it *store.Item, signQuery string) {
	action := "/" + it.ID + "/reveal"
	if signQuery != "" {
		action += "?" + signQuery
	}
	s.writePage(w, r, "burn", struct {
		OG     *ogMeta // always nil: a burn item is never previewed
		Title  string
		Action string
		Kind   string
	}{nil, s.i18n.T(s.locale(r), "page.burn.title"), action, it.Kind}, true)
}

func formatDuration(ms int64) string {
	sec := ms / 1000
	if sec >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", sec/3600, sec/60%60, sec%60)
	}
	return fmt.Sprintf("%d:%02d", sec/60, sec%60)
}

// viewCSP allows images and media from the raw origin when it differs.
func (s *Server) viewCSP() string {
	src := "'self'"
	if raw := s.cfg.RawBaseURL; raw != "" && raw != s.cfg.BaseURL {
		src += " " + raw
	}
	return "default-src 'self'; img-src " + src + " data: blob:; media-src " + src + " blob:; connect-src " + src +
		"; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
}
