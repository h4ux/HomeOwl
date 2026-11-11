/* HomeOwl — Cameras (UniFi Protect, read-only): live pictures, camera health, and a timeline of detections and doorbell rings.
   Depends on core.js and app.js */
"use strict";

S.cam = null;                                          // last /api/cameras answer (or the demo's)
S.camF = { days: 1, kind: "all", camera: "" };         // timeline filters

const CAM_KINDS = { person: "Person", vehicle: "Vehicle", package: "Package", animal: "Animal", face: "Face", licensePlate: "Licence plate",
  alrmSmoke: "Smoke alarm", alrmCmonx: "CO alarm", smoke_cmonx: "Smoke / CO alarm", alrmSiren: "Siren", alrmBabyCry: "Baby crying", alrmSpeak: "Speech",
  alrmBark: "Dog barking", alrmBurglar: "Burglar alarm", alrmCarHorn: "Car horn", alrmGlassBreak: "Glass breaking" };
const CAM_FILTERS = [["all", "Everything"], ["person", "People"], ["vehicle", "Vehicles"], ["package", "Packages"], ["animal", "Animals"],
  ["ring", "Doorbell"], ["audio", "Sounds"], ["motion", "Motion only"]];
const camDur = s => s < 60 ? `${Math.max(1, Math.round(s))}s` : fmtDur(s);
const kindName = k => CAM_KINDS[k] || String(k).replace(/^alrm/, "").replace(/([a-z])([A-Z])/g, "$1 $2");

function camEventLabel(e) {
  if (e.type === "ring") return "Doorbell ring";
  if (e.type === "motion") return "Motion";
  const k = (e.kinds || []).map(kindName).join(", ");
  if (e.type === "line") return `${k || "Something"} crossed a line`;
  return k || (e.type === "audio" ? "Sound" : "Detection");
}
const camEventTag = e => e.type === "ring" ? "warn" : e.type === "audio" ? "bad" : (e.kinds || []).some(k => k === "person" || k === "package") ? "info" : "";
function camMatches(e, f) {
  if (f.camera && e.camera !== f.camera) return false;
  if (f.kind === "all") return true;
  if (f.kind === "ring" || f.kind === "motion") return e.type === f.kind;
  if (f.kind === "audio") return e.type === "audio";
  return (e.kinds || []).includes(f.kind);
}

// demo / saved report: fictional cameras and events, times relative to now
function camFromBundle() {
  const p = S.bundle?.protect; if (!p) return { cameras: [], events: [], status: { state: "off" }, no_protect: true, demo: true };
  const now = Date.now();
  return { demo: true, status: p.status || { state: "live" }, cameras: arr(p.cameras),
    events: arr(p.events).map((e, i) => ({ id: "d" + i, ...e, start: now - e.ago * 1000, end: e.dur ? now - e.ago * 1000 + e.dur * 1000 : 0 }))
      .filter(e => e.start >= now - S.camF.days * 86400e3) };
}

V.cameras = () => `<div class="head"><div><h1>Cameras</h1><div class="muted">UniFi Protect at a glance: what the cameras see, whether they're healthy, and who came to the door. Read-only.</div></div>
  <div class="btnrow"><label class="check cam-livesw" title="Refresh the pictures every 2 seconds instead of every 15"><input type="checkbox" id="cam-live" ${S.camLive ? "checked" : ""}> Live</label><button class="btn sm" id="cam-refresh">Refresh pictures</button></div></div>
  <div id="cam-body"><div class="empty">Loading…</div></div>`;

async function loadCameras() {
  const el = $("#cam-body"); if (!el) return;
  if (S.source === "live") {
    try { S.cam = await api(`/api/cameras?days=${S.camF.days}`); }
    catch (err) { el.innerHTML = `<div class="card"><p class="muted">${esc(err.message)}</p></div>`; return; }
  } else S.cam = camFromBundle();
  if ($("#cam-body")) { $("#cam-body").innerHTML = camBody(S.cam); if (S.source === "live") refreshSnaps(true); }
}

function camPicture(c, demo) {
  const off = c.state !== "CONNECTED";
  if (off) return `<div class="cam-pic off"><span>Offline</span></div>`;
  if (demo) { // a drawn placeholder: the demo has no real cameras
    const h = [...c.id].reduce((a, ch) => a + ch.charCodeAt(0), 0) % 360;
    return `<div class="cam-pic"><svg viewBox="0 0 160 90" preserveAspectRatio="xMidYMid slice" role="img" aria-label="Demo picture">
      <rect width="160" height="90" fill="hsl(${h} 30% 62%)"/><rect y="58" width="160" height="32" fill="hsl(${(h + 90) % 360} 25% 38%)"/>
      <rect x="${20 + h % 60}" y="30" width="44" height="30" fill="hsl(${h} 15% 85%)"/><polygon points="${16 + h % 60},31 ${42 + h % 60},14 ${68 + h % 60},31" fill="hsl(${(h + 180) % 360} 30% 40%)"/>
      <circle cx="132" cy="18" r="8" fill="hsl(50 90% 85%)"/></svg><span class="cam-live">demo</span></div>`;
  }
  return `<div class="cam-pic"><img data-camsnap="${esc(c.id)}" alt="Latest picture from ${esc(c.name)}" hidden><span class="cam-wait" data-camerr="${esc(c.id)}">Loading picture…</span><span class="cam-live" data-camage="${esc(c.id)}"></span></div>`;
}

function camBody(r) {
  if (r.no_protect) return `<div class="card"><h2>No UniFi Protect here</h2><p>${r.demo ? "The demo has no cameras." : `This console doesn't run UniFi Protect, or Protect's API isn't reachable with HomeOwl's API key. Protect 5.3 or newer is needed.${r.error && !/isn't running/.test(r.error) ? ` <span class="fine">(${esc(r.error)})</span>` : ""}`}</p></div>`;
  if (r.error) return `<div class="card"><p class="muted">${esc(r.error)}</p></div>`;
  const cams = arr(r.cameras), ev = arr(r.events), byId = Object.fromEntries(cams.map(c => [c.id, c]));
  const online = cams.filter(c => c.state === "CONNECTED").length;
  const today = new Date(); today.setHours(0, 0, 0, 0);
  const evToday = ev.filter(e => e.start >= today.getTime());
  const count = k => evToday.filter(e => (e.kinds || []).includes(k)).length;
  const rings = evToday.filter(e => e.type === "ring");
  const last = {}; ev.forEach(e => { if (!last[e.camera] || e.start > last[e.camera].start) last[e.camera] = e; });
  const st = r.status || {};
  const rec = r.demo ? "" : st.state === "live" ? "" : `<div class="banner ${st.state === "error" ? "warn" : "info"}">${st.state === "error"
    ? `HomeOwl lost Protect's event stream (${esc(st.error || "unknown error")}) and keeps retrying. Detections that happen meanwhile aren't recorded.`
    : st.state === "off" ? `Protect's event stream isn't available${st.error ? ` (${esc(st.error)})` : ""}, so HomeOwl can't record detections. Pictures and health still work.`
    : "Connecting to Protect's event stream…"}</div>`;
  const ringsDays = r.days > 1 ? ev.filter(e => e.type === "ring").length : rings.length;
  return `${rec}
  <div class="grid g4">
    ${kpi(`${online}<span class="muted" style="font-size:18px">/${cams.length}</span>`, "Cameras online", cams.length - online ? `<span class="bad-t">${plural(cams.length - online, "camera")} offline</span>` : cams.length ? "all recording" : "")}
    ${kpi(count("person"), "People today", `${count("vehicle")} vehicles · ${count("package")} packages`)}
    ${kpi(rings.length, "Doorbell rings today", rings[0] ? `last ${ago(rings[0].start)}` : cams.some(c => c.doorbell) ? "" : "no doorbell")}
    ${kpi(evToday.length, "Events today", st.since && !r.demo ? `recording since ${fmtTime(st.since * 1000)}` : "")}
  </div>
  ${cams.length ? `<div class="cam-grid section">${cams.map(c => `<div class="card cam-card${c.state !== "CONNECTED" ? " is-off" : ""}" data-camopen="${esc(c.id)}" tabindex="0" role="button" aria-label="Show ${esc(c.name)} larger">
      ${camPicture(c, r.demo)}
      <div class="cam-meta"><b>${esc(c.name)}</b>${c.doorbell ? ` <span class="tag">doorbell</span>` : ""}
        <div class="fine">${last[c.id] ? `${esc(camEventLabel(last[c.id]))} · ${ago(last[c.id].start)}` : c.state === "CONNECTED" ? "nothing recorded yet" : "offline"}</div></div></div>`).join("")}</div>`
    : `<div class="card section"><p class="muted">Protect is running but has no cameras yet.</p></div>`}
  <div class="card section"><h2>Timeline</h2>
    <div class="cam-filters">
      <div class="seg">${[[1, "Today"], [7, "7 days"], [30, "30 days"]].map(([d, l]) => `<button class="${S.camF.days === d ? "on" : ""}" data-camdays="${d}">${l}</button>`).join("")}</div>
      <select id="cam-cam" aria-label="Camera"><option value="">All cameras</option>${cams.map(c => `<option value="${esc(c.id)}" ${S.camF.camera === c.id ? "selected" : ""}>${esc(c.name)}</option>`).join("")}</select>
      <div class="cam-kinds">${CAM_FILTERS.map(([k, l]) => `<button class="cam-kind${S.camF.kind === k ? " on" : ""}" data-camkind="${k}">${l}</button>`).join("")}</div>
    </div>
    <div id="cam-timeline">${camTimeline(ev, byId)}</div>
    ${r.truncated ? `<p class="fine">Showing the newest 3,000 events.</p>` : ""}
    <p class="fine">${r.demo ? "Demo data." : "HomeOwl records Protect's events while it runs and keeps them 30 days; the video itself stays in Protect."}${ringsDays && r.days > 1 ? ` ${plural(ringsDays, "doorbell ring")} in this period.` : ""}</p>
  </div>
  <div class="card section"><h2>Camera health</h2>
    ${table("cams", [
      { k: "name", label: "Camera", fmt: c => `${esc(c.name)} <span class="fine">${esc(c.model || "")}</span>`, sort: c => c.name },
      { k: "state", label: "State", fmt: c => c.state === "CONNECTED" ? `<span class="tag good">online</span>` : `<span class="tag bad">${esc((c.state || "offline").toLowerCase())}</span>` },
      { k: "smart", label: "Detects", fmt: c => (c.smart || []).length ? (c.smart || []).map(k => `<span class="tag">${esc(kindName(k))}</span>`).join(" ") : `<span class="fine">motion only</span>` },
      { k: "mode", label: "Video", fmt: c => esc([c.mode && c.mode !== "default" ? c.mode : "", c.hdr && c.hdr !== "off" ? `HDR ${c.hdr}` : ""].filter(Boolean).join(" · ") || "standard") },
      { k: "mic", label: "Microphone", fmt: c => c.mic ? "on" : "off" },
      { k: "last", label: "Last event", fmt: c => last[c.id] ? `${esc(camEventLabel(last[c.id]))} <span class="fine">${ago(last[c.id].start)}</span>` : "–", sort: c => -(last[c.id]?.start || 0) },
    ], cams, { empty: "No cameras" })}
    ${cams.some(c => c.state !== "CONNECTED") ? `<p class="fine">An offline camera records nothing. Check its cable or PoE port (Switch ports page) and that its switch is online.</p>` : ""}
  </div>`;
}

function camTimeline(ev, byId) {
  const l = ev.filter(e => camMatches(e, S.camF));
  if (!l.length) return `<p class="muted">${ev.length ? "Nothing matches these filters." : S.camF.days === 1 ? "No events today yet." : "No events in this period."}</p>`;
  const days = new Map();
  l.slice(0, 600).forEach(e => { const d = new Date(e.start).toDateString(); if (!days.has(d)) days.set(d, []); days.get(d).push(e); });
  const dayName = d => { const t = new Date(); if (d === t.toDateString()) return "Today"; t.setDate(t.getDate() - 1); return d === t.toDateString() ? "Yesterday" : new Date(d).toLocaleDateString(undefined, { weekday: "long", day: "numeric", month: "short" }); };
  return [...days].map(([d, es]) => `<div class="cam-day"><div class="cam-dayname">${esc(dayName(d))} <span class="fine">· ${plural(es.length, "event")}</span></div>
    ${es.map(e => `<div class="cam-ev"><span class="mono cam-t">${new Date(e.start).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" })}</span>
      <span class="tag ${camEventTag(e)}">${esc(camEventLabel(e))}</span>
      <span>${esc(byId[e.camera]?.name || "Unknown camera")}</span>
      ${e.end > e.start ? `<span class="fine">${camDur((e.end - e.start) / 1000)}</span>` : ""}</div>`).join("")}</div>`).join("")
    + (l.length > 600 ? `<p class="fine">And ${plural(l.length - 600, "older event")} — narrow the filters to see them.</p>` : "");
}

// larger picture in a modal (high quality, live only)
function openCamera(id) {
  const c = arr(S.cam?.cameras).find(x => x.id === id); if (!c) return;
  const live = !S.cam.demo && c.state === "CONNECTED";
  const body = !live ? camPicture(c, S.cam.demo)
    : `<div class="cam-pic big"><img id="cam-big" data-camid="${esc(c.id)}" alt="Picture from ${esc(c.name)}" hidden><span class="cam-wait" id="cam-big-msg">Loading picture…</span><span class="cam-live" id="cam-big-fps">live</span></div>`;
  const evs = arr(S.cam.events).filter(e => e.camera === id).slice(0, 8);
  openModal({ title: c.name, wide: true, body: `${body}
    <p class="fine">${esc(c.model || "")}${c.doorbell ? " · doorbell" : ""} · The video and recordings stay in UniFi Protect.</p>
    ${evs.length ? `<h3>Latest events</h3>${evs.map(e => `<div class="cam-ev"><span class="mono cam-t">${fmtTime(e.start)}</span><span class="tag ${camEventTag(e)}">${esc(camEventLabel(e))}</span></div>`).join("")}` : ""}` });
  if (live) camBigLoop(c.id);
}

// the big picture refreshes as fast as Protect answers (one request at a time) while the window is open
async function camBigLoop(id) {
  const img = $("#cam-big"); if (!img) return;
  let frames = 0; const t0 = Date.now(), times = [];
  while ($("#cam-big") === img && !$("#modal").hidden) {
    if (document.hidden) { await new Promise(r => setTimeout(r, 1000)); continue; }
    const started = Date.now();
    const r = await camFetch(id, true);
    if ($("#cam-big") !== img) { if (r.url) URL.revokeObjectURL(r.url); break; }
    if (r.url) {
      const old = img.src; img.src = r.url; img.hidden = false; if (old.startsWith("blob:")) URL.revokeObjectURL(old);
      $("#cam-big-msg").hidden = true; frames++; times.push(Date.now()); while (times.length > 6) times.shift();
      const fps = times.length > 1 ? (times.length - 1) / ((times[times.length - 1] - times[0]) / 1000) : 0;
      $("#cam-big-fps").textContent = fps ? `live · ${fps < 1 ? fps.toFixed(1) : Math.round(fps)} fps` : "live";
    } else { $("#cam-big-msg").hidden = false; $("#cam-big-msg").textContent = r.error; await new Promise(r => setTimeout(r, 3000)); }
    const wait = 700 - (Date.now() - started); if (wait > 0) await new Promise(r => setTimeout(r, wait));
  }
}

// pictures load through fetch, so a refusal from Protect shows its reason instead of a broken image.
// They refresh every 15 s, or every 2 s with Live on, while the page is open and visible; one request per camera at a time.
let camTimer = null;
const camBusy = new Set(), camLast = {};
try { S.camLive = localStorage.getItem("homeowl-cam-live") === "1"; } catch { S.camLive = false; }
async function camFetch(id, hq) {
  try {
    const r = await fetch(`api/cameras/snapshot?id=${encodeURIComponent(id)}${hq ? "&hq=1" : ""}&t=${Date.now()}`, { credentials: "same-origin" });
    if (!r.ok) { let m = `HTTP ${r.status}`; try { m = (await r.json()).error || m; } catch {} return { error: m }; }
    return { url: URL.createObjectURL(await r.blob()) };
  } catch (err) { return { error: "HomeOwl didn't answer: " + err.message }; }
}
function refreshSnaps(force) {
  const every = S.camLive ? 2000 : 15000;
  $$("img[data-camsnap]").forEach(async img => {
    const id = img.dataset.camsnap;
    if (camBusy.has(id) || (!force && Date.now() - (camLast[id] || 0) < every - 300)) return;
    camBusy.add(id); camLast[id] = Date.now();
    const r = await camFetch(id, false); camBusy.delete(id);
    const msg = $(`[data-camerr="${CSS.escape(id)}"]`), age = $(`[data-camage="${CSS.escape(id)}"]`);
    if (!img.isConnected) { if (r.url) URL.revokeObjectURL(r.url); return; }
    if (r.url) {
      const old = img.src; img.src = r.url; img.hidden = false; if (old.startsWith("blob:")) URL.revokeObjectURL(old);
      if (msg) msg.hidden = true; if (age) age.textContent = S.camLive ? "live" : "now";
    } else if (msg) { msg.hidden = false; msg.textContent = r.error; msg.classList.add("bad"); }
  });
}
function camTick() {
  if (S.view !== "cameras") { clearInterval(camTimer); camTimer = null; return; }
  if (!document.hidden && S.source === "live") refreshSnaps(false);
  $$("[data-camage]").forEach(el => { const id = el.dataset.camage; if (camLast[id] && !S.camLive) { const s = Math.round((Date.now() - camLast[id]) / 1000); el.textContent = s < 5 ? "now" : `${s}s ago`; } });
}
document.addEventListener("change", e => {
  if (e.target.id !== "cam-live") return;
  S.camLive = e.target.checked; try { localStorage.setItem("homeowl-cam-live", S.camLive ? "1" : "0"); } catch {}
  refreshSnaps(true);
});

document.addEventListener("click", e => {
  const d = e.target.closest("[data-camdays]"); if (d) { S.camF.days = +d.dataset.camdays; loadCameras(); return; }
  const k = e.target.closest("[data-camkind]");
  if (k) { S.camF.kind = k.dataset.camkind; $$("[data-camkind]").forEach(b => b.classList.toggle("on", b === k)); camRedrawTimeline(); return; }
  if (e.target.id === "cam-refresh") { if (S.source === "live") refreshSnaps(true); else loadCameras(); return; }
  const o = e.target.closest("[data-camopen]"); if (o) openCamera(o.dataset.camopen);
});
document.addEventListener("keydown", e => { const o = e.target.closest?.("[data-camopen]"); if (o && (e.key === "Enter" || e.key === " ")) { e.preventDefault(); openCamera(o.dataset.camopen); } });
document.addEventListener("change", e => { if (e.target.id === "cam-cam") { S.camF.camera = e.target.value; camRedrawTimeline(); } });
function camRedrawTimeline() {
  const el = $("#cam-timeline"); if (!el || !S.cam) return;
  el.innerHTML = camTimeline(arr(S.cam.events), Object.fromEntries(arr(S.cam.cameras).map(c => [c.id, c])));
}

// Settings: may viewers see the cameras?
function renderCameraSettings() {
  const el = $("#st-cameras"); if (!el || !S.settings) return;
  el.innerHTML = `<h2>Cameras</h2>
    <label class="check"><input type="checkbox" id="st-camviewers" ${S.settings.cameras?.viewers ? "checked" : ""}> Viewers can see the Cameras page (pictures and detections)</label>
    <p class="fine">Off: only admins see camera pictures. Pictures come straight from UniFi Protect and aren't stored by HomeOwl.</p>`;
}
document.addEventListener("change", async e => {
  if (e.target.id !== "st-camviewers") return;
  try { const se = await api("/api/settings", { cameras: { viewers: e.target.checked } }); S.settings.cameras = se.cameras; toast(e.target.checked ? "Viewers can see the cameras" : "Only admins can see the cameras", "ok"); }
  catch (err) { toast(err.message, "err"); e.target.checked = !e.target.checked; }
});

{ const _render = render; render = function () {
  _render();
  if (S.view === "cameras") { loadCameras(); if (!camTimer) camTimer = setInterval(camTick, 1000); }
}; }
