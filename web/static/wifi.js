/* HomeOwl — the guest Wi-Fi card at http://<name>.local (Settings › Access › Easy names): network, password and
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
  box.innerHTML = nets.map(n => `<div class="wifi-card">
      <div class="brand"><img class="logo-img lg" src="icon.png" alt=""> Join our Wi-Fi</div>
      <div class="wifi-qr">${QR.themed(QR.wifi(n), { theme: d.qr_theme || "rounded", size: 260, label: `Join ${n.ssid}`, caption: `${n.ssid} · scan to join` })}</div>
      <p class="muted">Point your phone's camera at the code and tap the Wi-Fi banner.</p>
      <dl class="wifi-kv"><dt>Network</dt><dd>${esc(n.ssid)}</dd>
        <dt>Password</dt><dd class="mono">${n.security === "nopass" ? "none, it's open" : esc(n.password)}</dd></dl>
    </div>`).join("");
}
loadWifi();
setInterval(loadWifi, 10 * 60e3); // a new password shows up without reloading
