package server

import (
	"bytes"
	"context"
	"io"
	"sync"

	"github.com/baojie/tibi/internal/blob"
	"github.com/baojie/tibi/internal/ingest"
	"github.com/baojie/tibi/internal/store"
)

// thumbMaker serializes making thumbnails (decoding is memory-heavy; see
// ingest.MakeThumb) and remembers images none can be made for, so such an
// image is not fetched and decoded again on every request.
type thumbMaker struct {
	sync.Mutex
	none map[string]bool // sha256 → no thumbnail possible
}

// makeThumb renders the thumbnail of an image that has none yet, stores it
// next to the blob and marks every item with that image. store.ErrNotFound
// when none can be made.
func (s *Server) makeThumb(ctx context.Context, it *store.Item) error {
	if !ingest.Thumbable(it.Mime, it.Size) {
		return store.ErrNotFound
	}
	s.thumbs.Lock()
	defer s.thumbs.Unlock()
	if s.thumbs.none[it.SHA256] {
		return store.ErrNotFound
	}
	tk := blob.ThumbKey(it.SHA256, ingest.ThumbWidth)
	if ok, err := s.blobs.Exists(ctx, tk); err != nil {
		return err
	} else if !ok {
		f, err := s.blobs.Open(ctx, blob.Key(it.SHA256))
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(f, ingest.MaxThumbSource+1))
		f.Close()
		if err != nil {
			return err
		}
		thumb, err := ingest.MakeThumb(data, it.Mime)
		if err != nil {
			if !ingest.ErrNoThumb(err) {
				s.log.Warn("thumbnail failed", "item", it.ID, "err", err)
			}
			if s.thumbs.none == nil {
				s.thumbs.none = map[string]bool{}
			}
			s.thumbs.none[it.SHA256] = true
			return store.ErrNotFound
		}
		// BlobMu: GC must not delete the image (and its thumbnail) between
		// this check and the write, leaving an orphan thumbnail.
		s.store.BlobMu.Lock()
		defer s.store.BlobMu.Unlock()
		if ref, err := s.store.BlobReferenced(ctx, it.SHA256); err != nil {
			return err
		} else if !ref {
			return store.ErrNotFound
		}
		if err := s.blobs.Put(ctx, tk, bytes.NewReader(thumb), int64(len(thumb)), "image/webp"); err != nil {
			return err
		}
	}
	it.Thumb = true
	return s.store.SetThumb(ctx, it.SHA256)
}
