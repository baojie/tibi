package app

import (
	"encoding/json"
	"os"
	"testing"
)

// The Chrome extension ships with the server and carries its version.
func TestExtensionVersion(t *testing.T) {
	b, err := os.ReadFile("../../extensions/chrome/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(b, &m); err != nil || m.Version != Version {
		t.Fatalf("extensions/chrome/manifest.json version %q, want %q", m.Version, Version)
	}
}
