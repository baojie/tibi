package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestMigrationsIdempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.db")
	for range 2 {
		s, err := Open(p)
		if err != nil {
			t.Fatal(err)
		}
		v, _ := s.SchemaVersion(context.Background())
		if v < 1 {
			t.Fatalf("schema version %d", v)
		}
		s.Close()
	}
}

func TestTokens(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	secret, tok, err := s.CreateToken(ctx, "a", []string{ScopeUpload}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(secret) != len(TokenPrefix)+32 {
		t.Fatalf("token length %d", len(secret))
	}
	if _, _, err := s.CreateToken(ctx, "a", []string{ScopeUpload}, nil); err == nil {
		t.Fatal("duplicate name accepted")
	}
	got, err := s.Authenticate(ctx, secret)
	if err != nil || got.ID != tok.ID || !got.Has(ScopeUpload) || got.Has(ScopeDelete) {
		t.Fatalf("authenticate: %+v %v", got, err)
	}
	// The secret itself is never stored.
	var n int
	s.DB().QueryRow(`SELECT COUNT(*) FROM tokens WHERE hash = ?`, secret).Scan(&n)
	if n != 0 {
		t.Fatal("plaintext token stored")
	}
	if err := s.RevokeToken(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, secret); err != ErrNotFound {
		t.Fatalf("revoked token authenticated: %v", err)
	}
	if err := s.RevokeToken(ctx, "a"); err == nil {
		t.Fatal("double revoke succeeded")
	}
	admin, _, _ := s.CreateToken(ctx, "root", []string{ScopeAdmin}, nil)
	at, _ := s.Authenticate(ctx, admin)
	if !at.Has(ScopeDelete) {
		t.Fatal("admin should imply all scopes")
	}
}

func TestParseScopes(t *testing.T) {
	if _, err := ParseScopes("upload,bogus"); err == nil {
		t.Fatal("bogus scope accepted")
	}
	got, err := ParseScopes(" upload, delete ,upload")
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
}

func TestExpiredAndDeletedHidden(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	_, tok, _ := s.CreateToken(ctx, "a", []string{ScopeUpload}, nil)
	past := time.Now().Add(-time.Minute).Unix()
	it := &Item{Kind: KindFile, Mime: "text/plain", Ext: "txt", Size: 1, SHA256: "00", TokenID: tok.ID, ExpiresAt: &past}
	if err := s.CreateItem(ctx, it); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetItem(ctx, it.ID); err != ErrNotFound {
		t.Fatalf("expired item visible: %v", err)
	}
	it2 := &Item{Kind: KindFile, Mime: "text/plain", Ext: "txt", Size: 1, SHA256: "00", TokenID: tok.ID}
	s.CreateItem(ctx, it2)
	s.DB().Exec(`UPDATE items SET deleted_at = 1 WHERE id = ?`, it2.ID)
	if _, err := s.GetItem(ctx, it2.ID); err != ErrNotFound {
		t.Fatalf("deleted item visible: %v", err)
	}
}

// Migration 0008 takes source_url from the page_url Chrome kept in meta.
func TestMigrationSourceURL(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.db")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, tok, err := s.CreateToken(ctx, "a", []string{ScopeUpload}, nil)
	if err != nil {
		t.Fatal(err)
	}
	add := func(meta string) string {
		it := &Item{Kind: KindText, Mime: "text/plain", Ext: "txt", SHA256: "aa" + strings.Repeat("0", 62), Meta: meta, TokenID: tok.ID}
		if err := s.CreateItem(ctx, it); err != nil {
			t.Fatal(err)
		}
		return it.ID
	}
	page := add(`{"page_url":"https://example.com/post?id=1"}`)
	bad := add(`not json`)
	js := add(`{"page_url":"javascript:alert(1)"}`)
	num := add(`{"page_url":42}`)
	none := add("")
	// Back to schema 7, as an older server left it.
	for _, q := range []string{`ALTER TABLE items DROP COLUMN source_url`, `DELETE FROM schema_migrations WHERE version = 8`} {
		if _, err := s.DB().ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	if s, err = Open(p); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for id, want := range map[string]string{page: "https://example.com/post?id=1", bad: "", js: "", num: "", none: ""} {
		it, err := s.GetItem(ctx, id)
		if err != nil || it.SourceURL != want {
			t.Errorf("%s: source_url %q, want %q (%v)", id, it.SourceURL, want, err)
		}
	}
}
