/* HomeOwl — Analytics (DuckDB): DNS, devices, traffic, security, logs, SQL. Depends on core.js + app.js */
"use strict";

S.an = { tab: "overview", range: "7d", profile: "", device: "", mac: "", log: "dns", q: "", status: "", st: null, sql: "" };
const AN_TABS = [["overview", "Overview"], ["explore", "Explore"], ["people", "People"], ["categories", "Categories"], ["dns", "DNS"], ["devices", "Devices"], ["traffic", "Traffic"], ["security", "Security"], ["logs", "Logs"], ["sql", "SQL"]];
const RANGES = [["24h", "24 h"], ["7d", "7 days"], ["30d", "30 days"], ["90d", "90 days"], ["1y", "1 year"], ["all", "All"]];
const fmtNum = n => n === null || n === undefined ? "–" : Number(n).toLocaleString();
const pct = (a, b) => b ? `${(100 * a / b).toFixed(1)}%` : "–";

async function aq(name, extra = {}) {
  const r = await api("/api/analytics/query", { name, params: { range: S.an.range, profile: S.an.profile, ...extra } });
  return r.rows.map(row => Object.fromEntries(r.columns.map((c, i) => [c, row[i]])));
}
const slot = (id, title, cls = "") => `<div class="card ${cls}"><h2>${esc(title)}</h2><div id="${id}"><div class="empty">Loading…</div></div></div>`;
const fill = (id, html) => { const el = document.getElementById(id); if (el) el.innerHTML = html; };
const fail = (id, err) => fill(id, `<div class="empty bad-t">${esc(err.message || err)}</div>`);
async function into(id, fn) { try { fill(id, await fn()); } catch (e) { fail(id, e); } }
const kpiTile = (label, value, sub = "") => `<div class="card kpi"><div class="l">${esc(label)}</div><div class="v">${value}</div><div class="s">${sub}</div></div>`;
const series = (rows, key) => rows.map(r => ({ t: r.t, v: r[key] }));

V.analytics = () => {
  const st = S.an.st;
  const admin = S.role === "admin";
  const head = `<div class="head"><div><h1>Analytics</h1><div class="muted">NextDNS queries, UniFi traffic, threats and syslog, stored in HomeOwl's built-in DuckDB database.</div></div>
    <div class="an-controls"><div class="seg">${RANGES.map(([k, l]) => `<button data-anrange="${k}" class="${S.an.range === k ? "on" : ""}">${l}</button>`).join("")}</div>
    ${st?.profiles && Object.keys(st.profiles).length && !["explore", "people", "categories"].includes(S.an.tab) ? `<select id="an-profile"><option value="">All NextDNS profiles</option>${Object.entries(st.profiles).map(([id, n]) => `<option value="${esc(id)}" ${S.an.profile === id ? "selected" : ""}>${esc(n)} (${esc(id)})</option>`).join("")}</select>` : ""}</div></div>`;
  if (!st) return `${head}<div class="empty">Loading…</div>`;
  if (!st.engine || !st.enabled || !st.open) {
    return `${head}<div class="card"><h2>Analytics isn't set up yet</h2>
      <p>It imports your NextDNS query logs and UniFi gateway data into a local DuckDB database, so you can see usage per device, top domains, blocked trackers, traffic, threats and firewall logs over time.</p>
      ${admin ? `<p><button class="btn primary" data-goto="settings">Set it up in Settings</button></p>` : `<p class="muted">An admin needs to turn it on in Settings.</p>`}</div>`;
  }
  const tabs = AN_TABS.filter(([k]) => k !== "sql" || admin);
  const syncing = Object.keys(st.running || {}).length, backfill = (st.sources || []).find(s => s.note && /Importing/.test(s.note));
  return `${head}
    ${backfill ? `<div class="banner info">Importing NextDNS history — ${esc(backfill.note.replace("Importing history… ", ""))}. Charts fill in as it goes.</div>` : ""}
    <div class="filters tabs">${tabs.map(([k, l]) => `<button data-antab="${k}" class="${S.an.tab === k ? "on" : ""}">${l}</button>`).join("")}
      <span class="muted fine" style="margin-left:auto">${syncing ? "Syncing…" : `${fmtNum(st.counts?.dns_queries)} DNS queries · ${fmtBytes(st.db_bytes)} database`}</span></div>
    <div id="an-body">${anBody()}</div>`;
};

function anBody() {
  const t = S.an.tab;
  if (t === "overview") return `<div class="grid g4" id="an-kpis"><div class="empty">Loading…</div></div>
    <div class="grid g2 section">${slot("an-dns-tl", "DNS queries")}${slot("an-wan-tl", "Internet traffic")}</div>
    <div class="grid g2 section">${slot("an-top-dev", "Most active devices (DNS)")}${slot("an-top-talk", "Top data users")}</div>
    <div class="grid g2 section">${slot("an-top-blk", "Most blocked domains")}${slot("an-threats", "Threats")}</div>`;
  if (t === "dns") return `<div class="grid g4" id="an-dns-kpis"><div class="empty">Loading…</div></div>
    <div class="card section"><h2>Queries over time</h2><div id="an-dns-tl"></div></div>
    <div class="grid g2 section">${slot("an-dom", "Top domains")}${slot("an-blk", "Top blocked domains")}</div>
    <div class="grid g3 section">${slot("an-trk", "Trackers")}${slot("an-rsn", "Why queries were blocked")}${slot("an-proto", "Protocols")}</div>
    <div class="section">${slot("an-prof", "By NextDNS profile")}</div>`;
  if (t === "devices") return `${S.an.device ? `<div id="an-devdetail" class="card"><div class="empty">Loading…</div></div><div class="section"></div>` : ""}
    ${slot("an-devs", "Devices — DNS activity (click for details)")}
    <div class="grid g2 section">${slot("an-talk", "Devices — data used")}${slot("an-online", "Time online")}</div>
    <div class="section">${slot("an-new", "New devices in this period")}</div>`;
  if (t === "traffic") return `${S.an.mac ? `<div id="an-macdetail" class="card"><div class="empty">Loading…</div></div><div class="section"></div>` : ""}
    <div class="grid g4" id="an-tr-kpis"><div class="empty">Loading…</div></div>
    <div class="card section"><h2>Internet traffic</h2><div id="an-wan"></div></div>
    <div class="grid g2 section">${slot("an-nets", "By network")}${slot("an-apps", "By app category (since tracking began)")}</div>
    <div class="section">${slot("an-clients", "Top clients (click for timeline)")}</div>`;
  if (t === "security") return `<div class="grid g4" id="an-sec-kpis"><div class="empty">Loading…</div></div>
    <div class="grid g2 section">${slot("an-thr-tl", "Threats (IPS) over time")}${slot("an-fw-tl", "Firewall log over time")}</div>
    <div class="grid g2 section">${slot("an-thr-top", "Top threat signatures")}${slot("an-thr-cty", "Threat sources by country")}</div>
    <div class="grid g3 section">${slot("an-fw-src", "Firewall: top sources")}${slot("an-fw-port", "Firewall: top ports")}${slot("an-fw-rule", "Firewall: rules hit")}</div>
    <div class="section">${slot("an-thr-list", "Latest threats")}</div>`;
  if (t === "logs") return `<div class="card"><div class="filters"><div class="seg">${[["dns", "DNS queries"], ["syslog", "Syslog"], ["events", "UniFi events"]].map(([k, l]) => `<button data-anlog="${k}" class="${S.an.log === k ? "on" : ""}">${l}</button>`).join("")}</div>
      <input id="an-q" class="search" placeholder="Search domain, device, IP…" value="${esc(S.an.q)}">
      ${S.an.log === "dns" ? `<select id="an-status" style="width:auto"><option value="">All statuses</option>${["blocked", "allowed", "default", "error"].map(s => `<option ${S.an.status === s ? "selected" : ""}>${s}</option>`).join("")}</select>` : ""}
      ${S.an.device && S.an.log === "dns" ? `<span class="tag">device: ${esc(S.an.device)} <a href="#" data-anclear="device">×</a></span>` : ""}</div>
      <div id="an-log"><div class="empty">Loading…</div></div><p class="fine">Newest 300 entries in the selected period.</p></div>`;
  if (t === "sql") return `<div class="card"><h2>SQL console (read-only)</h2>
      <div class="filters"><select id="an-sql-ex" style="width:auto"><option value="">Examples…</option>${SQL_EXAMPLES.map((e, i) => `<option value="${i}">${esc(e[0])}</option>`).join("")}</select>
        <span class="muted fine">Tables: dns_queries, dns_hourly_device, dns_daily_domain, client_hourly, site_hourly, sessions, clients, events, threats, syslog, syslog_hourly, dpi_snapshots, nextdns_profiles</span></div>
      <textarea id="an-sql" rows="7" spellcheck="false" placeholder="SELECT …">${esc(S.an.sql || SQL_EXAMPLES[0][1])}</textarea>
      <div class="btnrow" style="margin-top:8px"><button class="btn primary" id="an-sql-run">Run  ⌘↵</button><button class="btn" id="an-sql-csv">Download CSV</button></div>
      <div id="an-sql-out" class="section"></div></div>`;
  return "";
}

const SQL_EXAMPLES = [
  ["Busiest devices today", "SELECT coalesce(device_name, local_ip) AS device, count(*) AS queries,\n       count(*) FILTER (WHERE status = 'blocked') AS blocked\nFROM dns_queries WHERE ts >= current_date\nGROUP BY 1 ORDER BY 2 DESC LIMIT 20"],
  ["Domains a device talked to", "SELECT root, count(*) AS queries, max(ts) AS last_seen\nFROM dns_queries WHERE device_name = 'LG-TV'\nGROUP BY root ORDER BY queries DESC LIMIT 50"],
  ["Data used per device per day", "SELECT CAST(h.hour AS DATE) AS day, coalesce(c.name, c.hostname, h.mac) AS device,\n       round(sum(h.rx + h.tx) / 1e9, 2) AS gb\nFROM client_hourly h LEFT JOIN clients c USING (mac)\nWHERE h.hour >= now() - INTERVAL 7 DAY\nGROUP BY ALL ORDER BY day DESC, gb DESC"],
  ["Night-time activity (00–06)", "SELECT coalesce(device_name, local_ip) AS device, count(*) AS queries\nFROM dns_queries WHERE hour(ts) BETWEEN 0 AND 5 AND ts >= now() - INTERVAL 7 DAY\nGROUP BY 1 ORDER BY 2 DESC"],
  ["First time a domain was seen", "SELECT root, min(ts) AS first_seen, count(*) AS queries\nFROM dns_queries GROUP BY root\nHAVING min(ts) >= now() - INTERVAL 1 DAY ORDER BY first_seen DESC"],
  ["Blocked firewall connections by port", "SELECT dport, proto, count(*) AS hits, count(DISTINCT src) AS sources\nFROM syslog WHERE kind = 'firewall' AND action = 'blocked'\nGROUP BY ALL ORDER BY hits DESC LIMIT 25"],
  ["Table sizes", "SELECT table_name, estimated_size AS rows FROM duckdb_tables() ORDER BY rows DESC"],
];

// ---- tab loaders
async function loadAnalytics() {
  try { S.an.st = await api("/api/analytics/status"); } catch (e) { S.an.st = { error: e.message }; }
  const main = $("#main"); if (S.view !== "analytics" || !main) return;
  const y = window.scrollY; main.innerHTML = V.analytics(); window.scrollTo(0, y);
  if (!S.an.st?.open) return;
  const t = S.an.tab, L = loaders[t]; if (L) L();
}
const loaders = {
  overview() {
    (async () => {
      try {
        const [d, tr, sec] = await Promise.all([aq("dns_summary"), aq("traffic_summary"), aq("sec_summary")]);
        const a = d[0] || {}, b = tr[0] || {}, c = sec[0] || {};
        fill("an-kpis", kpiTile("DNS queries", fmtNum(a.total), `${pct(a.blocked, a.total)} blocked · ${fmtNum(a.devices)} devices`) +
          kpiTile("Internet download", fmtBytes(b.wan_rx), `upload ${fmtBytes(b.wan_tx)}`) +
          kpiTile("Active devices", fmtNum(b.active_clients), `${fmtNum(b.new_clients)} new in this period`) +
          kpiTile("Threats · firewall blocks", `${fmtNum(c.threats)} · ${fmtNum(c.fw_blocked)}`, `${fmtNum(c.syslog_lines)} syslog lines`));
      } catch (e) { fail("an-kpis", e); }
    })();
    into("an-dns-tl", async () => { const r = await aq("dns_timeline"); return lineChart([{ name: "All queries", color: "var(--c1)", fill: true, points: series(r, "total") }, { name: "Blocked", color: "var(--crit)", points: series(r, "blocked") }], { fmt: v => fmtNum(Math.round(v)), height: 180 }); });
    into("an-wan-tl", async () => { const r = await aq("traffic_timeline"); return lineChart([{ name: "Download", color: "var(--c2)", fill: true, points: series(r, "download") }, { name: "Upload", color: "var(--c3)", points: series(r, "upload") }], { fmt: fmtBytes, height: 180 }); });
    into("an-top-dev", async () => hbar((await aq("dns_devices")).slice(0, 10).map(r => ({ label: r.device, value: r.queries, color: "var(--c1)" })), { fmt: fmtNum }));
    into("an-top-talk", async () => hbar((await aq("traffic_clients")).slice(0, 10).map(r => ({ label: r.client, value: r.total, color: "var(--c2)" })), { fmt: fmtBytes }));
    into("an-top-blk", async () => hbar((await aq("dns_top_blocked")).slice(0, 10).map(r => ({ label: r.domain, value: r.queries, color: "var(--crit)" })), { fmt: fmtNum }));
    into("an-threats", async () => { const r = await aq("threat_top"); return r.length ? hbar(r.slice(0, 8).map(x => ({ label: x.signature, value: x.hits, color: "var(--high)" }))) : `<div class="empty">No threats recorded in this period.</div>`; });
  },
  dns() {
    (async () => {
      try {
        const [d, p] = await Promise.all([aq("dns_summary"), aq("dns_protocols")]);
        const a = d[0] || {}, enc = p.reduce((s, x) => s + x.queries * x.encrypted_pct, 0) / Math.max(1, p.reduce((s, x) => s + x.queries, 0));
        fill("an-dns-kpis", kpiTile("Queries", fmtNum(a.total)) + kpiTile("Blocked", fmtNum(a.blocked), pct(a.blocked, a.total)) + kpiTile("Devices", fmtNum(a.devices)) + kpiTile("Domains", fmtNum(a.domains), p.length ? `${enc.toFixed(0)}% encrypted` : ""));
      } catch (e) { fail("an-dns-kpis", e); }
    })();
    into("an-dns-tl", async () => { const r = await aq("dns_timeline"); return lineChart([{ name: "All queries", color: "var(--c1)", fill: true, points: series(r, "total") }, { name: "Blocked", color: "var(--crit)", points: series(r, "blocked") }], { fmt: v => fmtNum(Math.round(v)) }); });
    into("an-dom", async () => hbar((await aq("dns_top_domains")).slice(0, 15).map(r => ({ label: r.domain, value: r.queries, color: "var(--c1)" })), { fmt: fmtNum }));
    into("an-blk", async () => hbar((await aq("dns_top_blocked")).slice(0, 15).map(r => ({ label: r.domain, value: r.queries, color: "var(--crit)" })), { fmt: fmtNum }));
    into("an-trk", async () => hbar((await aq("dns_trackers")).map(r => ({ label: r.tracker, value: r.queries, color: "var(--c4)" })), { fmt: fmtNum }));
    into("an-rsn", async () => { const r = await aq("dns_reasons"); return r.length ? hbar(r.map(x => ({ label: x.reason, value: x.blocked, color: "var(--high)" })), { fmt: fmtNum }) : `<div class="empty">Only available for the last ${S.an.st.config.raw_days} days.</div>`; });
    into("an-proto", async () => donut((await aq("dns_protocols")).map((r, i) => ({ label: r.protocol, value: r.queries, color: PAL[i % PAL.length] })), "", ""));
    into("an-prof", async () => table("an-prof-t", [{ k: "name", label: "Profile" }, { k: "profile_id", label: "ID", fmt: r => `<span class="mono">${esc(r.profile_id)}</span>` }, { k: "queries", label: "Queries", num: 1, fmt: r => fmtNum(r.queries) },
      { k: "blocked", label: "Blocked", num: 1, fmt: r => `${fmtNum(r.blocked)} <span class="muted">(${pct(r.blocked, r.queries)})</span>` }, { k: "devices", label: "Devices", num: 1 }], await aq("dns_profiles"), { empty: "No DNS data yet" }));
  },
  devices() {
    if (S.an.device) loadDeviceDetail();
    into("an-devs", async () => table("an-devs-t", [
      { k: "device", label: "Device (as named in NextDNS)", fmt: r => `<a href="#" data-andev="${esc(r.device)}">${esc(r.device)}</a>` },
      { k: "queries", label: "Queries", num: 1, fmt: r => fmtNum(r.queries) }, { k: "blocked", label: "Blocked", num: 1, fmt: r => fmtNum(r.blocked) },
      { k: "block_pct", label: "Blocked %", num: 1, fmt: r => `${r.block_pct ?? 0}%` }, { k: "profiles", label: "Profiles", fmt: r => `<span class="mono">${esc(r.profiles || "")}</span>` },
    ], await aq("dns_devices"), { search: true, empty: "No DNS devices yet — check NextDNS import in Settings" }));
    into("an-talk", async () => table("an-talk-t", [{ k: "client", label: "Device", fmt: r => `<a href="#" data-anmac="${esc(r.mac)}">${esc(r.client)}</a>` }, { k: "network", label: "Network" },
      { k: "download", label: "Down", num: 1, fmt: r => fmtBytes(r.download) }, { k: "upload", label: "Up", num: 1, fmt: r => fmtBytes(r.upload) }], await aq("traffic_clients"), { search: true, empty: "No traffic data yet" }));
    into("an-online", async () => hbar((await aq("online_time")).slice(0, 15).map(r => ({ label: r.client, value: r.hours_online, color: "var(--c4)" })), { unit: " h" }));
    into("an-new", async () => table("an-new-t", [{ k: "client", label: "Device" }, { k: "vendor", label: "Vendor" }, { k: "network", label: "Network" }, { k: "ip", label: "IP", fmt: r => `<span class="mono">${esc(r.ip || "")}</span>` },
      { k: "first_seen", label: "First seen", num: 1, fmt: r => fmtTime(r.first_seen) }, { k: "mac", label: "MAC", fmt: r => `<span class="mono">${esc(r.mac)}</span>` }], await aq("new_clients"), { empty: "No new devices in this period" }));
  },
  traffic() {
    if (S.an.mac) loadMacDetail();
    (async () => {
      try {
        const b = (await aq("traffic_summary"))[0] || {};
        fill("an-tr-kpis", kpiTile("Download", fmtBytes(b.wan_rx)) + kpiTile("Upload", fmtBytes(b.wan_tx)) + kpiTile("Active devices", fmtNum(b.active_clients)) + kpiTile("New devices", fmtNum(b.new_clients)));
      } catch (e) { fail("an-tr-kpis", e); }
    })();
    into("an-wan", async () => { const r = await aq("traffic_timeline"); return lineChart([{ name: "Download", color: "var(--c2)", fill: true, points: series(r, "download") }, { name: "Upload", color: "var(--c3)", points: series(r, "upload") }], { fmt: fmtBytes }); });
    into("an-nets", async () => hbar((await aq("traffic_networks")).map((r, i) => ({ label: `${r.network} (${r.clients})`, value: r.total, color: PAL[i % PAL.length] })), { fmt: fmtBytes }));
    into("an-apps", async () => hbar((await aq("apps")).map(r => ({ label: DPI_CAT[r.cat] || `Category ${r.cat}`, value: r.total, color: "var(--c1)" })), { fmt: fmtBytes }));
    into("an-clients", async () => table("an-cl-t", [{ k: "client", label: "Device", fmt: r => `<a href="#" data-anmac="${esc(r.mac)}">${esc(r.client)}</a>` }, { k: "network", label: "Network" }, { k: "vendor", label: "Vendor" },
      { k: "download", label: "Down", num: 1, fmt: r => fmtBytes(r.download) }, { k: "upload", label: "Up", num: 1, fmt: r => fmtBytes(r.upload) }, { k: "total", label: "Total", num: 1, fmt: r => fmtBytes(r.total) }], await aq("traffic_clients"), { search: true }));
  },
  security() {
    (async () => {
      try {
        const c = (await aq("sec_summary"))[0] || {};
        fill("an-sec-kpis", kpiTile("Threats (IPS)", fmtNum(c.threats)) + kpiTile("Firewall blocks", fmtNum(c.fw_blocked), "from syslog") + kpiTile("Syslog lines", fmtNum(c.syslog_lines)) + kpiTile("UniFi events", fmtNum(c.events)));
      } catch (e) { fail("an-sec-kpis", e); }
    })();
    into("an-thr-tl", async () => columns((await aq("threat_timeline")).map(r => ({ label: new Date(r.t).toLocaleString([], S.an.range === "24h" || S.an.range === "7d" ? { day: "numeric", hour: "2-digit" } : { month: "short", day: "numeric" }), value: r.threats, color: "var(--high)" }))));
    into("an-fw-tl", async () => { const r = await aq("fw_timeline"); return r.length ? lineChart([{ name: "Blocked", color: "var(--crit)", fill: true, points: series(r, "blocked") }, { name: "Allowed / other", color: "var(--c7)", points: series(r, "other") }], { fmt: v => fmtNum(Math.round(v)), height: 170 }) : `<div class="empty">No firewall syslog yet. Turn on syslog in Settings and enable “Syslog Logging” on the UniFi policies you want to see.</div>`; });
    into("an-thr-top", async () => hbar((await aq("threat_top")).map(r => ({ label: r.signature, value: r.hits, color: "var(--high)" }))));
    into("an-thr-cty", async () => hbar((await aq("threat_countries")).map(r => ({ label: r.country, value: r.hits, color: "var(--c6)" }))));
    into("an-fw-src", async () => table("an-fws-t", [{ k: "src", label: "Source", fmt: r => `<span class="mono">${esc(r.src)}</span>` }, { k: "hits", label: "Hits", num: 1 }, { k: "blocked", label: "Blocked", num: 1 }], await aq("fw_sources"), { empty: "No firewall logs" }));
    into("an-fw-port", async () => table("an-fwp-t", [{ k: "dport", label: "Port", num: 1, fmt: r => `${r.dport}${RISKY_PORTS[r.dport] ? ` <span class="muted">${RISKY_PORTS[r.dport]}</span>` : ""}` }, { k: "proto", label: "Proto" }, { k: "hits", label: "Hits", num: 1 }], await aq("fw_ports"), { empty: "No firewall logs" }));
    into("an-fw-rule", async () => table("an-fwr-t", [{ k: "rule", label: "Rule", fmt: r => `<span class="mono">${esc(r.rule)}</span>` }, { k: "action", label: "Action", fmt: r => `<span class="tag ${r.action === "blocked" ? "bad" : "good"}">${esc(r.action || "?")}</span>` }, { k: "hits", label: "Hits", num: 1 }], await aq("fw_rules"), { empty: "No firewall logs" }));
    into("an-thr-list", async () => table("an-thl-t", [{ k: "t", label: "Time", fmt: r => fmtTime(r.t), sort: r => r.t }, { k: "signature", label: "Signature" }, { k: "action", label: "Action", fmt: r => `<span class="tag ${/block|drop/i.test(r.action || "") ? "good" : "warn"}">${esc(r.action || "?")}</span>` },
      { k: "src_ip", label: "From", fmt: r => `<span class="mono">${esc(r.src_ip || "")}</span> ${esc(r.country || "")}` }, { k: "dst_ip", label: "To", fmt: r => `<span class="mono">${esc(r.dst_ip || "")}${r.dst_port ? ":" + r.dst_port : ""}</span>` }], await aq("threat_list"), { search: true, empty: "No threats in this period" }));
  },
  logs() { loadLog(); },
  sql() { },
};

async function loadDeviceDetail() {
  const dev = S.an.device;
  try {
    const [tl, doms] = await Promise.all([aq("dns_device_timeline", { device: dev }), aq("dns_device_domains", { device: dev })]);
    fill("an-devdetail", `<div class="head" style="margin:0 0 8px"><h2 style="margin:0">${esc(dev)}</h2><div class="btnrow"><button class="btn sm primary" data-exgo="device|${esc(dev)}">Explore</button><button class="btn sm" data-anlogdev="${esc(dev)}">Show its queries</button><button class="btn sm" data-anclear="device">Close</button></div></div>
      <div class="grid g2"><div>${lineChart([{ name: "Queries", color: "var(--c1)", fill: true, points: series(tl, "total") }, { name: "Blocked", color: "var(--crit)", points: series(tl, "blocked") }], { fmt: v => fmtNum(Math.round(v)), height: 170 })}</div>
      <div>${doms.length ? table("an-devdom-t", [{ k: "domain", label: "Domain" }, { k: "queries", label: "Queries", num: 1 }, { k: "blocked", label: "Blocked", num: 1 }, { k: "tracker", label: "Tracker", fmt: r => esc(r.tracker || "") }], doms) : `<div class="empty">Per-domain detail is kept for ${S.an.st.config.raw_days} days.</div>`}</div></div>`);
  } catch (e) { fail("an-devdetail", e); }
}
async function loadMacDetail() {
  const mac = S.an.mac, name = S.model?.knownByMac?.[mac]?.label || S.model?.clients?.find(c => c.mac === mac)?.label || mac;
  try {
    const tl = await aq("traffic_client_timeline", { mac });
    fill("an-macdetail", `<div class="head" style="margin:0 0 8px"><h2 style="margin:0">${esc(name)} <span class="muted mono" style="font-weight:400">${esc(mac)}</span></h2><button class="btn sm" data-anclear="mac">Close</button></div>
      ${lineChart([{ name: "Download", color: "var(--c2)", fill: true, points: series(tl, "download") }, { name: "Upload", color: "var(--c3)", points: series(tl, "upload") }], { fmt: fmtBytes, height: 170 })}`);
  } catch (e) { fail("an-macdetail", e); }
}
async function loadLog() {
  const el = $("#an-log"); if (!el) return;
  const k = S.an.log;
  try {
    if (k === "dns") {
      const r = await aq("dns_log", { q: S.an.q, status: S.an.status, device: S.an.device });
      el.innerHTML = table("an-log-t", [{ k: "t", label: "Time", fmt: x => `<span style="white-space:nowrap">${fmtTime(x.t)}</span>`, sort: x => x.t }, { k: "device", label: "Device" }, { k: "domain", label: "Domain", fmt: x => `<span class="mono">${esc(x.domain)}</span>` },
        { k: "status", label: "Status", fmt: x => `<span class="tag ${x.status === "blocked" ? "bad" : x.status === "allowed" ? "good" : ""}">${esc(x.status)}</span>` }, { k: "reasons", label: "Reason" }, { k: "profile_id", label: "Profile", fmt: x => `<span class="mono">${esc(x.profile_id)}</span>` }], r, { empty: "No matching queries" });
    } else if (k === "syslog") {
      const r = await aq("syslog_latest", { q: S.an.q });
      el.innerHTML = table("an-log-t", [{ k: "t", label: "Time", fmt: x => `<span style="white-space:nowrap">${fmtTime(x.t)}</span>`, sort: x => x.t }, { k: "kind", label: "Type" }, { k: "what", label: "Event / rule", fmt: x => `<span class="mono">${esc(x.what || "")}</span>` },
        { k: "action", label: "Action", fmt: x => x.action ? `<span class="tag ${x.action === "blocked" ? "bad" : "good"}">${esc(x.action)}</span>` : "" }, { k: "src", label: "From", fmt: x => `<span class="mono">${esc(x.src || "")}</span>` },
        { k: "dst", label: "To", fmt: x => `<span class="mono">${esc(x.dst || "")}${x.dport ? ":" + x.dport : ""}</span>` }, { k: "msg", label: "Message", fmt: x => `<span class="fine">${esc((x.msg || "").slice(0, 160))}</span>` }], r, { empty: "No syslog received yet" });
    } else {
      const r = await aq("events", { q: S.an.q });
      el.innerHTML = table("an-log-t", [{ k: "t", label: "Time", fmt: x => `<span style="white-space:nowrap">${fmtTime(x.t)}</span>`, sort: x => x.t }, { k: "msg", label: "Event" }, { k: "key", label: "Type" }, { k: "subsystem", label: "Area" }, { k: "source", label: "Source" }], r, { empty: "No events" });
    }
  } catch (e) { el.innerHTML = `<div class="empty bad-t">${esc(e.message)}</div>`; }
}

let SQL_LAST = null;
async function runSql() {
  const out = $("#an-sql-out"), q = $("#an-sql").value; S.an.sql = q;
  out.innerHTML = `<div class="empty">Running…</div>`;
  try {
    const r = await api("/api/analytics/sql", { sql: q }); SQL_LAST = r;
    const rows = r.rows.map(row => Object.fromEntries(r.columns.map((c, i) => [c, row[i]])));
    out.innerHTML = `<p class="fine">${fmtNum(r.rows.length)} rows${r.truncated ? " (first 5,000)" : ""} · ${r.ms} ms</p>` + table("an-sql-t", r.columns.map(c => ({ k: c, label: c, fmt: x => esc(x[c] === null ? "NULL" : typeof x[c] === "object" ? JSON.stringify(x[c]) : x[c]) })), rows, { empty: "No rows" });
  } catch (e) { out.innerHTML = `<div class="empty bad-t">${esc(e.message)}</div>`; }
}
function sqlCsv() {
  if (!SQL_LAST) return toast("Run a query first", "err");
  const q = v => v === null || v === undefined ? "" : /[",\n]/.test(String(v)) ? `"${String(v).replace(/"/g, '""')}"` : String(v);
  download(`homeowl-query-${new Date().toISOString().slice(0, 10)}.csv`, [SQL_LAST.columns.map(q).join(","), ...SQL_LAST.rows.map(r => r.map(q).join(","))].join("\n"), "text/csv");
}

// ---- events
document.addEventListener("click", e => {
  const g = (sel) => e.target.closest(sel);
  let x;
  if ((x = g("[data-anrange]"))) { S.an.range = x.dataset.anrange; return loadAnalytics(); }
  if ((x = g("[data-antab]"))) { S.an.tab = x.dataset.antab; return loadAnalytics(); }
  if ((x = g("[data-andev]"))) { e.preventDefault(); S.an.device = x.dataset.andev; S.an.tab = "devices"; window.scrollTo(0, 0); return loadAnalytics(); }
  if ((x = g("[data-anmac]"))) { e.preventDefault(); S.an.mac = x.dataset.anmac; S.an.tab = "traffic"; window.scrollTo(0, 0); return loadAnalytics(); }
  if ((x = g("[data-anclear]"))) { e.preventDefault(); S.an[x.dataset.anclear] = ""; return loadAnalytics(); }
  if ((x = g("[data-anlogdev]"))) { S.an.device = x.dataset.anlogdev; S.an.tab = "logs"; S.an.log = "dns"; return loadAnalytics(); }
  if ((x = g("[data-anlog]"))) { S.an.log = x.dataset.anlog; return loadAnalytics(); }
  if (e.target.id === "an-sql-run") runSql();
  if (e.target.id === "an-sql-csv") sqlCsv();
});
document.addEventListener("change", e => {
  if (e.target.id === "an-profile") { S.an.profile = e.target.value; loadAnalytics(); }
  if (e.target.id === "an-status") { S.an.status = e.target.value; loadLog(); }
  if (e.target.id === "an-sql-ex" && e.target.value !== "") { $("#an-sql").value = SQL_EXAMPLES[+e.target.value][1]; }
});
let AN_T = null;
document.addEventListener("input", e => { if (e.target.id === "an-q") { S.an.q = e.target.value; clearTimeout(AN_T); AN_T = setTimeout(loadLog, 300); } });
document.addEventListener("keydown", e => { if (e.target.id === "an-sql" && e.key === "Enter" && (e.metaKey || e.ctrlKey)) { e.preventDefault(); runSql(); } });

const _renderAn = render;
render = function () { _renderAn(); if (S.view === "analytics") loadAnalytics(); };

// ------------------------------------------------------------------ Settings → Analytics & logs
async function loadAnSettings() {
  const el = $("#an-settings"); if (!el) return;
  let st; try { st = await api("/api/analytics/status"); } catch (e) { el.innerHTML = `<div class="card bad-t">${esc(e.message)}</div>`; return; }
  const c = st.config || {}, ins = st.install || {}, sys = st.syslog || {}, profs = st.profiles || {};
  const chosen = new Set(c.nextdns_profiles || []), targets = new Set(st.targets || []), ndErr = st.nextdns_errors || {};
  const pinfo = st.profile_info || [], srcs = Object.fromEntries((st.sources || []).map(x => [x.source, x]));
  const accts = [...(c.nextdns_accounts || [])];
  if (st.nextdns_key && !accts.some(a => a.slot === "nextdns-api-key") && pinfo.some(p => p.account === "nextdns-api-key")) accts.unshift({ slot: "nextdns-api-key", label: "NextDNS account 1" });
  const lan = st.lan_ip || "<this computer's LAN IP>";
  el.innerHTML = `<div class="card"><h2>Analytics &amp; logs</h2>
    <p class="muted">Imports NextDNS query logs, UniFi traffic, sessions, events and threats, and the gateway's syslog into HomeOwl's built-in DuckDB database. Raw queries and logs are kept ${esc(c.raw_days)} days; hourly and daily summaries are kept forever.</p>
    <div class="grid g2">
      <div>
        <h3>1 · Engine</h3>
        ${st.engine ? `<p><span class="tag good">DuckDB ${esc(st.engine_version || "")} installed</span></p>`
          : `<p><span class="tag bad">Not installed</span> HomeOwl needs DuckDB (~30 MB, downloaded once into its own folder).</p>
             <button class="btn primary" id="an-install" ${ins.state === "running" ? "disabled" : ""}>${ins.state === "running" ? "Installing…" : "Install DuckDB"}</button>`}
        ${ins.state && ins.state !== "idle" && ins.state !== "done" ? `<pre class="json" style="max-height:160px;margin-top:8px">${esc(ins.log || "")}</pre>` : ""}
        <label class="check" style="margin-top:12px"><input type="checkbox" id="an-enabled" ${c.enabled ? "checked" : ""} ${st.engine ? "" : "disabled"}> <span>Collect analytics</span></label>
        <label>Keep raw queries &amp; logs (days)<input id="an-days" type="number" min="7" max="3650" value="${esc(c.raw_days)}"></label>
        <label>Time zone for hour-of-day charts<input id="an-tz" value="${esc(c.timezone || "")}" placeholder="This computer's time zone (${esc(Intl.DateTimeFormat().resolvedOptions().timeZone)})"></label>

        <h3 style="margin-top:14px">2 · NextDNS</h3>
        <p class="fine">Each NextDNS account has its own API key (my.nextdns.io → Account → API, bottom of the page). Profiles in another account — e.g. one a family member set up — need that account's key too. Keys are stored in the system keychain.</p>
        ${accts.length ? `<div class="nd-accts">${accts.map(a => `<div class="nd-acct"><div><b>${esc(a.label || a.slot)}</b> <span class="fine muted">${plural(pinfo.filter(p => p.account === a.slot).length, "profile")}</span>
            ${ndErr[a.slot] ? `<div class="fine bad-t">${esc(ndErr[a.slot])}</div>` : ""}</div>
            <div class="btnrow"><button class="btn sm" data-ndreplace="${esc(a.slot)}">Replace key</button><button class="btn sm" data-ndremove="${esc(a.slot)}">Remove</button></div></div>`).join("")}</div>` : ""}
        <div class="ovr" style="grid-template-columns:1fr auto"><input id="an-ndkey" type="password" placeholder="${accts.length ? "Paste the API key of another NextDNS account" : "Paste NextDNS API key"}"><button class="btn sm" id="an-ndkey-save">${accts.length ? "Add account" : "Save key"}</button></div>
        ${!accts.length ? `<div class="banner info" style="margin:6px 0">No NextDNS data yet: paste your NextDNS API key and click <b>Save key</b>. All its profiles are imported (90 days of history).</div>` : ""}
        ${accts.length ? `<label class="check"><input type="checkbox" id="an-ndall" ${c.nextdns_all !== false ? "checked" : ""}> <span>Import <b>all</b> profiles of these accounts, including ones added later</span></label>
          <p class="fine"><a href="#" id="an-nd-load">Re-check accounts &amp; profiles</a></p>
          <div class="tbl-wrap"><table class="nd-prof"><thead><tr><th></th><th>Profile</th><th>Account</th><th>NextDNS logging</th><th class="num">Imported</th><th>Status</th></tr></thead><tbody>
          ${pinfo.map(p => { const src = srcs[`nextdns:${p.id}`] || {}, acc = accts.find(a => a.slot === p.account), on = targets.has(p.id);
            return `<tr><td><input type="checkbox" data-anprof="${esc(p.id)}" ${c.nextdns_all !== false ? (p.account ? "checked disabled" : "disabled") : chosen.has(p.id) ? "checked" : ""}></td>
              <td>${esc(p.name)} <span class="mono muted fine">${esc(p.id)}</span></td><td class="fine">${acc ? esc(acc.label) : `<span class="bad-t">no key for this profile</span>`}</td>
              <td>${p.logs === "off" ? `<span class="tag bad">off</span>` : p.logs ? `<span class="tag good">${esc(p.logs)}</span>` : `<span class="muted">?</span>`}</td>
              <td class="num">${src.rows !== undefined ? fmtNum(src.rows) : "–"}</td>
              <td class="fine">${!on ? `<span class="muted">not imported</span>` : src.ok === false ? `<span class="tag bad" title="${esc(src.error || "")}">error</span> <span class="fine">${esc((src.error || "").slice(0, 90))}</span>` : p.logs === "off" ? `<span class="tag warn" title="${esc(src.note || "")}">turn on Logs in NextDNS</span>` : src.last_run ? `<span class="tag good" title="${esc(src.note || "")}">${src.note ? esc(src.note.slice(0, 40)) : "ok"}</span>` : `<span class="tag">waiting</span>`}</td></tr>`; }).join("") || `<tr><td colspan="6" class="muted">No profiles found yet — click “Re-check accounts &amp; profiles”.</td></tr>`}
          </tbody></table></div>` : ""}
        <label class="check"><input type="checkbox" id="an-ndraw" ${c.nextdns_raw ? "checked" : ""}> <span>Import every query type (bigger; default matches the NextDNS dashboard)</span></label>
      </div>
      <div>
        <h3>3 · UniFi syslog (firewall &amp; security events)</h3>
        <label class="check"><input type="checkbox" id="an-sys" ${c.syslog_enabled ? "checked" : ""}> <span>Receive syslog from the gateway</span></label>
        <div class="grid g2"><label>UDP port<input id="an-sysport" type="number" min="1024" max="65535" value="${esc(c.syslog_port)}"></label>
          <label>Accept only from (gateway IPs)<input id="an-syssrc" value="${esc((c.syslog_sources || []).join(", "))}" placeholder="e.g. 192.168.1.1 — empty = anyone on the LAN"></label></div>
        <p>${sys.listening ? `<span class="tag good">listening on UDP ${esc(sys.port)}</span> ${fmtNum(sys.accepted)} lines received${sys.dropped ? `, ${fmtNum(sys.dropped)} ignored (not from an allowed IP)` : ""}${sys.last ? ` · last ${ago(sys.last)}` : ""}` : sys.error ? `<span class="tag bad">${esc(sys.error)}</span>` : `<span class="tag">off</span>`}</p>
        <ol class="dx fine">
          <li>UniFi → <b>Settings → CyberSecure → Traffic Logging</b> → Activity Logging (Syslog) → <b>SIEM Server</b>: <span class="mono">${esc(lan)}</span>, port <span class="mono">${esc(c.syslog_port)}</span>.</li>
          <li>UniFi → <b>Settings → Control Plane → Integrations</b> → Activity Logging (Syslog) → <b>SIEM Server</b>: same address and port.</li>
          <li>Optional — to log individual firewall rules: open the firewall rule you care about (e.g. a Block rule: <b>Settings → Policy Engine → Policy Table</b> on newer versions, <b>Settings → Security → Firewall</b> on older ones), edit it and switch on <b>Syslog Logging</b> at the bottom of the rule. Rules without it still count in the totals UniFi sends.</li>
          <li>If this computer has a firewall, allow incoming UDP on port ${esc(c.syslog_port)} (macOS asks once: click <b>Allow</b>).</li>
        </ol>
      </div>
    </div>
    <button class="btn primary" id="an-save">Save analytics settings</button>
    ${st.open ? `<h3 style="margin-top:18px">Imports</h3>
    <div class="tbl-wrap"><table><thead><tr><th>Source</th><th>Last run</th><th class="num">Rows imported</th><th>Status</th></tr></thead><tbody>
      ${(st.sources || []).map(s => `<tr><td class="mono">${esc(s.source)}</td><td>${s.last_run ? ago(s.last_run) : "–"}</td><td class="num">${fmtNum(s.rows)}</td><td>${s.ok ? `<span class="tag good">ok</span>` : `<span class="tag bad">error</span>`} <span class="fine">${esc(s.error || s.note || "")}</span></td></tr>`).join("") || `<tr><td colspan="4" class="muted">Nothing imported yet.</td></tr>`}
    </tbody></table></div>
    <div class="btnrow" style="margin-top:8px"><button class="btn sm" data-ansync="all">Sync everything now</button><button class="btn sm" data-ansync="nextdns">NextDNS</button><button class="btn sm" data-ansync="unifi">UniFi</button><button class="btn sm" data-ansync="maintenance">Clean up old logs</button>
      <span class="fine muted" style="align-self:center">Database: ${fmtBytes(st.db_bytes)} · ${fmtNum(st.counts?.dns_queries)} DNS queries · ${fmtNum(st.counts?.syslog)} syslog lines</span></div>` : ""}
  </div>`;
  if (ins.state === "running") setTimeout(loadAnSettings, 2500);
}
document.addEventListener("click", async e => {
  const id = e.target.id, sync = e.target.closest("[data-ansync]");
  const post = async (path, body, ok) => { try { const r = await api(path, body); if (ok) toast(ok, "ok"); loadAnSettings(); return r; } catch (err) { toast(err.message, "err"); loadAnSettings(); } };
  if (id === "an-install") { e.target.disabled = true; e.target.textContent = "Installing…"; post("/api/analytics/install", {}, "Installing DuckDB — this takes a minute"); }
  if (id === "an-ndkey-save") { const v = $("#an-ndkey").value.trim(); if (!v) return toast("Paste the key first", "err"); e.target.disabled = true;
    await post("/api/analytics/settings", { nextdns_key: v, nextdns_slot: S.ndReplace || "" }, "NextDNS key checked and saved — importing its profiles"); S.ndReplace = null; }
  if (id === "an-nd-load") { e.preventDefault(); post("/api/analytics/profiles", {}, "Accounts and profiles re-checked"); }
  const rep = e.target.closest("[data-ndreplace]"), rem = e.target.closest("[data-ndremove]");
  if (rep) { S.ndReplace = rep.dataset.ndreplace; const i = $("#an-ndkey"); i.placeholder = "Paste the new key for this account"; i.focus(); $("#an-ndkey-save").textContent = "Save key"; }
  if (rem) openModal({ title: "Remove this NextDNS account?", body: "<p>Its key is deleted from the keychain and its profiles stop importing. Queries already imported stay.</p>",
    buttons: [{ label: "Cancel" }, { label: "Remove", cls: "primary", onClick: () => { closeModal(); post("/api/analytics/settings", { nextdns_remove: rem.dataset.ndremove }, "Account removed"); } }] });
  if (id === "an-save") {
    const list = s => s.split(/[\s,]+/).map(x => x.trim()).filter(Boolean);
    post("/api/analytics/settings", { enabled: $("#an-enabled").checked, raw_days: +$("#an-days").value || 90, nextdns_raw: $("#an-ndraw").checked,
      nextdns_all: $("#an-ndall") ? $("#an-ndall").checked : true,
      ...($("#an-ndall") && !$("#an-ndall").checked ? { nextdns_profiles: $$("[data-anprof]").filter(x => x.checked).map(x => x.dataset.anprof) } : {}), syslog_enabled: $("#an-sys").checked,
      syslog_port: +$("#an-sysport").value || 5514, syslog_sources: list($("#an-syssrc").value), timezone: $("#an-tz").value.trim() }, "Analytics settings saved");
  }
  if (sync) post("/api/analytics/sync", { source: sync.dataset.ansync }, "Sync started — refresh in a minute");
});
const _renderSet = render;
render = function () { _renderSet(); if (S.view === "settings") setTimeout(loadAnSettings, 50); };
document.addEventListener("change", e => { if (e.target.id === "an-ndall") $$("[data-anprof]").forEach(x => { x.disabled = e.target.checked; if (e.target.checked) x.checked = true; }); });
