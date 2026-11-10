// Package categories maps domains to subjects ("Social", "Gaming", …).
//
// Matching order for a root domain (e.g. "tiktokv.com"):
//  1. the user's own rules, most specific first
//  2. the built-in list (exact root, then parent domains)
//  3. keyword hints in the name
//  4. NextDNS tracker flag → "Ads & trackers"
//  5. "Uncategorized"
//
// Rule patterns: "example.com" (and subdomains), "*.example.com" (same), "~word" (any domain containing word).
package categories

import "strings"

var builtin = func() map[string]string {
	m := map[string]string{}
	for _, cat := range Categories { // display order decides which category wins for a domain listed twice
		for _, d := range strings.Fields(builtinLists[cat]) {
			d = strings.ToLower(strings.Trim(d, "."))
			if _, ok := m[d]; !ok {
				m[d] = cat
			}
		}
	}
	return m
}()

type Rule struct{ Pattern, Category string }

func parents(domain string) []string {
	parts := strings.Split(domain, ".")
	out := make([]string, 0, len(parts))
	for i := 0; i < len(parts)-1; i++ {
		out = append(out, strings.Join(parts[i:], "."))
	}
	return out
}

var localSuffixes = []string{".lan", ".local", ".home.arpa", ".localdomain", ".internal", ".in-addr.arpa", ".ip6.arpa"}

// Categorize returns (category, source) where source is rule | builtin | keyword | tracker | none.
func Categorize(root, domain, tracker string, rules []Rule) (string, string) {
	var names []string
	for _, d := range []string{domain, root} {
		d = strings.ToLower(strings.Trim(d, "."))
		if d != "" {
			names = append(names, d)
		}
	}
	if len(names) == 0 {
		return "Uncategorized", "none"
	}
	last := names[len(names)-1]
	for _, s := range localSuffixes {
		if strings.HasSuffix(last, s) {
			return "Local network", "builtin"
		}
	}
	if last == "unifi" || last == "localhost" {
		return "Local network", "builtin"
	}
	bestLen, bestCat := -1, ""
	for _, r := range rules {
		p := strings.ToLower(strings.TrimSpace(r.Pattern))
		if strings.HasPrefix(p, "~") {
			continue
		}
		p = strings.TrimPrefix(p, "*.")
		for _, n := range names {
			if (n == p || strings.HasSuffix(n, "."+p)) && len(p) > bestLen {
				bestLen, bestCat = len(p), r.Category
			}
		}
	}
	if bestLen >= 0 {
		return bestCat, "rule"
	}
	for _, r := range rules {
		if strings.HasPrefix(r.Pattern, "~") {
			w := strings.ToLower(r.Pattern[1:])
			for _, n := range names {
				if strings.Contains(n, w) {
					return r.Category, "rule"
				}
			}
		}
	}
	for _, n := range names {
		for _, cand := range parents(n) {
			if c, ok := builtin[cand]; ok {
				return c, "builtin"
			}
		}
	}
	for _, k := range keywords {
		for _, w := range k.Words {
			for _, n := range names {
				if strings.Contains(n, w) {
					return k.Category, "keyword"
				}
			}
		}
	}
	if tracker != "" {
		return "Ads & trackers", "tracker"
	}
	return "Uncategorized", "none"
}
