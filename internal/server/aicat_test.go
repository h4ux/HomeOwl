package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Claude's suggestions: the key stays on the server, only valid domain names go out, and only answers for those
// domains with a listed category come back; accepted ones are saved as rules in one go.
func TestAICategories(t *testing.T) {
	a := checkinApp(t)
	old := aiCategorize
	defer func() { aiCategorize = old }()
	var sent []string
	aiCategorize = func(_ context.Context, ac aiConf, cats, doms []string) ([]aiSuggestion, error) {
		if ac.Provider != "anthropic" || ac.Key != "sk-ant-api03-abcdefghijklmnopqrstuvwxyz" || ac.Model != aiModel {
			t.Fatalf("conf %+v", ac)
		}
		sent = append(sent, doms...)
		return []aiSuggestion{{Domain: "example-games.com", Category: cats[0], Why: "a game site"},
			{Domain: "not-asked.com", Category: cats[0]}, {Domain: "example-cdn.net", Category: "Made up"}}, nil
	}
	if _, err := a.analyticsAdmin("ai-categorize", map[string]any{"domains": []any{"example-games.com"}}); err == nil {
		t.Fatal("ran without a key")
	}
	if _, err := a.analyticsAdmin("ai-setup", map[string]any{"provider": "anthropic", "key": "hello"}); err == nil {
		t.Fatal("took a key that isn't one")
	}
	if _, err := a.analyticsAdmin("ai-setup", map[string]any{"provider": "anthropic", "key": "sk-ant-api03-abcdefghijklmnopqrstuvwxyz"}); err != nil {
		t.Fatal(err)
	}
	st, _ := a.analyticsAdmin("ai-status", nil)
	if st.(map[string]any)["ready"] != true || strings.Contains(strings.ToLower(jsonS(st)), "sk-ant") {
		t.Fatalf("status leaks or misses the key: %v", st)
	}
	r, err := a.analyticsAdmin("ai-categorize", map[string]any{"domains": []any{"example-games.com", "EXAMPLE-cdn.net.", "<script>", "192.168.1.5", "example-games.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(sent, " ") != "example-games.com example-cdn.net" {
		t.Fatalf("sent %v", sent)
	}
	sugg := r.(map[string]any)["suggestions"].([]aiSuggestion)
	if len(sugg) != 1 || sugg[0].Domain != "example-games.com" {
		t.Fatalf("suggestions %+v", sugg)
	}
	res, err := a.analyticsAdmin("rules-import", map[string]any{"rules": []any{
		map[string]any{"pattern": "example-games.com", "category": "Games"},
		map[string]any{"pattern": "bad domain!", "category": "Games"},
		map[string]any{"pattern": "example-cdn.net", "category": "Uncategorized"}}})
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	if m["saved"] != 1 || len(m["skipped"].([]string)) != 2 {
		t.Fatalf("import: %v", m)
	}
	found := false
	for _, x := range a.An.Rules() {
		found = found || (x.Pattern == "example-games.com" && x.Category == "Games")
	}
	if !found {
		t.Fatal("rule not saved")
	}
	if _, err := a.analyticsAdmin("ai-setup", map[string]any{"provider": "anthropic", "remove": true}); err != nil || a.Sec.Get(aiKeySlots["anthropic"]) != "" {
		t.Fatal("key not removed")
	}
}

// OpenRouter and Ollama: keys checked per provider, an Ollama address only http(s) to a host, and a local model's
// answer read even when it's wrapped in prose.
func TestAIProviders(t *testing.T) {
	a := checkinApp(t)
	if _, err := a.analyticsAdmin("ai-setup", map[string]any{"provider": "openrouter", "key": "sk-ant-api03-abcdefghijklmnopqrstuvwxyz"}); err == nil {
		t.Fatal("an Anthropic key taken as an OpenRouter one")
	}
	if _, err := a.analyticsAdmin("ai-setup", map[string]any{"provider": "openrouter", "key": "sk-or-v1-abcdefghijklmnopqrstuvwxyz"}); err != nil {
		t.Fatal(err)
	}
	if ac := a.aiConf(); ac.Provider != "openrouter" || ac.Model != aiOpenRouterAuto || !ac.ready() {
		t.Fatalf("openrouter: %+v", ac)
	}
	for _, bad := range []string{"file:///etc/passwd", "http://user:pw@host:11434", "http://host:11434/api/chat", "gopher://x"} {
		if _, err := a.analyticsAdmin("ai-setup", map[string]any{"provider": "ollama", "url": bad, "model": "llama3.1"}); err == nil {
			t.Fatalf("took %s", bad)
		}
	}
	if _, err := a.analyticsAdmin("ai-setup", map[string]any{"provider": "ollama", "url": "http://127.0.0.1:11434"}); err == nil {
		t.Fatal("ollama without a model")
	}
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":"qwen2.5:7b"},{"name":"llama3.1:8b"}]}`))
		case "/api/chat":
			_ = json.NewDecoder(r.Body).Decode(&got)
			_, _ = w.Write([]byte(`{"message":{"content":"Here you go: {\"suggestions\":[{\"domain\":\"example-games.com\",\"category\":\"Games\",\"why\":\"game\"},{\"domain\":\"x.example\",\"category\":\"Unknown\",\"why\":\"\"}]}"}}`))
		}
	}))
	defer srv.Close()
	if _, err := a.analyticsAdmin("ai-setup", map[string]any{"provider": "ollama", "url": srv.URL + "/", "model": "llama3.1:8b"}); err != nil {
		t.Fatal(err)
	}
	ms, err := a.analyticsAdmin("ai-models", map[string]any{"provider": "ollama", "url": srv.URL})
	if err != nil || strings.Join(ms.(map[string]any)["models"].([]string), ",") != "llama3.1:8b,qwen2.5:7b" {
		t.Fatalf("models: %v %v", ms, err)
	}
	ac := a.aiConf()
	if ac.URL != srv.URL || ac.Key != "" || !ac.ready() {
		t.Fatalf("ollama conf %+v", ac)
	}
	l, err := aiCategorize(context.Background(), ac, []string{"Games", "Streaming"}, []string{"example-games.com", "x.example"})
	if err != nil || len(l) != 1 || l[0].Category != "Games" {
		t.Fatalf("ollama: %+v %v", l, err)
	}
	if got["model"] != "llama3.1:8b" || got["stream"] != false || got["format"] == nil {
		t.Fatalf("request: %v", got)
	}
	if _, err := aiParse("```json\n{\"suggestions\":[]}\n```"); err != nil {
		t.Fatal("code block not read")
	}
}

func jsonS(v any) string { b, _ := json.Marshal(v); return string(b) }
