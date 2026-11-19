package cli_test

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"tibi/internal/cli"
)

func TestManageCommands(t *testing.T) {
	e, env := loggedIn(t)
	// Upload token needs delete scope for rm.
	env["TIBI_TOKEN"] = e.Token("full", "upload", "delete", "read_private")

	r := exec(t, env, strings.NewReader("first"), false, "up", "-n", "a.txt", "-t", "First note", "-")
	first := strings.TrimSpace(r.stdout)
	r = exec(t, env, strings.NewReader("second"), false, "up", "-n", "b.txt", "-e", "1h", "-p", "--slug", "my-link", "-")
	second := strings.TrimSpace(r.stdout)
	if r.code != 0 {
		t.Fatalf("up with flags: %+v", r)
	}
	r = exec(t, env, strings.NewReader("burn me"), false, "paste", "--burn")
	burn := strings.TrimSpace(r.stdout)

	// ls: newest first, URL in the first column, flags shown.
	r = exec(t, env, nil, true, "ls")
	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	if r.code != 0 || len(lines) != 3 {
		t.Fatalf("ls: %+v", r)
	}
	if !strings.HasPrefix(lines[0], burn) || !strings.HasPrefix(lines[1], second) || !strings.HasPrefix(lines[2], first) {
		t.Fatalf("ls order:\n%s", r.stdout)
	}
	if !strings.Contains(lines[1], "private") || !strings.Contains(lines[1], "expires in") || !strings.Contains(lines[1], "/s/my-link") || !strings.Contains(lines[0], "burn") {
		t.Fatalf("ls flags:\n%s", r.stdout)
	}
	if !strings.Contains(lines[2], "First note") {
		t.Fatalf("ls title:\n%s", r.stdout)
	}
	r = exec(t, env, nil, true, "ls", "-n", "1", "--json")
	if strings.Count(r.stdout, "\n") != 1 || !strings.HasPrefix(r.stdout, `{"id":`) {
		t.Fatalf("ls --json: %q", r.stdout)
	}

	// info accepts IDs and URLs (incl. raw/ext forms).
	for _, ref := range []string{second, second + "/raw", strings.TrimPrefix(second, e.Cfg.BaseURL+"/")} {
		r = exec(t, env, nil, true, "info", ref)
		if r.code != 0 || !strings.Contains(r.stdout, "visibility:") || !strings.Contains(r.stdout, "private") || !strings.Contains(r.stdout, "b.txt") {
			t.Fatalf("info %s: %+v", ref, r)
		}
	}
	if r := exec(t, env, nil, true, "info", "not-an-id"); r.code != 2 {
		t.Fatalf("info bad id: %+v", r)
	}

	// slug: set, then the short link redirects.
	r = exec(t, env, nil, true, "slug", first, "first-note")
	if r.code != 0 || strings.TrimSpace(r.stdout) != e.Cfg.BaseURL+"/s/first-note" {
		t.Fatalf("slug: %+v", r)
	}
	resp, err := http.Get(e.Srv.URL + "/s/first-note")
	if err != nil || resp.Request.URL.String() != first {
		t.Fatalf("slug redirect landed at %v", resp.Request.URL)
	}
	if r := exec(t, env, nil, true, "slug", first, "api"); r.code == 0 {
		t.Fatal("reserved slug accepted")
	}

	// open uses the browser hook with the item URL.
	var opened string
	var out, errb bytes.Buffer
	a := &cli.App{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb, StdinIsTTY: true,
		Getenv: func(k string) string { return env[k] }, OpenURL: func(u string) error { opened = u; return nil }}
	if code := a.Run(context.Background(), []string{"open", first}); code != 0 || opened != first {
		t.Fatalf("open: %d %q %s", code, opened, errb.String())
	}

	// rm: several at once; then they are gone.
	r = exec(t, env, nil, true, "rm", first, second)
	if r.code != 0 || strings.Count(r.stderr, "deleted") != 2 || r.stdout != "" {
		t.Fatalf("rm: %+v", r)
	}
	if code := statusOf(t, first); code != 404 {
		t.Fatalf("after rm: %d", code)
	}
	r = exec(t, env, nil, true, "rm", first)
	if r.code != 1 || !strings.Contains(r.stderr, "404") {
		t.Fatalf("rm twice: %+v", r)
	}
	r = exec(t, env, nil, true, "ls")
	if len(strings.Split(strings.TrimSpace(r.stdout), "\n")) != 1 {
		t.Fatalf("ls after rm:\n%s", r.stdout)
	}
	// Bad expiry is reported by the server.
	if r := exec(t, env, strings.NewReader("x"), false, "up", "-e", "soon", "-"); r.code == 0 || !strings.Contains(r.stderr, "expiry") {
		t.Fatalf("bad expiry: %+v", r)
	}
}

func TestDefaultExpiresFromConfig(t *testing.T) {
	e, env := loggedIn(t)
	cfgDir := env["HOME"] + "/.config/tibi"
	r := exec(t, env, nil, false, "login", "--server", env["TIBI_SERVER"], "--token", env["TIBI_TOKEN"])
	if r.code != 0 {
		t.Fatal(r.stderr)
	}
	writeFile(t, cfgDir+"/config.toml", []byte("server = \""+e.Srv.URL+"\"\ntoken = \""+env["TIBI_TOKEN"]+"\"\ndefault_expires = \"7d\"\n"))
	r = exec(t, env, strings.NewReader("x"), false, "up", "--json", "-")
	if !strings.Contains(r.stdout, `"expires_at":1`) {
		t.Fatalf("default_expires not applied: %s", r.stdout)
	}
	r = exec(t, env, strings.NewReader("y"), false, "up", "--json", "-e", "never", "-")
	if !strings.Contains(r.stdout, `"expires_at":null`) {
		t.Fatalf("-e never should override: %s", r.stdout)
	}
}

func statusOf(t *testing.T, url string) int {
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}
