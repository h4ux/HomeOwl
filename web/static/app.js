/* HomeOwl — views, actions, device debugging, boot. Depends on core.js */
"use strict";

// ------------------------------------------------------------------ API, modal, toast
async function api(path, body) {
  const r = await fetch(path, { method: body ? "POST" : "GET", headers: { "Content-Type": "application/json", "X-Audit-Token": S.token || "" }, body: body ? JSON.stringify(body) : undefined });
  const j = await r.json().catch(() => ({}));
  if (r.status === 401 && !path.startsWith("/api/auth/") && S.source !== "demo" && S.source !== "file") { setTimeout(boot, 0); }
  if (!r.ok) throw new Error(j.error || `HTTP ${r.status}`); return j;
}
const busy = (on, txt) => { $("#loading").hidden = !on; if (txt) $("#loading-txt").textContent = txt; };
function toast(msg, kind = "") { const t = document.createElement("div"); t.className = `toast ${kind}`; t.textContent = msg; $("#toasts").append(t); setTimeout(() => t.remove(), 6000); }
let MODAL_BTNS = [];
function openModal({ title, body, buttons = [{ label: "Close" }], wide = false }) {
  MODAL_BTNS = buttons;
  $("#modal-box").className = "modal-box" + (wide ? " wide" : "");
  $("#modal-box").innerHTML = `<div class="modal-head"><h2>${esc(title)}</h2><button class="x" data-mclose aria-label="Close">×</button></div><div class="modal-body">${body}</div>
    <div class="modal-foot">${buttons.map((b, i) => `<button class="btn ${b.cls || ""}" data-mbtn="${i}">${esc(b.label)}</button>`).join("")}</div>`;
  $("#modal").hidden = false;
}
const closeModal = () => { $("#modal").hidden = true; };
document.addEventListener("click", async e => {
  if (e.target.closest("[data-mclose]") || e.target.id === "modal") return closeModal();
  const b = e.target.closest("[data-mbtn]"); if (!b) return;
  const def = MODAL_BTNS[+b.dataset.mbtn]; if (def?.onClick) { b.disabled = true; try { await def.onClick(); } finally { b.disabled = false; } } else closeModal();
});
document.addEventListener("keydown", e => { if (e.key === "Escape") closeModal(); });

// ------------------------------------------------------------------ actions
const ACTIONS = {}; let ACT_SEQ = 0;
const regAct = (actions, title) => { const id = "a" + (++ACT_SEQ); ACTIONS[id] = { actions: [].concat(actions).filter(Boolean), title }; return id; };
const actBtn = (actions, label, title, cls = "sm") => { const a = [].concat(actions).filter(Boolean); if (!a.length || S.source !== "live" || !S.model?.hasLegacy || S.role !== "admin") return ""; return `<button class="btn ${cls} act" data-act="${regAct(a, title || label)}">${esc(label)}</button>`; };
document.addEventListener("click", e => { const b = e.target.closest("[data-act]"); if (!b) return; e.preventDefault(); e.stopPropagation(); const a = ACTIONS[b.dataset.act]; if (a) runActions(a.actions, a.title); }, true);

const fmtVal = v => v === null || v === undefined ? "–" : typeof v === "object" ? JSON.stringify(v) : String(v);
async function runActions(actions, title) {
  if (S.source !== "live") return toast("Actions need a live connection to the console.", "err");
  if (S.readOnly) return toast("The server was started with --read-only.", "err");
  busy(true, "Preparing preview…");
  const previews = [];
  for (const a of actions) { try { previews.push({ a, p: (await api("/api/action", { action: a, dry_run: true })).preview }); } catch (err) { previews.push({ a, err: err.message }); } }
  busy(false);
  const ok = previews.filter(x => x.p);
  const body = `<p class="muted">Review exactly what will be sent to the console. ${S.write ? "" : `<b>Change mode is locked</b> — applying will unlock it for this session.`}</p>` +
    previews.map(({ a, p, err }) => `<div class="pv ${err ? "pv-err" : ""}"><div class="pv-h"><b>${esc(p?.description || a.description || a.kind)}</b> <span class="muted">${esc(p?.target || "")}</span></div>
      ${err ? `<div class="bad-t">Can't do this: ${esc(err)}</div>` : `
      ${Object.keys(p.after).length ? `<table class="pv-t"><tr><th>Setting</th><th>Now</th><th>New</th></tr>${Object.keys(p.after).map(k => `<tr><td class="mono">${esc(k)}</td><td>${esc(fmtVal(p.before[k]))}</td><td><b>${esc(fmtVal(p.after[k]))}</b></td></tr>`).join("")}</table>` : ""}
      <div class="fine mono">${esc(p.method)} ${esc(p.path)}</div><div class="fine">${p.undoable ? "↩ Can be undone from the Changes page." : "⚠ Can't be undone automatically."}</div>`}</div>`).join("");
  openModal({ title, body, wide: true, buttons: [{ label: "Cancel" }, ...(ok.length ? [{ label: `${S.write ? "Apply" : "Unlock & apply"} ${plural(ok.length, "change")}`, cls: "primary", onClick: () => applyActions(ok.map(x => x.a)) }] : [])] });
}
async function applyActions(actions) {
  closeModal();
  try { if (!S.write) await setWrite(true); } catch (err) { return toast(err.message, "err"); }
  busy(true, "Applying…"); const results = [];
  for (const a of actions) { try { results.push((await api("/api/action", { action: a, dry_run: false })).result); } catch (err) { results.push({ ok: false, error: err.message, description: a.description }); } }
  busy(false);
  const good = results.filter(r => r.ok).length, bad = results.length - good;
  toast(bad ? `${good} applied, ${bad} failed — see Changes` : `${plural(good, "change")} applied`, bad ? "err" : "ok");
  if (bad) openModal({ title: "Some changes failed", body: results.filter(r => !r.ok).map(r => `<p><b>${esc(r.description || r.target || "")}</b><br><span class="bad-t">${esc(r.error)}</span></p>`).join("") + `<p class="fine">A 401/403 here usually means the API key's role can't make changes — use a key created by a Super Admin / Full Management user.</p>` });
  if (actions.some(a => a.cmd === "speedtest")) pollSpeedtest();
  setTimeout(() => collectLive(true, true).catch(() => { }), 2500);
}
async function setWrite(on) { const r = await api("/api/write-mode", { enabled: on }); S.write = r.write; renderLock(); }
function renderLock() {
  const b = $("#btn-lock"); if (!b) return;
  b.hidden = S.source !== "live" || S.role !== "admin";
  b.className = "btn sm lock " + (S.write ? "unlocked" : "");
  b.textContent = S.readOnly ? "🔒 Read-only server" : S.write ? "🔓 Changes unlocked" : "🔒 Changes locked";
}
async function toggleLock() {
  if (S.readOnly) return;
  if (S.write) { await setWrite(false); return toast("Changes locked"); }
  openModal({ title: "Unlock changes?", body: `<p>While unlocked, this app can change your live UniFi configuration — but only through buttons you click, each one previewed first.</p><p class="muted">Every change is logged (changes-log.jsonl next to server.py) and most can be undone from the Changes page. Locking again or restarting the server re-locks it.</p>`,
    buttons: [{ label: "Cancel" }, { label: "Unlock", cls: "primary", onClick: async () => { await setWrite(true); closeModal(); toast("Changes unlocked", "ok"); } }] });
}

// ------------------------------------------------------------------ views
const V = {};
const kpi = (v, l, s = "") => `<div class="card kpi"><div class="l">${l}</div><div class="v">${v}</div><div class="s">${s}</div></div>`;

V.overview = m => {
  const F = S.findings, sc = scoreOf(F), cnt = s => F.filter(f => f.sev === s).length;
  const online = m.devices.filter(d => d.online).length, wired = m.clients.filter(c => c.wired).length;
  const byNet = {}; m.clients.forEach(c => { const k = c.network || "Unknown"; byNet[k] = (byNet[k] || 0) + 1; });
  const bands = { "2.4 GHz": 0, "5 GHz": 0, "6 GHz": 0, Wired: wired }; m.clients.filter(c => !c.wired).forEach(c => { const k = bandLabel(c.band); if (k in bands) bands[k]++; });
  const fw = { "Up to date": m.devices.filter(d => d.online && !d.upgradable).length, "Update available": m.devices.filter(d => d.online && d.upgradable).length, "Offline / not ready": m.devices.filter(d => !d.online).length };
  const last24 = m.history.hourly.filter(r => r.t > Date.now() - 86400000);
  const problem = m.devices.filter(d => !d.online);
  return `<div class="head"><div><h1>Overview</h1><div class="muted">${esc(m.info.hostname || m.info.host || "")} · site ${esc(m.info.site)} · Network ${esc(m.info.networkVersion || "?")} · collected ${esc(m.info.collectedAt || "")}</div></div></div>
  ${problem.length ? `<div class="banner">⚠ ${plural(problem.length, "device")} not working: ${problem.map(d => `<a href="#" data-dev="${esc(d.mac)}">${esc(d.name)}</a> (${esc(d.stateLabel)})`).join(", ")} — click to debug.</div>` : ""}
  <div class="grid g3">
    <div class="card"><h2>Configuration score</h2><div class="score">${gauge(sc)}<div><div class="muted" style="font-size:13px">${F.length ? `${plural(F.filter(f => f.sev !== "info").length, "issue")} + ${cnt("info")} tips · ${F.filter(f => f.actions.length).length} fixable here` : "No issues found"}</div>
      <div class="sevbar">${SEV_ORDER.map(s => `<span class="sev ${s}">${cnt(s)} ${s}</span>`).join("")}</div>
      <p style="margin-top:12px"><a href="#" data-goto="findings">See all suggestions →</a></p></div></div></div>
    <div class="card"><h2>Internet${m.wanLinks.length > 1 ? ` <span class="muted" style="font-weight:400;font-size:13px">· ${m.wanLinks.length} connections</span>` : ""}</h2>
      ${m.wanLinks.length ? `<div class="wan-list">${m.wanLinks.map(wanRow).join("")}</div>` : `<p class="muted">No internet connection reported.</p>`}
      <dl class="kv" style="margin-top:10px">
      <dt>Speed test</dt><dd>${m.info.down !== null ? `↓ ${fmtMbps(m.info.down)} · ↑ ${fmtMbps(m.info.up)} <span class="muted">${m.info.speedtestAt ? ago(m.info.speedtestAt) : ""}</span>` : "–"} ${actBtn({ kind: "devcmd", cmd: "speedtest", description: "Run an internet speed test on the gateway (uses your bandwidth for ~30 s)" }, "Run now", "Speed test")}</dd>
      <dt>VPN</dt><dd>${m.vpn.remoteUser ? m.vpn.remoteUser + " remote-user server(s)" : "none"}${m.vpn.siteToSite ? ` · ${m.vpn.siteToSite} site-to-site` : ""}</dd></dl></div>
    <div class="card"><h2>Device status</h2>${donut(Object.entries(fw).map(([label, value], i) => ({ label, value, color: ["var(--ok)", "var(--high)", "var(--crit)"][i] })), m.devices.length, "devices")}</div>
  </div>
  <div class="grid g4 section">${kpi(`${online}/${m.devices.length}`, "Devices online", `${m.devices.filter(d => d.role === "ap").length} APs · ${m.devices.filter(d => d.role === "sw").length} switches`)}
    ${kpi(m.clients.length, "Active clients", `${wired} wired · ${m.clients.length - wired} wireless · ${m.known.length} known`)}
    ${kpi(m.networks.length, "Networks", `${m.networks.filter(n => n.isolated || n.guest).length} isolated/guest`)}
    ${kpi(m.wlans.filter(w => w.enabled).length, "SSIDs", `${m.portForwards.filter(p => p.enabled).length} port forwards · IPS ${esc(m.settings.ips ?? "?")}`)}</div>
  <div class="grid g2 section">
    <div class="card"><h2>Internet traffic — last 24 h</h2>${lineChart([{ name: "Download", color: "var(--c1)", fill: true, points: last24.map(r => ({ t: r.t, v: r.wanRx })) }, { name: "Upload", color: "var(--c3)", points: last24.map(r => ({ t: r.t, v: r.wanTx })) }], { fmt: fmtBytes, height: 170 })}</div>
    <div class="card"><h2>Clients by connection</h2>${donut(Object.entries(bands).map(([label, value], i) => ({ label, value, color: PAL[[2, 0, 3, 1][i]] })), m.clients.length, "clients")}
      <h3 style="margin-top:14px">Per network</h3>${hbar(Object.entries(byNet).sort((a, b) => b[1] - a[1]).map(([label, value], i) => ({ label, value, color: PAL[i % PAL.length] })))}</div>
  </div>
  <div class="card section"><h2>Top suggestions</h2>${F.filter(f => f.sev !== "info").slice(0, 5).map(findingCard).join("") || `<div class="empty">Nothing urgent — nice.</div>`}</div>`;
};

// one internet connection on the Overview: provider badge, name, status, IP, latency
function wanRow(l) {
  const st = !l.enabled ? `<span class="tag">off</span>` : l.status ? `<span class="tag ${l.status === "ok" ? "good" : "bad"}">${esc(l.status === "ok" ? "up" : l.status)}</span>` : "";
  const role = l.active ? `<span class="tag good" title="Traffic currently goes out this way">active</span>` : l.lb === "failover-only" ? `<span class="tag">backup</span>` : "";
  const bits = [l.ip ? `<span class="mono">${esc(l.ip)}</span>` : "", l.latency !== null ? `${l.latency} ms` : "", l.availability !== null ? `${l.availability}% up` : "", l.speed ? `${fmtSpeed(l.speed)}bps link` : ""].filter(Boolean);
  const canSet = S.role === "admin" && S.source === "live" && l.isp;
  return `<div class="wan-row">${ispBadge(l.isp, 36)}<div class="wan-main"><div class="wan-top"><b>${esc(l.isp || l.name)}</b> ${st} ${role}</div>
    <div class="muted wan-sub">${l.isp ? esc(l.name) + (bits.length ? " · " : "") : ""}${bits.join(" · ")}</div></div>
    ${canSet ? `<button class="btn ghost sm" data-isplogo="${esc(l.isp)}" title="${ISP_LOGOS[String(l.isp).trim().toLowerCase()] ? "Change" : "Add"} ${esc(l.isp)}'s logo" aria-label="Provider logo">✎</button>` : ""}</div>`;
}
function editIspLogo(isp) {
  openModal({ title: `Logo for ${isp}`, body: `<p>HomeOwl can show the provider's own icon. Enter their website and HomeOwl fetches its icon once and keeps it on this computer.</p>
    <label>Provider website<input id="isp-site" placeholder="e.g. example.net" autocomplete="off"></label><div id="isp-msg" class="msg" role="status"></div>`,
    buttons: [{ label: "Cancel" }, ...(ISP_LOGOS[isp.trim().toLowerCase()] ? [{ label: "Remove logo", onClick: () => saveIspLogo(isp, "") }] : []), { label: "Fetch logo", cls: "primary", onClick: () => saveIspLogo(isp, $("#isp-site").value) }] });
}
async function saveIspLogo(isp, site) {
  try { const r = await api("/api/isp-logo", { name: isp, site }); Object.keys(ISP_LOGOS).forEach(k => delete ISP_LOGOS[k]); Object.assign(ISP_LOGOS, r.logos || {}); closeModal(); render(); toast(site ? "Logo saved" : "Logo removed", "ok"); }
  catch (err) { const m = $("#isp-msg"); if (m) { m.className = "msg err"; m.textContent = err.message; } }
}
async function loadIspLogos() { try { const r = await api("/api/isp-logos"); Object.assign(ISP_LOGOS, r.logos || {}); if (S.model && S.view === "overview") render(); } catch { } }
document.addEventListener("click", e => { const b = e.target.closest("[data-isplogo]"); if (b) { e.preventDefault(); editIspLogo(b.dataset.isplogo); } });

function findingCard(f) {
  return `<div class="finding ${f.sev}"><div class="t">${sevTag(f.sev)}<span>${esc(f.title)}</span><span class="area">${esc(f.area)}</span>
      ${f.actions.length ? `<span style="margin-left:auto">${actBtn(f.actions, f.actions.length > 1 ? `Apply fix (${f.actions.length})` : "Apply fix", f.title, "sm primary")}</span>` : ""}</div>
    <p>${esc(f.why)}</p><div class="fix"><b>Fix:</b> ${esc(f.fix)}</div>
    ${f.items.length ? `<div class="items">${f.items.slice(0, 20).map(i => `<span class="tag">${esc(i)}</span>`).join("")}${f.items.length > 20 ? ` <span class="muted">+${f.items.length - 20} more</span>` : ""}</div>` : ""}</div>`;
}
V.findings = m => {
  const F = S.findings, areas = [...new Set(F.map(f => f.area))];
  const shown = F.filter(f => (S.sevFilter === "all" || f.sev === S.sevFilter) && (S.areaFilter === "all" || f.area === S.areaFilter));
  const fixable = shown.filter(f => f.actions.length && f.sev !== "info" && f.area !== "Devices" && f.area !== "Firmware");
  const all = fixable.flatMap(f => f.actions);
  return `<div class="head"><div><h1>Suggestions</h1><div class="muted">${F.length} findings, most important first. “Apply fix” shows a preview before anything changes. Menu names can differ slightly between UniFi Network versions.</div></div>
    <div style="display:flex;gap:8px">${all.length > 1 ? actBtn(all, `Apply ${all.length} config fixes…`, "Apply all shown configuration fixes", "") : ""}<button class="btn" id="btn-md">Download as Markdown</button></div></div>
    <div class="filters">${["all", ...SEV_ORDER].map(s => `<button data-sev="${s}" class="${S.sevFilter === s ? "on" : ""}">${s}${s === "all" ? "" : " (" + F.filter(f => f.sev === s).length + ")"}</button>`).join("")}</div>
    <div class="filters">${["all", ...areas].map(a => `<button data-area="${esc(a)}" class="${S.areaFilter === a ? "on" : ""}">${esc(a)}</button>`).join("")}</div>
    ${shown.map(findingCard).join("") || `<div class="empty">No findings for this filter.</div>`}`;
};
V.topology = m => `<div class="head"><div><h1>Topology</h1><div class="muted">Physical layout built from device uplinks (click a device to debug it), and the logical map of networks and Wi-Fi.</div></div></div>
  <div class="card"><h2>Physical</h2>${physicalDiagram(m)}</div>
  <div class="card section"><h2>Logical — Internet → Gateway → VLANs → SSIDs</h2>${logicalDiagram(m)}</div>`;

// ---- devices + debug panel
const roleName = r => ({ gw: "Gateway", sw: "Switch", ap: "Access point", ups: "UPS", other: "Other" }[r] || r);
const statusTag = d => d.online ? `<span class="tag good">online</span>` : `<span class="tag bad">${esc(d.stateLabel)}</span>`;
V.devices = m => {
  const bad = m.devices.filter(d => !d.online);
  return `<div class="head"><div><h1>Devices</h1><div class="muted">Click any device for details, debug info and actions.</div></div></div>
  ${bad.length ? `<div class="card" style="border-color:var(--crit)"><h2>Not working (${bad.length})</h2>${bad.map(d => { const dx = diagnoseDevice(d, m); return `<div class="finding high"><div class="t"><span>${esc(d.name)}</span><span class="area">${esc(d.model || "")} · ${esc(d.mac)}</span>${statusTag(d)}<span style="margin-left:auto"><button class="btn sm primary" data-dev="${esc(d.mac)}">Debug</button></span></div><p>${esc(dx.summary)}</p></div>`; }).join("")}</div>` : ""}
  <div class="card ${bad.length ? "section" : ""}">${table("devs", [
    { k: "name", label: "Name", fmt: r => `${devIcon(r)}<a href="#" data-dev="${esc(r.mac)}">${esc(r.name)}</a>` }, { k: "role", label: "Role", fmt: r => roleName(r.role) }, { k: "model", label: "Model" },
    { k: "ip", label: "IP", fmt: r => `<span class="mono">${esc(r.ip || "–")}</span>`, sort: r => ipToInt(r.ip || "") }, { k: "mac", label: "MAC", fmt: r => `<span class="mono">${esc(r.mac)}</span>` },
    { k: "version", label: "Firmware", fmt: r => esc(r.version || "–") + (r.upgradable ? ` <span class="tag bad">→ ${esc(r.upgradeTo || "update")}</span>` : "") },
    { k: "online", label: "Status", fmt: statusTag }, { k: "uptime", label: "Uptime", num: 1, fmt: r => r.online ? fmtDur(r.uptime) : `<span class="muted">seen ${ago(r.lastSeen)}</span>` }, { k: "clients", label: "Clients", num: 1 },
    { k: "cpu", label: "CPU", num: 1, fmt: r => r.cpu === null || r.cpu === undefined ? "–" : r.cpu + "%" }, { k: "mem", label: "RAM", num: 1, fmt: r => r.mem === null || r.mem === undefined ? "–" : r.mem + "%" },
  ], m.devices, { search: true, empty: "No devices returned" })}</div>
  <div class="card section"><h2>Physical topology</h2>${physicalDiagram(m)}</div>`;
};
function devActions(d) {
  if (!d.mac) return "";
  const da = (cmd, label, desc) => actBtn({ kind: "devcmd", cmd, mac: d.mac, label: d.name, description: desc }, label, desc);
  const a = [da("set-locate", "Locate (blink)", `Blink ${d.name}'s LED`), da("unset-locate", "Stop locate", `Stop blinking ${d.name}`)];
  if (d.role !== "gw") a.push(da("restart", "Restart", `Restart ${d.name}`));
  a.push(da("force-provision", "Force provision", `Re-send configuration to ${d.name}`));
  if (d.upgradable && d.role !== "gw") a.push(da("upgrade", "Upgrade firmware", `Upgrade ${d.name} to ${d.upgradeTo || "latest"}`));
  if (d.state === 2) a.push(da("adopt", "Adopt", `Adopt ${d.name}`));
  return a.join(" ");
}
function showDevice(mac) {
  const m = S.model, d = m.devByMac[mac]; if (!d) return;
  const dx = diagnoseDevice(d, m), up = dx.uplink;
  const kv = rows => `<dl class="kv">${rows.filter(r => r[1] !== undefined && r[1] !== null && r[1] !== "").map(([k, v]) => `<dt>${esc(k)}</dt><dd>${v}</dd>`).join("")}</dl>`;
  const facts = kv([
    ["Status", statusTag(d) + (d.intState ? ` <span class="muted">integration: ${esc(d.intState)}</span>` : "") + (d.state !== null && d.state !== undefined ? ` <span class="muted">(code ${d.state})</span>` : "")],
    ["Model", esc(`${d.model || "?"}${d.modelCode && d.modelCode !== d.model ? ` (${d.modelCode})` : ""}`)], ["Type", esc(d.type || roleName(d.role))],
    ["IP", `<span class="mono">${esc(d.ip || "–")}</span>`], ["MAC", `<span class="mono">${esc(d.mac)}</span>`],
    ["Firmware", esc(d.version || "–") + (d.upgradable ? ` → ${esc(d.upgradeTo || "newer")}` : "")], ["Adopted", d.adopted === false ? "no" : "yes"],
    ["Uptime", d.online ? fmtDur(d.uptime) : "–"], ["Last seen", d.lastSeen ? `${fmtTime(d.lastSeen)} (${ago(d.lastSeen)})` : "–"],
    ["Uplink", up.parent ? `${esc(up.parent.name)} port ${up.port?.idx ?? d.uplinkPort ?? "?"}${up.port ? ` · ${up.port.up ? "link " + fmtSpeed(up.port.speed) + "bps" : "<b class='bad-t'>no link</b>"}${up.port.poeCap ? ` · PoE ${up.port.poeOn ? (up.port.poeW ?? 0) + " W" : "off"}` : ""}` : ""}` : (d.wireless ? "wireless (mesh)" : "unknown")],
    ["Inform URL", d.informUrl ? `<span class="mono">${esc(d.informUrl)}</span>` : null], ["Talks to console from", d.connectIp ? `<span class="mono">${esc(d.connectIp)}</span>` : null],
    ["Disconnect reason", d.disconnectReason ? esc(d.disconnectReason) : null], ["Temperature", d.temps ? esc(d.temps) : null],
    ["Load", d.cpu !== null && d.cpu !== undefined ? `CPU ${d.cpu}% · RAM ${d.mem ?? "–"}%` : null], ["Clients", d.clients || null], ["Data sources", esc(Object.keys(d.sources).join(", "))],
  ]);
  const ev = dx.events.slice(0, 25);
  const report = debugReport(d, dx);
  const live = S.source === "live" && m.hasLegacy;
  const body = `<div class="grid g2"><div><h3>Diagnosis</h3><ul class="dx">${dx.items.map(i => `<li>${esc(i)}</li>`).join("") || "<li>Nothing unusual.</li>"}</ul>
      ${dx.steps.length ? `<h3>Try this</h3><ol class="dx">${dx.steps.map(s => `<li>${esc(s).replace(/`([^`]+)`/g, "<code>$1</code>")}</li>`).join("")}</ol>` : ""}
      ${live ? `<h3>Actions</h3><div class="btnrow">${devActions(d)} ${dx.actions.filter(a => a.cmd === "power-cycle").map(a => actBtn(a, "Power-cycle PoE port", a.description)).join("")}</div>` : ""}</div>
    <div><h3>Facts</h3>${facts}</div></div>
    <h3 style="margin-top:14px">Related events (${dx.events.length})</h3>${ev.length ? `<div class="tbl-wrap"><table><tbody>${ev.map(e => `<tr><td class="mono" style="white-space:nowrap">${esc(fmtTime(e.t))}</td><td>${esc(e.msg || e.key)}</td><td class="muted">${esc(e.src)}</td></tr>`).join("")}</tbody></table></div>` : `<div class="empty">No events mention this device in the last 7 days.</div>`}
    <h3 style="margin-top:14px">Raw data (secrets redacted)</h3>${Object.entries(d.sources).map(([k, v]) => `<details><summary class="mono">${esc(k)}</summary><pre class="json">${esc(JSON.stringify(v, null, 2))}</pre></details>`).join("") || `<div class="empty">No raw data</div>`}`;
  openModal({ title: `${d.name} — ${d.stateLabel}`, body, wide: true, buttons: [{ label: "Copy debug report", onClick: async () => { try { await navigator.clipboard.writeText(report); toast("Debug report copied — paste it to Claude or support", "ok"); } catch { download(`debug-${d.mac.replace(/:/g, "")}.txt`, report, "text/plain"); } } }, { label: "Close" }] });
}
function debugReport(d, dx) {
  return [`UniFi device debug report — ${new Date().toISOString()}`, `Device: ${d.name} (${d.model} ${d.modelCode || ""}) ${d.mac} ip=${d.ip || "-"}`, `State: ${d.stateLabel} code=${d.state} integration=${d.intState || "-"} adopted=${d.adopted}`,
    `Firmware: ${d.version} upgradable=${d.upgradable} → ${d.upgradeTo || "-"}`, `Network app: ${S.model.info.networkVersion} console: ${S.model.info.consoleVersion || "-"}`, `Last seen: ${d.lastSeen ? new Date(toMs(d.lastSeen)).toISOString() : "-"}`,
    "", "Diagnosis:", ...dx.items.map(i => " - " + i), "", "Events:", ...dx.events.slice(0, 20).map(e => ` ${new Date(e.t).toISOString()} ${e.msg || e.key}`), "", "Raw (redacted):", JSON.stringify(d.sources, null, 1)].join("\n");
}

// ---- ports
V.ports = m => {
  const sws = m.devices.filter(d => d.ports.length);
  if (!sws.length) return `<div class="head"><h1>Switch ports</h1></div><div class="empty">No port data returned.</div>`;
  const conn = (s, p) => {
    const devs = m.devices.filter(d => d.uplinkMac === s.mac && d.uplinkPort === p.idx).map(d => `<a href="#" data-dev="${esc(d.mac)}">${esc(d.name)}</a>`);
    const cl = m.clients.filter(c => c.wired && c.swMac === s.mac && c.swPort === p.idx).map(c => esc(c.label));
    const up = p.isUplink && s.uplinkMac ? [`↑ ${esc(m.devByMac[s.uplinkMac]?.name || "uplink")}`] : [];
    return [...up, ...devs, ...cl.slice(0, 4), cl.length > 4 ? `+${cl.length - 4}` : ""].filter(Boolean).join(", ") || `<span class="muted">–</span>`;
  };
  return `<div class="head"><div><h1>Switch ports</h1><div class="muted">Link speed, PoE, errors and what's plugged into every port.</div></div></div>` + sws.map((s, si) => `
  <div class="card ${si ? "section" : ""}"><h2>${esc(s.name)} <span class="muted" style="font-weight:400">${esc(s.model || "")} · ${s.portsUp}/${s.ports.length} up${s.poeMax ? ` · PoE ${s.poeW.toFixed(1)}/${s.poeMax} W` : s.poeW ? ` · PoE ${s.poeW.toFixed(1)} W` : ""}</span></h2>
    <div class="ports">${s.ports.map(p => `<div class="port ${!p.up ? "down" : !p.speed ? "unk" : p.speed >= 2500 ? "mg" : p.speed >= 1000 ? "gig" : "slow"} ${p.rxErr + p.txErr > 100 ? "err" : ""}" title="Port ${p.idx}${p.name && p.name !== `Port ${p.idx}` ? " — " + esc(p.name) : ""}&#10;${p.up ? (p.speed ? fmtSpeed(p.speed) + "bps" : "link up") + (p.fdx ? "" : " half-duplex") : "no link"}${p.poeW ? `&#10;PoE ${p.poeW} W` : ""}${p.rxErr + p.txErr ? `&#10;${p.rxErr + p.txErr} errors` : ""}"><b>${p.idx}</b>${p.poeW ? "<i>⚡</i>" : ""}${p.sfp ? "<i>SFP</i>" : ""}</div>`).join("")}</div>
    <div class="legend"><span><i style="background:var(--ok)"></i>1 G</span><span><i style="background:var(--c1)"></i>2.5 G+</span><span><i style="background:var(--med)"></i>10/100</span><span><i style="background:var(--line)"></i>no link</span><span>red outline = errors · ⚡ PoE</span></div>
    <div style="margin-top:10px">${table("ports" + si, [
      { k: "idx", label: "#", num: 1 }, { k: "name", label: "Name" }, { k: "up", label: "Link", fmt: p => p.up ? (!p.speed ? `<span class="ok-t">up</span>` : `<span class="${p.speed < 1000 ? "bad-t" : "ok-t"}">${fmtSpeed(p.speed)}bps${p.fdx ? "" : " HDX"}</span>`) : `<span class="muted">down</span>` },
      { k: "conn", label: "Connected", fmt: p => conn(s, p) }, { k: "profile", label: "Profile" },
      { k: "poeW", label: "PoE", num: 1, fmt: p => p.poeCap ? (p.poeOn ? `${(p.poeW ?? 0).toFixed(1)} W` : `<span class="muted">off</span>`) : "–" },
      { k: "err", label: "Errors", num: 1, sort: p => p.rxErr + p.txErr, fmt: p => p.rxErr + p.txErr ? `<span class="bad-t">${p.rxErr + p.txErr}</span>` : "0" },
      { k: "traffic", label: "Traffic", num: 1, sort: p => (p.rx || 0) + (p.tx || 0), fmt: p => fmtBytes((p.rx || 0) + (p.tx || 0)) },
      { k: "act", label: "", fmt: p => p.poeCap && p.poeOn ? actBtn({ kind: "devcmd", cmd: "power-cycle", mac: s.mac, port_idx: p.idx, label: `${s.name} port ${p.idx}`, description: `Power-cycle PoE on ${s.name} port ${p.idx}` }, "Power-cycle", `Power-cycle ${s.name} port ${p.idx}`) : "" },
    ], s.ports)}</div></div>`).join("");
};

// ---- networks
V.networks = m => {
  m.networks.forEach(n => { if (n.dhcp && n.dhcpStart && n.dhcpStop && !n.poolSize) n.poolSize = (ipToInt(n.dhcpStop) - ipToInt(n.dhcpStart) + 1) || null; });
  const pool = m.networks.filter(n => n.poolSize).map(n => ({ label: n.name, value: Math.round(100 * n.clients / n.poolSize), color: n.clients / n.poolSize > .8 ? "var(--high)" : "var(--c2)" }));
  return `<div class="head"><div><h1>Networks &amp; VLANs</h1></div></div>
  <div class="card">${table("nets", [
    { k: "name", label: "Network" }, { k: "purpose", label: "Purpose" }, { k: "vlan", label: "VLAN", num: 1 }, { k: "subnet", label: "Subnet", fmt: r => `<span class="mono">${esc(r.subnet || "–")}</span>` },
    { k: "dhcp", label: "DHCP range", fmt: r => r.dhcp ? `<span class="mono">${esc(r.dhcpStart)} – ${esc(r.dhcpStop)}</span>` : `<span class="muted">off</span>` },
    { k: "dns", label: "DNS", fmt: r => r.dns?.length ? `<span class="mono">${r.dns.map(esc).join(", ")}</span>` : `<span class="muted">gateway</span>` },
    { k: "clients", label: "Clients", num: 1 }, { k: "isolated", label: "Isolated", fmt: r => yes(r.isolated || r.guest) + (r.guest || r.vpn || r.isDefault ? "" : " " + actBtn({ kind: "rest", resource: "networkconf", id: r.id, fields: { network_isolation_enabled: !r.isolated }, description: `${r.isolated ? "Stop isolating" : "Isolate"} “${r.name}”` }, r.isolated ? "Un-isolate" : "Isolate")) },
    { k: "internet", label: "Internet", fmt: r => yes(r.internet) }, { k: "mdns", label: "mDNS", fmt: r => yes(r.mdns ?? null) },
  ], m.networks, { empty: "No networks returned" })}</div>
  <div class="grid g2 section"><div class="card"><h2>DHCP pool usage (active clients ÷ pool size)</h2>${hbar(pool, { unit: "%", max: 100 })}</div>
  <div class="card"><h2>WAN</h2>${table("wans", [{ k: "name", label: "Name" }, { k: "type", label: "Type" }, { k: "group", label: "Port" }, { k: "lb", label: "Load balance" }], m.wans, { empty: "No WAN data" })}
   ${m.routes.length ? `<h3 style="margin-top:14px">Static routes</h3>${table("routes", [{ k: "name", label: "Name" }, { k: "static-route_network", label: "Destination" }, { k: "static-route_nexthop", label: "Next hop" }, { k: "enabled", label: "On", fmt: r => yes(r.enabled) }], m.routes)}` : ""}</div></div>
  <div class="card section"><h2>Fixed IPs / reservations</h2>${table("fixed", [{ k: "label", label: "Device" }, { k: "fixedIp", label: "Fixed IP", fmt: r => `<span class="mono">${esc(r.fixedIp)}</span>`, sort: r => ipToInt(r.fixedIp) }, { k: "network", label: "Network" }, { k: "mac", label: "MAC", fmt: r => `<span class="mono">${esc(r.mac)}</span>` }, { k: "online", label: "Online", fmt: r => yes(r.online) }], m.known.filter(k => k.fixedIp), { empty: "No fixed IPs set" })}</div>
  <div class="card section"><h2>Logical map</h2>${logicalDiagram(m)}</div>`;
};

// ---- wifi
const WIDTHS = { "2.4": [20, 40], "5": [20, 40, 80, 160], "6": [20, 40, 80, 160, 320] };
const CHANS = { "2.4": [1, 6, 11, 2, 3, 4, 5, 7, 8, 9, 10, 12, 13], "5": [36, 40, 44, 48, 52, 56, 60, 64, 100, 104, 108, 112, 116, 120, 124, 128, 132, 136, 140, 144, 149, 153, 157, 161, 165], "6": [1, 5, 21, 37, 53, 69, 85, 101, 117, 133, 149, 165, 181, 197, 213, 229] };
function editRadio(deviceId, radioName) {
  const m = S.model, d = m.devices.find(x => x.id === deviceId), r = d?.radios.find(x => x.name === radioName); if (!r) return;
  const sel = (id, opts, cur) => `<select id="${id}">${opts.map(o => `<option value="${esc(o[0])}" ${String(o[0]) === String(cur) ? "selected" : ""}>${esc(o[1])}</option>`).join("")}</select>`;
  const used = m.neighbors.filter(n => n.band === r.band).reduce((a, n) => { a[n.channel] = (a[n.channel] || 0) + 1; return a; }, {});
  openModal({ title: `${d.name} — ${r.band} GHz radio`, body: `<label>Channel ${sel("re-ch", [["auto", "Auto"], ...(CHANS[r.band] || []).map(c => [c, `${c}${used[c] ? ` (${used[c]} neighbours)` : ""}`])], r.channel)}</label>
    <label>Channel width ${sel("re-w", (WIDTHS[r.band] || [20]).map(w => [w, `${w} MHz`]), r.width)}</label>
    <label>Transmit power ${sel("re-p", [["auto", "Auto"], ["high", "High"], ["medium", "Medium"], ["low", "Low"]], r.txMode)}</label><p class="fine">The AP's radio restarts briefly (clients reconnect within seconds).</p>`,
    buttons: [{ label: "Cancel" }, { label: "Preview change", cls: "primary", onClick: () => {
      const f = {}, ch = $("#re-ch").value, w = +$("#re-w").value, p = $("#re-p").value;
      if (String(ch) !== String(r.channel)) f.channel = ch === "auto" ? "auto" : +ch;
      if (w !== r.width) f.ht = w; if (p !== r.txMode) f.tx_power_mode = p;
      closeModal(); if (!Object.keys(f).length) return;
      runActions([{ kind: "radio", device_id: d.id, radio: r.name, fields: f, description: `${d.name}: ${r.band} GHz radio settings` }], `${d.name} — ${r.band} GHz`);
    } }] });
}
document.addEventListener("click", e => { const b = e.target.closest("[data-radio]"); if (!b) return; const [id, name] = b.dataset.radio.split("|"); editRadio(id, name); });
V.wifi = m => {
  const aps = m.devices.filter(d => d.radios.length);
  const radios = aps.flatMap(a => a.radios.map(r => ({ ...r, ap: a.name, deviceId: a.id, online: a.online })));
  const util = radios.filter(r => r.util !== null).map(r => ({ label: `${r.ap} ${r.band}G`, value: r.util, color: r.util >= 50 ? "var(--high)" : r.band === "2.4" ? "var(--c3)" : r.band === "5" ? "var(--c1)" : "var(--c4)" }));
  const canEdit = S.source === "live" && m.hasLegacy;
  return `<div class="head"><div><h1>Wi-Fi &amp; Radios</h1></div></div>
  <div class="card"><h2>SSIDs</h2>${table("wlans", [
    { k: "name", label: "SSID" }, { k: "enabled", label: "On", fmt: r => yes(r.enabled) + " " + (r.legacy ? actBtn({ kind: "rest", resource: "wlanconf", id: r.id, fields: { enabled: !r.enabled }, description: `${r.enabled ? "Turn off" : "Turn on"} “${r.name}”` }, r.enabled ? "Turn off" : "Turn on") : "") },
    { k: "network", label: "Network" },
    { k: "sec", label: "Security", fmt: r => `<span class="tag ${/Open|WEP|WPA1|WPA\/WPA2/.test(r.sec) ? "bad" : /WPA3/.test(r.sec) ? "good" : ""}">${esc(r.sec)}</span>` },
    { k: "pskLen", label: "Key length", num: 1, fmt: r => r.pskLen === null || r.pskLen === undefined ? "–" : `<span class="${r.pskLen < 12 ? "bad-t" : "ok-t"}">${r.pskLen}</span>` },
    { k: "pmf", label: "PMF", fmt: r => esc(r.pmf || "–") }, { k: "bands", label: "Bands", fmt: r => r.bands.map(b => `<span class="tag">${bandOf(b)}</span>`).join("") },
    { k: "hidden", label: "Hidden", fmt: r => yes(r.hidden, false) }, { k: "fastRoaming", label: "802.11r", fmt: r => yes(r.fastRoaming) },
    { k: "l2iso", label: "Client isolation", fmt: r => yes(r.l2iso) }, { k: "clients", label: "Clients", num: 1 },
  ], m.wlans, { empty: "No SSIDs returned" })}</div>
  <div class="card section"><h2>Radios</h2>${table("radios", [
    { k: "ap", label: "AP" }, { k: "band", label: "Band", fmt: r => bandLabel(r.band) }, { k: "channel", label: "Channel", num: 1 }, { k: "width", label: "Width (MHz)", num: 1 },
    { k: "txMode", label: "Tx power", fmt: r => esc(r.txMode || "–") + (r.txPower ? ` <span class="muted">(${r.txPower} dBm)</span>` : "") },
    { k: "util", label: "Utilisation", num: 1, fmt: r => r.util === null ? "–" : `<span class="${r.util >= 50 ? "bad-t" : ""}">${r.util}%</span>` }, { k: "clients", label: "Clients", num: 1 },
    { k: "e", label: "", fmt: r => canEdit && r.online && r.name ? `<button class="btn sm" data-radio="${esc(r.deviceId)}|${esc(r.name)}">Edit</button>` : "" },
  ], radios, { empty: "No radio data" })}</div>
  <div class="grid g2 section"><div class="card"><h2>Channel utilisation</h2>${hbar(util, { unit: "%", max: 100 })}</div>
    <div class="card"><h2>Your channel plan</h2>${["2.4", "5", "6"].map(b => { const by = {}; radios.filter(r => r.band === b && r.channel).forEach(r => (by[r.channel] ||= []).push(r.ap)); const ks = Object.keys(by).map(Number).sort((a, c) => a - c);
      return `<h3 style="margin-top:8px">${b} GHz</h3>${ks.length ? ks.map(c => `<div style="display:flex;gap:8px;align-items:center;margin:4px 0"><span class="pill" style="min-width:58px;text-align:center">ch ${c}</span>${by[c].map(a => `<span class="tag ${by[c].length > 1 ? "warn" : ""}">${esc(a)}</span>`).join("")}</div>`).join("") : `<span class="muted">–</span>`}`; }).join("")}
      <p class="fine">Amber = more than one of your APs on the same channel. See <a href="#" data-goto="neighbors">Neighbors</a> for the neighbourhood scan and a suggested plan.</p></div></div>`;
};

// ---- neighbors
V.neighbors = m => {
  const N = m.neighbors, own = m.devices.flatMap(d => d.radios.filter(r => r.channel && d.online).map(r => ({ ...r, ap: d.name })));
  const chartFor = (band, chans) => columns(chans.map(c => ({ label: String(c), value: N.filter(n => n.band === band && n.channel === c).length, color: own.some(r => r.band === band && r.channel === c) ? "var(--c3)" : "var(--c7)" })));
  const { cands, plan } = channelPlan(m);
  const changes = plan.filter(p => p.change);
  const ch5 = [...new Set([36, 40, 44, 48, 52, 56, 60, 64, 100, 104, 108, 112, 116, 120, 124, 128, 132, 136, 140, 149, 153, 157, 161, 165, ...N.filter(n => n.band === "5").map(n => n.channel)])].filter(Boolean).sort((a, b) => a - b);
  return `<div class="head"><div><h1>Neighbour Wi-Fi</h1><div class="muted">${N.length} networks your APs heard in the last 24 h. Orange bars = channels your own APs use.</div></div></div>
  <div class="grid g2"><div class="card"><h2>2.4 GHz — networks per channel</h2>${chartFor("2.4", [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13])}</div>
    <div class="card"><h2>5 GHz — networks per channel</h2>${chartFor("5", ch5)}</div></div>
  <div class="card section"><h2>Suggested 2.4 GHz plan</h2>
    <p class="muted">Interference per non-overlapping channel (weighted by neighbour signal): ${cands.map(c => `<span class="tag">ch ${c.ch}: ${c.noise < 1 ? "quiet" : c.noise.toFixed(0)}</span>`).join(" ")}</p>
    ${plan.length ? `<table class="pv-t"><tr><th>AP</th><th>Now</th><th>Suggested</th></tr>${plan.map(p => `<tr><td>${esc(p.ap)}</td><td>${p.from ?? "auto"}</td><td><b>${p.to}</b>${p.change ? "" : " ✓"}</td></tr>`).join("")}</table>
      <div style="margin-top:10px">${changes.length ? actBtn(changes.map(p => radioAction(p, { channel: p.to }, `${p.ap}: 2.4 GHz channel ${p.from} → ${p.to}`)), `Apply plan (${plural(changes.length, "AP")})`, "Apply suggested 2.4 GHz channel plan", "primary") : `<span class="ok-t">Your 2.4 GHz channels already match the plan.</span>`}</div>` : `<div class="empty">No online 2.4 GHz radios</div>`}
    <p class="fine">For 5 GHz, prefer 80 MHz on 36 or 149 (non-DFS) for APs far apart; avoid channels where strong neighbours sit.</p></div>
  <div class="card section"><h2>All neighbours</h2>${table("nbr", [
    { k: "ssid", label: "SSID" }, { k: "band", label: "Band", fmt: r => bandLabel(r.band) }, { k: "channel", label: "Ch", num: 1 },
    { k: "signal", label: "Signal", num: 1, fmt: r => r.signal === null ? "–" : `<span class="${r.signal > -65 ? "bad-t" : ""}">${r.signal} dBm</span>` }, { k: "heardBy", label: "Heard by" },
    { k: "security", label: "Security" }, { k: "bssid", label: "BSSID", fmt: r => `<span class="mono">${esc(r.bssid)}</span>` }, { k: "oui", label: "Vendor" },
  ], N, { search: true, empty: "No neighbour scan returned (APs scan periodically; try again later)" })}</div>`;
};

// ---- security
V.security = m => {
  const s = m.settings;
  const tile = (l, v, good, action) => `<div class="card kpi"><div class="l">${l}</div><div class="v" style="font-size:18px">${v === null || v === undefined ? `<span class="muted">unknown</span>` : `<span class="tag ${good ? "good" : "bad"}" style="font-size:14px">${esc(v)}</span>`}</div>${action && s.has(action.key) ? `<div style="margin-top:8px">${actBtn(action, action.label, action.description)}</div>` : ""}</div>`;
  const pfRisk = p => { const r = [...portsIn(p.dstPort), ...portsIn(p.fwdPort)].filter(x => RISKY_PORTS[x]); return !p.enabled ? `<span class="tag">disabled</span>` : r.length ? `<span class="tag bad">high</span>` : !p.srcLimited ? `<span class="tag warn">any source</span>` : `<span class="tag good">restricted</span>`; };
  const custom = m.policies.filter(p => !p.predefined);
  const A = (key, fields, label, description) => ({ kind: "setting", key, fields, label, description });
  return `<div class="head"><div><h1>Security &amp; Exposure</h1></div></div>
  <div class="grid g4">
    ${tile("Intrusion prevention", s.ips, /ips/.test(s.ips || ""), s.ips !== "ips" && s.ips !== "ipsInline" ? A("ips", { ips_mode: "ips" }, "Set to block", "IPS → Notify and Block") : null)}
    ${tile("UPnP", s.upnp === null ? null : s.upnp ? "enabled" : "disabled", !s.upnp, A("usg", { upnp_enabled: !s.upnp }, s.upnp ? "Turn off" : "Turn on", `UPnP ${s.upnp ? "off" : "on"}`))}
    ${tile("Device SSH", s.ssh === null ? null : s.ssh ? (s.sshPw !== false ? "on (password)" : "on (keys)") : "off", !s.ssh || s.sshPw === false, A("mgmt", { x_ssh_enabled: !s.ssh }, s.ssh ? "Turn off" : "Turn on", `Device SSH ${s.ssh ? "off" : "on"}`))}
    ${tile("Encrypted DNS", s.doh, s.doh && s.doh !== "off", s.doh === "off" ? A("doh", { state: "auto" }, "Set to auto", "Encrypted DNS → auto") : null)}
    ${tile("Port forwards (active)", m.portForwards.filter(p => p.enabled).length, !m.portForwards.some(p => p.enabled))}
    ${tile("Region blocking", s.geoip === null ? null : s.geoip ? "on" : "off", !!s.geoip)}
    ${tile("Auto device updates", s.autoUpgrade === null ? null : s.autoUpgrade ? "on" : "off", !!s.autoUpgrade, A("mgmt", { auto_upgrade: !s.autoUpgrade }, s.autoUpgrade ? "Turn off" : "Turn on", `Auto updates ${s.autoUpgrade ? "off" : "on"}`))}
    ${tile("Threats (30 days)", m.ips.length, m.ips.length === 0)}
  </div>
  <div class="card section"><h2>Port forwards</h2>${table("pf", [
    { k: "name", label: "Name" }, { k: "risk", label: "Risk", fmt: pfRisk }, { k: "proto", label: "Proto" }, { k: "dstPort", label: "WAN port", fmt: r => `<span class="mono">${esc(r.dstPort)}</span>` },
    { k: "fwd", label: "Forward to", fmt: r => `<span class="mono">${esc(r.fwd)}:${esc(r.fwdPort)}</span>` }, { k: "src", label: "Source" }, { k: "log", label: "Log", fmt: r => yes(r.log) },
    { k: "a", label: "", fmt: r => actBtn({ kind: "rest", resource: "portforward", id: r.id, fields: { enabled: !r.enabled }, description: `${r.enabled ? "Disable" : "Enable"} port forward “${r.name}”` }, r.enabled ? "Disable" : "Enable") },
  ], m.portForwards, { empty: "No port forwards — good." })}</div>
  <div class="card section"><h2>Network isolation</h2>${table("iso", [
    { k: "name", label: "Network" }, { k: "vlan", label: "VLAN", num: 1 }, { k: "purpose", label: "Purpose" },
    { k: "isolated", label: "Isolated from other VLANs", fmt: r => yes(r.isolated || r.guest) }, { k: "internet", label: "Internet access", fmt: r => yes(r.internet) },
    { k: "rules", label: "Custom rules", num: 1, sort: r => m.rules.filter(x => x.srcNet === r.id || x.dstNet === r.id).length, fmt: r => m.rules.filter(x => x.srcNet === r.id || x.dstNet === r.id).length },
  ], m.networks)}</div>
  ${m.policies.length ? `<div class="card section"><h2>Firewall policies (zone-based) — ${custom.length} custom, ${m.policies.length - custom.length} built-in</h2>${table("pol", [
    { k: "index", label: "#", num: 1 }, { k: "name", label: "Name" }, { k: "action", label: "Action", fmt: r => `<span class="tag ${r.action === "ALLOW" ? "good" : "bad"}">${esc(r.action)}</span>` },
    { k: "srcZone", label: "From zone" }, { k: "dstZone", label: "To zone" }, { k: "dstPort", label: "Port" }, { k: "predefined", label: "Built-in", fmt: r => r.predefined ? "yes" : "" }, { k: "enabled", label: "On", fmt: r => yes(r.enabled) },
  ], m.policies, { search: true })}</div>` : ""}
  ${m.rules.length ? `<div class="card section"><h2>Firewall rules (classic)</h2>${table("rules", [
    { k: "ruleset", label: "Ruleset" }, { k: "index", label: "#", num: 1 }, { k: "name", label: "Name" }, { k: "action", label: "Action", fmt: r => `<span class="tag ${r.action === "accept" ? "good" : "bad"}">${esc(r.action)}</span>` },
    { k: "src", label: "Source" }, { k: "dst", label: "Destination" }, { k: "dstPort", label: "Port" }, { k: "proto", label: "Proto" }, { k: "enabled", label: "On", fmt: r => yes(r.enabled) },
  ], m.rules, { search: true })}</div>` : ""}`;
};

// ---- clients
function showClient(mac) {
  const m = S.model, c = m.clients.find(x => x.mac === mac) || m.knownByMac[mac]; if (!c) return;
  const k = m.knownByMac[mac] || {}, userId = c.userId || k.id;
  const dpi = (m.clientDpi[mac] || []).sort((a, b) => (b.rx + b.tx) - (a.rx + a.tx)).slice(0, 10);
  const ap = m.devByMac[c.apMac]?.name, sw = m.devByMac[c.swMac]?.name;
  const live = S.source === "live" && m.hasLegacy;
  const kv = rows => `<dl class="kv">${rows.filter(r => r[1]).map(([a, b]) => `<dt>${esc(a)}</dt><dd>${b}</dd>`).join("")}</dl>`;
  const body = `<div class="grid g2"><div>${kv([
    ["Status", c.online === false ? `<span class="tag">offline · seen ${ago(c.lastSeen)}</span>` : `<span class="tag good">online</span>`], ["Blocked", c.blocked ? `<span class="tag bad">blocked</span>` : null],
    ["IP", `<span class="mono">${esc(c.ip || "–")}</span>${c.fixedIp ? ` <span class="tag">fixed</span>` : ""}`], ["MAC", `<span class="mono">${esc(c.mac)}</span>`], ["Vendor", esc(c.vendor || "")], ["Hostname", esc(c.hostname || "")],
    ["Network", esc(c.network || "")], ["Connection", c.wired ? `wired${sw ? ` · ${esc(sw)} port ${c.swPort ?? "?"}` : ""}` : c.ssid ? `${esc(c.ssid)} · ${bandLabel(c.band)} · ch ${c.channel ?? "?"}${ap ? ` · ${esc(ap)}` : ""}` : ""],
    ["Signal", c.signal !== null && c.signal !== undefined ? `${c.signal} dBm` : null], ["Link rate", c.txRate ? `↓ ${Math.round(c.rxRate / 1000)} / ↑ ${Math.round(c.txRate / 1000)} Mbps` : null],
    ["Experience", c.satisfaction !== null && c.satisfaction !== undefined ? c.satisfaction + "%" : null], ["Traffic", c.tx !== undefined && c.tx !== null ? `↓ ${fmtBytes(c.rx)} · ↑ ${fmtBytes(c.tx)}` : null],
    ["Connected", c.uptime ? fmtDur(c.uptime) : null], ["First seen", c.firstSeen ? fmtTime(c.firstSeen) : null], ["Note", esc(k.note || "")],
  ])}</div><div><h3>App categories (since tracking began)</h3>${dpi.length ? hbar(dpi.map(d => ({ label: d.cat, value: d.rx + d.tx, color: "var(--c2)" })), { fmt: fmtBytes }) : `<div class="empty">No traffic-identification data${m.settings.dpi === false ? " (DPI is off)" : ""}</div>`}</div></div>
    ${live ? `<h3 style="margin-top:12px">Actions</h3><div class="btnrow">
      ${c.online !== false && !c.wired ? actBtn({ kind: "stacmd", cmd: "kick-sta", mac, label: c.label, description: `Reconnect ${c.label} (kick from Wi-Fi)` }, "Reconnect") : ""}
      ${actBtn({ kind: "stacmd", cmd: c.blocked ? "unblock-sta" : "block-sta", mac, label: c.label, description: `${c.blocked ? "Unblock" : "Block"} ${c.label}` }, c.blocked ? "Unblock" : "Block")}
      ${userId ? `<button class="btn sm" id="cl-rename">Rename</button><button class="btn sm" id="cl-fixed">${c.fixedIp ? "Change / clear fixed IP" : "Set fixed IP"}</button>` : `<span class="fine">Rename / fixed IP need the client's record (not returned for this device).</span>`}</div>` : ""}`;
  openModal({ title: c.label, body, wide: true });
  $("#cl-rename")?.addEventListener("click", () => openModal({ title: `Rename ${c.label}`, body: `<label>Name<input id="cl-name" value="${esc(c.name || "")}"></label>`, buttons: [{ label: "Cancel" }, { label: "Preview", cls: "primary", onClick: () => { const v = $("#cl-name").value.trim(); closeModal(); runActions([{ kind: "rest", resource: "user", id: userId, fields: { name: v }, description: `Rename ${c.label} → ${v}` }], "Rename client"); } }] }));
  $("#cl-fixed")?.addEventListener("click", () => {
    const n = m.netById[c.networkId] || m.netForIp(c.ip || "") || m.defaultNet;
    openModal({ title: `Fixed IP for ${c.label}`, body: `<label>Network<select id="cl-net">${m.networks.filter(x => !x.vpn).map(x => `<option value="${esc(x.id)}" ${x === n ? "selected" : ""}>${esc(x.name)} (${esc(x.subnet || "")})</option>`).join("")}</select></label>
      <label>IP address<input id="cl-ip" value="${esc(c.fixedIp || c.ip || "")}"></label><p class="fine">Tip: pick an address outside the DHCP range${n?.dhcpStart ? ` (${esc(n.dhcpStart)}–${esc(n.dhcpStop)})` : ""}. The device picks it up at its next DHCP renewal or reconnect.</p>`,
      buttons: [{ label: "Cancel" }, ...(c.fixedIp ? [{ label: "Clear fixed IP", onClick: () => { closeModal(); runActions([{ kind: "rest", resource: "user", id: userId, fields: { use_fixedip: false }, description: `Clear fixed IP of ${c.label}` }], "Clear fixed IP"); } }] : []),
        { label: "Preview", cls: "primary", onClick: () => { const ip = $("#cl-ip").value.trim(), nid = $("#cl-net").value; const net = m.netById[nid]; if (!ipToInt(ip) || (net?.subnet && !inSubnet(ip, net.subnet))) return toast(`${ip} isn't a valid address in ${net?.subnet || "that network"}`, "err"); closeModal(); runActions([{ kind: "rest", resource: "user", id: userId, fields: { use_fixedip: true, fixed_ip: ip, network_id: nid }, description: `Fixed IP ${ip} for ${c.label}` }], "Set fixed IP"); } }] });
  });
}
document.addEventListener("click", e => { const r = e.target.closest("[data-client]"); if (!r || e.target.closest("a[data-act],button[data-act]")) return; e.preventDefault(); showClient(r.dataset.client); });
document.addEventListener("click", e => { const t = e.target.closest("[data-ctab]"); if (!t) return; S.clientTab = t.dataset.ctab; render(); });
V.clients = m => {
  const wl = m.clients.filter(c => !c.wired);
  const bins = []; for (let b = -90; b < -30; b += 5) bins.push({ label: String(b), value: wl.filter(c => c.signal !== null && c.signal >= b && c.signal < b + 5).length, color: b < -75 ? "var(--high)" : b < -67 ? "var(--med)" : "var(--ok)" });
  const top = [...m.clients].filter(c => c.tx !== null || c.rx !== null).sort((a, b) => ((b.tx || 0) + (b.rx || 0)) - ((a.tx || 0) + (a.rx || 0))).slice(0, 10);
  const byAp = {}; wl.forEach(c => { const k = m.devByMac[c.apMac]?.name || "Unknown AP"; byAp[k] = (byAp[k] || 0) + 1; });
  const tabs = { active: ["Active", m.clients.length], known: ["All known (incl. offline)", m.known.length], blocked: ["Blocked", m.known.filter(k => k.blocked).length] };
  const tab = S.clientTab;
  const tbl = tab === "active" ? table("clients", [
    { k: "label", label: "Name", fmt: r => `${cliIcon(r)}${esc(r.label)}` }, { k: "ip", label: "IP", fmt: r => `<span class="mono">${esc(r.ip || "–")}</span>${r.fixedIp ? " 📌" : ""}`, sort: r => ipToInt(r.ip || "") }, { k: "mac", label: "MAC", fmt: r => `<span class="mono">${esc(r.mac)}</span>` },
    { k: "network", label: "Network" }, { k: "wired", label: "Link", fmt: r => r.wired ? `wired${r.swPort ? " · port " + r.swPort : ""}` : `${esc(r.ssid || "wifi")} · ${bandLabel(r.band)}` },
    { k: "signal", label: "Signal", num: 1, fmt: r => r.signal === null || r.signal === undefined ? "–" : `<span class="${r.signal < -75 ? "bad-t" : ""}">${r.signal}</span>` },
    { k: "satisfaction", label: "Experience", num: 1, fmt: r => r.satisfaction === null || r.satisfaction === undefined ? "–" : r.satisfaction + "%" },
    { k: "traffic", label: "Traffic", num: 1, sort: r => (r.tx || 0) + (r.rx || 0), fmt: r => fmtBytes((r.tx || 0) + (r.rx || 0)) }, { k: "uptime", label: "Connected", num: 1, fmt: r => fmtDur(r.uptime) },
    { k: "x", label: "", fmt: () => `<span class="muted">⋯</span>` },
  ], m.clients, { search: true, empty: "No clients returned", rowAttr: r => `data-client="${esc(r.mac)}" class="clickable"` })
    : table("known-" + tab, [
      { k: "label", label: "Name", fmt: r => `${cliIcon(r)}${esc(r.label)}` }, { k: "online", label: "Status", fmt: r => r.online ? `<span class="tag good">online</span>` : `<span class="muted">seen ${ago(r.lastSeen)}</span>`, sort: r => r.online ? Infinity : (r.lastSeen || 0) },
      { k: "ip", label: "IP", fmt: r => `<span class="mono">${esc(r.fixedIp || r.ip || "–")}</span>${r.fixedIp ? " 📌" : ""}` }, { k: "mac", label: "MAC", fmt: r => `<span class="mono">${esc(r.mac)}</span>` },
      { k: "vendor", label: "Vendor" }, { k: "network", label: "Network" }, { k: "blocked", label: "Blocked", fmt: r => r.blocked ? `<span class="tag bad">blocked</span>` : "" },
      { k: "firstSeen", label: "First seen", num: 1, fmt: r => fmtTime(r.firstSeen) }, { k: "x", label: "", fmt: () => `<span class="muted">⋯</span>` },
    ], tab === "blocked" ? m.known.filter(k => k.blocked) : m.known, { search: true, empty: tab === "blocked" ? "No blocked clients" : "No known-client list returned", rowAttr: r => `data-client="${esc(r.mac)}" class="clickable"` });
  return `<div class="head"><div><h1>Clients</h1><div class="muted">${m.clients.length} active · ${m.known.length} known. Click a client for details, app usage and actions.</div></div></div>
  <div class="grid g2"><div class="card"><h2>Wireless signal strength (dBm)</h2>${columns(bins)}<p class="fine">Green ≥ −67 (good for calls/video) · amber −67…−75 · red &lt; −75 (weak)</p></div>
    <div class="card"><h2>Top 10 by traffic (since connected)</h2>${hbar(top.map(c => ({ label: c.label, value: (c.tx || 0) + (c.rx || 0), color: "var(--c2)" })), { fmt: fmtBytes })}</div>
    <div class="card"><h2>Wireless clients per AP</h2>${hbar(Object.entries(byAp).sort((a, b) => b[1] - a[1]).map(([label, value]) => ({ label, value, color: "var(--c4)" })))}</div>
    <div class="card"><h2>Whole-network app categories</h2>${hbar([...m.dpiCats].sort((a, b) => (b.rx + b.tx) - (a.rx + a.tx)).slice(0, 8).map(d => ({ label: d.cat, value: d.rx + d.tx, color: "var(--c1)" })), { fmt: fmtBytes })}</div></div>
  <div class="card section"><div class="filters">${Object.entries(tabs).map(([k, [l, n]]) => `<button data-ctab="${k}" class="${tab === k ? "on" : ""}">${l} (${n})</button>`).join("")}</div>${tbl}</div>`;
};

// ---- history & events
V.history = m => {
  const H = m.history, wk = H.hourly;
  const top = (list, key) => Object.entries(list.reduce((a, x) => { const k = x[key] || "?"; a[k] = (a[k] || 0) + 1; return a; }, {})).sort((a, b) => b[1] - a[1]).slice(0, 8).map(([label, value]) => ({ label, value, color: "var(--crit)" }));
  return `<div class="head"><div><h1>History &amp; Events</h1><div class="muted">Usage over time, console events, and threats seen by IPS.</div></div></div>
  <div class="grid g2">
    <div class="card"><h2>Internet traffic per hour — 7 days</h2>${lineChart([{ name: "Download", color: "var(--c1)", fill: true, points: wk.map(r => ({ t: r.t, v: r.wanRx })) }, { name: "Upload", color: "var(--c3)", points: wk.map(r => ({ t: r.t, v: r.wanTx })) }], { fmt: fmtBytes })}</div>
    <div class="card"><h2>Connected clients — 7 days</h2>${lineChart([{ name: "Clients", color: "var(--c2)", fill: true, points: wk.map(r => ({ t: r.t, v: r.sta })) }], { fmt: v => Math.round(v) })}</div>
    <div class="card"><h2>Gateway load — 7 days</h2>${lineChart([{ name: "CPU %", color: "var(--c3)", points: H.gw.map(r => ({ t: r.t, v: r.cpu })) }, { name: "RAM %", color: "var(--c4)", points: H.gw.map(r => ({ t: r.t, v: r.mem })) }], { fmt: v => Math.round(v) + "%" })}</div>
    <div class="card"><h2>Daily internet usage — 30 days</h2>${columns(H.daily.map(r => ({ label: new Date(r.t).getDate() + "", value: (r.wanRx || 0) + (r.wanTx || 0), color: "var(--c1)" })), { fmt: fmtBytes })}</div>
  </div>
  <div class="card section"><h2>Threats (IPS) — last 30 days: ${m.ips.length}</h2>
    ${m.ips.length ? `<div class="grid g2"><div><h3>Top signatures</h3>${hbar(top(m.ips, "sig"))}</div><div><h3>Top source countries</h3>${hbar(top(m.ips, "country"))}</div></div>
    ${table("ips", [{ k: "t", label: "Time", fmt: r => fmtTime(r.t), sort: r => r.t }, { k: "sig", label: "Signature" }, { k: "cat", label: "Category" }, { k: "action", label: "Action", fmt: r => `<span class="tag ${/block|drop/i.test(r.action || "") ? "good" : "warn"}">${esc(r.action || "?")}</span>` }, { k: "src", label: "From", fmt: r => `<span class="mono">${esc(r.src || "")}</span> ${esc(r.country || "")}` }, { k: "dst", label: "To", fmt: r => `<span class="mono">${esc(r.dst || "")}</span>` }], m.ips, { search: true })}`
    : `<div class="empty">No IPS events returned${m.settings.ips === "disabled" ? " (IPS is off)" : ""}.</div>`}</div>
  ${m.alarms.length ? `<div class="card section"><h2>Open alarms (${m.alarms.length})</h2>${table("alarms", [{ k: "t", label: "Time", fmt: r => fmtTime(r.t), sort: r => r.t }, { k: "msg", label: "Alarm" }, { k: "key", label: "Type" }], m.alarms)}</div>` : ""}
  <div class="card section"><h2>Events — last 7 days (${m.events.length})</h2>${table("events", [{ k: "t", label: "Time", fmt: r => `<span style="white-space:nowrap">${fmtTime(r.t)}</span>`, sort: r => r.t }, { k: "msg", label: "Event" }, { k: "sub", label: "Area" }, { k: "src", label: "Source" }], m.events, { search: true, empty: "No events returned" })}</div>`;
};

// ---- changes
V.changes = () => `<div class="head"><div><h1>Changes</h1><div class="muted">Everything this app changed during this server run. Also appended to <code>changes-log.jsonl</code> next to server.py.</div></div><button class="btn" id="btn-reload-changes">Refresh</button></div>
  <div class="card" id="changes-body"><div class="empty">${S.source === "live" ? "Loading…" : "Changes are only available with a live connection."}</div></div>`;
async function loadChanges() {
  if (S.source !== "live") return;
  let list = []; try { list = await api("/api/changes"); } catch { }
  const el = $("#changes-body"); if (!el) return;
  el.innerHTML = list.length ? `<div class="tbl-wrap"><table><thead><tr><th>#</th><th>Time</th><th>Change</th><th>Before → after</th><th>Result</th><th></th></tr></thead><tbody>${list.slice().reverse().map(c => `<tr>
    <td>${c.id}</td><td style="white-space:nowrap">${esc(c.time)}</td><td><b>${esc(c.description || c.target)}</b><div class="fine mono">${esc(c.method)} ${esc(c.path)}</div></td>
    <td>${Object.keys(c.after || {}).map(k => `<div class="mono fine">${esc(k)}: ${esc(fmtVal(c.before?.[k]))} → <b>${esc(fmtVal(c.after[k]))}</b></div>`).join("")}</td>
    <td>${c.ok ? `<span class="tag good">ok</span>` : `<span class="tag bad">failed</span><div class="fine bad-t">${esc(c.error || "")}</div>`}${c.undone ? ` <span class="tag">undone</span>` : ""}</td>
    <td>${c.undoable && !c.undone ? `<button class="btn sm" data-undo="${c.id}">Undo</button>` : ""}</td></tr>`).join("")}</tbody></table></div>` : `<div class="empty">No changes made yet.</div>`;
}
document.addEventListener("click", e => {
  const u = e.target.closest("[data-undo]"); if (!u) return;
  openModal({ title: `Undo change #${u.dataset.undo}?`, body: "<p>This sends the previous values back to the console.</p>", buttons: [{ label: "Cancel" }, { label: "Undo", cls: "primary", onClick: async () => {
    closeModal(); try { if (!S.write) await setWrite(true); await api("/api/undo", { id: +u.dataset.undo }); toast("Undone", "ok"); loadChanges(); setTimeout(() => collectLive(true, true).catch(() => { }), 2000); } catch (err) { toast(err.message, "err"); loadChanges(); } } }] });
});

// ---- cheatsheet
function cheatData(m) {
  return {
    console: [["Console", m.info.hostname || m.info.host], ["Address", m.info.host], ["Gateway", `${m.info.gwName || ""} (${m.info.gwModel || "?"})`], ["Gateway LAN IP", m.info.gwIp], ["Network app", m.info.networkVersion], ["WAN IP", m.info.wanIp], ["ISP", m.info.isp], ["Timezone", m.info.timezone], ["Site", m.info.site]],
    settings: [["Intrusion prevention", m.settings.ips], ["UPnP", m.settings.upnp === null ? null : m.settings.upnp ? "on" : "off"], ["Device SSH", m.settings.ssh === null ? null : m.settings.ssh ? "on" : "off"], ["Encrypted DNS", m.settings.doh], ["Auto updates", m.settings.autoUpgrade === null ? null : m.settings.autoUpgrade ? "on" : "off"], ["Country", m.settings.country]],
  };
}
const NAV_PATHS = [
  ["Change Wi-Fi password / security", "Settings → WiFi → [SSID]"], ["Create a VLAN / isolate it", "Settings → Networks → New Virtual Network → Isolate Network"],
  ["Block traffic between networks", "Settings → Policy Engine / Firewall & Security → Create Policy (zone based)"], ["Port forwards", "Settings → Policy Engine / Firewall & Security → Port Forwarding"],
  ["Intrusion prevention, region blocking", "Settings → CyberSecure (Security) → Intrusion Prevention / Region Blocking"], ["UPnP, DNS, WAN, failover", "Settings → Internet → [WAN]"],
  ["VPN server (WireGuard / Teleport)", "Settings → VPN → VPN Server"], ["Firmware updates & schedule", "Settings → Control Plane → Updates  ·  UniFi Devices → Update"],
  ["AP channels / power", "UniFi Devices → [AP] → Settings → Radios  ·  Radio Manager"], ["Fixed IP for a device", "Client Devices → [device] → Settings → Fixed IP Address"],
  ["SSH to devices", "Settings → System → Advanced → Device SSH Authentication"], ["Backups", "Settings → Control Plane → Backups"], ["API keys", "Settings → Control Plane → Integrations"],
];
V.cheatsheet = m => {
  const c = cheatData(m);
  const kv = rows => `<dl class="kv">${rows.map(([k, v]) => `<dt>${esc(k)}</dt><dd>${esc(v ?? "–")}</dd>`).join("")}</dl>`;
  const small = (cols, rows) => rows.length ? `<table><thead><tr>${cols.map(c => `<th>${esc(c[0])}</th>`).join("")}</tr></thead><tbody>${rows.map(r => `<tr>${cols.map(c => `<td>${c[1](r)}</td>`).join("")}</tr>`).join("")}</tbody></table>` : `<div class="muted">–</div>`;
  return `<div class="head no-print"><div><h1>Cheatsheet</h1><div class="muted">Print it or save as Markdown. Passwords are never included.</div></div>
    <div style="display:flex;gap:8px"><button class="btn" id="btn-print">Print / PDF</button><button class="btn" id="btn-cheat-md">Download Markdown</button></div></div>
  <div class="cheat">
    <div class="cols"><div><h2>Console</h2>${kv(c.console)}</div><div><h2>Key settings</h2>${kv(c.settings)}<h2>Health</h2>${kv([["Score", `${scoreOf(S.findings)}/100 (grade ${grade(scoreOf(S.findings))})`], ["Open issues", S.findings.filter(f => f.sev !== "info").length], ["Collected", m.info.collectedAt]])}</div></div>
    <h2>Networks</h2>${small([["Network", r => esc(r.name)], ["VLAN", r => r.vpn ? "VPN" : esc(r.vlan ?? 1)], ["Subnet", r => `<span class="mono">${esc(r.subnet || "")}</span>`], ["Gateway", r => `<span class="mono">${esc(r.gwIp || "")}</span>`], ["DHCP", r => r.dhcp ? `<span class="mono">${esc(r.dhcpStart)}–${esc(r.dhcpStop)}</span>` : "off"], ["DNS", r => esc(r.dns?.join(", ") || "gateway")], ["Isolated", r => r.isolated || r.guest ? "yes" : "no"]], m.networks)}
    <h2>Wi-Fi</h2>${small([["SSID", r => esc(r.name)], ["Network", r => esc(r.network || "")], ["Security", r => esc(r.sec)], ["Bands", r => r.bands.map(bandOf).join("/") + " GHz"], ["Notes", r => [r.guest ? "guest" : "", r.hidden ? "hidden" : "", r.enabled ? "" : "disabled"].filter(Boolean).join(", ")]], m.wlans)}
    <h2>Infrastructure</h2>${small([["Device", r => esc(r.name)], ["Model", r => esc(r.model || "")], ["IP", r => `<span class="mono">${esc(r.ip || "")}</span>`], ["MAC", r => `<span class="mono">${esc(r.mac)}</span>`], ["Uplink", r => r.uplinkMac ? esc(`${m.devByMac[r.uplinkMac]?.name || "?"} p${r.uplinkPort ?? "?"}`) : ""], ["Firmware", r => esc(r.version || "") + (r.upgradable ? " ↑" : "")]], m.devices)}
    <h2>Fixed IPs</h2>${small([["Device", r => esc(r.label)], ["IP", r => `<span class="mono">${esc(r.fixedIp)}</span>`], ["MAC", r => `<span class="mono">${esc(r.mac)}</span>`]], m.known.filter(k => k.fixedIp).sort((a, b) => ipToInt(a.fixedIp) - ipToInt(b.fixedIp)))}
    <h2>Port forwards</h2>${small([["Name", r => esc(r.name)], ["WAN port", r => esc(r.dstPort)], ["→ Target", r => `<span class="mono">${esc(r.fwd)}:${esc(r.fwdPort)}</span>`], ["Source", r => esc(r.src)], ["On", r => r.enabled ? "yes" : "no"]], m.portForwards)}
    <div class="cols"><div><h2>Where to change things</h2><table><tbody>${NAV_PATHS.map(([a, b]) => `<tr><td>${esc(a)}</td><td class="muted">${esc(b)}</td></tr>`).join("")}</tbody></table></div>
    <div><h2>Quick troubleshooting</h2><table><tbody>
      <tr><td>Can I reach the gateway?</td><td class="mono">ping ${esc(m.info.gwIp || m.networks[0]?.gwIp || "192.168.1.1")}</td></tr>
      <tr><td>Is DNS working?</td><td class="mono">nslookup ui.com ${esc(m.info.gwIp || m.networks[0]?.gwIp || "")}</td></tr>
      <tr><td>Internet vs DNS problem?</td><td class="mono">ping 1.1.1.1</td></tr>
      <tr><td>My IP / gateway (macOS)</td><td class="mono">ipconfig getifaddr en0 ; route -n get default</td></tr>
      <tr><td>Renew DHCP (macOS)</td><td class="mono">sudo ipconfig set en0 DHCP</td></tr>
      <tr><td>Wi-Fi signal (macOS)</td><td>Option-click the Wi-Fi icon → RSSI</td></tr>
      <tr><td>SSH to a device</td><td class="mono">ssh &lt;user&gt;@&lt;device-ip&gt;</td></tr>
      <tr><td>Device info (on AP/switch)</td><td class="mono">info</td></tr>
      <tr><td>Re-adopt to console</td><td class="mono">set-inform http://${esc(m.info.gwIp || "&lt;console-ip&gt;")}:8080/inform</td></tr>
      <tr><td>Factory reset AP/switch</td><td>Hold reset 10 s (loses config)</td></tr>
    </tbody></table></div></div>
    <h2>Top actions</h2><ol>${S.findings.filter(f => f.sev !== "info").slice(0, 8).map(f => `<li><b>${esc(f.title)}</b> — ${esc(f.fix)}</li>`).join("") || "<li>No open issues.</li>"}</ol>
  </div>`;
};

V.raw = () => {
  const log = arr(S.bundle?.meta?.log);
  return `<div class="head"><div><h1>Raw data</h1><div class="muted">Every endpoint the app queried. Secrets are replaced with <code>{"__redacted__":true,"length":n}</code> before reaching this page. Failed rows are normal — not every console supports every endpoint.</div></div>
    <button class="btn" id="btn-dl">Download redacted JSON</button></div>
  <div class="grid g2"><div class="card">${table("log", [
    { k: "source", label: "API" }, { k: "name", label: "Data" }, { k: "status", label: "Status", num: 1, fmt: r => `<span class="${r.ok ? "ok-t" : "bad-t"}">${r.status || "err"}</span>` },
    { k: "count", label: "Items", num: 1, fmt: r => r.count ?? (r.ok ? "obj" : "–") }, { k: "view", label: "", fmt: r => r.ok ? `<a href="#" data-raw="${esc(r.source)}|${esc(r.name)}">view</a>` : `<span class="muted" title="${esc(r.error || "")}">${esc((r.error || "").slice(0, 28))}</span>` },
  ], log, { empty: "Demo / imported data has no request log" })}</div>
  <div class="card"><h2 id="raw-title">Select an endpoint</h2><pre class="json" id="raw-json">—</pre></div></div>`;
};

// ------------------------------------------------------------------ exports
function findingsMd() {
  const m = S.model, sc = scoreOf(S.findings);
  return `# UniFi configuration review — ${m.info.hostname || m.info.host || ""}\n\nScore: **${sc}/100 (grade ${grade(sc)})** · Network ${m.info.networkVersion || "?"} · ${m.info.collectedAt || ""}\n\n` +
    S.findings.map((f, i) => `## ${i + 1}. [${f.sev.toUpperCase()}] ${f.title}\n*${f.area}*\n\n${f.why}\n\n**Fix:** ${f.fix}\n${f.items.length ? "\n" + f.items.map(x => `- ${x}`).join("\n") + "\n" : ""}`).join("\n");
}
function cheatMd() {
  const m = S.model, c = cheatData(m);
  const t = (h, rows) => rows.length ? `| ${h.join(" | ")} |\n|${h.map(() => "---").join("|")}|\n` + rows.map(r => `| ${r.map(x => String(x ?? "").replace(/\|/g, "/")).join(" | ")} |`).join("\n") + "\n" : "_none_\n";
  return `# UniFi cheatsheet — ${m.info.hostname || m.info.host || ""}\n\n## Console\n${c.console.map(([k, v]) => `- **${k}:** ${v ?? "–"}`).join("\n")}\n\n## Key settings\n${c.settings.map(([k, v]) => `- **${k}:** ${v ?? "–"}`).join("\n")}\n\n` +
    `## Networks\n${t(["Network", "VLAN", "Subnet", "Gateway", "DHCP", "Isolated"], m.networks.map(n => [n.name, n.vpn ? "VPN" : n.vlan ?? 1, n.subnet, n.gwIp, n.dhcp ? `${n.dhcpStart}–${n.dhcpStop}` : "off", n.isolated || n.guest ? "yes" : "no"]))}\n` +
    `## Wi-Fi\n${t(["SSID", "Network", "Security", "Bands"], m.wlans.map(w => [w.name, w.network, w.sec, w.bands.map(bandOf).join("/")]))}\n` +
    `## Infrastructure\n${t(["Device", "Model", "IP", "MAC", "Firmware"], m.devices.map(d => [d.name, d.model, d.ip, d.mac, (d.version || "") + (d.upgradable ? " (update)" : "")]))}\n` +
    `## Fixed IPs\n${t(["Device", "IP", "MAC"], m.known.filter(k => k.fixedIp).map(k => [k.label, k.fixedIp, k.mac]))}\n` +
    `## Port forwards\n${t(["Name", "WAN port", "Target", "Source", "On"], m.portForwards.map(p => [p.name, p.dstPort, `${p.fwd}:${p.fwdPort}`, p.src, p.enabled ? "yes" : "no"]))}\n` +
    `## Where to change things\n${NAV_PATHS.map(([a, b]) => `- **${a}:** ${b}`).join("\n")}\n`;
}
function download(name, text, type = "text/markdown") { const a = document.createElement("a"); a.href = URL.createObjectURL(new Blob([text], { type })); a.download = name; a.click(); setTimeout(() => URL.revokeObjectURL(a.href), 2000); }

// ------------------------------------------------------------------ speed test polling
async function pollSpeedtest() {
  toast("Speed test running — results in about 30–60 s");
  for (let i = 0; i < 18; i++) {
    await new Promise(r => setTimeout(r, 5000));
    try {
      const r = await api("/api/speedtest-status", {}); const d = arr(r.data)[0] || {};
      if (d.xput_download && (d.status_summary === 0 || d.status_download === 2 || i > 6)) { toast(`Speed test: ↓ ${fmtMbps(d.xput_download)} ↑ ${fmtMbps(d.xput_upload)} · ${d.latency ?? "?"} ms`, "ok"); collectLive(true, true).catch(() => { }); return; }
    } catch { return; }
  }
}

// ------------------------------------------------------------------ render / routing
function render() {
  const m = S.model;
  ACT_SEQ = 0; for (const k in ACTIONS) delete ACTIONS[k];
  $("#main").innerHTML = (S.source === "none" && S.view !== "settings" ? `<div class="banner info">HomeOwl isn't connected to your UniFi console yet. ${S.role === "admin" ? `<a href="#" data-goto="settings">Open Settings › UniFi connection</a>` : "Ask an admin to connect it in Settings."}</div>` : "") + V[S.view](m);
  $$("#nav a").forEach(a => a.classList.toggle("active", a.dataset.view === S.view));
  $("#nav-findings").textContent = S.findings.filter(f => f.sev !== "info").length || "";
  const bad = m.devices.filter(d => !d.online).length; $("#nav-devices").textContent = bad || ""; $("#nav-devices").classList.toggle("alert", !!bad);
  renderChip(); $("#nav-settings").hidden = S.role !== "admin" || (S.source !== "live" && S.source !== "none");
  renderLock();
  if (S.view === "changes") loadChanges();
}
function load(bundle, source, keepView) {
  S.bundle = bundle; S.source = source; S.model = normalise(bundle); S.findings = analyse(S.model);
  if (!keepView) window.scrollTo(0, 0);
  $("#connect").hidden = true; $("#app").hidden = false; render();
}
document.addEventListener("click", e => {
  const dev = e.target.closest("[data-dev]"); if (dev) { e.preventDefault(); showDevice(dev.dataset.dev); return; }
  const nav = e.target.closest("#nav a,[data-goto]"); if (nav) { e.preventDefault(); closeModal(); S.view = nav.dataset.view || nav.dataset.goto; render(); window.scrollTo(0, 0); return; }
  const sev = e.target.closest("[data-sev]"); if (sev) { S.sevFilter = sev.dataset.sev; render(); return; }
  const area = e.target.closest("[data-area]"); if (area) { S.areaFilter = area.dataset.area; render(); return; }
  const raw = e.target.closest("[data-raw]"); if (raw) { e.preventDefault(); const [src, name] = raw.dataset.raw.split("|"); $("#raw-title").textContent = `${src} / ${name}`; $("#raw-json").textContent = JSON.stringify(S.bundle[src]?.[name], null, 2); return; }
  const id = e.target.id;
  if (id === "btn-md") download("unifi-suggestions.md", findingsMd());
  if (id === "btn-cheat-md") download("unifi-cheatsheet.md", cheatMd());
  if (id === "btn-dl" || id === "btn-export") download(`unifi-report-${new Date().toISOString().slice(0, 10)}.json`, JSON.stringify(S.bundle, null, 2), "application/json");
  if (id === "btn-reload-changes") loadChanges();
  if (id === "btn-print") window.print();
  if (id === "btn-lock") toggleLock().catch(err => toast(err.message, "err"));
});

// ------------------------------------------------------------------ sign-in, setup and start-up
async function collectLive(quiet, fresh) {
  if (!quiet) busy(true, fresh || !S.dataAge ? "Collecting configuration… (15–40 s)" : "Loading…");
  try { load(await api("/api/collect", { fresh: !!fresh }), "live", quiet); S.dataAt = (S.bundle?.meta?.cached_at || Date.now() / 1000) * 1000; renderChip(); } finally { busy(false); }
}
const EMPTY_BUNDLE = { integration: {}, legacy: {}, v2: {}, meta: {} };
async function authPost(path, body) {
  const r = await fetch(path, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
  const j = await r.json().catch(() => ({})); if (!r.ok) throw new Error(j.error || `HTTP ${r.status}`); return j;
}
function showAuth(html) { $("#app").hidden = true; $("#connect").hidden = false; $("#auth-box").innerHTML = html; const f = $("#auth-box input"); if (f) f.focus(); }
function showLogin(st) {
  showAuth(`<p class="muted">Sign in to keep an eye on your home network.</p>
    <form id="login-form" autocomplete="on"><label>Username<input id="l-user" autocomplete="username" required></label>
    <label>Password<input id="l-pass" type="password" autocomplete="current-password" required></label>
    <button class="btn primary" type="submit">Sign in</button><div id="auth-msg" class="msg" role="status"></div></form>
    ${st?.tailscale_login ? `<p class="fine">You're on the tailnet as <b>${esc(st.tailscale_login)}</b>. An admin can link this login to your user so you're signed in automatically.</p>` : ""}
    <p class="fine">Forgot your password? On the computer running HomeOwl run <span class="mono">homeowl reset-password USERNAME</span>.</p>`);
}
function showSetup(st) {
  showAuth(`<h2 style="margin:6px 0">Welcome! Let's create the admin account</h2>
    <p class="muted">This account manages HomeOwl. You can add family members (admins or view-only) later in Settings.</p>
    <form id="setup-form" autocomplete="off">
      ${st.code_required ? `<label>Setup code<input id="s-code" placeholder="XXXX-XXXX" required></label><p class="fine">You're not on the computer running HomeOwl, so a one-time code is needed: it's in HomeOwl's log, or run <span class="mono">homeowl setup-code</span> there.</p>` : ""}
      <div class="grid g2"><label>Username<input id="s-user" autocomplete="username" required placeholder="e.g. alex"></label><label>Your name<input id="s-name" placeholder="optional"></label></div>
      <div class="grid g2"><label>Password<input id="s-pass" type="password" autocomplete="new-password" required minlength="10"></label><label>Repeat password<input id="s-pass2" type="password" autocomplete="new-password" required></label></div>
      <label>Tailscale login (optional)<input id="s-ts" value="${esc(st.suggest_tailscale || "")}" placeholder="you@github or you@gmail.com"></label>
      <p class="fine">If you use HomeOwl's built-in Tailscale, this login signs you in automatically from your devices.</p>
      <button class="btn primary" type="submit">Create admin &amp; continue</button><div id="auth-msg" class="msg" role="status"></div></form>`);
}
document.addEventListener("submit", async e => {
  if (e.target.id !== "login-form" && e.target.id !== "setup-form") return;
  e.preventDefault(); const msg = $("#auth-msg"); msg.className = "msg"; msg.textContent = "…";
  try {
    if (e.target.id === "login-form") await authPost("/api/auth/login", { username: $("#l-user").value, password: $("#l-pass").value });
    else {
      if ($("#s-pass").value !== $("#s-pass2").value) throw new Error("The passwords don't match");
      await authPost("/api/auth/setup", { username: $("#s-user").value, name: $("#s-name").value, password: $("#s-pass").value, tailscale: $("#s-ts").value, setup_code: $("#s-code")?.value || "" });
    }
    msg.textContent = ""; await boot();
  } catch (err) { msg.className = "msg err"; msg.textContent = err.message; }
});
async function startApp() {
  const st = await api("/api/status");
  S.token = st.token; S.write = st.write; S.readOnly = st.read_only; S.user = st.user; S.role = st.user?.role; S.remote = !!st.user?.remote; S.dataAge = st.data_age;
  S.connected = st.connected; S.lastError = st.last_error;
  loadIspLogos();
  if (typeof renderCheckinBadge === "function") setTimeout(() => renderCheckinBadge(st.checkins_pending || 0), 0);
  setTimeout(() => typeof loadUpdate === "function" && loadUpdate(), 0);
  if (S.dataAge !== null && S.dataAge !== undefined) S.dataAt = Date.now() - S.dataAge * 1000;
  if (st.connected) {
    try { await collectLive(false, false); return; } catch (err) { toast(err.message, "err"); }
  }
  S.view = S.role === "admin" ? "settings" : "overview";
  load(EMPTY_BUNDLE, "none");
}
async function boot() {
  let st;
  try { st = await (await fetch("/api/auth/state")).json(); } catch { showAuth(`<p class="muted">Can't reach the HomeOwl server. You can still preview the app with demo data.</p>`); return; }
  if (st.needs_setup) return showSetup(st);
  if (!st.user) return showLogin(st);
  try { await startApp(); } catch (err) { showAuth(`<div class="msg err">${esc(err.message)}</div>`); }
}
$("#btn-demo").addEventListener("click", async () => { busy(true, "Loading demo…"); try { S.role = S.role || "viewer"; load(await (await fetch("demo.json")).json(), "demo"); } finally { busy(false); } });
$("#f-file").addEventListener("change", async e => { const f = e.target.files[0]; if (!f) return; try { load(JSON.parse(await f.text()), "file"); } catch (err) { toast("Not a valid report file: " + err.message, "err"); } });
$("#btn-refresh").addEventListener("click", () => S.source === "live" ? collectLive(false, true).catch(e => toast(e.message, "err")) : null);
$("#btn-logout").addEventListener("click", async () => {
  if (S.source === "demo" || S.source === "file") { location.reload(); return; }
  try { await fetch("/api/auth/logout", { method: "POST", headers: { "Content-Type": "application/json" }, body: "{}" }); } catch { }
  location.reload();
});
boot();

// ------------------------------------------------------------------ sidebar chip (who / data age)
function renderChip() {
  const m = S.model; if (!m) return;
  if (S.source === "none") { $("#site-chip").innerHTML = `not connected to UniFi` + (S.user ? `<br><span>👤 ${esc(S.user.name || S.user.username)} · ${S.role === "admin" ? "admin" : "view only"}</span>` : ""); return; }
  const age = S.dataAt ? Math.round((Date.now() - S.dataAt) / 60000) : null;
  $("#site-chip").innerHTML = `${esc(m.info.hostname || m.info.host || "")}<br>${S.source === "demo" ? "<b>demo data</b>" : S.source === "file" ? "saved report" : `live · ${age === null ? "" : age < 1 ? "just updated" : `updated ${age} min ago`}`}` +
    (S.user ? `<br><span>👤 ${esc(S.user.name || S.user.username)} · ${S.role === "admin" ? "admin" : "view only"}${S.user.via === "tailscale" ? " · via Tailscale" : ""}</span>` : "");
}
setInterval(renderChip, 60000);
