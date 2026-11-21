// Command tibi is the tibi server, admin tool and client CLI in one binary.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/baojie/tibi/internal/app"
	"github.com/baojie/tibi/internal/cli"
	"github.com/baojie/tibi/internal/mcp"
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "serve":
			exit(app.Serve(args[1:], os.Stderr))
		case "admin":
			exit(app.Admin(args[1:], os.Stdout, os.Stderr))
		case "version", "--version":
			fmt.Println("tibi", app.Version)
			return
		}
	}
	a := &cli.App{
		Stdin:       os.Stdin,
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
		StdinIsTTY:  term.IsTerminal(int(os.Stdin.Fd())),
		StdoutIsTTY: term.IsTerminal(int(os.Stdout.Fd())),
		StderrIsTTY: term.IsTerminal(int(os.Stderr.Fd())),
		// Secrets are read from the terminal even when stdin is a pipe
		// (e.g. `cat secret.txt | tibi paste --encrypt --password`).
		ReadSecret: func() (string, error) {
			tty, err := os.Open("/dev/tty")
			if err != nil {
				b, err := term.ReadPassword(int(os.Stdin.Fd()))
				return string(b), err
			}
			defer tty.Close()
			b, err := term.ReadPassword(int(tty.Fd()))
			return string(b), err
		},
	}
	if len(args) > 0 && args[0] == "mcp" {
		fs := flag.NewFlagSet("tibi mcp", flag.ExitOnError)
		var allow multiFlag
		fs.Var(&allow, "allow-dir", "also let agents upload files under `DIR` (repeatable; also TIBI_MCP_ALLOW_DIRS)")
		allowDelete := fs.Bool("allow-delete", false, "offer the delete_item tool")
		defExpires := fs.String("default-expires", "7d", "expiry of agent uploads that do not ask for one (`never` for none)")
		fs.Parse(args[1:])
		if v := os.Getenv("TIBI_MCP_ALLOW_DIRS"); v != "" {
			allow = append(allow, filepath.SplitList(v)...)
		}
		tools := &mcp.Toolbox{Client: a.APIClient, MaxFileBytes: 4 << 30, Paths: mcp.DefaultPathPolicy(allow),
			DefaultExpires: *defExpires, AllowDelete: *allowDelete}
		srv := &mcp.Server{Name: "tibi", Version: app.Version, Log: os.Stderr, Tools: tools}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		go func() {
			wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			if w := tools.ScopeWarning(wctx); w != "" {
				fmt.Fprintln(os.Stderr, w)
			}
		}()
		if err := srv.Serve(ctx, os.Stdin, os.Stdout); err != nil && ctx.Err() == nil {
			fmt.Fprintln(os.Stderr, "tibi mcp:", err)
			os.Exit(1)
		}
		return
	}
	os.Exit(a.Run(context.Background(), args))
}

func exit(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "tibi:", err)
		os.Exit(1)
	}
	os.Exit(0)
}

// multiFlag collects a repeatable string flag.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }
