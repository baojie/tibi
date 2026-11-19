package cli_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"tibi/internal/cli"
	"tibi/internal/testutil"
	"tibi/internal/testutil/testimg"
)

type fakeClip struct {
	types   []string
	data    map[string][]byte
	written []string
	failW   bool
}

func (f *fakeClip) Types() ([]string, error)       { return f.types, nil }
func (f *fakeClip) Read(mt string) ([]byte, error) { return f.data[mt], nil }
func (f *fakeClip) WriteText(s string) error {
	if f.failW {
		return errors.New("no clipboard tool")
	}
	f.written = append(f.written, s)
	return nil
}

func runWith(t *testing.T, env map[string]string, clip cli.Clipboard, stdoutTTY bool, args ...string) run {
	t.Helper()
	var out, errb bytes.Buffer
	a := &cli.App{
		Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb,
		StdinIsTTY: true, StdoutIsTTY: stdoutTTY, Clipboard: clip,
		Getenv: func(k string) string { return env[k] },
	}
	code := a.Run(context.Background(), args)
	return run{code, out.String(), errb.String()}
}

func loggedIn(t *testing.T) (*testutil.Env, map[string]string) {
	e := testutil.New(t, nil)
	return e, map[string]string{"HOME": t.TempDir(), "TIBI_SERVER": e.Srv.URL, "TIBI_TOKEN": e.Token("cli")}
}

func TestOutputModesAndCopy(t *testing.T) {
	e, env := loggedIn(t)
	f := t.TempDir() + "/pic.png"
	writeFile(t, f, testimg.PNG(10, 10))

	clip := &fakeClip{}
	r := runWith(t, env, clip, true, "up", f)
	url := strings.TrimSpace(r.stdout)
	if r.code != 0 || !strings.HasPrefix(url, e.Cfg.BaseURL+"/") || strings.Contains(url, ".png") {
		t.Fatalf("plain: %+v", r)
	}
	if len(clip.written) != 1 || clip.written[0] != url {
		t.Fatalf("clipboard = %q", clip.written)
	}

	r = runWith(t, env, clip, true, "up", "--raw", f)
	if !strings.HasSuffix(strings.TrimSpace(r.stdout), ".png") {
		t.Fatalf("--raw: %q", r.stdout)
	}
	r = runWith(t, env, clip, true, "up", "--md", f)
	md := strings.TrimSpace(r.stdout)
	if !strings.HasPrefix(md, "![](") || !strings.HasSuffix(md, ".png)") || clip.written[len(clip.written)-1] != md {
		t.Fatalf("--md: %q / %q", md, clip.written)
	}

	// No copy when stdout is not a terminal, with --no-copy, or with --json.
	n := len(clip.written)
	runWith(t, env, clip, false, "up", f)
	runWith(t, env, clip, true, "up", "--no-copy", f)
	runWith(t, env, clip, true, "up", "--json", f)
	if len(clip.written) != n {
		t.Fatalf("unexpected copies: %q", clip.written[n:])
	}

	// A clipboard failure is a warning, not an error.
	r = runWith(t, env, &fakeClip{failW: true}, true, "up", f)
	if r.code != 0 || !strings.Contains(r.stderr, "not copied") {
		t.Fatalf("copy failure: %+v", r)
	}
}

func TestClip(t *testing.T) {
	e, env := loggedIn(t)
	png := testimg.PNG(12, 7)

	// Image preferred over text.
	clip := &fakeClip{
		types: []string{"text/plain", "image/png", "TARGETS"},
		data:  map[string][]byte{"image/png": png, "text/plain": []byte("ignored")},
	}
	r := runWith(t, env, clip, false, "clip", "--raw")
	if r.code != 0 || !strings.HasSuffix(strings.TrimSpace(r.stdout), ".png") {
		t.Fatalf("clip image: %+v", r)
	}
	if got := fetch(t, strings.TrimSpace(r.stdout)); !bytes.Equal(got, png) {
		t.Fatal("clipboard image bytes differ")
	}

	// Text only.
	clip = &fakeClip{types: []string{"UTF8_STRING", "TARGETS"}, data: map[string][]byte{"UTF8_STRING": []byte("copied text")}}
	r = runWith(t, env, clip, false, "clip")
	if r.code != 0 || string(fetch(t, strings.TrimSpace(r.stdout)+"/raw")) != "copied text" {
		t.Fatalf("clip text: %+v", r)
	}
	if !strings.Contains(string(fetch(t, strings.TrimSpace(r.stdout))), "clipboard.txt") {
		t.Fatal("default filename not applied")
	}

	// Nothing usable.
	r = runWith(t, env, &fakeClip{types: []string{"application/x-kde-cutselection"}}, false, "clip")
	if r.code == 0 || r.stdout != "" {
		t.Fatalf("empty clip: %+v", r)
	}
	_ = e
}
