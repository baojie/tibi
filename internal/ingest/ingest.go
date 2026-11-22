// Package ingest implements the upload pipeline (PLAN A5): every upload,
// regardless of transport, is spooled, classified, hashed and stored here.
package ingest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"

	_ "golang.org/x/image/webp"

	"github.com/baojie/tibi/internal/blob"
	"github.com/baojie/tibi/internal/e2e"
	"github.com/baojie/tibi/internal/render"
	"github.com/baojie/tibi/internal/store"
)

// ErrTooLarge is returned when the upload exceeds the size limit.
var ErrTooLarge = errors.New("upload exceeds size limit")

// ErrEmpty is returned for zero-byte uploads.
var ErrEmpty = errors.New("upload is empty")

// BadRequestError is a client error with a message safe to return.
type BadRequestError struct{ Msg string }

func (e *BadRequestError) Error() string { return e.Msg }

// Pipeline stores uploads.
type Pipeline struct {
	Store          *store.Store
	Blobs          blob.Store
	TempDir        string
	StripImageMeta bool
	// HEIC converts HEIC/HEIF to JPEG; nil stores such photos as-is.
	HEIC *Converter
	// Log receives non-fatal problems (failed conversion or thumbnail).
	Log *slog.Logger
}

// Options are per-upload options supplied by the client.
type Options struct {
	Filename   string
	Title      string
	Visibility string
	Kind       string // "", "text" or "file"
	Lang       string // language for text; inferred when empty
	Slug       string
	Burn       bool
	ForkedFrom string
	KeepMeta   bool
	Source     string
	Meta       string
	ExpiresAt  *int64
	TokenID    int64
	MaxBytes   int64
}

// Prepared is an upload that has been spooled and classified but not stored.
type Prepared struct {
	Item *store.Item
	file *os.File
}

// Close removes the spooled temp file.
func (p *Prepared) Close() {
	if p != nil && p.file != nil {
		p.file.Close()
		os.Remove(p.file.Name())
		p.file = nil
	}
}

// Ingest prepares and commits a single upload.
func (p *Pipeline) Ingest(ctx context.Context, r io.Reader, o Options) (*store.Item, error) {
	pr, err := p.Prepare(ctx, r, o)
	if err != nil {
		return nil, err
	}
	defer pr.Close()
	if err := p.Commit(ctx, pr); err != nil {
		return nil, err
	}
	return pr.Item, nil
}

// Prepare runs steps 1–4: spool, sniff, classify, strip metadata.
func (p *Pipeline) Prepare(ctx context.Context, r io.Reader, o Options) (*Prepared, error) {
	if err := checkKind(o.Kind); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(p.TempDir, "upload-*")
	if err != nil {
		return nil, err
	}
	pr := &Prepared{file: tmp}

	// 1. Spool, enforcing the limit as bytes arrive.
	limit := o.MaxBytes
	n, err := io.Copy(tmp, io.LimitReader(r, limit+1))
	if err != nil {
		pr.Close()
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return nil, ErrTooLarge
		}
		return nil, err
	}
	if n > limit {
		pr.Close()
		return nil, ErrTooLarge
	}
	return p.prepareSpooled(ctx, pr, n, o)
}

// PrepareFile is Prepare for content already on disk (a finished resumable
// upload): the file is moved into the pipeline instead of being copied. It
// must be on the same filesystem as TempDir.
func (p *Pipeline) PrepareFile(ctx context.Context, path string, o Options) (*Prepared, error) {
	if err := checkKind(o.Kind); err != nil {
		return nil, err
	}
	dst, err := os.CreateTemp(p.TempDir, "upload-*")
	if err != nil {
		return nil, err
	}
	dst.Close()
	if err := os.Rename(path, dst.Name()); err != nil {
		os.Remove(dst.Name())
		return nil, err
	}
	f, err := os.OpenFile(dst.Name(), os.O_RDWR, 0)
	if err != nil {
		os.Remove(dst.Name())
		return nil, err
	}
	pr := &Prepared{file: f}
	st, err := f.Stat()
	if err != nil {
		pr.Close()
		return nil, err
	}
	return p.prepareSpooled(ctx, pr, st.Size(), o)
}

func checkKind(k string) error {
	switch k {
	case "", store.KindText, store.KindFile, store.KindEncrypted:
		return nil
	}
	return &BadRequestError{fmt.Sprintf("kind %q cannot be forced (allowed: text, file, encrypted)", k)}
}

// prepareEncrypted stores client-side encrypted content as opaque bytes
// (PLAN A9): no sniffing, metadata stripping, thumbnails or indexing. The
// file name, if a client sent one, is dropped: it belongs inside the
// ciphertext. The language is a non-secret display hint.
func (p *Pipeline) prepareEncrypted(pr *Prepared, n int64, o Options) (*Prepared, error) {
	// The format's minimum size is well under 64 bytes, so the start of the
	// file is enough to check the header.
	if e2e.Check(readAll(pr.file, min(n, 64))) != nil {
		return nil, &BadRequestError{"kind=encrypted needs content in the tibi encrypted format (version " + strconv.Itoa(e2e.Version) + ")"}
	}
	lang := ""
	if o.Lang != "" {
		l, ok := render.ResolveLang(o.Lang)
		if !ok {
			return nil, &BadRequestError{fmt.Sprintf("unknown lang %q", o.Lang)}
		}
		lang = l
	}
	it := &store.Item{
		Kind: store.KindEncrypted, Mime: "application/octet-stream", Ext: "bin", Size: n,
		Title: o.Title, Lang: lang, Visibility: o.Visibility, Slug: o.Slug, BurnAfterRead: o.Burn,
		Source: o.Source, Meta: o.Meta, TokenID: o.TokenID, ExpiresAt: o.ExpiresAt, ForkedFrom: o.ForkedFrom,
	}
	h := sha256.New()
	if _, err := io.Copy(h, io.NewSectionReader(pr.file, 0, n)); err != nil {
		return nil, err
	}
	it.SHA256 = hex.EncodeToString(h.Sum(nil))
	pr.Item = it
	return pr, nil
}

func readAll(f *os.File, n int64) []byte {
	b := make([]byte, n)
	m, _ := f.ReadAt(b, 0)
	return b[:m]
}

// prepareSpooled runs steps 2–4 on n bytes spooled in pr.file.
func (p *Pipeline) prepareSpooled(ctx context.Context, pr *Prepared, n int64, o Options) (*Prepared, error) {
	ok := false
	defer func() {
		if !ok {
			pr.Close()
		}
	}()
	if n == 0 {
		return nil, ErrEmpty
	}
	if o.Kind == store.KindEncrypted {
		res, err := p.prepareEncrypted(pr, n, o)
		ok = err == nil
		return res, err
	}
	tmp := pr.file
	limit := max(o.MaxBytes, n)

	// 2–3. Sniff and classify.
	head := make([]byte, 512)
	m, _ := tmp.ReadAt(head, 0)
	head = head[:m]
	filename := cleanFilename(o.Filename)
	cl := Classify(head, filename, tmp, n)
	// HEIC/HEIF photos become JPEG when a converter is available; the
	// original type is kept in orig_mime.
	var origMime string
	if (cl.Mime == "image/heic" || cl.Mime == "image/heif") && p.HEIC != nil && o.Kind != store.KindFile {
		jpg, err := p.HEIC.ToJPEG(ctx, tmp.Name(), 4*limit)
		if err != nil {
			p.warn("heic conversion failed; storing as-is", "err", err)
		} else {
			if err := pr.replace(p.TempDir, jpg); err != nil {
				return nil, err
			}
			origMime, n = cl.Mime, int64(len(jpg))
			cl = Class{Mime: "image/jpeg", Kind: store.KindImage}
			if ext := path.Ext(filename); strings.EqualFold(ext, ".heic") || strings.EqualFold(ext, ".heif") {
				filename = strings.TrimSuffix(filename, ext) + ".jpg"
			}
		}
	}

	var charset string
	switch {
	case o.Kind == store.KindFile:
		cl.Kind = store.KindFile
	case cl.Kind == store.KindText:
	case o.Kind == store.KindText && IsUTF8Text(io.NewSectionReader(tmp, 0, n)):
		if !strings.HasPrefix(cl.Mime, "text/") && !activeType(cl.Mime) {
			cl.Mime = "text/plain"
		}
		cl.Kind = store.KindText
	case cl.MaybeText || o.Kind == store.KindText:
		data, err := os.ReadFile(tmp.Name())
		if err != nil {
			return nil, err
		}
		out, cs, ok := Transcode(data)
		if !ok {
			if o.Kind == store.KindText {
				return nil, &BadRequestError{"kind=text requires UTF-8 text or a recognizable legacy charset"}
			}
			break
		}
		if err := pr.replace(p.TempDir, out); err != nil {
			return nil, err
		}
		n = int64(len(out))
		cl = Class{Mime: "text/plain", Kind: store.KindText}
		charset = cs
	}

	var durationMS *int64
	if cl.Kind == store.KindVideo || cl.Kind == store.KindAudio {
		if d := MediaDuration(pr.file, n, cl.Mime); d > 0 {
			durationMS = &d
		}
	}

	var lang string
	if cl.Kind == store.KindText {
		if o.Lang != "" {
			l, ok := render.ResolveLang(o.Lang)
			if !ok {
				return nil, &BadRequestError{fmt.Sprintf("unknown lang %q", o.Lang)}
			}
			lang = l
		} else {
			sample := make([]byte, 64<<10)
			m, _ := pr.file.ReadAt(sample, 0)
			lang = render.InferLang(filename, sample[:m])
		}
	}

	it := &store.Item{
		Kind:          cl.Kind,
		Mime:          cl.Mime,
		OrigMime:      origMime,
		ForkedFrom:    o.ForkedFrom,
		DurationMS:    durationMS,
		Filename:      filename,
		Size:          n,
		Title:         o.Title,
		Lang:          lang,
		Charset:       charset,
		Visibility:    o.Visibility,
		Slug:          o.Slug,
		BurnAfterRead: o.Burn,
		Source:        o.Source,
		Meta:          o.Meta,
		TokenID:       o.TokenID,
		ExpiresAt:     o.ExpiresAt,
	}

	// 4. Images: strip metadata, read dimensions.
	if it.Kind == store.KindImage {
		if err := p.prepareImage(pr, it, o.KeepMeta, origMime == ""); err != nil {
			return nil, err
		}
	}
	it.Ext = Ext(it.Kind, filename, it.Mime)
	if it.Kind == store.KindText && extFromName(filename) == "" {
		it.Ext = render.ExtForLang(lang)
	}

	// Hash the stored bytes.
	if _, err := pr.file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	h := sha256.New()
	if _, err := io.Copy(h, pr.file); err != nil {
		return nil, err
	}
	it.SHA256 = hex.EncodeToString(h.Sum(nil))
	pr.Item = it
	if it.Kind == store.KindImage {
		// Thumbnails are made when first requested (server handleThumb);
		// an identical image may already have one.
		if ok, err := p.Blobs.Exists(ctx, blob.ThumbKey(it.SHA256, ThumbWidth)); err == nil && ok {
			it.Thumb = true
		}
	}
	if it.Kind == store.KindText {
		body := make([]byte, store.MaxIndexBytes)
		m, _ := pr.file.ReadAt(body, 0)
		it.IndexBody = string(body[:m])
	}
	ok = true
	return pr, nil
}

func (p *Pipeline) prepareImage(pr *Prepared, it *store.Item, keepMeta, keepOrientation bool) error {
	data, err := os.ReadFile(pr.file.Name())
	if err != nil {
		return err
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err == nil {
		w, h := cfg.Width, cfg.Height
		if it.Mime == "image/jpeg" && keepOrientation && JPEGOrientation(data) >= 5 {
			w, h = h, w // displayed rotated by 90°
		}
		it.Width, it.Height = &w, &h
	} else if it.Mime != "image/avif" {
		// Claims to be an image but does not parse: store it as a plain file.
		it.Kind = store.KindFile
		return nil
	}
	if keepMeta || !p.StripImageMeta {
		return nil
	}
	stripped, err := stripMeta(it.Mime, data, keepOrientation)
	if err != nil {
		it.Kind = store.KindFile // malformed; never render it
		return nil
	}
	if bytes.Equal(stripped, data) {
		return nil
	}
	if err := pr.replace(p.TempDir, stripped); err != nil {
		return err
	}
	it.Size = int64(len(stripped))
	return nil
}

// replace swaps the spooled file for one holding b.
func (pr *Prepared) replace(dir string, b []byte) error {
	f, err := os.CreateTemp(dir, "upload-*")
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(f.Name())
		return err
	}
	pr.Close()
	pr.file = f
	return nil
}

// Backfill indexes items stored before search existed (or whose index rows
// are missing), reading text content from the blob store.
func Backfill(ctx context.Context, st *store.Store, bs blob.Store) (int, error) {
	items, err := st.Unindexed(ctx)
	if err != nil {
		return 0, err
	}
	for _, it := range items {
		body := ""
		if it.Kind == store.KindText && !it.BurnAfterRead {
			if f, err := bs.Open(ctx, blob.Key(it.SHA256)); err == nil {
				b, _ := io.ReadAll(io.LimitReader(f, store.MaxIndexBytes))
				f.Close()
				body = string(b)
			}
		}
		if err := st.Index(ctx, it.ID, body); err != nil {
			return 0, err
		}
	}
	return len(items), nil
}

func (p *Pipeline) warn(msg string, args ...any) {
	if p.Log != nil {
		p.Log.Warn(msg, args...)
	}
}

// Commit runs step 5: store the blob (dedup by hash), then the DB row.
// BlobMu keeps GC from deleting the blob between the check and the insert.
func (p *Pipeline) Commit(ctx context.Context, pr *Prepared) error {
	it := pr.Item
	p.Store.BlobMu.Lock()
	defer p.Store.BlobMu.Unlock()
	key := blob.Key(it.SHA256)
	exists, err := p.Blobs.Exists(ctx, key)
	if err != nil {
		return err
	}
	if !exists {
		if _, err := pr.file.Seek(0, io.SeekStart); err != nil {
			return err
		}
		if err := p.Blobs.Put(ctx, key, pr.file, it.Size, it.Mime); err != nil {
			return err
		}
	}
	return p.Store.CreateItem(ctx, it)
}

func activeType(mt string) bool {
	for _, v := range activeByExt {
		if v == mt {
			return true
		}
	}
	return false
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

// Ext picks the canonical extension for /{id}.{ext}. Images always use the
// extension of their real type, so raw URLs embed correctly.
func Ext(kind, filename, mimeType string) string {
	if kind == store.KindImage {
		if e, ok := imageMimes[mimeType]; ok {
			return e
		}
	}
	if kind == store.KindVideo || kind == store.KindAudio {
		if e, ok := mediaMimes[mimeType]; ok {
			return e
		}
	}
	if e := extFromName(filename); e != "" {
		return e
	}
	if e, ok := imageMimes[mimeType]; ok {
		return e
	}
	if e, ok := mediaMimes[mimeType]; ok {
		return e
	}
	switch mimeType {
	case "application/pdf":
		return "pdf"
	case "text/plain":
		return "txt"
	case "text/xml":
		return "xml"
	case "text/html":
		return "html"
	case "image/svg+xml":
		return "svg"
	case "image/heic", "image/heif":
		return "heic"
	}
	return "bin"
}

// extFromName returns the filename's extension if it is usable in a URL.
func extFromName(filename string) string {
	if e := strings.ToLower(strings.TrimPrefix(path.Ext(filename), ".")); extRe.MatchString(e) {
		return e
	}
	return ""
}
