package store

import (
	"strings"
	"testing"
	"time"
)

func TestSnippetFoldAndLinear(t *testing.T) {
	if s := snippet(strings.Repeat("Ⱥ", 40)+"zqymarker tail", "ZQYMARKER"); !strings.Contains(s, "zqymarker") {
		t.Fatalf("snippet: %q", s)
	}
	// A long query word against a large body: linear, not quadratic.
	body := strings.Repeat("a", 1<<20) + "b"
	start := time.Now()
	snippet(body, strings.Repeat("a", 450)+"b")
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("snippet took %v", d)
	}
}
