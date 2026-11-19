package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// Clipboard reads and writes the system clipboard.
type Clipboard interface {
	// Types lists the MIME types currently offered.
	Types() ([]string, error)
	// Read returns the clipboard content as type mt.
	Read(mt string) ([]byte, error)
	// WriteText puts text on the clipboard.
	WriteText(s string) error
}

// SystemClipboard uses wl-paste/wl-copy (Wayland), xclip/xsel (X11) or
// pbpaste/pbcopy (macOS).
type SystemClipboard struct {
	Getenv func(string) string
}

type clipTool int

const (
	toolNone clipTool = iota
	toolWayland
	toolXclip
	toolXsel
	toolMac
)

func have(name string) bool { _, err := exec.LookPath(name); return err == nil }

func (c SystemClipboard) tool() (clipTool, error) {
	if runtime.GOOS == "darwin" {
		return toolMac, nil
	}
	if c.Getenv("WAYLAND_DISPLAY") != "" {
		if have("wl-paste") && have("wl-copy") {
			return toolWayland, nil
		}
		if c.Getenv("DISPLAY") == "" {
			return toolNone, errors.New("clipboard: install wl-clipboard (wl-paste/wl-copy)")
		}
	}
	if c.Getenv("DISPLAY") != "" {
		if have("xclip") {
			return toolXclip, nil
		}
		if have("xsel") {
			return toolXsel, nil
		}
		return toolNone, errors.New("clipboard: install xclip (or xsel)")
	}
	return toolNone, errors.New("clipboard: no graphical session (WAYLAND_DISPLAY/DISPLAY unset)")
}

func run(name string, args ...string) ([]byte, error) {
	var out, errb bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s: %s", name, msg)
	}
	return out.Bytes(), nil
}

// Types implements Clipboard.
func (c SystemClipboard) Types() ([]string, error) {
	t, err := c.tool()
	if err != nil {
		return nil, err
	}
	var out []byte
	switch t {
	case toolWayland:
		out, err = run("wl-paste", "--list-types")
	case toolXclip:
		out, err = run("xclip", "-selection", "clipboard", "-t", "TARGETS", "-o")
	case toolXsel:
		return []string{"text/plain"}, nil
	case toolMac:
		types := []string{"text/plain"}
		if have("pngpaste") {
			types = append([]string{"image/png"}, types...)
		}
		return types, nil
	}
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(out)), nil
}

// Read implements Clipboard.
func (c SystemClipboard) Read(mt string) ([]byte, error) {
	t, err := c.tool()
	if err != nil {
		return nil, err
	}
	text := strings.HasPrefix(mt, "text/") || mt == "UTF8_STRING" || mt == "STRING"
	switch t {
	case toolWayland:
		args := []string{"--type", mt}
		if text {
			args = append(args, "--no-newline")
		}
		return run("wl-paste", args...)
	case toolXclip:
		return run("xclip", "-selection", "clipboard", "-t", mt, "-o")
	case toolXsel:
		return run("xsel", "--clipboard", "--output")
	case toolMac:
		if mt == "image/png" {
			return run("pngpaste", "-")
		}
		return run("pbpaste")
	}
	return nil, errors.New("clipboard unavailable")
}

// WriteText implements Clipboard.
func (c SystemClipboard) WriteText(s string) error {
	t, err := c.tool()
	if err != nil {
		return err
	}
	var cmd *exec.Cmd
	switch t {
	case toolWayland:
		cmd = exec.Command("wl-copy")
	case toolXclip:
		cmd = exec.Command("xclip", "-selection", "clipboard", "-i")
	case toolXsel:
		cmd = exec.Command("xsel", "--clipboard", "--input")
	case toolMac:
		cmd = exec.Command("pbcopy")
	}
	cmd.Stdin = strings.NewReader(s)
	return cmd.Run()
}

// pickClipType chooses what `tibi clip` uploads: image/png first, then any
// image, then text.
func pickClipType(types []string) (mt, filename string, ok bool) {
	has := map[string]bool{}
	for _, t := range types {
		has[t] = true
	}
	if has["image/png"] {
		return "image/png", "clipboard.png", true
	}
	for _, t := range types {
		if ext, ok := clipImageExt[t]; ok {
			return t, "clipboard." + ext, true
		}
	}
	for _, t := range []string{"text/plain;charset=utf-8", "UTF8_STRING", "text/plain", "STRING", "TEXT"} {
		if has[t] {
			return t, "clipboard.txt", true
		}
	}
	return "", "", false
}

var clipImageExt = map[string]string{"image/jpeg": "jpg", "image/webp": "webp", "image/gif": "gif", "image/avif": "avif"}
