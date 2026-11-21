package mcp

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PathPolicy decides which local files upload_file may publish
// (docs/design/2026-11-21-security-hardening.md §1). An agent that has been
// prompt-injected must not be able to turn "upload ~/.ssh/id_ed25519" into
// a public link.
type PathPolicy struct {
	// WorkDir is the directory relative paths resolve against. It is an
	// allowed root unless it is Home or "/".
	WorkDir string
	// Home is the user's home directory (for ~ and the deny list).
	Home string
	// TempDir is allowed too ("" for none).
	TempDir string
	// AllowDirs are extra allowed roots (tibi mcp --allow-dir).
	AllowDirs []string
}

// DefaultPathPolicy uses the process's working directory, home and temp
// directory.
func DefaultPathPolicy(allowDirs []string) PathPolicy {
	wd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	return PathPolicy{WorkDir: wd, Home: home, TempDir: os.TempDir(), AllowDirs: allowDirs}
}

// deniedHomeDirs are never uploaded from, whatever the roots say.
var deniedHomeDirs = []string{
	".ssh", ".gnupg", ".aws", ".azure", ".kube", ".docker", ".config",
	".password-store", ".local/share/keyrings", ".mozilla", ".claude",
}

// deniedNames are glob patterns (matched case-insensitively against the
// file name) of files that usually hold secrets.
var deniedNames = []string{
	"id_rsa*", "id_dsa*", "id_ecdsa*", "id_ed25519*",
	"*.pem", "*.key", "*.p12", "*.pfx", "*.jks", "*.keystore", "*.kdbx",
	"*.ovpn", "*.tfstate", "credentials*", "secret*",
}

// errPolicy marks refusals, so callers can tell them from I/O errors.
var errPolicy = errors.New("refused")

// Resolve expands ~, makes p absolute and resolves symlinks, then checks
// the result against the policy. It returns the real path to open.
func (pp PathPolicy) Resolve(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if pp.Home == "" {
			return "", fmt.Errorf("cannot expand ~: no home directory")
		}
		p = filepath.Join(pp.Home, strings.TrimPrefix(p, "~"))
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(pp.WorkDir, p)
	}
	// Check the path as written first, so refusals do not depend on
	// whether the file exists (no probing for ~/.ssh/id_rsa); then again
	// after symlinks are resolved.
	if err := pp.check(filepath.Clean(p)); err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(filepath.Clean(p))
	if err != nil {
		return "", err // does not exist, or unreadable: reported as "cannot open"
	}
	if real, err = filepath.Abs(real); err != nil {
		return "", err
	}
	if err := pp.check(real); err != nil {
		return "", err
	}
	return real, nil
}

func (pp PathPolicy) check(real string) error {
	home := resolveDir(pp.Home)
	if home != "" {
		for _, d := range deniedHomeDirs {
			if within(real, filepath.Join(home, d)) {
				return fmt.Errorf("%w: files under ~/%s are never uploaded by agents", errPolicy, d)
			}
		}
	}
	name := strings.ToLower(filepath.Base(real))
	for _, pat := range deniedNames {
		if ok, _ := filepath.Match(pat, name); ok {
			return fmt.Errorf("%w: %s looks like a key or credentials file (%s), which agents may not upload", errPolicy, filepath.Base(real), pat)
		}
	}
	root := pp.rootOf(real)
	if root == "" {
		return fmt.Errorf("%w: %s is outside the directories agents may upload from (%s); "+
			"the user can allow more with `tibi mcp --allow-dir DIR`", errPolicy, real, strings.Join(pp.roots(), ", "))
	}
	rel, _ := filepath.Rel(root, real)
	for _, c := range strings.Split(rel, string(filepath.Separator)) {
		if strings.HasPrefix(c, ".") && c != "." && c != ".." {
			return fmt.Errorf("%w: %s is in or is a hidden file or directory (%s), which agents may not upload", errPolicy, real, c)
		}
	}
	return nil
}

// roots returns the allowed roots, resolved through symlinks.
func (pp PathPolicy) roots() []string {
	var out []string
	add := func(d string) {
		if d = resolveDir(d); d != "" {
			out = append(out, d)
		}
	}
	if wd := resolveDir(pp.WorkDir); wd != "" && wd != resolveDir(pp.Home) && wd != string(filepath.Separator) {
		out = append(out, wd)
	}
	add(pp.TempDir)
	for _, d := range pp.AllowDirs {
		add(d)
	}
	return out
}

// rootOf returns the most specific allowed root containing real, or "".
func (pp PathPolicy) rootOf(real string) string {
	best := ""
	for _, r := range pp.roots() {
		if within(real, r) && len(r) > len(best) {
			best = r
		}
	}
	return best
}

func resolveDir(d string) string {
	if d == "" {
		return ""
	}
	if r, err := filepath.EvalSymlinks(d); err == nil {
		return r
	}
	return filepath.Clean(d)
}

// within reports whether p is dir or inside it.
func within(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
