/* HomeOwl — device check-in ("Hi HomeOwl"): runs on the family member's own phone, tablet or laptop, without signing in. */
"use strict";

const $ = s => document.querySelector(s);
const esc = s => String(s ?? "").replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
const KINDS = [["phone", "📱 Phone"], ["tablet", "📱 Tablet"], ["laptop", "💻 Laptop"], ["computer", "🖥 Computer"], ["tv", "📺 TV / streamer"],
  ["console", "🎮 Game console"], ["speaker", "🔊 Speaker"], ["watch", "⌚ Watch"], ["camera", "📷 Camera"], ["printer", "🖨 Printer"], ["other", "Something else"]];

function guessKind() {
  const ua = navigator.userAgent;
  if (/iPad|Tablet/i.test(ua) || (/Macintosh/.test(ua) && navigator.maxTouchPoints > 1)) return "tablet";
  if (/iPhone|Android.+Mobile|Mobile/i.test(ua)) return "phone";
  if (/Android/i.test(ua)) return "tablet";
  if (/Macintosh|Windows|Linux|CrOS/i.test(ua)) return "laptop";
  return "other";
}

async function call(method, body) {
  const r = await fetch("/api/hi", { method, headers: { "Content-Type": "application/json" }, body: body ? JSON.stringify(body) : undefined, cache: "no-store" });
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(j.error || `Something went wrong (HTTP ${r.status})`);
  return j;
}

function done(again) {
  $("#hi-box").innerHTML = `<h2 style="margin:10px 0 6px">Thanks! 🦉</h2>
    <p>An admin will approve it soon. After that, this device shows up with its new name in UniFi and in HomeOwl.</p>
    ${again ? `<p class="fine">This device is already waiting for approval — sending again replaces that request.</p><button class="btn" id="hi-again">Change my answer</button>` : ""}`;
}

function platform() {
  const ua = navigator.userAgent;
  if (/iPhone|iPad|iPod/.test(ua) || (/Macintosh/.test(ua) && navigator.maxTouchPoints > 1)) return "ios";
  if (/Macintosh/.test(ua)) return "mac";
  if (/Android/i.test(ua)) return "android";
  if (/Windows/i.test(ua)) return "windows";
  return "other";
}

// iOS installs profiles only from Safari; other browsers (and apps' built-in browsers) just save the file
const notSafariIOS = () => /CriOS|FxiOS|EdgiOS|OPiOS|OPT\/|YaBrowser|DuckDuckGo|Ddg\/|GSA\/|FBAN|FBAV|Instagram|Line\/|Snapchat|LinkedInApp|Twitter/i.test(navigator.userAgent);

const copyRow = (v, label) => `<div class="cmd"><code>${esc(v)}</code><button class="btn sm" type="button" data-hicopy="${esc(v)}" aria-label="Copy ${esc(label)}">Copy</button></div>`;

// the device is approved: offer to set it up so it's recognised by name everywhere (devprofile.go)
function registered(st) {
  const r = st.registered, nd = r.nextdns, pf = platform();
  const apple = `<a class="btn primary" href="/api/hi/profile${pf === "ios" ? "?p=ios" : pf === "mac" ? "?p=mac" : ""}" download="HomeOwl.mobileconfig">Install the HomeOwl profile</a>`;
  const inc = r.includes || {}, wifi = inc.wifi || [], vpn = inc.vpn, extra = wifi.length || vpn;
  const holds = extra ? `<ul class="hi-holds">${nd ? `<li>Encrypted DNS through NextDNS, under this device's name</li>` : ""}
      ${wifi.length ? `<li>Wi-Fi: ${wifi.map(esc).join(", ")} — joins by itself</li>` : ""}
      ${vpn ? `<li>VPN: ${esc(vpn.name)}${vpn.on_demand ? " — turns on by itself away from home" : ""}${vpn.app ? ` (needs the ${esc(vpn.app)} app${vpn.ready === false ? "; an admin still has to add this device's key" : ""})` : ""}</li>` : ""}</ul>` : "";
  const how = {
    ios: notSafariIOS()
      ? `<div class="msg warn">iPhone and iPad install profiles only from <b>Safari</b>. This browser would just save the file.</div>
        <a class="btn primary" href="${esc("x-safari-" + location.href)}">Open this page in Safari</a>
        <p class="fine">If nothing happens, copy the link and paste it into Safari:</p>${copyRow(location.href, "link")}`
      : `<ol class="hi-steps"><li>Tap the button and choose <b>Allow</b>.</li><li>Open <b>Settings</b> › <b>Profile Downloaded</b> (near the top) › <b>Install</b>.</li></ol>${apple}
        <p class="fine">It also puts a <b>HomeOwl</b> icon on the Home Screen that opens this page, for updates later. It works at home.</p>`,
    mac: `<ol class="hi-steps"><li>Click the button to download the profile.</li><li>Open <b>System Settings</b> › <b>General</b> › <b>Device Management</b>, double-click <b>HomeOwl</b> and choose <b>Install</b>.</li></ol>${apple}`,
    android: nd && `<ol class="hi-steps"><li>Open <b>Settings</b> › <b>Network &amp; internet</b> › <b>Private DNS</b>.</li><li>Choose <b>Private DNS provider hostname</b> and paste:</li></ol>${copyRow(nd.android, "host name")}`,
    windows: nd && `<ol class="hi-steps"><li>Open <b>Settings</b> › <b>Network &amp; internet</b> › your connection › <b>DNS server assignment</b> › <b>Edit</b> › <b>Manual</b>.</li>
      <li>Turn on IPv4, set Preferred DNS to <code>${esc(nd.servers[0])}</code> and Alternate to <code>${esc(nd.servers[1])}</code>.</li>
      <li>For each, set <b>DNS over HTTPS</b> to <b>On (manual template)</b> and paste:</li></ol>${copyRow(nd.doh, "template")}`,
  };
  const setup = !nd && !r.apple_only ? `<p class="fine">An admin can also choose a NextDNS profile for this device; then this page offers to set it up.</p>`
    : `${r.update ? `<div class="msg warn hi-update"><b>Update ready.</b> This device's settings changed. ${pf === "android" ? "Replace the Private DNS host name with the one below." : "Install the profile again — it replaces the old one."}</div>` : ""}<h3 style="margin:16px 0 6px">${r.update ? "Install the update" : "Set up this device"}</h3>
      ${extra ? `<p class="muted">It sets up:</p>${holds}<p class="fine">You can remove it any time.${pf === "android" || pf === "windows" ? " Wi-Fi and VPN install on iPhone, iPad and Mac; here you get the NextDNS part." : ""}</p>`
        : `<p class="muted">So HomeOwl and NextDNS recognise it by name at home, on mobile data and on other Wi-Fi. Nothing else changes, there are no passwords in it, and you can remove it any time.</p>`}
      ${!nd ? (pf === "ios" || pf === "mac" ? how[pf] : `<p>Open this page on the iPhone, iPad or Mac to install it.</p>`)
        : how[pf] || `<p>iPhone, iPad or Mac: open this page on it. Android: Private DNS host name:</p>${copyRow(nd.android, "host name")}<p>Anything else: DNS-over-HTTPS address:</p>${copyRow(nd.doh, "address")}`}
      ${r.profile_at && (pf === "ios" || pf === "mac") ? `<p class="fine">Downloaded before on ${esc(new Date(r.profile_at * 1000).toLocaleDateString())}. Installing again replaces it.</p>` : ""}
      ${pf === "ios" || pf === "mac" ? `<p class="fine">If iCloud Private Relay is on, Safari's visits go through Apple instead and aren't seen by NextDNS.</p>` : ""}`;
  $("#hi-box").innerHTML = `<h2 style="margin:10px 0 6px">This is ${esc(r.name)} 🦉</h2>
    <p class="muted">${r.person ? `It belongs to <b>${esc(r.person)}</b>. ` : ""}HomeOwl knows this device.</p>
    ${setup}
    <p class="fine" style="margin-top:16px"><button class="btn ghost sm" id="hi-again" type="button">Not right? Check in again</button></p>`;
}

async function start() {
  let st;
  try { st = await call("GET"); } catch (err) { $("#hi-box").innerHTML = `<div class="msg err">${esc(err.message)}</div>`; return; }
  if (st.waiting && !start.force) return done(true);
  if (st.registered && !start.force) return registered(st);
  const d = st.device, kind = guessKind();
  $("#hi-box").innerHTML = `<p class="muted">Tell HomeOwl what this device is and whose it is. An admin approves it, then it's named on the network.</p>
    <div class="hi-seen fine">The network sees this device as <b>${esc(d.name || d.hostname || "an unnamed device")}</b>${d.vendor ? ` (${esc(d.vendor)})` : ""}${d.network ? ` on ${esc(d.network)}` : ""}.</div>
    <form id="hi-form" autocomplete="off">
      <label>Whose device is it?
        <select id="hi-person" required>
          <option value="">Choose…</option>
          ${st.people.filter(p => p.kind !== "group").map(p => `<option value="${esc(p.id)}">${esc(p.name)}</option>`).join("")}
          ${st.people.some(p => p.kind === "group") ? `<optgroup label="Shared by everyone">${st.people.filter(p => p.kind === "group").map(p => `<option value="${esc(p.id)}">${esc(p.name)}</option>`).join("")}</optgroup>` : ""}
          <option value="__new">${st.people.length ? "Someone else…" : "Type a name…"}</option>
        </select></label>
      <label id="hi-newwrap" hidden>Name<input id="hi-newname" maxlength="40" placeholder="e.g. Alex"></label>
      <label>What is it?<select id="hi-kind">${KINDS.map(([k, l]) => `<option value="${k}" ${k === kind ? "selected" : ""}>${l}</option>`).join("")}</select></label>
      <label>Name for this device<input id="hi-name" maxlength="40" required placeholder="e.g. Alex's iPad"></label>
      <button class="btn primary" type="submit">Send to HomeOwl</button>
      <div id="hi-msg" class="msg" role="status"></div>
    </form>`;
  if (!st.people.length) { $("#hi-person").value = "__new"; $("#hi-newwrap").hidden = false; }
}

function suggestName() {
  const sel = $("#hi-person"), who = sel.value === "__new" ? $("#hi-newname").value.trim() : sel.selectedOptions[0]?.textContent || "";
  const kind = (KINDS.find(k => k[0] === $("#hi-kind").value)?.[1] || "").replace(/^\S+\s/, "");
  const nameEl = $("#hi-name");
  if (who && !nameEl.dataset.typed && $("#hi-kind").value !== "other") nameEl.value = `${who}'s ${kind}`;
}

document.addEventListener("change", e => {
  if (e.target.id === "hi-person") { $("#hi-newwrap").hidden = e.target.value !== "__new"; if (e.target.value === "__new") $("#hi-newname").focus(); }
  if (e.target.id === "hi-person" || e.target.id === "hi-kind") suggestName();
});
document.addEventListener("input", e => {
  if (e.target.id === "hi-name") e.target.dataset.typed = "1";
  if (e.target.id === "hi-newname") suggestName();
});
document.addEventListener("click", async e => {
  if (e.target.id === "hi-again") { start.force = true; start(); }
  const c = e.target.closest("[data-hicopy]");
  if (c) {
    try { await navigator.clipboard.writeText(c.dataset.hicopy); c.textContent = "Copied"; }
    catch { const r = document.createRange(); r.selectNodeContents(c.previousElementSibling); getSelection().removeAllRanges(); getSelection().addRange(r); c.textContent = "Selected"; }
  }
});
document.addEventListener("submit", async e => {
  if (e.target.id !== "hi-form") return;
  e.preventDefault();
  const msg = $("#hi-msg"), sel = $("#hi-person").value;
  msg.className = "msg"; msg.textContent = "Sending…";
  try {
    await call("POST", { name: $("#hi-name").value, kind: $("#hi-kind").value, person_id: sel === "__new" ? "" : sel, person_name: sel === "__new" ? $("#hi-newname").value : "" });
    done(false);
  } catch (err) { msg.className = "msg err"; msg.textContent = err.message; }
});
start();
