// Command tibi is the tibi server, admin tool and client CLI in one binary.
package main

import (
	"context"
	"fmt"
	"os"

	"golang.org/x/term"

	"tibi/internal/app"
	"tibi/internal/cli"
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
		Stdin:      os.Stdin,
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
		StdinIsTTY: term.IsTerminal(int(os.Stdin.Fd())),
		ReadSecret: func() (string, error) {
			b, err := term.ReadPassword(int(os.Stdin.Fd()))
			return string(b), err
		},
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
