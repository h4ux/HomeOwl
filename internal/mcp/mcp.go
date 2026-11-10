// Package mcp implements a small Model Context Protocol server (tools only) over JSON-RPC 2.0,
// served at POST /mcp ("Streamable HTTP" with plain JSON responses) and bridged to stdio by `homeowl mcp`.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Levels, lowest first. A tool is offered when the configured mode is at or above its level.
var levels = map[string]int{"off": 0, "read": 1, "guarded": 2, "god": 3}

func Allowed(mode, need string) bool { return levels[mode] >= levels[need] && levels[mode] > 0 }

type Tool struct {
	Name        string
	Title       string
	Description string
	Schema      map[string]any
	Level       string // read | guarded | god
	Destructive bool
	Run         func(ctx context.Context, args map[string]any, who string) (any, error)
}

type Server struct {
	Name, Version, Instructions string
	Tools                       func() []Tool // already filtered by the current mode
}

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

var supported = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// Handle processes one JSON-RPC message; it returns nil for notifications.
func (s *Server) Handle(ctx context.Context, raw []byte, who string) []byte {
	var r rpcReq
	if err := json.Unmarshal(raw, &r); err != nil || r.Method == "" {
		return reply(nil, nil, &rpcErr{-32700, "parse error"})
	}
	notification := len(r.ID) == 0
	res, e := s.dispatch(ctx, r, who)
	if notification {
		return nil
	}
	return reply(r.ID, res, e)
}

func reply(id json.RawMessage, result any, e *rpcErr) []byte {
	m := map[string]any{"jsonrpc": "2.0", "id": id}
	if id == nil {
		m["id"] = nil
	}
	if e != nil {
		m["error"] = e
	} else {
		m["result"] = result
	}
	b, _ := json.Marshal(m)
	return b
}

func (s *Server) dispatch(ctx context.Context, r rpcReq, who string) (any, *rpcErr) {
	switch r.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(r.Params, &p)
		v := supported[0]
		for _, x := range supported {
			if x == p.ProtocolVersion {
				v = x
			}
		}
		return map[string]any{"protocolVersion": v, "capabilities": map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo": map[string]any{"name": s.Name, "version": s.Version}, "instructions": s.Instructions}, nil
	case "notifications/initialized", "notifications/cancelled":
		return nil, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		var list []map[string]any
		for _, t := range s.Tools() {
			list = append(list, map[string]any{"name": t.Name, "title": t.Title, "description": t.Description, "inputSchema": t.Schema,
				"annotations": map[string]any{"title": t.Title, "readOnlyHint": t.Level == "read", "destructiveHint": t.Destructive}})
		}
		if list == nil {
			list = []map[string]any{}
		}
		return map[string]any{"tools": list}, nil
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(r.Params, &p); err != nil {
			return nil, &rpcErr{-32602, "invalid params"}
		}
		for _, t := range s.Tools() {
			if t.Name != p.Name {
				continue
			}
			if p.Arguments == nil {
				p.Arguments = map[string]any{}
			}
			ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			out, err := t.Run(ctx, p.Arguments, who)
			if err != nil {
				return toolResult(map[string]any{"error": err.Error()}, true), nil
			}
			return toolResult(out, false), nil
		}
		return nil, &rpcErr{-32602, "unknown tool (or not allowed in the current HomeOwl MCP mode): " + p.Name}
	}
	return nil, &rpcErr{-32601, "method not found: " + r.Method}
}

func toolResult(v any, isErr bool) map[string]any {
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		b = []byte(fmt.Sprint(v))
	}
	text := string(b)
	if len(text) > 200_000 {
		text = text[:200_000] + "\n… (truncated — narrow the request)"
	}
	res := map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": isErr}
	if m, ok := v.(map[string]any); ok && len(b) < 200_000 {
		res["structuredContent"] = m
	}
	return res
}

// HTTPHandler serves POST /mcp. auth returns the caller's name, or "" to reject.
func (s *Server) HTTPHandler(auth func(r *http.Request) string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "use POST", http.StatusMethodNotAllowed)
			return
		}
		who := auth(r)
		if who == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="homeowl"`)
			http.Error(w, `{"error":"missing or invalid HomeOwl MCP token, or MCP is turned off in Settings"}`, http.StatusUnauthorized)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		out := s.Handle(r.Context(), bytes.TrimSpace(body), who)
		if out == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(out)
	})
}

// Bridge copies JSON-RPC lines between stdio and a HomeOwl /mcp endpoint (for MCP clients that launch a command).
func Bridge(endpoint, token string, insecure bool) error {
	u, err := url.Parse(endpoint)
	if err != nil {
		return err
	}
	host := u.Hostname()
	local := host == "localhost" || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()
	hc := &http.Client{Timeout: 3 * time.Minute, Transport: &http.Transport{Proxy: nil,
		TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure || local}}} //nolint:gosec // HomeOwl's own self-signed cert on this computer
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 1<<20), 8<<20)
	outW := bufio.NewWriter(os.Stdout)
	for in.Scan() {
		line := bytes.TrimSpace(in.Bytes())
		if len(line) == 0 {
			continue
		}
		req, _ := http.NewRequest("POST", endpoint, bytes.NewReader(line))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := hc.Do(req)
		var body []byte
		if err == nil {
			body, _ = io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusAccepted {
				continue
			}
			if resp.StatusCode != 200 {
				err = fmt.Errorf("HomeOwl answered %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
			}
		}
		if err != nil {
			var r rpcReq
			_ = json.Unmarshal(line, &r)
			if len(r.ID) == 0 {
				continue
			}
			body = reply(r.ID, nil, &rpcErr{-32000, err.Error()})
		}
		_, _ = outW.Write(bytes.TrimSpace(body))
		_ = outW.WriteByte('\n')
		_ = outW.Flush()
	}
	return in.Err()
}
