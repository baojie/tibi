// Package blob defines content-addressed blob storage and its backends.
package blob

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrNotFound is returned by Open when the key does not exist.
var ErrNotFound = errors.New("blob: not found")

// Store is a content-addressed blob store (see PLAN A7).
type Store interface {
	Put(ctx context.Context, key string, r io.Reader, size int64, mime string) error
	Open(ctx context.Context, key string) (io.ReadSeekCloser, error)
	Delete(ctx context.Context, key string) error
	Exists(ctx context.Context, key string) (bool, error)
	// List calls fn for every key that starts with prefix (for orphan GC
	// and migration).
	List(ctx context.Context, prefix string, fn func(Info) error) error
	PresignGet(ctx context.Context, key string, ttl time.Duration, filename, mime string) (url string, ok bool, err error)
}

// Info describes a stored blob.
type Info struct {
	Key     string
	Size    int64
	ModTime time.Time
}

// Key returns the storage key for a hex sha256: ab/cd/<sha256>.
func Key(sha256hex string) string {
	return sha256hex[0:2] + "/" + sha256hex[2:4] + "/" + sha256hex
}
