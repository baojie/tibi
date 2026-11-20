package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"tibi/internal/client"
)

// album uploads files as one new collection and prints its URL. -t titles
// the collection.
func (a *App) album(ctx context.Context, cl *client.Client, cfg Config, paths []string, o *outFlags) error {
	if o.burn {
		return usageError{"--burn cannot be combined with --album"}
	}
	if o.slug != "" {
		return usageError{"--slug cannot be combined with --album"}
	}
	var files []client.File
	for _, p := range paths {
		if p == "-" {
			name := o.name
			if name == "" {
				name = "stdin"
			}
			files = append(files, client.File{Name: name, Body: a.Stdin})
			continue
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		if st, err := f.Stat(); err != nil || st.IsDir() {
			return fmt.Errorf("%s: not a regular file", p)
		}
		files = append(files, client.File{Name: filepath.Base(p), Body: f})
	}
	opts := a.uploadOptions(o)
	res, raw, err := cl.UploadAlbum(ctx, files, o.title, opts)
	if err != nil {
		return err
	}
	line := res.Collection.URL
	if o.json {
		var buf bytes.Buffer
		if err := json.Compact(&buf, raw); err != nil {
			return err
		}
		line = buf.String()
	}
	fmt.Fprintln(a.Stdout, line)
	a.copyResult(cfg, o, []string{line})
	fmt.Fprintf(a.Stderr, "%d items in collection\n", len(res.Items))
	return nil
}

// maxForkSource bounds how much of the original is loaded into the editor.
const maxForkSource = 64 << 20

func (a *App) fork(ctx context.Context, args []string) error {
	fs, o := a.uploadFlags("fork")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if fs.NArg() < 1 || fs.NArg() > 2 {
		return usageError{"usage: tibi fork ID [FILE|-]"}
	}
	iid, err := itemID(fs.Arg(0))
	if err != nil {
		return usageError{err.Error()}
	}
	cl, cfg, err := a.client()
	if err != nil {
		return err
	}
	orig, _, err := cl.Get(ctx, iid)
	if err != nil {
		return err
	}
	var body []byte
	switch {
	case fs.NArg() == 2 && fs.Arg(1) != "-":
		body, err = os.ReadFile(fs.Arg(1))
	case fs.NArg() == 2 || !a.StdinIsTTY:
		body, err = io.ReadAll(io.LimitReader(a.Stdin, maxPasteBytes))
	default:
		var before []byte
		if before, err = cl.Raw(ctx, orig, maxForkSource); err != nil {
			return err
		}
		name := orig.Filename
		if name == "" {
			name = "paste." + strings.TrimPrefix(filepath.Ext(orig.RawURL), ".")
		}
		if body, err = a.editText(name, before); err == nil && bytes.Equal(body, before) {
			return errors.New("no changes; nothing uploaded")
		}
	}
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return errors.New("empty content; nothing uploaded")
	}
	opts := a.uploadOptions(o)
	opts.Source = "cli"
	it, raw, err := cl.Fork(ctx, orig.ID, bytes.NewReader(body), int64(len(body)), opts)
	if err != nil {
		return err
	}
	line, err := a.result(it, raw, o)
	if err != nil {
		return err
	}
	fmt.Fprintln(a.Stdout, line)
	a.copyResult(cfg, o, []string{line})
	return nil
}
