/* HomeOwl — Tuya (Smart Life) devices under Smart: online or not, readings, on/off; set up through the Tuya Cloud
   or on the home network with each device's local key (tuya.go). Depends on core.js and app.js. */
"use strict";

S.tuya = null;
S.tuyaSet = false; // the settings card is open
// the house and type the page is filtered to ("" = all; "-" = devices in no house), kept per browser
try { S.tuyaHome = localStorage.getItem("homeowl-tuya-home") || ""; S.tuyaKind = localStorage.getItem("homeowl-tuya-kind") || ""; } catch { S.tuyaHome = S.tuyaKind = ""; }
const tuyaKeep = () => { try { localStorage.setItem("homeowl-tuya-home", S.tuyaHome); localStorage.setItem("homeowl-tuya-kind", S.tuyaKind); } catch { } };

V.tuya = () => `<div class="head"><div><h1>Tuya ${hint("Smart Life and Tuya plugs, switches, lights and sensors: online or not, their readings, and on/off. Through the Tuya Cloud or straight on the home network.")}</h1></div></div>
  <div id="tuya-body"><div class="empty">Loading…</div></div>`;

async function loadTuya() {
  const el = $("#tuya-body"); if (!el) return;
  if (S.source !== "live" && S.source !== "none") { el.innerHTML = `<div class="card"><p class="muted">Tuya devices need a running HomeOwl. They aren't part of the demo.</p></div>`; return; }
  try { S.tuya = await api("/api/tuya"); } catch (err) { el.innerHTML = `<div class="card"><p class="muted">${esc(err.message)}</p></div>`; return; }
  if ($("#tuya-body")) $("#tuya-body").innerHTML = tuyaBody(S.tuya);
}
{ const _render = render; render = function () { _render(); if (S.view === "tuya") loadTuya(); }; }
setInterval(() => { if (S.view === "tuya" && S.tuya?.mode && !document.hidden && $("#modal").hidden && !document.activeElement?.closest?.("#tuya-body input, #tuya-body select")) loadTuya(); }, 30000);

const tuyaAdmin = () => S.role === "admin";

function tuyaBody(d) {
  const all = arr(d.devices), homes = arr(d.homes).filter(h => !h.hidden), on = all.filter(x => x.online).length;
  const head = d.mode ? `<p class="muted">${all.length ? `${on} of ${plural(all.length, "device")} online` : "No devices yet"} · ${d.mode === "cloud" ? "through the Tuya Cloud" : "on the home network"}${d.synced ? ` · updated ${esc(ago(d.synced * 1000))}` : ""}
      ${tuyaAdmin() ? ` <button class="btn sm ghost" data-tuya="sync">Refresh</button>` : ""}</p>${d.error ? `<div class="msg warn">${esc(d.error)}</div>` : ""}` : "";
  if (S.tuyaHome && S.tuyaHome !== "-" && !homes.some(h => h.id === S.tuyaHome)) S.tuyaHome = "";
  const noHouse = all.filter(x => !x.home).length;
  const inHome = x => !S.tuyaHome || (S.tuyaHome === "-" ? !x.home : x.home === S.tuyaHome);
  const here = all.filter(inHome);
  const kinds = {}; here.forEach(x => { const k = x.kind || "Other"; kinds[k] = (kinds[k] || 0) + 1; });
  if (S.tuyaKind && !kinds[S.tuyaKind]) S.tuyaKind = "";
  const devs = here.filter(x => !S.tuyaKind || (x.kind || "Other") === S.tuyaKind);
  const houseBar = homes.length ? `<div class="filters tuya-filter" role="group" aria-label="House"><button data-tuyahome="" class="${!S.tuyaHome ? "on" : ""}">All houses (${all.length})</button>
      ${homes.map(h => `<button data-tuyahome="${esc(h.id)}" class="${S.tuyaHome === h.id ? "on" : ""}">${esc(h.name)} (${all.filter(x => x.home === h.id).length})</button>`).join("")}
      ${noHouse ? `<button data-tuyahome="-" class="${S.tuyaHome === "-" ? "on" : ""}">No house (${noHouse})</button>` : ""}</div>` : "";
  const kindBar = Object.keys(kinds).length > 1 ? `<div class="filters tuya-filter" role="group" aria-label="Type"><button data-tuyakind="" class="${!S.tuyaKind ? "on" : ""}">All types</button>
      ${Object.entries(kinds).sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).map(([k, n]) => `<button data-tuyakind="${esc(k)}" class="${S.tuyaKind === k ? "on" : ""}">${esc(k)} (${n})</button>`).join("")}</div>` : "";
  // one section per house (or just the chosen one), rooms inside
  const groups = S.tuyaHome ? [{ id: S.tuyaHome, name: "", list: devs }]
    : [...homes.map(h => ({ id: h.id, name: h.name, list: devs.filter(x => x.home === h.id) })), { id: "-", name: homes.length ? "No house" : "", list: devs.filter(x => !x.home) }];
  const house = g => {
    const h = homes.find(x => x.id === g.id), rooms = {};
    g.list.forEach(x => { (rooms[x.room || ""] ||= []).push(x); });
    const keys = Object.keys(rooms).sort((a, b) => (a === "") - (b === "") || a.localeCompare(b));
    const scenes = h && !S.tuyaKind ? tuyaScenes(h) : "";
    if (!g.list.length && !scenes) return "";
    return `<section class="tuya-house">${g.name ? `<h2>${esc(g.name)} <span class="fine muted">${plural(g.list.length, "device")}</span></h2>` : ""}${scenes}
      ${keys.map(r => `${keys.length > 1 || r ? `<h3 class="tuya-room">${esc(r || "No room")}</h3>` : ""}<div class="tuya-grid">${rooms[r].map(tuyaCard).join("")}</div>`).join("")}</section>`;
  };
  const cards = all.length ? houseBar + kindBar + (groups.map(house).join("") || `<div class="card"><p class="muted">Nothing here.</p></div>`)
    : d.mode ? `<div class="card"><p class="muted">${d.mode === "cloud" ? "The Tuya Cloud project has no devices yet. Link your Smart Life app account to the project (Devices › Link Tuya App Account), then Refresh." : "No devices yet. Scan the network below, or add one by hand."}</p></div>` : "";
  return head + cards + (tuyaAdmin() ? tuyaSettings(d) : !d.mode ? `<div class="card"><p class="muted">Tuya isn't set up. An admin can set it up here.</p></div>` : "");
}

// a home's Tap-to-Run scenes (Run) and automations (on/off), from Smart Life
function tuyaScenes(h) {
  const sc = arr(h.scenes), au = arr(h.automations);
  if (!sc.length && !au.length) return h.scene_error && tuyaAdmin() ? `<p class="fine muted">Scenes and automations can't be read: ${esc(h.scene_error)}. In the Tuya project, add the <b>Smart Home Scene Linkage</b> API service (Cloud › your project › Service API).</p>` : "";
  return `<div class="card tuya-scenes">${sc.length ? `<div><span class="fine muted">Tap-to-Run</span><div class="tuya-sclist">${sc.map(x => `<button class="btn sm" data-tuyascene="${esc(x.id)}" data-home="${esc(h.id)}" ${tuyaAdmin() ? "" : "disabled title=\"Admins can run scenes\""}>▶ ${esc(x.name)}</button>`).join("")}</div></div>` : ""}
    ${au.length ? `<div><span class="fine muted">Automations</span><div class="tuya-sclist">${au.map(x => `<button class="tuya-tog${x.enabled ? " on" : ""}" type="button" role="switch" aria-checked="${x.enabled ? "true" : "false"}" ${tuyaAdmin() ? "" : "disabled"}
      data-tuyaauto="${esc(x.id)}" data-home="${esc(h.id)}" data-on="${x.enabled ? "1" : ""}" title="${tuyaAdmin() ? `Turn ${x.enabled ? "off" : "on"}` : "Admins can switch it"}"><span class="tuya-knob"></span>${esc(x.name)}</button>`).join("")}</div></div>` : ""}</div>`;
}

function tuyaCard(x) {
  const sw = arr(x.switches), rd = arr(x.readings);
  return `<div class="card tuya-card${x.online ? "" : " off"}">
    <div class="tuya-top">${onDot(x.online, x.seen * 1000)}<div class="tuya-name"><b>${esc(x.name)}</b>${x.kind ? ` <span class="tuya-kind">${esc(x.kind)}</span>` : ""}<div class="fine muted">${esc([x.product !== x.kind ? x.product : "", x.unifi_name && x.unifi_name !== x.name ? `UniFi: ${x.unifi_name}` : "", x.ip].filter(Boolean).join(" · "))}</div></div>
      ${tuyaAdmin() ? `<button class="icon-btn" data-tuyaedit="${esc(x.id)}" title="Details" aria-label="Details of ${esc(x.name)}">✎</button>` : ""}</div>
    ${rd.length ? `<div class="tuya-read">${rd.map(r => `<div><b>${esc(r.value)}${r.unit ? `<span class="fine"> ${esc(r.unit)}</span>` : ""}</b><span class="fine muted">${esc(r.label)}</span></div>`).join("")}</div>` : ""}
    ${sw.length ? `<div class="tuya-sw">${sw.map(s => `<button class="tuya-tog${s.on ? " on" : ""}" type="button" role="switch" aria-checked="${s.on ? "true" : "false"}" ${tuyaAdmin() && x.online ? "" : "disabled"}
        data-tuyasw="${esc(x.id)}" data-code="${esc(s.code)}" data-on="${s.on ? "1" : ""}" title="${tuyaAdmin() ? `Turn ${s.on ? "off" : "on"}` : "Admins can switch it"}"><span class="tuya-knob"></span>${esc(s.label)}</button>`).join("")}</div>` : ""}
    ${!x.online && x.error ? `<p class="fine muted">${esc(x.error)}</p>` : ""}</div>`;
}

const TUYA_REGION = { eu: "Central Europe", "eu-w": "Western Europe", us: "Western America", "us-e": "Eastern America", cn: "China", in: "India" };
function tuyaSettings(d) {
  const open = S.tuyaSet || !d.mode, nets = new Set(arr(d.networks)), un = arr(d.unifi_networks), found = arr(d.found);
  const known = new Set(arr(d.devices).map(x => x.ip).filter(Boolean));
  return `<details class="card section tuya-set" id="tuya-set" ${open ? "open" : ""}><summary><h2 style="display:inline">Tuya settings</h2></summary>
    <label>Connect through<select id="tuya-mode"><option value="" ${!d.mode ? "selected" : ""}>Off</option><option value="cloud" ${d.mode === "cloud" ? "selected" : ""}>Tuya Cloud (every device, needs internet)</option><option value="local" ${d.mode === "local" ? "selected" : ""}>Home network only (local keys, no cloud)</option></select></label>
    <div id="tuya-cloud" ${d.mode === "cloud" ? "" : "hidden"}>
      <ol class="fine tuya-steps"><li>Sign in at <a href="https://platform.tuya.com" target="_blank" rel="noopener">platform.tuya.com</a> (free) and create a <b>Cloud › Development</b> project for <b>Smart Home</b>, in the data centre of your Smart Life account.</li>
        <li>In the project, <b>Devices › Link Tuya App Account › Add App Account</b>, and scan the code with the Smart Life app (Me › scan icon).</li>
        <li>Copy the project's <b>Access ID</b> and <b>Access Secret</b> (Overview) here.</li></ol>
      <div class="grid g2"><label>Data centre<select id="tuya-region">${arr(d.regions).map(r => `<option value="${r}" ${r === (d.region || "eu") ? "selected" : ""}>${esc(TUYA_REGION[r] || r)}</option>`).join("")}</select></label><span></span>
        <label>Access ID<input id="tuya-id" autocomplete="off" spellcheck="false" placeholder="${d.cloud_ready ? "saved — leave empty to keep" : ""}"></label>
        <label>Access Secret<input id="tuya-secret" type="password" autocomplete="new-password" placeholder="${d.cloud_ready ? "saved — leave empty to keep" : ""}"></label></div>
      <p class="fine">They're kept in the system keychain. The cloud also gives each device's local key, so you can switch to the home network later.${d.cloud_ready ? ` <button class="btn sm ghost" data-tuya="forget-cloud">Forget them</button>` : ""}</p></div>
    <div id="tuya-local" ${d.mode === "local" ? "" : "hidden"}>
      <p class="fine">HomeOwl talks to each device on port 6668 with its <b>local key</b> (16 characters). Get the keys once from the Tuya Cloud (pick Tuya Cloud, save, then come back here), or with a tool like tinytuya's wizard.</p></div>
    <div id="tuya-nets" ${d.mode ? "" : "hidden"}>
      <h3>Where to look for devices</h3>
      <p class="fine muted">Devices on HomeOwl's own network announce themselves. For other networks (an IoT VLAN), tick them and HomeOwl tries Tuya's port on each address. You can also mark single devices as Tuya in <a href="#" data-goto="clients">Devices › All devices</a> (open the device).</p>
      <div class="tuya-netlist">${un.map(n => `<label class="check"><input type="checkbox" data-tuyanet="${esc(n.subnet)}" ${nets.has(n.subnet) ? "checked" : ""}> ${esc(n.name)} <span class="fine muted mono">${esc(n.subnet)}</span></label>`).join("")}
        ${[...nets].filter(n => !un.some(u => u.subnet === n)).map(n => `<label class="check"><input type="checkbox" data-tuyanet="${esc(n)}" checked> <span class="mono">${esc(n)}</span></label>`).join("")}</div>
      <div class="btnrow"><input id="tuya-net-add" placeholder="Another network, e.g. 192.168.20.0/24" spellcheck="false" style="max-width:280px"><button class="btn sm" id="tuya-net-addbtn" type="button">Add</button></div>
      ${arr(d.marked).length ? `<p class="fine">Marked as Tuya: ${d.marked.map(m => `<span class="mono">${esc(m)}</span>`).join(", ")}</p>` : ""}</div>
    ${d.mode ? tuyaHouses(d) : ""}
    <div class="btnrow" style="margin-top:12px"><button class="btn primary" id="tuya-save">Save</button>${d.mode ? `<button class="btn" data-tuya="scan">Scan the network now</button>` : ""}${d.mode === "local" ? `<button class="btn" id="tuya-add">Add a device by hand</button>` : ""}</div>
    ${d.scanned ? `<h3 style="margin-top:16px">Last scan <span class="fine muted">${esc(ago(d.scanned * 1000))}</span></h3>
      ${found.length ? `<div class="ap-blocks">${found.map(f => `<div class="ap-block"><div><b>${esc(f.unifi_name || f.ip)}</b> <span class="fine muted mono">${esc(f.ip)}</span>
        <div class="fine muted">${f.id ? `Tuya ${esc(f.version || "")} · ${esc(f.id)}` : "Answers on Tuya's port"}${known.has(f.ip) ? " · added" : ""}</div></div>
        ${d.mode === "local" && !known.has(f.ip) ? `<button class="btn sm" data-tuyaadd="${esc(JSON.stringify({ ip: f.ip, id: f.id || "", version: f.version || "3.3", name: f.unifi_name || "" }))}">Add…</button>` : ""}</div>`).join("")}</div>`
        : arr(d.unreached).length ? "" : `<p class="fine muted">Nothing answered. Devices on another network need it ticked above, and the network must let HomeOwl reach port 6668.</p>`}
      ${arr(d.unreached).map(u => `<div class="msg warn tuya-fw"><div><b>Nothing on ${esc(u.name)} answered.</b> Devices on another network are only found when HomeOwl's network may reach them through the firewall,
        and an IoT network is usually closed off. One narrow rule fixes it: HomeOwl's network may reach Tuya's port (TCP 6668) there, nothing else.</div>
        <button class="btn sm" data-tuyafw="${esc(u.network)}">Preview the fix</button></div>`).join("")}` : ""}
  </details>`;
}

function tuyaHouses(d) {
  const homes = arr(d.homes);
  return `<div id="tuya-houses"><h3>Houses</h3>
    <p class="fine muted">${d.mode === "cloud" ? "Homes come from Smart Life (each linked app account), with their rooms, scenes and automations. Untick one to hide its devices here — for example a parent's home on the same account. " : ""}You can also make houses here and put devices in them (open a device, ✎).</p>
    ${homes.length ? `<div class="tuya-netlist">${homes.map(h => `<div class="tuya-hrow"><label class="check"><input type="checkbox" data-tuyashow="${esc(h.id)}" ${h.hidden ? "" : "checked"}> ${esc(h.name)} <span class="fine muted">${plural(h.count, "device")}${h.custom ? " · made here" : " · Smart Life"}</span></label>
      ${h.custom ? `<button class="btn sm ghost" data-tuyahedit="${esc(h.id)}">Rename</button><button class="btn sm ghost" data-tuyahdel="${esc(h.id)}">Remove</button>` : ""}</div>`).join("")}</div>` : ""}
    <div class="btnrow"><input id="tuya-home-new" placeholder="New house, e.g. Beach house" maxlength="40" style="max-width:280px"><button class="btn sm" id="tuya-home-add" type="button">Add house</button></div></div>`;
}

async function tuyaDo(op, body, msg) {
  try { S.tuya = await api(`/api/tuya/${op}`, body || {}); if (msg) toast(msg, "ok"); if ($("#tuya-body")) $("#tuya-body").innerHTML = tuyaBody(S.tuya); return true; }
  catch (err) { toast(err.message, "err"); return false; }
}

function tuyaDeviceModal(x) {
  const local = S.tuya?.mode === "local", homes = arr(S.tuya?.homes), known = !!x.known;
  const fromApp = known && x.home && !x.place ? homes.find(h => h.id === x.home) : null;
  const rooms = [...new Set(homes.flatMap(h => arr(h.rooms)).concat(arr(S.tuya?.devices).map(d => d.room).filter(Boolean)))];
  const tags = known ? `<h3>Where it is and what it is</h3><div class="grid g2">
      <label>House<select id="td-place"><option value="">${fromApp ? `As in Smart Life (${esc(fromApp.name)})` : "No house"}</option>${homes.map(h => `<option value="${esc(h.id)}" ${x.place === h.id ? "selected" : ""}>${esc(h.name)}</option>`).join("")}</select></label>
      <label>Room<input id="td-room" list="td-rooms" value="${esc(x.my_room || "")}" placeholder="${esc(x.room && !x.my_room ? x.room : "e.g. Kitchen")}" maxlength="40"><datalist id="td-rooms">${rooms.map(r => `<option value="${esc(r)}">`).join("")}</datalist></label>
      <label>Type<input id="td-type" list="td-kinds" value="${esc(x.type || "")}" placeholder="${esc(x.auto_kind || "e.g. Boiler switch")}" maxlength="40"><datalist id="td-kinds">${arr(S.tuya?.kinds).map(k => `<option value="${esc(k)}">`).join("")}</datalist></label></div>
    <p class="fine muted">Empty fields follow Smart Life. The type is what the page groups and filters by.</p>` : "";
  openModal({ title: x.id ? x.name || "Tuya device" : "Add a Tuya device", body: `${x.id && !local ? `<p class="fine muted">From the Tuya Cloud. ID <span class="mono">${esc(x.id)}</span>${x.version ? ` · protocol ${esc(x.version)}` : ""}${x.ip ? ` · ${esc(x.ip)}` : ""}${x.product ? ` · ${esc(x.product)}` : ""}</p>` : ""}
    ${local ? `<div class="grid g2"><label>Name<input id="td-name" value="${esc(x.name || "")}" placeholder="Kitchen plug"></label>
      <label>Device ID<input id="td-id" value="${esc(x.id || "")}" spellcheck="false" ${known ? "readonly" : ""}></label>
      <label>Address on the home network<input id="td-ip" value="${esc(x.ip || "")}" placeholder="192.168.1.50" spellcheck="false"></label>
      <label>Protocol<select id="td-ver">${["3.3", "3.4", "3.5"].map(v => `<option ${v === (x.version || "3.3") ? "selected" : ""}>${v}</option>`).join("")}</select></label>
      <label>Local key<input id="td-key" type="password" autocomplete="new-password" placeholder="${x.has_key ? "saved — leave empty to keep" : "16 characters"}"></label></div>
      <p class="fine">The device ID is in Smart Life › the device › ✎ › Device information (Virtual ID). Give the device a fixed address in UniFi so HomeOwl keeps finding it.</p>` : ""}${tags}`,
    buttons: [{ label: "Close" }, ...(known && local ? [{ label: "Remove", onClick: async () => { if (await tuyaDo("remove", { id: x.id }, "Removed")) closeModal(); } }] : []),
      ...(local || known ? [{ label: "Save", cls: "primary", onClick: async () => {
        if (local && !await tuyaDo("device", { id: $("#td-id").value, name: $("#td-name").value, ip: $("#td-ip").value, version: $("#td-ver").value, local_key: $("#td-key").value }, known ? "" : "Saved")) return;
        if (known && !await tuyaDo("tag", { id: x.id, place: $("#td-place").value, room: $("#td-room").value, type: $("#td-type").value }, "Saved")) return;
        closeModal();
      } }] : [])] });
}

document.addEventListener("change", e => {
  const hs = e.target.closest?.("[data-tuyashow]");
  if (hs) { tuyaDo("home-show", { id: hs.dataset.tuyashow, on: hs.checked }, hs.checked ? "Shown" : "Hidden"); return; }
  if (e.target.id !== "tuya-mode") return;
  const m = e.target.value;
  $("#tuya-cloud").hidden = m !== "cloud"; $("#tuya-local").hidden = m !== "local"; $("#tuya-nets").hidden = !m;
});
document.addEventListener("toggle", e => { if (e.target.id === "tuya-set") S.tuyaSet = e.target.open; }, true);
document.addEventListener("click", async e => {
  const t = e.target;
  const sw = t.closest("[data-tuyasw]");
  if (sw) { sw.disabled = true; const on = !sw.dataset.on; await tuyaDo("switch", { id: sw.dataset.tuyasw, code: sw.dataset.code, on }, `Turned ${on ? "on" : "off"}`); return; }
  const hb = t.closest("[data-tuyahome]"); if (hb) { S.tuyaHome = hb.dataset.tuyahome; tuyaKeep(); $("#tuya-body").innerHTML = tuyaBody(S.tuya); return; }
  const kb = t.closest("[data-tuyakind]"); if (kb) { S.tuyaKind = kb.dataset.tuyakind; tuyaKeep(); $("#tuya-body").innerHTML = tuyaBody(S.tuya); return; }
  const sc = t.closest("[data-tuyascene]");
  if (sc) { sc.disabled = true; await tuyaDo("scene", { home: sc.dataset.home, id: sc.dataset.tuyascene }, `Ran ${sc.textContent.replace("▶", "").trim()}`); return; }
  const au = t.closest("[data-tuyaauto]");
  if (au) { au.disabled = true; const on = !au.dataset.on; await tuyaDo("automation", { home: au.dataset.home, id: au.dataset.tuyaauto, on }, `Automation ${on ? "on" : "off"}`); return; }
  if (t.id === "tuya-home-add") { const n = $("#tuya-home-new").value.trim(); if (!n) return toast("Give the house a name", "err"); t.disabled = true; await tuyaDo("home-add", { name: n }, "House added"); return; }
  const he = t.closest("[data-tuyahedit]");
  if (he) { const h = arr(S.tuya?.homes).find(x => x.id === he.dataset.tuyahedit); const n = prompt("New name", h?.name || ""); if (n && n.trim()) await tuyaDo("home-edit", { id: he.dataset.tuyahedit, name: n.trim() }, "Renamed"); return; }
  const hd = t.closest("[data-tuyahdel]");
  if (hd) { if (confirm("Remove this house? Its devices stay, in no house.")) await tuyaDo("home-remove", { id: hd.dataset.tuyahdel }, "Removed"); return; }
  const op = t.closest("[data-tuya]");
  if (op) {
    const o = op.dataset.tuya; op.disabled = true;
    if (o === "scan") { op.textContent = "Scanning… (up to 40 s)"; await tuyaDo("scan", { networks: tuyaTicked() }, "Scan done"); }
    else if (o === "forget-cloud") await tuyaDo(o, {}, "Forgotten");
    else await tuyaDo(o, {}, "Updated");
    op.disabled = false; return;
  }
  if (t.id === "tuya-net-addbtn") {
    const v = $("#tuya-net-add").value.trim(); if (!/^\d+\.\d+\.\d+\.\d+\/\d+$/.test(v)) return toast("Type a network like 192.168.20.0/24", "err");
    $(".tuya-netlist").insertAdjacentHTML("beforeend", `<label class="check"><input type="checkbox" data-tuyanet="${esc(v)}" checked> <span class="mono">${esc(v)}</span></label>`); $("#tuya-net-add").value = ""; return;
  }
  if (t.id === "tuya-save") {
    t.disabled = true;
    await tuyaDo("settings", { mode: $("#tuya-mode").value, region: $("#tuya-region")?.value || "", access_id: $("#tuya-id")?.value || "", access_secret: $("#tuya-secret")?.value || "",
      networks: $$("[data-tuyanet]").filter(i => i.checked).map(i => i.dataset.tuyanet) }, "Saved");
    t.disabled = false; return;
  }
  if (t.id === "tuya-add") { tuyaDeviceModal({}); return; }
  const fw = t.closest("[data-tuyafw]"); if (fw) { fw.disabled = true; await tuyaFirewall(fw.dataset.tuyafw); fw.disabled = false; return; }
  const add = t.closest("[data-tuyaadd]"); if (add) { tuyaDeviceModal(JSON.parse(add.dataset.tuyaadd)); return; }
  const ed = t.closest("[data-tuyaedit]");
  if (ed) { const x = arr(S.tuya?.devices).find(d => d.id === ed.dataset.tuyaedit); if (x) tuyaDeviceModal({ ...x, known: true }); }
});

// Devices › All devices: mark a client as a Tuya device (local mode looks for it there)
async function tuyaMark(mac, on) {
  try { await api("/api/tuya/mark", { mac, on }); toast(on ? "Marked as a Tuya device: Smart › Tuya looks for it at its address" : "Not a Tuya device", "ok"); S.tuyaMarked = null; }
  catch (err) { toast(err.message, "err"); }
}
document.addEventListener("click", async e => {
  const b = e.target.closest("[data-tuyamark]"); if (!b) return;
  const on = !!b.dataset.on; await tuyaMark(b.dataset.tuyamark, on);
  try { S.tuya = await api("/api/tuya"); } catch { /* the button just won't flip */ }
  b.dataset.on = on ? "" : "1"; b.textContent = on ? "Not a Tuya device" : "It's a Tuya device";
});

const tuyaTicked = () => $$("[data-tuyanet]").filter(i => i.checked).map(i => i.dataset.tuyanet);

// the firewall fix (tuya.go tuyaFwPlan): a preview first, applied only on "Allow it", undoable from Changes
async function tuyaFirewall(network) {
  let r; try { r = await api("/api/tuya/firewall-check", { network }); } catch (err) { toast(err.message, "err"); return; }
  const fix = arr(r.fix);
  openModal({ title: `Let HomeOwl reach ${r.name}`, body: r.exists
      ? `<p>HomeOwl's rule for ${esc(r.name)} is already there (UniFi › Settings › Policy Engine), so the firewall isn't what stops it. Check the devices are on, and that they're Tuya (Smart Life) devices.</p>`
      : `<p>${esc(r.text)}</p>${fix.map(p => `<div class="pv"><div class="pv-h"><b>${esc(p.description)}</b></div>
        <div>${Object.entries(p.after || {}).map(([k, v]) => `<div><span class="fine muted">${esc(k)}</span> ${esc(fmtVal(v))}</div>`).join("")}</div>
        <div class="fine mono">${esc(p.method)} ${esc(p.path)}</div></div>`).join("")}
      <p class="fine">↩ Can be undone from the Changes page.${S.write ? "" : " Change mode is locked — allowing it unlocks it for this session."}</p>`,
    buttons: [{ label: "Close" }, ...(fix.length ? [{ label: "Allow it", cls: "primary", onClick: async () => {
      try {
        if (!S.write) await setWrite(true);
        await api("/api/tuya/firewall-fix", { network });
        closeModal(); toast("Allowed — scanning again", "ok");
        await tuyaDo("scan", { networks: tuyaTicked() }, "Scan done");
      } catch (err) { toast(err.message, "err"); }
    } }] : [])] });
}
