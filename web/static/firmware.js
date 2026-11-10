/* HomeOwl — firmware watch: what's waiting to be updated, for how long, and what changed lately. */
"use strict";

S.fw = null;
V.firmware = m => {
  const devs = m.devices.slice().sort((a, b) => (b.upgradable - a.upgradable) || String(a.name).localeCompare(b.name));
  const waiting = devs.filter(d => d.upgradable);
  const pend = S.fw?.pending || {};
  const days = d => { const p = pend[d.mac]; return p?.since ? Math.max(0, Math.floor((Date.now() / 1000 - p.since) / 86400)) : null; };
  const auto = m.settings.autoUpgrade;
  const notes = `<a href="https://community.ui.com/releases" target="_blank" rel="noopener noreferrer">release notes ↗</a>`;
  return `<div class="head"><div><h1>Firmware</h1><div class="muted">Updates fix security holes and Wi-Fi bugs. Each device restarts while it updates (1–3 minutes offline).</div></div></div>
  <div class="grid g3">
    ${kpi(waiting.length, "Devices with an update", !waiting.length ? "all up to date" : waiting.some(d => days(d) !== null) ? `oldest waiting ${plural(Math.max(...waiting.map(d => days(d) ?? 0)), "day")}` : "")}
    ${kpi(esc(m.info.consoleVersion || "–"), "Console (UniFi OS)", m.info.updateAvailable ? `<span class="tag warn">update available</span> — update it in UniFi OS › Settings` : "")}
    ${kpi(esc(m.info.networkVersion || "–"), "Network app", `Automatic device updates: ${auto === null ? "–" : auto ? "on" : "off"}`)}
  </div>
  <div class="card section"><h2>Devices</h2>
    ${table("fw", [
      { k: "name", label: "Device", fmt: d => `<a href="#" data-dev="${esc(d.mac)}">${esc(d.name)}</a> <span class="fine">${esc(d.model || "")}</span>`, sort: d => d.name },
      { k: "role", label: "Type", fmt: d => roleName(d.role) },
      { k: "version", label: "Firmware", fmt: d => `<span class="mono">${esc(d.version || "–")}</span>` },
      { k: "upgradeTo", label: "Update", fmt: d => d.upgradable ? `<span class="tag warn">→ ${esc(d.upgradeTo || "newer")}</span>${days(d) !== null ? ` <span class="fine">waiting ${plural(days(d), "day")}</span>` : ""} <span class="fine">· ${notes}</span>` : `<span class="tag good">up to date</span>`, sort: d => d.upgradable ? 0 : 1 },
      { k: "online", label: "", fmt: d => !d.upgradable ? "" : !d.online ? `<span class="fine">offline</span>` : d.role === "gw" ? `<span class="fine" title="The gateway restarts the internet connection — start it in UniFi when nobody needs the internet">update in UniFi</span>` :
        actBtn({ kind: "devcmd", cmd: "upgrade", mac: d.mac, label: d.name, description: `Update ${d.name} to ${d.upgradeTo || "the latest firmware"} (restarts it)` }, "Update", `Update ${d.name}`) },
    ], devs, { empty: "No UniFi devices" })}
    <p class="fine">Tip: update access points one at a time, at a quiet hour, so the Wi-Fi never goes away completely.${(b => auto === false && b ? ` Or let UniFi do it: ${b}` : "")(actBtn({ kind: "setting", key: "mgmt", fields: { auto_upgrade: true }, description: "Turn on automatic device updates" }, "Turn on automatic updates", "Automatic updates"))}</p>
  </div>
  <div class="card section"><h2>Recent firmware changes <span class="muted" style="font-weight:400;font-size:13px">· last 30 days</span></h2><div id="fw-recent">${S.source === "live" ? `<div class="empty">Loading…</div>` : `<p class="muted">Kept by a running HomeOwl (from Config history).</p>`}</div></div>`;
};
async function loadFirmware() {
  if (S.source !== "live") return;
  const first = !S.fw;
  try { S.fw = await api("/api/firmware"); } catch { return; }
  if (first && S.view === "firmware") { render(); return; } // once: now the "waiting N days" can be shown
  const el = $("#fw-recent"); if (!el) return;
  const r = S.fw.recent || [];
  el.innerHTML = r.length ? `<table class="pv-t" style="width:100%"><tr><th>When</th><th>Device</th><th>From</th><th>To</th></tr>${r.slice().reverse().map(c => `<tr><td>${fmtTime(c.when * 1000)}</td><td>${esc(c.name)}</td><td class="mono">${esc(c.before ?? "–")}</td><td class="mono"><b>${esc(c.after ?? "–")}</b></td></tr>`).join("")}</table>`
    : `<p class="muted">No firmware changes recorded in the last 30 days.</p>`;
}
{ const _render = render; render = function () { _render(); if (S.view === "firmware") loadFirmware(); }; }
