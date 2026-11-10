/* HomeOwl — Explore (cross-filtered drill-down), People and Categories. Depends on core.js, app.js, analytics.js */
"use strict";

S.ex = { filters: {}, stack: [], raw: { offset: 0, limit: 100, sort: "t", dir: "desc" }, metric: "n", meta: null, last: null, catAll: false };
const DOW = ["", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];
const CAT_COLORS = {};
const catColor = c => CAT_COLORS[c] || (CAT_COLORS[c] = c === "Uncategorized" ? "var(--c7)" : c === "Ads & trackers" ? "var(--crit)" : PAL[Object.keys(CAT_COLORS).length % 6]);
const fmtMin = m => m === null || m === undefined ? "–" : m < 60 ? `${Math.round(m)} min` : `${(m / 60).toFixed(m < 600 ? 1 : 0)} h`;
const personName = id => id === "__none__" ? "Not assigned" : (S.ex.meta?.people || []).find(p => p.id === id)?.name || id;
const personColor = id => (S.ex.meta?.people || []).find(p => p.id === id)?.color || "var(--c4)";
const isAdmin = () => S.role === "admin";

async function exMeta(force) {
  if (!S.ex.meta || force) S.ex.meta = await api("/api/analytics/meta", {});
  return S.ex.meta;
}
async function explore(views, filters = S.ex.filters, extra = {}) {
  return api("/api/analytics/explore", { range: S.an.range, filters, views, raw: S.ex.raw, ...extra });
}

// ------------------------------------------------------------------ filters
function setFilter(k, v, { replace = false } = {}) {
  S.ex.stack.push(JSON.stringify(S.ex.filters));
  const f = replace ? {} : { ...S.ex.filters };
  if (v === null || v === undefined || v === "") delete f[k]; else f[k] = v;
  S.ex.filters = f; S.ex.raw.offset = 0;
}
function chipLabel(k, v) {
  const f = S.ex.filters;
  if (k === "person") return `👤 ${personName(v)}`;
  if (k === "device") return `📱 ${v}`;
  if (k === "category") return `🏷 ${v}`;
  if (k === "root") return `🌐 ${v}`;
  if (k === "domain") return `🔗 ${v}`;
  if (k === "status") return `status: ${v}`;
  if (k === "profile") return `profile: ${S.ex.meta?.profiles?.[v] || v}`;
  if (k === "hour") return `🕒 ${String(v).padStart(2, "0")}:00–${String((+v + 1) % 24).padStart(2, "0")}:00`;
  if (k === "dow") return `📅 ${DOW[v]}`;
  if (k === "q") return `search: “${v}”`;
  if (k === "lfrom") return `⏱ ${fmtLocal(v)} → ${fmtLocal(f.lto)}`;
  return `${k}: ${v}`;
}
function fmtLocal(s) {
  if (!s) return "…";
  const [d, t] = String(s).split(" "); const [y, m, dd] = d.split("-").map(Number);
  const md = new Date(y, m - 1, dd).toLocaleDateString([], { month: "short", day: "numeric" });
  return t && t !== "00:00" ? `${md} ${t}` : md;
}
function chipsBar() {
  const f = S.ex.filters, keys = Object.keys(f).filter(k => k !== "lto");
  return `<div class="ex-chips">${keys.length ? keys.map(k => `<span class="chip">${esc(chipLabel(k, f[k]))}<button data-exdel="${k}" title="Remove">×</button></span>`).join("")
      : `<span class="muted fine">Everything in this period. Click any bar, cell or row to drill down.</span>`}
    ${S.ex.stack.length ? `<button class="btn sm ghost" data-exback>↶ Back</button>` : ""}${keys.length ? `<button class="btn sm ghost" data-exclear>Clear all</button>` : ""}</div>`;
}
function filterSelects(meta) {
  const f = S.ex.filters, ppl = meta.people || [];
  return `<div class="ex-selects">
    <input id="ex-q" class="search" placeholder="Search domains, devices, IPs…" value="${esc(f.q || "")}">
    <select data-exsel="person"><option value="">Everyone</option>${ppl.map(p => `<option value="${esc(p.id)}" ${f.person === p.id ? "selected" : ""}>${esc(p.name)}</option>`).join("")}<option value="__none__" ${f.person === "__none__" ? "selected" : ""}>Not assigned</option></select>
    <select data-exsel="category"><option value="">All categories</option>${meta.categories.map(c => `<option ${f.category === c ? "selected" : ""}>${esc(c)}</option>`).join("")}</select>
    <select data-exsel="status"><option value="">Any status</option>${["default", "blocked", "allowed", "relayed", "error"].map(s => `<option ${f.status === s ? "selected" : ""}>${s}</option>`).join("")}</select>
    ${Object.keys(meta.profiles || {}).length > 1 ? `<select data-exsel="profile"><option value="">All profiles</option>${Object.entries(meta.profiles).map(([id, n]) => `<option value="${esc(id)}" ${f.profile === id ? "selected" : ""}>${esc(n)}</option>`).join("")}</select>` : ""}
  </div>`;
}

// ------------------------------------------------------------------ charts
function blist(items, key, { fmt = fmtNum, color = "var(--c1)", label = i => i.key, sub = () => "", tag = () => "", empty = "No data", max, icon } = {}) {
  if (!items || items.error) return `<div class="empty bad-t">${esc(items?.error || "No data")}</div>`;
  if (!items.length) return `<div class="empty">${esc(empty)}</div>`;
  const mx = max || Math.max(...items.map(i => i.total || 0), 1);
  return `<div class="blist">${items.map(i => {
    const c = typeof color === "function" ? color(i) : color, w = Math.max(1, 100 * (i.total || 0) / mx), bw = i.blocked ? 100 * i.blocked / mx : 0;
    return `<button class="bl-row ${S.ex.filters[key] === i.key ? "on" : ""}" data-exf="${key}|${esc(i.key)}" title="Filter by ${esc(label(i))}">
      <span class="bl-l">${icon ? icon(i) : ""}${esc(label(i))}${tag(i)}</span>
      <span class="bl-bar"><i style="width:${w}%;background:${c}"></i>${bw ? `<b style="width:${bw}%"></b>` : ""}</span>
      <span class="bl-v">${fmt(i.total)}<small>${sub(i)}</small></span></button>`;
  }).join("")}</div>`;
}
const subActive = i => i.active_min ? ` · ${fmtMin(i.active_min)}` : "";

function timeCols(rows, { val = r => r.total, val2 = r => r.blocked, fmt = fmtNum, bucket = "day", color = "var(--c1)", color2 = "var(--crit)", key = "exrange" } = {}) {
  if (!rows || rows.error) return `<div class="empty bad-t">${esc(rows?.error || "")}</div>`;
  if (!rows.length) return `<div class="empty">No data in this period</div>`;
  const W = 720, H = 190, L = 50, B = 26, T = 8, n = rows.length, bw = (W - L) / n, mx = Math.max(...rows.map(val), 1);
  const y = v => T + (H - T - B) * (1 - v / mx);
  const lab = k => { const [d, t] = k.split(" "); const [yy, m, dd] = d.split("-").map(Number); const dt = new Date(yy, m - 1, dd);
    return bucket === "hour" ? t.slice(0, 2) + "h" : bucket === "week" ? dt.toLocaleDateString([], { month: "short", day: "numeric" }) : dt.toLocaleDateString([], n > 20 ? { day: "numeric" } : { weekday: "short", day: "numeric" }); };
  let g = "";
  for (let i = 0; i <= 3; i++) { const v = mx * i / 3; g += `<line x1="${L}" x2="${W}" y1="${y(v)}" y2="${y(v)}" style="stroke:var(--line)"/><text x="${L - 6}" y="${y(v) + 4}" text-anchor="end">${esc(fmt(Math.round(v)))}</text>`; }
  const every = Math.ceil(n / 16);
  const bars = rows.map((r, i) => {
    const x = L + i * bw, v = val(r) || 0, v2 = val2 ? val2(r) || 0 : 0;
    return `<g class="tc" data-${key}="${esc(r.k)}|${esc(r.k2)}"><rect x="${x}" y="${T}" width="${bw}" height="${H - T - B}" class="hit"/>
      <rect x="${x + 1}" y="${y(v)}" width="${Math.max(1, bw - 2)}" height="${y(0) - y(v)}" rx="2" style="fill:${color}"/>
      ${v2 ? `<rect x="${x + 1}" y="${y(v2)}" width="${Math.max(1, bw - 2)}" height="${y(0) - y(v2)}" rx="2" style="fill:${color2}"/>` : ""}
      <title>${esc(fmtLocal(r.k))}${bucket === "hour" ? "" : ""}: ${esc(fmt(v))}${val2 ? ` (${esc(fmt(v2))} blocked)` : ""}${r.active_min ? ` · active ≈ ${fmtMin(r.active_min)}` : ""} — click to zoom in</title>
      ${i % every === 0 ? `<text x="${x + bw / 2}" y="${H - 8}" text-anchor="middle">${esc(lab(r.k))}</text>` : ""}</g>`;
  }).join("");
  return `<svg class="chart ex-tl" viewBox="0 0 ${W} ${H}" width="100%">${g}${bars}</svg>`;
}

function heatmap(hm, { metric = "n", fmt = fmtNum, unit = "", clickable = true, color = "var(--c1)" } = {}) {
  if (!hm || hm.error) return `<div class="empty bad-t">${esc(hm?.error || "")}</div>`;
  const cells = hm.cells || hm, wd = hm.weekdays || {};
  if (!cells.length) return `<div class="empty">No data in this period</div>`;
  const m = {}; cells.forEach(c => { m[`${c.dow}-${c.hour}`] = c; });
  const valOf = c => { if (!c) return 0; const d = Math.max(1, wd[c.dow] || 1); return (metric === "active" ? c.active_min : metric === "bytes" ? c.bytes : c.n) / d; };
  const mx = Math.max(...cells.map(valOf), 1e-9);
  const W = 720, L = 40, T = 20, cw = (W - L) / 24, ch = 24, H = T + 7 * ch + 4;
  let s = "";
  for (let h = 0; h < 24; h++) s += `<text x="${L + h * cw + cw / 2}" y="13" text-anchor="middle" ${clickable ? `class="hm-lab" data-exf="hour|${h}"` : ""}>${h % 3 === 0 ? String(h).padStart(2, "0") : "·"}</text>`;
  for (let d = 1; d <= 7; d++) {
    s += `<text x="${L - 8}" y="${T + (d - 1) * ch + 16}" text-anchor="end" ${clickable ? `class="hm-lab" data-exf="dow|${d}"` : ""}>${DOW[d]}</text>`;
    for (let h = 0; h < 24; h++) {
      const c = m[`${d}-${h}`], v = valOf(c), o = v ? 0.12 + 0.88 * Math.sqrt(v / mx) : 0;
      const sel = S.ex.filters.dow === d && S.ex.filters.hour === h;
      s += `<rect x="${L + h * cw + 1}" y="${T + (d - 1) * ch + 1}" width="${cw - 2}" height="${ch - 2}" rx="3" class="hm ${sel ? "sel" : ""}" ${clickable ? `data-exhm="${d}|${h}"` : ""}
        style="fill:${v ? color : "var(--panel2)"};fill-opacity:${v ? o.toFixed(2) : 1}"><title>${DOW[d]} ${String(h).padStart(2, "0")}:00 — ${esc(fmt(v))}${unit} per ${DOW[d]} on average${c ? ` (${fmtNum(c.n ?? c.active_hours)} total)` : ""}</title></rect>`;
    }
  }
  return `<svg class="chart ex-hm" viewBox="0 0 ${W} ${H}" width="100%">${s}</svg>`;
}
function hourProfile(hm, { metric = "n", fmt = fmtNum, color = "var(--c1)" } = {}) {
  const cells = hm?.cells || [], wd = hm?.weekdays || {}, days = Math.max(1, Object.values(wd).reduce((a, b) => a + b, 0));
  if (!cells.length) return "";
  const by = Array(24).fill(0); cells.forEach(c => { by[c.hour] += metric === "active" ? c.active_min : metric === "bytes" ? c.bytes : c.n; });
  const W = 720, H = 110, L = 40, B = 18, bw = (W - L) / 24, mx = Math.max(...by, 1);
  return `<svg class="chart ex-hp" viewBox="0 0 ${W} ${H}" width="100%">${by.map((v, h) => {
    const hh = (H - B - 6) * v / mx;
    return `<g class="tc" data-exf="hour|${h}"><rect x="${L + h * bw}" y="0" width="${bw}" height="${H - B}" class="hit"/><rect x="${L + h * bw + 2}" y="${H - B - hh}" width="${bw - 4}" height="${hh}" rx="2" style="fill:${color};${S.ex.filters.hour === h ? "" : "fill-opacity:.75"}"/>
      <title>${String(h).padStart(2, "0")}:00 — ${esc(fmt(v / days))} per day on average</title>${h % 3 === 0 ? `<text x="${L + h * bw + bw / 2}" y="${H - 4}" text-anchor="middle">${String(h).padStart(2, "0")}</text>` : ""}</g>`;
  }).join("")}<text x="${L - 6}" y="12" text-anchor="end">avg</text></svg>`;
}

// ------------------------------------------------------------------ Explore tab
function exBody() {
  return `<div id="ex-top"><div class="empty">Loading…</div></div>
    <div class="grid g5 section" id="ex-kpis"></div>
    <div class="card section"><div class="head" style="margin:0"><h2 style="margin:0">Over time <span class="muted fine" id="ex-tl-note"></span></h2><span class="fine muted">Click a bar to zoom in</span></div><div id="ex-tl"></div></div>
    <div class="card section"><div class="head" style="margin:0 0 6px"><h2 style="margin:0">When — hour of day × weekday <span class="muted fine">(local time)</span></h2>
      <div class="seg">${[["n", "Queries"], ["active", "Active time"]].map(([k, l]) => `<button data-exmetric="${k}" class="${S.ex.metric === k ? "on" : ""}">${l}</button>`).join("")}</div></div>
      <div id="ex-hm"></div><div id="ex-hp"></div><p class="fine" id="ex-hm-note"></p></div>
    <div class="grid g3 section">
      <div class="card"><h2>People</h2><div id="ex-person"></div></div>
      <div class="card"><h2>Categories</h2><div id="ex-cat"></div></div>
      <div class="card"><h2>Devices</h2><div id="ex-dev"></div></div></div>
    <div class="grid g2 section">
      <div class="card"><h2>Domains</h2><div id="ex-dom"></div></div>
      <div><div class="card"><h2>Blocked vs allowed</h2><div id="ex-status"></div></div>
           <div class="card section" id="ex-prof-card"><h2>NextDNS profiles</h2><div id="ex-prof"></div></div></div></div>
    <div class="card section" id="ex-traffic-card"><h2>Data used <span class="muted fine">(UniFi, per device)</span></h2><div id="ex-traffic"></div></div>
    <div class="card section"><div class="head" style="margin:0 0 6px"><h2 style="margin:0">Queries</h2>
      <div class="btnrow"><select id="ex-pagesize" style="width:auto">${[100, 250, 1000].map(n => `<option ${S.ex.raw.limit === n ? "selected" : ""}>${n}</option>`).join("")}</select>
      <button class="btn sm" id="ex-csv">Download CSV</button></div></div><div id="ex-raw"></div></div>`;
}

async function loadExplore() {
  let meta;
  try { meta = await exMeta(); } catch (e) { return fill("ex-top", `<div class="empty bad-t">${esc(e.message)}</div>`); }
  fill("ex-top", `<div class="card ex-bar">${filterSelects(meta)}${chipsBar()}</div>`);
  const views = ["summary", "timeline", "heatmap", "by_person", "by_category", "by_device", "by_domain", "by_status", "by_profile", "raw", "traffic"];
  let r;
  try { r = await explore(views); } catch (e) { return fill("ex-kpis", `<div class="empty bad-t">${esc(e.message)}</div>`); }
  S.ex.last = r;
  const s = r.summary || {}, days = s.active_days || 1;
  fill("ex-kpis", kpiTile("Queries", fmtNum(s.total), `${fmtNum(Math.round((s.total || 0) / days))} a day`) +
    kpiTile("Blocked", pct(s.blocked, s.total), `${fmtNum(s.blocked)} queries`) +
    kpiTile("Active time ≈", s.active_min === null || s.active_min === undefined ? "–" : fmtMin(s.active_min / days), s.active_min ? `a day${Object.keys(S.ex.filters).some(k => ["person", "device", "category", "root"].includes(k)) ? "" : " · any device"} · ${fmtMin(s.active_min)} total` : r.source === "summary" ? "only for the raw-log period" : "") +
    kpiTile("Devices", fmtNum(s.devices), `${fmtNum(s.domains)} domains`) +
    kpiTile("Days with activity", fmtNum(s.active_days), s.first ? `${typeof s.first === "number" ? fmtTime(s.first) : s.first} → ${typeof s.last === "number" ? fmtTime(s.last) : s.last}` : ""));
  fill("ex-tl-note", r.source === "summary" ? `· daily summaries (raw queries are kept ${r.raw_days} days)` : r.clipped ? `· raw queries start ${fmtTime(r.raw_from)}` : "");
  fill("ex-tl", timeCols(r.timeline, { bucket: r.bucket === "hour" || r.source === "raw" ? r.bucket : "day" }));
  const hmOpts = S.ex.metric === "active" ? { metric: "active", fmt: v => fmtMin(v), color: "var(--c4)" } : { metric: "n", fmt: v => fmtNum(Math.round(v)) };
  fill("ex-hm", heatmap(r.heatmap, hmOpts));
  fill("ex-hp", hourProfile(r.heatmap, hmOpts));
  hmNote(r);
  exRest(r);
}
function hmNote(r) {
  fill("ex-hm-note", S.ex.metric === "active" ? "Active time = 5-minute periods with at least one query, averaged per weekday. Phones and TVs also query in the background, so filter by a category (e.g. Social, Video) to see real use." :
    r.heatmap?.source === "hourly" ? "Long period: built from hourly summaries (category/domain filters need raw queries)." : "Average queries per weekday at each hour. Click a cell, an hour or a day to filter.");
}
function exRest(r) {
  fill("ex-person", blist(r.by_person, "person", { label: i => i.name || personName(i.key), color: i => i.key === "__none__" ? "var(--c7)" : personColor(i.key), sub: subActive,
    empty: "No people yet — add them in the People tab" }) + (isAdmin() ? `<p class="fine"><a href="#" data-antab="people">Manage people →</a></p>` : ""));
  fill("ex-cat", blist(r.by_category, "category", { color: i => catColor(i.key), sub: subActive }));
  fill("ex-dev", blist(r.by_device, "device", { icon: i => cliIcon({ name: i.key }, 16), color: i => i.person_id ? personColor(i.person_id) : "var(--c1)", sub: subActive,
    tag: i => i.person_id ? ` <span class="tag">${esc(personName(i.person_id))}</span>` : "" }));
  fill("ex-dom", blist(r.by_domain, "root", { color: i => catColor(i.category), sub: subActive,
    tag: i => ` <span class="tag" style="border-color:transparent">${esc(i.category || "")}</span>${i.tracker ? ` <span class="tag bad">${esc(i.tracker)}</span>` : ""}` }) +
    (isAdmin() ? `<p class="fine">Wrong category? <a href="#" data-antab="categories">Fix categories →</a></p>` : ""));
  fill("ex-status", blist(r.by_status, "status", { color: i => i.key === "blocked" ? "var(--crit)" : "var(--c2)" }));
  const profs = r.by_profile || [];
  $("#ex-prof-card") && ($("#ex-prof-card").hidden = !(profs.length > 1 || S.ex.filters.profile));
  fill("ex-prof", blist(profs, "profile", { label: i => i.name || i.key }));
  renderTraffic(r.traffic);
  renderRaw(r.raw);
}

function renderTraffic(t) {
  const card = $("#ex-traffic-card"); if (!card) return;
  if (!t || t.error) return fill("ex-traffic", `<div class="empty bad-t">${esc(t?.error || "")}</div>`);
  const s = t.summary || {};
  if (!s.rx && !s.tx) { fill("ex-traffic", `<div class="empty">No UniFi traffic matches${S.ex.filters.device ? " — this DNS device name doesn't match a UniFi client. Link it to a person (People tab) together with its UniFi client to see its data use." : "."}</div>`); return; }
  fill("ex-traffic", `${t.note ? `<p class="fine">${esc(t.note)}</p>` : ""}
    <div class="grid g3"><div class="kpi"><div class="l">Downloaded</div><div class="v">${fmtBytes(s.rx)}</div></div><div class="kpi"><div class="l">Uploaded</div><div class="v">${fmtBytes(s.tx)}</div></div><div class="kpi"><div class="l">Devices</div><div class="v">${fmtNum(s.clients)}</div></div></div>
    <div class="grid g2 section"><div>${timeCols(t.timeline, { val: r => r.rx + r.tx, val2: null, fmt: fmtBytes, color: "var(--c2)", bucket: S.ex.last?.bucket })}</div>
      <div>${heatmap({ cells: t.heatmap, weekdays: t.weekdays }, { metric: "bytes", fmt: fmtBytes, color: "var(--c2)" })}</div></div>
    <details class="section"><summary class="fine" style="cursor:pointer">Devices (${(t.clients || []).length})</summary>${table("ex-tr-t", [{ k: "client", label: "Device", fmt: r => `<a href="#" data-anmac="${esc(r.mac)}">${esc(r.client)}</a>${r.person_id ? ` <span class="tag">${esc(personName(r.person_id))}</span>` : ""}` },
      { k: "network", label: "Network" }, { k: "rx", label: "Down", num: 1, fmt: r => fmtBytes(r.rx) }, { k: "tx", label: "Up", num: 1, fmt: r => fmtBytes(r.tx) }, { k: "total", label: "Total", num: 1, fmt: r => fmtBytes(r.total) }], t.clients || [])}</details>`);
}

function renderRaw(raw) {
  if (!raw || raw.error) return fill("ex-raw", `<div class="empty bad-t">${esc(raw?.error || "")}</div>`);
  const R = S.ex.raw, items = raw.items || [];
  const th = (k, l) => `<th data-exsort="${k}" class="${R.sort === k ? "sorted" : ""}">${l}${R.sort === k ? (R.dir === "asc" ? " ▲" : " ▼") : ""}</th>`;
  const end = raw.offset + items.length;
  fill("ex-raw", `<div class="tbl-wrap"><table class="ex-raw"><thead><tr>${th("t", "Time")}${th("device", "Device")}${th("person", "Person")}${th("domain", "Domain")}${th("category", "Category")}${th("status", "Status")}<th>Reason / tracker</th><th>Profile</th></tr></thead>
    <tbody>${items.map(x => `<tr><td class="mono nowrap">${esc(x.local || fmtTime(x.t))}</td>
      <td><a href="#" data-exf="device|${esc(x.device)}">${esc(x.device)}</a></td>
      <td>${x.person_id ? `<a href="#" data-exf="person|${esc(x.person_id)}">${esc(personName(x.person_id))}</a>` : `<span class="muted">–</span>`}</td>
      <td class="mono"><a href="#" data-exf="root|${esc(x.root)}" title="All queries to ${esc(x.root)}">${esc(x.domain)}</a></td>
      <td><a href="#" data-exf="category|${esc(x.category)}" class="tag" style="border-left:3px solid ${catColor(x.category)}">${esc(x.category)}</a>${isAdmin() ? ` <a href="#" class="fine" data-excat="${esc(x.root)}" title="Change category of ${esc(x.root)}">✎</a>` : ""}</td>
      <td><a href="#" data-exf="status|${esc(x.status)}" class="tag ${x.status === "blocked" ? "bad" : x.status === "allowed" ? "good" : ""}">${esc(x.status)}</a></td>
      <td class="fine">${esc(x.reasons || "")}${x.tracker ? ` <span class="tag">${esc(x.tracker)}</span>` : ""}</td>
      <td class="mono fine">${esc(S.ex.meta?.profiles?.[x.profile_id] || x.profile_id || "")}</td></tr>`).join("") || `<tr><td colspan="8"><div class="empty">No queries match${S.ex.last?.source === "summary" ? ` (raw queries are kept ${S.ex.last.raw_days} days)` : ""}</div></td></tr>`}</tbody></table></div>
    <div class="pager"><span class="fine">${raw.total ? `${fmtNum(raw.offset + 1)}–${fmtNum(end)} of ${fmtNum(raw.total)}` : ""}</span>
      <button class="btn sm" data-expage="-1" ${raw.offset ? "" : "disabled"}>‹ Newer</button><button class="btn sm" data-expage="1" ${end < raw.total ? "" : "disabled"}>Older ›</button></div>`);
}

async function reloadRaw() {
  try { const r = await explore(["raw"]); renderRaw(r.raw); } catch (e) { fill("ex-raw", `<div class="empty bad-t">${esc(e.message)}</div>`); }
}
async function exportCsv() {
  toast("Preparing CSV…");
  try {
    const r = await api("/api/analytics/explore", { range: S.an.range, filters: S.ex.filters, views: ["raw"], raw: { ...S.ex.raw, offset: 0, limit: 10000 } });
    const cols = ["local", "device", "person", "domain", "root", "category", "status", "reasons", "tracker", "protocol", "profile_id", "local_ip", "device_model"];
    const q = v => v === null || v === undefined ? "" : /[",\n]/.test(String(v)) ? `"${String(v).replace(/"/g, '""')}"` : String(v);
    const lines = [cols.join(","), ...r.raw.items.map(x => cols.map(c => q(c === "person" ? (x.person_id ? personName(x.person_id) : "") : x[c])).join(","))];
    download(`dns-queries-${new Date().toISOString().slice(0, 10)}.csv`, lines.join("\n"), "text/csv");
    if (r.raw.total > 10000) toast(`Exported the first 10,000 of ${fmtNum(r.raw.total)} — narrow the filters for the rest`);
  } catch (e) { toast(e.message, "err"); }
}

// ------------------------------------------------------------------ People tab
function peopleBody() {
  return `<div class="head" style="margin:0 0 12px"><div class="muted">Group devices by person to see each person's online habits — what they use and when. A device can belong to one person.</div>
    ${isAdmin() ? `<button class="btn primary" id="pp-add">+ Add person</button>` : ""}</div><div id="pp-list"><div class="empty">Loading…</div></div>`;
}
async function loadPeople() {
  let meta; try { meta = await exMeta(true); } catch (e) { return fill("pp-list", `<div class="empty bad-t">${esc(e.message)}</div>`); }
  const ppl = meta.people;
  if (!ppl.length) return fill("pp-list", `<div class="card"><h2>No people yet</h2><p>Add a person (e.g. each family member, “Guests”, “TV & IoT”) and pick their devices. Then every chart can be filtered by person, and you get a usage profile per person.</p>${isAdmin() ? `<button class="btn primary" id="pp-add2">Add the first person</button>` : ""}</div>`);
  fill("pp-list", `<div class="grid g2">${ppl.map(p => `<div class="card person" id="pp-${esc(p.id)}" style="border-top:3px solid ${esc(p.color || "var(--c4)")}">
    <div class="head" style="margin:0 0 6px"><h2 style="margin:0">${esc(p.name)} <span class="muted fine">${ppCount(p)}</span></h2>
      <div class="btnrow"><button class="btn sm primary" data-exgo="person|${esc(p.id)}">Explore</button>${isAdmin() ? `<button class="btn sm" data-ppedit="${esc(p.id)}">Edit</button>` : ""}</div></div>
    <div class="pp-body"><div class="empty">Loading…</div></div></div>`).join("")}</div>
    <div class="card section"><h2>Devices not assigned to anyone</h2><div id="pp-unassigned"><div class="empty">Loading…</div></div></div>`);
  ppl.forEach(async p => {
    const el = $(`#pp-${CSS.escape(p.id)} .pp-body`);
    try {
      const r = await explore(["summary", "by_category", "heatmap", "by_device", "traffic"], { person: p.id });
      const s = r.summary || {}, days = s.active_days || 1, tr = r.traffic?.summary || {};
      el.innerHTML = `<div class="pp-kpis"><div><b>${fmtNum(Math.round((s.total || 0) / days))}</b><span>queries a day</span></div>
          <div><b>${s.active_min ? fmtMin(s.active_min / days) : "–"}</b><span>active a day ≈</span></div><div><b>${pct(s.blocked, s.total)}</b><span>blocked</span></div>
          <div><b>${tr.rx || tr.tx ? fmtBytes((tr.rx + tr.tx) / days) : "–"}</b><span>data a day</span></div></div>
        <h3>Top categories</h3>${blist((r.by_category || []).slice(0, 6), "category", { color: i => catColor(i.key), sub: subActive })}
        <h3>When</h3>${heatmap(r.heatmap, { metric: "n", fmt: v => fmtNum(Math.round(v)), clickable: false, color: p.color || "var(--c4)" })}
        <p class="fine">${(r.by_device || []).map(d => `<a href="#" data-exgo="device|${esc(d.key)}">${esc(d.key)}</a>`).join(" · ") || "No DNS activity from these devices in this period."}</p>`;
      // clicking a category inside a person card explores person + category
      $$(".bl-row", el).forEach(b => { b.dataset.exgo = `person|${p.id};;${b.dataset.exf}`; delete b.dataset.exf; });
    } catch (e) { el.innerHTML = `<div class="empty bad-t">${esc(e.message)}</div>`; }
  });
  try {
    const r = await explore(["by_device"], { person: "__none__" });
    fill("pp-unassigned", blist(r.by_device, "device", { sub: subActive, empty: "Every device with DNS activity is assigned 🎉" }).replace(/data-exf="device\|/g, 'data-exgo="device|'));
  } catch (e) { fill("pp-unassigned", `<div class="empty bad-t">${esc(e.message)}</div>`); }
}

function ppCount(p) {
  const prof = p.devices.filter(d => d.kind === "profile").length, dev = p.devices.length - prof;
  return [prof ? plural(prof, "NextDNS profile") : "", dev || !prof ? plural(dev, "device") : ""].filter(Boolean).join(" + ");
}
function personModal(p) {
  const meta = S.ex.meta, owner = {};
  meta.people.forEach(x => x.devices.forEach(d => { owner[`${d.kind}|${d.key}`] = x; }));
  p = p || { id: "", name: "", color: ["#4c8dff", "#13a49b", "#d9480f", "#8a5cf6", "#c2a000", "#d6336c"][meta.people.length % 6], devices: [] };
  const mine = new Set(p.devices.map(d => `${d.kind}|${d.key}`));
  const shown = new Set();   // keys this editor shows; anything else the person owns is kept as it is
  const isMine = (kind, key, legacy) => mine.has(`${kind}|${key}`) || (legacy && mine.has(`dns|${legacy}`));
  const ownerOf = (kind, key, legacy) => owner[`${kind}|${key}`] || (legacy && owner[`dns|${legacy}`]);
  const row = (kind, key, label, sub, legacy) => { shown.add(`${kind}|${key}`); if (legacy) shown.add(`dns|${legacy}`);
    const o = ownerOf(kind, key, legacy); const other = o && o.id !== p.id;
    return `<label class="check pp-dev" data-txt="${esc((label + " " + sub).toLowerCase())}"><input type="checkbox" data-ppdev="${kind}|${esc(key)}" ${isMine(kind, key, legacy) ? "checked" : ""}>
      <span>${esc(label)} <span class="fine">${esc(sub)}</span>${other ? ` <span class="tag warn">now ${esc(o.name)}'s</span>` : ""}</span></label>`; };
  // NextDNS: devices grouped under their profile; a profile can belong to one person as a whole
  const profName = id => (meta.nd_profiles || []).find(x => x.id === id)?.name || meta.profiles?.[id] || id;
  const byProf = {};
  meta.dns_devices.forEach(d => { (byProf[d.profile || ""] ||= []).push(d); });
  const profIds = [...new Set([...(meta.nd_profiles || []).map(x => x.id), ...Object.keys(byProf)])];
  const ndGroup = pid => { const devs = byProf[pid] || [], info = (meta.nd_profiles || []).find(x => x.id === pid) || {};
    const head = pid ? `<div class="pp-prof">${row("profile", pid, `All of “${profName(pid)}”`, [plural(info.devices ?? devs.length, "device"), info.queries ? fmtNum(info.queries) + " q" : ""].filter(Boolean).join(" · "))}</div>` : "";
    return `<div class="pp-group"><div class="pp-ghead">${pid ? `NextDNS profile <b>${esc(profName(pid))}</b> <span class="fine mono">${esc(pid)}</span>` : "Other"}</div>${head}
      ${devs.map(d => row(pid ? "ndev" : "dns", pid ? `${pid}/${d.device}` : d.device, d.device, [d.local_ip, d.model, d.queries ? fmtNum(d.queries) + " q" : ""].filter(Boolean).join(" · "), d.device)).join("") || `<div class="fine muted">No devices in the last 30 days</div>`}</div>`; };
  openModal({
    title: p.id ? `Edit ${p.name}` : "Add a person", wide: true,
    body: `<div class="grid g2"><label>Name<input id="pp-name" value="${esc(p.name)}" placeholder="e.g. Noa"></label><label>Colour<input id="pp-color" type="color" value="${esc(p.color || "#8a5cf6")}" style="height:40px;padding:4px"></label></div>
      <div class="ovr" style="grid-template-columns:1fr auto"><input id="pp-find" placeholder="Filter devices… (typing the name above also suggests matches)"><button class="btn sm" id="pp-suggest" type="button">Select name matches</button></div>
      <div class="grid g2 section"><div><h3>NextDNS <span class="fine muted">(by profile, last 30 days)</span></h3><div class="pp-pick">${profIds.map(ndGroup).join("") || `<div class="empty">No DNS devices yet</div>`}</div></div>
        <div><h3>UniFi clients <span class="fine muted">(adds data usage)</span></h3><div class="pp-pick">${meta.clients.map(c => row("mac", c.mac, c.name || c.hostname || c.mac, [c.ip, c.network, c.vendor, c.mac].filter(Boolean).join(" · "))).join("") || `<div class="empty">No UniFi clients imported yet</div>`}</div></div></div>
      <p class="fine">If a NextDNS profile is just for this person, tick <b>All of “profile”</b> — handy when its devices have generic names like “iPhone 16”. A device ticked for someone else still counts as theirs. Pick the matching UniFi client too, so data usage is counted.</p>`,
    buttons: [...(p.id ? [{ label: "Delete", cls: "danger", onClick: () => confirmDelete(p) }] : []), { label: "Cancel" }, { label: "Save", cls: "primary", onClick: async () => {
      const name = $("#pp-name").value.trim(); if (!name) return toast("Give the person a name", "err");
      const devices = $$("[data-ppdev]").filter(x => x.checked).map(x => { const [kind, ...k] = x.dataset.ppdev.split("|"); return { kind, key: k.join("|") }; });
      p.devices.forEach(d => { if (!shown.has(`${d.kind}|${d.key}`)) devices.push(d); });   // devices not seen lately stay assigned
      try { await api("/api/analytics/person-save", { id: p.id, name, color: $("#pp-color").value, devices }); closeModal(); toast(`${name} saved`, "ok"); S.ex.meta = null; loadAnalytics(); } catch (e) { toast(e.message, "err"); }
    } }],
  });
  const filt = () => { const q = $("#pp-find").value.toLowerCase().trim(); $$(".pp-dev").forEach(l => { l.hidden = q && !l.dataset.txt.includes(q); }); };
  $("#pp-find").addEventListener("input", filt);
  $("#pp-suggest").addEventListener("click", () => {
    const n = ($("#pp-find").value || $("#pp-name").value).toLowerCase().trim(); if (n.length < 2) return toast("Type a name first", "err");
    let k = 0; $$(".pp-dev").forEach(l => { if (l.dataset.txt.includes(n)) { l.querySelector("input").checked = true; l.hidden = false; k++; } });
    toast(k ? `Selected ${k} matching device${k > 1 ? "s" : ""}` : "No device names match");
  });
}
function confirmDelete(p) {
  openModal({ title: `Delete ${p.name}?`, body: "<p>Their devices become unassigned. No data is deleted.</p>", buttons: [{ label: "Cancel" }, { label: "Delete", cls: "primary", onClick: async () => {
    try { await api("/api/analytics/person-delete", { id: p.id }); closeModal(); S.ex.meta = null; if (S.ex.filters.person === p.id) delete S.ex.filters.person; loadAnalytics(); } catch (e) { toast(e.message, "err"); }
  } }] });
}

// ------------------------------------------------------------------ Categories tab
function catsBody() {
  return `<div class="grid g2"><div class="card"><h2>Categories <span class="muted fine">(click to explore)</span></h2><div id="ct-cats"></div></div>
      <div class="card"><h2>${S.ex.catAll ? "All domains" : "Uncategorized domains"}</h2>
        <div class="filters"><div class="seg"><button data-excatall="0" class="${S.ex.catAll ? "" : "on"}">Uncategorized</button><button data-excatall="1" class="${S.ex.catAll ? "on" : ""}">All domains</button></div></div>
        <div id="ct-unc"></div></div></div>
    <div class="card section"><h2>Your rules</h2><p class="muted">Rules win over the built-in list. <span class="mono">example.com</span> also covers its subdomains; <span class="mono">~word</span> matches any domain containing “word”.</p>
      ${isAdmin() ? `<div class="ovr" style="grid-template-columns:1fr 1fr auto"><input id="ct-pat" placeholder="domain.com, *.domain.com or ~word"><select id="ct-cat">${(S.ex.meta?.categories || []).filter(c => c !== "Uncategorized").map(c => `<option>${esc(c)}</option>`).join("")}<option value="__new">New category…</option></select><button class="btn sm primary" id="ct-add">Add rule</button></div>` : ""}
      <div id="ct-rules" class="section"></div></div>`;
}
async function loadCats() {
  let meta; try { meta = await exMeta(true); } catch (e) { return fill("ct-cats", `<div class="empty bad-t">${esc(e.message)}</div>`); }
  if (isAdmin() && $("#ct-cat") && $("#ct-cat").options.length < 3) $("#an-body").innerHTML = catsBody();   // first visit: meta just arrived
  try {
    const [a, b] = await Promise.all([explore(["by_category"], {}), explore(["by_domain"], S.ex.catAll ? {} : { category: "Uncategorized" }, { page: { root_limit: 300 } })]);
    fill("ct-cats", blist(a.by_category, "category", { color: i => catColor(i.key), sub: i => `${subActive(i)} · ${fmtNum(i.domains)} domains` }).replace(/data-exf="category\|/g, 'data-exgo="category|'));
    const cats = [...meta.categories, ...new Set(meta.rules.map(r => r.category))].filter((c, i, x) => x.indexOf(c) === i && c !== "Uncategorized");
    fill("ct-unc", (b.by_domain || []).length ? `<div class="tbl-wrap" style="max-height:520px"><table><thead><tr><th>Domain</th><th class="num">Queries</th><th class="num">Devices</th><th>Category</th></tr></thead><tbody>
      ${b.by_domain.map(d => `<tr><td class="mono"><a href="#" data-exgo="root|${esc(d.key)}">${esc(d.key)}</a></td><td class="num">${fmtNum(d.total)}</td><td class="num">${fmtNum(d.devices)}</td>
        <td>${isAdmin() ? `<select data-ctassign="${esc(d.key)}" style="padding:4px 6px"><option value="">${esc(d.category || "Uncategorized")}</option>${cats.filter(c => c !== d.category).map(c => `<option>${esc(c)}</option>`).join("")}</select>` : esc(d.category)}</td></tr>`).join("")}
      </tbody></table></div>` : `<div class="empty">${S.ex.catAll ? "No domains in this period" : "Everything in this period has a category 🎉"}</div>`);
    fill("ct-rules", meta.rules.length ? table("ct-rules-t", [{ k: "pattern", label: "Pattern", fmt: r => `<span class="mono">${esc(r.pattern)}</span>` }, { k: "category", label: "Category" },
      { k: "created", label: "Added", fmt: r => fmtTime(r.created), sort: r => r.created }, ...(isAdmin() ? [{ k: "x", label: "", fmt: r => `<button class="btn sm" data-ctdel="${esc(r.pattern)}">Remove</button>` }] : [])], meta.rules, { search: true })
      : `<div class="empty">No rules yet — the built-in list (${fmtNum(meta.categories.length - 1)} categories, ~500 popular domains) is used.</div>`);
  } catch (e) { fill("ct-unc", `<div class="empty bad-t">${esc(e.message)}</div>`); }
}
function categoryModal(root) {
  const meta = S.ex.meta, cats = meta.categories.filter(c => c !== "Uncategorized");
  openModal({ title: `Category for ${root}`, body: `<label>Category<select id="cm-cat">${cats.map(c => `<option>${esc(c)}</option>`).join("")}<option value="__new">New category…</option></select></label>
      <label>Apply to<select id="cm-scope"><option value="${esc(root)}">${esc(root)} and its subdomains</option><option value="~${esc(root.split(".")[0])}">every domain containing “${esc(root.split(".")[0])}”</option></select></label>`,
    buttons: [{ label: "Cancel" }, { label: "Save", cls: "primary", onClick: async () => {
      let cat = $("#cm-cat").value; if (cat === "__new") { cat = prompt("New category name"); if (!cat) return; }
      try { await api("/api/analytics/rule-save", { pattern: $("#cm-scope").value, category: cat }); closeModal(); toast(`${root} → ${cat}`, "ok"); S.ex.meta = null; loadAnalytics(); } catch (e) { toast(e.message, "err"); }
    } }] });
}

// ------------------------------------------------------------------ wiring
const exTabs = { explore: exBody, people: peopleBody, categories: catsBody };
const _anBody = anBody;
anBody = function () { return exTabs[S.an.tab] ? exTabs[S.an.tab]() : _anBody(); };   // eslint-disable-line no-global-assign
loaders.explore = loadExplore; loaders.people = loadPeople; loaders.categories = loadCats;

function goExplore(spec) {
  S.ex.stack.push(JSON.stringify(S.ex.filters));
  const f = {}; spec.split(";;").forEach(p => { const i = p.indexOf("|"); const k = p.slice(0, i), v = p.slice(i + 1); f[k] = k === "hour" || k === "dow" ? +v : v; });
  S.ex.filters = f; S.ex.raw.offset = 0; S.an.tab = "explore"; window.scrollTo(0, 0); loadAnalytics();
}
document.addEventListener("click", e => {
  const g = sel => e.target.closest(sel); let x;
  if ((x = g("[data-exgo]"))) { e.preventDefault(); return goExplore(x.dataset.exgo); }
  if (S.view !== "analytics") return;
  if ((x = g("[data-exf]")) && S.an.tab === "explore") {
    e.preventDefault(); const i = x.dataset.exf.indexOf("|"), k = x.dataset.exf.slice(0, i); let v = x.dataset.exf.slice(i + 1);
    if (k === "hour" || k === "dow") v = +v;
    setFilter(k, S.ex.filters[k] === v ? null : v); return loadExplore();
  }
  if ((x = g("[data-exhm]"))) { const [d, h] = x.dataset.exhm.split("|").map(Number); S.ex.stack.push(JSON.stringify(S.ex.filters));
    const f = { ...S.ex.filters }; if (f.dow === d && f.hour === h) { delete f.dow; delete f.hour; } else { f.dow = d; f.hour = h; } S.ex.filters = f; S.ex.raw.offset = 0; return loadExplore(); }
  if ((x = g("[data-exrange]")) && S.an.tab === "explore") { const [a, b] = x.dataset.exrange.split("|"); S.ex.stack.push(JSON.stringify(S.ex.filters)); S.ex.filters = { ...S.ex.filters, lfrom: a, lto: b }; S.ex.raw.offset = 0; return loadExplore(); }
  if ((x = g("[data-exdel]"))) { const k = x.dataset.exdel; setFilter(k, null); if (k === "lfrom") delete S.ex.filters.lto; return loadExplore(); }
  if (g("[data-exclear]")) { setFilter("x", null, { replace: true }); return loadExplore(); }
  if (g("[data-exback]")) { S.ex.filters = JSON.parse(S.ex.stack.pop() || "{}"); S.ex.raw.offset = 0; return loadExplore(); }
  if ((x = g("[data-exmetric]"))) { S.ex.metric = x.dataset.exmetric; $$("[data-exmetric]").forEach(b => b.classList.toggle("on", b === x));
    const r = S.ex.last; if (r) { const o = S.ex.metric === "active" ? { metric: "active", fmt: v => fmtMin(v), color: "var(--c4)" } : { metric: "n", fmt: v => fmtNum(Math.round(v)) }; fill("ex-hm", heatmap(r.heatmap, o)); fill("ex-hp", hourProfile(r.heatmap, o)); hmNote(r); } return; }
  if ((x = g("[data-exsort]"))) { const k = x.dataset.exsort; S.ex.raw.dir = S.ex.raw.sort === k && S.ex.raw.dir === "desc" ? "asc" : "desc"; S.ex.raw.sort = k; S.ex.raw.offset = 0; return reloadRaw(); }
  if ((x = g("[data-expage]"))) { S.ex.raw.offset = Math.max(0, S.ex.raw.offset + (+x.dataset.expage) * S.ex.raw.limit); return reloadRaw(); }
  if (e.target.id === "ex-csv") return exportCsv();
  if ((x = g("[data-excat]"))) { e.preventDefault(); return categoryModal(x.dataset.excat); }
  if (e.target.id === "pp-add" || e.target.id === "pp-add2") return personModal(null);
  if ((x = g("[data-ppedit]"))) return personModal(S.ex.meta.people.find(p => p.id === x.dataset.ppedit));
  if ((x = g("[data-excatall]"))) { S.ex.catAll = x.dataset.excatall === "1"; return loadAnalytics(); }
  if ((x = g("[data-ctdel]"))) { api("/api/analytics/rule-delete", { pattern: x.dataset.ctdel }).then(() => { toast("Rule removed", "ok"); loadCats(); }).catch(err => toast(err.message, "err")); return; }
  if (e.target.id === "ct-add") {
    let cat = $("#ct-cat").value; if (cat === "__new") { cat = prompt("New category name"); if (!cat) return; }
    api("/api/analytics/rule-save", { pattern: $("#ct-pat").value, category: cat }).then(() => { toast("Rule added", "ok"); loadCats(); }).catch(err => toast(err.message, "err"));
  }
});
document.addEventListener("change", e => {
  if (S.view !== "analytics") return;
  const k = e.target.dataset?.exsel; if (k) { setFilter(k, e.target.value || null); return loadExplore(); }
  if (e.target.id === "ex-pagesize") { S.ex.raw.limit = +e.target.value; S.ex.raw.offset = 0; return reloadRaw(); }
  const root = e.target.dataset?.ctassign;
  if (root && e.target.value) api("/api/analytics/rule-save", { pattern: root, category: e.target.value }).then(() => { toast(`${root} → ${e.target.value}`, "ok"); loadCats(); }).catch(err => toast(err.message, "err"));
});
let EX_T = null;
document.addEventListener("input", e => { if (e.target.id === "ex-q") { clearTimeout(EX_T); const v = e.target.value.trim(); EX_T = setTimeout(() => { setFilter("q", v || null); loadExplore().then(() => { const i = $("#ex-q"); if (i) { i.focus(); i.setSelectionRange(v.length, v.length); } }); }, 400); } });
