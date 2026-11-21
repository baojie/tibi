package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// MaxIndexBytes is how much of a text item's content is indexed.
const MaxIndexBytes = 1 << 20

// isCJK reports whether r is a Han, Kana or Hangul character, which are
// written without spaces and need n-gram indexing.
func isCJK(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

// cjkGrams turns every run of CJK characters in s into overlapping bigrams,
// plus the run's last character alone (so a one-character query can match
// it as a prefix). "题壁诗 x 文" → "题壁 壁诗 诗 文".
func cjkGrams(s string) string {
	var b strings.Builder
	var run []rune
	flush := func() {
		switch len(run) {
		case 0:
		case 1:
			b.WriteString(string(run[0]) + " ")
		default:
			for i := 0; i+1 < len(run); i++ {
				b.WriteString(string(run[i:i+2]) + " ")
			}
			b.WriteString(string(run[len(run)-1]) + " ")
		}
		run = run[:0]
	}
	for _, r := range s {
		if isCJK(r) {
			run = append(run, r)
		} else {
			flush()
		}
	}
	flush()
	return b.String()
}

// indexItem writes the search rows for an item. body is the start of a text
// item's content ("" for other kinds); it is cut to MaxIndexBytes.
func indexItem(ctx context.Context, tx *sql.Tx, rowid int64, title, filename, body string) error {
	if len(body) > MaxIndexBytes {
		body = body[:MaxIndexBytes]
		for len(body) > 0 && !utf8.ValidString(body) { // don't split a rune
			body = body[:len(body)-1]
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM items_fts WHERE rowid = ?`, rowid); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM items_tri WHERE rowid = ?`, rowid); err != nil {
		return err
	}
	cjk := cjkGrams(title + " " + filename + " " + body)
	if _, err := tx.ExecContext(ctx, `INSERT INTO items_fts(rowid, title, filename, body, cjk) VALUES (?,?,?,?,?)`,
		rowid, title, filename, body, cjk); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO items_tri(rowid, title, filename, body) VALUES (?,?,?,?)`,
		rowid, title, filename, body)
	return err
}

func unindex(ctx context.Context, tx *sql.Tx, rowid int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM items_fts WHERE rowid = ?`, rowid); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM items_tri WHERE rowid = ?`, rowid)
	return err
}

// Index (re)builds the search rows of one item; used for backfilling.
func (s *Store) Index(ctx context.Context, itemID, body string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		var rowid int64
		var title, filename sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT rowid, title, filename FROM items WHERE id = ?`, itemID).Scan(&rowid, &title, &filename)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		return indexItem(ctx, tx, rowid, title.String, filename.String, body)
	})
}

// Unindexed returns live items that have no search rows yet (items stored
// before search existed).
func (s *Store) Unindexed(ctx context.Context) ([]*Item, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+itemCols+` FROM items
		WHERE deleted_at IS NULL AND rowid NOT IN (SELECT rowid FROM items_fts) ORDER BY rowid`)
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

// SearchFilter selects search results.
type SearchFilter struct {
	Query   string
	Kind    string
	Limit   int
	Cursor  string
	Private PrivateAccess
}

// ErrBadQuery is returned for empty or unusable queries.
var ErrBadQuery = errors.New("query must contain at least one word")

// maxTerms, maxTermRunes and SearchTimeout bound the work one query can
// cause: a long word becomes many trigrams, each matching everywhere in a
// large repetitive paste (a 451-character word over 10 MB took 15 s).
const (
	maxTerms      = 8
	maxTermRunes  = 64
	SearchTimeout = 5 * time.Second
)

// ErrTermTooLong and ErrSearchTimeout are returned for queries that would
// cost too much.
var (
	ErrTermTooLong   = fmt.Errorf("search words are limited to %d characters", maxTermRunes)
	ErrSearchTimeout = errors.New("search took too long; use more specific words")
)

// quoteFTS makes s a literal FTS5 string.
func quoteFTS(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// termClauses turns the query into SQL conditions on items.rowid, all of
// which must hold: every word of the query must occur.
//   - CJK runs: bigram phrase in items_fts.cjk (1 char: prefix match)
//   - other text ≥3 chars: substring via the trigram table (case-insensitive)
//   - shorter words: word-prefix match in items_fts
func termClauses(q string) ([]string, []any, error) {
	var clauses []string
	var args []any
	add := func(table, match string) {
		clauses = append(clauses, `i.rowid IN (SELECT rowid FROM `+table+` WHERE `+table+` MATCH ?)`)
		args = append(args, match)
	}
	for _, word := range strings.Fields(q) {
		// Split mixed words ("tibi题壁") into CJK and other parts.
		var parts []string
		var cur []rune
		curCJK := false
		for _, r := range word {
			if len(cur) > 0 && isCJK(r) != curCJK {
				parts = append(parts, string(cur))
				cur = cur[:0]
			}
			cur, curCJK = append(cur, r), isCJK(r)
		}
		if len(cur) > 0 {
			parts = append(parts, string(cur))
		}
		for _, p := range parts {
			rs := []rune(p)
			if len(rs) > maxTermRunes {
				return nil, nil, ErrTermTooLong
			}
			switch {
			case isCJK(rs[0]) && len(rs) == 1:
				add("items_fts", "cjk : "+quoteFTS(p)+" *")
			case isCJK(rs[0]):
				grams := make([]string, 0, len(rs)-1)
				for i := 0; i+1 < len(rs); i++ {
					grams = append(grams, string(rs[i:i+2]))
				}
				add("items_fts", "cjk : "+quoteFTS(strings.Join(grams, " ")))
			default:
				p = strings.TrimFunc(p, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '_' })
				if p == "" {
					continue
				}
				if utf8.RuneCountInString(p) >= 3 {
					add("items_tri", quoteFTS(p))
				} else {
					add("items_fts", "{title filename body} : "+quoteFTS(p)+" *")
				}
			}
			if len(clauses) > maxTerms {
				return nil, nil, errors.New("too many search terms")
			}
		}
	}
	if len(clauses) == 0 {
		return nil, nil, ErrBadQuery
	}
	return clauses, args, nil
}

// SearchHit is one result: the item and, for text, a line around the match.
type SearchHit struct {
	Item    *Item
	Snippet string
}

// Search returns live items matching every term, newest first.
func (s *Store) Search(ctx context.Context, f SearchFilter) ([]SearchHit, string, error) {
	clauses, args, err := termClauses(f.Query)
	if err != nil {
		return nil, "", err
	}
	if f.Limit <= 0 {
		f.Limit = 20
	}
	q := `SELECT i.rowid, ` + prefixed("i.", itemCols) + `, COALESCE(x.body, '')
		FROM items i LEFT JOIN items_fts x ON x.rowid = i.rowid
		WHERE i.deleted_at IS NULL AND (i.expires_at IS NULL OR i.expires_at > ?) AND i.kind != 'encrypted'`
	all := append([]any{s.Now().Unix()}, args...)
	for _, c := range clauses {
		q += " AND " + c
	}
	if f.Kind != "" {
		q += ` AND i.kind = ?`
		all = append(all, f.Kind)
	}
	if c, a := f.Private.itemClause("i."); c != "" {
		q += c
		all = append(all, a...)
	}
	if f.Cursor != "" {
		ts, seq, err := decodeCursor(f.Cursor)
		if err != nil {
			return nil, "", err
		}
		q += ` AND (i.created_at < ? OR (i.created_at = ? AND i.rowid < ?))`
		all = append(all, ts, ts, seq)
	}
	q += ` ORDER BY i.created_at DESC, i.rowid DESC LIMIT ?`
	all = append(all, f.Limit+1)
	ctx, cancel := context.WithTimeout(ctx, SearchTimeout) // interrupts SQLite
	defer cancel()
	rows, err := s.search.QueryContext(ctx, q, all...)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, "", ErrSearchTimeout
		}
		return nil, "", err
	}
	defer rows.Close()
	var hits []SearchHit
	for rows.Next() {
		var seq int64
		var body string
		it, err := scanItem(searchScanner{rows, &seq, &body})
		if err != nil {
			return nil, "", err
		}
		it.seq = seq
		hits = append(hits, SearchHit{Item: it, Snippet: snippet(body, f.Query)})
	}
	if err := rows.Err(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, "", ErrSearchTimeout
		}
		return nil, "", err
	}
	next := ""
	if len(hits) > f.Limit {
		hits = hits[:f.Limit]
		next = encodeCursor(hits[len(hits)-1].Item)
	}
	return hits, next, nil
}

type searchScanner struct {
	rows *sql.Rows
	seq  *int64
	body *string
}

func (r searchScanner) Scan(dest ...any) error {
	return r.rows.Scan(append(append([]any{r.seq}, dest...), r.body)...)
}

func prefixed(p, cols string) string {
	parts := strings.Split(cols, ",")
	for i, c := range parts {
		parts[i] = p + strings.TrimSpace(c)
	}
	return strings.Join(parts, ", ")
}

// snippet returns the (trimmed) line of body containing the first query
// word, shortened to about 160 characters around the match.
func snippet(body, query string) string {
	if body == "" {
		return ""
	}
	for _, w := range strings.Fields(query) {
		// Match on body itself: lower-casing can change byte lengths
		// (U+023A Ⱥ is 2 bytes, ⱥ is 3), so an index into a lowered copy
		// does not fit the original.
		i := indexFold(body, w)
		if i < 0 {
			continue
		}
		start := strings.LastIndexByte(body[:i], '\n') + 1
		end := strings.IndexByte(body[i:], '\n')
		if end < 0 {
			end = len(body)
		} else {
			end += i
		}
		line := []rune(strings.TrimSpace(body[start:end]))
		if len(line) <= 160 {
			return string(line)
		}
		pos := utf8.RuneCountInString(strings.TrimLeft(body[start:i], " \t"))
		from := max(0, pos-60)
		to := min(len(line), from+160)
		s := string(line[from:to])
		if from > 0 {
			s = "…" + s
		}
		if to < len(line) {
			s += "…"
		}
		return s
	}
	return ""
}

// snippetScan bounds how much of a body is searched for the snippet.
const snippetScan = 1 << 20

// indexFold is a case-insensitive strings.Index returning a byte offset
// into s. It lowers s rune by rune while remembering where each lowered
// byte came from — lower-casing can change byte lengths (Ⱥ is 2 bytes, ⱥ
// is 3) — and lets strings.Index do the search: linear in s and sub, where
// a scan comparing at every offset (or a case-folding regexp) multiplies
// the two, and a long query word over a large paste took seconds of CPU.
func indexFold(s, sub string) int {
	if sub == "" {
		return 0
	}
	if len(s) > snippetScan {
		s = s[:snippetScan]
	}
	var low strings.Builder
	low.Grow(len(s))
	from := make([]int32, 0, len(s))
	for i, r := range s {
		n := low.Len()
		low.WriteRune(unicode.ToLower(r))
		for range low.Len() - n {
			from = append(from, int32(i))
		}
	}
	j := strings.Index(low.String(), strings.ToLower(sub))
	if j < 0 {
		return -1
	}
	return int(from[j])
}
