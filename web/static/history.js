/* HomeOwl — configuration history: a snapshot whenever the UniFi configuration changes, and what changed. */
"use strict";

S.hist = { list: null, sel: null, since: 24 };
V.confighistory = () => `<div class="head"><div><h1>Config history</h1><div class="muted">HomeOwl keeps a copy of your UniFi settings whenever they change (passwords stay hidden), so you can see what changed and when.</div></div>
  <button class="btn" id="hist-reload">Refresh</button></div><div id="hist-body"><div class="empty">Loading…</div></div>`;

const HIST_AGO = [[24, "Since yesterday"], [168, "Last 7 days"], [720, "Last 30 days"]];
async function loadHistory() {
  const el = $("#hist-body"); if (!el) return;
  if (S.source !== "live") { el.innerHTML = `<div class="card"><p class="muted" style="margin:0">Config history is kept by a running HomeOwl connected to your console — it isn't part of demo data or saved reports.</p></div>`; return; }
  try { S.hist.list = await api("/api/history"); } catch (err) { el.innerHTML = `<div class="empty bad-t">${esc(err.message)}</div>`; return; }
  const snaps = S.hist.list.snapshots || [];
  if (!snaps.length) { el.innerHTML = `<div class="card"><p class="muted" style="margin:0">No snapshot yet — the first one is taken at the next refresh of the data.</p></div>`; return; }
  const q = S.hist.sel ? `to=${encodeURIComponent(S.hist.sel)}` : `since=${S.hist.since}`;
  let d; try { d = await api(`/api/history/diff?${q}`); } catch (err) { el.innerHTML = `<div class="empty bad-t">${esc(err.message)}</div>`; return; }
  if ($("#hist-body")) $("#hist-body").innerHTML = historyBody(snaps, d);
}

const histVal = v => v === undefined || v === null ? `<span class="muted">–</span>` : esc(typeof v === "object" ? JSON.stringify(v) : String(v));
function historyBody(snaps, d) {
  const title = S.hist.sel ? `Changes in this snapshot` : HIST_AGO.find(x => x[0] === S.hist.since)?.[1] || "Changes";
  const groups = {}; d.changes.forEach(c => (groups[c.area] ||= []).push(c));
  const kindTag = k => `<span class="tag ${k === "added" ? "good" : k === "removed" ? "bad" : "warn"}">${k}</span>`;
  const diff = d.changes.length ? Object.entries(groups).map(([area, list]) => `<h3 style="margin-top:14px">${esc(area)} <span class="muted" style="font-weight:400">· ${list.length}</span></h3>
      ${list.map(c => `<div class="hist-ch"><div>${kindTag(c.kind)} <b>${esc(c.name || c.id)}</b></div>
        ${c.fields?.length ? `<table class="pv-t hist-t"><tr><th>Setting</th><th>Before</th><th>After</th></tr>${c.fields.map(f => `<tr><td class="mono">${esc(f.path)}</td><td>${histVal(f.before)}</td><td><b>${histVal(f.after)}</b></td></tr>`).join("")}</table>` : ""}</div>`).join("")}`).join("")
    : `<p class="muted">No configuration changes ${S.hist.sel ? "in this snapshot" : "in this period"}.</p>`;
  const ours = d.homeowl?.length ? `<div class="banner info" style="margin-top:12px">Made through HomeOwl in this period: ${d.homeowl.map(c => `${esc(c.description || c.target)} <span class="fine">(${esc(c.by)}, ${esc(c.time)})</span>`).join("; ")}. Anything else was changed in UniFi directly (or by an automatic update).</div>` : "";
  return `<div class="hist-grid">
    <div class="card"><h2>Snapshots</h2>
      <div class="btnrow" style="margin-bottom:10px">${HIST_AGO.map(([h, l]) => `<button class="btn sm ${!S.hist.sel && S.hist.since === h ? "primary" : ""}" data-histsince="${h}">${l}</button>`).join("")}</div>
      <div class="hist-list">${snaps.slice().reverse().map((e, i, all) => `<a href="#" class="hist-item ${S.hist.sel === e.file ? "on" : ""}" data-histsel="${esc(e.file)}">
        <span>${fmtTime(e.time * 1000)}</span><span class="fine">${i === all.length - 1 ? "first snapshot" : `${plural(e.changes, "change")}${e.areas?.length ? " · " + esc(e.areas.slice(0, 3).join(", ")) : ""}`}</span></a>`).join("")}</div>
      <p class="fine">Checked ${S.hist.list.checked ? ago(S.hist.list.checked * 1000) : "–"}. A new snapshot is kept only when something changed.</p></div>
    <div class="card"><h2>${esc(title)}</h2><div class="fine">${fmtTime(d.from.time * 1000)} → ${fmtTime(d.to.time * 1000)}</div>${ours}${diff}</div>
  </div>`;
}

document.addEventListener("click", e => {
  const s = e.target.closest("[data-histsince]"); if (s) { S.hist.sel = null; S.hist.since = +s.dataset.histsince; loadHistory(); return; }
  const p = e.target.closest("[data-histsel]"); if (p) { e.preventDefault(); S.hist.sel = p.dataset.histsel; loadHistory(); return; }
  if (e.target.id === "hist-reload") loadHistory();
});
{ const _render = render; render = function () { _render(); if (S.view === "confighistory") loadHistory(); }; }
