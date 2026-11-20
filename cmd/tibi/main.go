// Command tibi is the tibi server, admin tool and client CLI in one binary.
package main

import (
	"context"
	"fmt"
	"os"

	"os/signal"
	"syscall"

	"golang.org/x/term"

	"tibi/internal/app"
	"tibi/internal/cli"
	"tibi/internal/mcp"
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
		ReadSecret: func() (string, error) {
			b, err := term.ReadPassword(int(os.Stdin.Fd()))
			return string(b), err
		},
	}
	if len(args) > 0 && args[0] == "mcp" {
		srv := &mcp.Server{Name: "tibi", Version: app.Version, Log: os.Stderr,
			Tools: &mcp.Toolbox{Client: a.APIClient, MaxFileBytes: 4 << 30}}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
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
