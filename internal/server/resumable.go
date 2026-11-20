package server

import (
	"context"
	"encoding/json"
	"errors"
	xslog "golang.org/x/exp/slog" // tusd v2 uses x/exp/slog
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/tus/tusd/v2/pkg/filestore"
	tusd "github.com/tus/tusd/v2/pkg/handler"
	"github.com/tus/tusd/v2/pkg/memorylocker"

	"tibi/internal/id"
	"tibi/internal/ingest"
	"tibi/internal/metrics"
	"tibi/internal/store"
)

// Resumable uploads (PLAN A6): the tus 1.0 protocol at /api/v1/uploads/.
// Upload-Metadata carries the same options as a normal upload (filename,
// title, expires, visibility, lang, kind, keep_meta, slug, burn_after_read,
// source, meta). When the last byte arrives, the file goes through the
// ingest pipeline; GET /api/v1/uploads/{id}/item then returns the item, and
// the final PATCH response carries Tibi-Item-Id and Tibi-Item-Url headers.

// StaleUploadAge is how long an unfinished upload is kept.
const StaleUploadAge = 24 * 60 * 60

type resumable struct {
	h     *tusd.UnroutedHandler
	store filestore.FileStore
	dir   string
}

func (s *Server) setupResumable() error {
	dir := filepath.Join(s.cfg.DataDir, "uploads")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	fs := filestore.New(dir)
	composer := tusd.NewStoreComposer()
	fs.UseIn(composer)
	memorylocker.New().UseIn(composer)
	h, err := tusd.NewUnroutedHandler(tusd.Config{
		StoreComposer:             composer,
		BasePath:                  s.cfg.BaseURL + "/api/v1/uploads/",
		MaxSize:                   s.cfg.MaxResumableBytes,
		DisableDownload:           true,
		DisableConcatenation:      true,
		Cors:                      &tusd.CorsConfig{Disable: true},
		PreUploadCreateCallback:   s.tusCreate,
		PreFinishResponseCallback: s.tusFinish,
		Logger:                    xslog.New(xslog.NewTextHandler(io.Discard, nil)), // tusd logs every request
	})
	if err != nil {
		return err
	}
	s.tus = &resumable{h: h, store: fs, dir: dir}
	return nil
}

// tusOptionKeys are the Upload-Metadata keys tibi understands.
var tusOptionKeys = []string{"filename", "title", "kind", "lang", "keep_meta", "expires", "visibility", "slug", "burn_after_read", "source", "meta"}

func (s *Server) tusOptions(ctx context.Context, meta map[string]string) (ingest.Options, error) {
	r := (&http.Request{Header: http.Header{}}).WithContext(ctx)
	o, err := s.uploadOptions(r, func(k string) string { return meta[k] })
	o.MaxBytes = s.cfg.MaxResumableBytes
	return o, err
}

func (s *Server) tusCreate(hook tusd.HookEvent) (tusd.HTTPResponse, tusd.FileInfoChanges, error) {
	tok := tokenFrom(hook.Context)
	if tok == nil {
		return tusd.HTTPResponse{}, tusd.FileInfoChanges{}, tusd.NewError("ERR_UNAUTHORIZED", "missing token", http.StatusUnauthorized)
	}
	if hook.Upload.SizeIsDeferred {
		return tusd.HTTPResponse{}, tusd.FileInfoChanges{}, tusd.NewError("ERR_LENGTH_REQUIRED", "Upload-Length is required", http.StatusBadRequest)
	}
	if _, err := s.tusOptions(hook.Context, hook.Upload.MetaData); err != nil {
		var bre *ingest.BadRequestError
		if errors.As(err, &bre) {
			return tusd.HTTPResponse{}, tusd.FileInfoChanges{}, tusd.NewError("ERR_BAD_METADATA", bre.Msg, http.StatusBadRequest)
		}
		return tusd.HTTPResponse{}, tusd.FileInfoChanges{}, err
	}
	r := (&http.Request{}).WithContext(hook.Context)
	defer s.quotaLock(r)() // until the upload row exists and counts as pending
	if msg := s.quotaError(r, hook.Upload.Size); msg != "" {
		return tusd.HTTPResponse{}, tusd.FileInfoChanges{}, tusd.NewError("ERR_QUOTA", msg, http.StatusRequestEntityTooLarge)
	}
	meta := map[string]string{}
	for _, k := range tusOptionKeys {
		if v, ok := hook.Upload.MetaData[k]; ok {
			meta[k] = v
		}
	}
	opts, _ := json.Marshal(meta)
	u := &store.Upload{ID: id.Random(24), TokenID: tok.ID, Options: string(opts), Size: hook.Upload.Size}
	if err := s.store.CreateUpload(hook.Context, u); err != nil {
		return tusd.HTTPResponse{}, tusd.FileInfoChanges{}, err
	}
	return tusd.HTTPResponse{}, tusd.FileInfoChanges{ID: u.ID}, nil
}

// tusFinish ingests a completed upload before the final PATCH is answered.
func (s *Server) tusFinish(hook tusd.HookEvent) (tusd.HTTPResponse, error) {
	ctx := context.WithoutCancel(hook.Context) // a slow ingest must not be cut off
	upID := hook.Upload.ID
	defer s.tus.remove(upID)
	u, err := s.store.GetUpload(ctx, upID)
	if err != nil {
		return tusd.HTTPResponse{}, err
	}
	var meta map[string]string
	json.Unmarshal([]byte(u.Options), &meta)
	o, err := s.tusOptions(ctx, meta)
	if err == nil {
		// Re-check the quota with the final size: other uploads may have
		// finished since this one was created.
		r := (&http.Request{}).WithContext(hook.Context)
		unlock := s.quotaLock(r)
		defer unlock()
		if msg := s.quotaErrorExcept(r, hook.Upload.Size, upID); msg != "" {
			s.store.FinishUpload(ctx, upID, "", msg)
			return tusd.HTTPResponse{}, tusd.NewError("ERR_QUOTA", msg, http.StatusRequestEntityTooLarge)
		}
		o.TokenID = u.TokenID
		var p *ingest.Prepared
		if p, err = s.ingest.PrepareFile(ctx, filepath.Join(s.tus.dir, upID), o); err == nil {
			defer p.Close()
			if err = s.ingest.Commit(ctx, p); err == nil {
				metrics.UploadBytes.Add(p.Item.Size)
				s.store.FinishUpload(ctx, upID, p.Item.ID, "")
				return tusd.HTTPResponse{Header: tusd.HTTPHeader{
					"Tibi-Item-Id":  p.Item.ID,
					"Tibi-Item-Url": s.cfg.BaseURL + "/" + p.Item.ID,
				}}, nil
			}
		}
	}
	s.store.FinishUpload(ctx, upID, "", err.Error())
	status := http.StatusInternalServerError
	var bre *ingest.BadRequestError
	switch {
	case errors.As(err, &bre), errors.Is(err, ingest.ErrEmpty), errors.Is(err, store.ErrSlugInvalid):
		status = http.StatusBadRequest
	case errors.Is(err, ingest.ErrTooLarge):
		status = http.StatusRequestEntityTooLarge
	}
	return tusd.HTTPResponse{}, tusd.NewError("ERR_INGEST", err.Error(), status)
}

// remove deletes an upload's files (data and tusd's .info).
func (r *resumable) remove(upID string) {
	os.Remove(filepath.Join(r.dir, upID))
	os.Remove(filepath.Join(r.dir, upID+".info"))
}

// ownUpload loads the upload named by {id}, which must belong to the
// calling token (or the token must be admin); otherwise it answers 404.
func (s *Server) ownUpload(w http.ResponseWriter, r *http.Request) (*store.Upload, bool) {
	u, err := s.store.GetUpload(r.Context(), r.PathValue("id"))
	tok := tokenFrom(r.Context())
	if err != nil || (u.TokenID != tok.ID && !tok.Has(store.ScopeAdmin)) {
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			s.internal(w, "upload", err)
			return nil, false
		}
		apiError(w, http.StatusNotFound, "not_found", "no such upload")
		return nil, false
	}
	return u, true
}

// tusRoute wraps a tusd handler: ownership for existing uploads, byte
// counting for PATCH. Both happen outside tusd's Middleware, which captures
// the request (and its body) on entry. tusd takes the upload ID from a path
// that has the base path stripped, as when mounted with http.StripPrefix.
func (s *Server) tusRoute(fn http.HandlerFunc, owned bool) http.Handler {
	inner := s.tus.h.Middleware(http.StripPrefix("/api/v1/uploads", fn))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if owned {
			if _, ok := s.ownUpload(w, r); !ok {
				return
			}
		}
		if r.Method == http.MethodPatch && r.Body != nil {
			r.Body = &countingReader{r.Body}
		}
		inner.ServeHTTP(w, r)
	})
}

type countingReader struct{ io.ReadCloser }

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.ReadCloser.Read(p)
	metrics.ResumableBytes.Add(int64(n))
	return n, err
}

// handleUploadItem serves GET /api/v1/uploads/{id}/item.
func (s *Server) handleUploadItem(w http.ResponseWriter, r *http.Request) {
	u, ok := s.ownUpload(w, r)
	if !ok {
		return
	}
	switch {
	case u.ItemID != "":
		it, err := s.store.GetItem(r.Context(), u.ItemID)
		if err != nil {
			apiError(w, http.StatusNotFound, "not_found", "the uploaded item no longer exists")
			return
		}
		writeJSON(w, http.StatusOK, s.itemJSON(it))
	case u.Error != "":
		apiError(w, http.StatusBadRequest, "bad_request", "upload could not be stored: "+u.Error)
	default:
		offset := int64(0)
		if up, err := s.tus.store.GetUpload(r.Context(), u.ID); err == nil {
			if info, err := up.GetInfo(r.Context()); err == nil {
				offset = info.Offset
			}
		}
		apiError(w, http.StatusConflict, "incomplete", "upload incomplete: "+strconv.FormatInt(offset, 10)+" of "+strconv.FormatInt(u.Size, 10)+" bytes received")
	}
}

// CleanupUploads removes unfinished uploads older than a day, and the
// records of finished ones.
func (s *Server) CleanupUploads(ctx context.Context) (int, error) {
	stale, err := s.store.StaleUploads(ctx, s.store.Now().Unix()-StaleUploadAge)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, u := range stale {
		if u.ItemID == "" {
			s.tus.remove(u.ID)
			n++
		}
		if err := s.store.DeleteUpload(ctx, u.ID); err != nil {
			return n, err
		}
	}
	return n, nil
}
