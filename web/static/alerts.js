/* HomeOwl — Records › Alerts: what HomeOwl noticed (a new device joined, the guest password changed…), with the
   buttons to act on it, and Settings › Alerts: what to alert about and where to send it (macOS, ntfy, webhook).
   Depends on core.js, app.js. */
"use strict";

S.alerts = null;
const alLive = () => S.source !== "demo" && S.source !== "file";   // a running HomeOwl keeps alerts even before UniFi is connected
const AL_ICON = { new_device: "unknown", guest_rotated: "router", test: "iot", setup_lost: "phone", setup_back: "phone" };

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
  if (S.alerts.attention?.length && !loadAlerts.drawn && (S.view === "today" || S.view === "overview")) { loadAlerts.drawn = true; render(); }
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
      : a.kind === "setup_lost" && S.role === "admin" ? `<button class="btn sm" data-goto="checkins">Check-ins</button>` : "";
    return `${head}<div class="al-row${a.read ? "" : " unread"}${a.level === "warn" ? " warn" : ""}">
      <span class="al-ic">${iconSvg(AL_ICON[a.kind] || "iot", 18)}</span>
      <div class="al-main"><b>${esc(a.title)}</b>${a.body ? `<span class="fine muted">${esc(a.body)}</span>` : ""}${mac ? `<span class="fine mono muted">${esc(mac)}</span>` : ""}</div>
      <span class="fine muted al-time">${new Date(a.time).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}</span>
      ${acts ? `<div class="btnrow al-acts">${acts}</div>` : ""}</div>`;
  }).join("")}</div>`;
}

document.addEventListener("click", e => {
  if (e.target.id === "al-read") { if (alLive()) api("/api/alerts/read", { id: 0 }).then(() => loadAlerts()).catch(err => toast(err.message, "err")); return; }
  const sh = e.target.closest("[data-alshow]"); if (sh) { showClient(sh.dataset.alshow); return; }
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
    <p class="fine">Always to <a href="#" data-goto="alerts">Records › Alerts</a>. Also:</p>
    ${st.macos_possible ? `<label class="check"><input type="checkbox" id="al-mac" ${st.macos ? "checked" : ""}> <span>A notification on this Mac</span></label>` : ""}
    <label>Phone push with ntfy ${saved("ntfy", "not set")}<input id="al-ntfy" placeholder="https://ntfy.sh/a-long-secret-topic-name" autocomplete="off"></label>
    <p class="fine muted">Install the free ntfy app, subscribe to a topic name nobody can guess, and paste its address here. Anyone who knows the topic can read the alerts.</p>
    <label>Webhook ${saved("webhook", "not set")}<input id="al-hook" placeholder="https://… (Home Assistant, Slack, Discord, n8n)" autocomplete="off"></label>
    <p class="fine muted">HomeOwl posts JSON with <span class="mono">title</span>, <span class="mono">body</span> and <span class="mono">kind</span>, plus <span class="mono">text</span> and <span class="mono">content</span>, so Slack and Discord show it as is. Passwords are never included.</p>
    ${st.error ? `<div class="msg err">Last alert couldn't be sent: ${esc(st.error)}</div>` : ""}
    <div class="btnrow" style="margin-top:10px"><button class="btn primary" id="al-save">Save</button><button class="btn" id="al-test">Send a test</button></div>`;
}

setInterval(() => { if (alLive() && !document.hidden) api("/api/alerts").then(r => { alBadge(r.unread); if (S.view === "alerts" && r.items.length !== S.alerts?.items.length) loadAlerts(); }).catch(() => { }); }, 60000);
{ const _render = render; render = function () { _render(); if (S.view === "alerts") loadAlerts(true); else if (alLive() && S.alerts === null) loadAlerts(); }; }
