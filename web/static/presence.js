/* HomeOwl — Family › Who's home: a person is home while one of their tagged devices (usually a phone) is on the
   home network. Admins tag devices; arrivals and departures can be kept as history. Depends on core.js, app.js. */
"use strict";

S.prDays = 1;
S.pr = null;

V.presence = () => `<div class="head"><div><h1>Who's home ${hint("Someone is home while their phone (or another device you tag) is connected to the home Wi-Fi. Worked out on this computer only.")}</h1></div></div>
  <div id="pr-body"><div class="empty">Loading…</div></div>`;

// demo: two fictional people with the demo's first wireless clients
function prFromBundle() {
  const w = arr(S.model?.clients).filter(c => !c.wired), now = Date.now(), h = 3600e3;
  const people = [
    { id: "d1", name: "Alex", color: "#2f6fde", devices: w[0] ? [w[0].mac] : [], state: { home: true, since: now - 1.5 * h, device: w[0]?.mac, device_name: w[0]?.label } },
    { id: "d2", name: "Sam", color: "#e8577e", devices: w[1] ? [w[1].mac] : [], state: { home: false, since: now - 2 * h, device: w[1]?.mac, device_name: w[1]?.label, last_seen: now - 2 * h } },
    { id: "d3", name: "Robin", color: "#13a49b", devices: [] },
  ];
  const ev = [
    { t: now - 6 * h, person: "d1", name: "Alex", kind: "leave", device_name: w[0]?.label },
    { t: now - 5.5 * h, person: "d2", name: "Sam", kind: "leave", device_name: w[1]?.label },
    { t: now - 3 * h, person: "d2", name: "Sam", kind: "arrive", device_name: w[1]?.label },
    { t: now - 2 * h, person: "d2", name: "Sam", kind: "leave", device_name: w[1]?.label },
    { t: now - 1.5 * h, person: "d1", name: "Alex", kind: "arrive", device_name: w[0]?.label },
  ];
  return { demo: true, people, events: ev, history: true, days: S.prDays, keep_days: 90, away_minutes: 10, admin: true, connected: true };
}

async function loadPresence() {
  const el = $("#pr-body"); if (!el) return;
  if (S.source === "live") {
    try { S.pr = await api(`/api/presence?days=${S.prDays}`); }
    catch (err) { el.innerHTML = `<div class="card"><p class="muted">${esc(err.message)}</p></div>`; return; }
  } else S.pr = prFromBundle();
  prRedraw();
}
const prRedraw = () => { if ($("#pr-body") && S.pr) $("#pr-body").innerHTML = prBody(S.pr); };

// names and online state of UniFi clients, for the device pickers
function prDevices() {
  const m = new Map();
  arr(S.model?.known).forEach(k => m.set(k.mac, { mac: k.mac, label: k.label || k.mac, vendor: k.vendor, online: !!k.online }));
  arr(S.model?.clients).forEach(c => m.set(c.mac, { ...(m.get(c.mac) || {}), mac: c.mac, label: c.label || m.get(c.mac)?.label || c.mac, vendor: c.vendor || m.get(c.mac)?.vendor, online: true }));
  return m;
}
const privateMac = mac => /^.[26ae]/i.test(mac || "");
const prTime = t => new Date(t).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
const prDur = ms => { const m = Math.round(ms / 60e3); return m < 60 ? `${m} min` : m < 1440 ? `${Math.floor(m / 60)} h${m % 60 ? ` ${m % 60} min` : ""}` : `${Math.round(m / 1440)} days`; };
function prWhen(t) {
  const d = new Date(t), today = new Date(); today.setHours(0, 0, 0, 0);
  if (t >= today.getTime()) return prTime(t);
  if (t >= today.getTime() - 86400e3) return `yesterday ${prTime(t)}`;
  return `${d.toLocaleDateString([], { weekday: "short", day: "numeric", month: "short" })} ${prTime(t)}`;
}

// home intervals for one person in [from, to]: from the history, ending with the current state
function prSpans(p, events, from, to) {
  const ev = events.filter(e => e.person === p.id && e.t <= to);
  let home = ev.length ? ev[0].kind === "leave" : !!p.state?.home, t = from;
  const spans = [];
  for (const e of ev) {
    if (e.t < from) { home = e.kind === "arrive"; continue; }
    if (home && e.kind === "leave") spans.push([t, e.t]);
    if (e.kind === "arrive") t = e.t;
    home = e.kind === "arrive";
  }
  if (home) spans.push([Math.max(t, from), to]);
  return spans.map(([a, b]) => [Math.max(a, from), Math.min(b, to)]).filter(([a, b]) => b > a);
}

function prStrip(p, events) {
  const d0 = new Date(); d0.setHours(0, 0, 0, 0); const from = d0.getTime(), day = 86400e3, now = Date.now();
  const spans = prSpans(p, events, from, now);
  const total = spans.reduce((s, [a, b]) => s + b - a, 0);
  return `<div class="pr-strip" role="img" aria-label="${esc(p.name)} at home today: ${prDur(total)}">
      ${spans.map(([a, b]) => `<i style="left:${((a - from) / day * 100).toFixed(2)}%;width:${Math.max(0.4, (b - a) / day * 100).toFixed(2)}%;background:${esc(p.color || "var(--ok)")}"></i>`).join("")}
      <b style="left:${((now - from) / day * 100).toFixed(2)}%"></b></div>
    <div class="pr-ticks fine"><span>0:00</span><span>6:00</span><span>12:00</span><span>18:00</span><span>24:00</span></div>`;
}

function prBody(d) {
  const people = arr(d.people), tagged = people.filter(p => arr(p.devices).length), devs = prDevices(), now = Date.now();
  const ev = arr(d.events), admin = !!d.admin && (S.role === "admin" || d.demo);
  if (!people.length) return `<div class="card"><h2>No people yet</h2><p>Add the family first under <a href="#" data-goto="an-people">Family › People & groups</a>, then come back to tag each person's phone.</p></div>`;
  const card = p => {
    const st = p.state, home = !!st?.home, dev = st?.device_name || devs.get(st?.device)?.label || "";
    const sub = !arr(p.devices).length ? "no device tagged yet" : !st ? "watching…" :
      home ? `home since ${prWhen(st.since)} · ${prDur(now - st.since)}` : `away since ${prWhen(st.since)}${st.last_seen ? "" : ""}`;
    return `<div class="card pr-card ${home ? "home" : arr(p.devices).length && st ? "away" : "none"}">
      <span class="pr-ava" style="background:${esc(p.color || "var(--muted)")}">${esc((p.name || "?").slice(0, 1).toUpperCase())}</span>
      <div class="pr-who"><b>${esc(p.name)}</b><span class="fine muted">${esc(sub)}</span>${dev && arr(p.devices).length ? `<span class="fine muted">${home ? "via" : "last seen on"} ${esc(dev)}</span>` : ""}</div>
      ${arr(p.devices).length && st ? `<span class="tag ${home ? "good" : ""}">${home ? "Home" : "Away"}</span>` : ""}</div>`;
  };
  const homeN = tagged.filter(p => p.state?.home).length;
  return `${!tagged.length ? `<div class="card section"><h2>Tag a phone to start</h2><p>Pick each person's phone below. From then on they count as <b>home</b> whenever it's connected to your Wi-Fi, and as away ${esc(d.away_minutes)} minutes after it's gone (phones drop Wi-Fi now and then while asleep).</p></div>` : ""}
    ${tagged.length ? `<p class="pr-sum">${homeN ? `<b>${homeN}</b> of ${tagged.length} home` : "Nobody is home"}${!d.connected ? ` <span class="warn-t fine">· HomeOwl can't see UniFi right now, so this may be out of date</span>` : ""}</p>` : ""}
    <div class="pr-grid">${[...tagged, ...(admin ? people.filter(p => !arr(p.devices).length) : [])].map(card).join("")}</div>

    ${d.history && tagged.length ? `<div class="card section"><h2>Today</h2>
      ${tagged.map(p => `<div class="pr-row"><span class="pr-name">${esc(p.name)}</span><div class="pr-bar">${prStrip(p, ev)}</div></div>`).join("")}</div>
    <div class="card section"><div class="head pr-head"><h2>History</h2><div class="seg">${[[1, "Today"], [7, "7 days"], [30, "30 days"]].map(([n, l]) => `<button class="${S.prDays === n ? "on" : ""}" data-prdays="${n}">${l}</button>`).join("")}</div></div>
      ${prHistory(d, tagged, ev)}</div>` : ""}

    ${admin ? prAdmin(d, people, devs) : ""}`;
}

function prHistory(d, tagged, ev) {
  const to = Date.now(), d0 = new Date(); d0.setHours(0, 0, 0, 0);
  const from = S.prDays === 1 ? d0.getTime() : to - S.prDays * 86400e3;
  const totals = tagged.map(p => [p, prSpans(p, ev, from, to).reduce((s, [a, b]) => s + b - a, 0)]);
  const list = ev.slice().reverse();
  let lastDay = "";
  return `<div class="pr-totals">${totals.map(([p, ms]) => `<div><span class="fine muted">${esc(p.name)} at home</span><b>${prDur(ms)}</b><span class="fine muted">${S.prDays > 1 ? `≈ ${prDur(ms / S.prDays)} a day` : "today"}</span></div>`).join("")}</div>
    ${list.length ? `<div class="pr-events">${list.map(e => {
      const day = new Date(e.t).toDateString(), head = day !== lastDay ? `<div class="pr-day">${esc(new Date(e.t).toLocaleDateString([], { weekday: "long", day: "numeric", month: "long" }))}</div>` : "";
      lastDay = day;
      return `${head}<div class="pr-ev"><span class="mono">${prTime(e.t)}</span><span class="pr-dot ${e.kind}"></span><span><b>${esc(e.name)}</b> ${e.kind === "arrive" ? "came home" : "left"}${e.device_name ? ` <span class="fine muted">· ${esc(e.device_name)}</span>` : ""}</span></div>`;
    }).join("")}</div>` : `<p class="muted">No arrivals or departures in this period.</p>`}`;
}

function prAdmin(d, people, devs) {
  return `<div class="card section"><h2>Devices that say someone is home ${hint("Tag the device each person takes with them, usually their phone. A tagged device also counts as theirs in Analytics.")}</h2>
    ${people.map(p => `<div class="pr-tagrow"><span class="pr-name"><i class="pr-swatch" style="background:${esc(p.color || "var(--muted)")}"></i>${esc(p.name)}</span>
      <div class="pr-tags">${arr(p.devices).map(mac => { const x = devs.get(mac); return `<span class="pr-tag${x?.online ? " on" : ""}">${cliIcon({ name: x?.label, vendor: x?.vendor }, 16)}<span>${esc(x?.label || mac)}${privateMac(mac) ? ` <span class="fine warn-t" title="A private Wi-Fi address. Keep it fixed for your home Wi-Fi, or HomeOwl loses track when the phone changes it.">private</span>` : ""}</span>${canRename(mac) && !d.demo ? `<button class="x" data-rename="${esc(mac)}" title="Rename" aria-label="Rename ${esc(x?.label || mac)}">✎</button>` : ""}<button class="x" data-pruntag="${esc(mac)}" aria-label="Untag ${esc(x?.label || mac)}">×</button></span>`; }).join("")}
        <button class="btn sm" data-prpick="${esc(p.id)}">+ Device</button></div></div>`).join("")}
    <p class="fine">iPhone and Android use a <b>private Wi-Fi address</b> per network. That's fine while it stays the same: on iPhone set <i>Settings › Wi-Fi › (your network) › Private Wi-Fi Address</i> to <b>Fixed</b> (not Rotating); on Android keep “Use randomized MAC” per network, not per connection.</p></div>

  <div class="card section"><h2>History settings</h2>
    <label class="check"><input type="checkbox" id="pr-history" ${d.history ? "checked" : ""}> Keep a history of arrivals and departures</label>
    <div class="grid g3"><label>Keep it for<select id="pr-keep" ${d.history ? "" : "disabled"}>${[7, 30, 90, 180, 365].map(n => `<option value="${n}" ${d.keep_days === n ? "selected" : ""}>${n} days</option>`).join("")}</select></label>
      <label>Away after<span class="ups-in"><input type="number" id="pr-away" min="2" max="120" value="${esc(d.away_minutes)}"> min without a device</span></label></div>
    <p class="fine">The history stays on this computer (<span class="mono">presence.json</span> in HomeOwl's data folder). Turning it off deletes it.</p>
    ${d.history ? `<div class="btnrow"><button class="btn sm" id="pr-clear">Clear history…</button></div>` : ""}</div>`;
}

async function prPost(path, body, msg) {
  if (S.pr?.demo) { toast("The demo doesn't save changes", ""); return; }
  try { S.pr = await api(path, body); if (S.prDays !== 1) await loadPresence(); else prRedraw(); if (msg) toast(msg, "ok"); }
  catch (err) { toast(err.message, "err"); prRedraw(); }
}

document.addEventListener("change", e => {
  if (!e.target.closest("#pr-body")) return;
  if (e.target.id === "pr-history") {
    if (!e.target.checked && arr(S.pr?.events).length) {
      e.target.checked = true;
      openModal({ title: "Stop keeping history?", body: `<p>This deletes the arrivals and departures HomeOwl has kept. Who's home now keeps working.</p>`,
        buttons: [{ label: "Cancel" }, { label: "Stop and delete", cls: "danger", onClick: () => { closeModal(); prPost("/api/presence/settings", { history: false }, "History off"); } }] });
      return;
    }
    prPost("/api/presence/settings", { history: e.target.checked }, e.target.checked ? "Keeping history" : "History off"); return;
  }
  if (e.target.id === "pr-keep") { prPost("/api/presence/settings", { keep_days: +e.target.value }, "Saved"); return; }
  if (e.target.id === "pr-away") { prPost("/api/presence/settings", { away_minutes: +e.target.value || 10 }, "Saved"); }
});
// pick a device for one person: suggestions are their check-ins, devices already theirs in Analytics and names that match
async function prPick(pid) {
  const d = S.pr, p = arr(d?.people).find(x => x.id === pid); if (!p) return;
  const taken = new Map(); arr(d.people).forEach(x => arr(x.devices).forEach(m => taken.set(m, x)));
  const items = [...prDevices().values()].map(x => ({ id: x.mac, mac: x.mac, name: x.label, vendor: x.vendor, online: x.online, wifi: true }));
  let checkins = [], mine = new Set();
  if (S.source === "live") {
    const [ci, meta] = await Promise.all([api("/api/checkins").catch(() => ({})), api("/api/analytics/meta", {}).catch(() => ({}))]);
    checkins = ci.items || [];
    arr(meta.people).find(x => x.id === pid)?.devices.forEach(k => { if (k.kind === "mac") mine.add(k.key); });
  }
  openModal({ title: `A device for ${p.name}`, wide: true, body: `<p class="fine muted">Usually the phone ${esc(p.name)} carries. They count as home while it's on your Wi-Fi.</p><div id="pr-picker"></div>`, buttons: [{ label: "Cancel" }] });
  devPicker($("#pr-picker"), { items, single: true, rank: it => ["phone", "watch"].includes(clientKind(it)) ? 1 : 0,
    ownerOf: it => it && taken.get(it.mac) && taken.get(it.mac).id !== pid ? taken.get(it.mac) : null,
    suggest: () => { const m = dpSuggest(items, { name: p.name, id: pid, checkins }); items.forEach(it => { if (mine.has(it.mac) && !m.has(it.id)) m.set(it.id, "theirs in Analytics"); });
      [...m.keys()].forEach(id => { if (arr(p.devices).includes(id)) m.delete(id); }); return m; },
    onPick: it => { closeModal(); prPost("/api/presence/tag", { mac: it.mac, person: pid }, `${it.name} tagged`); } });
}

document.addEventListener("click", e => {
  const pk = e.target.closest("[data-prpick]"); if (pk) { prPick(pk.dataset.prpick); return; }
  if (!e.target.closest("#pr-body")) return;
  const un = e.target.closest("[data-pruntag]");
  if (un) { prPost("/api/presence/tag", { mac: un.dataset.pruntag, person: "" }, "Untagged"); return; }
  const days = e.target.closest("[data-prdays]");
  if (days) { S.prDays = +days.dataset.prdays; loadPresence(); return; }
  if (e.target.id === "pr-clear") openModal({ title: "Clear the history?", body: `<p>Deletes every arrival and departure HomeOwl has kept. Who's home now keeps working.</p>`,
    buttons: [{ label: "Cancel" }, { label: "Clear", cls: "danger", onClick: () => { closeModal(); prPost("/api/presence/clear", {}, "History cleared"); } }] });
});

setInterval(() => { if (S.view === "presence" && S.source === "live" && !document.hidden && $("#modal").hidden && !document.activeElement?.closest?.("#pr-body select, #pr-body input")) loadPresence(); }, 60000);
{ const _render = render; render = function () { _render(); if (S.view === "presence") loadPresence(); }; }
