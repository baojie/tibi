package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"

	"github.com/baojie/tibi/internal/client"
	"github.com/baojie/tibi/internal/e2e"
)

// maxEncryptBytes bounds what is encrypted in memory (the browser decrypts
// in memory too).
const maxEncryptBytes = 512 << 20

// password asks for the optional E2E password (twice, to catch typos).
// TIBI_E2E_PASSWORD supplies it non-interactively (for scripts).
func (a *App) password() (string, error) {
	if p := a.getenv("TIBI_E2E_PASSWORD"); p != "" {
		return p, nil
	}
	if a.ReadSecret == nil {
		return "", errors.New("--password needs a terminal (or TIBI_E2E_PASSWORD)")
	}
	fmt.Fprint(a.Stderr, "Password: ")
	p1, err := a.ReadSecret()
	fmt.Fprintln(a.Stderr)
	if err != nil {
		return "", err
	}
	fmt.Fprint(a.Stderr, "Repeat password: ")
	p2, err := a.ReadSecret()
	fmt.Fprintln(a.Stderr)
	if err != nil {
		return "", err
	}
	if p1 != p2 {
		return "", errors.New("passwords do not match")
	}
	if p1 == "" {
		return "", errors.New("empty password")
	}
	return p1, nil
}

// uploadEncrypted encrypts content locally and uploads only ciphertext. The
// returned URL carries the key in its fragment, which never reaches the
// server. Name and type travel inside the ciphertext; the language is a
// (public) display hint.
func (a *App) uploadEncrypted(ctx context.Context, cl *client.Client, content []byte, name, typ string, o *outFlags) (*client.Item, string, json.RawMessage, error) {
	if o.md || o.raw {
		return nil, "", nil, usageError{"--md and --raw do not apply to encrypted items (the raw bytes are ciphertext)"}
	}
	if len(content) > maxEncryptBytes {
		return nil, "", nil, fmt.Errorf("too large to encrypt (%d bytes, limit %d)", len(content), maxEncryptBytes)
	}
	pw := ""
	if o.password {
		var err error
		if pw, err = a.password(); err != nil {
			return nil, "", nil, err
		}
	}
	if o.title != "" {
		fmt.Fprintln(a.Stderr, "note: the title is not encrypted")
	}
	key, frag := e2e.NewKey()
	blob, err := e2e.Encrypt(key, pw, e2e.Header{Name: name, Type: typ, Lang: o.lang}, content)
	if err != nil {
		return nil, "", nil, err
	}
	opts := a.uploadOptions(o)
	opts.Filename, opts.Kind, opts.KeepMeta = "", "encrypted", false
	var it *client.Item
	var raw json.RawMessage
	if int64(len(blob)) > a.resumableThreshold() {
		tmp, err := os.CreateTemp(a.stateDir(), "enc-*")
		if err != nil {
			if err = os.MkdirAll(a.stateDir(), 0o700); err == nil {
				tmp, err = os.CreateTemp(a.stateDir(), "enc-*")
			}
			if err != nil {
				return nil, "", nil, err
			}
		}
		defer os.Remove(tmp.Name())
		if _, err := tmp.Write(blob); err != nil {
			tmp.Close()
			return nil, "", nil, err
		}
		tmp.Close()
		it, raw, err = cl.UploadResumable(ctx, client.Resumable{Path: tmp.Name(), Options: opts})
		if err != nil {
			return nil, "", nil, err
		}
	} else if it, raw, err = cl.Upload(ctx, bytes.NewReader(blob), int64(len(blob)), opts); err != nil {
		return nil, "", nil, err
	}
	return it, it.URL + "#k=" + frag, raw, nil
}

// encryptedLine is what an encrypted upload prints.
func (a *App) encryptedLine(link string, raw json.RawMessage, o *outFlags) (string, error) {
	if !o.json {
		return link, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", err
	}
	m["url"] = link // only the client knows the key
	b, err := json.Marshal(m)
	return string(b), err
}

func typeOf(name string, head []byte) string {
	if t := mime.TypeByExtension(filepath.Ext(name)); t != "" {
		return t
	}
	return http.DetectContentType(head)
}

// encryptFile reads a file (or stdin) for --encrypt.
func (a *App) encryptFile(path, name string) ([]byte, string, string, error) {
	var r io.Reader = a.Stdin
	if path != "-" {
		f, err := os.Open(path)
		if err != nil {
			return nil, "", "", err
		}
		defer f.Close()
		if st, err := f.Stat(); err != nil || st.IsDir() {
			return nil, "", "", errors.New("not a regular file")
		}
		r = f
		if name == "" {
			name = filepath.Base(path)
		}
	}
	b, err := io.ReadAll(io.LimitReader(r, maxEncryptBytes+1))
	if err != nil {
		return nil, "", "", err
	}
	if name == "" {
		name = "stdin"
	}
	return b, name, typeOf(name, b[:min(len(b), 512)]), nil
}
