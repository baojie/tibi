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

	"tibi/internal/backup"
	"tibi/internal/blob"
	"tibi/internal/config"
	"tibi/internal/gc"
	"tibi/internal/ingest"
	"tibi/internal/migrate"
	"tibi/internal/server"
	"tibi/internal/store"
	"tibi/internal/units"
)

// Version is set at build time via -ldflags "-X tibi/internal/app.Version=...".
var Version = "0.0.16"

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
	bs, err := OpenBlobs(cfg, cfg.Storage.Backend)
	if err != nil {
		st.Close()
		return nil, err
	}
	return &Instance{Cfg: cfg, Store: st, Blobs: bs, Tmp: tmp}, nil
}

// OpenBlobs opens the named blob backend ("fs" or "s3") from cfg.
func OpenBlobs(cfg config.Server, backend string) (blob.Store, error) {
	switch backend {
	case "fs":
		return blob.NewFS(filepath.Join(cfg.DataDir, "blobs"))
	case "s3":
		s := cfg.Storage.S3
		return blob.NewS3(blob.S3Config{
			Endpoint: s.Endpoint, Region: s.Region, Bucket: s.Bucket, Prefix: s.Prefix,
			AccessKey: s.AccessKey, SecretKey: s.SecretKey, UsePathStyle: s.UsePathStyle,
		})
	}
	return nil, fmt.Errorf("unknown storage backend %q", backend)
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
	if cfg.Storage.Backend == "s3" {
		pctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := blob.Probe(pctx, in.Blobs)
		cancel()
		if err != nil {
			return fmt.Errorf("%w (storage.s3: endpoint %s, bucket %s)", err, cfg.Storage.S3.Endpoint, cfg.Storage.S3.Bucket)
		}
	}
	if n, err := ingest.Backfill(context.Background(), in.Store, in.Blobs); err != nil {
		return fmt.Errorf("search index backfill: %w", err)
	} else if n > 0 {
		log.Info("indexed existing items for search", "items", n)
	}
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
	go gc.Loop(ctx, in.Store, in.Blobs, gc.Interval, log)
	go func() { // abandoned resumable uploads
		t := time.NewTicker(gc.Interval)
		defer t.Stop()
		for {
			if n, err := h.CleanupUploads(ctx); err != nil && ctx.Err() == nil {
				log.Error("upload cleanup", "err", err)
			} else if n > 0 {
				log.Info("removed abandoned uploads", "uploads", n)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	usageDone := make(chan struct{})
	go func() { h.UsageLoop(ctx); close(usageDone) }()
	defer func() { stop(); <-usageDone }() // final flush before the DB closes
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("listening", "addr", cfg.Listen, "base_url", cfg.BaseURL, "data_dir", cfg.DataDir,
		"storage", cfg.Storage.Backend, "blob_delivery", cfg.BlobDelivery)
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
  tibi admin gc [--orphans]     remove expired/deleted items and unused blobs now;
                                --orphans also scans storage for unreferenced blobs
  tibi admin stats              storage, items, traffic, most-served items
  tibi admin export --out FILE.tar.zst
                                all live items, collections, tokens and blobs
  tibi admin import FILE.tar.zst
                                restore an export into an empty instance
  tibi admin blobs migrate --to s3|fs [--workers 8]
                                copy all blobs to the other backend (verified,
                                resumable); then switch storage.backend
`

// Admin implements `tibi admin`.
func Admin(args []string, stdout, stderr io.Writer) error {
	if len(args) >= 1 && args[0] == "gc" {
		return adminGC(args[1:], stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "blobs" && args[1] == "migrate" {
		return adminMigrate(args[2:], stdout, stderr)
	}
	if len(args) >= 1 {
		switch args[0] {
		case "stats":
			return adminStats(args[1:], stdout, stderr)
		case "export":
			return adminExport(args[1:], stdout, stderr)
		case "import":
			return adminImport(args[1:], stdout, stderr)
		}
	}
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

func adminGC(args []string, stdout, stderr io.Writer) error {
	fs := pflag.NewFlagSet("admin gc", pflag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := configFlag(fs)
	orphans := fs.Bool("orphans", false, "also delete stored blobs no item references (older than 1h)")
	if err := fs.Parse(args); err != nil {
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
	res, err := gc.Run(ctx, in.Store, in.Blobs)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "removed %d items, %d blobs\n", res.Items, res.Blobs)
	if *orphans {
		n, err := gc.Orphans(ctx, in.Store, in.Blobs, gc.OrphanMinAge)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "removed %d orphaned blobs\n", n)
	}
	return nil
}

func adminMigrate(args []string, stdout, stderr io.Writer) error {
	fs := pflag.NewFlagSet("admin blobs migrate", pflag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := configFlag(fs)
	to := fs.String("to", "", "destination backend: s3 or fs")
	workers := fs.Int("workers", 8, "parallel copies")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	from := cfg.Storage.Backend
	switch {
	case *to != "fs" && *to != "s3":
		return errors.New("--to must be s3 or fs")
	case *to == from:
		return fmt.Errorf("storage.backend is already %q; migrate copies from the configured backend to the other one", from)
	}
	if *to == "s3" {
		probe := cfg
		probe.Storage.Backend = "s3"
		if err := probe.Validate(); err != nil {
			return err
		}
	}
	in, err := Open(cfg)
	if err != nil {
		return err
	}
	defer in.Close()
	dst, err := OpenBlobs(cfg, *to)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := blob.Probe(ctx, dst); err != nil {
		return err
	}
	fmt.Fprintf(stderr, "copying blobs %s → %s …\n", from, *to)
	var last time.Time
	res, err := migrate.Run(ctx, in.Store, in.Blobs, dst, *workers, func(r migrate.Result) {
		if time.Since(last) > time.Second {
			last = time.Now()
			fmt.Fprintf(stderr, "\r%d/%d done", r.Copied+r.Skipped+r.Missing+r.Failed, r.Total)
		}
	})
	fmt.Fprintf(stderr, "\r")
	fmt.Fprintf(stdout, "blobs: %d total, %d copied, %d already present, %d missing at source, %d failed\n",
		res.Total, res.Copied, res.Skipped, res.Missing, res.Failed)
	if err != nil {
		return fmt.Errorf("migration incomplete (re-run to resume): %w", err)
	}
	fmt.Fprintf(stdout, "done. Now set storage.backend = %q in the config and restart the server.\n", *to)
	return nil
}

// openForAdmin parses --config (plus extra flags) and opens the instance.
func openForAdmin(name string, args []string, stderr io.Writer, extra func(*pflag.FlagSet)) (*Instance, *pflag.FlagSet, error) {
	fs := pflag.NewFlagSet(name, pflag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := configFlag(fs)
	if extra != nil {
		extra(fs)
	}
	if err := fs.Parse(args); err != nil {
		return nil, nil, err
	}
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		return nil, nil, err
	}
	in, err := Open(cfg)
	return in, fs, err
}

func adminStats(args []string, stdout, stderr io.Writer) error {
	in, _, err := openForAdmin("admin stats", args, stderr, nil)
	if err != nil {
		return err
	}
	defer in.Close()
	ctx := context.Background()
	st, err := in.Store.Stats(ctx, 10)
	if err != nil {
		return err
	}
	_, today, err := in.Store.DailyUsage(ctx, time.Now().UTC().Format("2006-01-02"))
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "items:\t%d\n", st.Items)
	for _, k := range []string{"image", "video", "audio", "text", "file", "encrypted"} {
		if n := st.ItemsByKind[k]; n > 0 {
			fmt.Fprintf(tw, "  %s:\t%d\n", k, n)
		}
	}
	fmt.Fprintf(tw, "collections:\t%d\n", st.Collections)
	fmt.Fprintf(tw, "tokens:\t%d\n", st.Tokens)
	fmt.Fprintf(tw, "storage used:\t%s (items total %s)\n", units.FormatBytes(st.StorageBytes), units.FormatBytes(st.LogicalBytes))
	fmt.Fprintf(tw, "served today:\t%s (UTC day; up to 10s behind a running server)\n", units.FormatBytes(today))
	fmt.Fprintf(tw, "served total:\t%s\n", units.FormatBytes(st.BytesServed))
	tw.Flush()
	if len(st.Top) > 0 {
		fmt.Fprintln(stdout, "\nmost served:")
		tw = tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		for _, it := range st.Top {
			name := it.Title
			if name == "" {
				name = it.Filename
			}
			fmt.Fprintf(tw, "  %s/%s\t%s\t%d views\t%s\n", in.Cfg.BaseURL, it.ID, units.FormatBytes(it.BytesServed), it.ViewCount, name)
		}
		tw.Flush()
	}
	return nil
}

func adminExport(args []string, stdout, stderr io.Writer) error {
	var out string
	in, _, err := openForAdmin("admin export", args, stderr, func(fs *pflag.FlagSet) {
		fs.StringVar(&out, "out", "", "archive to write (.tar.zst)")
	})
	if err != nil {
		return err
	}
	defer in.Close()
	if out == "" {
		return errors.New("usage: tibi admin export --out FILE.tar.zst")
	}
	f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	m, err := backup.Export(context.Background(), in.Store, in.Blobs, in.Cfg.SecretKeyPath(), Version, f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(out)
		return err
	}
	fmt.Fprintf(stdout, "exported %d items, %d collections, %d tokens, %d blobs to %s\n", m.Items, m.Collections, m.Tokens, m.Blobs, out)
	fmt.Fprintln(stderr, "the archive holds private items, token hashes and the share-link key: keep it safe")
	return nil
}

func adminImport(args []string, stdout, stderr io.Writer) error {
	in, fs, err := openForAdmin("admin import", args, stderr, nil)
	if err != nil {
		return err
	}
	defer in.Close()
	if fs.NArg() != 1 {
		return errors.New("usage: tibi admin import FILE.tar.zst")
	}
	f, err := os.Open(fs.Arg(0))
	if err != nil {
		return err
	}
	defer f.Close()
	res, err := backup.Import(context.Background(), in.Store, in.Blobs, in.Cfg.SecretKeyPath(), in.Tmp, f)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "imported %d items, %d collections, %d tokens (%d already present), %d blobs\n",
		res.Items, res.Collections, res.Tokens-res.TokensKept, res.TokensKept, res.Blobs)
	fmt.Fprintln(stderr, "restart the server so it picks up the imported share-link key")
	return nil
}
