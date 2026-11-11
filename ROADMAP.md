# Roadmap

What's planned, roughly in this order. Ideas and help are welcome — open an issue.

## Phase 1 — foundation ✅
Go version with built-in DuckDB, accounts and login, home-network HTTPS and built-in Tailscale, MCP server, install scripts, device icons.

## Phase 2 — more of the UniFi family
- **Cameras (UniFi Protect)** — ✅ a Cameras page: the latest picture per camera, which cameras are online, and a timeline of detections and doorbell rings recorded from Protect’s event stream (30 days), plus a Live view built from snapshots. Next: true video (RTSPS streams, which have to be turned on in Protect), thumbnails per event, recording and storage health (not in Protect’s official API yet), and alerts.
- **Doors (UniFi Access)** — ✅ a Doors page: lock and door status, who came in over the last 7 days (and who was refused), and a guarded remote unlock (off by default, logged). Next: live updates from Access's event stream, and a doorbell→door link with the Cameras page.
- **UPS (NUT)** — ✅ a UPS page for any UPS on a NUT server: status, battery and load history (7 days), power cuts and events (a year), and upsmon-style actions: scripts from HomeOwl's scripts folder on on battery, back on mains, battery low, forced shutdown, lost UPS, and a configurable “time to shut down” (with suggested shutdown and notification scripts for macOS, Linux and Windows). Next: alerts to phones, UPS self-test and beeper commands (guarded), and running HomeOwl as the NUT primary for a USB UPS.
- **Config history** — ✅ a snapshot of the UniFi settings whenever they change, and a “what changed since yesterday” diff (passwords hidden; HomeOwl's own changes attributed). Next: optional downloads of UniFi's own `.unf` backups.
- **Firmware watch** — ✅ a Firmware page: what's waiting and for how long, console and Network app versions, guarded one-click updates per device, and firmware changes from the config history. Next: release notes fetched per model, staged “one AP at a time” updates.

## Phase 3 — family & network health
- **Bedtime & homework modes** — per-person schedules that pause internet or block categories (UniFi traffic rules or NextDNS profiles).
- **Weekly family digest** — screen time per person, top apps, late-night use, new devices (email or push).
- **Who's home** — presence from phones on Wi-Fi (also for Home Assistant).
- **Device check-in** — ✅ a “Hi HomeOwl” page (`/hi` + QR code) where family members name their own device and choose its owner; admin approval sets the UniFi alias and the person. Next: a `hi.home` short name.
- **"Plug it in now" pairing** — catch a new device the moment it joins and name it (for plugs, bulbs, cameras).
- **Device identification** — UniFi fingerprint, DHCP name, vendor and DNS behaviour → device type and icon; an "unknown devices" queue.
- **Internet quality** — scheduled speed tests, latency and outage history, "ISP or us?" verdict.
- **New-device alerts** — Telegram, ntfy, Pushover or email, with approve / quarantine.
- **Wi-Fi planner** — channel use and interference over time, weak spots, roaming problems.
- **Exposure check** — what's reachable from the internet (port forwards, UPnP), firmware advisories.
- **Guest Wi-Fi** — ✅ join QR code in five styles, printable card and password rotation; next: hotspot vouchers.

## Phase 4 — everywhere
- **Mac menu bar** — ✅ an owl with status, who's home, the guest code and updates. Next: notifications (power cut, internet down, new device), and a Windows tray icon.
- **Phone app** — installable web app (PWA) with push notifications.
- **Ask HomeOwl** — a chat that uses the MCP tools, with Claude (API key), any OpenAI-compatible service, or a local Ollama model.

## Later / ideas
Pi-hole and AdGuard Home support, NextDNS allow/deny lists from HomeOwl, Home Assistant / MQTT sensors, multi-site support.
