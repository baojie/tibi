// Package mcp is tibi's Model Context Protocol server (`tibi mcp`): it lets
// AI agents such as Claude Code upload files and text and get back URLs.
//
// Transport: stdio, one JSON-RPC 2.0 message per line (MCP 2024-11-05 and
// later). Only stdout carries protocol messages; diagnostics go to stderr.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// Protocol versions this server speaks, newest first.
var protocolVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// JSON-RPC error codes.
const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

// Server answers MCP requests using a Toolbox.
type Server struct {
	Name, Version string
	Tools         *Toolbox
	Log           io.Writer // diagnostics (stderr)

	mu  sync.Mutex
	out *json.Encoder
}

// Serve reads requests from r and writes responses to w until r ends or
// ctx is cancelled.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	s.out = json.NewEncoder(w)
	s.out.SetEscapeHTML(false)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			s.send(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{codeParse, "parse error"}})
			continue
		}
		if len(req.ID) == 0 {
			continue // notification (e.g. notifications/initialized): no reply
		}
		result, rerr := s.handle(ctx, req)
		resp := response{JSONRPC: "2.0", ID: req.ID}
		if rerr != nil {
			resp.Error = rerr
		} else {
			resp.Result = result
		}
		s.send(resp)
	}
	return sc.Err()
}

func (s *Server) send(r response) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.out.Encode(r); err != nil && s.Log != nil {
		fmt.Fprintln(s.Log, "tibi mcp: write:", err)
	}
}

func (s *Server) handle(ctx context.Context, req request) (any, *rpcError) {
	if req.JSONRPC != "2.0" {
		return nil, &rpcError{codeInvalidRequest, "jsonrpc must be 2.0"}
	}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(req.Params, &p)
		version := protocolVersions[0]
		for _, v := range protocolVersions {
			if v == p.ProtocolVersion {
				version = v
			}
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": s.Name, "title": "tibi", "version": s.Version},
			"instructions": "tibi shares files and text by URL. Use upload_file for a local file (images, PDFs, " +
				"logs, archives…) and paste_text for text or code you have in hand; both return a link anyone can " +
				"open. Set private=true for things that must not be public, and expires (e.g. 1h, 7d) for temporary shares.",
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": s.Tools.List()}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{codeInvalidParams, "invalid params"}
		}
		if !s.Tools.Has(p.Name) {
			return nil, &rpcError{codeInvalidParams, "unknown tool: " + p.Name}
		}
		return s.Tools.Call(ctx, p.Name, p.Arguments), nil
	}
	return nil, &rpcError{codeMethodNotFound, "method not found: " + req.Method}
}
