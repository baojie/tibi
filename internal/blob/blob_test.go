package blob

import "testing"

func TestStoredType(t *testing.T) {
	for in, want := range map[string]string{
		"text/html": "application/octet-stream", "image/svg+xml": "application/octet-stream",
		"application/javascript": "application/octet-stream", "": "application/octet-stream",
		"image/png": "image/png", "VIDEO/MP4": "video/mp4", "text/plain; charset=utf-8": "text/plain",
	} {
		if got := StoredType(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}
