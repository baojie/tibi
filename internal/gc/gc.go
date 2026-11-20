// Package gc removes expired and deleted items and their unreferenced blobs.
package gc

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"tibi/internal/blob"
	"tibi/internal/ingest"
	"tibi/internal/metrics"
	"tibi/internal/store"
)

// Interval is how often the server runs GC.
const Interval = 10 * time.Minute

// Result reports one GC pass.
type Result struct {
	Items int // item rows hard-deleted
	Blobs int // blobs deleted
}

// Run performs one pass: hard-delete expired items and items soft-deleted
// more than a day ago, then delete blobs nothing references any more.
func Run(ctx context.Context, st *store.Store, bs blob.Store) (Result, error) {
	pr, err := st.Purge(ctx)
	if err != nil {
		return Result{}, err
	}
	res := Result{Items: pr.Items}
	defer func() {
		metrics.GCItems.Add(int64(res.Items))
		metrics.GCBlobs.Add(int64(res.Blobs))
	}()
	for _, sha := range pr.Hashes {
		deleted, err := deleteIfUnreferenced(ctx, st, bs, sha)
		if err != nil {
			return res, err
		}
		if deleted {
			res.Blobs++
		}
	}
	return res, nil
}

func deleteIfUnreferenced(ctx context.Context, st *store.Store, bs blob.Store, sha string) (bool, error) {
	st.BlobMu.Lock()
	defer st.BlobMu.Unlock()
	ref, err := st.BlobReferenced(ctx, sha)
	if err != nil || ref {
		return false, err
	}
	if err := bs.Delete(ctx, blob.ThumbKey(sha, ingest.ThumbWidth)); err != nil {
		return false, err
	}
	return true, bs.Delete(ctx, blob.Key(sha))
}

// Loop runs GC immediately and then every interval until ctx is done.
func Loop(ctx context.Context, st *store.Store, bs blob.Store, interval time.Duration, log *slog.Logger) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		res, err := Run(ctx, st, bs)
		switch {
		case err != nil && ctx.Err() == nil:
			log.Error("gc", "err", err)
		case res.Items > 0 || res.Blobs > 0:
			log.Info("gc", "items", res.Items, "blobs", res.Blobs)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// OrphanMinAge protects blobs written moments ago by an upload that has not
// inserted its row yet (possibly in another process, e.g. while
// `tibi admin gc --orphans` runs beside the server).
const OrphanMinAge = time.Hour

// Orphans deletes stored blobs that no item row references and that are
// older than minAge. Such blobs are left behind by crashes between storing a
// blob and inserting its row. It returns how many it deleted.
func Orphans(ctx context.Context, st *store.Store, bs blob.Store, minAge time.Duration) (int, error) {
	var candidates []string
	cutoff := time.Now().Add(-minAge)
	err := bs.List(ctx, "", func(i blob.Info) error {
		if i.ModTime.Before(cutoff) {
			candidates = append(candidates, i.Key)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, key := range candidates {
		sha := hashFromKey(key)
		if sha == "" {
			continue // not a blob key we manage
		}
		deleted, err := deleteKeyIfUnreferenced(ctx, st, bs, sha, key)
		if err != nil {
			return n, err
		}
		if deleted {
			n++
		}
	}
	return n, nil
}

// hashFromKey extracts the sha256 from "ab/cd/<sha>" or
// "thumb/ab/cd/<sha>-<w>.webp"; "" for anything else.
func hashFromKey(key string) string {
	base := key[strings.LastIndexByte(key, '/')+1:]
	if i := strings.IndexAny(base, "-."); i >= 0 {
		base = base[:i]
	}
	if len(base) != 64 || strings.Trim(base, "0123456789abcdef") != "" {
		return ""
	}
	return base
}

func deleteKeyIfUnreferenced(ctx context.Context, st *store.Store, bs blob.Store, sha, key string) (bool, error) {
	st.BlobMu.Lock()
	defer st.BlobMu.Unlock()
	ref, err := st.BlobReferenced(ctx, sha)
	if err != nil || ref {
		return false, err
	}
	return true, bs.Delete(ctx, key)
}
