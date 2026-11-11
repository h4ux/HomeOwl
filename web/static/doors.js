/* HomeOwl — Doors (UniFi Access): door and lock state, who came in (entry log), and a guarded remote unlock. Admins only.
   Depends on core.js and app.js */
"use strict";

S.doors = null;
const CRED = { NFC: "card", PIN_CODE: "PIN", MOBILE_TAP: "phone (tap)", MOBILE_BUTTON: "phone", FACE: "face", FINGERPRINT: "fingerprint",
  REMOTE: "remote", BUTTON: "exit button", QR_CODE: "QR code", TOUCH_PASS: "Touch Pass", WAVE: "wave", LICENSE_PLATE: "licence plate" };
const credName = c => CRED[c] || String(c || "").toLowerCase().replace(/_/g, " ");

V.doors = () => `<div class="head"><div><h1>Doors ${hint("UniFi Access: which doors are locked or open, who came in, and a remote unlock for when you're not home.")}</h1></div></div>
  <div id="doors-body"><div class="empty">Loading…</div></div>`;

function doorsFromBundle() {
  const a = S.bundle?.access; if (!a) return { demo: true, configured: false };
  const now = Date.now();
  return { demo: true, configured: true, unlock_allowed: false, doors: arr(a.doors), log: arr(a.log).map(e => ({ ...e, time: now - e.ago * 1000 })) };
}

async function loadDoors() {
  const el = $("#doors-body"); if (!el) return;
  if (S.source === "live") {
    if (S.role !== "admin") { el.innerHTML = `<div class="card"><p class="muted">Only admins can see and open the doors.</p></div>`; return; }
    try { S.doors = await api("/api/doors"); } catch (err) { el.innerHTML = `<div class="card"><p class="muted">${esc(err.message)}</p></div>`; return; }
  } else S.doors = doorsFromBundle();
  if ($("#doors-body")) $("#doors-body").innerHTML = doorsBody(S.doors);
}

function doorsConnectCard(d) {
  return `<div class="card"><h2>Connect UniFi Access</h2>
    <p>Access has its own API token (the UniFi API key HomeOwl already uses doesn't open it).</p>
    <ol class="steps"><li>Open <b>UniFi Access › Settings › General › Advanced › API Token</b> and create a token.</li>
      <li>Let it view doors and system logs; let it unlock doors only if you want the remote unlock.</li>
      <li>Paste it here. It's kept in the system keychain and never shown again.</li></ol>
    <label>API token<input id="dr-token" type="password" autocomplete="off" spellcheck="false"></label>
    <details class="adv"><summary>Access runs somewhere else</summary>
      <label>Address<input id="dr-host" placeholder="${esc(d.address || "https://192.168.1.1:12445")}" spellcheck="false"></label>
      <p class="fine">Default: your UniFi console on port 12445${d.address ? ` (${esc(d.address)})` : ""}.</p></details>
    <div class="btnrow"><button class="btn primary" id="dr-connect">Connect</button></div>
    ${d.error ? `<p class="bad-t">${esc(d.error)}</p>` : ""}</div>`;
}

function doorsBody(d) {
  if (d.demo && !d.configured) return `<div class="card"><p class="muted">The demo has no doors.</p></div>`;
  if (!d.configured) return doorsConnectCard(d);
  const admin = S.role === "admin" && !d.demo;
  if (d.error) return `<div class="banner warn">${esc(d.error)}</div>${admin ? doorsSettings(d) : ""}`;
  const doors = arr(d.doors), log = arr(d.log);
  const locked = doors.filter(x => x.lock === "locked").length, open = doors.filter(x => x.position === "open");
  const today = new Date(); today.setHours(0, 0, 0, 0);
  const inToday = log.filter(e => e.time >= today.getTime() && e.result === "ACCESS").length;
  const refused = log.filter(e => e.result && e.result !== "ACCESS");
  return `<div class="grid g4">
    ${kpi(`${locked}<span class="muted" style="font-size:18px">/${doors.length}</span>`, "Doors locked", doors.length - locked ? `<span class="warn-t">${plural(doors.length - locked, "door")} unlocked</span>` : doors.length ? "all locked" : "")}
    ${kpi(open.length, "Doors open", open.length ? esc(open.map(x => x.name).join(", ")) : doors.some(x => x.position !== "unknown") ? "all closed" : "no door sensors")}
    ${kpi(inToday, "Entries today")}
    ${kpi(refused.length, "Refused (7 days)", refused[0] ? `last ${ago(refused[0].time)} at ${esc(refused[0].door || "a door")}` : "none")}
  </div>
  <div class="card section"><h2>Doors</h2>
    ${table("doors", [
      { k: "name", label: "Door", fmt: x => `${esc(x.name)}${x.full_name && x.full_name !== x.name ? ` <span class="fine">${esc(x.full_name)}</span>` : ""}`, sort: x => x.name },
      { k: "lock", label: "Lock", fmt: x => x.lock === "locked" ? `<span class="tag good">locked</span>` : x.lock === "unlocked" ? `<span class="tag warn">unlocked</span>` : `<span class="tag">unknown</span>` },
      { k: "position", label: "Door", fmt: x => x.position === "open" ? `<span class="tag warn">open</span>` : x.position === "closed" ? `<span class="tag good">closed</span>` : `<span class="fine">no sensor</span>` },
      { k: "id", label: "", fmt: x => !admin ? "" : !d.unlock_allowed ? `<span class="fine">remote unlock off</span>` : !x.hub ? `<span class="fine">no hub</span>`
        : `<button class="btn sm" data-doorunlock="${esc(x.id)}">Unlock…</button>` },
    ], doors, { empty: "No doors in UniFi Access yet" })}
  </div>
  <div class="card section"><h2>Who came in <span class="muted" style="font-weight:400;font-size:13px">· last 7 days</span></h2>
    ${d.log_error ? `<p class="muted">${esc(d.log_error)}</p>` : table("doorlog", [
      { k: "time", label: "When", fmt: e => fmtTime(e.time), sort: e => -e.time },
      { k: "door", label: "Door", fmt: e => esc(e.door || "–") },
      { k: "who", label: "Who", fmt: e => e.who ? esc(e.who) : `<span class="fine">unknown</span>` },
      { k: "method", label: "With", fmt: e => esc(credName(e.method)) || "–" },
      { k: "result", label: "", fmt: e => e.result === "ACCESS" ? `<span class="tag good">let in</span>` : `<span class="tag bad" title="${esc(e.message || "")}">refused</span>` },
    ], log, { empty: "Nobody used a door in the last 7 days" })}
  </div>
  ${admin ? doorsSettings(d) : ""}`;
}

function doorsSettings(d) {
  return `<div class="card section"><h2>Access connection</h2>
    <p class="fine">Connected to <span class="mono">${esc(d.address || "")}</span>.</p>
    <label class="check"><input type="checkbox" id="dr-allow" ${d.unlock_allowed ? "checked" : ""}> Allow admins to unlock doors remotely</label>
    <p class="fine">Each unlock asks for confirmation, needs changes to be unlocked (sidebar), and is written to the Changes log with who did it. The door locks again by itself.</p>
    <div class="btnrow"><button class="btn sm" id="dr-disconnect">Disconnect</button></div></div>`;
}

async function unlockDoor(id) {
  const door = arr(S.doors?.doors).find(x => x.id === id); if (!door) return;
  let pv;
  try { pv = (await api("/api/doors/unlock", { id, dry_run: true })).preview; } catch (err) { toast(err.message, "err"); return; }
  openModal({ title: `Unlock ${door.name}?`, body: `<p>This opens <b>${esc(door.name)}</b> now, for the time set for this door in UniFi Access; then it locks again by itself.</p>
    <p class="fine">Make sure someone you trust is at the door. Logged as “${esc(pv.description)}”.</p>`,
    buttons: [{ label: "Cancel" }, { label: `Unlock ${door.name}`, cls: "primary", onClick: async () => { closeModal();
      try { await api("/api/doors/unlock", { id, dry_run: false }); toast(`${door.name} unlocked`, "ok"); setTimeout(loadDoors, 1500); }
      catch (err) { toast(err.message, "err"); }
    } }] });
}

document.addEventListener("click", async e => {
  const u = e.target.closest("[data-doorunlock]"); if (u) { unlockDoor(u.dataset.doorunlock); return; }
  if (e.target.id === "dr-connect") {
    e.target.disabled = true;
    try { S.doors = await api("/api/doors/connect", { token: $("#dr-token").value.trim(), host: $("#dr-host")?.value.trim() || "" }); toast("UniFi Access connected", "ok"); $("#doors-body").innerHTML = doorsBody(S.doors); }
    catch (err) { toast(err.message, "err"); e.target.disabled = false; }
    return;
  }
  if (e.target.id === "dr-disconnect") {
    openModal({ title: "Disconnect UniFi Access?", body: "<p>HomeOwl forgets the Access token. Nothing changes in UniFi Access.</p>",
      buttons: [{ label: "Cancel" }, { label: "Disconnect", cls: "primary", onClick: async () => { closeModal();
        try { S.doors = await api("/api/doors/disconnect", {}); $("#doors-body").innerHTML = doorsBody(S.doors); } catch (err) { toast(err.message, "err"); }
      } }] });
  }
});
document.addEventListener("change", async e => {
  if (e.target.id !== "dr-allow") return;
  try { const r = await api("/api/doors/settings", { unlock: e.target.checked }); S.doors.unlock_allowed = r.unlock_allowed; $("#doors-body").innerHTML = doorsBody(S.doors); toast(r.unlock_allowed ? "Remote unlock allowed" : "Remote unlock off", "ok"); }
  catch (err) { toast(err.message, "err"); e.target.checked = !e.target.checked; }
});

{ const _render = render; render = function () { _render(); if (S.view === "doors") loadDoors(); }; }
