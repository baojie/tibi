// Package migrate copies blobs between storage backends.
package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/baojie/tibi/internal/blob"
	"github.com/baojie/tibi/internal/store"
)

// Result summarizes a migration.
type Result struct {
	Total   int // distinct blobs referenced by items
	Copied  int // copied and verified
	Skipped int // already present at the destination
	Missing int // referenced but absent from the source
	Failed  int // copy or verification failed
}

// Run copies every blob referenced by an item from src to dst, verifying
// each copy by re-reading it and checking its SHA-256. It is idempotent and
// resumable: blobs already at the destination are skipped (both backends
// write atomically, so an existing object is complete). progress, if not
// nil, is called after each blob.
func Run(ctx context.Context, st *store.Store, src, dst blob.Store, workers int, progress func(Result)) (Result, error) {
	type job struct{ sha, mime string }
	var hashes []job
	if err := st.BlobHashes(ctx, func(sha, mime string) error { hashes = append(hashes, job{sha, mime}); return nil }); err != nil {
		return Result{}, err
	}
	var copied, skipped, missing, failed atomic.Int64
	var firstErr error
	var errOnce sync.Once
	report := func() {
		if progress != nil {
			progress(Result{len(hashes), int(copied.Load()), int(skipped.Load()), int(missing.Load()), int(failed.Load())})
		}
	}
	work := make(chan job)
	var wg sync.WaitGroup
	for range max(workers, 1) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range work {
				sha := j.sha
				switch err := copyOne(ctx, src, dst, sha, j.mime); {
				case err == nil:
					copied.Add(1)
				case errors.Is(err, errExists):
					skipped.Add(1)
				case errors.Is(err, blob.ErrNotFound):
					missing.Add(1)
					errOnce.Do(func() { firstErr = fmt.Errorf("blob %s missing from source", sha) })
				default:
					failed.Add(1)
					errOnce.Do(func() { firstErr = err })
				}
				report()
			}
		}()
	}
	for _, j := range hashes {
		select {
		case work <- j:
		case <-ctx.Done():
		}
	}
	close(work)
	wg.Wait()
	res := Result{len(hashes), int(copied.Load()), int(skipped.Load()), int(missing.Load()), int(failed.Load())}
	if err := ctx.Err(); err != nil {
		return res, err
	}
	return res, firstErr
}

var errExists = errors.New("already present")

func copyOne(ctx context.Context, src, dst blob.Store, sha, mime string) error {
	key := blob.Key(sha)
	if ok, err := dst.Exists(ctx, key); err != nil {
		return err
	} else if ok {
		return errExists
	}
	r, err := src.Open(ctx, key)
	if err != nil {
		return err
	}
	defer r.Close()
	size, err := r.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := dst.Put(ctx, key, r, size, mime); err != nil {
		return err
	}
	// Verify the stored copy end to end.
	got, err := hashOf(ctx, dst, key)
	if err != nil || got != sha {
		dst.Delete(ctx, key)
		if err == nil {
			err = fmt.Errorf("blob %s: copy has hash %s", sha, got)
		}
		return err
	}
	return nil
}

func hashOf(ctx context.Context, s blob.Store, key string) (string, error) {
	r, err := s.Open(ctx, key)
	if err != nil {
		return "", err
	}
	defer r.Close()
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
