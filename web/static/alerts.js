/* HomeOwl — Home › Alerts: what HomeOwl noticed (a new device joined, the guest password changed…), with the
   buttons to act on it, and Settings › Alerts: what to alert about and where to send it (macOS, ntfy, webhook).
   Depends on core.js, app.js. */
"use strict";

S.alerts = null;
const alLive = () => S.source !== "demo" && S.source !== "file";   // a running HomeOwl keeps alerts even before UniFi is connected
const AL_ICON = { new_device: "unknown", guest_rotated: "router", test: "iot", setup_lost: "phone", setup_back: "phone", checkin: "phone", approval: "unknown", approval_flood: "unknown", approval_done: "unknown", archive: "unknown", site_request: "unknown" };

// demo: two fictional new devices
function alFromBundle() {
  const c = arr(S.model?.clients), now = Date.now();
  return { demo: true, unread: 2, items: [
    ...(c[3] ? [{ id: 2, time: now - 40 * 60e3, kind: "new_device", level: "warn", title: `New device: ${c[3].label}`, body: [c[3].vendor, c[3].ip, c[3].network].filter(Boolean).join(" · "), data: { mac: c[3].mac } }] : []),
    { id: 1, time: now - 26 * 3600e3, kind: "guest_rotated", level: "info", title: "New guest Wi-Fi password", body: "Guests has a new password. The guest card shows it; print new cards from Guest Wi-Fi.", read: true },
  ] };
}

V.alerts = () => `<div class="head"><div><h1>Alerts ${hint("HomeOwl tells you when a device it has never seen joins your network, and when the guest Wi-Fi password changes. Choose where else alerts go in Settings › Alerts.")}</h1></div>
  <div class="btnrow">${S.role === "admin" && alLive() ? `<button class="btn" data-setgo="alerts">Alert settings</button>` : ""}<button class="btn" id="al-read">Mark all read</button></div></div>
  <div id="al-body"><div class="empty">Loading…</div></div>`;

async function loadAlerts(markRead) {
  if (alLive()) { try { S.alerts = await api("/api/alerts"); } catch (e) { S.alerts = S.alerts || { items: [], unread: 0 }; const el = $("#al-body"); if (el) el.innerHTML = `<div class="empty bad-t">${esc(e.message)}</div>`; return; } }
  else S.alerts = alFromBundle();
  alBadge(S.alerts.unread);
  if ((S.alerts.attention?.length || S.alerts.duplicates?.length || S.alerts.requests?.length) && !loadAlerts.drawn && (S.view === "today" || S.view === "overview")) { loadAlerts.drawn = true; render(); }
  const el = $("#al-body"); if (!el || S.view !== "alerts") return;
  let people = [];
  if (S.source === "live" && S.role === "admin" && S.alerts.items.some(a => a.kind === "new_device" && a.data?.mac)) { try { people = (await api("/api/analytics/meta", {})).people || []; } catch { /* no analytics: no "Give to" */ } }
  el.innerHTML = alList(S.alerts.items, people);
  if (markRead && S.alerts.unread && alLive()) setTimeout(() => api("/api/alerts/read", { id: 0 }).then(r => alBadge(r.unread)).catch(() => { }), 1500);
}
const alBadge = n => { const b = $("#nav-alerts"); if (b) { b.textContent = n || ""; b.classList.toggle("alert", !!n); } };

function alList(items, people) {
  if (!items.length) return emptyState({ title: "No alerts yet", text: "When a device HomeOwl has never seen joins your network, it shows up here, with buttons to name it, give it to someone or block it.",
    btn: S.role === "admin" && S.source === "live" ? `<button class="btn" data-setgo="alerts">Choose where alerts go</button>` : "" });
  let day = "";
  return `<div class="card al-list">${items.map(a => {
    const d = new Date(a.time).toDateString(), head = d !== day ? `<div class="al-day">${esc(new Date(a.time).toLocaleDateString([], { weekday: "long", day: "numeric", month: "long" }))}</div>` : ""; day = d;
    const mac = a.data?.mac, inModel = mac && (S.model?.clients.some(c => c.mac === mac) || S.model?.knownByMac?.[mac]);
    const acts = a.kind === "new_device" && mac ? [
      canRename(mac) ? `<button class="btn sm" data-rename="${esc(mac)}">Name it</button>` : "",
      people.length ? `<select data-algive="${esc(mac)}" aria-label="Give this device to"><option value="">Give to…</option>${people.map(p => `<option value="${esc(p.id)}">${esc(p.name)}</option>`).join("")}</select>` : "",
      S.source === "live" ? actBtn({ kind: "stacmd", cmd: "block-sta", mac, label: a.data?.name || mac, description: `Block ${a.data?.name || mac}` }, "Block") : "",
      inModel ? `<button class="btn sm" data-alshow="${esc(mac)}">Details</button>` : "",
    ].join("") : a.kind === "guest_rotated" ? `<button class="btn sm" data-goto="guest">Guest Wi-Fi</button>`
      : a.kind === "approval" && S.role === "admin" && a.data?.mac ? `<button class="btn sm primary" data-alapv="${esc(a.data.mac)}">Approve</button>${a.data.grace ? `<button class="btn sm" data-alapvblock="${esc(a.data.mac)}">Block</button>` : `<button class="btn sm" data-alapvkeep="${esc(a.data.mac)}">Keep blocked</button>`}`
      : a.kind === "site_request" && S.role === "admin" && a.data?.request ? siteReqBtns(a.data.request)
      : a.kind === "archive" && S.role === "admin" ? `<button class="btn sm" data-goto="clients">Review</button>`
      : a.kind === "approval_flood" && S.role === "admin" ? `<button class="btn sm" data-goto="settings">Settings</button>`
      : (a.kind === "setup_lost" || a.kind === "checkin") && S.role === "admin" ? `<button class="btn sm" data-goto="checkins">${a.kind === "checkin" ? "Approve…" : "New devices"}</button>` : "";
    return `${head}<div class="al-row${a.read ? "" : " unread"}${a.level === "warn" ? " warn" : ""}">
      <span class="al-ic">${iconSvg(AL_ICON[a.kind] || "iot", 18)}</span>
      <div class="al-main"><b>${esc(a.title)}</b>${a.body ? `<span class="fine muted">${esc(a.body)}</span>` : ""}${mac ? `<span class="fine mono muted">${esc(mac)}</span>` : ""}</div>
      <span class="fine muted al-time">${new Date(a.time).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}</span>
      ${acts ? `<div class="btnrow al-acts">${acts}</div>` : ""}</div>`;
  }).join("")}</div>`;
}

// a family member asked for a blocked site (myhome.go): allow puts it on the NextDNS allowlist of their device's profile
const siteReqBtns = id => { const r = arr(S.alerts?.requests).find(x => x.id === id);
  return r ? `<span class="btnrow"><button class="btn sm primary" data-sitereq="${esc(id)}" data-op="allow">Allow</button><button class="btn sm" data-sitereq="${esc(id)}" data-op="deny">Deny</button></span>` : ""; };
async function decideSiteReq(id, op) {
  try { S.siteReqs = await api("/api/site-requests/decide", { id, op }); toast({ allow: "Allowed — it works on that device in a minute", deny: "It stays blocked", remove: "Blocked again" }[op], "ok"); }
  catch (err) { toast(err.message, "err"); }
  loadAlerts(); if ($("#ci-reqs")) $("#ci-reqs").innerHTML = siteReqCard(S.siteReqs);
}
function siteReqCard(v) {
  const l = arr(v?.items); if (!l.length) return "";
  const st = { pending: `<span class="tag warn">waiting</span>`, allowed: `<span class="tag good">allowed</span>`, denied: `<span class="tag">denied</span>`, removed: `<span class="tag">blocked again</span>` };
  return `<div class="card section"><h2>Site requests ${hint("From a device's own HomeOwl page (the check-in page), family members ask for a site NextDNS blocked. Allowing it puts it on the allowlist of that device's NextDNS profile, so for everyone on that profile, and logs it in History › Changes. “Stop allowing” takes it off again.")}</h2>${table("site-reqs", [
    { k: "domain", label: "Site", fmt: r => `<b>${esc(r.domain)}</b>${r.note ? `<div class="fine">“${esc(r.note)}”</div>` : ""}${r.reasons ? `<div class="fine muted">blocked by ${esc(r.reasons)}</div>` : ""}` },
    { k: "device", label: "Asked from", fmt: r => `${esc(r.device)}${r.person ? `<div class="fine muted">${esc(r.person)}</div>` : ""}` },
    { k: "profile", label: "NextDNS profile", fmt: r => esc(v.profiles?.[r.profile] || r.profile) },
    { k: "time", label: "When", fmt: r => ago(r.time * 1000), sort: r => -r.time },
    { k: "status", label: "", fmt: r => `${st[r.status] || esc(r.status)}${r.by ? `<div class="fine muted">${esc(r.by)}</div>` : ""}` },
    { k: "act", label: "", fmt: r => r.status === "pending" ? `<div class="btnrow"><button class="btn sm primary" data-sitereq="${esc(r.id)}" data-op="allow">Allow</button><button class="btn sm" data-sitereq="${esc(r.id)}" data-op="deny">Deny</button></div>`
      : r.status === "allowed" ? `<button class="btn sm" data-sitereq="${esc(r.id)}" data-op="remove">Stop allowing</button>` : r.status === "denied" || r.status === "removed" ? `<button class="btn sm" data-sitereq="${esc(r.id)}" data-op="allow">Allow</button>` : "" },
  ], l)}</div>`;
}
async function loadSiteReqs() { const el = $("#ci-reqs"); if (!el) return; try { S.siteReqs = await api("/api/site-requests"); el.innerHTML = siteReqCard(S.siteReqs); } catch { } }

document.addEventListener("click", e => {
  if (e.target.id === "al-read") { if (alLive()) api("/api/alerts/read", { id: 0 }).then(() => loadAlerts()).catch(err => toast(err.message, "err")); return; }
  const sh = e.target.closest("[data-alshow]"); if (sh) { showClient(sh.dataset.alshow); return; }
  const sr = e.target.closest("[data-sitereq]"); if (sr) { sr.disabled = true; decideSiteReq(sr.dataset.sitereq, sr.dataset.op); return; }
  const apv = e.target.closest("[data-alapv],[data-alapvkeep],[data-alapvblock]");
  if (apv) {
    const op = apv.dataset.alapvkeep ? "keep" : apv.dataset.alapvblock ? "block" : "approve", mac = apv.dataset.alapv || apv.dataset.alapvkeep || apv.dataset.alapvblock; apv.disabled = true;
    api(`/api/approval/${op}`, { mac }).then(() => toast({ keep: "Kept blocked", block: "Blocked", approve: "Approved — it can connect now" }[op], "ok")).catch(err => toast(err.message, "err"));
    return;
  }
  if (e.target.id === "al-ntfy-gen") {
    const abc = "abcdefghijkmnpqrstuvwxyz23456789", r = crypto.getRandomValues(new Uint8Array(20));
    const topic = "homeowl-" + [...r].map(x => abc[x % abc.length]).join(""), url = "https://ntfy.sh/" + topic;
    $("#al-ntfy").value = url;
    const how = $("#al-ntfy-how"); how.hidden = false;
    how.innerHTML = `<div class="msg"><b>Now subscribe on each phone</b> (yours, and the other parent's):
      <ol class="fine"><li>Install the free <b>ntfy</b> app (App Store or Google Play).</li><li>Tap <b>+</b>, enter the topic <span class="mono">${esc(topic)}</span> and subscribe (server: ntfy.sh).</li><li>Back here: <b>Save</b>, then <b>Send a test</b>.</li></ol>
      <div class="cmd"><code>${esc(topic)}</code><button class="btn sm" data-copy="${esc(topic)}">Copy</button></div></div>`;
    return;
  }
  if (e.target.id === "al-test") { api("/api/alerts/test", {}).then(() => toast("Test sent", "ok")).catch(err => toast(err.message, "err")).finally(() => setTimeout(renderAlertSettings, 300)); return; }
  if (e.target.id === "al-save") {
    const body = { new_device: $("#al-new").value, setup: !!$("#al-setup")?.checked, macos: !!$("#al-mac")?.checked };
    const nt = $("#al-ntfy").value.trim(), wh = $("#al-hook").value.trim(); if (nt) body.ntfy = nt; if (wh) body.webhook = wh;
    api("/api/alerts/settings", body).then(() => { toast("Alert settings saved", "ok"); renderAlertSettings(); }).catch(err => toast(err.message, "err")); return;
  }
  const rm = e.target.closest("[data-alrm]"); if (rm) { api("/api/alerts/settings", { [rm.dataset.alrm]: "-" }).then(() => { toast("Removed", "ok"); renderAlertSettings(); }).catch(err => toast(err.message, "err")); }
});
document.addEventListener("change", e => {
  const g = e.target.closest("[data-algive]"); if (!g || !g.value) return;
  api("/api/alerts/assign", { mac: g.dataset.algive, person: g.value }).then(() => toast(`Given to ${g.selectedOptions[0].textContent}`, "ok")).catch(err => toast(err.message, "err"));
});

// ---- Settings › Alerts
async function renderAlertSettings() {
  const el = $("#st-alerts"); if (!el) return;
  let r; try { r = await api("/api/alerts"); } catch (e) { el.innerHTML = `<div class="empty bad-t">${esc(e.message)}</div>`; return; }
  const st = r.settings || {};
  const saved = (k, label) => st[`${k}_set`] ? `<span class="tag good">saved</span> <a href="#" data-alrm="${k}">Remove</a>` : `<span class="fine muted">${label}</span>`;
  el.innerHTML = `<h2>Alerts</h2>
    <label>Tell me when a new device joins<select id="al-new">${[["home", "On my own networks"], ["all", "Anywhere, guest Wi-Fi too"], ["off", "Never"]].map(([k, l]) => `<option value="${k}" ${st.new_device === k ? "selected" : ""}>${l}</option>`).join("")}</select></label>
    <p class="fine muted">A new device is one HomeOwl has never seen. Everything UniFi already knew when you turned this on counts as known.</p>
    <label class="check"><input type="checkbox" id="al-setup" ${st.setup !== false ? "checked" : ""}> <span>Tell me when a device stops using its NextDNS setup</span></label>
    <p class="fine muted">For devices set up from their check-in page: HomeOwl notices when one was on the home network for hours (or away for three days) without NextDNS seeing it by name — usually the profile was removed or Private DNS switched off.</p>
    <h3>Where alerts go</h3>
    <p class="fine">Always to <a href="#" data-goto="alerts">Home › Alerts</a>. Also:</p>
    ${st.macos_possible ? `<label class="check"><input type="checkbox" id="al-mac" ${st.macos ? "checked" : ""}> <span>A notification on this Mac</span></label>` : ""}
    <label>Phone push with ntfy ${saved("ntfy", "not set")}<span class="al-ntfy-row"><input id="al-ntfy" placeholder="https://ntfy.sh/a-long-secret-topic-name" autocomplete="off"><button class="btn sm" id="al-ntfy-gen" type="button">Generate a private topic</button></span></label>
    <div id="al-ntfy-how" hidden></div>
    <p class="fine muted">Install the free ntfy app and subscribe to a topic nobody can guess (Generate makes one). Anyone who knows the topic can read the alerts, so keep it to the family. New-device alerts get <b>Approve</b> and <b>Block</b> buttons that work from anywhere: a tap goes back through ntfy, and HomeOwl only acts on its own signed, one-time answers.</p>
    <label>Webhook ${saved("webhook", "not set")}<input id="al-hook" placeholder="https://… (Home Assistant, Slack, Discord, n8n)" autocomplete="off"></label>
    <p class="fine muted">HomeOwl posts JSON with <span class="mono">title</span>, <span class="mono">body</span> and <span class="mono">kind</span>, plus <span class="mono">text</span> and <span class="mono">content</span>, so Slack and Discord show it as is. Passwords are never included.</p>
    ${st.error ? `<div class="msg err">Last alert couldn't be sent: ${esc(st.error)}</div>` : ""}
    <div class="btnrow" style="margin-top:10px"><button class="btn primary" id="al-save">Save</button><button class="btn" id="al-test">Send a test</button></div>`;
}

setInterval(() => { if (alLive() && !document.hidden) api("/api/alerts").then(r => { alBadge(r.unread); if (S.view === "alerts" && r.items.length !== S.alerts?.items.length) loadAlerts(); }).catch(() => { }); }, 60000);
{ const _render = render; render = function () { _render(); if (S.view === "alerts") loadAlerts(true); else if (alLive() && S.alerts === null) loadAlerts(); }; }
