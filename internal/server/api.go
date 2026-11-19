package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"

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
func opt(r *http.Request, name, header string) string {
	if v := r.URL.Query().Get(name); v != "" {
		return v
	}
	return r.Header.Get(header)
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	limit := s.cfg.MaxUploadBytes
	if r.ContentLength > limit {
		apiError(w, http.StatusRequestEntityTooLarge, "too_large",
			"upload exceeds max_upload_bytes ("+strconv.FormatInt(limit, 10)+")")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit+1)
	t := tokenFrom(r.Context())
	it, err := s.ingest.Ingest(r.Context(), r.Body, ingest.Options{
		Filename: opt(r, "filename", "X-Tibi-Filename"),
		Title:    opt(r, "title", "X-Tibi-Title"),
		Source:   opt(r, "source", "X-Tibi-Source"),
		Meta:     opt(r, "meta", "X-Tibi-Meta"),
		TokenID:  t.ID,
		MaxBytes: limit,
	})
	switch {
	case errors.Is(err, ingest.ErrTooLarge):
		apiError(w, http.StatusRequestEntityTooLarge, "too_large",
			"upload exceeds max_upload_bytes ("+strconv.FormatInt(limit, 10)+")")
		return
	case errors.Is(err, ingest.ErrEmpty):
		apiError(w, http.StatusBadRequest, "empty", "upload body is empty")
		return
	case err != nil:
		s.log.Error("ingest", "err", err)
		apiError(w, http.StatusInternalServerError, "internal", "upload failed")
		return
	}
	writeJSON(w, http.StatusCreated, s.itemJSON(it))
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
	Filename    string `json:"filename,omitempty"`
	Title       string `json:"title,omitempty"`
	Visibility  string `json:"visibility"`
	ExpiresAt   *int64 `json:"expires_at"`
	CreatedAt   int64  `json:"created_at"`
}

func (s *Server) itemJSON(it *store.Item) ItemJSON {
	return ItemJSON{
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
	}
}
