/* HomeOwl — the guest Wi-Fi's sign-in page, when HomeOwl is UniFi's guest portal (portal.go). No signing in to
   HomeOwl: a Connect button, or the portal's password, then UniFi lets the device online. */
"use strict";

const $ = s => document.querySelector(s);
const esc = s => String(s ?? "").replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

async function call(method, body) {
  const r = await fetch("/api/welcome", { method, headers: { "Content-Type": "application/json" }, body: body ? JSON.stringify(body) : undefined, cache: "no-store" });
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(j.error || `Something went wrong (HTTP ${r.status})`);
  return j;
}

// where the device was going before the portal stepped in (only a plain web address)
function backTo() {
  const u = new URLSearchParams(location.search).get("url") || "";
  return /^https?:\/\/[^\s"'<>]+$/i.test(u) && !/captive|hotspot-detect|generate_204|connecttest|ncsi/i.test(u) ? u : "";
}

function online() {
  const u = backTo();
  $("#wl-box").innerHTML = `<h2 style="margin:10px 0 6px">You're online 🦉</h2><p class="muted">Enjoy the Wi-Fi. You can close this page.</p>
    ${u ? `<p><a class="btn primary" href="${esc(u)}">Continue</a></p>` : ""}`;
}

async function start() {
  let st;
  try { st = await call("GET"); } catch (err) { $("#wl-box").innerHTML = `<div class="msg err">${esc(err.message)}</div>`; return; }
  if (st.online) return online();
  $("#wl-box").innerHTML = `<p class="muted">You're on <b>${esc(st.ssid || "the guest Wi-Fi")}</b>.${st.password ? " Enter the Wi-Fi's sign-in password to go online." : " Tap Connect to go online."}</p>
    <form id="wl-form" autocomplete="off">
      ${st.password ? `<label>Password<input id="wl-pw" type="password" required autocomplete="off" autocapitalize="off" spellcheck="false"></label>` : ""}
      <button class="btn primary" type="submit">Connect</button>
      <div class="msg" id="wl-msg"></div>
    </form>`;
  if (st.password) $("#wl-pw").focus();
}

document.addEventListener("submit", async e => {
  if (e.target.id !== "wl-form") return;
  e.preventDefault();
  const b = e.target.querySelector("button"); b.disabled = true; $("#wl-msg").textContent = "";
  try { await call("POST", { password: $("#wl-pw")?.value || "" }); online(); }
  catch (err) { $("#wl-msg").textContent = err.message; $("#wl-msg").className = "msg err"; b.disabled = false; }
});

start();
