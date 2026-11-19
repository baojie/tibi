package store

import (
	"context"
	"path/filepath"
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
