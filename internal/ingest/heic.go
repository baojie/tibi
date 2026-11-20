package ingest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Converter turns HEIC/HEIF into JPEG with an external tool: no mature
// pure-Go HEIC decoder exists (PLAN A5).
type Converter struct {
	Name string // vips, heif-convert, magick, or a custom command path
	path string
}

// ConvertTimeout bounds one conversion.
const ConvertTimeout = 60 * time.Second

// DetectConverter resolves the heic_converter setting: "auto" tries vips,
// heif-convert and magick in that order; "off" disables conversion; a value
// containing "/" is a custom command run as `cmd INPUT OUTPUT.jpg`. It
// returns nil (and no error) when nothing usable is installed.
func DetectConverter(setting string) (*Converter, error) {
	switch setting {
	case "off", "":
		return nil, nil
	case "auto":
		for _, name := range []string{"vips", "heif-convert", "magick"} {
			if c, _ := DetectConverter(name); c != nil {
				return c, nil
			}
		}
		return nil, nil
	case "vips", "heif-convert", "magick":
		p, err := exec.LookPath(setting)
		if err != nil {
			return nil, fmt.Errorf("heic_converter %q not found in PATH", setting)
		}
		if setting == "magick" && !magickReadsHEIC(p) {
			return nil, errors.New("heic_converter magick: this ImageMagick cannot read HEIC (no libheif delegate)")
		}
		return &Converter{Name: setting, path: p}, nil
	}
	if strings.Contains(setting, "/") {
		if _, err := os.Stat(setting); err != nil {
			return nil, fmt.Errorf("heic_converter %q: %w", setting, err)
		}
		return &Converter{Name: setting, path: setting}, nil
	}
	return nil, fmt.Errorf("unknown heic_converter %q (auto, vips, heif-convert, magick, off, or a command path)", setting)
}

func magickReadsHEIC(path string) bool {
	out, err := exec.Command(path, "-list", "format").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && strings.TrimRight(f[0], "*") == "HEIC" && strings.HasPrefix(f[2], "r") {
			return true
		}
	}
	return false
}

// ToJPEG converts the HEIC file at in to JPEG bytes.
func (c *Converter) ToJPEG(ctx context.Context, in string, maxOut int64) ([]byte, error) {
	dir, err := os.MkdirTemp(filepath.Dir(in), "heic-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	src := filepath.Join(dir, "in.heic") // tools pick the decoder by extension
	if err := os.Link(in, src); err != nil {
		if err := copyFile(in, src); err != nil {
			return nil, err
		}
	}
	out := filepath.Join(dir, "out.jpg")
	ctx, cancel := context.WithTimeout(ctx, ConvertTimeout)
	defer cancel()
	// Name the input format instead of letting the tool sniff it: an
	// upload claiming to be HEIC must not reach some other decoder (SVG,
	// PostScript, …). Bound the tools' memory, and how many run at once.
	var args []string
	var env []string
	switch c.Name {
	case "vips":
		args = []string{"heifload", src, out + "[Q=90]"}
		env = []string{"VIPS_BLOCK_UNTRUSTED=1"}
	case "heif-convert":
		args = []string{"-q", "90", src, out}
	case "magick":
		// [0]: HEIC files can hold several images (bursts, previews);
		// without it magick writes out-0.jpg, out-1.jpg, ….
		args = []string{"-limit", "memory", "256MiB", "-limit", "map", "512MiB", "-limit", "area", "128MP",
			"-limit", "width", "32KP", "-limit", "height", "32KP", "heic:" + src + "[0]", "-quality", "90", out}
	default:
		args = []string{src, out}
	}
	convSlots <- struct{}{}
	defer func() { <-convSlots }()
	cmd := exec.CommandContext(ctx, c.path, args...)
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %v: %s", c.Name, err, strings.TrimSpace(stderr.String()))
	}
	fi, err := os.Stat(out)
	if err != nil {
		return nil, fmt.Errorf("%s produced no output", c.Name)
	}
	if fi.Size() > maxOut {
		return nil, fmt.Errorf("%s output too large (%d bytes)", c.Name, fi.Size())
	}
	b, err := os.ReadFile(out)
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(b, []byte{0xFF, 0xD8}) {
		return nil, fmt.Errorf("%s output is not a JPEG", c.Name)
	}
	return b, nil
}

// convSlots bounds concurrent external conversions (each can take hundreds
// of MB for a large photo).
var convSlots = make(chan struct{}, 2)

func copyFile(from, to string) error {
	b, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, b, 0o600)
}
