/* HomeOwl — Family › Apps: which apps are used on the network (from NextDNS logs, internal/apps), and blocking an
   app for the people on chosen NextDNS profiles, always or on a schedule (appblocks.go). Depends on core.js, app.js,
   explore.js. */
"use strict";

S.apps = { range: "7d", person: "", data: null, blocks: null, people: null, names: {}, cat: "" };
const APP_DAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

V.apps = () => `<div class="head"><div><h1>Apps ${hint("Apps are recognised from the domains a device looks up in NextDNS, so an app shows up when it's used (or updates itself), not when it's only installed. Blocking adds the app's own domains to the NextDNS profile's denylist.")}</h1></div>
    <div class="btnrow">${isAdmin() && S.source === "live" ? `<button class="btn primary" id="ap-new">Block an app…</button>` : ""}</div></div>
  <div id="ap-blocks"></div>
  <div class="card"><div class="ap-bar"><h2 style="margin:0">Seen on the network</h2><span style="flex:1"></span>
    <select id="ap-person" aria-label="Whose apps"><option value="">Everyone</option>${(S.apps.people || []).map(p => `<option value="${esc(p.id)}" ${S.apps.person === p.id ? "selected" : ""}>${esc(p.name)}</option>`).join("")}</select>
    <div class="seg">${[["24h", "24 h"], ["7d", "7 days"], ["30d", "30 days"]].map(([k, l]) => `<button data-aprange="${k}" class="${S.apps.range === k ? "on" : ""}">${l}</button>`).join("")}</div></div>
    <div id="ap-seen"><div class="empty">Loading…</div></div></div>`;

const apCatalog = () => S.apps.blocks?.apps || [];
const apApp = id => apCatalog().find(a => a.id === id);
const apPeople = ids => { const pf = S.apps.blocks?.profiles || []; return ids.map(id => { const p = pf.find(x => x.id === id); return p ? (p.people.length ? p.people.join(", ") : p.name) : id; }).join(" · "); };

async function loadApps() {
  if (S.view !== "apps") return;
  const live = S.source === "live";
  if (live && isAdmin() && !S.apps.blocks) { try { S.apps.blocks = await api("/api/appblocks"); apCatalog().forEach(a => { S.apps.names[a.id] = a.name; }); } catch (e) { S.apps.blocks = { blocks: [], apps: [], profiles: [], error: e.message }; } }
  if (live && !S.apps.people) { S.apps.people = []; try { const m = await api("/api/analytics/meta", {}); if (m?.people?.length) { S.apps.people = m.people; return render(); } } catch { /* analytics off */ } }
  renderAppBlocks();
  let r;
  try { r = await api("/api/analytics/explore", { range: S.apps.range, views: ["by_app"], filters: S.apps.person ? { person: S.apps.person } : {} }); }
  catch (e) { if ($("#ap-seen")) $("#ap-seen").innerHTML = `<div class="empty">${esc(/analytics/i.test(e.message) ? "Apps need Analytics with NextDNS logs (Settings › Analytics)." : e.message)}</div>`; return; }
  S.apps.data = r.by_app || []; S.apps.data.forEach(x => { if (x.name) S.apps.names[x.key] = x.name; });
  renderAppsSeen();
}

function renderAppBlocks() {
  const el = $("#ap-blocks"); if (!el) return;
  const b = S.apps.blocks; if (!b) { el.innerHTML = ""; return; }
  if (b.error) { el.innerHTML = `<div class="banner warn">${esc(b.error)}</div>`; return; }
  if (!b.blocks.length) { el.innerHTML = ""; return; }
  el.innerHTML = `<div class="card"><h2>Blocked apps</h2><div class="ap-blocks">${b.blocks.map(x => `<div class="ap-block">
      <div><b>${esc(x.name)}</b> <span class="tag ${x.error ? "bad" : x.blocked_now ? "warn" : "good"}">${x.error ? "needs attention" : x.blocked_now ? "blocked now" : "allowed now"}</span>
        <div class="fine muted">${esc(x.mode === "schedule" ? "Blocked " + x.when : "Always blocked")} · ${esc(apPeople(x.profiles))}</div>
        ${x.error ? `<div class="fine bad-t">${esc(x.error)}</div>` : ""}</div>
      <div class="btnrow"><button class="btn sm" data-apedit="${esc(x.id)}">Change</button><button class="btn sm danger" data-apdel="${esc(x.id)}">Unblock</button></div></div>`).join("")}</div></div>`;
}

function renderAppsSeen() {
  const el = $("#ap-seen"); if (!el) return;
  const rows = S.apps.data || [], blocked = new Set((S.apps.blocks?.blocks || []).map(b => b.app));
  if (!rows.length) { el.innerHTML = `<div class="empty">No known apps in this period${S.apps.person ? " for this person" : ""}.</div>`; return; }
  const cats = [...new Set(rows.map(r => r.category).filter(Boolean))];
  const shown = rows.filter(r => !S.apps.cat || (r.category) === S.apps.cat);
  const day = S.apps.range === "24h" ? { hour: "2-digit", minute: "2-digit" } : { day: "numeric", month: "short" };
  el.innerHTML = `${cats.length > 1 ? `<div class="seg ap-cats"><button data-apcat="" class="${S.apps.cat ? "" : "on"}">All</button>${cats.sort().map(c => `<button data-apcat="${esc(c)}" class="${S.apps.cat === c ? "on" : ""}">${esc(c)}</button>`).join("")}</div>` : ""}
    <div class="tbl-wrap"><table class="ap-t"><thead><tr><th>App</th><th>Category</th><th class="num">People</th><th class="num">Devices</th><th class="num">Days used</th><th class="num">Lookups</th><th class="num">Blocked</th><th>Last seen</th><th></th></tr></thead>
    <tbody>${shown.map(r => `<tr><td><a href="#" data-exgo="app|${esc(r.key)}${S.apps.person ? ";;person|" + esc(S.apps.person) : ""}" title="Explore: who, when, which devices">${esc(r.name || r.key)}</a>${blocked.has(r.key) ? ` <span class="tag warn">blocked</span>` : ""}</td>
      <td>${esc(r.category || "")}</td><td class="num">${r.people ?? "–"}</td><td class="num">${r.devices}</td><td class="num">${r.days}</td>
      <td class="num">${fmtN(r.total)}</td><td class="num">${r.blocked ? Math.round(100 * r.blocked / r.total) + "%" : "–"}</td>
      <td>${r.last ? esc(new Date(r.last).toLocaleString([], day)) : ""}</td>
      <td>${isAdmin() && S.source === "live" && S.apps.blocks ? `<button class="btn sm" data-apblock="${esc(r.key)}">${blocked.has(r.key) ? "Change…" : "Block…"}</button>` : ""}</td></tr>`).join("")}</tbody></table></div>`;
}
const fmtN = n => (+n || 0).toLocaleString();

// ---- block / change dialog
function apEdit(block, appId) {
  const b = block || { app: appId || "", profiles: [], mode: "always", days: [0, 1, 2, 3, 4], from: "21:00", to: "07:00" };
  const pf = S.apps.blocks?.profiles || [];
  const byCat = {}; apCatalog().forEach(a => { (byCat[a.category] ||= []).push(a); });
  openModal({ title: block ? `Change the ${b.name || "app"} block` : "Block an app", body: `
    <label>App<select id="ap-app" ${block ? "disabled" : ""}>${Object.keys(byCat).map(c => `<optgroup label="${esc(c)}">${byCat[c].map(a => `<option value="${esc(a.id)}" ${a.id === b.app ? "selected" : ""}>${esc(a.name)}</option>`).join("")}</optgroup>`).join("")}</select></label>
    <p class="fine muted" id="ap-doms"></p>
    <h3>For the people on these NextDNS profiles</h3><div>
      ${pf.length ? `<div class="ap-pf">${pf.map(p => `<label class="check"><input type="checkbox" value="${esc(p.id)}" ${b.profiles.includes(p.id) ? "checked" : ""}> <b>${esc(p.name)}</b> <span class="fine muted">${esc(p.people.join(", ") || "no one attached")}</span></label>`).join("")}</div>`
      : `<p class="bad-t fine">HomeOwl can't read your NextDNS profiles. Check the API key in Settings › Analytics.</p>`}
      <p class="fine muted">Blocking is per NextDNS profile: it applies to every device using that profile.</p></div>
    <h3>When</h3><div><div class="seg"><button type="button" data-apmode="always" class="${b.mode !== "schedule" ? "on" : ""}">Always</button><button type="button" data-apmode="schedule" class="${b.mode === "schedule" ? "on" : ""}">On a schedule</button></div></div>
    <div id="ap-sched" ${b.mode === "schedule" ? "" : "hidden"}>
      <div class="dt-checks ap-days">${APP_DAYS.map((d, i) => `<label class="check"><input type="checkbox" data-apday="${i}" ${b.days?.includes(i) ? "checked" : ""}> ${d}</label>`).join("")}</div>
      <div class="ap-times"><label>From <input type="time" id="ap-from" value="${esc(b.from || "21:00")}"></label><label>to <input type="time" id="ap-to" value="${esc(b.to || "07:00")}"></label></div>
      <p class="fine muted">A time that ends before it starts runs to the next morning (21:00 to 07:00 on Sunday is Sunday night). HomeOwl switches the block on and off, so it has to be running.</p></div>`,
    buttons: [{ label: "Cancel" }, { label: block ? "Save" : "Block", cls: "primary", onClick: async () => {
      const body = { id: block?.id || "", app: $("#ap-app").value, profiles: $$(".ap-pf input:checked").map(x => x.value),
        mode: $("[data-apmode].on")?.dataset.apmode || "always", days: $$("[data-apday]:checked").map(x => +x.dataset.apday), from: $("#ap-from").value, to: $("#ap-to").value };
      try { S.apps.blocks = { ...S.apps.blocks, ...(await api("/api/appblocks/save", body)) }; closeModal(); toast(`${apApp(body.app)?.name || "App"} ${body.mode === "schedule" ? "will be blocked on schedule" : "blocked"}`, "ok"); }
      catch (e) { toast(e.message, "err"); S.apps.blocks = null; }
      loadApps();
    } }] });
  apDoms();
}
function apDoms() { const a = apApp($("#ap-app")?.value); if ($("#ap-doms")) $("#ap-doms").textContent = a ? `Blocks ${a.roots.join(", ")} and their subdomains.` : ""; }

document.addEventListener("click", e => {
  const g = s => e.target.closest(s); let x;
  if ((x = g("[data-apmode]"))) { $$("[data-apmode]").forEach(b => b.classList.toggle("on", b === x)); $("#ap-sched").hidden = x.dataset.apmode !== "schedule"; return; }
  if (S.view !== "apps") return;
  if ((x = g("[data-aprange]"))) { S.apps.range = x.dataset.aprange; render(); return; }
  if ((x = g("[data-apcat]"))) { S.apps.cat = x.dataset.apcat; renderAppsSeen(); return; }
  if (e.target.id === "ap-new") { apEdit(null, ""); return; }
  if ((x = g("[data-apblock]"))) { const ex = S.apps.blocks?.blocks.find(b => b.app === x.dataset.apblock); apEdit(ex || null, x.dataset.apblock); return; }
  if ((x = g("[data-apedit]"))) { apEdit(S.apps.blocks.blocks.find(b => b.id === x.dataset.apedit)); return; }
  if ((x = g("[data-apdel]"))) {
    const b = S.apps.blocks.blocks.find(y => y.id === x.dataset.apdel);
    openModal({ title: `Unblock ${b.name}?`, body: `<p>HomeOwl removes the entries it added to the NextDNS denylist for ${esc(apPeople(b.profiles))}. Entries that were there before stay.</p>`,
      buttons: [{ label: "Cancel" }, { label: "Unblock", cls: "danger", onClick: async () => {
        try { S.apps.blocks = { ...S.apps.blocks, ...(await api("/api/appblocks/delete", { id: b.id })) }; closeModal(); toast(`${b.name} unblocked`, "ok"); } catch (err) { toast(err.message, "err"); S.apps.blocks = null; }
        loadApps();
      } }] });
  }
});
document.addEventListener("change", e => {
  if (e.target.id === "ap-app") apDoms();
  if (e.target.id === "ap-person") { S.apps.person = e.target.value; render(); }
});
{ const _render = render; render = function () { _render(); if (S.view === "apps") loadApps(); }; }
