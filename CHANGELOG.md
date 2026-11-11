# Changelog

All notable changes are listed here. The project follows [Semantic Versioning](https://semver.org).

## [Unreleased]

## [0.1.4] — 2026-11-11

### Added
- UPS page for NUT (Network UPS Tools) servers. You enter the server's address, port, user and password (the password is tested, then kept in the keychain) and optionally the UPS name.
  - It shows every UPS on the server: status in plain words, battery charge, runtime left, load and watts, input and output voltage, and all NUT values.
  - It keeps a reading every minute for 7 days and events for a year, with charts, a list of power cuts (how long, lowest charge, whether this computer shut down) and an event log. Viewers can see status and history; the new MCP read tool `ups` shows the same.
  - It works like `upsmon`: it can register as a secondary (`LOGIN`), so the NUT primary waits for this computer, and it fires the events on battery, back on mains, battery low, forced shutdown, lost the UPS, reaches the UPS again, replace battery, and “time to shut down”. By default that last one means battery low while on battery; it can also be N minutes on battery, a charge level or a runtime left. A forced shutdown, or losing the UPS while it's on battery, always counts.
  - Admins pick a script per event, optionally run only if the event still holds after a delay (skipped if the power comes back). Scripts get `NOTIFYTYPE` and `UPSNAME` like upsmon's NOTIFYCMD, plus `HOMEOWL_UPS_*` values, and run for at most 2 minutes.
  - For safety, scripts must be files in HomeOwl's `ups-scripts` folder that only its user can change (no links). The web page can only pick one, or save one of the built-in suggestions.
  - Suggested scripts for the OS HomeOwl runs on: a graceful shutdown (macOS: sudo or System Events; Linux: systemctl poweroff via a one-line sudoers rule HomeOwl shows; Windows: `shutdown /s` with a power-failure reason) and a notification script. Both only describe what they would do when run with the Test button (`HOMEOWL_UPS_TEST=1`). A test needs changes to be unlocked and is written to the Changes log.

## [0.1.3] — 2026-11-10

### Added
- Doors page for UniFi Access, using its own API token, which HomeOwl tests and then keeps in the keychain. It shows lock and door-sensor state, entries today, and the last 7 days of who came in, how (card, PIN, phone…), and who was refused. Admins can unlock a door remotely; this is off by default, previewed and confirmed each time, needs the change lock open, and is written to the Changes log (it can't be undone, and the door locks again by itself). Writes to apps other than Network now go through the same guarded, logged path (`Changes.External`).
- Tests keep secrets in their temporary data folders and never touch the system keychain.
- Cameras page for UniFi Protect 5.3 or newer, using the same API key. It shows the latest picture from each camera (refreshed every 15 seconds while the page is open, larger on click), which cameras are online, counts for today (people, vehicles, packages, doorbell rings), and a timeline of detections, sounds and doorbell rings by day, filtered by type and camera. The camera health table shows state, what each camera detects, video mode and microphone. HomeOwl records Protect’s event stream while it runs and keeps 30 days in `protect-events.json`. Pictures are not stored, and nothing is changed in Protect. Only admins can see it unless “Viewers can see the Cameras page” is turned on in Settings. There is also a new MCP read tool, `cameras`.
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
