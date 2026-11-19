package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"

	"github.com/h4ux/homeowl/internal/categories"
)

// Suggesting categories with Claude (Family › Categories). Optional: an admin adds their own Anthropic API key (kept
// in the keychain like every other secret, never sent to the browser). HomeOwl then sends Claude the uncategorized
// domain names — only the names: no devices, people, counts or times — with the list of categories, and shows what
// comes back for the admin to accept or change. Nothing is saved until they do.

const aiKeySlot = "anthropic-api-key"

const aiModel = "claude-opus-5-5"

var aiKeyRE = regexp.MustCompile(`^sk-ant-[A-Za-z0-9_\-]{20,200}$`)

// a domain name as the Categories tab lists them
var aiDomainRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9\-]{0,62}\.)+[a-z0-9\-]{2,63}$`)

type aiSuggestion struct {
	Domain   string `json:"domain"`
	Category string `json:"category"`
	Why      string `json:"why"`
}

const aiChunk = 100 // domains per request

func (a *App) aiCategoriesOp(op string, body map[string]any) (any, error) {
	switch op {
	case "ai-status":
		return map[string]any{"key": a.Sec.Get(aiKeySlot) != "", "model": aiModel}, nil
	case "ai-key":
		k := strings.TrimSpace(s(body, "key"))
		if k == "" {
			a.Sec.Delete(aiKeySlot)
			return map[string]any{"key": false}, nil
		}
		if !aiKeyRE.MatchString(k) {
			return nil, errors.New("that isn't an Anthropic API key (it starts with sk-ant-)")
		}
		if err := a.Sec.Set(aiKeySlot, k); err != nil {
			return nil, err
		}
		return map[string]any{"key": true}, nil
	}
	key := a.Sec.Get(aiKeySlot)
	if key == "" {
		return nil, errors.New("add an Anthropic API key first")
	}
	var doms []string
	seen := map[string]bool{}
	for _, x := range anyArr(body["domains"]) {
		d, _ := x.(string)
		d = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(d), "."))
		if aiDomainRE.MatchString(d) && !seen[d] {
			seen[d] = true
			doms = append(doms, d)
		}
	}
	if len(doms) == 0 {
		return nil, errors.New("no domains to categorize")
	}
	if len(doms) > 300 {
		doms = doms[:300]
	}
	cats := aiCategoryList(a)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	var out []aiSuggestion
	for i := 0; i < len(doms); i += aiChunk {
		part, err := aiCategorize(ctx, key, cats, doms[i:min(i+aiChunk, len(doms))])
		if err != nil {
			if len(out) > 0 { // keep what came back
				return map[string]any{"suggestions": out, "categories": cats, "error": err.Error()}, nil
			}
			return nil, err
		}
		out = append(out, aiKeepKnown(part, seen, cats)...)
	}
	return map[string]any{"suggestions": nz(out), "categories": cats}, nil
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

// aiCategorize asks Claude (a var, so tests don't call the API).
var aiCategorize = func(ctx context.Context, key string, cats, domains []string) ([]aiSuggestion, error) {
	client := anthropic.NewClient(option.WithAPIKey(key), option.WithRequestTimeout(3*time.Minute))
	enum := append(append([]string{}, cats...), "Unknown")
	schema := map[string]any{
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
	prompt := "Categories:\n- " + strings.Join(cats, "\n- ") + "\n\nDomains:\n" + strings.Join(domains, "\n")
	msg, err := client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
		Model:     aiModel,
		MaxTokens: 16000,
		Betas:     []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01},
		Fallbacks: anthropic.BetaFallbacksParamOfDefault(),
		System: []anthropic.BetaTextBlockParam{{Text: "You sort internet domain names, seen in a family's DNS logs, into categories for a home network dashboard. " +
			"For each domain, pick the one category from the list that fits the service it belongs to (a CDN or API domain goes with the app or company it serves). " +
			"Answer Unknown when you don't recognise the domain or it could be several things; don't guess. Keep each why to a few words (what the domain is)."}},
		Messages:     []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(prompt))},
		OutputConfig: anthropic.BetaOutputConfigParam{Effort: anthropic.BetaOutputConfigEffortLow, Format: anthropic.BetaJSONOutputFormatParam{Schema: schema}},
	})
	if err != nil {
		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) {
			switch apiErr.StatusCode {
			case 401, 403:
				return nil, errors.New("Anthropic rejected the API key")
			case 429:
				return nil, errors.New("Anthropic's rate limit: try again in a minute")
			}
			return nil, fmt.Errorf("Anthropic API %d", apiErr.StatusCode)
		}
		return nil, fmt.Errorf("couldn't reach Anthropic: %v", err)
	}
	switch msg.StopReason {
	case anthropic.BetaStopReasonRefusal:
		return nil, errors.New("Claude declined to categorize these domains")
	case anthropic.BetaStopReasonMaxTokens:
		return nil, errors.New("Claude's answer was cut off")
	}
	var text strings.Builder
	for _, b := range msg.Content {
		if t, ok := b.AsAny().(anthropic.BetaTextBlock); ok {
			text.WriteString(t.Text)
		}
	}
	var res struct {
		Suggestions []aiSuggestion `json:"suggestions"`
	}
	if err := json.Unmarshal([]byte(text.String()), &res); err != nil {
		return nil, fmt.Errorf("couldn't read Claude's answer: %v", err)
	}
	var out []aiSuggestion
	for _, x := range res.Suggestions {
		if x.Category != "Unknown" {
			out = append(out, x)
		}
	}
	return out, nil
}
