// Package e2e implements tibi's end-to-end encrypted item format (PLAN A9).
// The server only ever stores the ciphertext; the key travels in the URL
// fragment (#k=…), which browsers never send to the server.
//
// Format, version 1:
//
//	byte 0       version (1)
//	byte 1       flags: bit 0 = password-protected
//	[16 bytes]   PBKDF2 salt          (only when password-protected)
//	[4 bytes]    PBKDF2 iterations, big-endian (ditto)
//	12 bytes     AES-GCM nonce
//	rest         AES-256-GCM(payload), additional data = all bytes before the nonce
//
// payload = 4-byte big-endian header length | header JSON | content, where
// the header holds the file name and type, so those stay secret too.
//
// Key: K is 32 random bytes, encoded base64url in the fragment. The AES key
// is HKDF-SHA256(K, info "tibi-e2e-v1"); with a password it is
// HKDF-SHA256(K ‖ PBKDF2-SHA256(password, salt, iterations, 32)). Browsers
// decrypt the same format with WebCrypto (web/static/e2e.js).
package e2e

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
)

// Version is the format version written by Encrypt.
const Version = 1

const (
	flagPassword = 1
	info         = "tibi-e2e-v1"
	// Iterations is the PBKDF2 work factor for passwords (OWASP 2023).
	Iterations = 600_000
	keyLen     = 32
	saltLen    = 16
	nonceLen   = 12
)

// Header describes the encrypted content; it is encrypted with it.
type Header struct {
	Name string `json:"name,omitempty"`
	Type string `json:"type,omitempty"` // MIME type
	Lang string `json:"lang,omitempty"` // highlighting language for text
}

// Errors.
var (
	ErrFormat   = errors.New("not a tibi encrypted item, or a newer format")
	ErrKey      = errors.New("the key (or password) is wrong")
	ErrPassword = errors.New("this item is protected by a password")
)

// NewKey returns a random key and its URL-fragment encoding.
func NewKey() ([]byte, string) {
	k := make([]byte, keyLen)
	rand.Read(k)
	return k, base64.RawURLEncoding.EncodeToString(k)
}

// ParseKey decodes the fragment form of a key ("k=…" or just the value).
func ParseKey(s string) ([]byte, error) {
	if i := bytes.IndexByte([]byte(s), '='); i >= 0 && s[:i] == "k" {
		s = s[i+1:]
	}
	k, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(k) != keyLen {
		return nil, errors.New("malformed key")
	}
	return k, nil
}

func deriveKey(k []byte, password string, salt []byte, iter int) ([]byte, error) {
	ikm := append([]byte{}, k...)
	if password != "" {
		p, err := pbkdf2.Key(sha256.New, password, salt, iter, keyLen)
		if err != nil {
			return nil, err
		}
		ikm = append(ikm, p...)
	}
	return hkdf.Key(sha256.New, ikm, nil, info, keyLen)
}

func gcm(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Encrypt seals content with key k (and an optional password).
func Encrypt(k []byte, password string, h Header, content []byte) ([]byte, error) {
	if len(k) != keyLen {
		return nil, errors.New("key must be 32 bytes")
	}
	head := []byte{Version, 0}
	var salt []byte
	iter := 0
	if password != "" {
		head[1] |= flagPassword
		salt = make([]byte, saltLen)
		rand.Read(salt)
		iter = Iterations
		head = append(head, salt...)
		head = binary.BigEndian.AppendUint32(head, uint32(iter))
	}
	key, err := deriveKey(k, password, salt, iter)
	if err != nil {
		return nil, err
	}
	aead, err := gcm(key)
	if err != nil {
		return nil, err
	}
	hj, _ := json.Marshal(h)
	payload := binary.BigEndian.AppendUint32(nil, uint32(len(hj)))
	payload = append(append(payload, hj...), content...)
	nonce := make([]byte, nonceLen)
	rand.Read(nonce)
	out := append(append([]byte{}, head...), nonce...)
	return aead.Seal(out, nonce, payload, head), nil
}

// NeedsPassword reports whether blob is password-protected.
func NeedsPassword(blob []byte) bool {
	return len(blob) > 1 && blob[0] == Version && blob[1]&flagPassword != 0
}

// Check reports whether blob is structurally a version-1 item (it cannot
// tell whether the ciphertext is genuine without the key).
func Check(blob []byte) error {
	_, _, _, err := split(blob)
	return err
}

func split(blob []byte) (head, nonce, sealed []byte, err error) {
	if len(blob) < 2 || blob[0] != Version {
		return nil, nil, nil, ErrFormat
	}
	n := 2
	if blob[1]&flagPassword != 0 {
		n += saltLen + 4
	}
	if blob[1]&^flagPassword != 0 || len(blob) < n+nonceLen+16 {
		return nil, nil, nil, ErrFormat
	}
	return blob[:n], blob[n : n+nonceLen], blob[n+nonceLen:], nil
}

// Decrypt opens blob with key k (and the password if it has one).
func Decrypt(blob, k []byte, password string) (Header, []byte, error) {
	var h Header
	head, nonce, sealed, err := split(blob)
	if err != nil {
		return h, nil, err
	}
	var salt []byte
	iter := 0
	if head[1]&flagPassword != 0 {
		if password == "" {
			return h, nil, ErrPassword
		}
		salt = head[2 : 2+saltLen]
		iter = int(binary.BigEndian.Uint32(head[2+saltLen:]))
		if iter < 1 || iter > 10_000_000 {
			return h, nil, ErrFormat
		}
	}
	key, err := deriveKey(k, password, salt, iter)
	if err != nil {
		return h, nil, err
	}
	aead, err := gcm(key)
	if err != nil {
		return h, nil, err
	}
	payload, err := aead.Open(nil, nonce, sealed, head)
	if err != nil {
		return h, nil, ErrKey
	}
	if len(payload) < 4 {
		return h, nil, ErrFormat
	}
	hl := int(binary.BigEndian.Uint32(payload))
	if hl > len(payload)-4 {
		return h, nil, ErrFormat
	}
	if err := json.Unmarshal(payload[4:4+hl], &h); err != nil {
		return h, nil, fmt.Errorf("%w: bad header", ErrFormat)
	}
	return h, payload[4+hl:], nil
}
