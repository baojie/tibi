package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "server.toml")
	os.WriteFile(p, []byte("base_url = \"https://example.com/\"\ndata_dir = \"/tmp/tibi-test\"\nmax_upload_bytes = 123\n"), 0o600)
	t.Setenv("TIBI_MAX_UPLOAD_BYTES", "456")
	t.Setenv("TIBI_CORS_ORIGINS", "chrome-extension://abc, https://app.example")
	c, err := Load(p, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(c.CORSOrigins, "|") != "chrome-extension://abc|https://app.example" {
		t.Fatalf("cors origins: %q", c.CORSOrigins)
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

func TestS3SecretsPermission(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "server.toml")
	body := []byte("data_dir = \"/tmp/x\"\n[storage]\nbackend = \"s3\"\n[storage.s3]\nendpoint = \"s3.example.com\"\nbucket = \"b\"\naccess_key = \"AK\"\nsecret_key = \"SK\"\n")
	os.WriteFile(p, body, 0o644)
	os.Chmod(p, 0o644)
	if _, err := Load(p, true); err == nil || !strings.Contains(err.Error(), "world-readable") {
		t.Fatalf("world-readable secrets accepted: %v", err)
	}
	os.Chmod(p, 0o600)
	c, err := Load(p, true)
	if err != nil || c.Storage.Backend != "s3" {
		t.Fatalf("0600 rejected: %v", err)
	}
	// Keys from the environment are fine with a world-readable file.
	os.WriteFile(p, []byte("data_dir = \"/tmp/x\"\n[storage]\nbackend = \"s3\"\n[storage.s3]\nendpoint = \"e\"\nbucket = \"b\"\n"), 0o644)
	os.Chmod(p, 0o644)
	t.Setenv("TIBI_S3_ACCESS_KEY", "AK")
	t.Setenv("TIBI_S3_SECRET_KEY", "SK")
	if _, err := Load(p, true); err != nil {
		t.Fatalf("env keys: %v", err)
	}
	t.Setenv("TIBI_S3_SECRET_KEY", "")
	if _, err := Load(p, true); err == nil {
		t.Fatal("missing secret accepted")
	}
}
