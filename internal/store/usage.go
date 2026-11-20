package store

import (
	"context"
	"database/sql"
)

// UsageBatch is accumulated traffic to write in one transaction.
type UsageBatch struct {
	Day   string           // YYYY-MM-DD (UTC) the bytes belong to
	Bytes map[string]int64 // item ID → bytes served
	Views map[string]int64 // item ID → hits
	Total int64            // all bytes served
}

// AddUsage adds a batch to items.view_count/bytes_served and daily_usage.
func (s *Store) AddUsage(ctx context.Context, b UsageBatch) error {
	if len(b.Bytes) == 0 && len(b.Views) == 0 && b.Total == 0 {
		return nil
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		ids := map[string]bool{}
		for id := range b.Bytes {
			ids[id] = true
		}
		for id := range b.Views {
			ids[id] = true
		}
		for id := range ids {
			if _, err := tx.ExecContext(ctx, `UPDATE items SET view_count = view_count + ?, bytes_served = bytes_served + ? WHERE id = ?`,
				b.Views[id], b.Bytes[id], id); err != nil {
				return err
			}
			if n := b.Bytes[id]; n > 0 {
				if err := addDaily(ctx, tx, b.Day, id, n); err != nil {
					return err
				}
			}
		}
		if b.Total > 0 {
			return addDaily(ctx, tx, b.Day, "", b.Total)
		}
		return nil
	})
}

func addDaily(ctx context.Context, tx *sql.Tx, day, id string, n int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO daily_usage(day, item_id, bytes) VALUES (?,?,?)
		ON CONFLICT(day, item_id) DO UPDATE SET bytes = bytes + excluded.bytes`, day, id, n)
	return err
}

// DailyUsage returns the bytes served on day per item and in total.
func (s *Store) DailyUsage(ctx context.Context, day string) (map[string]int64, int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT item_id, bytes FROM daily_usage WHERE day = ?`, day)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	per := map[string]int64{}
	var total int64
	for rows.Next() {
		var id string
		var n int64
		if err := rows.Scan(&id, &n); err != nil {
			return nil, 0, err
		}
		if id == "" {
			total = n
		} else {
			per[id] = n
		}
	}
	return per, total, rows.Err()
}

// PruneUsage drops daily rows older than keepFrom (YYYY-MM-DD).
func (s *Store) PruneUsage(ctx context.Context, keepFrom string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM daily_usage WHERE day < ?`, keepFrom)
	return err
}

// TokenUsage is the total size of the live items a token uploaded (for
// quotas; identical content counts each time, as the uploader sees it).
func (s *Store) TokenUsage(ctx context.Context, tokenID int64) (int64, error) {
	var n sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT SUM(size) FROM items WHERE token_id = ? AND deleted_at IS NULL
		AND (expires_at IS NULL OR expires_at > ?)`, tokenID, s.Now().Unix()).Scan(&n)
	return n.Int64, err
}

// PendingUploadBytes is the declared size of a token's unfinished resumable
// uploads (except one), which a quota must count before they complete.
func (s *Store) PendingUploadBytes(ctx context.Context, tokenID int64, except string) (int64, error) {
	var n sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT SUM(size) FROM uploads WHERE token_id = ? AND id != ?
		AND (item_id IS NULL OR item_id = '') AND (error IS NULL OR error = '')`, tokenID, except).Scan(&n)
	return n.Int64, err
}

// Stats summarizes the instance.
type Stats struct {
	Items        int
	ItemsByKind  map[string]int
	LogicalBytes int64 // sum of live item sizes
	StorageBytes int64 // distinct blobs referenced by any row
	BytesServed  int64 // all time, as flushed
	Collections  int
	Tokens       int // active
	Top          []*Item
}

// Stats computes instance statistics; top lists the most-served live items.
func (s *Store) Stats(ctx context.Context, top int) (*Stats, error) {
	now := s.Now().Unix()
	st := &Stats{ItemsByKind: map[string]int{}}
	rows, err := s.db.QueryContext(ctx, `SELECT kind, COUNT(*), COALESCE(SUM(size),0) FROM items
		WHERE deleted_at IS NULL AND (expires_at IS NULL OR expires_at > ?) GROUP BY kind`, now)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k string
		var n int
		var b int64
		if err := rows.Scan(&k, &n, &b); err != nil {
			rows.Close()
			return nil, err
		}
		st.ItemsByKind[k], st.Items, st.LogicalBytes = n, st.Items+n, st.LogicalBytes+b
	}
	rows.Close()
	q := func(dst any, query string, args ...any) error {
		return s.db.QueryRowContext(ctx, query, args...).Scan(dst)
	}
	if err := q(&st.StorageBytes, `SELECT COALESCE(SUM(size),0) FROM (SELECT sha256, MAX(size) AS size FROM items GROUP BY sha256)`); err != nil {
		return nil, err
	}
	if err := q(&st.BytesServed, `SELECT COALESCE(SUM(bytes_served),0) FROM items`); err != nil {
		return nil, err
	}
	if err := q(&st.Collections, `SELECT COUNT(*) FROM collections WHERE deleted_at IS NULL AND (expires_at IS NULL OR expires_at > ?)`, now); err != nil {
		return nil, err
	}
	if err := q(&st.Tokens, `SELECT COUNT(*) FROM tokens WHERE revoked_at IS NULL`); err != nil {
		return nil, err
	}
	trows, err := s.db.QueryContext(ctx, `SELECT `+itemCols+` FROM items WHERE deleted_at IS NULL
		AND (expires_at IS NULL OR expires_at > ?) AND (bytes_served > 0 OR view_count > 0)
		ORDER BY bytes_served DESC, view_count DESC LIMIT ?`, now, top)
	if err != nil {
		return nil, err
	}
	defer trows.Close()
	for trows.Next() {
		it, err := scanItem(trows)
		if err != nil {
			return nil, err
		}
		st.Top = append(st.Top, it)
	}
	return st, trows.Err()
}

// BytesServed returns an item's all-time bytes served (as flushed).
func (s *Store) BytesServed(ctx context.Context, itemID string) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT bytes_served FROM items WHERE id = ?`, itemID).Scan(&n)
	return n, err
}
