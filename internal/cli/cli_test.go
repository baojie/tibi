package cli_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tibi/internal/cli"
	"tibi/internal/testutil"
)

type run struct {
	code   int
	stdout string
	stderr string
}

func newApp(t *testing.T, env map[string]string, stdin io.Reader, tty bool) (*cli.App, *bytes.Buffer, *bytes.Buffer) {
	var out, errb bytes.Buffer
	if stdin == nil {
		stdin = strings.NewReader("")
	}
	return &cli.App{
		Stdin: stdin, Stdout: &out, Stderr: &errb, StdinIsTTY: tty,
		Getenv: func(k string) string { return env[k] },
	}, &out, &errb
}

func exec(t *testing.T, env map[string]string, stdin io.Reader, tty bool, args ...string) run {
	t.Helper()
	a, out, errb := newApp(t, env, stdin, tty)
	code := a.Run(context.Background(), args)
	return run{code, out.String(), errb.String()}
}

func fetch(t *testing.T, url string) []byte {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("GET %s: %d", url, resp.StatusCode)
	}
	return b
}

func TestLoginAndUpload(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("cli")
	home := t.TempDir()
	env := map[string]string{"HOME": home}

	// Interactive-style login reading server and token from stdin.
	r := exec(t, env, strings.NewReader(e.Srv.URL+"\n"+tok+"\n"), false, "login")
	if r.code != 0 {
		t.Fatalf("login: %d %s", r.code, r.stderr)
	}
	if r.stdout != "" {
		t.Fatalf("login wrote to stdout: %q", r.stdout)
	}
	cfgPath := filepath.Join(home, ".config", "tibi", "config.toml")
	st, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("config mode %v", st.Mode().Perm())
	}

	// tibi up x.bin → one URL whose /dl matches.
	data := bytes.Repeat([]byte{0, 1, 2, 3, 250}, 10000)
	f := filepath.Join(t.TempDir(), "x.bin")
	os.WriteFile(f, data, 0o644)
	r = exec(t, env, nil, true, "up", f)
	if r.code != 0 {
		t.Fatalf("up: %d %s", r.code, r.stderr)
	}
	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], e.Cfg.BaseURL+"/") {
		t.Fatalf("stdout = %q", r.stdout)
	}
	if got := fetch(t, lines[0]+"/dl"); !bytes.Equal(got, data) {
		t.Fatal("downloaded bytes differ")
	}
	if !strings.Contains(string(fetch(t, lines[0])), "x.bin") {
		t.Fatal("filename not on view page")
	}

	// tibi up - (stdin)
	r = exec(t, env, strings.NewReader("from stdin"), false, "up", "-n", "note.txt", "-")
	if r.code != 0 || string(fetch(t, strings.TrimSpace(r.stdout)+"/dl")) != "from stdin" {
		t.Fatalf("up -: %+v", r)
	}

	// Bare `tibi` with piped stdin.
	r = exec(t, env, strings.NewReader("piped"), false)
	if r.code != 0 || string(fetch(t, strings.TrimSpace(r.stdout)+"/raw")) != "piped" {
		t.Fatalf("bare tibi: %+v", r)
	}

	// Bare `tibi` on a TTY prints usage, not an upload.
	if r := exec(t, env, nil, true); r.code != 2 || r.stdout != "" {
		t.Fatalf("bare tibi tty: %+v", r)
	}

	// Multiple files → one URL per line; --json prints JSON.
	g := filepath.Join(t.TempDir(), "y.txt")
	os.WriteFile(g, []byte("y"), 0o644)
	r = exec(t, env, nil, true, "up", f, g)
	if n := len(strings.Split(strings.TrimSpace(r.stdout), "\n")); r.code != 0 || n != 2 {
		t.Fatalf("multi: %+v", r)
	}
	r = exec(t, env, nil, true, "up", "--json", g)
	if r.code != 0 || !strings.HasPrefix(r.stdout, `{"id":`) {
		t.Fatalf("json: %+v", r)
	}
}

func TestEnvOverridesAndErrors(t *testing.T) {
	e := testutil.New(t, nil)
	tok := e.Token("cli")
	env := map[string]string{"HOME": t.TempDir(), "TIBI_SERVER": e.Srv.URL, "TIBI_TOKEN": tok}
	r := exec(t, env, strings.NewReader("abc"), false, "up", "-")
	if r.code != 0 || !strings.HasPrefix(r.stdout, e.Cfg.BaseURL) {
		t.Fatalf("env login: %+v", r)
	}
	// Bad token → nonzero exit, nothing on stdout.
	env["TIBI_TOKEN"] = "tibi_wrongwrongwrongwrongwrongwrong"
	r = exec(t, env, strings.NewReader("abc"), false, "up", "-")
	if r.code == 0 || r.stdout != "" || !strings.Contains(r.stderr, "401") {
		t.Fatalf("bad token: %+v", r)
	}
	// Login with bad token fails and does not save.
	r = exec(t, map[string]string{"HOME": env["HOME"]}, nil, false, "login", "--server", e.Srv.URL, "--token", "tibi_nope")
	if r.code == 0 {
		t.Fatalf("login with bad token succeeded")
	}
	// Not logged in.
	r = exec(t, map[string]string{"HOME": t.TempDir()}, strings.NewReader("x"), false, "up", "-")
	if r.code == 0 || !strings.Contains(r.stderr, "not logged in") {
		t.Fatalf("no config: %+v", r)
	}
	// Missing file.
	env["TIBI_TOKEN"] = tok
	r = exec(t, env, nil, true, "up", "/nonexistent/file")
	if r.code != 1 || r.stdout != "" {
		t.Fatalf("missing file: %+v", r)
	}
}
