/* HomeOwl — Records › Reports: printable reports built from what HomeOwl already knows. Pick one, pick a period,
   then print it, save it as PDF (from the print dialog) or download it as a web page (or CSV for the inventory).
   Depends on core.js, app.js, analytics.js, explore.js, presence.js, alerts.js. */
"use strict";

S.rp = { type: null, days: 7, person: "" };
const REPORTS = [
  { id: "weekly", title: "Weekly summary", icon: "router", text: "One page: what needs you, the internet, devices, new arrivals, who was home and how the family used the internet." },
  { id: "health", title: "Network health", icon: "switch", text: "The configuration score, every suggestion by severity, offline devices and firmware." },
  { id: "internet", title: "Internet", icon: "gateway", text: "Each internet connection, its provider, speed, latency and availability, and recent outages." },
  { id: "inventory", title: "Device inventory", icon: "laptop", text: "Every device UniFi knows: name, owner, network, address and maker. Also as CSV.", noPeriod: true },
  { id: "categories", title: "Categories", icon: "tv", an: true, text: "What the internet was used for: each category's share, its top sites, who used it and on which devices. Click a category to drill down." },
  { id: "blocked", title: "Blocked sites", icon: "unknown", an: true, text: "What NextDNS blocked and why (ads, trackers, parental control, security), for whom, the top blocked sites and anything unusual." },
  { id: "apps", title: "Apps", icon: "tablet", an: true, text: "Which apps were used, by whom, on which devices and on how many days; what stands out, and the apps you block. Click an app to drill down." },
  { id: "family", title: "Family & usage", icon: "phone", text: "Per person and group: time online, top categories and apps, blocked queries, data used and time at home." },
  { id: "security", title: "Security", icon: "camera", text: "Exposure findings, threats the gateway stopped, firewall blocks and the most blocked domains." },
  { id: "changes", title: "Changes & alerts", icon: "iot", text: "What HomeOwl changed on the console, and every alert, in the period." },
];

V.reports = () => {
  if (S.rp.type) return `<div class="rp-bar no-print"><button class="btn" id="rp-back">‹ All reports</button>
      ${REPORTS.find(r => r.id === S.rp.type)?.noPeriod ? "" : `<div class="seg">${(REPORTS.find(r => r.id === S.rp.type)?.an ? [[1, "24 h"], [7, "7 days"], [30, "30 days"], [90, "90 days"]] : [[7, "7 days"], [30, "30 days"]]).map(([d, l]) => `<button data-rpdays="${d}" class="${S.rp.days === d ? "on" : ""}">${l}</button>`).join("")}</div>`}
      ${REPORTS.find(r => r.id === S.rp.type)?.an && S.rp.people?.length ? `<select id="rp-person" aria-label="Whose usage"><option value="">Everyone</option>${S.rp.people.map(p => `<option value="${esc(p.id)}" ${S.rp.person === p.id ? "selected" : ""}>${esc(p.name)}</option>`).join("")}</select>` : ""}
      ${REPORTS.find(r => r.id === S.rp.type)?.an ? `<button class="btn" id="rp-expand">Expand all</button>` : ""}
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
const rpRange = () => ({ 1: "24h", 7: "7d", 30: "30d", 90: "90d" })[S.rp.days] || (S.rp.days <= 7 ? "7d" : "30d");
const rpWhen = t => new Date(t).toLocaleString([], { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });
const rpLive = () => S.source === "live";
async function rpTry(f, fallback = null) { try { return await f(); } catch { return fallback; } }
async function rpAq(name) { const r = await api("/api/analytics/query", { name, params: { range: rpRange() } }); return r.rows.map(row => Object.fromEntries(r.columns.map((c, i) => [c, row[i]]))); }
async function rpAnalyticsOn() { return rpLive() && (await rpTry(() => api("/api/analytics/status"), {}))?.open; }
async function rpAlerts() { const r = alLive() ? await rpTry(() => api("/api/alerts"), { items: [] }) : alFromBundle(); return arr(r.items).filter(a => a.time >= rpSince()); }
async function rpChanges() { return rpLive() ? arr(await rpTry(() => api("/api/changes"), [])).filter(c => Date.parse(c.time) >= rpSince()).reverse() : []; }
async function rpPresence() { return rpLive() ? await rpTry(() => api(`/api/presence?days=${S.rp.days}`), null) : prFromBundle(); }

// ---- Explore-based reports (categories, blocked): every query goes through the same cross-filtered Explore API
const rpEx = (views, filters = {}, page = {}) => api("/api/analytics/explore", { range: rpRange(), views, page, filters: { ...(S.rp.person ? { person: S.rp.person } : {}), ...filters } });
const rpWho = () => S.rp.person ? ` for ${S.rp.people?.find(p => p.id === S.rp.person)?.name || "this person"}` : "";
const rpShareOf = (a, b) => b ? `${Math.round(100 * a / b)}%` : "–";
const rpPct = (a, b) => b ? 100 * a / b : 0;
const rpNeedsAn = () => rpSec("Not available", `<p>This report needs Analytics with NextDNS logs (Settings › Analytics).</p>`);
const rpExLink = f => `<a href="#" class="fine no-print" data-exgo="${esc(f)}">Explore ›</a>`;
// one bar split into the biggest parts, with a legend
function rpShare(items, total, color) {
  const top = items.slice(0, 8), rest = total - top.reduce((a, i) => a + (i.total || 0), 0);
  const parts = [...top.map(i => ({ k: i.key, v: i.total, c: color(i) })), ...(rest > 0 ? [{ k: "Other", v: rest, c: "var(--line)" }] : [])];
  return `<div class="rp-share">${parts.map(p => `<i style="width:${rpPct(p.v, total)}%;background:${p.c}" title="${esc(p.k)}: ${rpShareOf(p.v, total)}"></i>`).join("")}</div>
    <div class="legend">${parts.map(p => `<span><i style="background:${p.c}"></i>${esc(p.k)} ${rpShareOf(p.v, total)}</span>`).join("")}</div>`;
}
// a row that opens to show more (drill-down); printed open
const rpDrill = (head, body) => `<details class="rp-dd"><summary>${head}</summary><div class="rp-ddb">${body}</div></details>`;
const rpRowHead = (name, color, w, cells) => `<span class="rp-ddn">${color ? `<i style="background:${color}"></i>` : ""}${name}</span>
  <span class="rp-ddbar"><b style="width:${Math.max(1, w)}%;${color ? `background:${color}` : ""}"></b></span>${cells.map(c => `<span class="rp-ddc">${c}</span>`).join("")}`;
const rpMini = (title, items, label, fmtSub = i => "") => `<div><h4>${esc(title)}</h4>${items?.length ? `<ol class="rp-mini">${items.map(i => `<li><span>${label(i)}</span><span class="muted">${fmtNum(i.total)}${fmtSub(i)}</span></li>`).join("")}</ol>` : `<p class="fine muted">–</p>`}</div>`;
const rpPersonName = i => esc(i.name || (i.person_id ? S.rp.people?.find(p => p.id === i.person_id)?.name : "") || i.key);
const RP_SECURITY = /security|threat|malware|phish|crypto|newly registered|nrd|dga|typosquat|parked|ddns|dynamic dns|google safe|homograph|csam/i;
const RP_PARENTAL = /parental|porn|gambling|dating|piracy|social|gaming|video|block(ed)? (apps|categor)|recreation|bypass|safe ?search|youtube/i;
const rpReasonKind = r => RP_SECURITY.test(r) ? ["Security", "bad"] : RP_PARENTAL.test(r) ? ["Parental control", "warn"] : /denylist|blacklist|deny/i.test(r) ? ["Your denylist", ""] : ["Ads & trackers list", ""];

// ---- the reports
const RP_BUILD = {
  async categories() {
    if (!(await rpAnalyticsOn())) return rpNeedsAn();
    const base = await rpEx(["summary", "by_category", "by_person", "timeline"], {}, { category_limit: 40 });
    const s = base.summary || {}, cats = arr(base.by_category), days = s.active_days || 1;
    if (!s.total) return rpSec("Nothing in this period", `<p>No DNS queries${rpWho()} in this period.</p>`);
    const top = cats.slice(0, 15);
    const drill = await Promise.all(top.map(c => rpTry(() => rpEx(["by_domain", "by_person", "by_device"], { category: c.key }, { root_limit: 10, person_id_limit: 6, device_limit: 6 }), {})));
    const mx = Math.max(...top.map(c => c.total || 0), 1);
    const list = top.map((c, i) => {
      const d = drill[i] || {};
      return rpDrill(rpRowHead(esc(c.key), catColor(c.key), 100 * c.total / mx, [fmtNum(c.total), rpShareOf(c.total, s.total), c.active_min ? fmtMin(c.active_min / days) + "/day" : "", c.blocked ? `${rpShareOf(c.blocked, c.total)} blocked` : ""]),
        `<div class="rp-ddg">${rpMini("Top sites", arr(d.by_domain), x => esc(x.key) + (x.blocked ? ` <span class="tag bad">${rpShareOf(x.blocked, x.total)} blocked</span>` : ""))}
          ${S.rp.person ? "" : rpMini("Who", arr(d.by_person), rpPersonName, x => ` · ${rpShareOf(x.total, c.total)}`)}
          ${rpMini("Devices", arr(d.by_device), x => esc(x.key))}</div>
        <p class="fine muted">${fmtNum(c.domains)} sites · ${fmtNum(c.devices)} devices ${rpExLink(`category|${c.key}${S.rp.person ? `;;person|${S.rp.person}` : ""}`)}</p>`);
    }).join("");
    // each person's own mix
    let mix = "";
    const ppl = arr(base.by_person).filter(p => p.key !== "__none__");
    if (!S.rp.person && ppl.length > 1) {
      const per = await Promise.all(ppl.slice(0, 10).map(p => rpTry(() => api("/api/analytics/explore", { range: rpRange(), views: ["by_category"], filters: { person: p.key }, page: { category_limit: 5 } }), {})));
      mix = rpTable([{ label: "Person", fmt: p => rpPersonName(p) }, { label: "Queries", num: 1, fmt: p => fmtNum(p.total) },
        { label: "Top categories", fmt: (p) => arr(per[ppl.indexOf(p)]?.by_category).map(c => `<span class="rp-chip"><i style="background:${catColor(c.key)}"></i>${esc(c.key)} ${rpShareOf(c.total, p.total)}</span>`).join(" ") }], ppl.slice(0, 10));
    }
    const tl = arr(base.timeline);
    return rpSec("At a glance", rpKpis([["DNS queries", fmtNum(s.total), `${fmtNum(Math.round(s.total / days))} a day`], ["Categories", fmtNum(cats.length)], ["Sites", fmtNum(s.domains)],
      ["Active a day ≈", s.active_min ? fmtMin(s.active_min / days) : "–"], ["Blocked", rpShareOf(s.blocked, s.total)], ["Top category", esc(cats[0]?.key || "–"), rpShareOf(cats[0]?.total, s.total)]]))
      + rpSec("Share of queries", rpShare(cats, s.total, c => catColor(c.key)))
      + rpSec("Categories", `<p class="fine muted no-print">Click a category for its top sites, who used it and on which devices.</p><div class="rp-ddl">${list}</div>${cats.length > top.length ? `<p class="fine muted">${cats.length - top.length} smaller categories not shown.</p>` : ""}`)
      + rpSec("Each person's top categories", mix)
      + rpSec("Over time", tl.length > 1 ? lineChart([{ name: "Queries", color: "var(--c1)", fill: true, points: tl.map(r => ({ t: Date.parse(r.k.replace(" ", "T")), v: r.total })) }], { fmt: v => fmtNum(Math.round(v)), height: 160, legend: false }) : "");
  },
  async apps() {
    if (!(await rpAnalyticsOn())) return rpNeedsAn();
    const [base, blocks] = await Promise.all([rpEx(["by_app", "summary"]), isAdmin() ? rpTry(() => api("/api/appblocks"), null) : Promise.resolve(null)]);
    const list = arr(base.by_app), s = base.summary || {}, days = s.active_days || 1;
    if (!list.length) return rpSec("Nothing in this period", `<p>No known apps${rpWho()} in this period. HomeOwl recognises about 50 popular apps from NextDNS logs.</p>`);
    const top = list.slice(0, 20), mx = Math.max(...top.map(a => a.total || 0), 1);
    const drill = await Promise.all(top.map(a => rpTry(() => rpEx(["by_person", "by_device", "heatmap"], { app: a.key }, { device_limit: 6, person_id_limit: 6 }), {})));
    const cats = {}; list.forEach(a => { cats[a.category || "Other"] = (cats[a.category || "Other"] || 0) + a.total; });
    const catList = Object.entries(cats).sort((x, y) => y[1] - x[1]);
    // what stands out
    const notes = [], name = a => esc(a.name || a.key);
    list.filter(a => a.category === "Dating").forEach(a => notes.push(["warn", `<b>${name(a)}</b> was used on ${a.days} day${a.days === 1 ? "" : "s"} (${fmtNum(a.devices)} device${a.devices === 1 ? "" : "s"}).`]));
    list.filter(a => a.blocked >= 50 && a.blocked / a.total >= 0.3).slice(0, 5).forEach(a => notes.push(["", `<b>${name(a)}</b>: ${rpShareOf(a.blocked, a.total)} of its lookups were blocked. If you block it, the app keeps trying; if you don't, a NextDNS list is catching it.`]));
    top.forEach((a, i) => { const late = arr(drill[i]?.heatmap?.cells).filter(c => c.hour < 5).reduce((t, c) => t + (c.n || 0), 0);
      if (late >= 30 && late / a.total >= 0.1) notes.push(["warn", `<b>${name(a)}</b> was used late at night: ${rpShareOf(late, a.total)} of its lookups were between midnight and 5 am.`]); });
    const rows = top.map((a, i) => {
      const d = drill[i] || {};
      const head = rpRowHead(name(a) + ` <span class="fine muted">${esc(a.category || "")}</span>`, "var(--c2)", 100 * a.total / mx,
        [`${a.days} day${a.days === 1 ? "" : "s"}`, a.active_min ? fmtMin(a.active_min / days) + "/day" : `${fmtNum(a.total)} lookups`, a.blocked ? `${rpShareOf(a.blocked, a.total)} blocked` : ""]);
      return rpDrill(head, `<div class="rp-ddg">${S.rp.person ? "" : rpMini("Who", arr(d.by_person), rpPersonName)}${rpMini("Devices", arr(d.by_device), x => esc(x.key))}</div>
        <p class="fine muted">Last seen ${a.last ? esc(rpWhen(Date.parse(a.last) || a.last)) : "–"} ${rpExLink(`app|${a.key}${S.rp.person ? `;;person|${S.rp.person}` : ""}`)}</p>`);
    }).join("");
    const bl = arr(blocks?.blocks);
    return rpSec("At a glance", rpKpis([["Apps seen", fmtNum(list.length)], ["Most used", name(list[0]), `${list[0].days} day${list[0].days === 1 ? "" : "s"}`],
        ["Top kind", esc(catList[0][0]), rpShareOf(catList[0][1], list.reduce((t, a) => t + a.total, 0))], ["Blocked apps", blocks ? fmtNum(bl.length) : "–"]]))
      + rpSec("What stands out", notes.length ? `<ul class="rp-notes">${notes.map(([c, t]) => `<li class="${c}">${t}</li>`).join("")}</ul>` : "")
      + rpSec("Apps", `<p class="fine muted no-print">Click an app for who used it and on which devices. Apps show up when they're used or update themselves, not when they're only installed.</p><div class="rp-ddl">${rows}</div>${list.length > top.length ? `<p class="fine muted">${list.length - top.length} less used apps not shown.</p>` : ""}`)
      + rpSec("By kind", rpShare(catList.map(([k, v]) => ({ key: k, total: v })), list.reduce((t, a) => t + a.total, 0), c => catColor(c.key)))
      + rpSec("Apps you block", bl.length ? rpTable([{ label: "App", k: "name" }, { label: "When", fmt: b => esc(b.mode === "schedule" ? b.when : "Always") },
          { label: "For", fmt: b => esc(b.profiles.map(id => { const p = arr(blocks.profiles).find(x => x.id === id); return p ? (p.people.length ? p.people.join(", ") : p.name) : id; }).join(" · ")) },
          { label: "Now", fmt: b => b.error ? `<span class="tag bad">needs attention</span>` : b.blocked_now ? "blocked" : "allowed" }], bl) : "");
  },
  async blocked() {
    if (!(await rpAnalyticsOn())) return rpNeedsAn();
    const [base, blk, reasons] = await Promise.all([
      rpEx(["summary", "timeline", "by_person", "by_device"], {}, { device_limit: 60 }),
      rpEx(["summary", "by_category", "by_domain", "by_device"], { status: "blocked" }, { root_limit: 25, device_limit: 15, category_limit: 15 }),
      S.rp.person ? Promise.resolve([]) : rpTry(() => rpAq("dns_reasons"), [])]);
    const s = base.summary || {}, b = blk.summary || {};
    if (!s.total) return rpSec("Nothing in this period", `<p>No DNS queries${rpWho()} in this period.</p>`);
    if (!b.total) return rpSec("At a glance", rpKpis([["DNS queries", fmtNum(s.total)], ["Blocked", "0"]])) + `<p>Nothing was blocked${rpWho()} in this period.</p>`;
    const doms = arr(blk.by_domain), cats = arr(blk.by_category);
    // what stands out
    const notes = [];
    const secR = reasons.filter(r => rpReasonKind(r.reason)[0] === "Security"), secN = secR.reduce((a, r) => a + r.blocked, 0);
    if (secN) notes.push(["bad", `<b>${fmtNum(secN)} security blocks</b> (${secR.map(r => esc(r.reason)).join(", ")}). NextDNS stopped something risky; check which device asked for it. ${rpExLink(`status|blocked${S.rp.person ? `;;person|${S.rp.person}` : ""}`)}`]);
    arr(base.by_device).filter(d => d.total >= 200 && d.blocked >= 100 && d.blocked / d.total >= 0.4).slice(0, 5)
      .forEach(d => notes.push(["warn", `<b>${esc(d.key)}</b>: ${rpShareOf(d.blocked, d.total)} of its queries were blocked (${fmtNum(d.blocked)}). An app may keep retrying a blocked tracker, or something on it misbehaves — worth a look.`]));
    const ads = cats.find(c => /ads|tracker/i.test(c.key));
    if (ads && ads.total / b.total >= 0.5) notes.push(["", `Most blocks (${rpShareOf(ads.total, b.total)}) are ads and trackers — that's NextDNS doing its job.`]);
    const tl = arr(base.timeline), peak = tl.reduce((m, r) => r.blocked > (m?.blocked || 0) ? r : m, null);
    if (peak && tl.length > 3 && peak.blocked > 3 * (b.total / tl.length)) notes.push(["", `The busiest ${base.bucket === "hour" ? "hour" : base.bucket === "week" ? "week" : "day"} was ${esc(new Date(peak.k.replace(" ", "T")).toLocaleString([], base.bucket === "hour" ? { weekday: "short", hour: "2-digit", minute: "2-digit" } : { weekday: "long", day: "numeric", month: "short" }))} with ${fmtNum(peak.blocked)} blocks, over three times the usual.`]);
    const mxd = Math.max(...doms.map(d => d.total || 0), 1);
    const top = doms.slice(0, 15);
    const drill = await Promise.all(top.map(d => rpTry(() => rpEx(["by_device", "by_person"], { status: "blocked", root: d.key }, { device_limit: 6, person_id_limit: 6 }), {})));
    const domList = doms.map((d, i) => {
      const head = rpRowHead(esc(d.key) + (d.category ? ` <span class="fine muted">${esc(d.category)}</span>` : ""), "var(--high)", 100 * d.total / mxd, [fmtNum(d.total), `${fmtNum(d.devices)} device${d.devices === 1 ? "" : "s"}`]);
      if (i >= top.length) return `<div class="rp-dd flat"><div class="rp-sum">${head}</div></div>`;
      const x = drill[i] || {};
      return rpDrill(head, `<div class="rp-ddg">${rpMini("Devices", arr(x.by_device), y => esc(y.key))}${S.rp.person ? "" : rpMini("Who", arr(x.by_person), rpPersonName)}</div>
        <p class="fine muted">${d.tracker ? `Tracker: ${esc(d.tracker)} · ` : ""}${rpExLink(`root|${d.key};;status|blocked${S.rp.person ? `;;person|${S.rp.person}` : ""}`)}</p>`);
    }).join("");
    const rsum = reasons.reduce((a, r) => a + r.blocked, 0), rmx = Math.max(...reasons.map(r => r.blocked), 1);
    return rpSec("At a glance", rpKpis([["Blocked", fmtNum(b.total), `${rpShareOf(b.total, s.total)} of all queries`], ["Blocked a day", fmtNum(Math.round(b.total / (s.active_days || 1)))],
      ["Sites blocked", fmtNum(b.domains)], ["Devices affected", `${fmtNum(b.devices)} of ${fmtNum(s.devices)}`], ["Security blocks", reasons.length ? fmtNum(secN) : "–"]]))
      + rpSec("What stands out", notes.length ? `<ul class="rp-notes">${notes.map(([c, t]) => `<li class="${c}">${t}</li>`).join("")}</ul>` : "")
      + rpSec("Why it was blocked", reasons.length ? `<div class="rp-ddl">${reasons.map(r => { const [k, c] = rpReasonKind(r.reason);
          return `<div class="rp-dd flat"><div class="rp-sum">${rpRowHead(`${esc(r.reason)} <span class="tag ${c}">${k}</span>`, c === "bad" ? "var(--crit)" : c === "warn" ? "var(--med)" : "var(--c1)", 100 * r.blocked / rmx, [fmtNum(r.blocked), rpShareOf(r.blocked, rsum)])}</div></div>`; }).join("")}</div>
          <p class="fine muted">A query can match more than one list, so these add up to more than the blocked total.</p>` : "")
      + rpSec("Blocked by category", rpShare(cats, b.total, c => catColor(c.key)))
      + rpSec("Top blocked sites", `<p class="fine muted no-print">Click a site to see which devices and people asked for it.</p><div class="rp-ddl">${domList}</div>`)
      + rpSec("Who gets blocked", S.rp.person ? "" : rpTable([{ label: "Person", fmt: rpPersonName }, { label: "Blocked", num: 1, fmt: p => fmtNum(p.blocked) }, { label: "Of their queries", num: 1, fmt: p => rpShareOf(p.blocked, p.total) }],
          arr(base.by_person).filter(p => p.blocked).sort((x, y) => y.blocked - x.blocked)))
      + rpSec("Devices with the most blocks", rpTable([{ label: "Device", k: "key" }, { label: "Blocked", num: 1, fmt: d => fmtNum(d.total) }, { label: "Sites", num: 1, fmt: d => fmtNum(d.domains) }], arr(blk.by_device)))
      + rpSec("Over time", tl.length > 1 ? lineChart([{ name: "Blocked", color: "var(--high)", fill: true, points: tl.map(r => ({ t: Date.parse(r.k.replace(" ", "T")), v: r.blocked })) }], { fmt: v => fmtNum(Math.round(v)), height: 160, legend: false }) : "");
  },
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
      const r = await rpTry(() => api("/api/analytics/explore", { range: rpRange(), filters: { person: p.id }, views: ["summary", "by_category", "traffic", "by_app"], raw: S.ex.raw }), {});
      const s = r.summary || {}, days = s.active_days || 1, tr = r.traffic?.summary || {};
      const home = pr && arr(pr.people).find(x => x.id === p.id);
      const homeMs = home ? prSpans(home, arr(pr.events), Math.max(rpSince(), Math.min(...arr(pr.events).map(e => e.t), Date.now())), Date.now()).reduce((a, [x, y]) => a + y - x, 0) : 0;
      return `<div class="rp-person"><h3><i style="background:${esc(p.color || "var(--c4)")}"></i>${esc(p.name)}${p.kind === "group" ? ` <span class="fine muted">group</span>` : ""}</h3>
        ${rpKpis([["Queries a day", fmtNum(Math.round((s.total || 0) / days))], ["Active a day ≈", s.active_min ? fmtMin(s.active_min / days) : "–"], ["Blocked", pct(s.blocked, s.total)],
          ["Data a day", tr.rx || tr.tx ? fmtBytes((tr.rx + tr.tx) / days) : "–"], p.kind !== "group" && pr?.history ? ["At home", prDur(homeMs)] : null])}
        ${(r.by_category || []).length ? `<p class="fine">Top: ${r.by_category.slice(0, 6).map(c => `${esc(c.key)} (${fmtNum(c.total)})`).join(" · ")}</p>` : ""}
        ${arr(r.by_app).length ? `<p class="fine">Apps: ${r.by_app.slice(0, 8).map(x => `${esc(x.name || x.key)} (${x.days} day${x.days === 1 ? "" : "s"}${x.blocked ? `, ${rpShareOf(x.blocked, x.total)} blocked` : ""})`).join(" · ")}</p>` : ""}</div>`;
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
  if (r.an && !S.rp.people && rpLive()) { S.rp.people = []; const meta = await rpTry(() => api("/api/analytics/meta", {}), null); if (meta?.people?.length) { S.rp.people = meta.people; return render(); } }
  const head = `<header class="rp-head"><div><img src="icon.png" alt="" class="logo-img"><span>HomeOwl</span></div><h1>${esc(r.title)}</h1>
    <p class="muted">${esc(S.model.info.hostname || S.model.info.site || "")}${r.noPeriod ? "" : S.rp.days === 1 ? " · last 24 hours" : ` · last ${S.rp.days} days`}${r.an ? esc(rpWho()) : ""} · made ${esc(new Date().toLocaleString([], { day: "numeric", month: "long", year: "numeric", hour: "2-digit", minute: "2-digit" }))}</p></header>`;
  try { const body = await RP_BUILD[r.id](S.model); if (S.rp.type === r.id && $("#rp-doc")) $("#rp-doc").innerHTML = head + (body || `<p class="muted">Nothing to report.</p>`); }
  catch (e) { el.innerHTML = head + `<div class="empty bad-t">${esc(e.message)}</div>`; }
}

// a stand-alone copy: the report plus the colours and styles it uses
function rpDownload() {
  const doc = $("#rp-doc"); if (!doc) return;
  const cs = getComputedStyle(document.documentElement);
  const vars = ["--bg", "--panel", "--panel2", "--text", "--muted", "--line", "--accent", "--ok", "--crit", "--high", "--med", "--low", "--info", ...Array.from({ length: 8 }, (_, i) => `--c${i + 1}`)]
    .map(v => `${v}:${cs.getPropertyValue(v).trim()}`).join(";");
  const css = [...document.styleSheets].flatMap(sh => { try { return [...sh.cssRules].map(r => r.cssText); } catch { return []; } }).filter(t => /^(\.rp-|\.report|\.sev|\.tag|\.muted|\.fine|\.mono|svg|\.gauge|\.chart|\.legend|details|summary)/.test(t)).join("\n");
  const html = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>${esc($("#rp-doc h1")?.textContent || "HomeOwl report")}</title>
<style>:root{${vars}}body{font-family:-apple-system,"Segoe UI",Roboto,sans-serif;background:var(--bg);color:var(--text);margin:0;padding:24px}${css}</style></head><body><article class="report">${doc.innerHTML.replace(/<img[^>]*>/g, "").replace(/<details class="rp-dd">/g, '<details class="rp-dd" open>').replace(/<a [^>]*class="fine no-print"[^>]*>[^<]*<\/a>/g, "").replace(/<p class="fine muted no-print">[^<]*<\/p>/g, "")}</article></body></html>`;
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
  if (e.target.id === "rp-expand") { const all = $$("#rp-doc details"), open = all.some(d => !d.open); all.forEach(d => { d.open = open; }); e.target.textContent = open ? "Collapse all" : "Expand all"; return; }
  if (e.target.id === "rp-html") { rpDownload(); return; }
  if (e.target.id === "rp-csv") rpCsv();
});
document.addEventListener("change", e => { if (e.target.id === "rp-person") { S.rp.person = e.target.value; render(); } });
// printed and downloaded reports show every drill-down open
let rpClosed = [];
window.addEventListener("beforeprint", () => { rpClosed = $$("#rp-doc details:not([open])"); rpClosed.forEach(d => { d.open = true; }); });
window.addEventListener("afterprint", () => { rpClosed.forEach(d => { d.open = false; }); rpClosed = []; });
{ const _render = render; render = function () { _render(); if (S.view === "reports" && S.rp.type) loadReport(); }; }
