// Package ingest implements the upload pipeline (PLAN A5): every upload,
// regardless of transport, is spooled, classified, hashed and stored here.
package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"regexp"
	"strings"

	"tibi/internal/blob"
	"tibi/internal/store"
)

// ErrTooLarge is returned when the upload exceeds the size limit.
var ErrTooLarge = errors.New("upload exceeds size limit")

// ErrEmpty is returned for zero-byte uploads.
var ErrEmpty = errors.New("upload is empty")

// Pipeline stores uploads.
type Pipeline struct {
	Store   *store.Store
	Blobs   blob.Store
	TempDir string
}

// Options are per-upload options supplied by the client.
type Options struct {
	Filename   string
	Title      string
	Visibility string
	Source     string
	Meta       string
	ExpiresAt  *int64
	TokenID    int64
	MaxBytes   int64
}

// Ingest reads r fully and creates an item.
func (p *Pipeline) Ingest(ctx context.Context, r io.Reader, o Options) (*store.Item, error) {
	tmp, err := os.CreateTemp(p.TempDir, "upload-*")
	if err != nil {
		return nil, err
	}
	defer func() {
		tmp.Close()
		os.Remove(tmp.Name())
	}()

	// 1. Spool, enforcing the limit as bytes arrive.
	h := sha256.New()
	limit := o.MaxBytes
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r, limit+1))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return nil, ErrTooLarge
		}
		return nil, err
	}
	if n > limit {
		return nil, ErrTooLarge
	}
	if n == 0 {
		return nil, ErrEmpty
	}

	// 2. Sniff.
	head := make([]byte, 512)
	m, _ := tmp.ReadAt(head, 0)
	mt := sniff(head[:m])

	// 3. Classify. 0.0.1: everything is a file.
	it := &store.Item{
		Kind:       store.KindFile,
		Mime:       mt,
		Filename:   cleanFilename(o.Filename),
		Size:       n,
		SHA256:     hex.EncodeToString(h.Sum(nil)),
		Title:      o.Title,
		Visibility: o.Visibility,
		Source:     o.Source,
		Meta:       o.Meta,
		TokenID:    o.TokenID,
		ExpiresAt:  o.ExpiresAt,
	}
	it.Ext = Ext(it.Filename, mt)

	// 5. Store blob (dedup by hash), then DB row.
	key := blob.Key(it.SHA256)
	ok, err := p.Blobs.Exists(ctx, key)
	if err != nil {
		return nil, err
	}
	if !ok {
		if _, err := tmp.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		if err := p.Blobs.Put(ctx, key, tmp, n, mt); err != nil {
			return nil, err
		}
	}
	if err := p.Store.CreateItem(ctx, it); err != nil {
		return nil, err
	}
	return it, nil
}

func sniff(b []byte) string {
	mt, _, err := mime.ParseMediaType(http.DetectContentType(b))
	if err != nil {
		return "application/octet-stream"
	}
	return mt
}

func cleanFilename(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base(name)
	if name == "." || name == "/" {
		return ""
	}
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	if len(name) > 255 {
		name = name[:255]
	}
	return name
}

var extRe = regexp.MustCompile(`^[a-z0-9]{1,10}$`)

// Ext picks the canonical extension for /{id}.{ext}.
func Ext(filename, mimeType string) string {
	if e := strings.ToLower(strings.TrimPrefix(path.Ext(filename), ".")); extRe.MatchString(e) {
		return e
	}
	switch mimeType {
	case "image/png":
		return "png"
	case "image/jpeg":
		return "jpg"
	case "image/gif":
		return "gif"
	case "image/webp":
		return "webp"
	case "application/pdf":
		return "pdf"
	case "text/plain":
		return "txt"
	}
	return "bin"
}
