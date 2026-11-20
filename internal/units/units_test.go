package units

import (
	"testing"
	"time"
)

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

func TestParseExpiry(t *testing.T) {
	cases := map[string]time.Duration{"1s": time.Second, "10m": 10 * time.Minute, "1h": time.Hour, "7d": 7 * 24 * time.Hour, "2w": 14 * 24 * time.Hour, "1.5h": 90 * time.Minute}
	for in, want := range cases {
		d, never, err := ParseExpiry(in)
		if err != nil || never || d != want {
			t.Errorf("ParseExpiry(%q) = %v %v %v", in, d, never, err)
		}
	}
	for _, in := range []string{"never", "", "NEVER"} {
		if _, never, err := ParseExpiry(in); !never || err != nil {
			t.Errorf("ParseExpiry(%q) not never", in)
		}
	}
	for _, in := range []string{"soon", "-1h", "0d", "1x", "1e300d", "infd", "nand", "101y", "1000000h"} {
		if _, _, err := ParseExpiry(in); err == nil {
			t.Errorf("ParseExpiry(%q) accepted", in)
		}
	}
}
