// Package config loads the server configuration from a TOML file with
// TIBI_* environment variable overrides.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Server is the tibi server configuration.
type Server struct {
	Listen            string   `toml:"listen"`
	BaseURL           string   `toml:"base_url"`
	RawBaseURL        string   `toml:"raw_base_url"`
	DataDir           string   `toml:"data_dir"`
	DefaultExpires    string   `toml:"default_expires"`
	MaxUploadBytes    int64    `toml:"max_upload_bytes"`
	MaxResumableBytes int64    `toml:"max_resumable_bytes"`
	MaxTextBytes      int64    `toml:"max_text_bytes"`
	StripImageMeta    bool     `toml:"strip_image_meta"`
	ConvertHEIC       bool     `toml:"convert_heic"`
	HEICConverter     string   `toml:"heic_converter"`
	BlobDelivery      string   `toml:"blob_delivery"`
	CORSOrigins       []string `toml:"cors_origins"`
	AllowedReferers   []string `toml:"allowed_referers"`
	MaxItemBytesDay   int64    `toml:"max_item_bytes_per_day"`
	MaxTotalBytesDay  int64    `toml:"max_total_bytes_per_day"`
	DefaultLocale     string   `toml:"default_locale"`
	SecretKeyFile     string   `toml:"secret_key_file"`
	MetricsAllowLocal bool     `toml:"metrics_allow_local"`
	RateLimit         int64    `toml:"rate_limit"` // API requests per minute per token; 0 = off
	RateBurst         int64    `toml:"rate_burst"`
	Storage           Storage  `toml:"storage"`
}

// Storage selects and configures the blob backend.
type Storage struct {
	Backend string `toml:"backend"`
	S3      S3     `toml:"s3"`
}

// S3 configures an S3-compatible backend.
type S3 struct {
	Endpoint     string `toml:"endpoint"`
	Region       string `toml:"region"`
	Bucket       string `toml:"bucket"`
	Prefix       string `toml:"prefix"`
	AccessKey    string `toml:"access_key"`
	SecretKey    string `toml:"secret_key"`
	UsePathStyle bool   `toml:"use_path_style"`
}

// Default returns the built-in defaults.
func Default() Server {
	return Server{
		Listen:            "127.0.0.1:8070",
		BaseURL:           "http://127.0.0.1:8070",
		DataDir:           "/var/lib/tibi",
		DefaultExpires:    "never",
		MaxUploadBytes:    50 << 20,
		MaxResumableBytes: 2 << 30,
		MaxTextBytes:      2 << 20,
		StripImageMeta:    true,
		ConvertHEIC:       true,
		HEICConverter:     "auto",
		BlobDelivery:      "proxy",
		DefaultLocale:     "en",
		RateLimit:         1200,
		RateBurst:         200,
		Storage:           Storage{Backend: "fs"},
	}
}

// DefaultPath is used when no --config is given and TIBI_CONFIG is unset.
const DefaultPath = "/etc/tibi/server.toml"

// Load reads path (if it exists; a missing file is not an error when
// mustExist is false), then applies environment overrides and validates.
func Load(path string, mustExist bool) (Server, error) {
	c := Default()
	if path != "" {
		_, err := os.Stat(path)
		switch {
		case err == nil:
			if _, err := toml.DecodeFile(path, &c); err != nil {
				return c, fmt.Errorf("config %s: %w", path, err)
			}
			if err := checkSecretsPerm(path, &c); err != nil {
				return c, err
			}
		case os.IsNotExist(err) && !mustExist:
		default:
			return c, fmt.Errorf("config %s: %w", path, err)
		}
	}
	if err := applyEnv(&c, os.Getenv); err != nil {
		return c, err
	}
	return c, c.Validate()
}

// Validate checks values and normalizes them.
func (c *Server) Validate() error {
	c.BaseURL = strings.TrimRight(c.BaseURL, "/")
	c.RawBaseURL = strings.TrimRight(c.RawBaseURL, "/")
	if c.BaseURL == "" {
		return fmt.Errorf("config: base_url is required")
	}
	if c.DataDir == "" {
		return fmt.Errorf("config: data_dir is required")
	}
	if abs, err := filepath.Abs(c.DataDir); err == nil {
		c.DataDir = abs
	}
	if c.MaxUploadBytes <= 0 {
		return fmt.Errorf("config: max_upload_bytes must be > 0")
	}
	switch c.Storage.Backend {
	case "fs":
	case "s3":
		s3 := c.Storage.S3
		if s3.Endpoint == "" || s3.Bucket == "" {
			return fmt.Errorf("config: storage.s3 needs endpoint and bucket")
		}
		if s3.AccessKey == "" || s3.SecretKey == "" {
			return fmt.Errorf("config: storage.s3 needs access_key and secret_key (prefer env TIBI_S3_ACCESS_KEY / TIBI_S3_SECRET_KEY)")
		}
	default:
		return fmt.Errorf("config: unknown storage backend %q (fs or s3)", c.Storage.Backend)
	}
	switch c.HEICConverter {
	case "auto", "vips", "heif-convert", "magick", "off":
	default:
		if !strings.Contains(c.HEICConverter, "/") {
			return fmt.Errorf("config: heic_converter must be auto, vips, heif-convert, magick, off or a command path")
		}
	}
	switch c.BlobDelivery {
	case "proxy", "redirect":
	default:
		return fmt.Errorf("config: blob_delivery must be proxy or redirect")
	}
	return nil
}

// checkSecretsPerm refuses a config file that holds S3 credentials while
// being readable by other users (PLAN A8.11).
func checkSecretsPerm(path string, c *Server) error {
	if c.Storage.S3.AccessKey == "" && c.Storage.S3.SecretKey == "" {
		return nil
	}
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if fi.Mode().Perm()&0o004 != 0 {
		return fmt.Errorf("config %s holds S3 credentials but is world-readable; run: chmod 600 %s (or move the keys to TIBI_S3_ACCESS_KEY / TIBI_S3_SECRET_KEY)", path, path)
	}
	return nil
}

// SecretKeyPath is the share-link signing key file (created on first start).
func (c *Server) SecretKeyPath() string {
	if c.SecretKeyFile != "" {
		return c.SecretKeyFile
	}
	return filepath.Join(c.DataDir, "secret.key")
}

// RawBase is the origin used for raw URLs.
func (c *Server) RawBase() string {
	if c.RawBaseURL != "" {
		return c.RawBaseURL
	}
	return c.BaseURL
}

func applyEnv(c *Server, getenv func(string) string) error {
	str := map[string]*string{
		"TIBI_LISTEN":          &c.Listen,
		"TIBI_BASE_URL":        &c.BaseURL,
		"TIBI_RAW_BASE_URL":    &c.RawBaseURL,
		"TIBI_DATA_DIR":        &c.DataDir,
		"TIBI_DEFAULT_EXPIRES": &c.DefaultExpires,
		"TIBI_STORAGE_BACKEND": &c.Storage.Backend,
		"TIBI_S3_ENDPOINT":     &c.Storage.S3.Endpoint,
		"TIBI_S3_REGION":       &c.Storage.S3.Region,
		"TIBI_S3_BUCKET":       &c.Storage.S3.Bucket,
		"TIBI_S3_PREFIX":       &c.Storage.S3.Prefix,
		"TIBI_S3_ACCESS_KEY":   &c.Storage.S3.AccessKey,
		"TIBI_S3_SECRET_KEY":   &c.Storage.S3.SecretKey,
		"TIBI_DEFAULT_LOCALE":  &c.DefaultLocale,
		"TIBI_BLOB_DELIVERY":   &c.BlobDelivery,
		"TIBI_HEIC_CONVERTER":  &c.HEICConverter,
		"TIBI_SECRET_KEY_FILE": &c.SecretKeyFile,
	}
	for k, p := range str {
		if v, ok := lookup(getenv, k); ok {
			*p = v
		}
	}
	if v, ok := lookup(getenv, "TIBI_METRICS_ALLOW_LOCAL"); ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("TIBI_METRICS_ALLOW_LOCAL: %w", err)
		}
		c.MetricsAllowLocal = b
	}
	if v, ok := lookup(getenv, "TIBI_CORS_ORIGINS"); ok {
		c.CORSOrigins = strings.Fields(strings.ReplaceAll(v, ",", " "))
	}
	if v, ok := lookup(getenv, "TIBI_ALLOWED_REFERERS"); ok {
		c.AllowedReferers = strings.Fields(strings.ReplaceAll(v, ",", " "))
	}
	ints := map[string]*int64{
		"TIBI_RATE_LIMIT":          &c.RateLimit,
		"TIBI_RATE_BURST":          &c.RateBurst,
		"TIBI_MAX_UPLOAD_BYTES":    &c.MaxUploadBytes,
		"TIBI_MAX_RESUMABLE_BYTES": &c.MaxResumableBytes,
		"TIBI_MAX_TEXT_BYTES":      &c.MaxTextBytes,
	}
	for k, p := range ints {
		if v, ok := lookup(getenv, k); ok {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return fmt.Errorf("env %s: %w", k, err)
			}
			*p = n
		}
	}
	return nil
}

func lookup(getenv func(string) string, k string) (string, bool) {
	v := getenv(k)
	return v, v != ""
}
