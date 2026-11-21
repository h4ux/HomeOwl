# How HomeOwl works

HomeOwl is one program that runs on a computer in your home (a Mac, a Linux box or a Windows PC). It talks to your
UniFi console and, if you connect them, to NextDNS, Tailscale and Cloudflare. Everything it learns stays on that
computer: settings and history in its data folder, secrets in the system keychain.

All names and addresses on this page are examples (`Home Wi-Fi`, `Home Setup`, `192.168.1.x`, "Alex").

## The pieces

```mermaid
flowchart LR
  subgraph home["Your home"]
    direction TB
    owl["HomeOwl<br/>(one program, port 8555)"]
    db[("Data folder<br/>DuckDB analytics,<br/>change log, history")]
    keys[("Keychain<br/>API keys, passwords")]
    console["UniFi console<br/>Network · Protect · Access"]
    devices["Phones, laptops,<br/>TVs, smart home"]
    ups["NUT server<br/>(UPS)"]
    owl --- db
    owl --- keys
    owl -- "API: read, and allow-listed<br/>changes with undo" --> console
    owl -- "syslog" --- console
    owl -- "status" --> ups
    devices -- "Wi-Fi / cable" --> console
    devices -- "check-in page<br/>/hi" --> owl
  end
  subgraph cloud["Services you choose to connect"]
    nextdns["NextDNS<br/>query logs, profiles"]
    ts["Tailscale<br/>sign-in, remote access"]
    cf["Cloudflare<br/>HTTPS certificate, names"]
  end
  owl -- "logs, profiles" --> nextdns
  owl --- ts
  owl -- "DNS records" --> cf
  you["You (browser, menu bar,<br/>or Claude over MCP)"] --> owl
```

- **Reads** come from the UniFi console's API and NextDNS's API, and are stored in an embedded DuckDB database for the
  Explore and analytics pages.
- **Changes** to the console go through one allow-list. Each one is previewed first, logged with who made it, and can
  be undone from the Changes page. Changes stay locked until an admin unlocks them.
- **Secrets** (API keys, Wi-Fi and VPN passwords) live in the keychain. They are never sent to the browser, to MCP
  clients or to the logs.

## Getting a new device onto the home Wi-Fi

The home Wi-Fi password stays with the admins. New devices come in through a separate **setup Wi-Fi** and get the
home Wi-Fi from a configuration profile.

```mermaid
sequenceDiagram
  autonumber
  actor Alex as Alex's new phone
  participant Setup as Setup Wi-Fi<br/>(own VLAN)
  participant Owl as HomeOwl
  actor Admin
  participant UniFi as UniFi console
  Alex->>Setup: joins "Home Setup" with the setup password
  Alex->>Owl: opens the check-in page (link or QR code)
  Alex->>Owl: "This is Alex's phone"
  Owl->>Admin: alert: a device is waiting for approval
  Admin->>Owl: Approve (name, person, NextDNS profile)
  Owl->>UniFi: names the device (logged, undoable)
  Owl-->>Alex: page moves on: "Install the profile"
  Alex->>Owl: downloads the profile
  Note over Alex: The profile holds: encrypted DNS through NextDNS,<br/>the home Wi-Fi (joins by itself, real Wi-Fi address),<br/>"don't rejoin Home Setup"
  Alex->>UniFi: joins "Home Wi-Fi" by itself
  Owl->>Owl: follows the phone to its new address
```

- **Profile types** decide what goes in the profile: which Wi-Fi networks it joins, a VPN, a Home Screen icon. One
  type can be the default for everyone. "Don't join our other Wi-Fi networks" lists open networks as don't-join and
  leaves password networks out, so a device can't join them without someone typing the password.
- **Wi-Fi passwords come from UniFi.** If UniFi doesn't give HomeOwl a network's password (the UniFi account HomeOwl
  signs in with may not be allowed to see them), that network is left out of profiles, and Settings › Device profiles
  and the device's setup say so: a profile without the password would only make the device ask for it.
- **Android and Windows** can't install these profiles: their page shows the NextDNS setting and the home Wi-Fi's
  name and password instead.
- **NextDNS on the move:** the profile's encrypted DNS stays on at home, on mobile data and on other Wi-Fi. It stands
  down on networks in a list (planes and hotels whose sign-in page needs ordinary DNS).

## The device's NextDNS setup

When an admin approves a device with a NextDNS profile, the device's check-in page offers encrypted DNS through that
profile under the device's name, so NextDNS logs it by name on any network. On iPhone, iPad and Mac it's a
configuration profile; the admin can also pick the **device model** (or type its model identifier, like
`iPhone17,1`). Then the profile uses NextDNS's Apple address,
`https://apple.dns.nextdns.io/<profile>/<device name>/<model>`, and NextDNS shows the model next to the name — by its
marketing name ("Apple MacBook Air 13-inch (M4)"), or the identifier for a model HomeOwl's list doesn't know yet.
Changing the model later shows **Update ready** on the device's page. Android (Private DNS) and Windows have no model.

The model is found by itself on **Automatic** (the default): Apple devices announce their model identifier over
multicast DNS (`model=` in `_device-info._tcp`, `rpMd=` in `_companion-link._tcp`), so when a device checks in, opens
its page or downloads its profile, HomeOwl asks that one device directly — a single query to its address, port 5353 —
at most once every 10 minutes. What it answers is only used if it looks like a model identifier. A device that says
nothing (it's asleep, not an Apple device, or on a network HomeOwl can't reach) gets no model until an admin picks one;
**None** sends no model at all.

To give a device to someone else, or to a group like Servers, use **Move to…** (⇄) on its chip in Family. Every name
the device has moves together, and its check-in follows, so a later address change keeps it with the new owner.

### Filling in uncategorized domains

Family › Categories lists the domains no built-in category or rule covers. An admin can pick a model for
suggestions: Claude with an Anthropic API key, any model on OpenRouter with an OpenRouter key (keys are kept in the
keychain, never sent to the browser), or a local model through Ollama, where nothing leaves the house. For each: HomeOwl sends only the domain
names and the category list, nothing about devices, people or times, and keeps only answers for domains it asked
about with a category on the list. Without a key, the list downloads as a CSV with a prompt to give any AI chat, and
its answer imports back. Suggestions are always reviewed before they become rules.

A suggestion run is a background job on the server. It takes every domain that had no category in the last 30 days
and asks about it in groups of 100, remembering each domain it asked (in `<data>/ai-categories.json`). A domain that
got a suggestion is never asked again; one the model didn't know is asked again after 30 days. Suggestions wait for
review, and a dismissed one stays dismissed. With the daily run on, the job starts once a day after 03:00 and the
admins get an alert when there's something to review.

### Blocking YouTube but keeping YouTube Music

An app block on YouTube can keep YouTube Music. Both use the same streaming (googlevideo.com) and picture
(ytimg.com) domains, so those stay open, and music.youtube.com goes on the profile's allowlist, which wins over the
denylist. HomeOwl removes only the allowlist entries it added. DNS can't tell the two apps apart completely: the
YouTube website is blocked, but the YouTube app can partly keep working, so Screen Time or Family Link should block
the app itself.

### Rotating private Wi-Fi addresses

iOS 18 and macOS 15 can rotate a device's private Wi-Fi address on a network. HomeOwl remembers the host name each
checked-in device's address last had online. When a new private address shows up with that same specific name
(not a generic "iPhone"), the old address is gone, the name was seen in the last three weeks and only one device
fits, HomeOwl moves the check-in, person and type to the new address, so device approval doesn't block it. The
admins get an alert naming both addresses. The check-in page suggests setting Private Wi-Fi Address to Fixed or Off.

### Is the device's DNS really going through its profile?

The check-in page asks NextDNS's own test, test.nextdns.io, from the device itself (the page's security policy
allows that one address and only on that page). The answer says whether the DNS reaches NextDNS and by what device
name. When it isn't this device's name (or one linked with "this is me"), something else answers its DNS. A common
cause is Tailscale with **Use Tailscale DNS** on: HomeOwl's built-in Tailscale node can see the device is a peer (one
of its addresses is the device's home address), and then the warning names Tailscale and says where to turn it off.

Admins get links to each NextDNS profile and its logs on my.nextdns.io (a new tab, signed in to NextDNS there): on a
person's card in Family, on New devices and in the person editor. HomeOwl sends nothing to build them; they're plain
links with the profile id.

The check-in form asks for the model as well (on iPhone, iPad and Mac), with the one the device announced already
picked; the Approve dialog shows both — what the device said and what its owner chose — and the admin decides.
After approval the device's own page shows the model too, with the one found ticked, and its owner can pick another (or "Don't say")
before installing. An admin's choice in **Set up…** wins over both, and the device list shows each device's model and
where it came from.

**Name prefix.** Settings › Device profiles › Name in NextDNS puts a short prefix before the name a setup reports
("HO" → "HO Alex's iPhone"), so the devices HomeOwl set up stand out in NextDNS. HomeOwl matches both names in the
logs; changing the prefix shows **Update ready** on the devices.

**The Home Screen icon knows its device.** The icon the iPhone and iPad profile adds opens `/hi?k=<key>`, with a key
only that device's profile carries (an HMAC of its check-in under HomeOwl's local secret). A phone's private Wi-Fi
address can change, and the check-in page otherwise knows a device only by its address; opened with the key from a
new address, the device's check-in moves there — its person, approval, Who's home and UniFi name follow — instead of
asking it to check in again. Another approved device's address is never taken over, and nothing moves while the
device's old address is still online (a shared link opened on another device). A Mac takes no icon from a profile, so
the device's page also keeps the key in the browser and in its address — for a bookmark or Safari's Add to Dock.

## Built-in Tailscale

HomeOwl can join your tailnet as its own device (Settings › Access › Built-in Tailscale) and get a
`https://<name>.<tailnet>.ts.net` address. It joins with an auth key if you paste one (kept in the keychain), or
Settings shows a link to approve it. Two things are separate:

- **HomeOwl's device.** By default it belongs to the login that approved it, and its key expires like that login's
  devices do. **Tags** (for example `tag:homeowl`) are asked for when it joins: a tagged device belongs to the tailnet,
  its key doesn't expire, and your Tailscale policy can name it ("family may reach tag:homeowl"). The policy's
  `tagOwners` must let the approving login — or the auth key — use the tag. A Tailscale OAuth client secret
  (`tskey-client-…`) also works as the key: HomeOwl makes a tagged auth key from it (a tag is needed). Tags apply when
  HomeOwl joins, so for a HomeOwl already on the tailnet, **Re-join with these tags** joins it again (same address).
- **Signing in.** Over the tailnet HomeOwl knows who is connecting (Tailscale's WhoIs). With "Sign people in
  automatically", a login linked to a HomeOwl user (Settings › Users) is signed in without a password; requests from
  tagged devices are never signed in this way, since they don't belong to a person.

## Approving new devices

With **Approve new devices** on, HomeOwl checks the network every minute.

```mermaid
flowchart TD
  new["A device joins"] --> where{"Where?"}
  where -- "guest Wi-Fi, setup Wi-Fi,<br/>or a cable" --> pass["Not gated"]
  where -- "home Wi-Fi" --> known{"Approved before,<br/>or checked in?"}
  known -- yes --> ok["Stays connected"]
  known -- "it checked in,<br/>waiting" --> wait["Online; the check-in<br/>is its one approval"]
  known -- "no" --> block["HomeOwl blocks it in UniFi<br/>(logged, undoable)"]
  block --> alert["Alert to the admins"]
  alert --> decide{"Admin"}
  decide -- Approve --> unblock["Unblocked, optionally<br/>given to a person"]
  decide -- "Keep blocked" --> stay["Stays blocked, no more alerts"]
```

Turning it on approves the devices seen in the last day, so nothing in use is cut off. Devices not seen for longer
wait for approval when they come back. More than ten new devices at once pauses blocking and alerts instead.

**A grace period** (Settings › Device profiles › Approve new devices) keeps a new device online for a while —
15 minutes to 7 days — so there's time to decide; it's blocked only if nobody approves it by then. With an ntfy
topic in Settings › Alerts (Generate makes a private one), each new device is pushed to your phone with **Approve**
and **Block** buttons. A tap doesn't call HomeOwl: it posts a signed, one-time answer to a second private ntfy topic
that HomeOwl listens to, so it works from anywhere without opening HomeOwl to the internet. Answers expire after two
days, and HomeOwl only acts on its own.

**One approval per device.** A device that checks in (its page, "This is Sam's iPhone") is waiting for that
check-in only: it gets no grace-period alert of its own and isn't blocked meanwhile, and a grace period already
running ends there. Approving it — on the New devices page, from the notification, or with Approve in Devices › Grace
period — does everything at once: names it in UniFi as entered (or as the admin changed it; logged, undoable), gives
it to the person (creating them if they're new), and unblocks it if HomeOwl had blocked it. Rejecting a check-in
blocks the device when approval is on, so it doesn't come back asking as a new device.

**A device that changes address.** Installing a profile from the setup Wi-Fi gives a phone a new address on the
home Wi-Fi, and a private address can rotate later. HomeOwl follows the device to its new address by its host name
when that name is specific; when it isn't ("iPhone"), it follows a device that installed its profile from the setup
Wi-Fi if it's the only one waiting for that move (in the last two hours) and the only new device that joined since.
Otherwise the approval lists say who a new device probably is, with an *It's …* button that moves the checked-in
device there. Each check-in remembers the addresses it was followed from.

Tagging a device to someone in Who's home gives them its earlier addresses too, and rebuilds their arrivals and
departures from UniFi's record of when their devices were connected (as far back as that record and the history
setting go), so the days a new address went untagged fill in.

**Guests who got the home password.** iPhone password sharing gives visitors the home Wi-Fi, not the guest one. A new
device's **Guest** button (or *When nobody decides: move it to the guest network*) keeps it on the same Wi-Fi but puts
it on the guest network with UniFi's per-client virtual network override, so it gets the internet and nothing of the
home network. It's a logged UniFi change with undo, the device reconnects once, and *Back to home network* takes the
override off. Checked-in devices are never moved. **More time** gives a new device another day, three days or a week.

Old addresses are suggested for archiving (Devices › All devices › Review): a device's earlier addresses, nameless
private addresses nobody owns that haven't been seen for two weeks, strangers blocked for approval that left two
weeks ago, and anything unseen for months. Archiving hides them from HomeOwl's lists; nothing is deleted.

**If HomeOwl is down, or blocked a device by mistake:** the block lives in UniFi, so lift it there. In the UniFi
app (or unifi.ui.com), open Client Devices, show the Blocked ones, open the device and tap Unblock. When HomeOwl runs
again it sees the device connected and counts it as approved. A stopped HomeOwl blocks nothing new. Settings › Device
profiles has these steps too, with a page to print and keep by the router.

## The device's own page (and asking for a blocked site)

Once a device is approved, its check-in page (the **HomeOwl** icon its setup profile puts on the Home Screen) becomes
that device's own page. There's no sign-in: HomeOwl recognises the device by its address on the home network, so the
page shows that device and nothing about anyone else's:

- **Is it OK?** Protected and in use, an update to install, or the setup stopped (with the usual reasons and the
  steps to fix it). When NextDNS logs aren't arriving at all, it says it can't tell rather than "stopped".

When NextDNS logs the device under a name HomeOwl doesn't know (a profile installed under an older name), the page
lists the names NextDNS saw lately in the device's profile that share a word with it, never another device's. **This
is me** links one: the status, the blocked sites and Analytics follow it, and *not me* undoes it.

The status and the guest Wi-Fi are shown; the other sections are folded, each with a one-line summary.
- **Right now.** The Wi-Fi and access point it's on, the signal (with a tip when it's weak), and whether the internet
  works, with the console's last speed test.
- **Blocked on this device.** What NextDNS blocked under the device's name in the last day, and why. **Ask to allow**
  (with an optional reason), or type another site. At most five requests wait per device.
- **Guest Wi-Fi.** The guest network's name, password and join code, so a visitor can scan it off the family member's
  phone (the setup Wi-Fi is never shown). This works whether or not the guest card at `wifi.local` is turned on.

"Is it OK?" matches NextDNS logs by every name the device has had (on letters and digits only, so a curly
apostrophe or dashes for spaces still match): its name now, the name in the profile it installed
(the profile keeps reporting that one after a rename), and the names of check-ins merged into it. A device marked
stopped that NextDNS has seen since no longer shows as stopped.

A request shows up on **Home › Today** and in **Home › Alerts** with **Allow** and **Deny**, and on
**Devices › New devices** under *Site requests*. With ntfy set up, the push has Allow and Deny buttons that work the
same way as the new-device ones (a signed one-time token posted to the private reply topic). Allowing puts the site
on the **allowlist of the device's NextDNS profile**, so it's allowed for everyone using that profile, and it's
logged in **History › Changes**. *Stop allowing* takes it off again. The device's page shows each answer.

## The setup Wi-Fi's network

HomeOwl can create the setup Wi-Fi for you (Settings › Device profiles): a network on its own VLAN and a Wi-Fi on it
with its own password, modelled on your home network and Wi-Fi. Settings then shows three things about it:

```mermaid
flowchart LR
  phone["Phone on Home Setup<br/>192.168.30.x"] -->|"same network:<br/>works without internet"| owl30["HomeOwl's address<br/>on VLAN 30"]
  phone -.->|"through the router:<br/>phones send this over<br/>mobile data if there's no internet"| owl1["HomeOwl's home address<br/>192.168.1.x"]
  phone -.-x|"isolated"| homelan["Home network"]
```

1. **Internet:** a phone on a Wi-Fi without internet soon sends everything outside that network over mobile data, so
   a check-in page on the home network stops loading. Either the setup Wi-Fi has internet, or
2. **HomeOwl has its own address on it** (its computer joins the VLAN over the same cable). Then the page loads
   directly, and the setup Wi-Fi can do without internet. Anyone who knows the setup password can use its internet,
   so turn it off once HomeOwl is on the VLAN.
3. **Isolation** keeps devices on the setup Wi-Fi away from the home network.

## Keeping the lists tidy

Phones and laptops use a private Wi-Fi address per network, and change it now and then. UniFi keeps every address it
has seen, so one phone can show up several times. **Devices › All devices** suggests archiving older copies of a device
that is now on a newer address, and private addresses unused for 90 days. Archived addresses leave HomeOwl's lists;
UniFi, the logs and the history keep them, and they can be restored. A device that comes back online shows again.

## Where things live

| What | Where |
| --- | --- |
| Settings, people, check-ins, change log | the data folder (a per-user app folder, or `--data` / `$HOMEOWL_DATA`) |
| Analytics (DNS queries, traffic, sessions) | DuckDB in the data folder, read-only SQL console |
| API keys, Wi-Fi and VPN passwords | the system keychain (or `<data>/secrets` without one) |
| The web app | built into the program: plain HTML, CSS and JavaScript, no build step |
| AI access | the MCP server, with its own mode: off, read, guarded or god (raw API, logged) — see [mcp.md](mcp.md) |

## Tuya (Smart Life) devices

**Smart › Tuya** shows Smart Life and Tuya plugs, switches, lights and sensors: online or not, their readings (power,
voltage, temperature, humidity, battery), and on/off switches for admins. Every switch is logged in
**History › Changes** like other actions. Pick how HomeOwl reaches them in the Tuya settings on that page:

- **Tuya Cloud.** A free Tuya IoT Platform project with your Smart Life app account linked to it. Paste its Access
  ID and Access Secret (kept in the system keychain). Every device shows, with readings in the right units. The cloud
  also gives each device's local key, kept in the keychain for the home-network mode.
- **Home network only.** HomeOwl talks to each device on port 6668 with its local key (protocol 3.3, 3.4 or 3.5), with
  no cloud at run time. Battery sensors sleep and can't be reached this way.

**Houses, rooms and types.** In Cloud mode HomeOwl reads the Smart Life homes of every linked app account, their
rooms and which device is where (hourly, or on **Refresh**). The page filters by house and by type and groups the
devices by room. In **Houses** (Tuya settings) untick a home to hide its devices — another family's home on the same
account, say — or make your own houses. Opening a device (✎) puts it in a house and room and says what it is (a
"Boiler switch" rather than "Switch"); empty fields follow Smart Life, and the choices survive every sync.

**How it looks.** Each house has a banner with its name, how many devices are online and on, and its Tap-to-Run
scenes; **✎ Look** picks a background (a colour preset, or a photo of the house — shrunk in the browser, kept in the
data folder, shown only to people signed in). Its automations sit folded under the banner. Every device card has an
icon (picked from its name — a switch called "Boiler" gets the water-heater icon — or its type; ✎ picks another), and
a device with several switches lists them by name: the names given in the Smart Life app (read once a day), or your
own in ✎ › Switch names. In Cloud mode HomeOwl asks the Tuya Cloud every 5 minutes, which keeps well inside the free
plan's monthly calls.

**Scenes and automations.** Each Smart Life home's Tap-to-Run scenes show with **Run**, its automations with an
on/off switch, for admins, logged in **History › Changes**. They need the **Smart Home Scene Linkage** API service in
the Tuya project; without it the page says so.

**Where to look for devices.** Devices on HomeOwl's own network announce themselves. For other networks (an IoT
VLAN), tick them and **Scan the network now** tries Tuya's port on each address (up to a /22). In
**Devices › All devices**, opening a device offers **It's a Tuya device**, so the scan looks for it at its address.
Devices found are matched to their UniFi client by address.

**When a network stays silent.** An IoT network is usually closed off by the firewall, so HomeOwl's computer can't
reach port 6668 there and the scan finds nothing. The scan result then says so for that network and offers **Preview
the fix**: one narrow rule that lets HomeOwl's network reach TCP 6668 on that network and nothing else (a zone-based
policy on UniFi Network 9+, a LAN In rule with a port group on classic firewalls). You see the change first, it is
applied only when you click **Allow it**, it shows in **History › Changes** and can be undone there. HomeOwl scans
again right after. Broadcasts don't cross networks, so devices elsewhere are found by their port, not by announcing.



### Plan, Dollhouse and Rooms views

The Tuya page can show a house four ways: **Cards** (one per device), **Plan** (the house from above, each switch a
dot where it sits), **Dollhouse** (floor by floor, each switch a window, outdoor lights in the garden) and **Rooms**
(a card per room with its own picture). The choice is kept in the browser. Lit things are amber; tapping one switches
it, tapping a room opens its devices.

A house's layout is kept with the house in HomeOwl's Tuya state: each room's place and size on the plan, its floor,
whether it's outside, and its look, plus where each switch sits in its room. Until an admin arranges it, rooms are
laid out in rows, and rooms named like a garden, pool or parking go outside. **Arrange** (Plan) drags rooms,
corners and switches; **Rooms…** sets floors, outside and looks; a room photo is shrunk in the browser and kept like
the house's.
