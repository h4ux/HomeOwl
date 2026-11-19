/* HomeOwl — device check-in ("Hi HomeOwl"): runs on the family member's own phone, tablet or laptop, without signing in. */
"use strict";

const $ = s => document.querySelector(s);
const esc = s => String(s ?? "").replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
const KINDS = [["phone", "📱 Phone"], ["tablet", "📱 Tablet"], ["laptop", "💻 Laptop"], ["computer", "🖥 Computer"], ["tv", "📺 TV / streamer"],
  ["console", "🎮 Game console"], ["speaker", "🔊 Speaker"], ["watch", "⌚ Watch"], ["camera", "📷 Camera"], ["printer", "🖨 Printer"], ["other", "Something else"]];

function guessKind() {
  const ua = navigator.userAgent;
  if (/iPad|Tablet/i.test(ua) || (/Macintosh/.test(ua) && navigator.maxTouchPoints > 1)) return "tablet";
  if (/iPhone|Android.+Mobile|Mobile/i.test(ua)) return "phone";
  if (/Android/i.test(ua)) return "tablet";
  if (/Macintosh|Windows|Linux|CrOS/i.test(ua)) return "laptop";
  return "other";
}

// the key in the Home Screen icon's link: HomeOwl knows this device by it even after its Wi-Fi address changes
// (a Mac gets no Home Screen icon from a profile, so the key is also kept in this browser and in the page's address,
// for a bookmark or Safari's Add to Dock)
const hiStore = { get: () => { try { return localStorage.getItem("hi-key") || ""; } catch { return ""; } }, set: v => { try { localStorage.setItem("hi-key", v); } catch { } } };
let HI_KEY = new URLSearchParams(location.search).get("k") || hiStore.get();
function keepKey(k) {
  if (!/^[0-9a-f]{32}$/.test(k || "")) return;
  HI_KEY = k; hiStore.set(k);
  const u = new URL(location.href);
  if (u.searchParams.get("k") !== k) { u.searchParams.set("k", k); history.replaceState(null, "", u); }
}
async function call(method, body) {
  const q = new URLSearchParams();
  if (method === "GET" && /^[0-9a-f]{32}$/.test(HI_KEY)) q.set("k", HI_KEY);
  if (method === "GET" && ["ios", "mac"].includes(platform())) q.set("probe", "1"); // HomeOwl asks the device its model
  const r = await fetch("/api/hi" + (q.size ? `?${q}` : ""), { method, headers: { "Content-Type": "application/json" }, body: body ? JSON.stringify(body) : undefined, cache: "no-store" });
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(j.error || `Something went wrong (HTTP ${r.status})`);
  return j;
}

function done(again) {
  $("#hi-box").innerHTML = `<h2 style="margin:10px 0 6px">Thanks! 🦉</h2>
    <p>An admin will approve it soon. After that, this device shows up with its new name in UniFi and in HomeOwl.</p>
    <p class="fine hi-wait">Keep this page open: it moves on by itself once it's approved.</p>${cellTip()}
    <div class="msg warn hi-lost" hidden>This page can't reach HomeOwl right now. Turn off <b>Mobile Data</b>, stay on this Wi-Fi and reload the page.</div>
    ${again ? `<p class="fine">This device is already waiting for approval — sending again replaces that request.</p><button class="btn" id="hi-again">Change my answer</button>` : ""}`;
  waitApproval();
}

// The setup Wi-Fi has no internet, so a phone soon sends everything outside the setup network (HomeOwl too) over
// mobile data instead, and the page stops loading. Mobile data off keeps it on this Wi-Fi.
function cellTip() {
  return start.setup && /ios|android/.test(platform()) ? `<p class="fine"><b>Turn off Mobile Data</b> until you're done (${platform() === "ios" ? "Settings › Mobile Data" : "Settings › Network › Mobile data"}): this Wi-Fi has no internet, so otherwise the phone switches this page to mobile data and it stops loading. Turn it back on afterwards.</p>` : "";
}
waitApproval.fails = 0;
// while waiting: look every few seconds, and go on to the setup once it's approved
function waitApproval() {
  clearTimeout(waitApproval.t);
  waitApproval.t = setTimeout(async () => {
    if (!$(".hi-wait")) return;
    try { const st = await call("GET"); waitApproval.fails = 0; if (st.registered) return registered(st); }
    catch { if (++waitApproval.fails >= 2 && $(".hi-lost")) $(".hi-lost").hidden = false; } // try again
    waitApproval();
  }, 5000);
}

function platform() {
  const ua = navigator.userAgent;
  if (/iPhone|iPad|iPod/.test(ua) || (/Macintosh/.test(ua) && navigator.maxTouchPoints > 1)) return "ios";
  if (/Macintosh/.test(ua)) return "mac";
  if (/Android/i.test(ua)) return "android";
  if (/Windows/i.test(ua)) return "windows";
  return "other";
}

// iOS installs profiles only from Safari; other browsers (and apps' built-in browsers) just save the file
const notSafariIOS = () => /CriOS|FxiOS|EdgiOS|OPiOS|OPT\/|YaBrowser|DuckDuckGo|Ddg\/|GSA\/|FBAN|FBAV|Instagram|Line\/|Snapchat|LinkedInApp|Twitter/i.test(navigator.userAgent);

const copyRow = (v, label) => `<div class="cmd"><code>${esc(v)}</code><button class="btn sm" type="button" data-hicopy="${esc(v)}" aria-label="Copy ${esc(label)}">Copy</button></div>`;

const iconNote = k => k === "none" ? ""
  : k === "app" ? `<p class="fine">It also puts a <b>HomeOwl</b> icon on the Home Screen that opens HomeOwl. It works at home.</p>`
  : `<p class="fine">It also puts a <b>HomeOwl</b> icon on the Home Screen that opens this page, for updates later. It works at home.</p>`;

// the check-in form's model list (iPhone, iPad, Mac): the one the device announced is suggested; the admin sees both
// A phone is offered iPhones, a tablet iPads, a laptop or computer Macs; the model found on the device stays offered.
const MODEL_GROUP = { phone: "iPhone", tablet: "iPad", laptop: "Mac", computer: "Mac" };
function modelGroups(groups, kind, keep) {
  const g = MODEL_GROUP[kind], l = groups.filter(x => x.group === g || x.models.some(m => m.id === keep));
  return g && l.length ? l : groups;
}
let FORM_MODELS = null;
function formModelOpts(m, kind, cur) {
  const known = m.groups.some(g => g.models.some(x => x.id === m.found));
  return `<option value="" ${cur ? "" : "selected"}>Not sure</option>
    ${modelGroups(m.groups, kind, cur).map(g => `<optgroup label="${esc(g.group)}">${g.models.map(x => `<option value="${esc(x.id)}" ${x.id === cur ? "selected" : ""}>${esc(x.name)}</option>`).join("")}</optgroup>`).join("")}
    ${m.found && !known ? `<option value="${esc(m.found)}" ${cur === m.found ? "selected" : ""}>${esc(m.found)}</option>` : ""}`;
}
// the kind changed: only its models (the choice stays if it's still offered)
document.addEventListener("change", e => {
  if (e.target.id !== "hi-kind" || !FORM_MODELS || !$("#hi-fmodel")) return;
  const sel = $("#hi-fmodel"), cur = sel.value || FORM_MODELS.found || "";
  sel.innerHTML = formModelOpts(FORM_MODELS, e.target.value, cur);
  if (sel.value !== cur) sel.value = "";
});
function formModel(m, kind) {
  if (!m?.groups?.length || !["ios", "mac"].includes(platform())) return "";
  FORM_MODELS = m;
  return `<label>Which model?
      <select id="hi-fmodel">${formModelOpts(m, kind, m.found || "")}</select></label>
    ${m.found ? `<p class="fine muted" style="margin-top:-6px">Suggested by this device itself — change it if it's wrong.</p>` : ""}`;
}

// which model this is (devmodel.go): the one found on the device is ticked; an admin's choice can't be changed here
function modelPick(m) {
  if (!m?.groups?.length) return "";
  const known = id => m.groups.some(g => g.models.some(x => x.id === id));
  const cur = m.current || (m.by === "device" ? "-" : "");
  const opts = modelGroups(m.groups, m.kind, cur).map(g => `<optgroup label="${esc(g.group)}">${g.models.map(x => `<option value="${esc(x.id)}" ${x.id === cur ? "selected" : ""}>${esc(x.name)}</option>`).join("")}</optgroup>`).join("")
    + (cur && cur !== "-" && !known(cur) ? `<option value="${esc(cur)}" selected>${esc(cur)}</option>` : "")
    + `<option value="-" ${cur === "-" ? "selected" : ""}>Don't say</option>`;
  const note = m.by === "admin" ? "An admin chose this model." : m.by === "found" ? "Found on this device — change it if it's wrong."
    : m.by === "device" ? "Chosen here." : "HomeOwl couldn't tell — pick it, so NextDNS shows it.";
  return `<label class="hi-model">This device is
      <select id="hi-model" ${m.by === "admin" ? "disabled" : ""}>${cur ? "" : `<option value="" selected>Choose…</option>`}${opts}</select></label>
    <p class="fine muted" id="hi-model-note">${esc(note)} NextDNS shows it next to the device's name.</p>`;
}
document.addEventListener("change", async e => {
  if (e.target.id !== "hi-model") return;
  const sel = e.target; sel.disabled = true;
  try {
    const r = await fetch("/api/hi/model", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ model: sel.value }), cache: "no-store" });
    const j = await r.json().catch(() => ({}));
    if (!r.ok) throw new Error(j.error || `Something went wrong (HTTP ${r.status})`);
    const open = $("#hi-setup-fold")?.open;
    await start();
    if (open && $("#hi-setup-fold")) $("#hi-setup-fold").open = true;
  } catch (err) { sel.disabled = false; const n = $("#hi-model-note"); if (n) { n.textContent = err.message; n.className = "fine bad-t"; } }
});

// the device is approved: offer to set it up so it's recognised by name everywhere (devprofile.go)
function registered(st) {
  const r = st.registered, nd = r.nextdns, pf = platform();
  keepKey(r.key);
  const apple = `<a class="btn primary" href="/api/hi/profile${pf === "ios" ? "?p=ios" : pf === "mac" ? "?p=mac" : ""}" download="HomeOwl.mobileconfig">Install the HomeOwl profile</a>`;
  const inc = r.includes || {}, wifi = inc.wifi || [], vpn = inc.vpn, extra = wifi.length || vpn || (inc.no_join || []).length;
  const setupWiFi = start.setup, moveNote = setupWiFi && wifi.length
    ? `<p class="fine">After installing, this device moves to <b>${esc(wifi[0])}</b> by itself and won't rejoin ${esc(setupWiFi)}.</p>` : "";
  const captive = setupWiFi && pf === "ios"
    ? `<p class="fine">If this page opened in a sign-in window, it can't install profiles: copy the link into Safari. If iOS asks, choose <b>Use Without Internet</b> to stay on ${esc(setupWiFi)}.</p>${copyRow(location.href, "link")}` : "";
  // the WireGuard VPN needs its app on the device before the profile goes in, or the install fails
  // ("The VPN service could not be created")
  const appStep = p => vpn?.app === "WireGuard" && vpn.ready !== false
    ? `<li><b>First</b> install <a href="${p === "mac" ? "https://apps.apple.com/app/wireguard/id1451685025" : "https://apps.apple.com/app/wireguard/id1441195209"}" target="_blank" rel="noopener">WireGuard from the App Store</a> and open it once. Without it the profile can't add the VPN.</li>` : "";
  const holds = extra ? `<ul class="hi-holds">${nd ? `<li>Encrypted DNS through NextDNS, under this device's name</li>` : ""}
      ${wifi.length ? `<li>Wi-Fi: ${wifi.map(esc).join(", ")} — joins by itself${inc.fixed_mac ? ", with this device's real Wi-Fi address so it's recognised at home (Settings shows a privacy note for that network)" : ""}</li>` : ""}
      ${(inc.no_join || []).length ? `<li>Doesn't join by itself: ${inc.no_join.map(esc).join(", ")}${inc.no_join.length > 1 ? `. While installing it asks for their passwords: leave each one empty and tap <b>Next</b>` : ""}</li>` : ""}
      ${vpn ? `<li>VPN: ${esc(vpn.name)}${vpn.on_demand ? " — turns on by itself away from home" : ""}${vpn.app ? ` (needs the ${esc(vpn.app)} app${vpn.ready === false ? "; an admin still has to add this device's key" : ""})` : ""}</li>` : ""}</ul>` : "";
  const how = {
    ios: notSafariIOS()
      ? `<div class="msg warn">iPhone and iPad install profiles only from <b>Safari</b>. This browser would just save the file.</div>
        <a class="btn primary" href="${esc("x-safari-" + (st.safari || location.href))}">Open this page in Safari</a>
        <p class="fine">If nothing happens (Chrome often blocks it), copy the link, open <b>Safari</b> and paste it in the address bar:</p>${copyRow(st.safari || location.href, "link")}`
      : `<ol class="hi-steps">${appStep("ios")}<li>Tap the button and choose <b>Allow</b>.</li><li>Open <b>Settings</b> › <b>Profile Downloaded</b> (near the top) › <b>Install</b>.</li></ol>${apple}
        ${iconNote(inc.icon)}${moveNote}${captive}`,
    mac: `<ol class="hi-steps">${appStep("mac")}<li>Click the button to download the profile.</li><li>Open <b>System Settings</b> › <b>General</b> › <b>Device Management</b>, double-click <b>HomeOwl</b> and choose <b>Install</b>.</li></ol>${apple}
      <p class="fine">A Mac takes no HomeOwl icon from a profile. For a shortcut to this page, choose <b>File</b> › <b>Add to Dock</b> in Safari (or bookmark it): it knows this Mac even if its Wi-Fi address changes.</p>`,
    android: nd && `<ol class="hi-steps"><li>Open <b>Settings</b> › <b>Network &amp; internet</b> › <b>Private DNS</b>.</li><li>Choose <b>Private DNS provider hostname</b> and paste:</li></ol>${copyRow(nd.android, "host name")}`,
    windows: nd && `<ol class="hi-steps"><li>Open <b>Settings</b> › <b>Network &amp; internet</b> › your connection › <b>DNS server assignment</b> › <b>Edit</b> › <b>Manual</b>.</li>
      <li>Turn on IPv4, set Preferred DNS to <code>${esc(nd.servers[0])}</code> and Alternate to <code>${esc(nd.servers[1])}</code>.</li>
      <li>For each, set <b>DNS over HTTPS</b> to <b>On (manual template)</b> and paste:</li></ol>${copyRow(nd.doh, "template")}`,
  };
  const wifiOnly = !nd && wifi.length && pf !== "ios" && pf !== "mac"; // the home Wi-Fi block below is all there is
  const setup = wifiOnly ? "" : !nd && !r.apple_only ? `<p class="fine">An admin can also choose a NextDNS profile for this device; then this page offers to set it up.</p>`
    : `${r.update ? `<div class="msg warn hi-update"><b>Update ready.</b> This device's settings changed. ${pf === "android" ? "Replace the Private DNS host name with the one below." : "Install the profile again — it replaces the old one."}</div>` : ""}<h3 style="margin:16px 0 6px">${r.update ? "Install the update" : "Set up this device"}</h3>
      ${extra ? `<p class="muted">It sets up:</p>${holds}<p class="fine">You can remove it any time.${pf === "android" || pf === "windows" ? " Wi-Fi and VPN install on iPhone, iPad and Mac; here you get the NextDNS part." : ""}</p>`
        : `<p class="muted">So HomeOwl and NextDNS recognise it by name at home, on mobile data and on other Wi-Fi. Nothing else changes, there are no passwords in it, and you can remove it any time.</p>`}
      ${nd && (pf === "ios" || pf === "mac") ? modelPick(r.model) : ""}
      ${!nd ? (pf === "ios" || pf === "mac" ? how[pf] : `<p>Open this page on the iPhone, iPad or Mac to install it.</p>`)
        : how[pf] || `<p>iPhone, iPad or Mac: open this page on it. Android: Private DNS host name:</p>${copyRow(nd.android, "host name")}<p>Anything else: DNS-over-HTTPS address:</p>${copyRow(nd.doh, "address")}`}
      ${r.profile_at && (pf === "ios" || pf === "mac") ? `<p class="fine">Downloaded before on ${esc(new Date(r.profile_at * 1000).toLocaleDateString())}. Installing again replaces it.</p>` : ""}
      ${nd && (pf === "ios" || pf === "mac") ? `<p class="fine">On a plane or in a hotel and the Wi-Fi sign-in page won't load? ${pf === "ios" ? "Open <b>Settings</b> › <b>General</b> › <b>VPN &amp; Device Management</b> › <b>DNS</b> and choose <b>Automatic</b>" : "Open <b>System Settings</b> › <b>Network</b> › <b>Filters</b> (or <b>VPN &amp; Filters</b>) and turn the NextDNS DNS setting off"}, sign in, then switch it back. Tell an admin the network's name so HomeOwl skips it next time.</p>` : ""}
      ${pf === "ios" || pf === "mac" ? `<p class="fine">If iCloud Private Relay is on, Safari's visits go through Apple instead and aren't seen by NextDNS.</p>` : ""}`;
  const mine = st.mine || {}, ss = mine.setup || {};
  const settled = ss.state === "active" && !r.update && !ss.update;
  // the status and the guest Wi-Fi up top; everything else folded
  $("#hi-box").innerHTML = `<h2 style="margin:10px 0 6px">This is ${esc(r.name)} 🦉</h2>
    ${r.person ? `<p class="muted">It belongs to <b>${esc(r.person)}</b>.</p>` : ""}
    ${myStatus(mine, r)}<div id="hi-dnscheck"></div><div id="hi-guest"></div>
    ${myConnection(mine.connection)}${privAddr(st.private_addr, pf)}<div id="hi-blocked">${myBlocked(mine)}</div>
    ${wifi.length && pf !== "ios" && pf !== "mac" ? hiFold("Home Wi-Fi", `<div id="hi-wifi"><div class="empty">Loading the home Wi-Fi…</div></div>`) : ""}
    ${setup ? hiFold(settled ? "Set up this device again" : r.update || ss.update ? "Install the update" : "Set up this device", myNDLinked(mine) + setup, "hi-setup-fold") : ""}
    <p class="fine" style="margin-top:16px"><button class="btn ghost sm" id="hi-again" type="button">Not right? Check in again</button></p>`;
  if ($("#hi-wifi")) homeWiFi();
  guestWiFi();
  if (nd && (r.profile_at || ["active", "quiet", "stopped", "not_seen"].includes(ss.state))) dnsCheck(st);
}

// a private Wi-Fi address can rotate: HomeOwl follows it by name, but a fixed address never looks like a new device
function privAddr(p, pf) {
  if (!p || (pf !== "ios" && pf !== "mac")) return "";
  const net = p.ssid ? `<strong>${esc(p.ssid)}</strong>` : "this Wi-Fi";
  const where = pf === "mac" ? `<strong>System Settings</strong> › <strong>Wi-Fi</strong> › <strong>Details…</strong> next to ${net}`
    : `<strong>Settings</strong> › <strong>Wi-Fi</strong> › ⓘ next to ${net}`;
  return hiFold("Keep this device's Wi-Fi address", `<p class="fine">It uses a private Wi-Fi address here. If it's set to <strong>Rotating</strong>, it changes every couple of weeks and the home network may take it for a new device (with device approval on, it's held until an admin lets it in). Open ${where}, and set <strong>Private Wi-Fi Address</strong> to <strong>Fixed</strong> or <strong>Off</strong>.</p>`);
}

// where this device's DNS really goes, asked from the device itself: NextDNS's own test answers with the profile and
// the device name it sees. Not this device's name (or nothing) while it's set up means something else answers its
// DNS; HomeOwl's Tailscale node can tell when that's Tailscale (the device is a peer).
async function dnsCheck(st) {
  const el = $("#hi-dnscheck"), r = st.registered, stopped = st.mine?.setup?.state === "stopped";
  const mine = new Set([r.nextdns?.name, r.name, ...(r.nextdns?.names || [])].filter(Boolean).map(n => String(n).trim().toLowerCase()));
  if (!el) return;
  let t = null;
  try {
    const ctl = new AbortController(), timer = setTimeout(() => ctl.abort(), 5000);
    const res = await fetch("https://test.nextdns.io/", { cache: "no-store", credentials: "omit", referrerPolicy: "no-referrer", signal: ctl.signal });
    clearTimeout(timer); t = await res.json();
  } catch { t = null; } // blocked or offline: say nothing rather than guess
  const ts = !!st.tailscale, pf = platform();
  if (!t && !(ts && stopped)) return; // no test: only a stopped device that runs Tailscale is worth a guess
  const seen = String(t?.deviceName || "").trim(), right = t?.status === "ok" && mine.has(seen.toLowerCase());
  if (right) return;
  const tsHow = pf === "mac" ? `Click the Tailscale icon in the menu bar, then turn off <strong>Use Tailscale DNS settings</strong>.`
    : pf === "ios" ? `Open the Tailscale app, tap your account › <strong>Settings</strong>, and turn off <strong>Use Tailscale DNS</strong>.`
    : `In the Tailscale app's settings, turn off <strong>Use Tailscale DNS</strong>.`;
  const what = !t ? "" : t.status !== "ok" ? `NextDNS's own test says this device's DNS isn't reaching NextDNS right now.`
    : seen ? `NextDNS sees this device as <strong>${esc(seen)}</strong>, not by its own name, so its DNS isn't going through its HomeOwl profile.`
    : `NextDNS sees this device without a name, so its DNS isn't going through its HomeOwl profile.`;
  el.innerHTML = `<div class="hi-stat warn"><span class="hi-stat-ic" aria-hidden="true">!</span><div>
    <b>${ts ? (t ? "Tailscale is answering this device's DNS" : "Tailscale may be answering this device's DNS") : "Its DNS goes elsewhere"}</b>
    <div class="fine">${what}${ts ? ` This device runs Tailscale${t ? "" : ", and NextDNS's test couldn't be reached to check"}: with Tailscale DNS on, Tailscale answers every lookup and the profile is skipped. ${tsHow}` : ""}</div>
    ${ts ? `<p class="fine muted">Need Tailscale's names on this device? Keep it on, and in the Tailscale admin console set NextDNS as the tailnet's DNS instead; then link the name NextDNS shows here with "this is me".</p>` : ""}
    <p class="fine muted"><a href="https://test.nextdns.io" target="_blank" rel="noopener noreferrer">Check it yourself</a> — status should be ok and deviceName ${esc(r.nextdns?.name || r.name)}.</p></div></div>`;
}

// the guest Wi-Fi: a visitor scans the code off this screen
async function guestWiFi() {
  const el = $("#hi-guest");
  let r;
  try { const res = await fetch("/api/hi/guest", { cache: "no-store" }); r = await res.json(); if (!res.ok) throw new Error(r.error); }
  catch { return; } // nothing to show: no guest network, or UniFi isn't reachable right now
  if (!el || !r.networks?.length || typeof QR !== "object") return;
  el.innerHTML = `<div class="hi-sect"><h3>Guest Wi-Fi</h3>${r.networks.map((n, i) => `<details class="hi-guest"${i === 0 ? " open" : ""}><summary><b>${esc(n.ssid)}</b> <span class="fine muted">for a visitor</span></summary>
      <div class="wifi-qr">${QR.themed(QR.wifi(n), { theme: r.qr_theme || "rounded", size: 240, label: `Join ${n.ssid}`, caption: `${n.ssid} · scan to join` })}</div>
      ${n.security === "nopass" ? `<p class="fine">No password: it's open.</p>` : copyRow(n.password, "password")}
      <p class="fine muted">They point their phone's camera at the code and tap the Wi-Fi banner.</p></details>`).join("")}</div>`;
}

// a folded section (closed by default); extra is a short summary after the title
const hiFold = (title, body, id, extra) => `<details class="hi-fold"${id ? ` id="${id}"` : ""}><summary><span>${esc(title)}</span>${extra ? `<span class="fine muted">${esc(extra)}</span>` : ""}</summary><div class="hi-fold-body">${body}</div></details>`;
document.addEventListener("click", e => {
  if (!e.target.closest("[data-hifix]")) return;
  const f = $("#hi-setup-fold"); if (!f) return;
  f.open = true; f.scrollIntoView({ behavior: "smooth", block: "start" });
});

// ---- "my device" (myhome.go): is it OK, how it's connected, what was blocked on it
const agoS = t => { const m = Math.round((Date.now() / 1000 - t) / 60); return m < 1 ? "just now" : m < 60 ? `${m} min ago` : m < 1440 ? `${Math.round(m / 60)} h ago` : `${Math.round(m / 1440)} day${Math.round(m / 1440) === 1 ? "" : "s"} ago`; };
function myStatus(mine, r) {
  const ss = mine.setup || {};
  const [ok, title, note, fix] = {
    active: [true, "All good", `Protected and recognised by name${ss.where === "away" ? " (away from home right now)" : ""}.`],
    quiet: [true, "All good", `Set up. Not used in the last day${ss.last_seen ? ` (last ${agoS(ss.last_seen)})` : ""}.`],
    stopped: [false, "Protection stopped", `This device stopped sending its DNS to NextDNS${ss.last_seen ? ` (last ${agoS(ss.last_seen)})` : ""}, while NextDNS logs for the rest of the home${ss.logs_at ? ` arrive (latest ${agoS(ss.logs_at)})` : " arrive"}.`, true],
    not_seen: [false, "Not installed yet", "The profile was downloaded but isn't in use. Finish installing it under Set up this device.", true],
    logs_late: [null, "Can't tell right now", `${ss.logs_at ? `NextDNS logs last reached HomeOwl ${agoS(ss.logs_at)}` : "NextDNS logs haven't reached HomeOwl yet"}, so it can't see whether this device is protected. An admin can check Settings › NextDNS.`],
    offered: [false, "Not set up yet", "Set it up under Set up this device, so it's protected and recognised by name everywhere.", true],
  }[ss.state] || [null, "", ""];
  const upd = r.update || ss.update;
  if (!title && !upd) return "";
  const good = ok && !upd, unsure = ok === null && !upd;
  // stopped: the usual reasons on an iPhone, iPad or Mac
  const why = ss.state === "stopped" ? `<details class="hi-why"><summary>Why, and how to fix it</summary><ul>
      ${platform() === "mac" ? `<li>Open <b>System Settings</b> › <b>Network</b> › <b>VPN &amp; Filters</b>: under <b>DNS</b>, <b>HomeOwl</b> should be there and <b>Enabled</b>. If it's missing, install the profile again under <b>Set up this device</b> (then <b>System Settings</b> › <b>General</b> › <b>Device Management</b>).</li>`
        : `<li>Open <b>Settings</b> › <b>General</b> › <b>VPN &amp; Device Management</b> › <b>DNS</b>: <b>HomeOwl</b> should be there and selected (not <b>Automatic</b>). If it's missing, install the profile again under <b>Set up this device</b>.</li>`}
      <li>Open <a href="https://test.nextdns.io" target="_blank" rel="noopener noreferrer">test.nextdns.io</a> on this device: <b>status</b> should be <b>ok</b> and <b>deviceName</b> this device's name. <b>unconfigured</b> means its DNS isn't reaching NextDNS.</li>
      <li>A VPN app that brings its own DNS takes over while it's on (Tailscale with <b>Use Tailscale DNS</b>, other VPNs, ad blockers). Turn that DNS option off, or the VPN off, and check again in a few minutes.</li>
      <li><b>iCloud Private Relay</b> and <b>Limit IP Address Tracking</b> send some traffic past NextDNS, but not all of it.</li></ul>
      <p class="fine">This clears by itself within about 15 minutes of NextDNS seeing the device again.</p></details>` : "";
  return `<div class="hi-stat ${good ? "good" : unsure ? "" : "warn"}"><span class="hi-stat-ic" aria-hidden="true">${good ? "✓" : unsure ? "?" : "!"}</span>
    <div><b>${esc(upd && ok !== false ? "Update ready" : title)}</b><div class="fine">${esc(upd && ok !== false ? "Its settings changed. Install the profile again under Install the update — it replaces the old one." : note)}</div>
    ${myNDCands(mine)}${fix || upd ? `<button class="btn sm hi-fix" type="button" data-hifix>${upd ? "Install the update" : "Set up this device"}</button>` : ""}${why}</div></div>`;
}
// names NextDNS sees that look like this device: "this is me" links one (ndnames.go)
function myNDCands(mine) {
  const l = mine.nd_candidates || [];
  if (!l.length) return "";
  return `<div class="hi-ndc"><p class="fine"><b>Is this device one of these in NextDNS?</b> An older profile may use another name.</p>
    ${l.map(c => `<div class="hi-ndc-row"><div><b>${esc(c.name)}</b><div class="fine muted">last ${esc(agoS(c.last))}${c.day ? ` · ${c.day} today` : ""}</div></div><button class="btn sm" type="button" data-hind="${esc(c.name)}">This is me</button></div>`).join("")}</div>`;
}
function myNDLinked(mine) {
  const l = mine.nd_names || [];
  return l.length ? `<p class="fine" style="margin-top:0">Also known in NextDNS as: ${l.map(n => `<b>${esc(n)}</b> <button class="hi-link fine" type="button" data-hiunlink="${esc(n)}">not me</button>`).join(", ")}</p>` : "";
}
async function ndName(name, op) {
  const r = await fetch("/api/hi/ndname", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ name, op }), cache: "no-store" });
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(j.error || `Something went wrong (HTTP ${r.status})`);
  start();
}
document.addEventListener("click", async e => {
  const b = e.target.closest("[data-hind], [data-hiunlink]"); if (!b) return;
  b.disabled = true;
  try { await ndName(b.dataset.hind || b.dataset.hiunlink, b.dataset.hind ? "link" : "unlink"); }
  catch (err) { b.disabled = false; let m = b.parentElement.querySelector(".hi-ask-err"); if (!m) { m = document.createElement("p"); m.className = "fine bad-t hi-ask-err"; b.after(m); } m.textContent = err.message; }
});
function myConnection(c) {
  if (!c) return "";
  const i = c.internet || {}, mb = v => v >= 1 ? Math.round(v) : v ? v.toFixed(1) : "";
  const net = i.status ? `<div class="hi-row"><span>Internet</span><b class="${i.status === "ok" ? "good-t" : "bad-t"}">${i.status === "ok" ? "Working" : "Having trouble"}</b>
    <span class="fine muted">${[i.latency ? `${i.latency} ms` : "", i.xput_down ? `↓ ${mb(i.xput_down)} ↑ ${mb(i.xput_up)} Mbps` : "", i.speedtest_lastrun ? `tested ${agoS(i.speedtest_lastrun)}` : ""].filter(Boolean).join(" · ")}</span></div>` : "";
  const q = { great: ["Great", "good-t"], good: ["Good", "good-t"], weak: ["Weak", "warn-t"], poor: ["Poor", "bad-t"] }[c.quality];
  const link = c.wired ? `<div class="hi-row"><span>Connection</span><b>Wired</b><span class="fine muted">${esc(c.network || "")}</span></div>`
    : `<div class="hi-row"><span>Wi-Fi</span><b>${esc(c.ssid || "–")}</b><span class="fine muted">${[c.band, c.ap ? `via ${c.ap}` : ""].filter(Boolean).map(esc).join(" · ")}</span></div>
      ${q ? `<div class="hi-row"><span>Signal</span><b class="${q[1]}">${q[0]}</b><span class="fine muted">${c.signal} dBm${c.rate ? ` · ${Math.round(c.rate / 1000)} Mbps link` : ""}</span></div>` : ""}
      ${c.quality === "weak" || c.quality === "poor" ? `<p class="fine">Weak signal: move closer to an access point${c.ap ? ` (it's on ${esc(c.ap)})` : ""}, or turn Wi-Fi off and on to pick a nearer one.</p>` : ""}`;
  const sum = [c.wired ? "wired" : q ? `${q[0].toLowerCase()} signal` : "", i.status ? (i.status === "ok" ? "internet working" : "internet trouble") : ""].filter(Boolean).join(" · ");
  return hiFold("Right now", link + net, "", sum);
}
const ASKED = { pending: ["asked · waiting", ""], allowed: ["allowed", "good-t"], denied: ["not allowed", "bad-t"], removed: ["allowed for a while, then blocked again", "muted"] };
function myBlocked(mine) {
  if (!mine.can_request) return "";
  const bl = mine.blocked || [], reqs = mine.requests || [];
  const row = b => `<div class="hi-bl"><div><b>${esc(b.domain)}</b><div class="fine muted">${[b.reasons ? `blocked by ${b.reasons}` : "blocked", `${b.n}×`, agoS(b.last)].map(esc).join(" · ")}</div></div>
    ${b.asked && ASKED[b.asked] && b.asked !== "removed" ? `<span class="fine ${ASKED[b.asked][1]}">${ASKED[b.asked][0]}</span>` : `<button class="btn sm" type="button" data-hiask="${esc(b.domain)}">Ask to allow</button>`}</div>`;
  const waiting = reqs.filter(q => q.status === "pending").length;
  const sum = [bl.length ? `${bl.length} site${bl.length === 1 ? "" : "s"} today` : "nothing today", waiting ? `${waiting} waiting` : ""].filter(Boolean).join(" · ");
  return `<details class="hi-fold" id="hi-blocked-fold"${hiBlockedOpen ? " open" : ""}><summary><span>Blocked on this device</span><span class="fine muted">${esc(sum)}</span></summary><div class="hi-fold-body">
    ${bl.length ? `<p class="fine muted">Sites NextDNS stopped in the last day. Something you need? Ask, and an admin decides.</p>${bl.map(row).join("")}` : `<p class="fine muted">Nothing blocked in the last day.</p>`}
    <form class="hi-ask-other" id="hi-ask-form"><input id="hi-ask-domain" placeholder="Another site, e.g. example.com" autocomplete="off" autocapitalize="off" spellcheck="false" aria-label="Site to ask for"><button class="btn sm" type="submit">Ask</button></form>
    ${reqs.length ? `<p class="fine" style="margin:12px 0 4px"><b>Your requests</b></p>${reqs.map(q => `<div class="hi-req"><span>${esc(q.domain)}</span><span class="fine ${ASKED[q.status]?.[1] || ""}">${esc(ASKED[q.status]?.[0] || q.status)}</span></div>`).join("")}` : ""}</div></details>`;
}
let hiBlockedOpen = false; // stays open when the list redraws after asking
document.addEventListener("toggle", e => { if (e.target.id === "hi-blocked-fold") hiBlockedOpen = e.target.open; }, true);
async function askSite(domain, note) {
  const r = await fetch("/api/hi/request", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ domain, note }), cache: "no-store" });
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(j.error || `Something went wrong (HTTP ${r.status})`);
  const st = await call("GET"); if (st.mine && $("#hi-blocked")) $("#hi-blocked").innerHTML = myBlocked(st.mine);
}
// "Ask to allow": a short reason, then send
function askForm(domain, el) {
  const row = el.closest(".hi-bl"); el.remove();
  row.insertAdjacentHTML("afterend", `<form class="hi-ask" data-hidomain="${esc(domain)}"><input name="note" maxlength="200" placeholder="Why do you need it? (optional)" aria-label="Why you need ${esc(domain)}"><button class="btn sm primary" type="submit">Ask</button></form>`);
  document.querySelector(`form.hi-ask[data-hidomain="${CSS.escape(domain)}"] input`)?.focus();
}
document.addEventListener("click", e => { const b = e.target.closest("[data-hiask]"); if (b) askForm(b.dataset.hiask, b); });
document.addEventListener("submit", async e => {
  const f = e.target.closest("form.hi-ask, #hi-ask-form"); if (!f) return;
  e.preventDefault();
  const domain = f.id === "hi-ask-form" ? $("#hi-ask-domain").value : f.dataset.hidomain, note = f.id === "hi-ask-form" ? "" : f.note.value;
  const btn = f.querySelector("button"); btn.disabled = true;
  try { await askSite(domain, note); }
  catch (err) { btn.disabled = false; let m = f.querySelector(".hi-ask-err"); if (!m) { m = document.createElement("p"); m.className = "fine bad-t hi-ask-err"; f.after(m); } m.textContent = err.message; }
});

// Android, Windows and the rest take no profile: the home Wi-Fi's name and password to join by hand (or scan from
// another phone), and the setup Wi-Fi to forget
async function homeWiFi() {
  const el = $("#hi-wifi");
  let r;
  try { const res = await fetch("/api/hi/wifi", { cache: "no-store" }); r = await res.json(); if (!res.ok) throw new Error(r.error || `HTTP ${res.status}`); }
  catch (err) { el.innerHTML = `<div class="msg err">${esc(err.message)}</div>`; return; }
  if (!r.networks.length) { el.innerHTML = ""; return; }
  el.innerHTML = `<h3 style="margin:16px 0 6px">Join the home Wi-Fi</h3>${r.networks.map(n => `
      <p>Network <b>${esc(n.ssid)}</b>${n.hidden ? " <span class=\"fine muted\">(hidden: type its name)</span>" : ""}</p>
      ${n.password ? copyRow(n.password, "password") : `<p class="fine">No password.</p>`}
      ${typeof QR === "object" ? `<details class="hi-qr"><summary>Show as a QR code</summary><p class="fine">Scan it with another device's camera to join it too.</p>${QR.svg(QR.wifi(n), { size: 200, label: `Join ${n.ssid}` })}</details>` : ""}`).join("")}
    ${r.leave ? `<p class="fine">Once you're on the home Wi-Fi, open <b>Settings</b> › <b>Wi-Fi</b>, tap <b>${esc(r.leave)}</b> and choose <b>Forget</b>.</p>` : ""}`;
}

start.tries = 0;
// hiWaiting: a big "we're on it" while the network looks for this device
function hiWaiting(n) {
  const steps = ["Connecting to the network…", "Looking for this device…", "Still looking: the network can take a moment…", "Almost there…"];
  return `<div class="hi-wait">
    <div class="hi-wait-ring"><img src="icon.png" alt="" width="64" height="64"></div>
    <h2>Registering this device</h2>
    <p class="hi-wait-step">${esc(steps[Math.min(Math.floor((n - 1) / 3), steps.length - 1)])}</p>
    <p class="fine muted">Keep this page open. It carries on by itself.</p></div>`;
}
document.addEventListener("click", e => { if (e.target.id === "hi-retry") { start.tries = 0; $("#hi-box").innerHTML = hiWaiting(1); start(); } });
async function start() {
  let st;
  try { st = await call("GET"); start.tries = 0; if (st.version && $("#hi-ver")) $("#hi-ver").textContent = `HomeOwl ${st.version}`; }
  catch (err) { // the network may be busy for a moment (a phone just joined): keep trying rather than leave a dead end
    const again = ++start.tries <= 40;
    $("#hi-box").innerHTML = again ? hiWaiting(start.tries) : `<div class="msg err">${esc(err.message)}</div><button class="btn primary" id="hi-retry" type="button">Try again</button>`;
    if (again) setTimeout(start, Math.min(3000 + start.tries * 500, 10000));
    return;
  }
  start.setup = st.setup_wifi || "";
  if (st.waiting && !start.force) return done(true);
  if (st.registered && !start.force) return registered(st);
  const d = st.device, kind = guessKind();
  $("#hi-box").innerHTML = `${start.setup ? `<div class="msg">You're on <b>${esc(start.setup)}</b>, the setup Wi-Fi. Check in here and keep this page open; once it's approved, it sets this device up for the home Wi-Fi.</div>${cellTip()}` : ""}
    <p class="muted">Tell HomeOwl what this device is and whose it is. An admin approves it, then it's named on the network.</p>
    <div class="hi-seen fine">The network sees this device as <b>${esc(d.name || d.hostname || "an unnamed device")}</b>${d.vendor ? ` (${esc(d.vendor)})` : ""}${d.network ? ` on ${esc(d.network)}` : ""}.</div>
    <form id="hi-form" autocomplete="off">
      <label>Whose device is it?
        <select id="hi-person" required>
          <option value="">Choose…</option>
          ${st.people.filter(p => p.kind !== "group").map(p => `<option value="${esc(p.id)}">${esc(p.name)}</option>`).join("")}
          ${st.people.some(p => p.kind === "group") ? `<optgroup label="Shared by everyone">${st.people.filter(p => p.kind === "group").map(p => `<option value="${esc(p.id)}">${esc(p.name)}</option>`).join("")}</optgroup>` : ""}
          <option value="__new">${st.people.length ? "Someone else…" : "Type a name…"}</option>
        </select></label>
      <label id="hi-newwrap" hidden>Name<input id="hi-newname" maxlength="40" placeholder="e.g. Alex"></label>
      <label>What is it?<select id="hi-kind">${KINDS.map(([k, l]) => `<option value="${k}" ${k === kind ? "selected" : ""}>${l}</option>`).join("")}</select></label>
      ${formModel(st.model, kind)}
      <label>Name for this device<input id="hi-name" maxlength="40" required placeholder="e.g. Alex's iPad"></label>
      <label class="check hi-main" id="hi-mainwrap" hidden><input type="checkbox" id="hi-main"> <span><b id="hi-mainq">Is this your main phone?</b><br><span class="fine muted">HomeOwl uses it to see when you're home (Who's home). Only for the one you carry with you.</span></span></label>
      <button class="btn primary" type="submit">Send to HomeOwl</button>
      <div id="hi-msg" class="msg" role="status"></div>
    </form>`;
  if (!st.people.length) { $("#hi-person").value = "__new"; $("#hi-newwrap").hidden = false; }
  start.groups = new Set(st.people.filter(p => p.kind === "group").map(p => p.id));
  mainAsk();
}

function suggestName() {
  const sel = $("#hi-person"), who = sel.value === "__new" ? $("#hi-newname").value.trim() : sel.selectedOptions[0]?.textContent || "";
  const kind = (KINDS.find(k => k[0] === $("#hi-kind").value)?.[1] || "").replace(/^\S+\s/, "");
  const nameEl = $("#hi-name");
  if (who && !nameEl.dataset.typed && $("#hi-kind").value !== "other") nameEl.value = `${who}'s ${kind}`;
}

document.addEventListener("change", e => {
  if (e.target.id === "hi-person") { $("#hi-newwrap").hidden = e.target.value !== "__new"; if (e.target.value === "__new") $("#hi-newname").focus(); }
  if (e.target.id === "hi-person" || e.target.id === "hi-kind") { suggestName(); mainAsk(); }
  if (e.target.id === "hi-main") $("#hi-main").dataset.touched = "1";
});
document.addEventListener("input", e => {
  if (e.target.id === "hi-name") e.target.dataset.typed = "1";
  if (e.target.id === "hi-newname") suggestName();
});
document.addEventListener("click", async e => {
  if (e.target.id === "hi-again") { start.force = true; start(); }
  const c = e.target.closest("[data-hicopy]");
  if (c) {
    try { await navigator.clipboard.writeText(c.dataset.hicopy); c.textContent = "Copied"; }
    catch { const r = document.createRange(); r.selectNodeContents(c.previousElementSibling); getSelection().removeAllRanges(); getSelection().addRange(r); c.textContent = "Selected"; }
  }
});
// "is this your main device?": for a person's own device (not a shared one); ticked for a phone unless they chose
function mainAsk() {
  const sel = $("#hi-person")?.value || "", kind = $("#hi-kind")?.value || "", box = $("#hi-main"); if (!box) return;
  const own = sel && !start.groups?.has(sel), ask = own && ["phone", "watch", "tablet", "laptop"].includes(kind);
  $("#hi-mainwrap").hidden = !ask;
  const what = { phone: "phone", watch: "watch", tablet: "tablet", laptop: "laptop" }[kind] || "device";
  $("#hi-mainq").textContent = `Is this ${sel === "__new" ? "their" : "your"} main ${what}?`;
  if (!box.dataset.touched) box.checked = ask && kind === "phone";
}
document.addEventListener("submit", async e => {
  if (e.target.id !== "hi-form") return;
  e.preventDefault();
  const msg = $("#hi-msg"), sel = $("#hi-person").value;
  msg.className = "msg"; msg.textContent = "Sending…";
  try {
    await call("POST", { name: $("#hi-name").value, kind: $("#hi-kind").value, person_id: sel === "__new" ? "" : sel, person_name: sel === "__new" ? $("#hi-newname").value : "", main: !$("#hi-mainwrap").hidden && $("#hi-main").checked, model: $("#hi-fmodel")?.value || "" });
    done(false);
  } catch (err) { msg.className = "msg err"; msg.textContent = err.message; }
});
start();
