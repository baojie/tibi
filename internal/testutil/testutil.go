// Package testutil starts an in-process tibi server for tests.
package testutil

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"tibi/internal/blob"
	"time"

	"tibi/internal/app"
	"tibi/internal/config"
	"tibi/internal/gc"
	"tibi/internal/server"
	"tibi/internal/store"
	"tibi/internal/testutil/s3test"
)

// Env is a running test server.
type Env struct {
	T        *testing.T
	Srv      *httptest.Server
	Instance *app.Instance
	Cfg      config.Server
	offset   atomic.Int64 // clock offset in nanoseconds
}

// Advance moves the server's clock forward.
func (e *Env) Advance(d time.Duration) { e.offset.Add(int64(d)) }

// GC runs one garbage-collection pass.
func (e *Env) GC() gc.Result {
	e.T.Helper()
	res, err := gc.Run(context.Background(), e.Instance.Store, e.Instance.Blobs)
	if err != nil {
		e.T.Fatal(err)
	}
	return res
}

// BlobCount counts stored blobs.
func (e *Env) BlobCount() int {
	n := 0
	e.Instance.Blobs.List(context.Background(), "", func(blob.Info) error { n++; return nil })
	return n
}

// New starts a server with a temp data dir. mod may adjust the config.
func New(t *testing.T, mod func(*config.Server)) *Env {
	t.Helper()
	cfg := config.Default()
	cfg.DataDir = filepath.Join(t.TempDir(), "data")
	srv := httptest.NewUnstartedServer(nil)
	cfg.BaseURL = "http://" + srv.Listener.Addr().String()
	if s3test.Enabled() {
		// The whole suite runs on S3 when it is configured (PLAN 0.0.5).
		UseS3(t, &cfg)
	}
	t.Logf("blob backend: %s", cfg.Storage.Backend)
	if mod != nil {
		mod(&cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	in, err := app.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e := &Env{T: t, Instance: in, Cfg: cfg}
	in.Store.Now = func() time.Time { return time.Now().Add(time.Duration(e.offset.Load())) }
	h, err := server.New(cfg, in.Store, in.Blobs, in.Tmp, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	srv.Config.Handler = h
	srv.Start()
	t.Cleanup(func() {
		srv.Close()
		in.Close()
	})
	e.Srv = srv
	return e
}

// UseS3 points cfg at the test S3 bucket under a fresh prefix (skipping the
// test if S3 is not configured).
func UseS3(t testing.TB, cfg *config.Server) {
	sc := s3test.Config(t)
	cfg.Storage.Backend = "s3"
	cfg.Storage.S3 = config.S3{
		Endpoint: sc.Endpoint, Region: sc.Region, Bucket: sc.Bucket, Prefix: sc.Prefix,
		AccessKey: sc.AccessKey, SecretKey: sc.SecretKey, UsePathStyle: sc.UsePathStyle,
	}
}

// Backend names the blob backend in use, for test logs.
func (e *Env) Backend() string { return e.Cfg.Storage.Backend }

// Token creates a token with the given scopes and returns its secret.
func (e *Env) Token(name string, scopes ...string) string {
	e.T.Helper()
	if len(scopes) == 0 {
		scopes = []string{store.ScopeUpload}
	}
	secret, _, err := e.Instance.Store.CreateToken(context.Background(), name, scopes, nil)
	if err != nil {
		e.T.Fatal(err)
	}
	return secret
}
