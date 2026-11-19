package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPaste(t *testing.T) {
	e, env := loggedIn(t)

	// echo 'print(1)' | tibi paste -l python → one URL, highlighted Python.
	r := exec(t, env, strings.NewReader("print(1)\n"), false, "paste", "-l", "python")
	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	if r.code != 0 || len(lines) != 1 || !strings.HasPrefix(lines[0], e.Cfg.BaseURL+"/") {
		t.Fatalf("paste: %+v", r)
	}
	if page := string(fetch(t, lines[0])); !strings.Contains(page, `<span class="nb">print</span>`) {
		t.Fatal("not highlighted as Python")
	}

	// cat main.go | tibi → Go inferred from content.
	gosrc := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(1)\n}\n"
	r = exec(t, env, strings.NewReader(gosrc), false, "--json")
	var it struct {
		Lang string `json:"lang"`
	}
	if r.code != 0 || json.Unmarshal([]byte(r.stdout), &it) != nil || it.Lang != "go" {
		t.Fatalf("piped go: %+v", r)
	}

	// Title and unknown lang.
	r = exec(t, env, strings.NewReader("x"), false, "paste", "-t", "My Note")
	if r.code != 0 || !strings.Contains(string(fetch(t, strings.TrimSpace(r.stdout))), "My Note") {
		t.Fatalf("title: %+v", r)
	}
	r = exec(t, env, strings.NewReader("x"), false, "paste", "-l", "nosuchlang")
	if r.code == 0 || !strings.Contains(r.stderr, "unknown lang") {
		t.Fatalf("bad lang: %+v", r)
	}

	// Empty paste uploads nothing.
	r = exec(t, env, strings.NewReader("  \n\n"), false, "paste")
	if r.code == 0 || r.stdout != "" || !strings.Contains(r.stderr, "empty") {
		t.Fatalf("empty: %+v", r)
	}
	// Binary on stdin is rejected by the server as kind=text.
	r = exec(t, env, strings.NewReader("\x00\x01\x02"), false, "paste")
	if r.code == 0 {
		t.Fatalf("binary paste accepted: %+v", r)
	}
}

func TestPasteEditor(t *testing.T) {
	_, env := loggedIn(t)
	src := filepath.Join(t.TempDir(), "typed.txt")
	os.WriteFile(src, []byte("written in the editor\n"), 0o644)
	// The "editor" copies prepared text into the file it is given.
	env["EDITOR"] = "cp " + src
	r := exec(t, env, nil, true, "paste")
	if r.code != 0 {
		t.Fatalf("editor paste: %+v", r)
	}
	if got := string(fetch(t, strings.TrimSpace(r.stdout)+"/raw")); got != "written in the editor\n" {
		t.Fatalf("raw = %q", got)
	}
	// Saving nothing aborts.
	env["EDITOR"] = "true"
	if r := exec(t, env, nil, true, "paste"); r.code == 0 || r.stdout != "" {
		t.Fatalf("empty editor: %+v", r)
	}
	// A failing editor aborts.
	env["EDITOR"] = "false"
	if r := exec(t, env, nil, true, "paste"); r.code == 0 {
		t.Fatalf("failing editor: %+v", r)
	}
}
