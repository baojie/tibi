package store

import (
	"context"
	"database/sql"
)

// DeletedRetention is how long soft-deleted items are kept before GC
// removes them for good.
const DeletedRetention = 24 * 60 * 60

// PurgeResult reports what Purge removed.
type PurgeResult struct {
	Items  int
	Hashes []string // sha256 of blobs that lost their last reference
}

// Purge hard-deletes items that have expired or were soft-deleted more than
// DeletedRetention seconds ago, and returns the hashes no row references any
// more. Callers delete those blobs while holding BlobMu, re-checking with
// BlobReferenced, since an upload may have reused the hash meanwhile.
func (s *Store) Purge(ctx context.Context) (PurgeResult, error) {
	now := s.Now().Unix()
	var res PurgeResult
	err := s.tx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT rowid, id, sha256 FROM items
			WHERE (expires_at IS NOT NULL AND expires_at <= ?) OR (deleted_at IS NOT NULL AND deleted_at <= ?)`,
			now, now-DeletedRetention)
		if err != nil {
			return err
		}
		var ids []string
		var rowids []int64
		hashes := map[string]bool{}
		for rows.Next() {
			var rowid int64
			var itemID, sha string
			if err := rows.Scan(&rowid, &itemID, &sha); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, itemID)
			rowids = append(rowids, rowid)
			hashes[sha] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for i, itemID := range ids {
			if _, err := tx.ExecContext(ctx, `DELETE FROM items WHERE id = ?`, itemID); err != nil {
				return err
			}
			if err := unindex(ctx, tx, rowids[i]); err != nil {
				return err
			}
		}
		res.Items = len(ids)
		for sha := range hashes {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM items WHERE sha256 = ?`, sha).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				res.Hashes = append(res.Hashes, sha)
			}
		}
		return nil
	})
	return res, err
}

// BlobReferenced reports whether any item row (live, expired or
// soft-deleted) still uses the blob.
func (s *Store) BlobReferenced(ctx context.Context, sha string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM items WHERE sha256 = ?`, sha).Scan(&n)
	return n > 0, err
}

// BlobHashes calls fn for every distinct blob referenced by any item row
// (live, expired or soft-deleted), with the MIME type of its stored bytes.
func (s *Store) BlobHashes(ctx context.Context, fn func(sha, mime string) error) error {
	rows, err := s.db.QueryContext(ctx, `SELECT sha256, MIN(mime) FROM items GROUP BY sha256 ORDER BY sha256`)
	if err != nil {
		return err
	}
	type pair struct{ sha, mime string }
	var all []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.sha, &p.mime); err != nil {
			rows.Close()
			return err
		}
		all = append(all, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, p := range all {
		if err := fn(p.sha, p.mime); err != nil {
			return err
		}
	}
	return nil
}
