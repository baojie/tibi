// Package id generates random base62 identifiers.
package id

import (
	"crypto/rand"
	"math/big"
	"strings"
)

const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// Len is the length of item and collection IDs (~47 bits).
const Len = 8

var max = big.NewInt(int64(len(alphabet)))

// Random returns n base62 characters from crypto/rand.
func Random(n int) string {
	var b strings.Builder
	b.Grow(n)
	for range n {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic("id: crypto/rand failed: " + err.Error())
		}
		b.WriteByte(alphabet[v.Int64()])
	}
	return b.String()
}

// reserved are top-level path names that must never be issued as IDs, even
// though current reserved paths are not 8 chars long; this guards future ones.
var reserved = map[string]bool{
	"robotstx": true, "favicons": true, "settings": true, "metadata": true,
	"download": true, "explorer": true, "ui-admin": true,
}

// IsReserved reports whether s must not be used as an ID.
func IsReserved(s string) bool { return reserved[strings.ToLower(s)] }

// New returns a fresh 8-char ID that is not on the reserved list.
func New() string {
	for {
		s := Random(Len)
		if !IsReserved(s) {
			return s
		}
	}
}

// Valid reports whether s is syntactically an ID.
func Valid(s string) bool {
	if len(s) != Len {
		return false
	}
	for i := 0; i < len(s); i++ {
		if strings.IndexByte(alphabet, s[i]) < 0 {
			return false
		}
	}
	return true
}
