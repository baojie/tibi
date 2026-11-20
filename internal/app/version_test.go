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

// Every released version has a screenshot of what it added, shown under
// its CHANGELOG heading (docs/screenshots/README.md), and the current
// version has a CHANGELOG section.
func TestChangelogScreenshots(t *testing.T) {
	b, err := os.ReadFile("../../CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	head := regexp.MustCompile(`(?m)^## \[([0-9.]+)\][^\n]*\n\n!\[[^\]]+\]\(([^)]+)\)$|^## \[([0-9.]+)\]`)
	seen := map[string]bool{}
	for _, m := range head.FindAllStringSubmatch(string(b), -1) {
		if m[3] != "" {
			t.Errorf("CHANGELOG.md: version %s has no screenshot right under its heading", m[3])
			continue
		}
		v, img := m[1], m[2]
		seen[v] = true
		if want := "docs/screenshots/v" + v + ".png"; img != want {
			t.Errorf("CHANGELOG.md: version %s shows %s, want %s", v, img, want)
		}
		if _, err := os.Stat("../../" + img); err != nil {
			t.Errorf("CHANGELOG.md: version %s: %v", v, err)
		}
	}
	if !seen[Version] {
		t.Errorf("CHANGELOG.md has no section for the current version %s", Version)
	}
}
