package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"tibi/internal/id"
)

// Item kinds.
const (
	KindImage     = "image"
	KindVideo     = "video"
	KindAudio     = "audio"
	KindText      = "text"
	KindFile      = "file"
	KindEncrypted = "encrypted"
)

// Item is an uploaded item.
type Item struct {
	ID         string
	Kind       string
	Mime       string
	Filename   string
	Ext        string
	Size       int64
	SHA256     string
	Title      string
	Visibility string
	Source     string
	Meta       string
	TokenID    int64
	CreatedAt  int64
	ExpiresAt  *int64
	DeletedAt  *int64
}

// CreateItem inserts it, assigning a fresh ID (retrying on collision).
func (s *Store) CreateItem(ctx context.Context, it *Item) error {
	if it.Visibility == "" {
		it.Visibility = "unlisted"
	}
	it.CreatedAt = s.Now().Unix()
	var exp sql.NullInt64
	if it.ExpiresAt != nil {
		exp = sql.NullInt64{Int64: *it.ExpiresAt, Valid: true}
	}
	for attempt := 0; ; attempt++ {
		it.ID = id.New()
		_, err := s.db.ExecContext(ctx, `INSERT INTO items
			(id, kind, mime, filename, ext, size, sha256, title, visibility, source, meta, token_id, created_at, expires_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			it.ID, it.Kind, it.Mime, nullStr(it.Filename), it.Ext, it.Size, it.SHA256, nullStr(it.Title),
			it.Visibility, nullStr(it.Source), nullStr(it.Meta), it.TokenID, it.CreatedAt, exp)
		if err != nil && strings.Contains(err.Error(), "UNIQUE") && strings.Contains(err.Error(), "items.id") && attempt < 5 {
			continue
		}
		return err
	}
}

// GetItem returns a live item: not deleted and not expired.
func (s *Store) GetItem(ctx context.Context, itemID string) (*Item, error) {
	var it Item
	var filename, title, source, meta sql.NullString
	var exp, del sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT id, kind, mime, filename, ext, size, sha256, title, visibility, source, meta, token_id, created_at, expires_at, deleted_at
		FROM items WHERE id = ?`, itemID).Scan(&it.ID, &it.Kind, &it.Mime, &filename, &it.Ext, &it.Size, &it.SHA256, &title,
		&it.Visibility, &source, &meta, &it.TokenID, &it.CreatedAt, &exp, &del)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	it.Filename, it.Title, it.Source, it.Meta = filename.String, title.String, source.String, meta.String
	if exp.Valid {
		it.ExpiresAt = &exp.Int64
	}
	if del.Valid {
		it.DeletedAt = &del.Int64
	}
	if it.DeletedAt != nil || (it.ExpiresAt != nil && *it.ExpiresAt <= s.Now().Unix()) {
		return nil, ErrNotFound
	}
	return &it, nil
}
