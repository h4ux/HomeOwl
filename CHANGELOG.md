# Changelog

All notable changes are listed here. The project follows [Semantic Versioning](https://semver.org).

## [Unreleased]

## [0.1.46] — 2026-11-14

- Wi-Fi sign-in check: the "whose password" answer now counts the devices actually on a private-password (PPSK)
  network. With none on it, it says to join one and check again instead of guessing. It also compares the field names
  those devices have with everyone else's (names only, never values), so a key field under any name is found.

## [0.1.45] — 2026-11-14

### Added
- **Wi-Fi sign-in check** (Settings › Device profiles). A read-only look at what your UniFi console offers for knowing whose device it is by its Wi-Fi login instead of its MAC address:
  - private passwords per person (PPSK), and whether UniFi records which one a device used;
  - the built-in RADIUS server and Enterprise Wi-Fi, and whether UniFi shows each device's login;
  - the guest portal, for an onboarding network that sends new devices to HomeOwl.
  It only reads, and no password, secret or login name leaves the console: only which settings exist, whether they're on, and counts.

## [0.1.44] — 2026-11-14

### Added
- **"Comes and goes together"** owner clue. A device that joined and left the home Wi-Fi with one person's linked devices (within 10 minutes, at least 8 times in the last 30 days, 75% or more of its arrivals and departures, and clearly more than with anyone else) is suggested as theirs, for example "Arrived and left with Sam's iPhone 23 of 25 times". Devices that never leave home, and people who always go out together, give no guess.
- Every owner suggestion shows **how sure it is in %**: measured for "comes and goes together", an estimate of how often that kind of clue is right for the others. Two clues pointing at the same person add a little. "Link the … that are 85%+ sure" links the surest at once.

## [0.1.43] — 2026-11-14

### Added
- **Whose device is this?** (Family › People & groups, admins). HomeOwl suggests an owner for UniFi devices nobody owns yet, says why and how sure it is, and links nothing until you click **Link** (or **Link the sure ones**). The clues:
  - the same name as a NextDNS device already linked to someone;
  - someone's phone or tablet back with a new private Wi-Fi address (same host name, never online at the same time);
  - a person's first name in the device's name or host name ("Alexs-iPhone", "alex-mbp");
  - a TV, speaker, printer, server or smart-home device, offered to the matching group.
- "Someone else…" links the device to another person; "Not Alex's" stops that guess coming back.

## [0.1.41] — 2026-11-14

### Fixed
- **Check-in address uses your domain.** Family › Check-ins shows (and puts in the QR code) the nicest address that works: the Cloudflare check-in link, then the home-domain name, then HomeOwl's certificate name, then the .local name. The IP addresses move to "Other addresses".

## [0.1.40] — 2026-11-13

### Added
- **App logos** next to apps on the Apps page, on People cards and in the Apps report. The first time a logo is needed, HomeOwl fetches the icons of every app in its list at once through DuckDuckGo's icon service and keeps them in `<data>/app-logos/`. This avoids looking up the apps' own sites (which would show in NextDNS as if HomeOwl's computer used them) and tells nobody which apps the family uses. An app without an icon shows its first letter; failed icons are tried again a day later.

## [0.1.39] — 2026-11-13

### Added
- **Home Screen icon per profile type** (Settings › Device profiles). The icon that iPhone and iPad get with their setup profile can open the device's check-in page (as before), open HomeOwl itself, or be left out. A HomeOwl icon opens by HomeOwl's trusted name and runs full screen when there's a Let's Encrypt certificate; otherwise it opens HomeOwl's address in Safari. Changing the choice shows "Update ready" on the device's page.

## [0.1.38] — 2026-11-13

### Added
- **Top apps on each person's card** (Family › People & groups). Each app links to Explore for that person and app; an app NextDNS mostly blocks is marked.
- **Apps report** (Records › Reports). It shows which apps were used, on how many days and for how long. Click an app to see who used it and on which devices. It also covers what stands out (dating apps, apps used between midnight and 5 am, apps NextDNS keeps blocking), the share of each kind of app, and the apps you block with their schedules.
- The Family & usage report lists each person's apps.

### Fixed
- Explore's day-and-hour heatmap ignored the app filter and showed everything.


## [0.1.37] — 2026-11-13

### Security
- **Every admin action now needs a POST with the session token.** Before, older admin routes (changes mode, disconnecting, users, MCP tokens, updates, undo, guest Wi-Fi rotation, door unlocks, alerts, analytics settings and others) also ran from a plain GET. A GET skips the token check, so a link or an image on another page, opened by a signed-in admin, could have made HomeOwl act. The rule is now the other way round: an admin route needs a POST unless it's on a short list of routes that only read (check-ins, device profiles, MDM status, app blocks, doors, the guest Wi-Fi schedule, camera discovery). A test checks both lists.


## [0.1.36] — 2026-11-13

### Added
- **Family › Apps: which apps are used on the network.** HomeOwl recognises about 50 popular apps (social, messaging, video, games, AI, dating, shopping and more) from the domains devices look up in NextDNS. For each app it shows how many people and devices use it, on how many days, how often it was blocked, and when it was last seen. Filter by person, period (24 hours, 7 or 30 days) and category. Click an app to explore who used it, when and on which devices.
- **Blocking an app, always or on a schedule.** Pick the app and the NextDNS profiles it applies to (each profile shows the people on it). The block can be always on, or on a schedule such as 21:00–07:00 on school nights. HomeOwl adds the app's own domains to each profile's denylist and switches them on and off to follow the schedule, because NextDNS has no schedule for the denylist. It checks every few hours that the entries are still there and retries when NextDNS can't be reached. Unblocking removes only the entries HomeOwl added. Every change is in Records › Changes.
- Explore has an **app** filter.


## [0.1.35] — 2026-11-13

### Added
- **MDM, step 1: the push certificate (Settings › Device profiles › Device management).** Apple needs it before HomeOwl can manage iPhones, iPads and Macs. It comes from mdmcert.download, which signs for organizations only. The card walks through it:
  1. HomeOwl makes the push key (it stays in the secret store) and sends a signing request with your organization's email.
  2. You upload the encrypted file mdmcert.download emails you, and HomeOwl decrypts it.
  3. You download the signed request and upload it at identity.apple.com.
  4. You upload Apple's certificate. HomeOwl checks it matches the key and is an MDM certificate, and shows its topic and expiry.
- **Renewal** keeps the current certificate working until the renewed one is uploaded. Uploading a certificate with a different topic (created instead of renewed) needs confirming, because enrolled devices would have to enrol again.
- **Alerts 30, 7 and 1 days before the certificate expires.**
- Enrolling devices comes in the next release.

### Security
- New admin routes that change something (device profiles, check-in setup, client types, MDM) only run from a POST with the session token, never from a plain GET.

## [0.1.34] — 2026-11-13

### Added
- **Profile types (Settings › Device profiles):** named sets of settings, such as "Kids" or "Adults", that the device setup profile carries besides NextDNS on iPhone, iPad and Mac.
  - **Wi-Fi networks:** the device joins them by itself. The password is read from UniFi when the device downloads its profile and isn't stored by HomeOwl. Enterprise (802.1X) networks can't be included.
  - **A VPN, in one of three kinds:**
    - **IKEv2** (built in), with a shared secret or a user name and password;
    - **L2TP over IPsec** (built in; UniFi gateways can run an L2TP server);
    - **WireGuard** (needs the app): each device gets its own config, pasted in Check-ins › Change….
  - **"Connect by itself when not on the home Wi-Fi"** for IKEv2 and WireGuard.
  - VPN secrets and WireGuard configs live in the secret store, never go back to the browser, and only go into that device's profile.
- **How types reach devices:** a type is attached to people, so their devices get it. A device can be given another type, or none, in Check-ins › Change….
- **What the device's page shows:** what the setup holds (NextDNS, which Wi-Fi, which VPN). It says **Update ready** when any of that changes, including a new Wi-Fi password in UniFi.

### Changed
- A device can be set up with Wi-Fi and VPN only, without a NextDNS profile.

## [0.1.33] — 2026-11-13

### Added
- **Reports › Categories:** what the internet was used for.
  - Each category's share of queries, its time a day and how much of it was blocked.
  - Click a category to drill down to its top sites, who used it and on which devices, with a link into Explore.
  - Also: each person's own top categories, and queries over time.
- **Reports › Blocked sites:** what NextDNS blocked, as a share of all queries.
  - Why it was blocked: each blocklist, tagged as ads and trackers, parental control, security, or your own denylist.
  - Blocks by category, and who gets blocked most, as a share of their own queries.
  - Top blocked sites, each opening to the devices and people that asked for it.
  - The devices with the most blocks, and blocks over time.
  - "What stands out": security blocks, a device with an unusually high share of blocked queries (a misbehaving app or worse), and unusual spikes.
- Both new reports can be filtered to one person and cover 24 hours, 7, 30 or 90 days. "Expand all" opens every drill-down, and printing, PDF and downloads show them open.

## [0.1.32] — 2026-11-13

### Added
- **Alert when a device stops using its setup:** HomeOwl notices when a device whose NextDNS setup worked goes unseen by name, for example because the profile was removed or Private DNS was switched off.
  - It counts only when the device was on the home network for hours with no query under its name, or when nothing came for three days, so a device that's just switched off doesn't trigger it.
  - It checks that NextDNS logs are arriving first.
  - The alert goes wherever your alerts go. Home lists the device under "needs you" until the setup works again, and then a "working again" note follows.
  - There's a switch in Settings › Alerts.
- **Home Screen icon on iPhone and iPad:** the setup profile also adds a HomeOwl icon that opens the device's check-in page, so updates are one tap away. It works at home. It adds no other permissions and can be removed like any app icon.
- **Setup coverage in People:** each person's card shows how many of their phones, tablets and computers use the NextDNS setup, and names the ones that don't (not checked in, not set up, stopped).

### Changed
- When a device's NextDNS profile changes, its old NextDNS name stays with the person in People until the device installs the new setup, so its queries stay labelled in the meantime.

## [0.1.31] — 2026-11-13

### Added
- **Is the device's setup working?** Family › Check-ins has a new "Devices set up" card. It shows each approved device, its NextDNS profile and whether the setup is in use, read from the NextDNS logs under the device's name:
  - **active** (queries in the last day), **quiet**, **not seen** (downloaded but no queries, so probably not installed) or **not set up**;
  - when it was last seen, whether that was at home or away, and how many queries it made today.
- **Change a device's NextDNS profile after approval:** use the card's Change… button. The device's NextDNS name moves to the new profile in People, and the device's check-in page says **Update ready** until the new setup is installed.

### Fixed
- Dialog titles showed names with an apostrophe as `&#39;`.

## [0.1.30] — 2026-11-13

### Added
- **No port needed for the trusted name:** once the Let's Encrypt certificate is in place, HomeOwl also answers HTTPS on the standard port 443, so `https://homeowl.example.com` works without `:8555`.
  - Visiting `http://homeowl.example.com` (port 80) is sent there too.
  - It's on by default, and Settings › Access has a tick-box to turn it off.
  - If port 443 can't be used (another program has it, or Linux needs permission), the card says why and the address keeps its port.

## [0.1.29] — 2026-11-13

### Fixed
- **"Public name: Cloudflare 400: An identical record already exists"**: the certificate request and the public-name sync ran at the same time. When the request finished, it saved an older copy of the state, erasing the ID of the record HomeOwl had just added, so HomeOwl tried to add it again.
  - Each job now changes only its own part of the state.
  - A record HomeOwl made earlier is taken over instead of added twice. This fixes existing setups on the next sync.
  - A record HomeOwl's state still points at but that was deleted on Cloudflare is looked up and added again.

### Added
- **Asks before replacing someone else's record:** when the certificate's name already has a record HomeOwl didn't make (A, AAAA or CNAME), the card shows it and offers **Replace…**.
  - After you confirm, HomeOwl deletes those records and adds its own.
  - Other kinds of records with that name (TXT, MX…) are kept, and the change is logged on the Changes page.

## [0.1.28] — 2026-11-13

### Fixed
- **Let's Encrypt failed with "the check record didn't show up on the domain's name servers in time"** on networks that block, redirect or filter direct DNS queries (NextDNS, a router that intercepts port 53, a broken IPv6 route). The check no longer depends on that:
  - It asks the domain's name servers over IPv4 first, then IPv6.
  - It also asks public DNS over HTTPS (Cloudflare, Google).
  - If nothing can confirm the record within 90 seconds, it carries on and lets Let's Encrypt check with its own resolvers. Cloudflare has already confirmed the record exists.
  - The progress shows what each check saw.

## [0.1.27] — 2026-11-13

### Fixed
- **Let's Encrypt showed no progress:** after Save, the page reloaded before the request had started, so it said "No certificate yet" and never updated. The request is now marked as working before the page reloads.

### Added
- **Let's Encrypt progress and result:** the certificate card now always shows where things stand:
  - **While working:** a progress bar and each step as it happens, for example how many of the domain's name servers already show the check record.
  - **When done:** a message, then the result: *trusted* (with the address, expiry date and an Open button) or *failed* (with Let's Encrypt's reason and a **Try again** button).

## [0.1.26] — 2026-11-13

### Added
- **Edit a client's name, type and note** (the ✎ next to a client, or Edit in its details):
  - **Name and note** are saved in UniFi itself, previewed and undoable like every change.
  - **Type** (phone, TV, smart plug, camera…) sets the device's icon everywhere in HomeOwl. HomeOwl keeps it, because UniFi has no simple field for it. "Automatic" goes back to HomeOwl's guess.
  - Notes show under the client's name in the list.
  - Both the active and the "all known" lists have the edit button.
- **Approved check-ins set the type:** a device approved at check-in gets the type the family member picked (phone, tablet, TV…), unless an admin already chose one.

## [0.1.25] — 2026-11-13

### Added
- **Trusted certificate from Let's Encrypt** (Settings › Access, under the Cloudflare links): choose a name on your domain, like `homeowl.example.com`.
  - **How HomeOwl gets it:** it proves the domain with a short-lived TXT record through the Cloudflare token you already connected, so nothing is opened to the internet. It renews the certificate by itself 30 days before it expires.
  - **HTTPS without warnings:** HomeOwl serves that certificate for its name, and keeps the self-signed one for visits by IP address.
  - **Finding HomeOwl by that name:** HomeOwl adds the name to your NextDNS profiles, like the other home names.
    - You can optionally also publish it in public DNS, pointing to HomeOwl's home address.
    - HomeOwl never touches a record of that name it didn't create.
  - **Signed device setup profiles:** profiles are now signed with the certificate, so iPhones show them as **Verified**.
  - **Where the keys live:** the account key and the certificate's private key are kept with the other secrets, in the keychain or `<data>/secrets`.
  - **Logging:** requests and DNS changes appear on the Changes page.

## [0.1.24] — 2026-11-13

### Fixed
- **Device setup in Chrome (and other browsers) on iPhone/iPad**: iOS installs profiles only from Safari, so these browsers just saved the file.
  - When the check-in page is opened in Chrome, Firefox, Edge or an app's built-in browser, it now says so.
  - It shows an **Open this page in Safari** button, and a link to copy.

## [0.1.23] — 2026-11-13

### Added
- **Device setup after check-in**: when approving a check-in, you can pick a NextDNS profile. It defaults to the one that person's devices already use.
  - Once approved, the device's own "Hi HomeOwl" page offers to set it up with encrypted DNS through that profile, under the device's name:
    - **iPhone, iPad, Mac:** a configuration profile to install.
    - **Android:** the Private DNS host name.
    - **Windows:** the DNS-over-HTTPS template.
  - NextDNS then logs the device by name at home, on mobile data and on other Wi-Fi, and those logs count as that person's.
  - The profile contains no keys or passwords, the person can remove it at any time, and only the device itself, on the home network, can download it.
  - Installing it again replaces the old one.
  - Check-ins › Recently decided shows which devices downloaded their profile.

## [0.1.22] — 2026-11-12

### Changed
- **Suggestions leave IoT networks alone**: an SSID whose name, or whose network's name, looks like smart-home gear (IoT, cameras, TVs, Alexa…) no longer gets "could use WPA3" or "Fast roaming is off", because many such devices can't connect with those turned on. Clients on those networks no longer count towards "clients with weak signal".

## [0.1.21] — 2026-11-12

### Changed
- **Navigation takes much less room**:
  - The icon rail stays on the left. Each area's pages are now a row of tabs at the top, and pages with several views (Devices › Topology › Switch ports) get a second, smaller row.
  - Settings sections are a row of pills instead of a third column.
  - The console status, version, lock, Refresh, Save report and Sign out sit in one slim bar across the top.
  - Pages get nearly the full width of the window.

## [0.1.20] — 2026-11-11

### Fixed
- **Cloudflare "only from home" now covers all of the home's internet addresses**. Before, a visit could miss the rule and end in Cloudflare's error 522. The rule now includes:
  - what Cloudflare sees from HomeOwl over IPv4 and over IPv6 (with fallbacks that don't depend on DNS);
  - the address of every WAN on the UniFi gateway (a second internet line or failover);
  - the IPv6 network (/56) of this computer's own addresses, so every device and VLAN at home matches.
  The rule is updated when any of them changes.

## [0.1.19] — 2026-11-11

### Added
- **Cloudflare links check themselves**: after each change HomeOwl opens each link the way a browser at home would, and shows "works" or what went wrong. For example, Cloudflare's error 522 (the redirect rule didn't apply to this visit), or another rule on the domain sending visitors elsewhere first. A failing link is checked again every 10 minutes.

## [0.1.18] — 2026-11-11

### Fixed
- **Cloudflare links always lead to the home network**: the redirect target is HomeOwl's home-network address, over http, with the page path (`http://192.168.1.10:8555/wifi`).
  - A public address is never used as a target.
  - Settings shows where each link leads, and you can set HomeOwl's address yourself if the one found automatically is wrong.
  - Your home's public address only appears in the rule's "only from" condition.

## [0.1.17] — 2026-11-11

### Added
- **Links on your own domain with Cloudflare** (Settings › Access): connect a Cloudflare API token and pick your domain. HomeOwl adds `wifi.<your domain>` and `hi.<your domain>`.
  - Each one is a proxied name plus a redirect rule that sends the visitor on to HomeOwl at home, so a real domain works in every browser and on every device.
  - By default it only redirects visits from your home's internet address.
  - HomeOwl updates the rules when its own address or your internet address changes.
  - It only touches what it made. "Disconnect" removes those and forgets the token. Each change is logged in Records › Changes.
- The guest Wi-Fi page links to the device check-in page ("Family: register this device with HomeOwl").

## [0.1.16] — 2026-11-11

### Added
- **One name for every device** (Settings › Access): names like `wifi.home.arpa` and `hi.home.arpa` that work on any device at home, an Echo Show too, with no .local lookup needed.
  - HomeOwl adds them as rewrites to every profile of every NextDNS account it has a key for.
  - It points them at this computer again when its address changes, and removes the ones it added when you turn this off.
  - Your other rewrites aren't touched. Each change is logged in Records › Changes.
  - For devices that don't use NextDNS, "Also add to UniFi…" creates local DNS records on the console (UniFi Network 8.2 or newer). You see a preview first, and you can undo it.

## [0.1.15] — 2026-11-11

### Fixed
- **Guest Wi-Fi card showed "not found"**: the card was simply turned off. The switch in Settings › Access now saves at once, and a card that is off says so and where to turn it on.
- **Port 80 problems explain themselves**: Settings › Access says why port 80 couldn't be used (another program has it, or Linux needs permission, with the one command to allow it). The links then include HomeOwl's port and work as they are.

## [0.1.14] — 2026-11-11

### Changed
- **Check-in page and guest Wi-Fi card, made simple** (Settings › Access): each page now has a short link by address, such as `192.168.1.10/wifi`, that works on every device, plus a QR code, Open and Copy. The guest card is a single on/off switch. Names and port options are under "Names and options".
- **The guest Wi-Fi card fits a smart display**: on a landscape screen (Echo Show, a tablet on its side), the join code sits next to the network name and password, in large type. Settings explains how to open it on an Echo Show: say "Alexa, open Silk" and bookmark the link.

### Fixed
- **hi.local and wifi.local on a Mac**: the names are now registered with the Mac's own Bonjour service, the same one that answers for the computer's name. They no longer depend on HomeOwl's Local Network permission, and they are registered again when the address changes. Settings shows when they're registered.
- The short links on port 80 work by address even when no .local name is set.
- **"Keep this address"**: if the computer's address could change (it isn't fixed in UniFi), Settings offers to fix it, through the usual previewed and undoable change.

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
