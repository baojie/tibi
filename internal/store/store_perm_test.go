package store

import (
	"os"
	"path/filepath"
	"testing"
)

// The database holds private items' metadata: owner-only when created, and
// never world-readable afterwards.
func TestDatabaseFileMode(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "tibi.db")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Fatalf("new database mode %o", fi.Mode().Perm())
	}
	os.Chmod(p, 0o644)
	s, err = Open(p)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o640 {
		t.Fatalf("world-readable database left as %o", fi.Mode().Perm())
	}
}
