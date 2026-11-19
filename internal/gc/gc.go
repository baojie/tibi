// Package gc removes expired and deleted items and their unreferenced blobs.
package gc

import (
	"context"
	"log/slog"
	"time"

	"tibi/internal/blob"
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
