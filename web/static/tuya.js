/* HomeOwl — Tuya (Smart Life) devices under Smart: online or not, readings, on/off; set up through the Tuya Cloud
   or on the home network with each device's local key (tuya.go). Depends on core.js and app.js. */
"use strict";

S.tuya = null;
S.tuyaSet = false; // the settings card is open

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
  const devs = arr(d.devices), on = devs.filter(x => x.online).length;
  const head = d.mode ? `<p class="muted">${devs.length ? `${on} of ${plural(devs.length, "device")} online` : "No devices yet"} · ${d.mode === "cloud" ? "through the Tuya Cloud" : "on the home network"}${d.synced ? ` · updated ${esc(ago(d.synced * 1000))}` : ""}
      ${tuyaAdmin() ? ` <button class="btn sm ghost" data-tuya="sync">Refresh</button>` : ""}</p>${d.error ? `<div class="msg warn">${esc(d.error)}</div>` : ""}` : "";
  const cards = devs.length ? `<div class="tuya-grid">${devs.map(tuyaCard).join("")}</div>` : d.mode ? `<div class="card"><p class="muted">${d.mode === "cloud" ? "The Tuya Cloud project has no devices yet. Link your Smart Life app account to the project (Devices › Link Tuya App Account), then Refresh." : "No devices yet. Scan the network below, or add one by hand."}</p></div>` : "";
  return head + cards + (tuyaAdmin() ? tuyaSettings(d) : !d.mode ? `<div class="card"><p class="muted">Tuya isn't set up. An admin can set it up here.</p></div>` : "");
}

function tuyaCard(x) {
  const sw = arr(x.switches), rd = arr(x.readings);
  return `<div class="card tuya-card${x.online ? "" : " off"}">
    <div class="tuya-top">${onDot(x.online, x.seen * 1000)}<div class="tuya-name"><b>${esc(x.name)}</b><div class="fine muted">${esc([x.kind || x.product, x.unifi_name && x.unifi_name !== x.name ? `UniFi: ${x.unifi_name}` : "", x.ip].filter(Boolean).join(" · "))}</div></div>
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

async function tuyaDo(op, body, msg) {
  try { S.tuya = await api(`/api/tuya/${op}`, body || {}); if (msg) toast(msg, "ok"); if ($("#tuya-body")) $("#tuya-body").innerHTML = tuyaBody(S.tuya); return true; }
  catch (err) { toast(err.message, "err"); return false; }
}

function tuyaDeviceModal(x) {
  const local = S.tuya?.mode === "local";
  openModal({ title: x.id ? x.name || "Tuya device" : "Add a Tuya device", body: `${x.id && !local ? `<p class="fine muted">From the Tuya Cloud. ID <span class="mono">${esc(x.id)}</span>${x.version ? ` · protocol ${esc(x.version)}` : ""}${x.ip ? ` · ${esc(x.ip)}` : ""}</p>` : ""}
    ${local ? `<div class="grid g2"><label>Name<input id="td-name" value="${esc(x.name || "")}" placeholder="Kitchen plug"></label>
      <label>Device ID<input id="td-id" value="${esc(x.id || "")}" spellcheck="false" ${x.known ? "readonly" : ""}></label>
      <label>Address on the home network<input id="td-ip" value="${esc(x.ip || "")}" placeholder="192.168.1.50" spellcheck="false"></label>
      <label>Protocol<select id="td-ver">${["3.3", "3.4", "3.5"].map(v => `<option ${v === (x.version || "3.3") ? "selected" : ""}>${v}</option>`).join("")}</select></label>
      <label>Local key<input id="td-key" type="password" autocomplete="new-password" placeholder="${x.has_key ? "saved — leave empty to keep" : "16 characters"}"></label></div>
      <p class="fine">The device ID is in Smart Life › the device › ✎ › Device information (Virtual ID). Give the device a fixed address in UniFi so HomeOwl keeps finding it.</p>` : ""}`,
    buttons: [{ label: "Close" }, ...(x.known ? [{ label: "Remove", onClick: async () => { if (await tuyaDo("remove", { id: x.id }, "Removed")) closeModal(); } }] : []),
      ...(local ? [{ label: "Save", cls: "primary", onClick: async () => {
        if (await tuyaDo("device", { id: $("#td-id").value, name: $("#td-name").value, ip: $("#td-ip").value, version: $("#td-ver").value, local_key: $("#td-key").value }, "Saved")) closeModal();
      } }] : [])] });
}

document.addEventListener("change", e => {
  if (e.target.id !== "tuya-mode") return;
  const m = e.target.value;
  $("#tuya-cloud").hidden = m !== "cloud"; $("#tuya-local").hidden = m !== "local"; $("#tuya-nets").hidden = !m;
});
document.addEventListener("toggle", e => { if (e.target.id === "tuya-set") S.tuyaSet = e.target.open; }, true);
document.addEventListener("click", async e => {
  const t = e.target;
  const sw = t.closest("[data-tuyasw]");
  if (sw) { sw.disabled = true; const on = !sw.dataset.on; await tuyaDo("switch", { id: sw.dataset.tuyasw, code: sw.dataset.code, on }, `Turned ${on ? "on" : "off"}`); return; }
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
