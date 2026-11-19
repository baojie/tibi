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
	Filename      string
	Ext           string
	Size          int64
	SHA256        string
	Title         string
	Lang          string
	Charset       string
	Width         *int
	Height        *int
	Visibility    string
	Slug          string
	BurnAfterRead bool
	Source        string
	Meta          string
	TokenID       int64
	ViewCount     int64
	CreatedAt     int64
	ExpiresAt     *int64
	DeletedAt     *int64

	seq int64 // SQLite rowid: insertion order, breaks created_at ties in lists
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
			if it.Slug != "" {
				if _, err := tx.ExecContext(ctx, `UPDATE items SET slug = NULL WHERE slug = ?`, it.Slug); err != nil {
					return err
				}
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO items
				(id, kind, mime, filename, ext, size, sha256, title, lang, charset, width, height, visibility,
				 slug, burn_after_read, source, meta, token_id, created_at, expires_at)
				VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				it.ID, it.Kind, it.Mime, nullStr(it.Filename), it.Ext, it.Size, it.SHA256, nullStr(it.Title),
				nullStr(it.Lang), nullStr(it.Charset), nullInt(it.Width), nullInt(it.Height), it.Visibility,
				nullStr(it.Slug), it.BurnAfterRead, nullStr(it.Source), nullStr(it.Meta), it.TokenID,
				it.CreatedAt, nullInt64(it.ExpiresAt))
			return err
		})
		if err != nil && strings.Contains(err.Error(), "UNIQUE") && strings.Contains(err.Error(), "items.id") && attempt < 5 {
			continue
		}
		return err
	}
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

const itemCols = `id, kind, mime, filename, ext, size, sha256, title, lang, charset, width, height,
	visibility, slug, burn_after_read, source, meta, token_id, view_count, created_at, expires_at, deleted_at`

func scanItem(sc interface{ Scan(...any) error }) (*Item, error) {
	var it Item
	var filename, title, lang, charset, slug, source, meta sql.NullString
	var width, height, exp, del sql.NullInt64
	err := sc.Scan(&it.ID, &it.Kind, &it.Mime, &filename, &it.Ext, &it.Size, &it.SHA256, &title,
		&lang, &charset, &width, &height, &it.Visibility, &slug, &it.BurnAfterRead, &source, &meta,
		&it.TokenID, &it.ViewCount, &it.CreatedAt, &exp, &del)
	if err != nil {
		return nil, err
	}
	it.Filename, it.Title, it.Lang, it.Charset = filename.String, title.String, lang.String, charset.String
	it.Slug, it.Source, it.Meta = slug.String, source.String, meta.String
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
	Kind   string
	Source string
	Limit  int
	Cursor string // opaque, from a previous page
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
