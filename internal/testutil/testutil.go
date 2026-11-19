// Package testutil starts an in-process tibi server for tests.
package testutil

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"tibi/internal/app"
	"tibi/internal/config"
	"tibi/internal/server"
	"tibi/internal/store"
)

// Env is a running test server.
type Env struct {
	T        *testing.T
	Srv      *httptest.Server
	Instance *app.Instance
	Cfg      config.Server
}

// New starts a server with a temp data dir. mod may adjust the config.
func New(t *testing.T, mod func(*config.Server)) *Env {
	t.Helper()
	cfg := config.Default()
	cfg.DataDir = filepath.Join(t.TempDir(), "data")
	srv := httptest.NewUnstartedServer(nil)
	cfg.BaseURL = "http://" + srv.Listener.Addr().String()
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
	return &Env{T: t, Srv: srv, Instance: in, Cfg: cfg}
}

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
