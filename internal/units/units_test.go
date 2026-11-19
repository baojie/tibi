package units

import "testing"

func TestParseBytes(t *testing.T) {
	cases := map[string]int64{"512": 512, "1k": 1024, "10MiB": 10 << 20, "1.5 GB": 3 << 29, "2TiB": 2 << 40}
	for in, want := range cases {
		got, err := ParseBytes(in)
		if err != nil || got != want {
			t.Errorf("ParseBytes(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "MiB", "-1", "10XB"} {
		if _, err := ParseBytes(bad); err == nil {
			t.Errorf("ParseBytes(%q) succeeded", bad)
		}
	}
	if s := FormatBytes(1536); s != "1.5 KiB" {
		t.Errorf("FormatBytes = %q", s)
	}
}
