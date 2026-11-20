package app

import (
	"encoding/json"
	"os"
	"regexp"
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

// So does the Android app.
func TestAndroidVersion(t *testing.T) {
	b, err := os.ReadFile("../../android/app/build.gradle.kts")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`versionName = "([^"]+)"`).FindSubmatch(b)
	if m == nil || string(m[1]) != Version {
		t.Fatalf("android versionName %q, want %q", m, Version)
	}
}
