package server

import (
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"strings"

	"tibi/internal/ingest"
	"tibi/internal/store"
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
		if scope != "" && !t.Has(scope) {
			apiError(w, http.StatusForbidden, "forbidden", "token lacks scope "+scope)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), tokenKey, t)))
	})
}

func (s *Server) handleWhoami(w http.ResponseWriter, r *http.Request) {
	t := tokenFrom(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"name":        t.Name,
		"scopes":      t.Scopes,
		"quota_bytes": t.QuotaBytes,
	})
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
func (s *Server) uploadOptions(r *http.Request, get func(string) string) ingest.Options {
	return ingest.Options{
		Filename: get("filename"),
		Title:    get("title"),
		Kind:     get("kind"),
		KeepMeta: truthy(get("keep_meta")),
		Source:   get("source"),
		Meta:     get("meta"),
		TokenID:  tokenFrom(r.Context()).ID,
		MaxBytes: s.cfg.MaxUploadBytes,
	}
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
	r.Body = http.MaxBytesReader(w, r.Body, limit+1)
	it, err := s.ingest.Ingest(r.Context(), r.Body, s.uploadOptions(r, func(k string) string { return opt(r, k) }))
	if err != nil {
		s.ingestError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.itemJSON(it))
}

// multipartOverhead allows for part headers and form fields on top of the
// file bytes themselves.
const multipartOverhead = 1 << 20

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
	for _, sf := range files {
		o := s.uploadOptions(r, get)
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
	out := make([]ItemJSON, 0, len(prepared))
	for _, p := range prepared {
		if err := s.ingest.Commit(r.Context(), p); err != nil {
			s.ingestError(w, err)
			return
		}
		out = append(out, s.itemJSON(p.Item))
	}
	if len(out) == 1 {
		writeJSON(w, http.StatusCreated, out[0])
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

// ItemJSON is the API representation of an item.
type ItemJSON struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	URL         string `json:"url"`
	RawURL      string `json:"raw_url"`
	DownloadURL string `json:"download_url"`
	Mime        string `json:"mime"`
	Size        int64  `json:"size"`
	Width       *int   `json:"width,omitempty"`
	Height      *int   `json:"height,omitempty"`
	Markdown    string `json:"markdown"`
	Filename    string `json:"filename,omitempty"`
	Title       string `json:"title,omitempty"`
	Visibility  string `json:"visibility"`
	ExpiresAt   *int64 `json:"expires_at"`
	CreatedAt   int64  `json:"created_at"`
}

func (s *Server) itemJSON(it *store.Item) ItemJSON {
	j := ItemJSON{
		ID:          it.ID,
		Kind:        it.Kind,
		URL:         s.cfg.BaseURL + "/" + it.ID,
		RawURL:      s.cfg.RawBase() + "/" + it.ID + "." + it.Ext,
		DownloadURL: s.cfg.RawBase() + "/" + it.ID + "/dl",
		Mime:        it.Mime,
		Size:        it.Size,
		Filename:    it.Filename,
		Title:       it.Title,
		Visibility:  it.Visibility,
		ExpiresAt:   it.ExpiresAt,
		CreatedAt:   it.CreatedAt,
		Width:       it.Width,
		Height:      it.Height,
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
