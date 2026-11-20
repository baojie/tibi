package cli_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"tibi/internal/testutil/testimg"
)

func TestAlbumCommand(t *testing.T) {
	e, env := loggedIn(t)
	env["TIBI_TOKEN"] = e.Token("full", "upload", "delete", "read_private")
	dir := t.TempDir()
	a, b, c := filepath.Join(dir, "a.png"), filepath.Join(dir, "b.png"), filepath.Join(dir, "c.txt")
	writeFile(t, a, testimg.PNG(20, 20))
	writeFile(t, b, testimg.PNG(30, 10))
	writeFile(t, c, []byte("third file\n"))

	// tibi up --album a.png b.png c.txt → one collection URL, all three in order.
	r := exec(t, env, nil, true, "up", "--album", "-t", "Holiday", a, b, c)
	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	if r.code != 0 || len(lines) != 1 || !strings.HasPrefix(lines[0], e.Cfg.BaseURL+"/c/") {
		t.Fatalf("album: %+v", r)
	}
	page := string(fetch(t, lines[0]))
	ia, ib, ic := strings.Index(page, "a.png"), strings.Index(page, "b.png"), strings.Index(page, "third file")
	if !strings.Contains(page, "Holiday") || ia < 0 || ib < ia || ic < ib {
		t.Fatalf("collection page: order %d %d %d", ia, ib, ic)
	}
	// --json gives the collection and its items.
	r = exec(t, env, nil, true, "up", "--album", "--json", a, c)
	var res struct {
		Collection struct{ ID, URL string }    `json:"collection"`
		Items      []struct{ Filename string } `json:"items"`
	}
	if json.Unmarshal([]byte(r.stdout), &res) != nil || len(res.Items) != 2 || res.Items[1].Filename != "c.txt" {
		t.Fatalf("album --json: %q", r.stdout)
	}
	if r := exec(t, env, nil, true, "up", "--album", "--burn", a); r.code != 2 {
		t.Fatalf("--album --burn: %+v", r)
	}
	// rm accepts a collection URL and deletes its items too.
	r = exec(t, env, nil, true, "rm", lines[0])
	if r.code != 0 || !strings.Contains(r.stderr, "deleted collection") {
		t.Fatalf("rm collection: %+v", r)
	}
	if statusOf(t, lines[0]) != 404 {
		t.Fatal("collection page still served")
	}
}

func TestForkCommand(t *testing.T) {
	e, env := loggedIn(t)
	r := exec(t, env, strings.NewReader("line one\n"), false, "paste", "-n", "notes.md")
	orig := strings.TrimSpace(r.stdout)

	// From a file.
	f := filepath.Join(t.TempDir(), "v2.md")
	writeFile(t, f, []byte("line one\nline two\n"))
	r = exec(t, env, nil, true, "fork", orig, f)
	v2 := strings.TrimSpace(r.stdout)
	if r.code != 0 || v2 == orig || string(fetch(t, v2+"/raw")) != "line one\nline two\n" {
		t.Fatalf("fork from file: %+v", r)
	}
	if string(fetch(t, orig+"/raw")) != "line one\n" {
		t.Fatal("original changed")
	}
	if !strings.Contains(string(fetch(t, v2)), "Forked from") {
		t.Fatal("fork page lacks backlink")
	}
	// From stdin, with an ID instead of a URL.
	id := strings.TrimPrefix(orig, e.Cfg.BaseURL+"/")
	r = exec(t, env, strings.NewReader("piped version\n"), false, "fork", id)
	if r.code != 0 || string(fetch(t, strings.TrimSpace(r.stdout)+"/raw")) != "piped version\n" {
		t.Fatalf("fork from stdin: %+v", r)
	}
	// In $EDITOR, prefilled with the original.
	env["EDITOR"] = `sh -c 'echo "added in editor" >> "$0"'`
	r = exec(t, env, nil, true, "fork", orig)
	if r.code != 0 || string(fetch(t, strings.TrimSpace(r.stdout)+"/raw")) != "line one\nadded in editor\n" {
		t.Fatalf("fork in editor: %+v", r)
	}
	// Saving without changes uploads nothing.
	env["EDITOR"] = "true"
	if r := exec(t, env, nil, true, "fork", orig); r.code == 0 || !strings.Contains(r.stderr, "no changes") {
		t.Fatalf("unchanged fork: %+v", r)
	}
	if r := exec(t, env, nil, true, "fork", "zzzzzzzz", f); r.code == 0 || !strings.Contains(r.stderr, "404") {
		t.Fatalf("fork missing: %+v", r)
	}
}
