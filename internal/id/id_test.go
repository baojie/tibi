package id

import "testing"

func TestNew(t *testing.T) {
	seen := map[string]bool{}
	for range 10000 {
		s := New()
		if !Valid(s) {
			t.Fatalf("invalid id %q", s)
		}
		if seen[s] {
			t.Fatalf("collision %q", s)
		}
		seen[s] = true
	}
}

func TestValid(t *testing.T) {
	for _, s := range []string{"", "abc", "abcdefg!", "abcdefghi", "robots.txt", "healthz"} {
		if Valid(s) {
			t.Errorf("Valid(%q) = true", s)
		}
	}
	if !Valid("aZ3k9QxP") {
		t.Error("aZ3k9QxP should be valid")
	}
}
