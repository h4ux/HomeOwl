# Contributing to HomeOwl

Thanks for helping! Bug reports, ideas, docs and code are all welcome.

## Before you start

- **Bugs and ideas**: open an [issue](https://github.com/h4ux/HomeOwl/issues/new/choose). For bigger changes, please discuss the idea in an issue first.
- **Security problems**: never in public — see [SECURITY.md](SECURITY.md).
- Never paste API keys, passwords, tokens, your public IP or other personal data into issues, logs or screenshots. HomeOwl's "Save report" output redacts passwords, but check it before sharing.

## Development setup

You need Go 1.24+ and a C compiler (cgo, for the embedded DuckDB): Xcode Command Line Tools on macOS, `build-essential` on Linux, MinGW-w64 on Windows.

```sh
git clone https://github.com/h4ux/HomeOwl && cd HomeOwl
make build-lite                  # fast build without built-in Tailscale
./homeowl run --data ./.devdata  # http://127.0.0.1:8555
make test lint
```

`make tidy build` builds with the built-in Tailscale node (downloads `tailscale.com` once).

No UniFi console at hand? Click **Preview with demo data** on the sign-in page.

## Project layout

| Path | What's there |
|---|---|
| `cmd/homeowl` | command line, service install, MCP stdio bridge |
| `internal/server` | web server, login, JSON API, MCP tools |
| `internal/unifi` | UniFi API client, data collection, allow-listed actions |
| `internal/analytics` | DuckDB schema, NextDNS/UniFi/syslog import, Explore queries |
| `internal/auth` | users, password hashing, sessions |
| `internal/mcp` | Model Context Protocol (JSON-RPC) server |
| `internal/tsnetx` | built-in Tailscale node (`-tags notsnet` leaves it out) |
| `internal/categories` | domain → subject categories |
| `internal/mdns` | answers mDNS for the easy names (hi.local, wifi.local) |
| `web/static` | the web UI (plain HTML/CSS/JS, no build step) |
| `scripts` | install scripts |

## Guidelines

- **Safety first.** Anything that changes the UniFi console must go through the action allow-list (`internal/unifi/actions.go`): previewed, logged, and undoable where possible. New MCP tools that change things belong in the *guarded* level.
- **No secrets in the browser.** API keys and passwords stay on the server; redact anything that looks like a secret.
- **Keep it dependency-light.** Prefer the Go standard library; the UI uses no frameworks.
- **Plain language.** HomeOwl is for families, not only network engineers — explain what a setting does and why.
- Run `gofmt`, `go vet` and `go test` (`make lint test`) before opening a pull request, and describe how you tested.
- Device icons and artwork must be original — no vendor logos or product photos.

## Releases

Maintainers tag `vX.Y.Z`; CI builds macOS (Apple Silicon + Intel), Linux (x86-64 + ARM64) and Windows binaries, publishes them with checksums and the install scripts.

By contributing you agree that your contributions are licensed under the [MIT License](LICENSE).
