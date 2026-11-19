package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// maxPasteBytes bounds what `tibi paste` reads from stdin into memory; the
// server enforces its own limit.
const maxPasteBytes = 256 << 20

func (a *App) paste(ctx context.Context, args []string) error {
	fs, o := a.uploadFlags("paste")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if fs.NArg() > 0 {
		return usageError{"usage: tibi paste [-l LANG] [-t TITLE] (reads stdin, or opens $EDITOR)"}
	}
	cl, cfg, err := a.client()
	if err != nil {
		return err
	}
	var text []byte
	if a.StdinIsTTY {
		text, err = a.editText(o.name)
	} else {
		text, err = io.ReadAll(io.LimitReader(a.Stdin, maxPasteBytes))
	}
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(text)) == 0 {
		return errors.New("empty paste; nothing uploaded")
	}
	opts := a.uploadOptions(o)
	opts.Kind = "text"
	it, raw, err := cl.Upload(ctx, bytes.NewReader(text), int64(len(text)), opts)
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

// editor returns the user's editor command ($VISUAL, $EDITOR, then vi).
func (a *App) editor() string {
	for _, k := range []string{"VISUAL", "EDITOR"} {
		if v := strings.TrimSpace(a.getenv(k)); v != "" {
			return v
		}
	}
	return "vi"
}

// editText opens $EDITOR on an empty temp file and returns what was saved.
func (a *App) editText(name string) ([]byte, error) {
	pattern := "tibi-paste-*.txt"
	if ext := filepath.Ext(name); ext != "" && !strings.ContainsAny(ext, "*/\\") {
		pattern = "tibi-paste-*" + ext
	}
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return nil, err
	}
	path := f.Name()
	f.Close()
	defer os.Remove(path)

	// $EDITOR may carry arguments (e.g. "code --wait"), so run it via sh.
	cmd := exec.Command("sh", "-c", a.editor()+` "$1"`, "sh", path)
	if f, ok := a.Stdin.(*os.File); ok {
		cmd.Stdin = f
	} else {
		cmd.Stdin = os.Stdin
	}
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("editor %q: %w", a.editor(), err)
	}
	return os.ReadFile(path)
}
