package blob

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestFS(t *testing.T) {
	ctx := context.Background()
	s, err := NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key := Key("abcdef0123456789")
	if key != "ab/cd/abcdef0123456789" {
		t.Fatalf("key = %q", key)
	}
	if ok, _ := s.Exists(ctx, key); ok {
		t.Fatal("exists before put")
	}
	if err := s.Put(ctx, key, strings.NewReader("hello"), 5, "text/plain"); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "x/y", strings.NewReader("hello"), 6, ""); err == nil {
		t.Fatal("size mismatch accepted")
	}
	f, err := s.Open(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(f)
	f.Close()
	if string(b) != "hello" {
		t.Fatalf("got %q", b)
	}
	var keys []string
	s.List(ctx, "", func(k string) error { keys = append(keys, k); return nil })
	if len(keys) != 1 || keys[0] != key {
		t.Fatalf("list = %v", keys)
	}
	if err := s.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(ctx, key); err != ErrNotFound {
		t.Fatalf("open after delete: %v", err)
	}
	if _, err := s.Open(ctx, "../etc/passwd"); err == nil {
		t.Fatal("path traversal allowed")
	}
}
