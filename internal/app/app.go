// Package app wires configuration, storage and the HTTP server together for
// `tibi serve` and `tibi admin`.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/pflag"

	"tibi/internal/blob"
	"tibi/internal/config"
	"tibi/internal/server"
	"tibi/internal/store"
	"tibi/internal/units"
)

// Version is set at build time via -ldflags "-X tibi/internal/app.Version=...".
var Version = "0.0.2"

// Instance is an opened data directory.
type Instance struct {
	Cfg   config.Server
	Store *store.Store
	Blobs blob.Store
	Tmp   string
}

// Open prepares data_dir and opens the DB and blob store.
func Open(cfg config.Server) (*Instance, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return nil, err
	}
	tmp := filepath.Join(cfg.DataDir, "tmp")
	if err := os.MkdirAll(tmp, 0o750); err != nil {
		return nil, err
	}
	st, err := store.Open(filepath.Join(cfg.DataDir, "tibi.db"))
	if err != nil {
		return nil, err
	}
	bs, err := blob.NewFS(filepath.Join(cfg.DataDir, "blobs"))
	if err != nil {
		st.Close()
		return nil, err
	}
	return &Instance{Cfg: cfg, Store: st, Blobs: bs, Tmp: tmp}, nil
}

// Close releases resources.
func (in *Instance) Close() error { return in.Store.Close() }

func configFlag(fs *pflag.FlagSet) *string {
	def := os.Getenv("TIBI_CONFIG")
	if def == "" {
		def = config.DefaultPath
	}
	return fs.String("config", def, "server config file")
}

func loadConfig(path string) (config.Server, error) {
	// An explicit non-default path must exist.
	return config.Load(path, path != config.DefaultPath)
}

// Serve implements `tibi serve`.
func Serve(args []string, stderr io.Writer) error {
	fs := pflag.NewFlagSet("serve", pflag.ContinueOnError)
	cfgPath := configFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	log := slog.New(slog.NewJSONHandler(stderr, nil))
	in, err := Open(cfg)
	if err != nil {
		return err
	}
	defer in.Close()
	h, err := server.New(cfg, in.Store, in.Blobs, in.Tmp, log)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("listening", "addr", cfg.Listen, "base_url", cfg.BaseURL, "data_dir", cfg.DataDir)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	log.Info("stopped")
	return nil
}

const adminUsage = `usage:
  tibi admin token create --name N --scopes upload,delete,read_private [--quota 10GiB]
  tibi admin token list
  tibi admin token revoke N
`

// Admin implements `tibi admin`.
func Admin(args []string, stdout, stderr io.Writer) error {
	if len(args) < 2 || args[0] != "token" {
		fmt.Fprint(stderr, adminUsage)
		return errors.New("unknown admin command")
	}
	sub, rest := args[1], args[2:]
	fs := pflag.NewFlagSet("admin token "+sub, pflag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := configFlag(fs)
	name := fs.String("name", "", "token name")
	scopes := fs.String("scopes", "upload", "comma-separated scopes: upload,read_private,delete,admin")
	quota := fs.String("quota", "", "upload quota, e.g. 10GiB (default unlimited)")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	in, err := Open(cfg)
	if err != nil {
		return err
	}
	defer in.Close()
	ctx := context.Background()

	switch sub {
	case "create":
		sc, err := store.ParseScopes(*scopes)
		if err != nil {
			return err
		}
		var q *int64
		if *quota != "" {
			n, err := units.ParseBytes(*quota)
			if err != nil || n <= 0 {
				return fmt.Errorf("invalid --quota %q", *quota)
			}
			q = &n
		}
		secret, _, err := in.Store.CreateToken(ctx, *name, sc, q)
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, secret)
		fmt.Fprintln(stderr, "token created; it is shown only once — store it now")
		return nil
	case "list":
		toks, err := in.Store.ListTokens(ctx)
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tSCOPES\tQUOTA\tCREATED\tLAST USED\tSTATUS")
		for _, t := range toks {
			quota, last, status := "-", "-", "active"
			if t.QuotaBytes != nil {
				quota = units.FormatBytes(*t.QuotaBytes)
			}
			if t.LastUsedAt != nil {
				last = fmtTime(*t.LastUsedAt)
			}
			if t.RevokedAt != nil {
				status = "revoked " + fmtTime(*t.RevokedAt)
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", t.Name, strings.Join(t.Scopes, ","), quota, fmtTime(t.CreatedAt), last, status)
		}
		return tw.Flush()
	case "revoke":
		n := *name
		if n == "" && fs.NArg() == 1 {
			n = fs.Arg(0)
		}
		if n == "" {
			return errors.New("usage: tibi admin token revoke NAME")
		}
		if err := in.Store.RevokeToken(ctx, n); err != nil {
			return err
		}
		fmt.Fprintf(stderr, "revoked %s\n", n)
		return nil
	}
	fmt.Fprint(stderr, adminUsage)
	return fmt.Errorf("unknown token command %q", sub)
}

func fmtTime(unix int64) string { return time.Unix(unix, 0).UTC().Format("2006-01-02 15:04Z") }
