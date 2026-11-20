package sign

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestSign(t *testing.T) {
	p := filepath.Join(t.TempDir(), "secret.key")
	s, err := LoadOrCreate(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
	s2, _ := LoadOrCreate(p) // same key on restart
	sig := s.Sign("aZ3k9QxP", 2000)
	// Flip a character in the middle (the last base64 character carries
	// padding bits that decoding ignores, so it is no place to tamper).
	flip := map[byte]byte{'A': 'B'}
	c := sig[10]
	if r, ok := flip[c]; ok {
		c = r
	} else {
		c = 'A'
	}
	tampered := sig[:10] + string(c) + sig[11:]
	if !s2.Valid("aZ3k9QxP", "2000", sig, 1999) {
		t.Fatal("valid signature rejected")
	}
	for _, c := range []struct {
		id, exp, sig string
		now          int64
	}{
		{"aZ3k9QxP", "2000", sig, 2000},   // expired
		{"bZ3k9QxP", "2000", sig, 1},      // other item
		{"aZ3k9QxP", "3000", sig, 1},      // extended expiry
		{"aZ3k9QxP", "2000", tampered, 1}, // tampered
		{"aZ3k9QxP", "x", sig, 1},
		{"aZ3k9QxP", "2000", "", 1},
		{"aZ3k9QxP", "2000", "!!!", 1},
	} {
		if s.Valid(c.id, c.exp, c.sig, c.now) {
			t.Errorf("accepted %+v", c)
		}
	}
	os.Chmod(p, 0o644)
	if _, err := LoadOrCreate(p); err == nil {
		t.Fatal("world-readable secret accepted")
	}
	other, _ := New(make([]byte, 32))
	if other.Valid("aZ3k9QxP", strconv.Itoa(2000), sig, 1) {
		t.Fatal("signature valid under another key")
	}
}
