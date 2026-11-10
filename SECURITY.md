# Security policy

HomeOwl holds the keys to your home network, so we take security reports seriously.

## Reporting a vulnerability

**Please don't open a public issue.** Use GitHub's private reporting instead:
**[Report a vulnerability](https://github.com/h4ux/HomeOwl/security/advisories/new)** (Security tab › *Report a vulnerability*).

Please include what you found, how to reproduce it, which version (`homeowl version`) and system you used, and the impact you expect. We aim to reply within 7 days, agree on a fix and a disclosure date with you, and credit you in the release notes if you'd like.

## Supported versions

Security fixes go into the latest release. Please update before reporting.

## How HomeOwl protects your data

- **Secrets** (UniFi, NextDNS and Tailscale keys) live in the system keychain, or in files readable only by your user when no keychain exists (e.g. a headless Linux server). They are never sent to the browser, the MCP client or the logs, and passwords/PSKs in UniFi data are redacted before display.
- **Accounts**: PBKDF2-SHA256 password hashes (600k rounds), random 256-bit session tokens stored only as hashes, HttpOnly + SameSite=Strict cookies, a per-session anti-CSRF token on every change, an Origin check, and login rate limiting.
- **Network**: the `Host` header must be one of the computer's own names/addresses (blocks DNS rebinding); other devices are redirected to HTTPS; strict Content-Security-Policy and security headers.
- **Changes** to UniFi go through an allow-list, are previewed, logged with the user's name and are undoable. The MCP server has its own mode (off / read / guarded / full) and per-client tokens that can be revoked.
- **Analytics**: DuckDB runs without file or network access; the SQL console accepts one read-only statement and always rolls back.

## Hardening tips

- Keep **Settings › Access › home network** off if you only use HomeOwl over Tailscale or on the computer itself.
- Block your **guest and IoT networks** from reaching the computer running HomeOwl.
- Use **Full access (god mode)** for MCP only while you need it, and revoke tokens you no longer use.
- Create **view-only** accounts for family members who don't manage the network.
