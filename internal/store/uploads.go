package store

import (
	"context"
	"database/sql"
	"errors"
)

// Upload is a resumable upload session.
type Upload struct {
	ID        string
	TokenID   int64
	Options   string // JSON
	Size      int64
	CreatedAt int64
	ItemID    string
	Error     string
}

// CreateUpload records a new resumable upload.
func (s *Store) CreateUpload(ctx context.Context, u *Upload) error {
	u.CreatedAt = s.Now().Unix()
	_, err := s.db.ExecContext(ctx, `INSERT INTO uploads (id, token_id, options, size, created_at) VALUES (?,?,?,?,?)`,
		u.ID, u.TokenID, u.Options, u.Size, u.CreatedAt)
	return err
}

// GetUpload returns an upload session.
func (s *Store) GetUpload(ctx context.Context, id string) (*Upload, error) {
	var u Upload
	var item, errText sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id, token_id, options, size, created_at, item_id, error FROM uploads WHERE id = ?`, id).
		Scan(&u.ID, &u.TokenID, &u.Options, &u.Size, &u.CreatedAt, &item, &errText)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.ItemID, u.Error = item.String, errText.String
	return &u, nil
}

// FinishUpload records the outcome of a finished upload.
func (s *Store) FinishUpload(ctx context.Context, id, itemID, errText string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE uploads SET item_id = ?, error = ? WHERE id = ?`, nullStr(itemID), nullStr(errText), id)
	return err
}

// StaleUploads lists uploads created at or before cutoff (unix seconds).
func (s *Store) StaleUploads(ctx context.Context, cutoff int64) ([]*Upload, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, item_id FROM uploads WHERE created_at <= ?`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Upload
	for rows.Next() {
		var u Upload
		var item sql.NullString
		if err := rows.Scan(&u.ID, &item); err != nil {
			return nil, err
		}
		u.ItemID = item.String
		out = append(out, &u)
	}
	return out, rows.Err()
}

// DeleteUpload forgets an upload session.
func (s *Store) DeleteUpload(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM uploads WHERE id = ?`, id)
	return err
}
