# Changelog

All notable changes are listed here. The project follows [Semantic Versioning](https://semver.org).

## [Unreleased]

### Added
- Firmware page: every device's firmware, which updates are waiting and since when, console (UniFi OS) and Network app versions, automatic-update setting, a guarded Update button per device (not the gateway), and firmware changes of the last 30 days.
- Config history: HomeOwl keeps a copy of the UniFi configuration (networks, Wi-Fi, firewall, port forwards, routes, settings, named clients, device settings and firmware) whenever it changes, and shows what changed since yesterday, in the last week or month, or in any snapshot — with changes made through HomeOwl listed with who made them. Live counters are left out; passwords stay hidden (length plus a short local fingerprint, so a new password still shows up as a change).
- Device check-in: a “Hi HomeOwl” page (`/hi`, also over plain HTTP on the home network, with a QR code in HomeOwl) where a device says what it is and whose it is. HomeOwl matches its IP to the UniFi client; admins approve in **Check-ins**, which sets the UniFi name (previewed, undoable) and assigns the device to the person (creating them if needed). Guest-network devices can't check in; requests are rate-limited.
- Guest Wi-Fi page: network name, password and a scan-to-join QR code (built in, no external service), a printable card, and a new password through the allow-listed actions (previewed, logged without the password, undoable). Admins can mark other SSIDs as guest networks.
- Update check: HomeOwl looks for new releases twice a day (can be turned off), shows “Update available” with the release notes, and updates in one click where it can replace its own program file — otherwise it shows the command (`homeowl update`, new). The running program restarts by itself after its file is replaced.
- Overview shows every internet connection (WAN1, WAN2…) with provider, status, IP, latency and uptime, and the provider's logo (fetched from the provider's website you give; a lettered badge until then).
- One program for macOS, Linux and Windows, written in Go with DuckDB built in; install scripts and background services for each system.
- User accounts (admin / view-only) with sign-in, first-run setup and a one-time setup code for remote setup.
- Access from the home network over HTTPS (self-signed), and a built-in Tailscale node with its own `*.ts.net` address and automatic sign-in for linked tailnet users.
- UniFi connection, access, users, AI & MCP and analytics settings in the Settings page.
- MCP server (HTTP at `/mcp` and `homeowl mcp` over stdio) with read, guarded and full-access modes and revocable tokens.
- Device icons for UniFi gear and common client types.
- NextDNS: several accounts, all profiles imported automatically, newest data first.

### Changed
- DuckDB 2.0 (driver `github.com/duckdb/duckdb-go/v2`). Databases from earlier versions open as they are.
- Renamed from "Big Mama" / "UniFi Audit". Settings, keys and data from those versions are moved over automatically.
- Threat detections are also read from syslog and the UniFi system log on UniFi versions without the threat API.
