package cli_test

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baojie/tibi/internal/cli"
	"github.com/baojie/tibi/internal/e2e"
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

func TestSearchCommand(t *testing.T) {
	_, env := loggedIn(t)
	exec(t, env, strings.NewReader("func handleUpload() {}\n"), false, "up", "-n", "h.go", "-")
	exec(t, env, strings.NewReader("题壁诗\n"), false, "paste")
	r := exec(t, env, nil, true, "search", "handleUpload")
	lines := strings.Split(strings.TrimRight(r.stdout, "\n"), "\n")
	if r.code != 0 || len(lines) != 2 || !strings.Contains(lines[0], "h.go") || strings.TrimSpace(lines[1]) != "func handleUpload() {}" {
		t.Fatalf("search: %+v", r)
	}
	r = exec(t, env, nil, true, "search", "题壁")
	if r.code != 0 || !strings.Contains(r.stdout, "题壁诗") {
		t.Fatalf("chinese: %+v", r)
	}
	r = exec(t, env, nil, true, "search", "--json", "题壁")
	if !strings.Contains(r.stdout, `"snippet":"题壁诗"`) {
		t.Fatalf("json: %q", r.stdout)
	}
	if r := exec(t, env, nil, true, "search", "zzzznomatch"); r.code != 0 || r.stdout != "" || !strings.Contains(r.stderr, "no matches") {
		t.Fatalf("no match: %+v", r)
	}
	if r := exec(t, env, nil, true, "search"); r.code != 2 {
		t.Fatalf("no query: %+v", r)
	}
}

func TestShareCommand(t *testing.T) {
	_, env := loggedIn(t)
	r := exec(t, env, strings.NewReader("top secret"), false, "up", "-p", "-")
	u := strings.TrimSpace(r.stdout)
	if statusOf(t, u) != 404 {
		t.Fatal("private item public")
	}
	r = exec(t, env, nil, true, "share", "--ttl", "10m", u)
	link := strings.TrimSpace(r.stdout)
	if r.code != 0 || !strings.Contains(link, "sig=") || !strings.Contains(r.stderr, "valid until") {
		t.Fatalf("share: %+v", r)
	}
	if statusOf(t, link) != 200 {
		t.Fatal("share link does not open the private item")
	}
	if r := exec(t, env, nil, true, "share", "--ttl", "never", u); r.code == 0 {
		t.Fatal("ttl never accepted")
	}
}

func TestUpUsesResumableAndResumes(t *testing.T) {
	e, env := loggedIn(t)
	env["XDG_CACHE_HOME"] = t.TempDir()
	data := make([]byte, 40<<20)
	for i := range data {
		data[i] = byte(i * 7)
	}
	f := filepath.Join(t.TempDir(), "video.bin")
	writeFile(t, f, data)

	run := func(ctx context.Context) (int, string, string) {
		var out, errb bytes.Buffer
		a := &cli.App{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb, StdinIsTTY: true,
			StderrIsTTY: true, ResumableThreshold: 1 << 20, Getenv: func(k string) string { return env[k] }}
		code := a.Run(ctx, []string{"up", f})
		return code, out.String(), errb.String()
	}
	// First attempt dies partway (the context stands in for Ctrl-C / kill).
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for {
			entries, _ := os.ReadDir(filepath.Join(env["XDG_CACHE_HOME"], "tibi", "uploads"))
			parts, _ := os.ReadDir(filepath.Join(e.Cfg.DataDir, "uploads"))
			if len(entries) > 0 && len(parts) > 0 {
				if fi, err := os.Stat(filepath.Join(e.Cfg.DataDir, "uploads", strings.TrimSuffix(parts[0].Name(), ".info"))); err == nil && fi.Size() > 16<<20 {
					cancel()
					return
				}
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	if code, out, _ := run(ctx); code == 0 || out != "" {
		t.Fatalf("interrupted run: %d %q", code, out)
	}
	time.Sleep(200 * time.Millisecond)
	code, out, stderr := run(context.Background())
	if code != 0 || !strings.Contains(stderr, "resuming at") || !strings.Contains(stderr, "100.0%") {
		t.Fatalf("second run: %d %q %q", code, out, stderr)
	}
	if got := fetch(t, strings.TrimSpace(out)+"/dl"); !bytes.Equal(got, data) {
		t.Fatal("resumed upload differs")
	}
}

func TestEncryptCommands(t *testing.T) {
	e, env := loggedIn(t)
	decrypt := func(link, password string) (e2e.Header, string) {
		t.Helper()
		page, frag, ok := strings.Cut(link, "#k=")
		if !ok {
			t.Fatalf("no key in %q", link)
		}
		key, err := e2e.ParseKey(frag)
		if err != nil {
			t.Fatal(err)
		}
		h, plain, err := e2e.Decrypt(fetch(t, page+"/raw"), key, password)
		if err != nil {
			t.Fatal(err)
		}
		return h, string(plain)
	}
	r := exec(t, env, strings.NewReader("my secret notes\n"), false, "paste", "--encrypt", "-l", "go")
	link := strings.TrimSpace(r.stdout)
	if r.code != 0 || !strings.Contains(link, "#k=") {
		t.Fatalf("paste --encrypt: %+v", r)
	}
	if h, plain := decrypt(link, ""); plain != "my secret notes\n" || h.Lang != "go" || h.Name != "paste.txt" {
		t.Fatalf("decrypted %q %+v", plain, h)
	}
	if strings.Contains(string(fetch(t, strings.Split(link, "#")[0]+"/raw")), "secret") {
		t.Fatal("server has plaintext")
	}
	// up --encrypt keeps the name and type inside the ciphertext.
	f := filepath.Join(t.TempDir(), "report.pdf")
	writeFile(t, f, []byte("%PDF-1.4 confidential"))
	r = exec(t, env, nil, true, "up", "--encrypt", "--no-copy", f)
	if h, plain := decrypt(strings.TrimSpace(r.stdout), ""); plain != "%PDF-1.4 confidential" || h.Name != "report.pdf" || h.Type != "application/pdf" {
		t.Fatalf("up --encrypt: %+v %q", h, plain)
	}
	// --password (from the environment, as scripts do).
	env["TIBI_E2E_PASSWORD"] = "open sesame"
	r = exec(t, env, strings.NewReader("pw protected"), false, "paste", "--encrypt", "--password")
	if _, plain := decrypt(strings.TrimSpace(r.stdout), "open sesame"); plain != "pw protected" {
		t.Fatal("password paste")
	}
	delete(env, "TIBI_E2E_PASSWORD")
	// Interactive password: asked twice, must match.
	var out, errb bytes.Buffer
	answers := []string{"a", "b"}
	a := &cli.App{Stdin: strings.NewReader("x"), Stdout: &out, Stderr: &errb, Getenv: func(k string) string { return env[k] },
		ReadSecret: func() (string, error) { s := answers[0]; answers = answers[1:]; return s, nil }}
	if code := a.Run(context.Background(), []string{"paste", "--encrypt", "--password"}); code == 0 || !strings.Contains(errb.String(), "do not match") {
		t.Fatalf("mismatched passwords: %d %s", code, errb.String())
	}
	// --json reports the link with its key; incompatible flags are refused.
	r = exec(t, env, strings.NewReader("j"), false, "paste", "--encrypt", "--json")
	if !strings.Contains(r.stdout, `#k=`) || !strings.Contains(r.stdout, `"kind":"encrypted"`) {
		t.Fatalf("--json: %q", r.stdout)
	}
	for _, args := range [][]string{{"up", "--encrypt", "--md", f}, {"up", "--encrypt", "--album", f, f}, {"up", "--password", f}} {
		if r := exec(t, env, nil, true, args...); r.code != 2 {
			t.Errorf("%v: %+v", args, r)
		}
	}
	_ = e
}

func TestSourceURLFlag(t *testing.T) {
	e, env := loggedIn(t)
	env["TIBI_TOKEN"] = e.Token("laptop", "upload")
	r := exec(t, env, strings.NewReader("from the web"), false, "up", "-n", "a.txt", "--source-url", "https://example.com/post/1", "-")
	link := strings.TrimSpace(r.stdout)
	if r.code != 0 {
		t.Fatalf("up --source-url: %+v", r)
	}
	r = exec(t, env, nil, true, "info", link)
	if r.code != 0 || !strings.Contains(r.stdout, "https://example.com/post/1") {
		t.Fatalf("info does not show the source URL: %+v", r)
	}
	r = exec(t, env, strings.NewReader("pasted"), false, "paste", "--source-url", "https://example.com/post/2")
	if r.code != 0 {
		t.Fatalf("paste --source-url: %+v", r)
	}
	if r := exec(t, env, nil, true, "info", strings.TrimSpace(r.stdout)); !strings.Contains(r.stdout, "https://example.com/post/2") {
		t.Fatalf("paste lost the source URL: %+v", r)
	}
	// Not stored for encrypted items (it would be plaintext next to them),
	// and only http(s) URLs.
	if r := exec(t, env, strings.NewReader("secret"), false, "paste", "--encrypt", "--source-url", "https://example.com/x"); r.code != 2 {
		t.Fatalf("--encrypt --source-url: %+v", r)
	}
	if r := exec(t, env, strings.NewReader("x"), false, "up", "-n", "x.txt", "--source-url", "file:///etc/passwd", "-"); r.code == 0 {
		t.Fatalf("file: URL accepted: %+v", r)
	}
}
