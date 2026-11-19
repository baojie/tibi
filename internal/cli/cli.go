// Package cli implements the tibi client commands. stdout carries only result
// URLs (or JSON with --json); everything else goes to stderr.
package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/pflag"

	"tibi/internal/client"
)

// App holds the CLI's environment so tests can drive it.
type App struct {
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
	StdinIsTTY bool
	Getenv     func(string) string
	HTTP       *http.Client
	// ReadSecret reads a line without echo; nil means plain line read.
	ReadSecret func() (string, error)

	in *bufio.Reader
}

func (a *App) getenv(k string) string {
	if a.Getenv == nil {
		return os.Getenv(k)
	}
	return a.Getenv(k)
}

func (a *App) errf(format string, args ...any) {
	fmt.Fprintf(a.Stderr, "tibi: "+format+"\n", args...)
}

// Usage is the top-level help text.
const Usage = `tibi — share files, images and text by URL

usage:
  tibi login                       set server and token
  tibi up FILE...                  upload files, print URLs
  tibi up -                        upload stdin
  <cmd> | tibi                     upload stdin
  tibi serve [--config PATH]       run the server
  tibi admin ...                   manage the server (tokens)

upload flags:
  -n, --name NAME    filename for stdin uploads
  -t, --title TITLE  title
      --json         print the full API response
`

// Run executes a client command and returns the process exit code.
func (a *App) Run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		if a.StdinIsTTY {
			fmt.Fprint(a.Stderr, Usage)
			return 2
		}
		args = []string{"up", "-"}
	}
	var err error
	switch args[0] {
	case "login":
		err = a.login(ctx, args[1:])
	case "up":
		err = a.up(ctx, args[1:])
	case "help", "-h", "--help":
		fmt.Fprint(a.Stdout, Usage)
		return 0
	default:
		if strings.HasPrefix(args[0], "-") && !a.StdinIsTTY {
			// `cmd | tibi -n foo.txt`
			err = a.up(ctx, append(args, "-"))
			break
		}
		a.errf("unknown command %q (see tibi help)", args[0])
		return 2
	}
	if err != nil {
		var ue usageError
		if errors.As(err, &ue) {
			a.errf("%s", ue.msg)
			return 2
		}
		a.errf("%v", err)
		return 1
	}
	return 0
}

type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func (a *App) flagSet(name string) *pflag.FlagSet {
	fs := pflag.NewFlagSet(name, pflag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	return fs
}

func (a *App) client() (*client.Client, error) {
	c, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	if c.Server == "" || c.Token == "" {
		return nil, errors.New("not logged in: run `tibi login` or set TIBI_SERVER and TIBI_TOKEN")
	}
	cl := client.New(c.Server, c.Token)
	if a.HTTP != nil {
		cl.HTTP = a.HTTP
	}
	return cl, nil
}

func (a *App) readLine(prompt string) (string, error) {
	fmt.Fprint(a.Stderr, prompt)
	if a.in == nil {
		a.in = bufio.NewReader(a.Stdin)
	}
	s, err := a.in.ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && s != "") {
		return "", err
	}
	return strings.TrimSpace(s), nil
}

func (a *App) login(ctx context.Context, args []string) error {
	fs := a.flagSet("login")
	server := fs.String("server", "", "server URL")
	token := fs.String("token", "", "API token")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	cfg, err := a.loadConfig()
	if err != nil {
		return err
	}
	if *server == "" {
		def := ""
		if cfg.Server != "" {
			def = " [" + cfg.Server + "]"
		}
		s, err := a.readLine("Server URL" + def + ": ")
		if err != nil {
			return err
		}
		if s == "" {
			s = cfg.Server
		}
		*server = s
	}
	if *token == "" {
		fmt.Fprint(a.Stderr, "Token: ")
		var t string
		if a.ReadSecret != nil && a.StdinIsTTY {
			t, err = a.ReadSecret()
			fmt.Fprintln(a.Stderr)
		} else {
			t, err = a.readLine("")
		}
		if err != nil {
			return err
		}
		*token = strings.TrimSpace(t)
	}
	*server = strings.TrimRight(strings.TrimSpace(*server), "/")
	if *server == "" || *token == "" {
		return usageError{"server and token are required"}
	}
	if !strings.HasPrefix(*server, "http://") && !strings.HasPrefix(*server, "https://") {
		*server = "https://" + *server
	}
	cl := client.New(*server, *token)
	if a.HTTP != nil {
		cl.HTTP = a.HTTP
	}
	who, err := cl.Whoami(ctx)
	if err != nil {
		return fmt.Errorf("verify token: %w", err)
	}
	cfg.Server, cfg.Token = *server, *token
	p, err := a.saveConfig(cfg)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.Stderr, "logged in to %s as %q (scopes: %s); saved %s\n",
		*server, who.Name, strings.Join(who.Scopes, ","), p)
	return nil
}

func (a *App) up(ctx context.Context, args []string) error {
	fs := a.flagSet("up")
	name := fs.StringP("name", "n", "", "filename for stdin")
	title := fs.StringP("title", "t", "", "title")
	asJSON := fs.Bool("json", false, "print full API response")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	files := fs.Args()
	if len(files) == 0 {
		if a.StdinIsTTY {
			return usageError{"usage: tibi up FILE... (or pipe data to `tibi up -`)"}
		}
		files = []string{"-"}
	}
	cl, err := a.client()
	if err != nil {
		return err
	}
	var failed int
	for _, f := range files {
		it, raw, err := a.upload(ctx, cl, f, *name, *title)
		if err != nil {
			a.errf("%s: %v", displayName(f), err)
			failed++
			continue
		}
		if *asJSON {
			var buf bytes.Buffer
			if err := json.Compact(&buf, raw); err != nil {
				return err
			}
			fmt.Fprintln(a.Stdout, buf.String())
		} else {
			fmt.Fprintln(a.Stdout, it.URL)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d uploads failed", failed, len(files))
	}
	return nil
}

func displayName(f string) string {
	if f == "-" {
		return "stdin"
	}
	return f
}

func (a *App) upload(ctx context.Context, cl *client.Client, path, name, title string) (*client.Item, json.RawMessage, error) {
	opts := client.UploadOptions{Filename: name, Title: title, Source: "cli"}
	if path == "-" {
		return cl.Upload(ctx, a.Stdin, -1, opts)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	if st.IsDir() {
		return nil, nil, errors.New("is a directory")
	}
	if opts.Filename == "" {
		opts.Filename = filepath.Base(path)
	}
	return cl.Upload(ctx, f, st.Size(), opts)
}
