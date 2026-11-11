/* HomeOwl — the guest Wi-Fi card at http://<address>/wifi and http://<name>.local (Settings › Access): network, password and
   a join code, big enough to leave open on a tablet by the door. No sign-in; only the guest networks. */
"use strict";

function esc(s) { return String(s ?? "").replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c])); }

async function loadWifi() {
  const box = document.getElementById("wifi-box");
  let d;
  try {
    const r = await fetch("/api/wifi", { cache: "no-store" });
    d = await r.json();
    if (!r.ok) throw new Error(d.error || "Not available");
  } catch (e) {
    box.innerHTML = `<div class="wifi-card"><div class="brand"><img class="logo-img lg" src="icon.png" alt=""> Guest Wi-Fi</div><p class="muted">${esc(e.message)}</p></div>`;
    return;
  }
  const nets = d.networks || [];
  if (!nets.length) { box.innerHTML = `<div class="wifi-card"><div class="brand"><img class="logo-img lg" src="icon.png" alt=""> Guest Wi-Fi</div><p class="muted">There's no guest Wi-Fi to show right now.</p></div>`; return; }
  // landscape screens (an Echo Show, a tablet on its side): code on the left, name and password on the right
  box.innerHTML = nets.map(n => `<div class="wifi-card">
      <div class="wifi-qr">${QR.themed(QR.wifi(n), { theme: d.qr_theme || "rounded", size: 260, label: `Join ${n.ssid}`, caption: `${n.ssid} · scan to join` })}</div>
      <div class="wifi-txt"><div class="brand"><img class="logo-img lg" src="icon.png" alt=""> Join our Wi-Fi</div>
      <dl class="wifi-kv"><dt>Network</dt><dd>${esc(n.ssid)}</dd>
        <dt>Password</dt><dd class="mono">${n.security === "nopass" ? "none, it's open" : esc(n.password)}</dd></dl>
      <p class="muted">Point your phone's camera at the code and tap the Wi-Fi banner.</p></div>
    </div>`).join("") + (d.checkin_url ? `<p class="wifi-hi"><a href="${esc(d.checkin_url)}">Family: register this device with HomeOwl ›</a></p>` : "");
}
loadWifi();
setInterval(loadWifi, 10 * 60e3); // a new password shows up without reloading
