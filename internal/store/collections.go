package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"tibi/internal/id"
)

// Collection is an album or multi-file paste: an ordered set of items.
type Collection struct {
	ID         string
	Title      string
	Visibility string
	TokenID    int64
	CreatedAt  int64
	ExpiresAt  *int64
	DeletedAt  *int64
	ItemCount  int // live items

	seq int64
}

// Live reports whether the collection is neither deleted nor expired.
func (c *Collection) Live(now int64) bool {
	return c.DeletedAt == nil && (c.ExpiresAt == nil || *c.ExpiresAt > now)
}

// CreateCollection inserts c with a fresh ID from the shared namespace.
func (s *Store) CreateCollection(ctx context.Context, c *Collection) error {
	if c.Visibility == "" {
		c.Visibility = Unlisted
	}
	c.CreatedAt = s.Now().Unix()
	for attempt := 0; ; attempt++ {
		c.ID = id.New()
		err := s.tx(ctx, func(tx *sql.Tx) error {
			if taken, err := idTaken(ctx, tx, c.ID); err != nil || taken {
				if err == nil {
					err = errIDTaken
				}
				return err
			}
			res, err := tx.ExecContext(ctx, `INSERT INTO collections (id, title, visibility, token_id, created_at, expires_at)
				VALUES (?,?,?,?,?,?)`, c.ID, nullStr(c.Title), c.Visibility, c.TokenID, c.CreatedAt, nullInt64(c.ExpiresAt))
			if err != nil {
				return err
			}
			c.seq, err = res.LastInsertId()
			return err
		})
		if errors.Is(err, errIDTaken) && attempt < 5 {
			continue
		}
		return err
	}
}

const collCols = `c.id, c.title, c.visibility, c.token_id, c.created_at, c.expires_at, c.deleted_at,
	(SELECT COUNT(*) FROM items i WHERE i.collection_id = c.id AND i.deleted_at IS NULL AND (i.expires_at IS NULL OR i.expires_at > ?))`

func scanCollection(sc interface{ Scan(...any) error }, extra ...any) (*Collection, error) {
	var c Collection
	var title sql.NullString
	var exp, del sql.NullInt64
	dest := append([]any{&c.ID, &title, &c.Visibility, &c.TokenID, &c.CreatedAt, &exp, &del, &c.ItemCount}, extra...)
	if err := sc.Scan(dest...); err != nil {
		return nil, err
	}
	c.Title = title.String
	if exp.Valid {
		c.ExpiresAt = &exp.Int64
	}
	if del.Valid {
		c.DeletedAt = &del.Int64
	}
	return &c, nil
}

// GetCollection returns a live collection.
func (s *Store) GetCollection(ctx context.Context, cid string) (*Collection, error) {
	now := s.Now().Unix()
	c, err := scanCollection(s.db.QueryRowContext(ctx, `SELECT `+collCols+` FROM collections c WHERE c.id = ?`, now, cid))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !c.Live(now) {
		return nil, ErrNotFound
	}
	return c, nil
}

// CollectionItems returns the live items of a collection in order.
func (s *Store) CollectionItems(ctx context.Context, cid string) ([]*Item, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+itemCols+` FROM items
		WHERE collection_id = ? AND deleted_at IS NULL AND (expires_at IS NULL OR expires_at > ?)
		ORDER BY position, rowid`, cid, s.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// NextPosition is the position for an item appended to the collection.
func (s *Store) NextPosition(ctx context.Context, cid string) (int, error) {
	var n sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT MAX(position) FROM items WHERE collection_id = ?`, cid).Scan(&n)
	if !n.Valid {
		return 0, err
	}
	return int(n.Int64) + 1, err
}

// ListCollections returns live collections, newest first.
func (s *Store) ListCollections(ctx context.Context, limit int, cursor string, priv PrivateAccess) ([]*Collection, string, error) {
	if limit <= 0 {
		limit = 20
	}
	now := s.Now().Unix()
	q := `SELECT ` + collCols + `, c.rowid FROM collections c WHERE c.deleted_at IS NULL AND (c.expires_at IS NULL OR c.expires_at > ?)`
	args := []any{now, now}
	if c, a := priv.clause("c."); c != "" {
		q += c
		args = append(args, a...)
	}
	if cursor != "" {
		ts, seq, err := decodeCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		q += ` AND (c.created_at < ? OR (c.created_at = ? AND c.rowid < ?))`
		args = append(args, ts, ts, seq)
	}
	q += ` ORDER BY c.created_at DESC, c.rowid DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []*Collection
	for rows.Next() {
		var seq int64
		c, err := scanCollection(rows, &seq)
		if err != nil {
			return nil, "", err
		}
		c.seq = seq
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		last := out[len(out)-1]
		next = encodeCursor(&Item{CreatedAt: last.CreatedAt, seq: last.seq})
	}
	return out, next, nil
}

// CollectionPatch holds optional collection updates.
type CollectionPatch struct {
	Title      *string
	Visibility *string
	ExpiresAt  **int64
	Order      []string // the collection's live item IDs in their new order
}

// ErrBadOrder is returned when Order is not a permutation of the members.
var ErrBadOrder = errors.New("items must list every item of the collection exactly once")

// UpdateCollection applies p to a live collection.
func (s *Store) UpdateCollection(ctx context.Context, cid string, p CollectionPatch) (*Collection, error) {
	if p.Visibility != nil && *p.Visibility != Unlisted && *p.Visibility != Private {
		return nil, fmt.Errorf("visibility must be %q or %q", Unlisted, Private)
	}
	if _, err := s.GetCollection(ctx, cid); err != nil {
		return nil, err
	}
	if p.Order != nil {
		members, err := s.CollectionItems(ctx, cid)
		if err != nil {
			return nil, err
		}
		have := make([]string, len(members))
		for i, m := range members {
			have[i] = m.ID
		}
		want := append([]string(nil), p.Order...)
		sort.Strings(have)
		sort.Strings(want)
		if strings.Join(have, ",") != strings.Join(want, ",") {
			return nil, ErrBadOrder
		}
	}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		set := func(col string, v any) error {
			_, err := tx.ExecContext(ctx, `UPDATE collections SET `+col+` = ? WHERE id = ?`, v, cid)
			return err
		}
		if p.Title != nil {
			if err := set("title", nullStr(*p.Title)); err != nil {
				return err
			}
		}
		if p.Visibility != nil {
			if err := set("visibility", *p.Visibility); err != nil {
				return err
			}
		}
		if p.ExpiresAt != nil {
			if err := set("expires_at", nullInt64(*p.ExpiresAt)); err != nil {
				return err
			}
		}
		for i, itemID := range p.Order {
			if _, err := tx.ExecContext(ctx, `UPDATE items SET position = ? WHERE id = ? AND collection_id = ?`, i, itemID, cid); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetCollection(ctx, cid)
}

// DeleteCollection soft-deletes a live collection and all of its items.
func (s *Store) DeleteCollection(ctx context.Context, cid string) error {
	now := s.Now().Unix()
	return s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE collections SET deleted_at = ?
			WHERE id = ? AND deleted_at IS NULL AND (expires_at IS NULL OR expires_at > ?)`, now, cid, now)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		_, err = tx.ExecContext(ctx, `UPDATE items SET deleted_at = ?, slug = NULL
			WHERE collection_id = ? AND deleted_at IS NULL`, now, cid)
		return err
	})
}

// SetCollection puts a live item into a collection at the given position.
func (s *Store) SetCollection(ctx context.Context, itemID, cid string, pos int) error {
	_, err := s.db.ExecContext(ctx, `UPDATE items SET collection_id = ?, position = ? WHERE id = ?`, cid, pos, itemID)
	return err
}
