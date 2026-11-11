/* HomeOwl — Guest Wi-Fi: network name, password and a join QR code; printable card; new password through the allow-list.
   Depends on core.js, app.js and qr.js */
"use strict";

S.guest = null;
V.guest = () => `<div class="head"><div><h1>Guest Wi-Fi ${hint("Let visitors join with a scan: show this page or print a card for the fridge.")}</h1></div></div>
  <div id="guest-body"><div class="empty">Loading…</div></div>`;

// demo / saved report: guest SSIDs straight from the bundle (live passwords come from the server, which only sends guest ones)
function guestFromBundle() {
  const nets = Object.fromEntries(arr(S.bundle?.legacy?.networks).map(n => [n._id, n]));
  return {
    networks: arr(S.bundle?.legacy?.wlans).filter(w => w.is_guest || nets[w.networkconf_id]?.purpose === "guest").map(w => ({
      id: w._id, ssid: w.name, password: typeof w.x_passphrase === "string" ? w.x_passphrase : "", hidden: !!w.hide_ssid, enabled: w.enabled !== false,
      security: w.security === "open" ? "nopass" : w.security === "wpapsk" ? (w.wpa3_support && !w.wpa3_transition ? "SAE" : "WPA") : "", can_rotate: false,
    })),
  };
}

async function loadGuest() {
  const el = $("#guest-body"); if (!el) return;
  if (S.source === "live") {
    try { S.guest = await api("/api/guest-wifi"); } catch (err) { el.innerHTML = `<div class="empty bad-t">${esc(err.message)}</div>`; return; }
  } else S.guest = guestFromBundle();
  if ($("#guest-body")) $("#guest-body").innerHTML = guestBody(S.guest);
}

const guestCode = g => QR.wifi({ ssid: g.ssid, password: g.password, security: g.security, hidden: g.hidden });
function guestBody(gw) {
  const admin = S.role === "admin" && S.source === "live";
  const nets = gw.networks || [];
  const pick = admin ? `<details class="card section"><summary><b>Which Wi-Fi networks are for guests?</b> <span class="fine">UniFi's guest networks are found automatically; add others here.</span></summary>
      <div style="margin-top:10px">${(gw.all || []).map(w => `<label class="check"><input type="checkbox" data-guestpick="${esc(w.id)}" ${w.picked || w.guest ? "checked" : ""} ${w.auto ? "disabled title=\"A guest network in UniFi\"" : ""}> ${esc(w.ssid)}${w.auto ? ` <span class="tag">guest in UniFi</span>` : ""}</label>`).join("") || `<p class="muted">No Wi-Fi networks.</p>`}</div></details>` : "";
  if (!nets.length) return `<div class="card"><h2>No guest Wi-Fi yet</h2><p>Create a guest Wi-Fi in UniFi (Settings → WiFi → Create New, with “Guest network” turned on) — it keeps visitors away from your own devices.${admin ? " Or pick an existing Wi-Fi below." : ""}</p></div>${pick}`;
  return `<div class="guest-grid">${nets.map(g => {
    const qr = g.security ? QR.themed(guestCode(g), { theme: guestTheme(), size: 220, label: `Join ${g.ssid}`, caption: `${g.ssid} · scan to join` }) : `<div class="empty">This Wi-Fi uses individual logins (enterprise), so there's no shared password to put in a code.</div>`;
    return `<div class="card guest-card">
      <div class="guest-qr">${qr}</div>
      <div class="guest-info">
        <div class="l">Network</div><div class="guest-ssid">${esc(g.ssid)}${!g.enabled ? ` <span class="tag bad">turned off</span>` : ""}${g.hidden ? ` <span class="tag" title="Hidden networks don't show up in the Wi-Fi list; the code still works">hidden</span>` : ""}</div>
        <div class="l">Password</div><div class="guest-pass mono">${g.security === "nopass" ? `<span class="muted">none — open network</span>` : g.password ? esc(g.password) : "–"}</div>
        <p class="fine">Point a phone's camera at the code and tap the Wi-Fi banner to join.</p>
        <div class="btnrow">${g.security ? `<button class="btn sm primary" data-guestprint="${esc(g.id)}">Print card</button>` : ""}
          ${g.password ? `<button class="btn sm" data-copy="${esc(g.password)}">Copy password</button>` : ""}
          ${admin && g.can_rotate ? `<button class="btn sm" data-guestrotate="${esc(g.id)}">New password…</button>` : ""}</div>
      </div></div>`;
  }).join("")}</div>${nets.some(g => g.security) ? guestThemes(admin) : ""}${pick}`;
}

// QR style: saved in the settings by an admin (live), or just for this browser (demo)
const guestTheme = () => S.guest?.qr_theme || S.qrTheme || "rounded";
function guestThemes(admin) {
  if (S.source === "live" && !admin) return "";
  const cur = guestTheme(), code = QR.wifi({ ssid: "Guests", password: "example-only", security: "WPA" });
  return `<div class="card section"><h2>Code style</h2><p class="fine">Every style scans on iPhone and Android cameras. The printed card uses the same style.</p>
    <div class="qr-themes">${QR.THEMES.map(t => `<button class="qr-theme${t.id === cur ? " on" : ""}" data-qrtheme="${t.id}" aria-pressed="${t.id === cur}">
      <span class="qr-mini">${QR.themed(code, { theme: t.id, size: 72, label: t.name, caption: "Scan to join" })}</span><span>${esc(t.name)}</span></button>`).join("")}</div></div>`;
}

function rotateGuest(id) {
  const g = S.guest.networks.find(x => x.id === id); if (!g) return;
  openModal({ title: `New password for ${g.ssid}`, body: `<p>Everyone using ${esc(g.ssid)} has to join again with the new password (old printed cards stop working). Your own Wi-Fi isn't affected.</p>
    <label>New password<div class="ovr" style="grid-template-columns:1fr auto;margin-top:5px"><input id="gp-new" class="mono" minlength="8" maxlength="63" autocomplete="off"><button class="btn sm" id="gp-gen" type="button">Suggest another</button></div></label>
    <p class="fine">8–63 characters. You'll see a preview of the change before anything is sent to the console, and it can be undone from the Changes page.</p>`,
    buttons: [{ label: "Cancel" }, { label: "Preview change", cls: "primary", onClick: () => {
      const pw = $("#gp-new").value.trim();
      if (pw.length < 8 || pw.length > 63) return toast("The password must be 8 to 63 characters", "err");
      runActions([{ kind: "guestpass", wlan_id: id, password: pw, description: `New guest Wi-Fi password for ${g.ssid}` }], "Change the guest Wi-Fi password");
    } }] });
  suggestGuestPass();
}
async function suggestGuestPass() { try { const r = await api("/api/guest-wifi/suggest", {}); if ($("#gp-new")) $("#gp-new").value = r.password; } catch (err) { toast(err.message, "err"); } }

// printable card: only #print-card is printed (see style.css)
function printGuestCard(id) {
  const g = S.guest.networks.find(x => x.id === id); if (!g) return;
  let pc = $("#print-card"); if (!pc) { pc = document.createElement("div"); pc.id = "print-card"; document.body.append(pc); }
  pc.innerHTML = `<div class="pc">
    <div class="pc-title">Wi-Fi for our guests</div>
    ${QR.themed(guestCode(g), { theme: guestTheme(), size: 260, label: `Join ${g.ssid}`, caption: g.ssid })}
    <div class="pc-hint">Scan with your phone's camera to join</div>
    <dl><dt>Network</dt><dd>${esc(g.ssid)}</dd>${g.password ? `<dt>Password</dt><dd class="mono">${esc(g.password)}</dd>` : ""}</dl>
    <div class="pc-foot"><img src="icon.png" alt="" width="18" height="18"> Made with HomeOwl · github.com/h4ux/HomeOwl</div></div>`;
  document.body.classList.add("printing-card");
  const done = () => { document.body.classList.remove("printing-card"); window.removeEventListener("afterprint", done); };
  window.addEventListener("afterprint", done);
  const img = pc.querySelector("img");
  const go = () => window.print();
  if (img && !img.complete) { img.onload = go; img.onerror = go; } else go();
}

document.addEventListener("click", e => {
  const p = e.target.closest("[data-guestprint]"); if (p) { printGuestCard(p.dataset.guestprint); return; }
  const r = e.target.closest("[data-guestrotate]"); if (r) { rotateGuest(r.dataset.guestrotate); return; }
  if (e.target.id === "gp-gen") suggestGuestPass();
  const th = e.target.closest("[data-qrtheme]");
  if (th) {
    const id = th.dataset.qrtheme;
    if (S.source === "live") api("/api/guest-wifi/theme", { theme: id }).then(r => { S.guest.qr_theme = r.qr_theme; $("#guest-body").innerHTML = guestBody(S.guest); }).catch(err => toast(err.message, "err"));
    else { S.qrTheme = id; $("#guest-body").innerHTML = guestBody(S.guest); }
  }
});
document.addEventListener("change", async e => {
  if (!e.target.matches("[data-guestpick]")) return;
  const ids = $$("[data-guestpick]").filter(x => x.checked && !x.disabled).map(x => x.dataset.guestpick);
  try { await api("/api/guest-wifi/pick", { ids }); loadGuest(); } catch (err) { toast(err.message, "err"); }
});

{ const _render = render; render = function () { _render(); if (S.view === "guest") loadGuest(); }; }
