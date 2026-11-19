package server

import (
	"context"
	"encoding/json"
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
	aiCategorize = func(_ context.Context, key string, cats, doms []string) ([]aiSuggestion, error) {
		if key != "sk-ant-api03-abcdefghijklmnopqrstuvwxyz" {
			t.Fatalf("key %q", key)
		}
		sent = append(sent, doms...)
		return []aiSuggestion{{Domain: "example-games.com", Category: cats[0], Why: "a game site"},
			{Domain: "not-asked.com", Category: cats[0]}, {Domain: "example-cdn.net", Category: "Made up"}}, nil
	}
	if _, err := a.analyticsAdmin("ai-categorize", map[string]any{"domains": []any{"example-games.com"}}); err == nil {
		t.Fatal("ran without a key")
	}
	if _, err := a.analyticsAdmin("ai-key", map[string]any{"key": "hello"}); err == nil {
		t.Fatal("took a key that isn't one")
	}
	if _, err := a.analyticsAdmin("ai-key", map[string]any{"key": "sk-ant-api03-abcdefghijklmnopqrstuvwxyz"}); err != nil {
		t.Fatal(err)
	}
	st, _ := a.analyticsAdmin("ai-status", nil)
	if st.(map[string]any)["key"] != true || strings.Contains(strings.ToLower(jsonS(st)), "sk-ant") {
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
	if _, err := a.analyticsAdmin("ai-key", map[string]any{"key": ""}); err != nil || a.Sec.Get(aiKeySlot) != "" {
		t.Fatal("key not removed")
	}
}

func jsonS(v any) string { b, _ := json.Marshal(v); return string(b) }
