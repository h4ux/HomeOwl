# Roadmap

What's planned, roughly in this order. Ideas and help are welcome — open an issue.

## Phase 1 — foundation ✅
Go version with built-in DuckDB, accounts and login, home-network HTTPS and built-in Tailscale, MCP server, install scripts, device icons.

## Phase 2 — more of the UniFi family
- **Cameras (UniFi Protect)** — camera grid with live snapshots, motion / person / vehicle / package detections timeline, camera health (offline, recording, storage), doorbell rings.
- **Doors (UniFi Access)** — door and lock status, entry log, guarded remote unlock.
- **Config history** — nightly UniFi backups and a "what changed since yesterday" diff.
- **Firmware watch** — pending updates with release notes, guarded one-click updates.

## Phase 3 — family & network health
- **Bedtime & homework modes** — per-person schedules that pause internet or block categories (UniFi traffic rules or NextDNS profiles).
- **Weekly family digest** — screen time per person, top apps, late-night use, new devices (email or push).
- **Who's home** — presence from phones on Wi-Fi (also for Home Assistant).
- **Device check-in** — a "Hi HomeOwl" page (`http://hi.home` + QR code) where family members name their own device and choose its owner; admin approval sets the UniFi alias.
- **"Plug it in now" pairing** — catch a new device the moment it joins and name it (for plugs, bulbs, cameras).
- **Device identification** — UniFi fingerprint, DHCP name, vendor and DNS behaviour → device type and icon; an "unknown devices" queue.
- **Internet quality** — scheduled speed tests, latency and outage history, "ISP or us?" verdict.
- **New-device alerts** — Telegram, ntfy, Pushover or email, with approve / quarantine.
- **Wi-Fi planner** — channel use and interference over time, weak spots, roaming problems.
- **Exposure check** — what's reachable from the internet (port forwards, UPnP), firmware advisories.
- **Guest Wi-Fi** — QR codes and vouchers.

## Phase 4 — everywhere
- **Phone app** — installable web app (PWA) with push notifications.
- **Ask HomeOwl** — a chat that uses the MCP tools, with Claude (API key), any OpenAI-compatible service, or a local Ollama model.

## Later / ideas
Pi-hole and AdGuard Home support, NextDNS allow/deny lists from HomeOwl, Home Assistant / MQTT sensors, multi-site support.
