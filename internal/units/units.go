// Package units parses and formats byte sizes.
package units

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

var suffixes = map[string]int64{
	"": 1, "b": 1,
	"k": 1 << 10, "kb": 1 << 10, "kib": 1 << 10,
	"m": 1 << 20, "mb": 1 << 20, "mib": 1 << 20,
	"g": 1 << 30, "gb": 1 << 30, "gib": 1 << 30,
	"t": 1 << 40, "tb": 1 << 40, "tib": 1 << 40,
}

// ParseBytes parses sizes like "512", "10MiB", "1.5 GB" (binary multiples).
func ParseBytes(s string) (int64, error) {
	t := strings.ToLower(strings.TrimSpace(s))
	i := strings.IndexFunc(t, func(r rune) bool { return (r < '0' || r > '9') && r != '.' })
	num, unit := t, ""
	if i >= 0 {
		num, unit = t[:i], strings.TrimSpace(t[i:])
	}
	mult, ok := suffixes[unit]
	if !ok || num == "" {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	f, err := strconv.ParseFloat(num, 64)
	if err != nil || f < 0 {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	return int64(f * float64(mult)), nil
}

// FormatBytes formats n as e.g. "1.5 MiB".
func FormatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// ParseExpiry parses an expiry such as "10m", "1h", "7d", "2w", "30d" or
// "never". never is true for "never" (and ""); d is then zero.
func ParseExpiry(s string) (d time.Duration, never bool, err error) {
	t := strings.ToLower(strings.TrimSpace(s))
	if t == "" || t == "never" || t == "0" {
		return 0, true, nil
	}
	mult := time.Duration(0)
	switch {
	case strings.HasSuffix(t, "d"):
		mult, t = 24*time.Hour, strings.TrimSuffix(t, "d")
	case strings.HasSuffix(t, "w"):
		mult, t = 7*24*time.Hour, strings.TrimSuffix(t, "w")
	case strings.HasSuffix(t, "y"):
		mult, t = 365*24*time.Hour, strings.TrimSuffix(t, "y")
	}
	if mult != 0 {
		n, err := strconv.ParseFloat(t, 64)
		if err != nil || n <= 0 {
			return 0, false, fmt.Errorf("invalid expiry %q (examples: 10m, 1h, 7d, never)", s)
		}
		return time.Duration(n * float64(mult)), false, nil
	}
	d, err = time.ParseDuration(t)
	if err != nil || d <= 0 {
		return 0, false, fmt.Errorf("invalid expiry %q (examples: 10m, 1h, 7d, never)", s)
	}
	return d, false, nil
}
