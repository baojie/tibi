package store

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"

	"tibi/internal/id"
)

// Scopes.
const (
	ScopeUpload      = "upload"
	ScopeReadPrivate = "read_private"
	ScopeDelete      = "delete"
	ScopeAdmin       = "admin"
)

// AllScopes lists valid scopes.
var AllScopes = []string{ScopeUpload, ScopeReadPrivate, ScopeDelete, ScopeAdmin}

// TokenPrefix starts every token.
const TokenPrefix = "tibi_"

// Token is an API token row (never contains the secret).
type Token struct {
	ID         int64
	Name       string
	Scopes     []string
	QuotaBytes *int64
	CreatedAt  int64
	LastUsedAt *int64
	RevokedAt  *int64
}

// Has reports whether the token carries scope (admin implies all).
func (t *Token) Has(scope string) bool {
	return slices.Contains(t.Scopes, scope) || slices.Contains(t.Scopes, ScopeAdmin)
}

// HashToken returns the stored hash of a secret token.
func HashToken(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])
}

// ParseScopes validates a comma-separated scope list.
func ParseScopes(s string) ([]string, error) {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !slices.Contains(AllScopes, p) {
			return nil, fmt.Errorf("unknown scope %q (valid: %s)", p, strings.Join(AllScopes, ","))
		}
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("at least one scope is required")
	}
	return out, nil
}

// CreateToken creates a token and returns its secret (shown once).
func (s *Store) CreateToken(ctx context.Context, name string, scopes []string, quota *int64) (string, *Token, error) {
	if name == "" {
		return "", nil, errors.New("token name is required")
	}
	secret := TokenPrefix + id.Random(32)
	now := s.Now().Unix()
	var q sql.NullInt64
	if quota != nil {
		q = sql.NullInt64{Int64: *quota, Valid: true}
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO tokens(name, hash, scopes, quota_bytes, created_at) VALUES(?,?,?,?,?)`,
		name, HashToken(secret), strings.Join(scopes, ","), q, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return "", nil, fmt.Errorf("a token named %q already exists", name)
		}
		return "", nil, err
	}
	tid, _ := res.LastInsertId()
	return secret, &Token{ID: tid, Name: name, Scopes: scopes, QuotaBytes: quota, CreatedAt: now}, nil
}

const tokenCols = `id, name, hash, scopes, quota_bytes, created_at, last_used_at, revoked_at`

func scanToken(sc interface{ Scan(...any) error }) (*Token, string, error) {
	var t Token
	var hash, scopes string
	var quota, last, rev sql.NullInt64
	if err := sc.Scan(&t.ID, &t.Name, &hash, &scopes, &quota, &t.CreatedAt, &last, &rev); err != nil {
		return nil, "", err
	}
	t.Scopes = strings.Split(scopes, ",")
	if quota.Valid {
		t.QuotaBytes = &quota.Int64
	}
	if last.Valid {
		t.LastUsedAt = &last.Int64
	}
	if rev.Valid {
		t.RevokedAt = &rev.Int64
	}
	return &t, hash, nil
}

// Authenticate resolves a secret to an active (non-revoked) token.
func (s *Store) Authenticate(ctx context.Context, secret string) (*Token, error) {
	if !strings.HasPrefix(secret, TokenPrefix) {
		return nil, ErrNotFound
	}
	want := HashToken(secret)
	row := s.db.QueryRowContext(ctx, `SELECT `+tokenCols+` FROM tokens WHERE hash = ?`, want)
	t, hash, err := scanToken(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare([]byte(hash), []byte(want)) != 1 || t.RevokedAt != nil {
		return nil, ErrNotFound
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE tokens SET last_used_at = ? WHERE id = ?`, s.Now().Unix(), t.ID)
	return t, nil
}

// ListTokens returns all tokens, oldest first.
func (s *Store) ListTokens(ctx context.Context) ([]*Token, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+tokenCols+` FROM tokens ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Token
	for rows.Next() {
		t, _, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeToken revokes the token with the given name.
func (s *Store) RevokeToken(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE tokens SET revoked_at = ? WHERE name = ? AND revoked_at IS NULL`, s.Now().Unix(), name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no active token named %q", name)
	}
	return nil
}
