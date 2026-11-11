/* HomeOwl — Records › Reports: printable reports built from what HomeOwl already knows. Pick one, pick a period,
   then print it, save it as PDF (from the print dialog) or download it as a web page (or CSV for the inventory).
   Depends on core.js, app.js, analytics.js, explore.js, presence.js, alerts.js. */
"use strict";

S.rp = { type: null, days: 7 };
const REPORTS = [
  { id: "weekly", title: "Weekly summary", icon: "router", text: "One page: what needs you, the internet, devices, new arrivals, who was home and how the family used the internet." },
  { id: "health", title: "Network health", icon: "switch", text: "The configuration score, every suggestion by severity, offline devices and firmware." },
  { id: "internet", title: "Internet", icon: "gateway", text: "Each internet connection, its provider, speed, latency and availability, and recent outages." },
  { id: "inventory", title: "Device inventory", icon: "laptop", text: "Every device UniFi knows: name, owner, network, address and maker. Also as CSV.", noPeriod: true },
  { id: "family", title: "Family & usage", icon: "phone", text: "Per person and group: time online, top categories, blocked queries, data used and time at home." },
  { id: "security", title: "Security", icon: "camera", text: "Exposure findings, threats the gateway stopped, firewall blocks and the most blocked domains." },
  { id: "changes", title: "Changes & alerts", icon: "iot", text: "What HomeOwl changed on the console, and every alert, in the period." },
];

V.reports = () => {
  if (S.rp.type) return `<div class="rp-bar no-print"><button class="btn" id="rp-back">‹ All reports</button>
      ${REPORTS.find(r => r.id === S.rp.type)?.noPeriod ? "" : `<div class="seg">${[[7, "7 days"], [30, "30 days"]].map(([d, l]) => `<button data-rpdays="${d}" class="${S.rp.days === d ? "on" : ""}">${l}</button>`).join("")}</div>`}
      <span style="flex:1"></span>${S.rp.type === "inventory" ? `<button class="btn" id="rp-csv">Download CSV</button>` : ""}<button class="btn" id="rp-html">Download</button><button class="btn primary" id="rp-print">Print or save PDF</button></div>
    <article class="report" id="rp-doc"><div class="empty">Building the report…</div></article>`;
  return `<div class="head"><div><h1>Reports ${hint("Built from what HomeOwl already knows, on this computer. Print them, save them as PDF, or download them.")}</h1></div></div>
    <div class="rp-grid">${REPORTS.map(r => `<button class="card rp-card" data-rp="${r.id}"><span class="rp-ic">${iconSvg(r.icon, 22)}</span><b>${esc(r.title)}</b><span class="fine muted">${esc(r.text)}</span></button>`).join("")}</div>`;
};

// ---- building blocks
const rpSec = (title, body) => body ? `<section class="rp-sec"><h2>${esc(title)}</h2>${body}</section>` : "";
const rpKpis = list => `<div class="rp-kpis">${list.filter(Boolean).map(([l, v, s]) => `<div><span class="fine muted">${esc(l)}</span><b>${v}</b>${s ? `<span class="fine muted">${s}</span>` : ""}</div>`).join("")}</div>`;
const rpTable = (cols, rows, empty = "Nothing in this period.") => rows.length ? `<table class="rp-t"><thead><tr>${cols.map(c => `<th${c.num ? ` class="num"` : ""}>${esc(c.label)}</th>`).join("")}</tr></thead>
  <tbody>${rows.map(r => `<tr>${cols.map(c => `<td${c.num ? ` class="num"` : ""}>${c.fmt ? c.fmt(r) : esc(r[c.k] ?? "–")}</td>`).join("")}</tr>`).join("")}</tbody></table>` : `<p class="muted">${esc(empty)}</p>`;
const rpSince = () => Date.now() - S.rp.days * 86400e3;
const rpRange = () => S.rp.days <= 7 ? "7d" : "30d";
const rpWhen = t => new Date(t).toLocaleString([], { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });
const rpLive = () => S.source === "live";
async function rpTry(f, fallback = null) { try { return await f(); } catch { return fallback; } }
async function rpAq(name) { const r = await api("/api/analytics/query", { name, params: { range: rpRange() } }); return r.rows.map(row => Object.fromEntries(r.columns.map((c, i) => [c, row[i]]))); }
async function rpAnalyticsOn() { return rpLive() && (await rpTry(() => api("/api/analytics/status"), {}))?.open; }
async function rpAlerts() { const r = alLive() ? await rpTry(() => api("/api/alerts"), { items: [] }) : alFromBundle(); return arr(r.items).filter(a => a.time >= rpSince()); }
async function rpChanges() { return rpLive() ? arr(await rpTry(() => api("/api/changes"), [])).filter(c => Date.parse(c.time) >= rpSince()).reverse() : []; }
async function rpPresence() { return rpLive() ? await rpTry(() => api(`/api/presence?days=${S.rp.days}`), null) : prFromBundle(); }

// ---- the reports
const RP_BUILD = {
  async weekly(m) {
    const need = needsYou(m), up = m.wanLinks.some(l => l.enabled && (!l.status || l.status === "ok"));
    const [al, pr, an] = await Promise.all([rpAlerts(), rpPresence(), rpAnalyticsOn()]);
    const newDev = al.filter(a => a.kind === "new_device");
    let usage = "";
    if (an) {
      const [d, t] = await Promise.all([rpTry(() => rpAq("dns_summary"), []), rpTry(() => rpAq("traffic_summary"), [])]);
      const a = d[0] || {}, b = t[0] || {};
      usage = rpKpis([["DNS queries", fmtNum(a.total), `${pct(a.blocked, a.total)} blocked`], ["Downloaded", fmtBytes(b.wan_rx)], ["Uploaded", fmtBytes(b.wan_tx)], ["Active devices", fmtNum(b.active_clients)]]);
    }
    return rpSec("At a glance", rpKpis([["Status", need.length ? `${need.length} need you` : "All good"], ["Internet", up ? "Up" : "Down", m.info.down !== null ? `${Math.round(m.info.down)} / ${Math.round(m.info.up)} Mbps` : ""],
      ["Devices online", `${m.devices.filter(d => d.online).length}/${m.devices.length}`], ["Clients now", m.clients.length], ["Config score", scoreOf(S.findings)], ["New devices", newDev.length]]))
      + rpSec("Needs you", need.length ? `<ul>${need.map(n => `<li>${esc(n.t)}</li>`).join("")}</ul>` : "")
      + rpSec("New devices", newDev.length ? rpTable([{ label: "When", fmt: a => rpWhen(a.time) }, { label: "Device", fmt: a => esc(a.title.replace(/^New device: /, "")) }, { label: "Details", k: "body" }], newDev) : "")
      + rpSec("Who was home", rpHomeTable(pr))
      + rpSec("Internet use", usage)
      + rpSec("Top suggestions", rpTable([{ label: "Severity", fmt: f => sevTag(f.sev) }, { label: "Suggestion", k: "title" }, { label: "Area", k: "area" }], S.findings.filter(f => f.sev !== "info").slice(0, 8), "No suggestions."));
  },
  async health(m) {
    const F = S.findings, off = m.devices.filter(d => !d.online), fw = m.devices.filter(d => d.upgradable);
    return rpSec("Score", `<div class="rp-score">${gauge(scoreOf(F), 110)}${rpKpis(SEV_ORDER.map(s => [s, F.filter(f => f.sev === s).length]))}</div>`)
      + rpSec("Offline devices", off.length ? rpTable([{ label: "Device", k: "name" }, { label: "Model", k: "model" }, { label: "Status", k: "stateLabel" }], off) : `<p>Every UniFi device is online.</p>`)
      + rpSec("Firmware updates available", fw.length ? rpTable([{ label: "Device", k: "name" }, { label: "Model", k: "model" }, { label: "Running", k: "version" }], fw) : `<p>All devices are up to date.</p>`)
      + rpSec("Suggestions", rpTable([{ label: "Severity", fmt: f => sevTag(f.sev) }, { label: "Suggestion", k: "title" }, { label: "Area", k: "area" }, { label: "What to do", k: "fix" }], F, "No suggestions."))
      + rpSec("Devices", rpTable([{ label: "Device", k: "name" }, { label: "Model", k: "model" }, { label: "IP", k: "ip" }, { label: "Firmware", k: "version" }, { label: "Up for", fmt: d => d.uptime ? fmtDur(d.uptime) : "–" }, { label: "Clients", k: "clients", num: 1 }], m.devices));
  },
  async internet(m) {
    const outages = [...m.events, ...m.alarms].filter(e => e.t >= rpSince() && /wan|internet|isp|offline.*gateway|failover/.test(e.blob)).sort((a, b) => b.t - a.t).slice(0, 40);
    let traffic = "";
    if (await rpAnalyticsOn()) {
      const r = await rpTry(() => rpAq("traffic_timeline"), []);
      if (r.length > 1) traffic = lineChart([{ name: "Download", color: "var(--c2)", fill: true, points: series(r, "download") }, { name: "Upload", color: "var(--c3)", points: series(r, "upload") }], { fmt: fmtBytes, height: 170 });
    }
    return rpSec("Connections", rpTable([{ label: "Connection", k: "name" }, { label: "Provider", k: "isp" }, { label: "Status", fmt: l => esc(l.status || "–") + (l.active ? " · active" : "") },
      { label: "Latency", num: 1, fmt: l => l.latency !== null ? `${l.latency} ms` : "–" }, { label: "Availability", num: 1, fmt: l => l.availability !== null ? `${l.availability}%` : "–" }], m.wanLinks, "No internet connection reported."))
      + rpSec("Speed", rpKpis([["Download", m.info.down !== null ? `${Math.round(m.info.down)} Mbps` : "–"], ["Upload", m.info.up !== null ? `${Math.round(m.info.up)} Mbps` : "–"],
        ["Last speed test", m.info.speedtestAt ? rpWhen(m.info.speedtestAt * 1000) : "–"], ["Latency", m.info.latency !== null ? `${m.info.latency} ms` : "–"]]))
      + rpSec("Traffic", traffic)
      + rpSec("Outages and internet events", rpTable([{ label: "When", fmt: e => rpWhen(e.t) }, { label: "What", fmt: e => esc(e.msg || e.key) }], outages, "No internet problems recorded in this period."));
  },
  async inventory(m) {
    const owners = await rpOwners();
    const rows = rpInventoryRows(m, owners);
    return rpSec(`${rows.length} devices`, rpTable([{ label: "Name", k: "name" }, { label: "Owner", k: "owner" }, { label: "Network", k: "network" }, { label: "IP", k: "ip" }, { label: "MAC", fmt: r => `<span class="mono">${esc(r.mac)}</span>` },
      { label: "Maker", k: "vendor" }, { label: "Link", k: "link" }, { label: "Last seen", k: "seen" }], rows));
  },
  async family() {
    if (!(await rpAnalyticsOn())) return rpSec("Not available", `<p>This report needs Analytics with NextDNS or UniFi data.</p>`);
    const meta = await api("/api/analytics/meta", {}), pr = await rpPresence();
    const parts = await Promise.all(meta.people.map(async p => {
      const r = await rpTry(() => api("/api/analytics/explore", { range: rpRange(), filters: { person: p.id }, views: ["summary", "by_category", "traffic"], raw: S.ex.raw }), {});
      const s = r.summary || {}, days = s.active_days || 1, tr = r.traffic?.summary || {};
      const home = pr && arr(pr.people).find(x => x.id === p.id);
      const homeMs = home ? prSpans(home, arr(pr.events), Math.max(rpSince(), Math.min(...arr(pr.events).map(e => e.t), Date.now())), Date.now()).reduce((a, [x, y]) => a + y - x, 0) : 0;
      return `<div class="rp-person"><h3><i style="background:${esc(p.color || "var(--c4)")}"></i>${esc(p.name)}${p.kind === "group" ? ` <span class="fine muted">group</span>` : ""}</h3>
        ${rpKpis([["Queries a day", fmtNum(Math.round((s.total || 0) / days))], ["Active a day ≈", s.active_min ? fmtMin(s.active_min / days) : "–"], ["Blocked", pct(s.blocked, s.total)],
          ["Data a day", tr.rx || tr.tx ? fmtBytes((tr.rx + tr.tx) / days) : "–"], p.kind !== "group" && pr?.history ? ["At home", prDur(homeMs)] : null])}
        ${(r.by_category || []).length ? `<p class="fine">Top: ${r.by_category.slice(0, 6).map(c => `${esc(c.key)} (${fmtNum(c.total)})`).join(" · ")}</p>` : ""}</div>`;
    }));
    return rpSec("People and groups", parts.join("") || `<p>No people or groups yet.</p>`);
  },
  async security(m) {
    const exp = S.findings.filter(f => /security|exposure|firewall|port/i.test(f.area + " " + f.title));
    const ips = m.ips.filter(e => e.t >= rpSince());
    let an = "";
    if (await rpAnalyticsOn()) {
      const [s, top, blk] = await Promise.all([rpTry(() => rpAq("sec_summary"), []), rpTry(() => rpAq("threat_top"), []), rpTry(() => rpAq("dns_top_blocked"), [])]);
      const c = s[0] || {};
      an = rpKpis([["Threats", fmtNum(c.threats)], ["Firewall blocks", fmtNum(c.fw_blocked)], ["Syslog lines", fmtNum(c.syslog_lines)]])
        + rpTable([{ label: "Threat", k: "signature" }, { label: "Hits", k: "hits", num: 1 }], top.slice(0, 10), "No threats recorded.")
        + `<h3>Most blocked domains</h3>` + rpTable([{ label: "Domain", k: "domain" }, { label: "Queries", num: 1, fmt: r => fmtNum(r.queries) }], blk.slice(0, 10));
    }
    return rpSec("Exposure", rpTable([{ label: "Severity", fmt: f => sevTag(f.sev) }, { label: "Finding", k: "title" }, { label: "What to do", k: "fix" }], exp, "Nothing exposed that HomeOwl knows of."))
      + rpSec("Threats and blocks", an || rpTable([{ label: "When", fmt: e => rpWhen(e.t) }, { label: "Signature", k: "sig" }, { label: "Action", k: "action" }], ips.slice(0, 30), "No threats recorded in this period."));
  },
  async changes() {
    const [ch, al] = await Promise.all([rpChanges(), rpAlerts()]);
    return rpSec("Changes HomeOwl made", rpTable([{ label: "When", fmt: c => esc(c.time) }, { label: "Change", fmt: c => esc(c.description || c.target) }, { label: "By", k: "by" },
      { label: "Result", fmt: c => c.ok ? (c.undone ? "undone" : "done") : "failed" }], ch, rpLive() ? "No changes in this period." : "Changes are only kept by a running HomeOwl."))
      + rpSec("Alerts", rpTable([{ label: "When", fmt: a => rpWhen(a.time) }, { label: "Alert", k: "title" }, { label: "Details", k: "body" }], al));
  },
};

function rpHomeTable(pr) {
  if (!pr || !pr.history) return "";
  const people = arr(pr.people).filter(p => arr(p.devices).length); if (!people.length) return "";
  // count only from when the history starts, or the hours before it would all look like "home"
  const first = Math.min(...arr(pr.events).map(e => e.t), Date.now()), from = Math.max(rpSince(), first);
  return (from > rpSince() ? `<p class="fine muted">History starts ${rpWhen(from)}.</p>` : "") + rpTable([{ label: "Person", k: "name" }, { label: "At home", fmt: p => prDur(prSpans(p, arr(pr.events), from, Date.now()).reduce((a, [x, y]) => a + y - x, 0)) },
    { label: "Arrivals", num: 1, fmt: p => arr(pr.events).filter(e => e.person === p.id && e.kind === "arrive" && e.t >= rpSince()).length }], people);
}
async function rpOwners() {
  const owners = {};
  if (await rpAnalyticsOn()) { const meta = await rpTry(() => api("/api/analytics/meta", {}), { people: [] }); meta.people.forEach(p => p.devices.forEach(d => { if (d.kind === "mac") owners[d.key] = p.name; })); }
  return owners;
}
function rpInventoryRows(m, owners) {
  const list = m.known.length ? m.known : m.clients;
  return list.map(c => ({ name: c.label || c.name || c.mac, owner: owners[c.mac] || "", network: c.network || "", ip: c.ip || "", mac: c.mac, vendor: c.vendor || "",
    link: c.wired === true ? "wired" : c.wired === false ? "Wi-Fi" : "", seen: c.online ? "online" : c.lastSeen ? ago(c.lastSeen) : "" })).sort((a, b) => a.name.localeCompare(b.name));
}

async function loadReport() {
  const el = $("#rp-doc"); if (!el || !S.model) return;
  const r = REPORTS.find(x => x.id === S.rp.type);
  const head = `<header class="rp-head"><div><img src="icon.png" alt="" class="logo-img"><span>HomeOwl</span></div><h1>${esc(r.title)}</h1>
    <p class="muted">${esc(S.model.info.hostname || S.model.info.site || "")}${r.noPeriod ? "" : ` · last ${S.rp.days} days`} · made ${esc(new Date().toLocaleString([], { day: "numeric", month: "long", year: "numeric", hour: "2-digit", minute: "2-digit" }))}</p></header>`;
  try { const body = await RP_BUILD[r.id](S.model); if (S.rp.type === r.id && $("#rp-doc")) $("#rp-doc").innerHTML = head + (body || `<p class="muted">Nothing to report.</p>`); }
  catch (e) { el.innerHTML = head + `<div class="empty bad-t">${esc(e.message)}</div>`; }
}

// a stand-alone copy: the report plus the colours and styles it uses
function rpDownload() {
  const doc = $("#rp-doc"); if (!doc) return;
  const cs = getComputedStyle(document.documentElement);
  const vars = ["--bg", "--panel", "--panel2", "--text", "--muted", "--line", "--accent", "--ok", "--crit", "--high", "--med", "--low", "--info", ...Array.from({ length: 8 }, (_, i) => `--c${i + 1}`)]
    .map(v => `${v}:${cs.getPropertyValue(v).trim()}`).join(";");
  const css = [...document.styleSheets].flatMap(sh => { try { return [...sh.cssRules].map(r => r.cssText); } catch { return []; } }).filter(t => /^(\.rp-|\.report|\.sev|\.tag|\.muted|\.fine|\.mono|svg|\.gauge|\.chart|\.legend)/.test(t)).join("\n");
  const html = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>${esc($("#rp-doc h1")?.textContent || "HomeOwl report")}</title>
<style>:root{${vars}}body{font-family:-apple-system,"Segoe UI",Roboto,sans-serif;background:var(--bg);color:var(--text);margin:0;padding:24px}${css}</style></head><body><article class="report">${doc.innerHTML.replace(/<img[^>]*>/g, "")}</article></body></html>`;
  const a = document.createElement("a"); a.href = URL.createObjectURL(new Blob([html], { type: "text/html" }));
  a.download = `homeowl-${S.rp.type}-${new Date().toISOString().slice(0, 10)}.html`; a.click(); setTimeout(() => URL.revokeObjectURL(a.href), 2000);
}
async function rpCsv() {
  const rows = rpInventoryRows(S.model, await rpOwners()), cols = ["name", "owner", "network", "ip", "mac", "vendor", "link", "seen"];
  const q = v => `"${String(v ?? "").replace(/"/g, '""')}"`;
  const a = document.createElement("a"); a.href = URL.createObjectURL(new Blob([[cols.join(","), ...rows.map(r => cols.map(c => q(r[c])).join(","))].join("\n")], { type: "text/csv" }));
  a.download = `homeowl-devices-${new Date().toISOString().slice(0, 10)}.csv`; a.click(); setTimeout(() => URL.revokeObjectURL(a.href), 2000);
}

document.addEventListener("click", e => {
  if (S.view !== "reports") return;
  const c = e.target.closest("[data-rp]"); if (c) { S.rp.type = c.dataset.rp; render(); window.scrollTo(0, 0); return; }
  const d = e.target.closest("[data-rpdays]"); if (d) { S.rp.days = +d.dataset.rpdays; render(); return; }
  if (e.target.id === "rp-back") { S.rp.type = null; render(); return; }
  if (e.target.id === "rp-print") { window.print(); return; }
  if (e.target.id === "rp-html") { rpDownload(); return; }
  if (e.target.id === "rp-csv") rpCsv();
});
{ const _render = render; render = function () { _render(); if (S.view === "reports" && S.rp.type) loadReport(); }; }
