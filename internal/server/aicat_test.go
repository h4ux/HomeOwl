package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The suggestion job: the key stays on the server, only valid domain names go out, nothing is asked twice (a domain
// the model didn't know only after 30 days), one run at a time, and reviewed suggestions leave the waiting list.
func TestAIJob(t *testing.T) {
	a := checkinApp(t)
	oldC, oldU := aiCategorize, aiUncategorized
	defer func() { aiCategorize, aiUncategorized = oldC, oldU }()
	uncat := []string{"example-games.com", "EXAMPLE-cdn.net", "<script>", "192.168.1.5", "example-video.com"}
	aiUncategorized = func(*App, time.Time, int) ([]string, error) { return uncat, nil }
	var sent []string
	gate := make(chan bool, 10)
	aiCategorize = func(_ context.Context, ac aiConf, cats, doms []string) ([]aiSuggestion, error) {
		<-gate
		if ac.Provider != "anthropic" || ac.Key != "sk-ant-api03-abcdefghijklmnopqrstuvwxyz" || ac.Model != aiModel {
			t.Fatalf("conf %+v", ac)
		}
		sent = append(sent, doms...)
		return []aiSuggestion{{Domain: "example-games.com", Category: cats[0], Why: "a game site"},
			{Domain: "not-asked.com", Category: cats[0]}, {Domain: "example-cdn.net", Category: "Made up"}}, nil
	}
	wait := func() {
		for i := 0; i < 200 && a.aiJobs.progress().Running; i++ {
			time.Sleep(10 * time.Millisecond)
		}
		if a.aiJobs.progress().Running {
			t.Fatal("job didn't finish")
		}
	}
	run := func() bool {
		r, err := a.analyticsAdmin("ai-run", nil)
		if err != nil {
			t.Fatal(err)
		}
		return r.(map[string]any)["started"] == true
	}
	if _, err := a.analyticsAdmin("ai-run", nil); err == nil {
		t.Fatal("ran without a model set up")
	}
	if _, err := a.analyticsAdmin("ai-setup", map[string]any{"provider": "anthropic", "key": "hello"}); err == nil {
		t.Fatal("took a key that isn't one")
	}
	if _, err := a.analyticsAdmin("ai-setup", map[string]any{"provider": "anthropic", "key": "sk-ant-api03-abcdefghijklmnopqrstuvwxyz", "daily": true}); err != nil {
		t.Fatal(err)
	}
	st, _ := a.analyticsAdmin("ai-status", nil)
	if m := st.(map[string]any); m["ready"] != true || m["daily"] != true || strings.Contains(strings.ToLower(jsonS(st)), "sk-ant") {
		t.Fatalf("status leaks or misses the key: %v", st)
	}
	if !run() {
		t.Fatal("didn't start")
	}
	if run() {
		t.Fatal("a second run started while one was running")
	}
	gate <- true
	wait()
	if strings.Join(sent, " ") != "example-games.com example-cdn.net example-video.com" {
		t.Fatalf("sent %v", sent)
	}
	st, _ = a.analyticsAdmin("ai-status", nil)
	if m := st.(map[string]any); m["pending"] != 1 || m["asked"] != 3 || m["last"].(*aiRunInfo).Found != 1 {
		t.Fatalf("after the run: %v", st)
	}
	// again: nothing new to ask
	sent = nil
	run()
	wait()
	if len(sent) != 0 {
		t.Fatalf("asked twice: %v", sent)
	}
	// a month on, the ones it didn't know are asked again; the suggested one isn't
	a.aiJobs.update(func(st *aiState) {
		for d, x := range st.Asked {
			x.At -= int64(31 * 24 * 3600)
			st.Asked[d] = x
		}
	})
	gate <- true
	run()
	wait()
	if strings.Join(sent, " ") != "example-cdn.net example-video.com" {
		t.Fatalf("retry sent %v", sent)
	}
	// Stop: the group in flight is cancelled, nothing of it is marked asked, the run ends
	a.aiJobs.update(func(st *aiState) { delete(st.Asked, "example-video.com") })
	aiCategorize = func(ctx context.Context, _ aiConf, _, doms []string) ([]aiSuggestion, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	run()
	for i := 0; i < 100 && !a.aiJobs.aiStop(); i++ {
		time.Sleep(5 * time.Millisecond)
	}
	wait()
	a.aiJobs.mu.Lock()
	st2 := a.aiJobs.load()
	a.aiJobs.mu.Unlock()
	if _, asked := st2.Asked["example-video.com"]; asked || st2.Last.Error != "stopped" {
		t.Fatalf("after stop: asked=%v last=%+v", asked, st2.Last)
	}
	p, _ := a.analyticsAdmin("ai-pending", nil)
	if l := p.(map[string]any)["suggestions"].([]aiPending); len(l) != 1 || l[0].Domain != "example-games.com" {
		t.Fatalf("pending %+v", l)
	}
	res, err := a.analyticsAdmin("rules-import", map[string]any{"rules": []any{
		map[string]any{"pattern": "example-games.com", "category": "Games"},
		map[string]any{"pattern": "bad domain!", "category": "Games"},
		map[string]any{"pattern": "example-cdn.net", "category": "Uncategorized"}}})
	if err != nil {
		t.Fatal(err)
	}
	if m := res.(map[string]any); m["saved"] != 1 || len(m["skipped"].([]string)) != 2 {
		t.Fatalf("import: %v", m)
	}
	if p, _ = a.analyticsAdmin("ai-pending", nil); len(p.(map[string]any)["suggestions"].([]aiPending)) != 0 {
		t.Fatal("a saved suggestion still waits")
	}
	found := false
	for _, x := range a.An.Rules() {
		found = found || (x.Pattern == "example-games.com" && x.Category == "Games")
	}
	if !found {
		t.Fatal("rule not saved")
	}
	// a domain that got a category some other way drops off the waiting list
	a.aiJobs.update(func(st *aiState) {
		st.Pending = []aiPending{{aiSuggestion: aiSuggestion{Domain: "example-old.com", Category: "Games"}}}
	})
	if p, _ = a.analyticsAdmin("ai-pending", nil); len(p.(map[string]any)["suggestions"].([]aiPending)) != 0 {
		t.Fatal("a domain no longer uncategorized still waits")
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
	if opts, _ := got["options"].(map[string]any); opts["num_ctx"] != float64(8192) || opts["num_predict"] == nil || aiChunkFor(ac) != 25 {
		t.Fatalf("ollama options: %v", got["options"])
	}
	if got["model"] != "llama3.1:8b" || got["stream"] != false || got["format"] == nil {
		t.Fatalf("request: %v", got)
	}
	if _, err := aiParse("```json\n{\"suggestions\":[]}\n```"); err != nil {
		t.Fatal("code block not read")
	}
}

func jsonS(v any) string { b, _ := json.Marshal(v); return string(b) }
