// Package backup exports an instance to a .tar.zst archive and imports it
// into an empty one (PLAN A11), reproducing every URL byte for byte.
//
// Archive layout:
//
//	manifest.json             format, versions, counts
//	tokens.jsonl              one row per line (column → value)
//	collections.jsonl
//	items.jsonl               live items, in insertion order
//	blobs/ab/cd/<sha256>      content (as stored)
//	thumb/ab/cd/<sha256>-400.webp
//	secret.key                share-link signing key (if present)
//
// Rows are copied column by column, so archives stay importable after
// later schema additions (new columns take their defaults).
package backup

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	"tibi/internal/blob"
	"tibi/internal/ingest"
	"tibi/internal/store"
)

// Format identifies tibi archives.
const Format = "tibi-export"

// Manifest describes an archive.
type Manifest struct {
	Format        string `json:"format"`
	Version       int    `json:"version"`
	TibiVersion   string `json:"tibi_version"`
	SchemaVersion int    `json:"schema_version"`
	CreatedAt     int64  `json:"created_at"`
	Items         int    `json:"items"`
	Collections   int    `json:"collections"`
	Tokens        int    `json:"tokens"`
	Blobs         int    `json:"blobs"`
}

// Export writes the live content of an instance to w.
func Export(ctx context.Context, st *store.Store, bs blob.Store, secretKey string, tibiVersion string, w io.Writer) (Manifest, error) {
	zw, err := zstd.NewWriter(w)
	if err != nil {
		return Manifest{}, err
	}
	tw := tar.NewWriter(zw)
	now := st.Now().Unix()
	m := Manifest{Format: Format, Version: 1, TibiVersion: tibiVersion, CreatedAt: now}
	m.SchemaVersion, _ = st.SchemaVersion(ctx)

	db := st.DB()
	tables := []struct {
		name  string
		query string
		count *int
	}{
		{"tokens", `SELECT * FROM tokens ORDER BY id`, &m.Tokens},
		{"collections", `SELECT * FROM collections WHERE deleted_at IS NULL AND (expires_at IS NULL OR expires_at > ?) ORDER BY rowid`, &m.Collections},
		{"items", `SELECT * FROM items WHERE deleted_at IS NULL AND (expires_at IS NULL OR expires_at > ?) ORDER BY rowid`, &m.Items},
	}
	type blobRef struct {
		sha   string
		thumb bool
	}
	var refs []blobRef
	seen := map[string]bool{}
	for _, t := range tables {
		var buf bytes.Buffer
		args := []any{}
		if strings.Contains(t.query, "?") {
			args = append(args, now)
		}
		n, err := dumpRows(ctx, db, t.query, args, &buf, func(row map[string]any) {
			if t.name != "items" {
				return
			}
			sha, _ := row["sha256"].(string)
			thumb := fmt.Sprint(row["thumb"]) == "1"
			if sha != "" && !seen[sha] {
				seen[sha] = true
				refs = append(refs, blobRef{sha, thumb})
			} else if thumb {
				for i := range refs {
					if refs[i].sha == sha {
						refs[i].thumb = true
					}
				}
			}
		})
		if err != nil {
			return m, fmt.Errorf("export %s: %w", t.name, err)
		}
		*t.count = n
		if err := addFile(tw, t.name+".jsonl", buf.Bytes()); err != nil {
			return m, err
		}
	}
	for _, ref := range refs {
		if err := addBlob(ctx, tw, bs, blob.Key(ref.sha), "blobs/"+blob.Key(ref.sha)); err != nil {
			return m, fmt.Errorf("blob %s: %w", ref.sha, err)
		}
		m.Blobs++
		if ref.thumb {
			tk := blob.ThumbKey(ref.sha, ingest.ThumbWidth)
			if err := addBlob(ctx, tw, bs, tk, tk); err != nil && !errors.Is(err, blob.ErrNotFound) {
				return m, err
			}
		}
	}
	if secretKey != "" {
		if b, err := os.ReadFile(secretKey); err == nil {
			if err := addFile(tw, "secret.key", b); err != nil {
				return m, err
			}
		}
	}
	mb, _ := json.MarshalIndent(m, "", "  ")
	if err := addFile(tw, "manifest.json", mb); err != nil {
		return m, err
	}
	if err := tw.Close(); err != nil {
		return m, err
	}
	return m, zw.Close()
}

func dumpRows(ctx context.Context, db *sql.DB, query string, args []any, w io.Writer, each func(map[string]any)) (int, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return 0, err
	}
	enc := json.NewEncoder(w)
	n := 0
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return n, err
		}
		row := make(map[string]any, len(cols))
		for i, c := range cols {
			if b, ok := vals[i].([]byte); ok {
				vals[i] = string(b)
			}
			row[c] = vals[i]
		}
		each(row)
		if err := enc.Encode(row); err != nil {
			return n, err
		}
		n++
	}
	return n, rows.Err()
}

func addFile(tw *tar.Writer, name string, b []byte) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(b)), ModTime: time.Now()}); err != nil {
		return err
	}
	_, err := tw.Write(b)
	return err
}

func addBlob(ctx context.Context, tw *tar.Writer, bs blob.Store, key, name string) error {
	r, err := bs.Open(ctx, key)
	if err != nil {
		return err
	}
	defer r.Close()
	size, err := r.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: size, ModTime: time.Now()}); err != nil {
		return err
	}
	_, err = io.Copy(tw, r)
	return err
}

// ImportResult summarizes an import.
type ImportResult struct {
	Manifest
	TokensKept int // tokens that already existed (matched by name)
}

// ErrNotEmpty is returned when the target already has items.
var ErrNotEmpty = errors.New("import needs an empty instance (no items or collections)")

// Import restores an archive into an empty instance. secretKey, if set, is
// overwritten with the archive's key so share links keep working (restart
// the server afterwards). tmpDir holds blobs while they are verified.
func Import(ctx context.Context, st *store.Store, bs blob.Store, secretKey, tmpDir string, r io.Reader) (ImportResult, error) {
	var res ImportResult
	db := st.DB()
	var n int
	if err := db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM items) + (SELECT COUNT(*) FROM collections)`).Scan(&n); err != nil {
		return res, err
	}
	if n > 0 {
		return res, ErrNotEmpty
	}
	zr, err := zstd.NewReader(r)
	if err != nil {
		return res, err
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	tables := map[string][]map[string]any{}
	gotManifest := false
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return res, fmt.Errorf("archive: %w", err)
		}
		name := path.Clean(h.Name)
		switch {
		case name == "manifest.json":
			if err := json.NewDecoder(tr).Decode(&res.Manifest); err != nil {
				return res, fmt.Errorf("manifest: %w", err)
			}
			if res.Format != Format {
				return res, errors.New("not a tibi export")
			}
			gotManifest = true
		case strings.HasSuffix(name, ".jsonl") && !strings.Contains(name, "/"):
			rows, err := readRows(tr)
			if err != nil {
				return res, fmt.Errorf("%s: %w", name, err)
			}
			tables[strings.TrimSuffix(name, ".jsonl")] = rows
		case strings.HasPrefix(name, "blobs/"):
			key := strings.TrimPrefix(name, "blobs/")
			if err := putVerified(ctx, bs, key, tr, h.Size, tmpDir); err != nil {
				return res, err
			}
		case strings.HasPrefix(name, "thumb/"):
			if strings.Contains(name, "..") {
				return res, fmt.Errorf("bad path %q", name)
			}
			if err := bs.Put(ctx, name, tr, h.Size, "image/webp"); err != nil {
				return res, err
			}
		case name == "secret.key" && secretKey != "":
			b, err := io.ReadAll(io.LimitReader(tr, 4096))
			if err != nil {
				return res, err
			}
			if err := os.WriteFile(secretKey, b, 0o600); err != nil {
				return res, err
			}
		}
	}
	if !gotManifest {
		return res, errors.New("archive has no manifest.json")
	}
	err = withTx(ctx, db, func(tx *sql.Tx) error {
		tokenIDs := map[string]int64{} // old id → new id
		for _, row := range tables["tokens"] {
			name := fmt.Sprint(row["name"])
			var existing int64
			err := tx.QueryRowContext(ctx, `SELECT id FROM tokens WHERE name = ? OR hash = ?`, name, row["hash"]).Scan(&existing)
			oldID := fmt.Sprint(row["id"])
			if err == nil {
				tokenIDs[oldID] = existing
				res.TokensKept++
				continue
			}
			delete(row, "id")
			newID, err := insertRow(ctx, tx, "tokens", row)
			if err != nil {
				return fmt.Errorf("token %s: %w", name, err)
			}
			tokenIDs[oldID] = newID
		}
		remap := func(row map[string]any) {
			if v, ok := row["token_id"]; ok {
				if id, ok := tokenIDs[fmt.Sprint(v)]; ok {
					row["token_id"] = id
				}
			}
		}
		for _, row := range tables["collections"] {
			remap(row)
			if _, err := insertRow(ctx, tx, "collections", row); err != nil {
				return fmt.Errorf("collection %v: %w", row["id"], err)
			}
		}
		for _, row := range tables["items"] {
			remap(row)
			if _, err := insertRow(ctx, tx, "items", row); err != nil {
				return fmt.Errorf("item %v: %w", row["id"], err)
			}
		}
		return nil
	})
	if err != nil {
		return res, err
	}
	// Search rows are derived data: rebuild them from the content.
	if _, err := ingest.Backfill(ctx, st, bs); err != nil {
		return res, fmt.Errorf("search index: %w", err)
	}
	return res, nil
}

func readRows(r io.Reader) ([]map[string]any, error) {
	var out []map[string]any
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		dec := json.NewDecoder(bytes.NewReader(sc.Bytes()))
		dec.UseNumber()
		var row map[string]any
		if err := dec.Decode(&row); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, sc.Err()
}

func putVerified(ctx context.Context, bs blob.Store, key string, r io.Reader, size int64, tmpDir string) error {
	sha := path.Base(key)
	if blob.Key(sha) != key || len(sha) != 64 {
		return fmt.Errorf("bad blob path %q", key)
	}
	tmp, err := os.CreateTemp(tmpDir, "import-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), r); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != sha {
		return fmt.Errorf("blob %s: content does not match its hash", sha)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return bs.Put(ctx, key, tmp, size, "")
}

// insertRow inserts the columns of row that the table has, returning the
// new rowid.
func insertRow(ctx context.Context, tx *sql.Tx, table string, row map[string]any) (int64, error) {
	cols, err := tableColumns(ctx, tx, table)
	if err != nil {
		return 0, err
	}
	var names, marks []string
	var args []any
	for _, c := range cols {
		v, ok := row[c]
		if !ok {
			continue
		}
		if num, ok := v.(json.Number); ok {
			if i, err := num.Int64(); err == nil {
				v = i
			} else {
				v, _ = num.Float64()
			}
		}
		names = append(names, c)
		marks = append(marks, "?")
		args = append(args, v)
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO `+table+` (`+strings.Join(names, ", ")+`) VALUES (`+strings.Join(marks, ", ")+`)`, args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func tableColumns(ctx context.Context, tx *sql.Tx, table string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

func withTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
