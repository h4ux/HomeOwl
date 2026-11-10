# HomeOwl — notes for Claude Code

HomeOwl is a single Go binary that watches a UniFi home network: config review, safe
allow-listed changes, NextDNS/UniFi/syslog analytics in embedded DuckDB, people & categories,
user accounts, optional built-in Tailscale node, and an MCP server. Public open-source repo (MIT).

Start with CONTRIBUTING.md (layout table, dev setup) and ROADMAP.md (what's planned).

## Build, run, test

- Go 1.24+, cgo required (DuckDB is linked in). On macOS: Xcode Command Line Tools.
- `make build-lite` — fast build without Tailscale (`-tags notsnet`).
- `make tidy build` — full build; `tidy` fetches `tailscale.com/tsnet`, which is intentionally
  NOT pinned in go.mod (CI runs the same fetch). Don't commit a go.mod that requires tsnet.
- `./homeowl run --data ./.devdata` → http://127.0.0.1:8555 (`.devdata/` is git-ignored).
  "Preview with demo data" on the sign-in page needs no UniFi console.
- `make test lint` before every commit (gofmt, go vet, go test with `-tags notsnet`),
  plus `for f in web/static/*.js; do node --check "$f"; done`.
- `internal/analytics/engine_test.go` runs the real embedded DuckDB end to end — extend it
  when you touch schema, views, people or Explore SQL.
- UI changes: run the app and look at the page (Playwright works) — check light and dark.

## Rules that matter

- **No personal data in the repo, ever**: no real IPs, MACs, NextDNS profile IDs, tailnet names,
  device or family names, keys or tokens — not in code, tests, docs or screenshots. Use the
  fictional demo data (`web/static/demo.json`, 192.168.1.x, 203.0.113.x, "Alex").
- Commits are authored as `h4ux <h4ux@users.noreply.github.com>`.
- Secrets live only in the system keychain / `<data>/secrets/` via `internal/secrets`.
  They never go to the browser, to MCP clients, or into logs.
- UniFi writes go through `internal/unifi/actions.go` (allow-list, preview, log, undo).
  Only MCP "god mode" may call `Raw`, and it is logged.
- DuckDB is opened with external access and extension autoloading off; the SQL console is
  read-only. Keep it that way.
- Web UI: plain HTML/CSS/JS in `web/static`, no build step, no inline scripts (CSP).
  Every state-changing API call needs the session CSRF token (`X-Audit-Token`).
- `.github/` changes: keep the macOS matrix on a supported runner image; release is published
  when a `v*` tag builds on all five targets.

## Data model hints (analytics)

- `person_devices(person_id, kind, key)` kinds: `ndev` = `<profile id>/<device>` (a NextDNS device
  in one profile), `profile` = a whole NextDNS profile, `mac` = UniFi client, `dns` = legacy
  name-only match. `RefreshDevPerson` expands them into `dev_person` labels; the `dnsq`, `dnsd`
  and `dnsh` views resolve the person, most specific first.
