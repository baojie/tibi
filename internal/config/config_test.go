package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "server.toml")
	os.WriteFile(p, []byte("base_url = \"https://example.com/\"\ndata_dir = \"/tmp/tibi-test\"\nmax_upload_bytes = 123\n"), 0o600)
	t.Setenv("TIBI_MAX_UPLOAD_BYTES", "456")
	c, err := Load(p, true)
	if err != nil {
		t.Fatal(err)
	}
	if c.BaseURL != "https://example.com" || c.MaxUploadBytes != 456 || c.Listen != "127.0.0.1:8070" {
		t.Fatalf("%+v", c)
	}
	if c.RawBase() != "https://example.com" {
		t.Fatal("raw base")
	}
	if _, err := Load(filepath.Join(dir, "missing.toml"), true); err == nil {
		t.Fatal("missing required file accepted")
	}
	if _, err := Load(filepath.Join(dir, "missing.toml"), false); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TIBI_MAX_UPLOAD_BYTES", "x")
	if _, err := Load(p, true); err == nil {
		t.Fatal("bad env accepted")
	}
}
