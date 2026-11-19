package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FS stores blobs under a local directory.
type FS struct {
	root string
}

// NewFS returns an FS store rooted at dir, creating it if needed.
func NewFS(dir string) (*FS, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	return &FS{root: dir}, nil
}

func (s *FS) path(key string) (string, error) {
	if key == "" || strings.Contains(key, "..") || strings.HasPrefix(key, "/") {
		return "", fmt.Errorf("blob: invalid key %q", key)
	}
	return filepath.Join(s.root, filepath.FromSlash(key)), nil
}

// Put writes r to key via a temp file and atomic rename.
func (s *FS) Put(ctx context.Context, key string, r io.Reader, size int64, mime string) error {
	p, err := s.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".put-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op after successful rename
	n, err := io.Copy(f, r)
	if err == nil && size >= 0 && n != size {
		err = fmt.Errorf("blob: wrote %d bytes, expected %d", n, size)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Open opens key for reading.
func (s *FS) Open(ctx context.Context, key string) (io.ReadSeekCloser, error) {
	p, err := s.path(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	return f, err
}

// Delete removes key; deleting a missing key is not an error.
func (s *FS) Delete(ctx context.Context, key string) error {
	p, err := s.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// Exists reports whether key is present.
func (s *FS) Exists(ctx context.Context, key string) (bool, error) {
	p, err := s.path(key)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// List calls fn for every key starting with prefix.
func (s *FS) List(ctx context.Context, prefix string, fn func(Info) error) error {
	return filepath.WalkDir(s.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || strings.HasPrefix(d.Name(), ".put-") {
			return nil
		}
		rel, err := filepath.Rel(s.root, p)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		if !strings.HasPrefix(key, prefix) {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		return fn(Info{Key: key, Size: fi.Size(), ModTime: fi.ModTime()})
	})
}

// PresignGet is unsupported for the fs backend.
func (s *FS) PresignGet(ctx context.Context, key string, ttl time.Duration, filename, mime string) (string, bool, error) {
	return "", false, nil
}
