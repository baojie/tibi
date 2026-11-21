package mcp_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baojie/tibi/internal/mcp"
)

// TestPathPolicy: an agent may upload from the project, the temp directory
// and directories the user allowed — never keys, credentials, hidden files,
// or anything a symlink smuggles in (design 2026-11-21 §1).
func TestPathPolicy(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	proj := filepath.Join(home, "proj")
	extra := filepath.Join(base, "extra")
	tmp := filepath.Join(base, "tmp")
	write := func(p string) string {
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write(filepath.Join(proj, "chart.png"))
	write(filepath.Join(proj, "sub", "report.pdf"))
	write(filepath.Join(proj, ".env"))
	write(filepath.Join(proj, ".git", "config"))
	write(filepath.Join(proj, "deploy", "server.pem"))
	write(filepath.Join(proj, "credentials.json"))
	write(filepath.Join(home, ".ssh", "id_ed25519"))
	write(filepath.Join(home, ".ssh", "known_hosts"))
	write(filepath.Join(home, ".config", "tibi", "config.toml"))
	write(filepath.Join(home, "Documents", "tax.pdf"))
	write(filepath.Join(extra, "photo.jpg"))
	write(filepath.Join(tmp, "out.log"))
	os.Symlink(filepath.Join(home, ".ssh", "id_ed25519"), filepath.Join(proj, "innocent.txt"))
	os.Symlink(filepath.Join(home, "Documents", "tax.pdf"), filepath.Join(proj, "tax-link.pdf"))
	os.Symlink(filepath.Join(home, ".ssh"), filepath.Join(proj, "keys"))

	pp := mcp.PathPolicy{WorkDir: proj, Home: home, TempDir: tmp, AllowDirs: []string{extra}}
	for _, ok := range []string{
		"chart.png", "sub/report.pdf", filepath.Join(proj, "chart.png"), "./sub/../chart.png",
		filepath.Join(extra, "photo.jpg"), filepath.Join(tmp, "out.log"),
	} {
		if _, err := pp.Resolve(ok); err != nil {
			t.Errorf("%s refused: %v", ok, err)
		}
	}
	for path, want := range map[string]string{
		".env":                       "hidden",
		".git/config":                "hidden",
		"deploy/server.pem":          "key or credentials",
		"credentials.json":           "key or credentials",
		"~/.ssh/id_ed25519":          "~/.ssh",
		"../.ssh/id_ed25519":         "~/.ssh",
		"~/.config/tibi/config.toml": "~/.config",
		"~/Documents/tax.pdf":        "outside",
		"innocent.txt":               "~/.ssh", // symlink into ~/.ssh
		"tax-link.pdf":               "outside",
		"keys/known_hosts":           "~/.ssh",
	} {
		_, err := pp.Resolve(path)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err %v, want %q", path, err, want)
		}
	}
	// Refusals do not reveal whether the file exists.
	if _, err := pp.Resolve("~/.ssh/id_rsa"); err == nil || !strings.Contains(err.Error(), "~/.ssh") {
		t.Errorf("missing key: %v", err)
	}
	if _, err := pp.Resolve("missing.png"); err == nil || strings.Contains(err.Error(), "refused") {
		t.Errorf("missing file: %v", err)
	}

	// Started in $HOME, the working directory is not a root: otherwise
	// everything in the home directory would be allowed.
	inHome := mcp.PathPolicy{WorkDir: home, Home: home, TempDir: tmp}
	if _, err := inHome.Resolve("Documents/tax.pdf"); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Errorf("cwd=$HOME: %v", err)
	}
	if _, err := inHome.Resolve(filepath.Join(tmp, "out.log")); err != nil {
		t.Errorf("cwd=$HOME, temp file: %v", err)
	}
}
