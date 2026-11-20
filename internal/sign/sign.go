// Package sign makes and checks time-limited share links for private items
// (PLAN A8.9): an HMAC-SHA256 over "id|expiry" with a server secret.
package sign

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Signer signs item IDs with an expiry.
type Signer struct {
	key []byte
}

// New returns a signer for key (at least 32 bytes).
func New(key []byte) (*Signer, error) {
	if len(key) < 32 {
		return nil, errors.New("sign: key must be at least 32 bytes")
	}
	return &Signer{key: key}, nil
}

// LoadOrCreate reads the secret from path, creating a random one (mode
// 0600) on first start. A secret file readable by others is refused.
func LoadOrCreate(path string) (*Signer, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, fmt.Errorf("secret key %s: %w", path, err)
		}
		if _, err := f.WriteString(hex.EncodeToString(key) + "\n"); err != nil {
			f.Close()
			return nil, err
		}
		if err := f.Close(); err != nil {
			return nil, err
		}
		return New(key)
	}
	if err != nil {
		return nil, fmt.Errorf("secret key %s: %w", path, err)
	}
	if fi, err := os.Stat(path); err == nil && fi.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("secret key %s must not be readable by others (chmod 600)", path)
	}
	key, err := hex.DecodeString(strings.TrimSpace(string(b)))
	if err != nil {
		return nil, fmt.Errorf("secret key %s: not hex", path)
	}
	return New(key)
}

func (s *Signer) mac(id string, exp int64) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(id + "|" + strconv.FormatInt(exp, 10)))
	return m.Sum(nil)
}

// Sign returns the signature for id valid until exp (unix seconds).
func (s *Signer) Sign(id string, exp int64) string {
	return base64.RawURLEncoding.EncodeToString(s.mac(id, exp))
}

// Query returns "exp=…&sig=…" for appending to a URL.
func (s *Signer) Query(id string, exp int64) string {
	return "exp=" + strconv.FormatInt(exp, 10) + "&sig=" + s.Sign(id, exp)
}

// Valid reports whether sig authorizes id at unix time now.
func (s *Signer) Valid(id, expStr, sig string, now int64) bool {
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || exp <= now || sig == "" {
		return false
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return false
	}
	return hmac.Equal(got, s.mac(id, exp))
}
