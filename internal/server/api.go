package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/baojie/tibi/internal/id"
	"github.com/baojie/tibi/internal/ingest"
	"github.com/baojie/tibi/internal/metrics"
	"github.com/baojie/tibi/internal/store"
	"github.com/baojie/tibi/internal/units"
)

type ctxKey int

const tokenKey ctxKey = 0

func tokenFrom(ctx context.Context) *store.Token {
	t, _ := ctx.Value(tokenKey).(*store.Token)
	return t
}

// auth requires a valid bearer token, and scope if non-empty.
func (s *Server) auth(scope string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secret := bearer(r)
		if secret == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="tibi"`)
			apiError(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
			return
		}
		t, err := s.store.Authenticate(r.Context(), secret)
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				s.log.Error("authenticate", "err", err)
			}
			w.Header().Set("WWW-Authenticate", `Bearer realm="tibi", error="invalid_token"`)
			apiError(w, http.StatusUnauthorized, "unauthorized", "invalid or revoked token")
			return
		}
		if ok, wait := s.limiter.allow(t.ID, time.Now()); !ok {
			rateLimited(w, wait)
			return
		}
		if scope != "" && !t.Has(scope) {
			apiError(w, http.StatusForbidden, "forbidden", "token lacks scope "+scope)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), tokenKey, t)))
	})
}

func (s *Server) handleWhoami(w http.ResponseWriter, r *http.Request) {
	t := tokenFrom(r.Context())
	used, err := s.store.TokenUsage(r.Context(), t.ID)
	if err != nil {
		s.internal(w, "whoami", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":        t.Name,
		"scopes":      t.Scopes,
		"quota_bytes": t.QuotaBytes,
		"used_bytes":  used,
	})
}

// quotaError reports why adding n bytes would exceed the token's upload
// quota (PLAN A8.7), or "" when it fits or there is no quota.
//
// Unfinished resumable uploads count as used. Callers that go on to commit
// hold quotaLock across the check and the commit, so concurrent uploads
// cannot each pass on the same remaining space.
func (s *Server) quotaError(r *http.Request, n int64) string {
	return s.quotaErrorExcept(r, n, "")
}

// quotaErrorExcept is quotaError not counting the resumable upload except
// (the one being finished).
func (s *Server) quotaErrorExcept(r *http.Request, n int64, except string) string {
	t := tokenFrom(r.Context())
	if t == nil || t.QuotaBytes == nil {
		return ""
	}
	used, err := s.store.TokenUsage(r.Context(), t.ID)
	if err != nil {
		s.log.Error("quota", "err", err)
		return "upload quota could not be checked"
	}
	pending, err := s.store.PendingUploadBytes(r.Context(), t.ID, except)
	if err != nil {
		s.log.Error("quota", "err", err)
		return "upload quota could not be checked"
	}
	used += pending
	if used+n <= *t.QuotaBytes {
		return ""
	}
	return fmt.Sprintf("upload quota exceeded: %s of %s already used, this upload needs %s",
		units.FormatBytes(used), units.FormatBytes(*t.QuotaBytes), units.FormatBytes(n))
}

// quotaLock serializes quota check and commit per token with a quota
// (others pay nothing): one token's slow upload (a long HEIC conversion, a
// 2 GiB write to S3) holds up only that token's other uploads. Returns the
// unlock function.
func (s *Server) quotaLock(r *http.Request) func() {
	t := tokenFrom(r.Context())
	if t == nil || t.QuotaBytes == nil {
		return func() {}
	}
	s.quotaMu.Lock()
	if s.quotaLocks == nil {
		s.quotaLocks = map[int64]*sync.Mutex{}
	}
	m, ok := s.quotaLocks[t.ID]
	if !ok {
		m = &sync.Mutex{}
		s.quotaLocks[t.ID] = m
	}
	s.quotaMu.Unlock()
	m.Lock()
	return m.Unlock
}

// opt reads an upload option from the query string or an X-Tibi-* header.
func opt(r *http.Request, name string) string {
	if v := r.URL.Query().Get(name); v != "" {
		return v
	}
	return r.Header.Get("X-Tibi-" + headerName(name))
}

// headerName maps keep_meta → Keep-Meta.
func headerName(name string) string {
	parts := strings.Split(name, "_")
	for i, p := range parts {
		if p != "" {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "-")
}

func truthy(v string) bool {
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// uploadOptions builds ingest options from a value lookup.
func (s *Server) uploadOptions(r *http.Request, get func(string) string) (ingest.Options, error) {
	o := ingest.Options{
		Filename:   get("filename"),
		Title:      get("title"),
		Kind:       get("kind"),
		Lang:       get("lang"),
		KeepMeta:   truthy(get("keep_meta")),
		Source:     get("source"),
		SourceURL:  get("source_url"),
		Meta:       get("meta"),
		Slug:       strings.ToLower(get("slug")),
		Burn:       truthy(get("burn_after_read")),
		Visibility: get("visibility"),
		TokenID:    tokenFrom(r.Context()).ID,
		MaxBytes:   s.cfg.MaxUploadBytes,
	}
	switch o.Visibility {
	case "", store.Unlisted, store.Private:
	default:
		return o, &ingest.BadRequestError{Msg: "visibility must be unlisted or private"}
	}
	if o.Slug != "" && !store.ValidSlug(o.Slug) {
		return o, &ingest.BadRequestError{Msg: store.ErrSlugInvalid.Error()}
	}
	if o.SourceURL != "" && !validSourceURL(o.SourceURL) {
		return o, &ingest.BadRequestError{Msg: "source_url must be an http(s) URL of at most 2048 bytes"}
	}
	exp := get("expires")
	if exp == "" {
		exp = s.cfg.DefaultExpires
	}
	at, err := s.expiresAt(exp)
	if err != nil {
		return o, &ingest.BadRequestError{Msg: err.Error()}
	}
	o.ExpiresAt = at
	return o, nil
}

// validSourceURL accepts absolute http(s) URLs without control characters.
func validSourceURL(v string) bool {
	if len(v) > 2048 || strings.ContainsFunc(v, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return false
	}
	u, err := url.Parse(v)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// expiresAt converts an expiry like "7d" into an absolute unix time
// (nil for never).
func (s *Server) expiresAt(exp string) (*int64, error) {
	d, never, err := units.ParseExpiry(exp)
	if err != nil || never {
		return nil, err
	}
	// Round up: expires_at has second resolution, and an item must never
	// expire before the requested duration has passed.
	t := s.store.Now().Add(d)
	at := t.Unix()
	if t.Nanosecond() > 0 {
		at++
	}
	return &at, nil
}

func (s *Server) tooLarge(w http.ResponseWriter) {
	apiError(w, http.StatusRequestEntityTooLarge, "too_large",
		"upload exceeds max_upload_bytes ("+strconv.FormatInt(s.cfg.MaxUploadBytes, 10)+")")
}

// ingestError writes the response for a failed upload.
func (s *Server) ingestError(w http.ResponseWriter, err error) {
	var bre *ingest.BadRequestError
	var mbe *http.MaxBytesError
	switch {
	case errors.Is(err, ingest.ErrTooLarge), errors.As(err, &mbe):
		s.tooLarge(w)
	case errors.Is(err, ingest.ErrEmpty):
		apiError(w, http.StatusBadRequest, "empty", "upload is empty")
	case errors.As(err, &bre):
		apiError(w, http.StatusBadRequest, "bad_request", bre.Msg)
	case errors.Is(err, store.ErrSlugInvalid):
		apiError(w, http.StatusBadRequest, "bad_request", err.Error())
	default:
		s.log.Error("ingest", "err", err)
		apiError(w, http.StatusInternalServerError, "internal", "upload failed")
	}
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	limit := s.cfg.MaxUploadBytes
	mt, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt == "multipart/form-data" {
		s.handleMultipart(w, r, params["boundary"])
		return
	}
	if r.ContentLength > limit {
		s.tooLarge(w)
		return
	}
	if msg := s.quotaError(r, max(r.ContentLength, 0)); msg != "" && r.ContentLength > 0 {
		apiError(w, http.StatusRequestEntityTooLarge, "too_large", msg) // before reading the body
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit+1)
	get := func(k string) string { return opt(r, k) }
	o, err := s.uploadOptions(r, get)
	if err != nil {
		s.ingestError(w, err)
		return
	}
	cp, err := s.collectionParam(get, o)
	if err != nil {
		s.ingestError(w, err)
		return
	}
	p, err := s.ingest.Prepare(r.Context(), r.Body, o)
	if err != nil {
		s.ingestError(w, err)
		return
	}
	defer p.Close()
	s.finishUpload(w, r, []*ingest.Prepared{p}, o, cp)
}

// multipartOverhead allows for part headers and form fields on top of the
// file bytes themselves.
const multipartOverhead = 1 << 20

// maxMultipartFiles and maxMultipartFields bound one multipart request:
// each file part holds temporary files open until the request ends, and
// 20 000 one-byte parts once meant 40 000 open descriptors.
const (
	maxMultipartFiles  = 100
	maxMultipartFields = 50
)

type spooled struct {
	name string
	f    *os.File
}

func (s *Server) handleMultipart(w http.ResponseWriter, r *http.Request, boundary string) {
	limit := s.cfg.MaxUploadBytes
	if boundary == "" {
		apiError(w, http.StatusBadRequest, "bad_request", "multipart boundary missing")
		return
	}
	if r.ContentLength > limit+multipartOverhead {
		s.tooLarge(w)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit+multipartOverhead)
	mr := multipart.NewReader(r.Body, boundary)
	fields := map[string]string{}
	var files []spooled
	defer func() {
		for _, f := range files {
			f.f.Close()
			os.Remove(f.f.Name())
		}
	}()
	var total int64
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				s.tooLarge(w)
				return
			}
			apiError(w, http.StatusBadRequest, "bad_request", "malformed multipart body")
			return
		}
		name := part.FormName()
		if name != "file" && len(fields) >= maxMultipartFields {
			apiError(w, http.StatusBadRequest, "bad_request", "too many form fields")
			return
		}
		if name == "file" && len(files) >= maxMultipartFiles {
			apiError(w, http.StatusBadRequest, "bad_request", fmt.Sprintf("at most %d files per request; use several requests or a collection", maxMultipartFiles))
			return
		}
		if name != "file" {
			v, err := io.ReadAll(io.LimitReader(part, 64<<10))
			if err != nil {
				s.ingestError(w, err)
				return
			}
			fields[name] = string(v)
			continue
		}
		f, err := os.CreateTemp(s.tempDir, "part-*")
		if err != nil {
			s.ingestError(w, err)
			return
		}
		files = append(files, spooled{part.FileName(), f})
		n, err := io.Copy(f, io.LimitReader(part, limit-total+1))
		total += n
		if err != nil {
			s.ingestError(w, err)
			return
		}
		if total > limit {
			s.tooLarge(w)
			return
		}
	}
	if len(files) == 0 {
		apiError(w, http.StatusBadRequest, "bad_request", "no file parts (field name must be \"file\")")
		return
	}
	get := func(k string) string {
		if v, ok := fields[k]; ok {
			return v
		}
		return opt(r, k)
	}
	var prepared []*ingest.Prepared
	defer func() {
		for _, p := range prepared {
			p.Close()
		}
	}()
	base, err := s.uploadOptions(r, get)
	if err != nil {
		s.ingestError(w, err)
		return
	}
	if base.Slug != "" && len(files) > 1 {
		apiError(w, http.StatusBadRequest, "bad_request", "slug can only be set when uploading a single file")
		return
	}
	cp, err := s.collectionParam(get, base)
	if err != nil {
		s.ingestError(w, err)
		return
	}
	for _, sf := range files {
		o := base
		if sf.name != "" {
			o.Filename = sf.name
		}
		if _, err := sf.f.Seek(0, io.SeekStart); err != nil {
			s.ingestError(w, err)
			return
		}
		p, err := s.ingest.Prepare(r.Context(), sf.f, o)
		if err != nil {
			s.ingestError(w, err)
			return
		}
		prepared = append(prepared, p)
	}
	s.finishUpload(w, r, prepared, base, cp)
}

// collectionTarget says where uploads go: nowhere, a new collection, or
// the end of an existing one.
type collectionTarget struct {
	id    string // "" none, "new", or an existing collection ID
	title string
}

func (s *Server) collectionParam(get func(string) string, o ingest.Options) (collectionTarget, error) {
	ct := collectionTarget{id: get("collection"), title: get("collection_title")}
	if ct.id == "" {
		return ct, nil
	}
	if o.Burn {
		return ct, &ingest.BadRequestError{Msg: "burn_after_read cannot be combined with a collection"}
	}
	if ct.id != "new" && !id.Valid(ct.id) {
		return ct, &ingest.BadRequestError{Msg: `collection must be "new" or a collection ID`}
	}
	return ct, nil
}

// finishUpload commits prepared uploads, into a collection if requested,
// and writes the response: an Item, an array of Items, or — with a
// collection — {"collection": …, "items": […]}.
func (s *Server) finishUpload(w http.ResponseWriter, r *http.Request, prepared []*ingest.Prepared, o ingest.Options, ct collectionTarget) {
	ctx := r.Context()
	var total int64
	for _, p := range prepared {
		total += p.Item.Size
	}
	defer s.quotaLock(r)()
	if msg := s.quotaError(r, total); msg != "" {
		apiError(w, http.StatusRequestEntityTooLarge, "too_large", msg)
		return
	}
	var coll *store.Collection
	pos := 0
	switch ct.id {
	case "":
	case "new":
		coll = &store.Collection{Title: ct.title, Visibility: o.Visibility, TokenID: o.TokenID, ExpiresAt: o.ExpiresAt}
		if err := s.store.CreateCollection(ctx, coll); err != nil {
			s.ingestError(w, err)
			return
		}
	default:
		c, err := s.store.GetCollection(ctx, ct.id)
		if p := privateAccess(r); err == nil && c.Visibility == store.Private && !p.All && c.TokenID != p.OwnToken {
			err = store.ErrNotFound // answered like a missing one
		}
		if errors.Is(err, store.ErrNotFound) {
			apiError(w, http.StatusBadRequest, "bad_request", "no such collection "+ct.id)
			return
		}
		if err != nil {
			s.ingestError(w, err)
			return
		}
		coll = c
		if pos, err = s.store.NextPosition(ctx, c.ID); err != nil {
			s.ingestError(w, err)
			return
		}
	}
	out := make([]ItemJSON, 0, len(prepared))
	for i, p := range prepared {
		if coll != nil {
			n := pos + i
			p.Item.CollectionID, p.Item.Position = coll.ID, &n
		}
		if err := s.ingest.Commit(ctx, p); err != nil {
			s.ingestError(w, err)
			return
		}
		metrics.UploadBytes.Add(p.Item.Size)
		out = append(out, s.apiItemJSON(r, p.Item))
	}
	switch {
	case coll != nil:
		c, err := s.store.GetCollection(ctx, coll.ID) // fresh item count
		if err != nil {
			s.ingestError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"collection": s.collectionJSON(c), "items": out})
	case len(out) == 1:
		writeJSON(w, http.StatusCreated, out[0])
	default:
		writeJSON(w, http.StatusCreated, out)
	}
}

// ItemJSON is the API representation of an item.
type ItemJSON struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	URL           string `json:"url"`
	RawURL        string `json:"raw_url"`
	DownloadURL   string `json:"download_url"`
	Mime          string `json:"mime"`
	Size          int64  `json:"size"`
	Width         *int   `json:"width,omitempty"`
	Height        *int   `json:"height,omitempty"`
	DurationMS    *int64 `json:"duration_ms,omitempty"`
	OrigMime      string `json:"orig_mime,omitempty"`
	ThumbnailURL  string `json:"thumbnail_url,omitempty"`
	Excerpt       string `json:"excerpt,omitempty"`
	Markdown      string `json:"markdown"`
	Filename      string `json:"filename,omitempty"`
	Title         string `json:"title,omitempty"`
	Lang          string `json:"lang,omitempty"`
	Charset       string `json:"charset,omitempty"`
	Visibility    string `json:"visibility"`
	Slug          string `json:"slug,omitempty"`
	SlugURL       string `json:"slug_url,omitempty"`
	CollectionID  string `json:"collection_id,omitempty"`
	CollectionURL string `json:"collection_url,omitempty"`
	Position      *int   `json:"position,omitempty"`
	ForkedFrom    string `json:"forked_from,omitempty"`
	BurnAfterRead bool   `json:"burn_after_read"`
	Source        string `json:"source,omitempty"`
	SourceURL     string `json:"source_url,omitempty"`
	ViewCount     int64  `json:"view_count"`
	ExpiresAt     *int64 `json:"expires_at"`
	CreatedAt     int64  `json:"created_at"`
}

// apiItemJSON is itemJSON for API answers: it adds what only the item's
// uploader (and read_private tokens) may see, like where it came from.
// itemJSON alone also feeds public pages.
func (s *Server) apiItemJSON(r *http.Request, it *store.Item) ItemJSON {
	j := s.itemJSON(it)
	if p := privateAccess(r); p.All || (p.OwnToken != 0 && it.TokenID == p.OwnToken) {
		j.SourceURL = it.SourceURL
	}
	return j
}

func (s *Server) itemJSON(it *store.Item) ItemJSON {
	j := ItemJSON{
		ID:            it.ID,
		Kind:          it.Kind,
		URL:           s.cfg.BaseURL + "/" + it.ID,
		RawURL:        s.cfg.RawBase() + "/" + it.ID + "." + it.Ext,
		DownloadURL:   s.cfg.RawBase() + "/" + it.ID + "/dl",
		Mime:          it.Mime,
		Size:          it.Size,
		Filename:      it.Filename,
		Title:         it.Title,
		Lang:          it.Lang,
		Charset:       it.Charset,
		Visibility:    it.Visibility,
		ExpiresAt:     it.ExpiresAt,
		CreatedAt:     it.CreatedAt,
		Width:         it.Width,
		Height:        it.Height,
		Slug:          it.Slug,
		BurnAfterRead: it.BurnAfterRead,
		Source:        it.Source,
		ViewCount:     it.ViewCount,
	}
	if it.Slug != "" {
		j.SlugURL = s.cfg.BaseURL + "/s/" + it.Slug
	}
	j.DurationMS, j.OrigMime = it.DurationMS, it.OrigMime
	j.ForkedFrom, j.Position = it.ForkedFrom, it.Position
	if it.CollectionID != "" {
		j.CollectionID, j.CollectionURL = it.CollectionID, s.cfg.BaseURL+"/c/"+it.CollectionID
	}
	if it.Thumb || (it.Kind == store.KindImage && ingest.Thumbable(it.Mime, it.Size)) {
		j.ThumbnailURL = s.cfg.RawBase() + "/" + it.ID + "/thumb"
	}
	j.Markdown = markdownFor(it, j.URL, j.RawURL)
	return j
}

// markdownFor returns a Markdown snippet: an embedded image for images, a
// link otherwise.
func markdownFor(it *store.Item, url, rawURL string) string {
	if it.Kind == store.KindImage {
		return "![" + mdEscape(it.Title) + "](" + rawURL + ")"
	}
	label := it.Title
	if label == "" {
		label = it.Filename
	}
	if label == "" {
		label = it.ID
	}
	return "[" + mdEscape(label) + "](" + url + ")"
}

var mdReplacer = strings.NewReplacer(`\`, `\\`, `[`, `\[`, `]`, `\]`, "\n", " ", "\r", " ")

func mdEscape(s string) string { return mdReplacer.Replace(s) }
