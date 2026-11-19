package blob_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"tibi/internal/blob"
	"tibi/internal/testutil/s3test"
)

func backends(t *testing.T) map[string]func(t *testing.T) blob.Store {
	return map[string]func(t *testing.T) blob.Store{
		"fs": func(t *testing.T) blob.Store {
			s, err := blob.NewFS(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			return s
		},
		"s3": func(t *testing.T) blob.Store {
			s, err := blob.NewS3(s3test.Config(t))
			if err != nil {
				t.Fatal(err)
			}
			return s
		},
	}
}

func TestConformance(t *testing.T) {
	for name, mk := range backends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			s := mk(t)
			if err := blob.Probe(ctx, s); err != nil {
				t.Fatal(err)
			}
			data := make([]byte, 300_000)
			rand.Read(data)
			key := blob.Key("abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789")
			if ok, err := s.Exists(ctx, key); ok || err != nil {
				t.Fatalf("exists before put: %v %v", ok, err)
			}
			if _, err := s.Open(ctx, key); err != blob.ErrNotFound {
				t.Fatalf("open missing: %v", err)
			}
			if err := s.Put(ctx, key, bytes.NewReader(data), int64(len(data)), "image/png"); err != nil {
				t.Fatal(err)
			}
			if ok, _ := s.Exists(ctx, key); !ok {
				t.Fatal("not found after put")
			}
			f, err := s.Open(ctx, key)
			if err != nil {
				t.Fatal(err)
			}
			got, _ := io.ReadAll(f)
			if !bytes.Equal(got, data) {
				t.Fatal("content differs")
			}
			// Seek (used for Range requests).
			if _, err := f.Seek(1000, io.SeekStart); err != nil {
				t.Fatal(err)
			}
			part := make([]byte, 100)
			if _, err := io.ReadFull(f, part); err != nil || !bytes.Equal(part, data[1000:1100]) {
				t.Fatalf("seek+read: %v", err)
			}
			if end, _ := f.Seek(0, io.SeekEnd); end != int64(len(data)) {
				t.Fatalf("seek end = %d", end)
			}
			f.Close()

			s.Put(ctx, "thumb/ab/cd/x-200.webp", strings.NewReader("t"), 1, "image/webp")
			var keys []string
			var info blob.Info
			s.List(ctx, "", func(i blob.Info) error {
				keys = append(keys, i.Key)
				if i.Key == key {
					info = i
				}
				return nil
			})
			sort.Strings(keys)
			if strings.Join(keys, ",") != key+",thumb/ab/cd/x-200.webp" {
				t.Fatalf("list = %v", keys)
			}
			if info.Size != int64(len(data)) || time.Since(info.ModTime) > time.Hour {
				t.Fatalf("info = %+v", info)
			}
			var thumbs []string
			s.List(ctx, "thumb/", func(i blob.Info) error { thumbs = append(thumbs, i.Key); return nil })
			if len(thumbs) != 1 {
				t.Fatalf("prefix list = %v", thumbs)
			}

			u, ok, err := s.PresignGet(ctx, key, time.Minute, "", "image/png")
			if err != nil {
				t.Fatal(err)
			}
			if name == "s3" {
				if !ok {
					t.Fatal("s3 cannot presign")
				}
				resp, err := http.Get(u)
				if err != nil {
					t.Fatal(err)
				}
				b, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if resp.StatusCode != 200 || !bytes.Equal(b, data) || resp.Header.Get("Content-Type") != "image/png" {
					t.Fatalf("presigned GET: %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
				}
			} else if ok {
				t.Fatal("fs claims presign support")
			}

			if err := s.Delete(ctx, key); err != nil {
				t.Fatal(err)
			}
			if err := s.Delete(ctx, key); err != nil {
				t.Fatalf("delete missing: %v", err)
			}
			if ok, _ := s.Exists(ctx, key); ok {
				t.Fatal("exists after delete")
			}
			if err := s.Put(ctx, "../escape", strings.NewReader("x"), 1, ""); err == nil {
				t.Fatal("path traversal accepted")
			}
		})
	}
}
