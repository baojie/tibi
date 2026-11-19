package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config is the client configuration (~/.config/tibi/config.toml).
type Config struct {
	Server         string `toml:"server"`
	Token          string `toml:"token"`
	Copy           *bool  `toml:"copy,omitempty"`
	DefaultExpires string `toml:"default_expires,omitempty"`
}

func (a *App) configPath() (string, error) {
	if p := a.getenv("TIBI_CONFIG"); p != "" {
		return p, nil
	}
	dir := a.getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home := a.getenv("HOME")
		if home == "" {
			var err error
			if home, err = os.UserHomeDir(); err != nil {
				return "", err
			}
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "tibi", "config.toml"), nil
}

// loadConfig reads the config file (if any) and applies env overrides.
func (a *App) loadConfig() (Config, error) {
	var c Config
	p, err := a.configPath()
	if err != nil {
		return c, err
	}
	if _, err := toml.DecodeFile(p, &c); err != nil && !errors.Is(err, os.ErrNotExist) {
		return c, err
	}
	if v := a.getenv("TIBI_SERVER"); v != "" {
		c.Server = v
	}
	if v := a.getenv("TIBI_TOKEN"); v != "" {
		c.Token = v
	}
	return c, nil
}

func (a *App) saveConfig(c Config) (string, error) {
	p, err := a.configPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(c); err != nil {
		return "", err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o600); err != nil {
		return "", err
	}
	return p, os.Rename(tmp, p)
}
