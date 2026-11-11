/* HomeOwl — the macOS menu bar popover. HomeOwl opens this page inside its own menu bar window with a start-up
   token in the address (#t=…); the token only unlocks /api/menubar. Depends on core.js (data model) and qr.js. */
"use strict";

const MB = { token: "", d: null, guest: null, showGuest: false, busy: false };
try {
  const t = new URLSearchParams(location.hash.slice(1)).get("t");
  if (t) { sessionStorage.setItem("homeowl-mb", t); history.replaceState(null, "", location.pathname); }
  MB.token = t || sessionStorage.getItem("homeowl-mb") || "";
} catch { }

async function mbApi(path, body) {
  const r = await fetch(path, { method: body ? "POST" : "GET", headers: { "Content-Type": "application/json", "X-HomeOwl-Menubar": MB.token }, body: body ? JSON.stringify(body) : undefined });
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(j.error || `HTTP ${r.status}`);
  return j;
}
const mbDo = (what, extra = {}) => mbApi("/api/menubar/action", { do: what, ...extra });

const dot = cls => `<span class="mb-dot ${cls}" aria-hidden="true"></span>`;
const mins = s => { s = num(s); return s === null ? "" : s < 90 ? `${Math.round(s)} s` : `${Math.round(s / 60)} min`; };

// what the icon should say: 2 = internet or power is down, 1 = something to look at, 0 = all fine
function mbSummary(d) {
  const out = { level: 0, count: 0, rows: [] };
  const bump = (lvl, n = 1) => { out.level = Math.max(out.level, lvl); out.count += n; };
  if (d.bundle) {
    let m = null, f = [];
    try { m = normalise(d.bundle); f = analyse(m); } catch (e) { console.error(e); }
    if (m) {
      const links = arr(m.wanLinks).filter(l => l.enabled !== false);
      const down = links.filter(l => l.status === "down" || (l.active && /down|error|offline/i.test(l.status || "")));
      if (down.some(l => l.active) || (links.length && down.length === links.length)) bump(2);
      else if (down.length) bump(1, down.length);
      out.rows.push({ id: "internet", label: "Internet", cls: down.length ? (down.some(l => l.active) ? "bad" : "warn") : "ok",
        text: links.length ? links.map(l => `${l.name}${l.status === "down" ? " down" : `${l.latency !== null && l.latency !== undefined ? ` ${Math.round(l.latency)} ms` : " online"}`}`).join(" · ") : (m.info.wanStatus || "–") });
      const off = m.devices.filter(x => !x.online).length;
      const imp = f.filter(x => x.sev === "crit" || x.sev === "high").length;
      if (off) bump(1, off);
      if (imp) bump(1, imp);
      const bits = [off ? `${plural(off, "device")} offline` : `all ${m.devices.length} devices online`];
      if (imp) bits.push(`${plural(imp, "important suggestion")}`);
      out.rows.push({ id: "network", label: "Network", cls: off ? "warn" : imp ? "warn" : "ok", text: bits.join(" · "), view: off ? "devices" : imp ? "findings" : "overview" });
    }
  } else {
    out.rows.push({ id: "network", label: "Network", cls: "off", text: "not connected to UniFi", view: "settings" });
  }
  if (d.ups) {
    const u = arr(d.ups.ups).find(x => x.power) || arr(d.ups.ups)[0];
    if (d.ups.state === "error" || !u) out.rows.push({ id: "ups", label: "UPS", cls: "warn", text: d.ups.error || "can't reach the NUT server", view: "ups" });
    else {
      const onBatt = !!u.on_battery_since || /\bOB\b/.test(u.status || "");
      if (onBatt) bump(2); else if (u.comm_bad) bump(1);
      const bits = [onBatt ? "on battery" : u.comm_bad ? "no reading" : "on mains"];
      if (u.charge !== null && u.charge !== undefined) bits.push(`${Math.round(u.charge)}%`);
      if (onBatt && u.runtime) bits.push(`${mins(u.runtime)} left`);
      out.rows.push({ id: "ups", label: "UPS", cls: onBatt ? "bad" : u.comm_bad ? "warn" : "ok", text: bits.join(" · "), view: "ups" });
    }
  }
  if (d.checkins_pending) { bump(1, d.checkins_pending); }
  return out;
}

function mbRender() {
  const d = MB.d, el = $("#mb"); if (!d || !el) return;
  const sum = mbSummary(d), u = d.update || {}, st = d.startup || {};
  const people = arr(d.people);
  const g = MB.guest && arr(MB.guest.networks).find(x => x.enabled && x.security) || null;
  el.innerHTML = `<header class="mb-head"><img src="icon.png" alt="" class="mb-logo"><b>HomeOwl</b><span class="muted mb-ver">${esc(u.current || d.version || "")}</span>
      <button class="mb-icon" data-mbopen="settings" title="Settings" aria-label="Open Settings">⚙︎</button></header>
    <section class="mb-rows">${sum.rows.map(r => `<button class="mb-row" data-mbopen="${esc(r.view || "overview")}">${dot(r.cls)}<span class="mb-l">${esc(r.label)}</span><span class="mb-t">${esc(r.text)}</span></button>`).join("")}
      ${d.checkins_pending ? `<button class="mb-row" data-mbopen="checkins">${dot("warn")}<span class="mb-l">Check-ins</span><span class="mb-t">${esc(plural(d.checkins_pending, "device"))} waiting for you</span></button>` : ""}</section>
    ${people.length ? `<section class="mb-people" aria-label="Who's home">${people.map(p => `<span class="mb-person ${p.home ? "home" : ""}" title="${p.home ? "home" : "away"}"><i style="background:${/^#[0-9a-f]{3,8}$/i.test(p.color) ? p.color : "var(--muted)"}"></i>${esc(p.name)}<small>${p.home ? "home" : "away"}</small></span>`).join("")}</section>` : ""}
    ${MB.showGuest ? `<section class="mb-guest">${g ? `${QR.themed(QR.wifi({ ssid: g.ssid, password: g.password, security: g.security, hidden: g.hidden }), { theme: MB.guest.qr_theme || "rounded", size: 200, label: `Join ${g.ssid}`, caption: g.ssid })}
        <div class="mb-gtxt"><b>${esc(g.ssid)}</b>${g.password ? `<span class="mono">${esc(g.password)}</span>` : ""}</div>` : `<p class="muted">${esc(MB.guest?.error || "No guest network with a password found.")}</p>`}</section>` : ""}
    <section class="mb-acts">
      ${d.connected ? `<button class="btn sm ${MB.showGuest ? "act" : ""}" id="mb-guest">${MB.showGuest ? "Hide guest code" : "Guest Wi-Fi code"}</button>` : ""}
      <button class="btn sm primary" data-mbopen="">Open HomeOwl</button></section>
    <section class="mb-foot">
      <div class="mb-upd">${u.available ? `${dot("info")}<span>Update: ${esc(u.current)} → <b>${esc(u.latest)}</b></span>${u.can_apply && !d.read_only ? `<button class="btn sm primary" id="mb-upd-apply" ${u.installing ? "disabled" : ""}>${u.installing ? "Installing…" : "Update"}</button>` : `<button class="btn sm" data-mbopen="settings">How…</button>`}`
        : `<span class="muted">${u.dev_build ? "Development build" : "Up to date"}</span><button class="btn sm ghost" id="mb-upd-check">Check</button>`}</div>
      ${st.available ? (st.can_change ? `<label class="check mb-check"><input type="checkbox" id="mb-startup" ${st.enabled ? "checked" : ""}> Start HomeOwl ${esc(st.kind || "at login")}</label>`
        : `<p class="fine muted">${st.enabled ? `Starts ${esc(st.kind)}` : `Doesn't start ${esc(st.kind)} — see Settings`}</p>`) : ""}
    </section>`;
  // ask the menu bar to show the state, and fit the window to the content
  mbDo("status", { level: sum.level, count: sum.count, height: Math.ceil(el.getBoundingClientRect().height) }).catch(() => { });
}

async function mbLoad() {
  if (MB.busy) return; MB.busy = true;
  try { MB.d = await mbApi("/api/menubar" + (MB.showGuest ? "?guest=1" : "")); if (MB.showGuest) MB.guest = MB.d.guest ? MB.d.guest : { error: MB.d.guest_error }; mbRender(); }
  catch (e) { $("#mb").innerHTML = `<p class="muted mb-pad">${esc(e.message)}</p>`; }
  finally { MB.busy = false; }
}
// HomeOwl's native code calls this each time the window opens
window.homeowlShown = () => mbLoad();

document.addEventListener("click", async e => {
  const open = e.target.closest("[data-mbopen]");
  try {
    if (open) { await mbDo("open", { view: open.dataset.mbopen }); return; }
    if (e.target.id === "mb-guest") { MB.showGuest = !MB.showGuest; if (!MB.showGuest) MB.guest = null; await mbLoad(); return; }
    if (e.target.id === "mb-upd-check") { e.target.disabled = true; e.target.textContent = "Checking…"; MB.d.update = await mbDo("update-check"); mbRender(); return; }
    if (e.target.id === "mb-upd-apply") { e.target.disabled = true; e.target.textContent = "Installing…"; await mbDo("update-apply"); return; }
  } catch (err) { $("#mb").insertAdjacentHTML("beforeend", `<p class="bad-t fine mb-pad">${esc(err.message)}</p>`); }
});
document.addEventListener("change", async e => {
  if (e.target.id !== "mb-startup") return;
  try { MB.d.startup = await mbDo("startup", { on: e.target.checked }); mbRender(); }
  catch (err) { e.target.checked = !e.target.checked; $("#mb").insertAdjacentHTML("beforeend", `<p class="bad-t fine mb-pad">${esc(err.message)}</p>`); }
});

mbLoad();
setInterval(() => { if (!MB.showGuest) mbLoad(); }, 60000);
