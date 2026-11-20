package e2e

import (
	"bytes"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	k, frag := NewKey()
	if len(frag) != 43 {
		t.Fatalf("fragment length %d", len(frag))
	}
	if k2, err := ParseKey("k=" + frag); err != nil || !bytes.Equal(k, k2) {
		t.Fatal("ParseKey")
	}
	content := []byte("the secret plan 题壁")
	blob, err := Encrypt(k, "", Header{Name: "plan.txt", Type: "text/plain", Lang: "text"}, content)
	if err != nil {
		t.Fatal(err)
	}
	if blob[0] != 1 || bytes.Contains(blob, content) || bytes.Contains(blob, []byte("plan.txt")) {
		t.Fatal("plaintext or name visible in ciphertext")
	}
	h, got, err := Decrypt(blob, k, "")
	if err != nil || !bytes.Equal(got, content) || h.Name != "plan.txt" {
		t.Fatalf("decrypt: %v %+v", err, h)
	}
	other, _ := NewKey()
	if _, _, err := Decrypt(blob, other, ""); err != ErrKey {
		t.Fatalf("wrong key: %v", err)
	}
	tampered := append([]byte{}, blob...)
	tampered[len(tampered)-1] ^= 1
	if _, _, err := Decrypt(tampered, k, ""); err != ErrKey {
		t.Fatalf("tampered: %v", err)
	}
	if err := Check([]byte("hello")); err != ErrFormat {
		t.Fatal("plain text passed Check")
	}
}

func TestPassword(t *testing.T) {
	k, _ := NewKey()
	blob, err := Encrypt(k, "correct horse", Header{}, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if !NeedsPassword(blob) {
		t.Fatal("password flag not set")
	}
	if _, _, err := Decrypt(blob, k, ""); err != ErrPassword {
		t.Fatalf("no password: %v", err)
	}
	if _, _, err := Decrypt(blob, k, "wrong"); err != ErrKey {
		t.Fatalf("wrong password: %v", err)
	}
	if _, got, err := Decrypt(blob, k, "correct horse"); err != nil || string(got) != "x" {
		t.Fatalf("right password: %v", err)
	}
	// The header (with salt and iterations) is authenticated.
	blob[3] ^= 1
	if _, _, err := Decrypt(blob, k, "correct horse"); err != ErrKey {
		t.Fatalf("tampered salt: %v", err)
	}
}
