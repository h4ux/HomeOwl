package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

func TestHandle(t *testing.T) {
	s := &Server{Name: "t", Version: "1", Tools: func() []Tool {
		return []Tool{{Name: "echo", Level: "read", Schema: map[string]any{"type": "object"},
			Run: func(ctx context.Context, args map[string]any, who string) (any, error) {
				return map[string]any{"you": who, "args": args}, nil
			}}}
	}}
	var out map[string]any
	_ = json.Unmarshal(s.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`), "x"), &out)
	if out["result"].(map[string]any)["protocolVersion"] != "2025-03-26" {
		t.Fatalf("version negotiation: %v", out)
	}
	if s.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`), "x") != nil {
		t.Fatal("notifications get no reply")
	}
	_ = json.Unmarshal(s.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{"a":1}}}`), "bob"), &out)
	res := out["result"].(map[string]any)
	if res["isError"] != false || res["structuredContent"].(map[string]any)["you"] != "bob" {
		t.Fatalf("tool call: %v", out)
	}
	_ = json.Unmarshal(s.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"nope"}}`), "x"), &out)
	if out["error"] == nil {
		t.Fatal("unknown tool must error")
	}
	if !Allowed("guarded", "read") || Allowed("read", "guarded") || Allowed("off", "read") || !Allowed("god", "god") {
		t.Fatal("mode levels")
	}
}
