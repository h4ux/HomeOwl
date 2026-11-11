/* HomeOwl — "is there a newer version?": sidebar notice, update dialog, Settings section. Depends on core.js and app.js */
"use strict";

S.update = null;
async function loadUpdate() {
  if (S.source !== "live" && S.source !== "none") return;
  try { S.update = await api("/api/update"); } catch { S.update = null; }
  renderUpdateChip();
}
function renderUpdateChip() {
  const el = $("#update-chip"); if (!el) return;
  const u = S.update;
  // always show the running version; with an update waiting, show where it goes
  el.hidden = !(u && u.current);
  if (!u || !u.current) return;
  el.classList.toggle("avail", !!u.available);
  el.innerHTML = u.available ? `⬆ Update available<span class="upd-v">${esc(u.current)} → <b>${esc(u.latest)}</b></span>`
    : `<span class="upd-v">HomeOwl <b>${esc(u.current)}</b>${u.dev_build ? " · dev build" : u.latest ? " · up to date" : ""}</span>`;
  el.title = u.available ? "See what's new and how to update" : "Check for updates";
}
const relNotes = t => esc(String(t || "").replace(/\r/g, "").slice(0, 4000)).replace(/^#+ ?(.*)$/gm, "<b>$1</b>").replace(/\*\*(.+?)\*\*/g, "$1");
function updateBody(u) {
  const admin = S.role === "admin";
  const cmds = (u.commands || []).map((c, i) => `<div class="cmd"><code>${esc(c)}</code><button class="btn sm" data-copy="${esc(c)}">Copy</button></div>${i === 0 && u.commands.length > 1 ? `<p class="fine">or reinstall with the installer, which also refreshes the background service:</p>` : ""}`).join("");
  return `<dl class="kv"><dt>You have</dt><dd>${esc(u.current)}${u.dev_build ? ` <span class="tag">development build</span>` : ""}</dd>
    <dt>Latest release</dt><dd>${u.latest ? `${esc(u.latest)}${u.published ? ` <span class="muted">· ${fmtTime(Date.parse(u.published))}</span>` : ""}` : "–"}</dd>
    <dt>Checked</dt><dd>${u.checked ? ago(u.checked * 1000) : "not yet"}${u.error ? ` <span class="tag bad" title="${esc(u.error)}">couldn't reach GitHub</span>` : ""}</dd></dl>
    ${u.available ? `${u.notes ? `<h3 style="margin-top:14px">What's new</h3><div class="relnotes">${relNotes(u.notes)}</div>` : ""}
      ${/^https:\/\/github\.com\//.test(u.url || "") ? `<p class="fine"><a href="${esc(u.url)}" target="_blank" rel="noopener noreferrer">Full release notes on GitHub ↗</a></p>` : ""}
      ${admin ? `<h3 style="margin-top:14px">${u.can_apply ? "Update now, or run this on the computer running HomeOwl" : "Run this on the computer running HomeOwl"}</h3>${cmds}
        ${u.can_apply ? "" : `<p class="fine">HomeOwl can't replace its own program file where it's installed (that needs administrator rights), so the update has to be started there. HomeOwl restarts by itself once the new version is in place.</p>`}`
      : `<p class="muted">Ask an admin to update HomeOwl.</p>`}` : `<p class="muted" style="margin-top:12px">${u.latest ? "You're running the latest version." : ""}</p>`}`;
}
function showUpdate() {
  const u = S.update || { current: "?", commands: [] }, admin = S.role === "admin";
  openModal({ title: "HomeOwl updates", body: `<div id="upd-body">${updateBody(u)}</div>`, buttons: [
    { label: "Close" },
    ...(admin ? [{ label: "Check again", onClick: checkUpdate }] : []),
    ...(admin && u.can_apply ? [{ label: `Update to ${u.latest}`, cls: "primary", onClick: applyUpdate }] : []),
  ] });
}
async function checkUpdate() {
  try { S.update = await api("/api/update/check", {}); renderUpdateChip(); showUpdate(); if (S.view === "settings") renderUpdateSettings(); }
  catch (err) { toast(err.message, "err"); }
}
async function applyUpdate() {
  const box = $("#upd-body"); if (box) box.innerHTML = `<div class="empty">Downloading ${esc(S.update.latest)} and checking it against the release checksums… HomeOwl restarts when it's done.</div>`;
  const want = S.update.latest;
  try { await api("/api/update/apply", {}); }
  catch (err) { if (box) box.innerHTML = `<div class="msg err">${esc(err.message)}</div>`; return; }
  if (box) box.innerHTML = `<div class="empty">Installed. Waiting for HomeOwl ${esc(want)} to start…</div>`;
  for (let i = 0; i < 60; i++) {
    await new Promise(r => setTimeout(r, 2000));
    try { const p = await (await fetch("/api/ping", { cache: "no-store" })).json(); if (p.version && p.version !== S.update.current) { location.reload(); return; } } catch { }
  }
  if (box) box.innerHTML = `<div class="msg err">HomeOwl hasn't come back yet. If it runs in a terminal, start it again; otherwise check its log.</div>`;
}
// Settings › Updates
function renderUpdateSettings() {
  const el = $("#st-updates"); if (!el || !S.settings) return;
  const u = S.update || {}, on = S.settings.updates?.check !== false;
  el.innerHTML = `<h2>Updates</h2>
    <p>${u.available ? `<span class="tag warn">${esc(u.latest)} is available</span> ` : u.latest ? `<span class="tag good">up to date</span> ` : ""}You have <b>${esc(u.current || S.settings.version)}</b>.${u.checked ? ` <span class="fine">Checked ${ago(u.checked * 1000)}.</span>` : ""}</p>
    <label class="check"><input type="checkbox" id="st-updcheck" ${on ? "checked" : ""}> Check for new versions automatically (asks GitHub twice a day; nothing about your network is sent)</label>
    <div class="btnrow" style="margin-top:10px"><button class="btn sm" id="st-updnow">Check now</button>${u.available ? `<button class="btn sm primary" id="st-updshow">Update…</button>` : ""}</div>`;
}
document.addEventListener("click", async e => {
  if (e.target.closest("#update-chip")) { e.preventDefault(); showUpdate(); return; }
  const c = e.target.closest("[data-copy]");
  if (c) { try { await navigator.clipboard.writeText(c.dataset.copy); toast("Copied", "ok"); } catch { toast("Select the command and copy it", "err"); } return; }
  if (e.target.id === "st-updnow") { e.target.disabled = true; await checkUpdate(); return; }
  if (e.target.id === "st-updshow") { showUpdate(); return; }
});
document.addEventListener("change", async e => {
  if (e.target.id !== "st-updcheck") return;
  try { const se = await api("/api/settings", { updates: { check: e.target.checked } }); S.settings.updates = se.updates; toast(e.target.checked ? "Update checks on" : "Update checks off", "ok"); }
  catch (err) { toast(err.message, "err"); e.target.checked = !e.target.checked; }
});
