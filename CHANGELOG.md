# Changelog

All notable changes are listed here. The project follows [Semantic Versioning](https://semver.org).

## [Unreleased]

## [0.1.13] — 2026-11-11

### Added
- **Alerts** (Records › Alerts, with an unread count): HomeOwl tells you when a device it has never seen joins your network, with buttons to name it, give it to someone, block it or see its details. Settings › Alerts picks what to alert about (your own networks, guest Wi-Fi too, or never) and where else alerts go: a notification on this Mac, phone push with ntfy, or a webhook (Home Assistant, Slack, Discord, n8n). The ntfy and webhook addresses are kept in the keychain, and alerts never carry passwords. "Send a test" checks the setup.
- **Guest Wi-Fi password on a schedule** (Settings › Home): a new password every week or every month, on the day and hour you pick, or "Change it now". Each change goes through the usual allow-list, is logged in Records › Changes and can be undone. You get an alert each time, and the guest card and wifi.local show the new password.
- **Reports** (Records › Reports): Weekly summary, Network health, Internet, Device inventory (also as CSV), Family & usage, Security, and Changes & alerts. Pick 7 or 30 days, then print, save as PDF or download a copy.

### Fixed
- **Easy names** (hi.local, wifi.local):
  - Port 80 is now on by default, so the plain names work without typing the port.
  - HomeOwl also answers over IPv6, which some phones use to look up names.
  - It announces the names when it starts.
  - Settings › Access shows whether it's working: how many questions it heard and answered, the last device that asked, and any error, with a hint about the Mac's Local Network permission. It also gives IP-address links that always work, and the guest card opens by IP address too.

## [0.1.12] — 2026-11-11

### Added
- **Groups** next to people, for things everyone uses or nobody owns: the living-room TV, servers, smart-home gear. They own devices and filter every chart like a person does, but never count in Who's home. Quick-start buttons create "TV & streaming", "Servers", "Smart home" or "Printers" with matching devices suggested.
- **Easy names on the home network** (Settings › Access): HomeOwl answers Bonjour/mDNS for names you choose. `http://hi.local` opens the device check-in page and `http://wifi.local` shows a guest Wi-Fi card (network, password and join code, big enough to leave open on a tablet). Each name serves only its own page, over plain HTTP. An option to also answer on port 80 lets the names work without `:8555`. The guest card is off until you give it a name.
- **Rename devices** in UniFi from the Clients list, from People & groups and from Who's home (✎). The change is previewed, logged and undoable, like every other change.

### Changed
- **Analytics has its own section** in the side menu (Overview, Explore, DNS, Devices, Traffic, Security, Categories, Logs, SQL) instead of tabs inside a Family page.
- **Family › People & groups**: avatar cards; each card's UniFi devices are shown as chips and its charts fold away; "Not assigned yet" has a "Give to…" menu on each device.
- **A friendlier device picker** for people, groups and Who's home:
  - The devices you picked appear as chips.
  - One search box covers name, IP address, maker and type ("phone", "TV").
  - Suggestions come from names that match, from devices that checked in for that person and, for groups, from the right device types.
  - A UniFi client and its NextDNS device are folded into one entry, so each device is picked once.
  - Who's home lists phones first.

## [0.1.11] — 2026-11-11

### Changed
A cleaner, shorter interface: less text, the list before the charts, and the fix button next to each problem.
- **Today** (new, under Home, and where HomeOwl now opens): what needs you, the internet, who's home, a glance at the cameras, the UPS and the network, each linking to its full page.
- **Overview** opens with one status line ("2 things need you" or "All good"), with a button for each item and a small configuration-score ring and severity bar beside it. Cards with no data are hidden, and the per-network details fold away.
- **Suggestions** are one line each: severity, title, area and a button (Fix, Debug or Show). The explanation opens on click, and findings with the same cause are merged ("+ 1 more like it"). The filters are Needs you / Worth doing / Tips / All, plus an area menu. The Suggestions page went from about 2,200 words to under 100.
- **Lists are compact tables** everywhere: click a column header to sort, again to reverse. Secondary details (model, IP address) sit in a small line under the name. On phones each row becomes a short card.
- **Devices**: broken devices are pinned to the top of the list in red, with Debug (and Power-cycle port when that helps) on the row, instead of a separate box that repeated them.
- **Clients**: the list comes first; the charts are under "Insights", which remembers whether you left it open.
- Page explanations moved from a sentence under every title to an ⓘ button beside it.
- Pages with several views (Devices › Topology › Switch ports, Wi-Fi, Networks) list them under the page in the side menu instead of tabs across the top.
- Plainer messages, e.g. "UniFi sees it but isn't managing it" instead of internal API names (still shown in Debug).
- Analytics, when it isn't set up, shows what you'll get and a "Set up analytics" button that opens the right Settings section.

## [0.1.10] — 2026-11-11

### Added
- AI & MCP: a ready-made prompt to paste into an AI assistant (Claude Code, Codex, Cursor, desktop apps) that connects it to HomeOwl, checks the connection and tells it to ask before any change. Tabs with the Claude Code command, the Claude Desktop config and notes for ChatGPT, Grok and other web apps. "Connection prompt" in Settings › AI & MCP shows it at any time, with a placeholder for the token.

## [0.1.9] — 2026-11-11

### Added
- **Family › Who's home.** Tag the device each person takes with them, usually their phone. They count as home while it's connected to your Wi-Fi, and as away once it has been gone for 10 minutes (adjustable), so a phone that drops Wi-Fi while asleep doesn't count as leaving. The page shows:
  - who's home and since when, a strip of today for each person, time at home over 1, 7 or 30 days, and every arrival and departure.
  - The history is optional (on by default), kept for 7 to 365 days on this computer only, and can be cleared or turned off, which deletes it. A tagged device also counts as that person's in Analytics.
  - Hints about private Wi-Fi addresses ("Fixed", not "Rotating").
  - The menu bar's "who's home" and the new MCP read tool `whos_home` use the same data.
- Camera wall: every camera, or the ones you pick, in a grid that fills the window or the whole screen (Auto, 1, 2×2, 3×3 or 4×4). Click a camera for a single large, sharper view. Fewer cameras refresh faster (about every 0.7 s for one).
- Float a camera: a small window that stays on top while you work. In Chrome and Edge it's a floating page; other browsers use video picture-in-picture. Single cameras also open full screen.
- Cameras: HomeOwl looks for the console that runs Protect among the Ubiquiti devices on your network (asking them without a key, read-only) and suggests it, so you only paste an API key.

### Fixed
- Camera health: a long "Detects" list squeezed its label into one letter per line. Labels now stay on one line, and long lists show the first five and "+N more".
- The demo data contained a real first name in a device name; it is now fictional.

## [0.1.8] — 2026-11-11

### Added
- Cameras can come from a different console than Network. A Cloud Gateway (UCG Ultra, Express…) can't run UniFi Protect, so the cameras live on a UNVR, a Cloud Key or a Dream Machine. Enter that console's address and an API key created on it, on the Cameras page. HomeOwl tests both, keeps the key in the keychain, and reads cameras, pictures and the event stream from there. You can switch back at any time.

### Fixed
- Cameras on a console without Protect: the page said “Protect is running but has no cameras yet” and “lost Protect's event stream (HTTP 200)”. The console was answering with its web page, not Protect's API. HomeOwl now says Protect isn't on this console and offers to connect the one that runs it.

## [0.1.7] — 2026-11-11

### Added
- macOS menu bar icon. HomeOwl shows an owl in the menu bar (on by default when someone is signed in to the Mac; turn it off in **Settings › System**). Click it for a small window with:
  - internet (each WAN with latency) and the network (devices offline, important suggestions), the UPS (on mains or battery, charge, time left), who's home (people whose devices are connected) and check-ins waiting for you. Each line opens that page in HomeOwl.
  - the guest Wi-Fi code in your chosen style, Open HomeOwl, updates (check, or update in one click), and Start at login.
- Five icon styles: owl outline (default), owl with a status dot (amber when something needs a look, red when the internet or power is down), filled owl, colour logo, and owl with a count of things to look at. The owls follow the light or dark menu bar.
- The window is HomeOwl's own page, unlocked by a random code made at each start that only the menu bar knows, from this computer only. It can read the summary and do those few actions, nothing else.
- Links such as `http://127.0.0.1:8555/#guest` open that page directly.

## [0.1.6] — 2026-11-11

### Changed
- UPS: letting HomeOwl shut the computer down is now one step. HomeOwl runs as a normal user (on a Mac, a login item without administrator rights), so it needs permission once. On macOS, **Allow shutdown…** opens the Mac's own password window. On Linux it shows one command to paste, which checks the rule with `visudo` before putting it in place. The page shows whether shutdown is already allowed, and on macOS you can take the permission back. The rule now allows only the exact shutdown command the suggested script runs.

### Fixed
- UPS: the "may HomeOwl shut down?" check said yes for any administrator account, even when a password would still be asked. It now looks for a rule that needs no password.

## [0.1.5] — 2026-11-11

### Added
- Guest Wi-Fi code styles: Classic, Rounded (the new default), HomeOwl blue, Night and Fridge card. Admins pick one on the Guest Wi-Fi page; the printed card uses the same style, and every style scans with iPhone and Android cameras.
- Start with this computer (**Settings › System**): on macOS a switch installs or removes a login item (no admin rights needed). On Linux and Windows, where this needs root or admin, HomeOwl shows the command to run instead.
- Cameras: a **Live** switch refreshes pictures every 2 seconds, and clicking a camera opens a live view that refreshes about once a second. Each picture shows how old it is. When Protect refuses a picture, the camera tile says why (for example, an API key that can't read Protect) instead of showing a broken image. HomeOwl now asks Protect for at most one picture per camera about once a second, however many pages are open.
- The sidebar shows the running version next to the update status (“HomeOwl v0.1.5 · up to date”, or “Update available” with current → new).

### Changed
- New navigation. An icon strip picks an area (Network, Home, Family, Records, System), the sidebar lists that area's pages, and related pages share tabs (Devices · Topology · Switch ports, Wi-Fi · Neighbour Wi-Fi, Networks · Security · NextDNS & Tailscale). On phones, both become scrollable rows at the top.
- Tables are now lists. Each row shows its name, labelled details and key numbers on the right, with a filter and a sort menu. This applies to Devices, Clients, Wi-Fi, Firmware, Users, MCP tokens, the Changes log and the other pages.
- Settings are split into sections with a side menu: Connection, Access, Users, Analytics, Home, AI & MCP and System. Your last section is remembered.

### Fixed
- Cameras, video recorders (NVR, Cloud Key) and UniFi Access hubs are now shown as Camera, Video recorder and Door access instead of “Other” on Devices, Firmware and Topology.
- The Wi-Fi SSID list no longer wraps names, bands and the on/off button awkwardly.

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
