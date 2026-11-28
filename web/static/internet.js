/* HomeOwl — Network › Internet: is the internet OK, and when it isn't, is it the provider or something at home?
   HomeOwl's own reading every minute (gateway and internet), the gateway's speed tests, outages and the verdict.
   Depends on core.js and app.js. */
"use strict";

S.inet = null;      // last /api/internet answer (or the demo's)
S.inetDays = 1;
S.inetEdit = null;  // settings being edited

const INET_CAUSE = { isp: "Your provider", home: "At home" };
const INET_SRC = { unifi: "UniFi", schedule: "Schedule", homeowl: "HomeOwl" };
const inetMs = v => v === null || v === undefined ? "–" : v < 10 ? `${(+v).toFixed(1)} ms` : `${Math.round(v)} ms`;
const inetPct = v => v === null || v === undefined ? "–" : v >= 99.995 ? "100%" : v >= 99 ? `${(+v).toFixed(2)}%` : `${(+v).toFixed(1)}%`;
const inetMin = m => m < 1 ? "under a minute" : m < 90 ? `${Math.round(m)} min` : m < 2880 ? `${(m / 60).toFixed(1)} h` : `${Math.round(m / 1440)} days`;

V.internet = () => `<div class="head"><div><h1>Internet ${hint("How the internet connection is doing. Every minute HomeOwl times a connection from this computer to your gateway and to the internet. When the internet stops answering but the gateway still does, it's your provider; when the gateway doesn't answer either, it's something at home. The gateway's own speed tests show here too.")}</h1></div></div>
  <div id="inet-body"><div class="empty">Loading…</div></div>`;

// demo: a fibre line that dropped three times this week (the provider), one short power blip at home, daily speed tests
function inetFromBundle() {
  const now = Date.now(), H = 3600e3, days = S.inetDays, step = days > 1 ? 30 * 60e3 : 5 * 60e3;
  const outages = [
    { start: now - 26 * H, end: now - 26 * H + 14 * 60e3, cause: "isp", isp_min: 14, home_min: 0 },
    { start: now - 75 * H, end: now - 75 * H + 4 * 60e3, cause: "home", isp_min: 0, home_min: 4 },
    { start: now - 98 * H, end: now - 98 * H + 9 * 60e3, cause: "isp", isp_min: 9, home_min: 0 },
    { start: now - 141 * H, end: now - 141 * H + 22 * 60e3, cause: "isp", isp_min: 22, home_min: 0 },
  ];
  const buckets = [];
  for (let t = Math.floor((now - days * 86400e3) / step) * step; t <= now; t += step) {
    const o = outages.find(o => t + step > o.start && t < o.end);
    const evening = new Date(t).getHours() >= 19 && new Date(t).getHours() <= 22;
    const n = 9 + Math.sin(t / 2.3e6) * 1.5 + Math.sin(t / 7.1e5) * .8 + (evening ? 4 : 0);
    buckets.push(o ? { t, n: null, g: o.cause === "home" ? null : 1.4, m: 100 } : { t, n: +n.toFixed(1), g: +(1.3 + Math.sin(t / 1.1e6) * .3).toFixed(2), m: 0 });
  }
  const tests = [];
  for (let d = 0; d < 30; d++) {
    const t = new Date(now - d * 86400e3); t.setHours(4, 2, 0, 0); if (t > now) continue;
    tests.push({ t: +t, down: Math.round(905 + Math.sin(d * 1.7) * 40), up: Math.round(880 + Math.cos(d) * 30), latency: 6 + (d % 3), server: "Example City", source: d % 7 === 3 ? "homeowl" : "schedule", by: d % 7 === 3 ? "admin" : "HomeOwl (schedule)" });
  }
  return { demo: true, days, connected: true, targets: ["1.1.1.1:443", "8.8.8.8:443", "9.9.9.9:443"], schedule: "daily",
    status: { last: { t: now - 20e3, g: 1.4, n: 9.6 }, away: false, gateway: "192.168.1.1", off: false },
    verdict: { level: "warn", who: "isp", title: "Mostly your provider",
      lines: ["Your provider dropped 3 times in the last 7 days (45 min in all), while your gateway kept answering."] },
    week: { readings: 10080, uptime: 99.51, net_median: 9.8, net_p95: 17, gw_median: 1.4, jitter: 2.1, missed: 0.1, isp_min: 45, home_min: 4, isp_count: 3, home_count: 1 },
    day: { readings: 1440, uptime: 99.03, net_median: 10.2, net_p95: 18, gw_median: 1.4, jitter: 2.3, missed: 0, isp_min: 14, home_min: 0, isp_count: 1, home_count: 0 },
    buckets, tests, outages: [...outages].sort((a, b) => b.start - a.start), plan: { down: 1000, up: 1000 },
    unifi: { status: "ok", isp: "Example Fiber", latency: 9, xput_down: tests[0]?.down, xput_up: tests[0]?.up },
    next_test: (() => { const n = new Date(now); n.setHours(4, 0, 0, 0); if (n <= now) n.setDate(n.getDate() + 1); return +n; })() };
}

async function loadInternet() {
  const el = $("#inet-body"); if (!el) return;
  if (S.source === "live" || S.source === "none") {
    try { S.inet = await api(`/api/internet?days=${S.inetDays}`); }
    catch (err) { el.innerHTML = `<div class="card"><p class="muted">${esc(err.message)}</p></div>`; return; }
  } else S.inet = inetFromBundle();
  if (S.inet.settings && !S.inetEdit) S.inetEdit = { ...S.inet.settings };
  if ($("#inet-body")) $("#inet-body").innerHTML = inetBody(S.inet);
}

function inetVerdict(d) {
  const v = d.verdict || {}, cls = { good: "good", warn: "warn", bad: "bad" }[v.level] || "";
  const l = d.status?.last;
  return `<div class="status ${cls}"><div class="status-main">
      <div class="status-big">${esc(v.title || "")}</div>
      <ul class="status-list">${arr(v.lines).map(x => `<li><span>${esc(x)}</span></li>`).join("")}</ul>
      ${l ? `<p class="fine" style="margin:8px 0 0">Last reading ${ago(l.t)}: ${l.n === null || l.n === undefined ? "no answer from the internet" : `internet ${inetMs(l.n)}`}${d.status.gateway ? ` · gateway ${l.g === null || l.g === undefined ? "no answer" : inetMs(l.g)}` : ""}</p>` : ""}
    </div></div>`;
}

function inetKpis(d) {
  const w = d.week || {}, dy = d.day || {}, t = arr(d.tests)[0], plan = d.plan || {};
  const out = (w.isp_count || 0) + (w.home_count || 0);
  return `<div class="grid g4 section">
    ${kpi(inetPct(w.uptime), "Up · 7 days", out ? `${plural(out, "outage")} · ${inetMin((w.isp_min || 0) + (w.home_min || 0))}` : "no outages")}
    ${kpi(inetMs(dy.net_median), "To the internet", dy.net_p95 !== null && dy.net_p95 !== undefined ? `typical · 1 in 20 over ${inetMs(dy.net_p95)}` : "typical over 24 hours")}
    ${kpi(inetMs(dy.gw_median), "To the gateway", d.status?.gateway ? "from this computer" : "no gateway known yet")}
    ${kpi(t ? `${Math.round(t.down)}<span class="kpi-u"> / ${Math.round(t.up)} Mbps</span>` : "–", "Last speed test", t ? `${ago(t.t)}${plan.down ? ` · plan ${Math.round(plan.down)} / ${Math.round(plan.up || 0)}` : ""}` : "none yet")}
  </div>`;
}

function inetChart(d) {
  const b = arr(d.buckets), pts = k => b.map(x => ({ t: x.t, v: x[k] ?? null }));
  return `<div class="card section"><div class="ups-head"><h2>Response time</h2>
      <div class="seg">${[[1, "24 hours"], [7, "7 days"]].map(([x, l]) => `<button class="${S.inetDays === x ? "on" : ""}" data-inetdays="${x}">${l}</button>`).join("")}</div></div>
    ${b.length ? lineChart([{ name: "Internet", points: pts("n"), gaps: true }, { name: "Gateway", points: pts("g"), gaps: true }], { fmt: v => inetMs(v), height: 210 })
      : `<div class="empty">${d.status?.off ? "Readings are off." : "HomeOwl takes a reading every minute. The first ones show up here in a minute."}</div>`}
    ${b.some(x => x.m > 0) ? `<p class="fine">Gaps in the line are minutes without an answer from the internet.</p>` : ""}</div>`;
}

function inetTests(d) {
  const ts = arr(d.tests), plan = d.plan || {};
  const chrono = [...ts].reverse();
  const chart = chrono.length >= 2 ? lineChart([{ name: "Download", points: chrono.map(t => ({ t: t.t, v: t.down })) }, { name: "Upload", points: chrono.map(t => ({ t: t.t, v: t.up })) },
    ...(plan.down ? [{ name: "Your plan", points: [{ t: chrono[0].t, v: plan.down }, { t: chrono[chrono.length - 1].t, v: plan.down }], color: "var(--muted)" }] : [])],
    { fmt: v => `${Math.round(v)}`, height: 180 }) : "";
  const sched = { daily: "every day", "6h": "every 6 hours" }[d.schedule];
  return `<div class="card section"><div class="ups-head"><h2>Speed tests</h2>${inetRunBtn(d)}</div>
    <p class="fine">The gateway runs these itself, so the home Wi-Fi doesn't slow them down: they show what your provider delivers. ${sched ? `HomeOwl starts one ${sched}${d.next_test ? `, next ${fmtTime(d.next_test)}` : ""}.` : "Tests started in UniFi show up here too."}</p>
    ${chart ? `<h3 class="fine">Mbps</h3>${chart}` : ""}
    ${table("inettests", [
      { k: "t", label: "When", fmt: t => fmtTime(t.t), sort: t => -t.t },
      { k: "down", label: "Down", num: true, fmt: t => `${Math.round(t.down)} Mbps${t.note ? ` <span class="tag warn">${esc(t.note)}</span>` : ""}`, sort: t => t.down },
      { k: "up", label: "Up", num: true, fmt: t => `${Math.round(t.up)} Mbps`, sort: t => t.up },
      { k: "latency", label: "Latency", num: true, fmt: t => inetMs(t.latency) },
      { k: "server", label: "Server", fmt: t => esc(t.server || "") },
      { k: "source", label: "Started by", fmt: t => `<span class="tag">${esc(INET_SRC[t.source] || t.source)}</span>${t.source === "homeowl" && t.by ? ` <span class="fine">${esc(t.by)}</span>` : ""}` },
    ], ts, { empty: d.connected === false ? "Connect UniFi to see the gateway's speed tests." : "No speed tests yet." })}</div>`;
}

function inetRunBtn(d) {
  if (S.role !== "admin" || d.demo || !d.connected) return "";
  return d.running ? `<span class="tag info">running… results in about a minute</span>` : `<button class="btn sm" id="inet-run">Run speed test</button>`;
}

function inetOutages(d) {
  const now = Date.now();
  return `<div class="card section"><h2>Outages <span class="muted" style="font-weight:400;font-size:13px">· last year</span></h2>
    <p class="fine">Two readings in a row without an answer from the internet make an outage. <b>Your provider</b>: the gateway kept answering. <b>At home</b>: it didn't either (its power, a cable, or this computer's own connection).</p>
    ${table("inetout", [
      { k: "start", label: "When", fmt: o => fmtTime(o.start), sort: o => -o.start },
      { k: "dur", label: "How long", fmt: o => o.end ? inetMin((o.end - o.start) / 60000) : `<span class="warn-t">still down · ${inetMin((now - o.start) / 60000)}</span>`, sort: o => (o.end || now) - o.start },
      { k: "cause", label: "Likely cause", fmt: o => `<span class="tag ${o.cause === "isp" ? "bad" : "warn"}">${esc(INET_CAUSE[o.cause] || o.cause)}</span>` },
    ], arr(d.outages), { empty: "No outages since HomeOwl started watching." })}</div>`;
}

function inetAdmin(d) {
  const e = S.inetEdit || d.settings || {};
  const hours = Array.from({ length: 24 }, (_, h) => `<option value="${h}" ${+e.hour === h ? "selected" : ""}>${String(h).padStart(2, "0")}:00</option>`).join("");
  return `<div class="card section"><h2>Settings</h2>
    <label class="check"><input type="checkbox" id="inet-readings" ${e.readings !== false ? "checked" : ""}> Take a reading every minute (a connection to the gateway and to ${arr(d.targets).map(t => `<span class="mono">${esc(String(t).replace(/:443$/, ""))}</span>`).join(", ")}; a few bytes each)</label>
    <label class="check"><input type="checkbox" id="inet-alerts" ${e.alerts !== false ? "checked" : ""}> Alert when the internet goes down and when it's back (Home › Alerts and your phone; while it's down, a phone may only hear when it's back)</label>
    <div class="grid g3">
      <label>Speed tests<select id="inet-sched"><option value="" ${!e.speed_test ? "selected" : ""}>only when someone runs one</option><option value="daily" ${e.speed_test === "daily" ? "selected" : ""}>every day</option><option value="6h" ${e.speed_test === "6h" ? "selected" : ""}>every 6 hours</option></select></label>
      <label>At<select id="inet-hour" ${e.speed_test === "daily" ? "" : "disabled"}>${hours}</select></label>
      <span></span>
      <label>Your plan, download<span class="ups-in"><input type="number" min="0" max="100000" id="inet-pd" value="${esc(e.plan_down || "")}" placeholder="e.g. 1000"> Mbps</span></label>
      <label>Upload<span class="ups-in"><input type="number" min="0" max="100000" id="inet-pu" value="${esc(e.plan_up || "")}" placeholder="e.g. 1000"> Mbps</span></label>
    </div>
    <p class="fine">A speed test uses your full bandwidth for about 30 seconds (on a 1 Gbps line, roughly 2–4 GB of data), so pick a quiet hour. Each test HomeOwl starts is written to the Changes log. With your plan filled in, HomeOwl says when tests come in under half of it.</p>
    ${d.import_error ? `<p class="warn-t fine">${esc(d.import_error)}</p>` : ""}
    <div class="btnrow"><button class="btn primary" id="inet-save">Save</button></div></div>`;
}

function inetBody(d) {
  const admin = S.role === "admin" && !d.demo;
  return `${d.status?.off ? `<div class="banner">Readings are off, so HomeOwl doesn't notice outages. ${admin ? "Turn them on under Settings below." : ""}</div>` : ""}
    ${inetVerdict(d)}
    ${inetKpis(d)}
    ${inetChart(d)}
    ${inetTests(d)}
    ${inetOutages(d)}
    ${admin ? inetAdmin(d) : ""}`;
}

function inetCollect() {
  if (!S.inetEdit || !$("#inet-save")) return;
  S.inetEdit = { readings: !!$("#inet-readings")?.checked, alerts: !!$("#inet-alerts")?.checked, speed_test: $("#inet-sched")?.value || "",
    hour: +($("#inet-hour")?.value || 0), plan_down: +($("#inet-pd")?.value || 0), plan_up: +($("#inet-pu")?.value || 0) };
}

document.addEventListener("click", async e => {
  if (!e.target.closest("#inet-body")) return;
  const days = e.target.closest("[data-inetdays]");
  if (days) { inetCollect(); S.inetDays = +days.dataset.inetdays; loadInternet(); return; }
  if (e.target.id === "inet-run") {
    e.target.disabled = true;
    try {
      S.inet = await api("/api/internet/speedtest", {}); $("#inet-body").innerHTML = inetBody(S.inet);
      toast("Speed test started. The result shows up in about a minute.", "ok");
      [80e3, 170e3, 300e3].forEach(ms => setTimeout(() => { if (S.view === "internet") loadInternet(); }, ms));
    } catch (err) { toast(err.message, "err"); e.target.disabled = false; }
  } else if (e.target.id === "inet-save") {
    inetCollect(); e.target.disabled = true;
    try { S.inet = await api("/api/internet/settings", S.inetEdit); S.inetEdit = { ...S.inet.settings }; $("#inet-body").innerHTML = inetBody(S.inet); toast("Internet settings saved", "ok"); }
    catch (err) { toast(err.message, "err"); e.target.disabled = false; }
  }
});
document.addEventListener("change", e => {
  if (e.target.id === "inet-sched") { const h = $("#inet-hour"); if (h) { h.disabled = e.target.value !== "daily"; if (!h.disabled && S.inetEdit && !S.inetEdit.speed_test) h.value = "4"; } }
});

// a fresh reading every minute while the page is open
setInterval(() => { if (S.view === "internet" && (S.source === "live" || S.source === "none") && !document.hidden && $("#modal").hidden && !document.activeElement?.closest?.("#inet-body input, #inet-body select")) { inetCollect(); loadInternet(); } }, 60000);

{ const _render = render; render = function () { _render(); if (S.view === "internet") loadInternet(); }; }
