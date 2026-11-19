package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/h4ux/homeowl/internal/config"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"

	"github.com/h4ux/homeowl/internal/categories"
)

// Suggesting categories with an AI model (Family › Categories). Optional, and the admin's choice of provider:
//   - anthropic: Claude, with their own Anthropic API key (the Go SDK)
//   - openrouter: any model on OpenRouter, with their OpenRouter key (OpenAI-style chat completions)
//   - ollama: a model running on their own computer or server (nothing leaves the house)
// Keys are kept in the keychain like every other secret, never sent to the browser. HomeOwl sends only the
// uncategorized domain names (no devices, people, counts or times) and the list of categories, and shows what comes
// back for the admin to accept or change. Nothing is saved until they do.

var aiKeySlots = map[string]string{"anthropic": "anthropic-api-key", "openrouter": "openrouter-api-key"}

const (
	aiModel          = "claude-opus-5-5" // Anthropic's default
	aiOpenRouterAuto = "openrouter/auto" // OpenRouter picks a model
	aiOllamaURL      = "http://127.0.0.1:11434"
)

var aiKeyRE = map[string]*regexp.Regexp{
	"anthropic":  regexp.MustCompile(`^sk-ant-[A-Za-z0-9_\-]{20,200}$`),
	"openrouter": regexp.MustCompile(`^sk-or-[A-Za-z0-9_\-]{20,200}$`),
}

var aiModelRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@\-]{0,127}$`)

// a domain name as the Categories tab lists them
var aiDomainRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9\-]{0,62}\.)+[a-z0-9\-]{2,63}$`)

// aiConf: the provider to ask, with its key, model and (Ollama) address.
type aiConf struct {
	Provider, Key, Model, URL string
}

func (a *App) aiConf() aiConf {
	c := a.Cfg.Get().AI
	p := c.Provider
	if p == "" {
		p = "anthropic"
	}
	ac := aiConf{Provider: p, Model: c.Model, URL: c.URL}
	if slot := aiKeySlots[p]; slot != "" {
		ac.Key = a.Sec.Get(slot)
	}
	switch {
	case p == "anthropic" && ac.Model == "":
		ac.Model = aiModel
	case p == "openrouter" && ac.Model == "":
		ac.Model = aiOpenRouterAuto
	case p == "ollama" && ac.URL == "":
		ac.URL = aiOllamaURL
	}
	return ac
}

func (ac aiConf) ready() bool {
	if ac.Provider == "ollama" {
		return ac.Model != ""
	}
	return ac.Key != ""
}

// aiLabel names the model for the page ("Claude", "openrouter/auto", "llama3.1 on this network").
func (ac aiConf) label() string {
	switch ac.Provider {
	case "ollama":
		return ac.Model + " (Ollama)"
	case "openrouter":
		return ac.Model + " (OpenRouter)"
	}
	return "Claude"
}

// aiOllamaBase checks an Ollama address: http(s), a host, nothing else.
func aiOllamaBase(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
		return "", errors.New("the Ollama address is like http://127.0.0.1:11434")
	}
	return u.Scheme + "://" + u.Host, nil
}

type aiSuggestion struct {
	Domain   string `json:"domain"`
	Category string `json:"category"`
	Why      string `json:"why"`
}

const aiChunk = 100 // domains per request

func (a *App) aiCategoriesOp(op string, body map[string]any, by string) (any, error) {
	switch op {
	case "ai-status":
		ac := a.aiConf()
		keys := map[string]bool{}
		for p, slot := range aiKeySlots {
			keys[p] = a.Sec.Get(slot) != ""
		}
		a.aiJobs.mu.Lock()
		st := a.aiJobs.load()
		a.aiJobs.mu.Unlock()
		return map[string]any{"provider": ac.Provider, "model": ac.Model, "url": ac.URL, "keys": keys, "ready": ac.ready(), "label": ac.label(),
			"daily": a.Cfg.Get().AI.Daily, "job": a.aiJobs.progress(), "last": st.Last, "pending": len(st.Pending), "asked": len(st.Asked),
			"defaults": map[string]string{"anthropic": aiModel, "openrouter": aiOpenRouterAuto, "ollama_url": aiOllamaURL}}, nil
	case "ai-setup":
		return a.aiSetup(body, by)
	case "ai-models":
		return a.aiModels(body)
	case "ai-run":
		started, err := a.aiStart(by)
		if err != nil {
			return nil, err
		}
		return map[string]any{"started": started, "job": a.aiJobs.progress()}, nil
	case "ai-pending":
		l, err := a.aiPendingView()
		return map[string]any{"suggestions": l, "categories": aiCategoryList(a)}, err
	case "ai-dismiss":
		var doms []string
		for _, x := range anyArr(body["domains"]) {
			if d, ok := x.(string); ok {
				doms = append(doms, d)
			}
		}
		a.aiDone(doms)
		return map[string]any{"ok": true}, nil
	}
	return nil, errors.New("not found")
}

// aiSetup saves the provider, its model and address, and a key (an empty key keeps the saved one; remove: true
// deletes it).
func (a *App) aiSetup(body map[string]any, by string) (any, error) {
	p := s(body, "provider")
	if p != "anthropic" && p != "openrouter" && p != "ollama" {
		return nil, errors.New("pick Anthropic, OpenRouter or Ollama")
	}
	model := strings.TrimSpace(s(body, "model"))
	if model != "" && !aiModelRE.MatchString(model) {
		return nil, errors.New("that isn't a model name")
	}
	addr := ""
	if p == "ollama" {
		var err error
		if addr, err = aiOllamaBase(orStr(s(body, "url"), aiOllamaURL)); err != nil {
			return nil, err
		}
		if model == "" {
			return nil, errors.New("pick an Ollama model (ollama pull llama3.1 on that computer, for one)")
		}
	}
	if slot := aiKeySlots[p]; slot != "" {
		k := strings.TrimSpace(s(body, "key"))
		switch {
		case b(body, "remove"):
			a.Sec.Delete(slot)
		case k != "":
			if !aiKeyRE[p].MatchString(k) {
				return nil, fmt.Errorf("that isn't %s API key", map[string]string{"anthropic": "an Anthropic", "openrouter": "an OpenRouter"}[p])
			}
			if err := a.Sec.Set(slot, k); err != nil {
				return nil, err
			}
		}
	}
	if err := a.Cfg.Update(func(c *config.Config) {
		c.AI = config.AI{Provider: p, Model: model, URL: addr, Daily: b(body, "daily")}
	}); err != nil {
		return nil, err
	}
	return a.aiCategoriesOp("ai-status", nil, by)
}

// aiModels lists the models an Ollama server has, or OpenRouter offers, for the picker.
func (a *App) aiModels(body map[string]any) (any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var u string
	switch s(body, "provider") {
	case "ollama":
		base, err := aiOllamaBase(orStr(s(body, "url"), aiOllamaURL))
		if err != nil {
			return nil, err
		}
		u = base + "/api/tags"
	case "openrouter":
		u = "https://openrouter.ai/api/v1/models"
	default:
		return map[string]any{"models": []string{}}, nil
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	res, err := aiHTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("couldn't reach %s: %v", req.URL.Host, err)
	}
	defer res.Body.Close()
	var r struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if res.StatusCode != 200 || json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&r) != nil {
		return nil, fmt.Errorf("%s didn't list its models (%d)", req.URL.Host, res.StatusCode)
	}
	out := []string{}
	for _, m := range r.Models {
		out = append(out, m.Name)
	}
	for _, m := range r.Data {
		out = append(out, m.ID)
	}
	sort.Strings(out)
	return map[string]any{"models": out}, nil
}

// aiCategoryList: the built-in categories and the admin's own (from their rules), without Uncategorized.
func aiCategoryList(a *App) []string {
	var cats []string
	have := map[string]bool{}
	add := func(c string) {
		if c != "" && c != "Uncategorized" && !have[c] {
			have[c] = true
			cats = append(cats, c)
		}
	}
	for _, c := range categories.Categories {
		add(c)
	}
	if a.An.Open() {
		for _, r := range a.An.Rules() {
			add(r.Category)
		}
	}
	return cats
}

// aiKeepKnown drops anything that isn't one of the domains asked about, or a category that isn't on the list.
func aiKeepKnown(l []aiSuggestion, asked map[string]bool, cats []string) []aiSuggestion {
	ok := map[string]bool{}
	for _, c := range cats {
		ok[c] = true
	}
	var out []aiSuggestion
	for _, x := range l {
		x.Domain = strings.ToLower(strings.TrimSpace(x.Domain))
		if asked[x.Domain] && ok[x.Category] {
			if len(x.Why) > 200 {
				x.Why = x.Why[:200]
			}
			out = append(out, x)
		}
	}
	return out
}

// aiHTTP: each request's context sets its time limit (aiTimeout)
var aiHTTP = &http.Client{}

// aiTimeout: how long one group of 100 may take. A model on a computer at home writes much slower than a hosted
// one (a 14B model on a laptop can need several minutes), so Ollama gets far longer.
func aiTimeout(ac aiConf) time.Duration {
	if ac.Provider == "ollama" {
		return 20 * time.Minute
	}
	return 4 * time.Minute
}

const aiSystem = "You sort internet domain names, seen in a family's DNS logs, into categories for a home network dashboard. " +
	"For each domain, pick the one category from the list that fits the service it belongs to (a CDN or API domain goes with the app or company it serves). " +
	"Answer Unknown when you don't recognise the domain or it could be several things; don't guess. Keep each why to a few words (what the domain is). " +
	"Reply with JSON only: {\"suggestions\":[{\"domain\":\"…\",\"category\":\"…\",\"why\":\"…\"}]}."

func aiSchema(cats []string) map[string]any {
	enum := append(append([]string{}, cats...), "Unknown")
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"suggestions": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"domain":   map[string]any{"type": "string"},
						"category": map[string]any{"type": "string", "enum": enum},
						"why":      map[string]any{"type": "string"},
					},
					"required":             []string{"domain", "category", "why"},
					"additionalProperties": false,
				},
			},
		},
		"required":             []string{"suggestions"},
		"additionalProperties": false,
	}
}

func aiPrompt(cats, domains []string) string {
	return "Categories:\n- " + strings.Join(cats, "\n- ") + "\n\nDomains:\n" + strings.Join(domains, "\n")
}

// aiCategorize asks the chosen provider (a var, so tests don't call anything).
var aiCategorize = func(ctx context.Context, ac aiConf, cats, domains []string) ([]aiSuggestion, error) {
	var text string
	var err error
	switch ac.Provider {
	case "openrouter":
		text, err = aiOpenRouter(ctx, ac, cats, domains)
	case "ollama":
		text, err = aiOllama(ctx, ac, cats, domains)
	default:
		text, err = aiClaude(ctx, ac, cats, domains)
	}
	if err != nil {
		return nil, err
	}
	return aiParse(text)
}

// aiParse reads the JSON answer (a model without structured output may wrap it in a code block or a sentence).
func aiParse(text string) ([]aiSuggestion, error) {
	if i, j := strings.Index(text, "{"), strings.LastIndex(text, "}"); i >= 0 && j > i {
		text = text[i : j+1]
	}
	var res struct {
		Suggestions []aiSuggestion `json:"suggestions"`
	}
	if err := json.Unmarshal([]byte(text), &res); err != nil {
		return nil, fmt.Errorf("couldn't read the model's answer: %v", err)
	}
	var out []aiSuggestion
	for _, x := range res.Suggestions {
		if x.Category != "Unknown" {
			out = append(out, x)
		}
	}
	return out, nil
}

// aiOpenRouter: OpenAI-style chat completions, asking for the JSON schema (models that can't follow one still get
// the instruction in the system prompt).
func aiOpenRouter(ctx context.Context, ac aiConf, cats, domains []string) (string, error) {
	body := map[string]any{
		"model":           ac.Model,
		"messages":        []map[string]string{{"role": "system", "content": aiSystem}, {"role": "user", "content": aiPrompt(cats, domains)}},
		"response_format": map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "categories", "strict": true, "schema": aiSchema(cats)}},
	}
	var r struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	code, err := aiPost(ctx, "https://openrouter.ai/api/v1/chat/completions", map[string]string{"Authorization": "Bearer " + ac.Key, "X-Title": "HomeOwl"}, body, &r)
	switch {
	case err != nil:
		return "", fmt.Errorf("couldn't reach OpenRouter: %v", err)
	case code == 401 || code == 403:
		return "", errors.New("OpenRouter rejected the API key")
	case code == 402:
		return "", errors.New("the OpenRouter account is out of credits")
	case code == 429:
		return "", errors.New("OpenRouter's rate limit: try again in a minute")
	case r.Error != nil:
		return "", fmt.Errorf("OpenRouter: %s", r.Error.Message)
	case code != 200 || len(r.Choices) == 0:
		return "", fmt.Errorf("OpenRouter answered %d", code)
	case r.Choices[0].FinishReason == "length":
		return "", errors.New("the model's answer was cut off")
	}
	return r.Choices[0].Message.Content, nil
}

// aiOllama: a local model, with the answer held to the JSON schema.
func aiOllama(ctx context.Context, ac aiConf, cats, domains []string) (string, error) {
	base, err := aiOllamaBase(ac.URL)
	if err != nil {
		return "", err
	}
	body := map[string]any{
		"model": ac.Model, "stream": false, "format": aiSchema(cats), "options": map[string]any{"temperature": 0},
		"messages": []map[string]string{{"role": "system", "content": aiSystem}, {"role": "user", "content": aiPrompt(cats, domains)}},
	}
	var r struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		Error string `json:"error"`
	}
	code, err := aiPost(ctx, base+"/api/chat", nil, body, &r)
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "", fmt.Errorf("%s took over %d minutes for 100 domains: a smaller model, or a faster computer, would keep up", ac.Model, int(aiTimeout(ac).Minutes()))
	case err != nil:
		return "", fmt.Errorf("couldn't reach Ollama at %s: %v", base, err)
	case r.Error != "":
		return "", fmt.Errorf("Ollama: %s", r.Error)
	case code != 200:
		return "", fmt.Errorf("Ollama answered %d", code)
	}
	return r.Message.Content, nil
}

func aiPost(ctx context.Context, u string, hdr map[string]string, body, out any) (int, error) {
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", u, bytes.NewReader(raw))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := aiHTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	_ = json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(out)
	return res.StatusCode, nil
}

// aiClaude: the Anthropic API through its Go SDK, with structured output.
func aiClaude(ctx context.Context, ac aiConf, cats, domains []string) (string, error) {
	key := ac.Key
	client := anthropic.NewClient(option.WithAPIKey(key), option.WithRequestTimeout(3*time.Minute))
	schema := aiSchema(cats)
	prompt := aiPrompt(cats, domains)
	msg, err := client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
		Model:        ac.Model,
		MaxTokens:    16000,
		Betas:        []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01},
		Fallbacks:    anthropic.BetaFallbacksParamOfDefault(),
		System:       []anthropic.BetaTextBlockParam{{Text: aiSystem}},
		Messages:     []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(prompt))},
		OutputConfig: anthropic.BetaOutputConfigParam{Effort: anthropic.BetaOutputConfigEffortLow, Format: anthropic.BetaJSONOutputFormatParam{Schema: schema}},
	})
	if err != nil {
		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) {
			switch apiErr.StatusCode {
			case 401, 403:
				return "", errors.New("Anthropic rejected the API key")
			case 429:
				return "", errors.New("Anthropic's rate limit: try again in a minute")
			}
			return "", fmt.Errorf("Anthropic API %d", apiErr.StatusCode)
		}
		return "", fmt.Errorf("couldn't reach Anthropic: %v", err)
	}
	switch msg.StopReason {
	case anthropic.BetaStopReasonRefusal:
		return "", errors.New("Claude declined to categorize these domains")
	case anthropic.BetaStopReasonMaxTokens:
		return "", errors.New("Claude's answer was cut off")
	}
	var text strings.Builder
	for _, b := range msg.Content {
		if t, ok := b.AsAny().(anthropic.BetaTextBlock); ok {
			text.WriteString(t.Text)
		}
	}
	return text.String(), nil
}
