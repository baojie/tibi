package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/baojie/tibi/internal/id"
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

// Visibilities.
const (
	Unlisted = "unlisted"
	Private  = "private"
)

// ErrSlugInvalid is returned for slugs that are malformed or reserved.
var ErrSlugInvalid = errors.New("slug must be 3–64 characters of a-z, 0-9 and '-', and not a reserved word")

// Item is an uploaded item.
type Item struct {
	ID            string
	Kind          string
	Mime          string
	OrigMime      string // set when converted (e.g. HEIC → JPEG)
	Filename      string
	Ext           string
	Size          int64
	SHA256        string
	Title         string
	Lang          string
	Charset       string
	Width         *int
	Height        *int
	DurationMS    *int64
	Thumb         bool // a thumbnail blob exists
	Visibility    string
	Slug          string
	CollectionID  string
	Position      *int
	ForkedFrom    string
	BurnAfterRead bool
	Source        string
	Meta          string
	TokenID       int64
	ViewCount     int64
	BytesServed   int64
	CreatedAt     int64
	ExpiresAt     *int64
	DeletedAt     *int64

	seq int64 // SQLite rowid: insertion order, breaks created_at ties in lists

	// IndexBody is the start of a text item's content, indexed for search by
	// CreateItem (not stored in the items table).
	IndexBody string
}

// Live reports whether the item is neither deleted nor expired at unix time now.
func (it *Item) Live(now int64) bool {
	return it.DeletedAt == nil && (it.ExpiresAt == nil || *it.ExpiresAt > now)
}

var slugRe = regexp.MustCompile(`^[a-z0-9-]{3,64}$`)

var reservedSlugs = map[string]bool{
	"api": true, "s": true, "c": true, "static": true, "admin": true, "robots-txt": true,
	"healthz": true, "metrics": true, "login": true, "logout": true, "upload": true,
	"new": true, "edit": true, "raw": true, "dl": true, "www": true, "help": true,
	"about": true, "favicon": true, "share": true, "settings": true,
}

// ValidSlug reports whether s may be used as a slug.
func ValidSlug(s string) bool {
	return slugRe.MatchString(s) && !reservedSlugs[s] && !strings.HasPrefix(s, "-") && !strings.HasSuffix(s, "-")
}

// CreateItem inserts it, assigning a fresh ID (retrying on collision). If
// it.Slug is set, the slug is moved to this item (a slug is a pointer).
func (s *Store) CreateItem(ctx context.Context, it *Item) error {
	if it.Visibility == "" {
		it.Visibility = Unlisted
	}
	if it.Slug != "" && !ValidSlug(it.Slug) {
		return ErrSlugInvalid
	}
	it.CreatedAt = s.Now().Unix()
	for attempt := 0; ; attempt++ {
		it.ID = id.New()
		err := s.tx(ctx, func(tx *sql.Tx) error {
			if taken, err := idTaken(ctx, tx, it.ID); err != nil || taken {
				if err == nil {
					err = errIDTaken
				}
				return err
			}
			if it.Slug != "" {
				if _, err := tx.ExecContext(ctx, `UPDATE items SET slug = NULL WHERE slug = ?`, it.Slug); err != nil {
					return err
				}
			}
			res, err := tx.ExecContext(ctx, `INSERT INTO items
				(id, kind, mime, orig_mime, filename, ext, size, sha256, title, lang, charset, width, height,
				 duration_ms, thumb, visibility, slug, burn_after_read, source, meta, token_id, created_at, expires_at,
				 collection_id, position, forked_from)
				VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				it.ID, it.Kind, it.Mime, nullStr(it.OrigMime), nullStr(it.Filename), it.Ext, it.Size, it.SHA256, nullStr(it.Title),
				nullStr(it.Lang), nullStr(it.Charset), nullInt(it.Width), nullInt(it.Height),
				nullInt64(it.DurationMS), it.Thumb, it.Visibility,
				nullStr(it.Slug), it.BurnAfterRead, nullStr(it.Source), nullStr(it.Meta), it.TokenID,
				it.CreatedAt, nullInt64(it.ExpiresAt), nullStr(it.CollectionID), nullInt(it.Position), nullStr(it.ForkedFrom))
			if err != nil {
				return err
			}
			rowid, err := res.LastInsertId()
			if err != nil {
				return err
			}
			it.seq = rowid
			body := it.IndexBody
			if it.Kind == KindEncrypted || it.BurnAfterRead {
				// Never index ciphertext, nor burn-after-read content:
				// search must not read it without consuming it.
				body = ""
			}
			return indexItem(ctx, tx, rowid, it.Title, it.Filename, body)
		})
		if err != nil && (errors.Is(err, errIDTaken) || strings.Contains(err.Error(), "UNIQUE") && strings.Contains(err.Error(), "items.id")) && attempt < 5 {
			continue
		}
		return err
	}
}

var errIDTaken = errors.New("id taken")

// idTaken reports whether an item or a collection already uses id: both
// live in one namespace, so no ID is ever both.
func idTaken(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM items WHERE id = ?) + (SELECT COUNT(*) FROM collections WHERE id = ?)`, id, id).Scan(&n)
	return n > 0, err
}

func (s *Store) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

const itemCols = `id, kind, mime, orig_mime, filename, ext, size, sha256, title, lang, charset, width, height,
	duration_ms, thumb, visibility, slug, burn_after_read, source, meta, token_id, view_count, created_at, expires_at, deleted_at,
	collection_id, position, forked_from, bytes_served`

func scanItem(sc interface{ Scan(...any) error }) (*Item, error) {
	var it Item
	var origMime, filename, title, lang, charset, slug, source, meta, coll, forked sql.NullString
	var width, height, dur, exp, del, pos sql.NullInt64
	err := sc.Scan(&it.ID, &it.Kind, &it.Mime, &origMime, &filename, &it.Ext, &it.Size, &it.SHA256, &title,
		&lang, &charset, &width, &height, &dur, &it.Thumb, &it.Visibility, &slug, &it.BurnAfterRead, &source, &meta,
		&it.TokenID, &it.ViewCount, &it.CreatedAt, &exp, &del, &coll, &pos, &forked, &it.BytesServed)
	if err != nil {
		return nil, err
	}
	it.Filename, it.Title, it.Lang, it.Charset = filename.String, title.String, lang.String, charset.String
	it.Slug, it.Source, it.Meta = slug.String, source.String, meta.String
	it.OrigMime = origMime.String
	it.CollectionID, it.ForkedFrom = coll.String, forked.String
	if pos.Valid {
		p := int(pos.Int64)
		it.Position = &p
	}
	if dur.Valid {
		it.DurationMS = &dur.Int64
	}
	if width.Valid && height.Valid {
		w, h := int(width.Int64), int(height.Int64)
		it.Width, it.Height = &w, &h
	}
	if exp.Valid {
		it.ExpiresAt = &exp.Int64
	}
	if del.Valid {
		it.DeletedAt = &del.Int64
	}
	return &it, nil
}

// GetItem returns a live item: not deleted and not expired.
func (s *Store) GetItem(ctx context.Context, itemID string) (*Item, error) {
	it, err := scanItem(s.db.QueryRowContext(ctx, `SELECT `+itemCols+` FROM items WHERE id = ?`, itemID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !it.Live(s.Now().Unix()) {
		return nil, ErrNotFound
	}
	return it, nil
}

// ResolveSlug returns the live item a slug points to.
func (s *Store) ResolveSlug(ctx context.Context, slug string) (*Item, error) {
	var itemID string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM items WHERE slug = ?`, slug).Scan(&itemID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.GetItem(ctx, itemID)
}

// ListFilter selects items for ListItems.
type ListFilter struct {
	Kind       string
	Source     string
	Collection string
	Limit      int
	Cursor     string // opaque, from a previous page
	Private    PrivateAccess
}

// PrivateAccess says which private items a listing may include: all of
// them (a token with read_private), or only those uploaded by OwnToken.
type PrivateAccess struct {
	All      bool
	OwnToken int64
}

// clause returns an SQL condition (with its argument) limiting private
// rows; col is the column prefix ("" or "c."). Used for collections.
func (p PrivateAccess) clause(col string) (string, []any) {
	if p.All {
		return "", nil
	}
	return " AND (" + col + "visibility != 'private' OR " + col + "token_id = ?)", []any{p.OwnToken}
}

// itemClause is clause for items: burn-after-read items are limited like
// private ones, since knowing one's ID is enough to consume it.
func (p PrivateAccess) itemClause(col string) (string, []any) {
	if p.All {
		return "", nil
	}
	return " AND ((" + col + "visibility != 'private' AND " + col + "burn_after_read = 0) OR " + col + "token_id = ?)", []any{p.OwnToken}
}

// ErrBadCursor is returned for malformed cursors.
var ErrBadCursor = errors.New("invalid cursor")

func encodeCursor(it *Item) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(it.CreatedAt, 10) + ":" + strconv.FormatInt(it.seq, 10)))
}

func decodeCursor(c string) (int64, int64, error) {
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return 0, 0, ErrBadCursor
	}
	ts, seq, ok := strings.Cut(string(b), ":")
	n, err1 := strconv.ParseInt(ts, 10, 64)
	m, err2 := strconv.ParseInt(seq, 10, 64)
	if !ok || err1 != nil || err2 != nil {
		return 0, 0, ErrBadCursor
	}
	return n, m, nil
}

// ListItems returns live items, newest first, and the cursor of the next
// page ("" when there is none).
// SetThumb records that the image with this hash now has a thumbnail.
func (s *Store) SetThumb(ctx context.Context, sha256hex string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE items SET thumb = 1 WHERE sha256 = ? AND thumb = 0`, sha256hex)
	return err
}

// Excerpts returns the start of the indexed text of the given text items,
// at most n characters each. Burn-after-read items are not indexed and so
// get none.
func (s *Store) Excerpts(ctx context.Context, ids []string, n int) (map[string]string, error) {
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	q := `SELECT i.id, substr(x.body, 1, ?) FROM items i JOIN items_fts x ON x.rowid = i.rowid
		WHERE i.kind = 'text' AND i.burn_after_read = 0 AND i.id IN (?` + strings.Repeat(",?", len(ids)-1) + `)`
	args := []any{n}
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var body sql.NullString
		if err := rows.Scan(&id, &body); err != nil {
			return nil, err
		}
		if body.String != "" {
			out[id] = body.String
		}
	}
	return out, rows.Err()
}

func (s *Store) ListItems(ctx context.Context, f ListFilter) ([]*Item, string, error) {
	if f.Limit <= 0 {
		f.Limit = 20
	}
	q := `SELECT rowid, ` + itemCols + ` FROM items WHERE deleted_at IS NULL AND (expires_at IS NULL OR expires_at > ?)`
	args := []any{s.Now().Unix()}
	if f.Kind != "" {
		q += ` AND kind = ?`
		args = append(args, f.Kind)
	}
	if f.Source != "" {
		q += ` AND source = ?`
		args = append(args, f.Source)
	}
	if f.Collection != "" {
		q += ` AND collection_id = ?`
		args = append(args, f.Collection)
	}
	if c, a := f.Private.itemClause(""); c != "" {
		q += c
		args = append(args, a...)
	}
	if f.Cursor != "" {
		ts, seq, err := decodeCursor(f.Cursor)
		if err != nil {
			return nil, "", err
		}
		q += ` AND (created_at < ? OR (created_at = ? AND rowid < ?))`
		args = append(args, ts, ts, seq)
	}
	q += ` ORDER BY created_at DESC, rowid DESC LIMIT ?`
	args = append(args, f.Limit+1)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []*Item
	for rows.Next() {
		var seq int64
		it, err := scanItem(rowidScanner{rows, &seq})
		if err != nil {
			return nil, "", err
		}
		it.seq = seq
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > f.Limit {
		out = out[:f.Limit]
		next = encodeCursor(out[len(out)-1])
	}
	return out, next, nil
}

// rowidScanner scans a leading rowid column before the item columns.
type rowidScanner struct {
	rows *sql.Rows
	seq  *int64
}

func (r rowidScanner) Scan(dest ...any) error {
	return r.rows.Scan(append([]any{r.seq}, dest...)...)
}

// Patch holds optional item updates; nil fields are left unchanged.
// Content is immutable and cannot be patched.
type Patch struct {
	Title      *string
	Lang       *string
	Visibility *string
	ExpiresAt  **int64 // points to nil for "never"
	Slug       *string // "" clears the slug
}

// UpdateItem applies p to a live item and returns the updated item.
func (s *Store) UpdateItem(ctx context.Context, itemID string, p Patch) (*Item, error) {
	if p.Slug != nil && *p.Slug != "" && !ValidSlug(*p.Slug) {
		return nil, ErrSlugInvalid
	}
	if p.Visibility != nil && *p.Visibility != Unlisted && *p.Visibility != Private {
		return nil, fmt.Errorf("visibility must be %q or %q", Unlisted, Private)
	}
	if _, err := s.GetItem(ctx, itemID); err != nil {
		return nil, err
	}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		set := func(col string, v any) error {
			_, err := tx.ExecContext(ctx, `UPDATE items SET `+col+` = ? WHERE id = ?`, v, itemID)
			return err
		}
		if p.Title != nil {
			if err := set("title", nullStr(*p.Title)); err != nil {
				return err
			}
			// Re-index with the new title, keeping the indexed body.
			var rowid int64
			var filename sql.NullString
			var body string
			if err := tx.QueryRowContext(ctx, `SELECT i.rowid, i.filename, COALESCE(x.body, '')
				FROM items i LEFT JOIN items_fts x ON x.rowid = i.rowid WHERE i.id = ?`, itemID).Scan(&rowid, &filename, &body); err != nil {
				return err
			}
			if err := indexItem(ctx, tx, rowid, *p.Title, filename.String, body); err != nil {
				return err
			}
		}
		if p.Lang != nil {
			if err := set("lang", nullStr(*p.Lang)); err != nil {
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
		if p.Slug != nil {
			if *p.Slug != "" {
				if _, err := tx.ExecContext(ctx, `UPDATE items SET slug = NULL WHERE slug = ? AND id != ?`, *p.Slug, itemID); err != nil {
					return err
				}
			}
			if err := set("slug", nullStr(*p.Slug)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetItem(ctx, itemID)
}

// DeleteItem soft-deletes a live item; its slug is released.
func (s *Store) DeleteItem(ctx context.Context, itemID string) error {
	now := s.Now().Unix()
	res, err := s.db.ExecContext(ctx, `UPDATE items SET deleted_at = ?, slug = NULL
		WHERE id = ? AND deleted_at IS NULL AND (expires_at IS NULL OR expires_at > ?)`, now, itemID, now)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Burn deletes a burn-after-read item. It returns ErrNotFound if another
// request burned it first, so exactly one reader ever sees it.
func (s *Store) Burn(ctx context.Context, itemID string) error {
	now := s.Now().Unix()
	res, err := s.db.ExecContext(ctx, `UPDATE items SET deleted_at = ?, slug = NULL, view_count = view_count + 1
		WHERE id = ? AND burn_after_read = 1 AND deleted_at IS NULL AND (expires_at IS NULL OR expires_at > ?)`,
		now, itemID, now)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func nullInt64(p *int64) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *p, Valid: true}
}
