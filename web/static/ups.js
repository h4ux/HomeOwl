/* HomeOwl — UPS (NUT): power status, battery history, power cuts, and the scripts this computer runs when the power fails.
   Depends on core.js, app.js and dnsvpn.js (copyBox) */
"use strict";

S.ups = null;          // last /api/ups answer (or the demo's)
S.upsDays = 1;
S.upsPick = "";        // which UPS the charts show
S.upsEdit = null;      // the actions/shutdown settings being edited
S.upsChange = false;   // showing the connect form while connected

const UPS_EVENTS = { onbatt: "On battery", online: "Back on mains", lowbatt: "Battery low", fsd: "Forced shutdown (from the NUT server)",
  commbad: "Lost the UPS", commok: "Reaches the UPS again", replbatt: "Battery needs replacing", shutdown: "Time to shut down" };
const UPS_EV_ORDER = ["shutdown", "onbatt", "online", "lowbatt", "fsd", "commbad", "commok", "replbatt"];
const UPS_EV_TAG = { onbatt: "warn", online: "good", lowbatt: "bad", fsd: "bad", shutdown: "bad", commbad: "bad", commok: "good", replbatt: "warn", script: "info", test: "" };
const upsEvLabel = k => k === "script" ? "Script" : k === "test" ? "Test" : UPS_EVENTS[k] || k;
const upsPct = v => v === null || v === undefined ? "–" : `${Math.round(v)}%`;
const upsDur = s => s < 60 ? `${Math.max(1, Math.round(s))}s` : fmtDur(s);
const upsVolts = v => v === null || v === undefined ? "–" : `${Math.round(v)} V`;

V.ups = () => `<div class="head"><div><h1>UPS</h1><div class="muted">Battery backup through NUT (Network UPS Tools): power status and history, power cuts, and what this computer does when the power fails.</div></div></div>
  <div id="ups-body"><div class="empty">Loading…</div></div>`;

// demo: a fictional rack UPS with a 12-minute power cut three hours ago
function upsFromBundle() {
  const u = S.bundle?.ups; if (!u) return { demo: true, configured: false };
  const now = Date.now(), cutAt = now - 3 * 3600e3, cutEnd = cutAt + 12 * 60e3, samples = [];
  const step = S.upsDays > 1 ? 10 * 60e3 : 5 * 60e3;
  for (let t = now - S.upsDays * 86400e3; t <= now; t += step) {
    const onb = t >= cutAt && t < cutEnd, after = t >= cutEnd ? Math.min(1, (t - cutEnd) / 5400e3) : 1;
    const charge = onb ? 100 - (t - cutAt) / 60e3 * 2.4 : t >= cutEnd ? 71 + 29 * after : 100;
    const wob = Math.sin(t / 3.7e6) * 2;
    samples.push({ t, u: "rack", s: onb ? "OB DISCHRG" : charge < 100 ? "OL CHRG" : "OL", c: Math.round(charge), r: Math.round(charge * 21), l: Math.round(22 + wob), i: onb ? 0 : Math.round(230 + wob), o: 230 });
  }
  const ev = arr(u.events).map(e => ({ ...e, t: now - e.ago * 1000 }));
  const out = [{ start: cutAt, end: cutEnd, ups: "rack", min_charge: 71, shutdown: false }];
  return { demo: true, configured: true, days: S.upsDays, status: { ...u.status, ups: arr(u.status.ups).map(x => ({ ...x, updated: now })) }, samples, events: ev, outages: out };
}

async function loadUPS() {
  const el = $("#ups-body"); if (!el) return;
  const live = S.source === "live" || S.source === "none"; // a UPS doesn't need UniFi
  if (live) {
    try { S.ups = await api(`/api/ups?days=${S.upsDays}`); }
    catch (err) { el.innerHTML = `<div class="card"><p class="muted">${esc(err.message)}</p></div>`; return; }
  } else S.ups = upsFromBundle();
  if (S.ups.settings && !S.upsEdit) S.upsEdit = JSON.parse(JSON.stringify(S.ups.settings));
  if ($("#ups-body")) $("#ups-body").innerHTML = upsBody(S.ups);
}

function upsConnectCard(d) {
  const s = d.settings || {};
  return `<div class="card section"><h2>${d.configured ? "Change the NUT server" : "Connect a NUT server"}</h2>
    <p>HomeOwl reads your UPS from the NUT server it's plugged into (a Raspberry Pi, a NAS like Synology or Unraid, or any computer running <span class="mono">upsd</span>), like NUT's own <span class="mono">upsmon</span> does.</p>
    <div class="grid g2"><label>Server IP or name<input id="ups-host" value="${esc(s.host || "")}" placeholder="192.168.1.20" spellcheck="false" autocomplete="off"></label>
      <label>Port<input id="ups-port" type="number" min="1" max="65535" value="${esc(s.port || 3493)}"></label>
      <label>User<input id="ups-user" value="${esc(s.user || "")}" placeholder="from upsd.users" spellcheck="false" autocomplete="off"></label>
      <label>Password<input id="ups-pass" type="password" autocomplete="new-password" placeholder="${s.has_password ? "saved — leave empty to keep it" : ""}"></label>
      <label>UPS name <span class="fine">(optional)</span><input id="ups-name" value="${esc(s.name || "")}" placeholder="the first one on the server" spellcheck="false"></label></div>
    <label class="check"><input type="checkbox" id="ups-secondary" ${s.secondary ? "checked" : ""}> Register as a secondary (like <span class="mono">upsmon</span>), so the NUT server waits for this computer before it turns the power off</label>
    <p class="fine">Synology's built-in NUT server uses user <span class="mono">monuser</span> with password <span class="mono">secret</span>, and you add this computer's IP under Permitted DiskStation devices. For a secondary, the user needs <span class="mono">upsmon secondary</span> in <span class="mono">upsd.users</span>. The password is kept in the system keychain. NUT sends it unencrypted, so use it on your home network only.</p>
    <div class="btnrow"><button class="btn primary" id="ups-connect">${d.configured ? "Test and save" : "Connect"}</button>${d.configured ? `<button class="btn" id="ups-cancel">Cancel</button>` : ""}</div></div>`;
}

function upsStateTag(u) {
  if (u.comm_bad) return `<span class="tag bad">no contact</span>`;
  const f = String(u.status || "").split(/\s+/);
  if (f.includes("FSD")) return `<span class="tag bad">forced shutdown</span>`;
  if (f.includes("OB")) return `<span class="tag ${f.includes("LB") ? "bad" : "warn"}">on battery${f.includes("LB") ? ", low" : ""}</span>`;
  if (f.includes("OL")) return `<span class="tag good">on mains</span>`;
  return `<span class="tag">${esc(u.text || "unknown")}</span>`;
}

function upsCard(u, st) {
  const v = u.vars || {}, model = [v["ups.mfr"] || v["device.mfr"], v["ups.model"] || v["device.model"]].filter(Boolean).join(" ");
  const onb = u.on_battery_since ? `on battery for ${fmtDur((Date.now() - u.on_battery_since) / 1000)}` : "";
  const lowCharge = u.charge !== null && u.charge !== undefined && u.charge < 40;
  return `<div class="card section ups-card"><div class="ups-head"><div><h2>${esc(u.name)} ${upsStateTag(u)}${u.power ? ` <span class="tag info">powers this computer</span>` : ""}</h2>
      <div class="fine">${esc([u.desc, model].filter(Boolean).join(" · ") || "")}${u.updated ? ` · read ${ago(u.updated)}` : ""}</div></div></div>
    ${u.error ? `<p class="${u.comm_bad ? "bad-t" : "warn-t"}">${esc(u.error)}</p>` : ""}
    ${u.login_error ? `<p class="warn-t">Not registered as a secondary: ${esc(u.login_error)}</p>` : ""}
    <div class="grid g4">
      ${kpi(upsPct(u.charge), "Battery", `<div class="ups-bar${lowCharge ? " low" : ""}"><i style="width:${Math.max(0, Math.min(100, u.charge || 0))}%"></i></div>`)}
      ${kpi(u.runtime ? fmtDur(u.runtime) : "–", "Runtime left", onb ? `<span class="warn-t">${esc(onb)}</span>` : "at the current load")}
      ${kpi(upsPct(u.load), "Load", v["ups.realpower.nominal"] && u.load ? `about ${Math.round(v["ups.realpower.nominal"] * u.load / 100)} W of ${esc(v["ups.realpower.nominal"])} W` : "")}
      ${kpi(upsVolts(u.input_voltage), "Mains", u.output_voltage ? `output ${upsVolts(u.output_voltage)}` : v["input.frequency"] ? `${esc(v["input.frequency"])} Hz` : "")}
    </div>
    <details class="adv"><summary>All values from NUT (${Object.keys(v).length})</summary>
      <div class="tbl-wrap"><table class="ups-kv">${Object.keys(v).sort().map(k => `<tr><td class="mono">${esc(k)}</td><td>${esc(v[k])}</td></tr>`).join("")}</table></div></details></div>`;
}

function upsCharts(d, list) {
  const name = list.some(u => u.name === S.upsPick) ? S.upsPick : (list.find(u => u.power) || list[0] || {}).name;
  const ss = arr(d.samples).filter(s => s.u === name);
  const pts = k => ss.map(s => ({ t: s.t, v: s[k] ?? null }));
  return `<div class="card section"><div class="ups-head"><h2>History</h2><div class="btnrow">
      ${list.length > 1 ? `<select id="ups-pick">${list.map(u => `<option ${u.name === name ? "selected" : ""}>${esc(u.name)}</option>`).join("")}</select>` : ""}
      <div class="seg">${[[1, "24 hours"], [7, "7 days"]].map(([x, l]) => `<button class="${S.upsDays === x ? "on" : ""}" data-upsdays="${x}">${l}</button>`).join("")}</div></div></div>
    ${ss.length ? `<div class="grid g2"><div><h3 class="fine">Battery and load</h3>${lineChart([{ name: "Battery %", points: pts("c"), fill: true }, { name: "Load %", points: pts("l") }], { fmt: v => `${Math.round(v)}%`, height: 200 })}</div>
      <div><h3 class="fine">Mains voltage</h3>${lineChart([{ name: "Input V", points: pts("i") }], { fmt: v => `${Math.round(v)} V`, height: 200 })}</div></div>`
      : `<div class="empty">HomeOwl keeps a reading every minute for 7 days. The first ones show up here in a minute.</div>`}</div>`;
}

function upsBody(d) {
  const admin = S.role === "admin" && !d.demo;
  if (!d.configured) return d.demo ? `<div class="card"><p class="muted">The demo has no UPS.</p></div>`
    : admin ? upsConnectCard(d) : `<div class="card"><p class="muted">No UPS yet. An admin can connect a NUT server here.</p></div>`;
  const st = d.status || {}, list = arr(st.ups);
  const sorted = [...list].sort((a, b) => (b.power ? 1 : 0) - (a.power ? 1 : 0));
  const out = arr(d.outages), events = arr(d.events);
  const yearAgo = Date.now() - 365 * 86400e3;
  const cuts = out.filter(o => o.start >= yearAgo);
  return `${st.state === "error" ? `<div class="banner warn">Can't read the NUT server at ${esc(st.server || "")}: ${esc(st.error || "")}</div>` : st.state === "connecting" ? `<div class="banner">Connecting to ${esc(st.server || "the NUT server")}…</div>` : ""}
    ${st.shutdown_fired ? `<div class="banner bad">HomeOwl started the shutdown actions ${ago(st.shutdown_fired)}.</div>` : ""}
    ${arr(st.pending).length ? `<div class="banner">Waiting to run: ${esc(st.pending.join("; "))}</div>` : ""}
    ${sorted.map(u => upsCard(u, st)).join("") || (st.state === "live" ? `<div class="card"><p class="muted">The NUT server has no UPS.</p></div>` : "")}
    ${list.length ? upsCharts(d, list) : ""}
    <div class="grid g2 section">
      <div class="card"><h2>Power cuts <span class="muted" style="font-weight:400;font-size:13px">· last year</span></h2>
        ${table("upscuts", [
          { k: "start", label: "When", fmt: o => fmtTime(o.start), sort: o => -o.start },
          { k: "dur", label: "How long", fmt: o => o.end ? upsDur((o.end - o.start) / 1000) : `<span class="warn-t">still on battery</span>` },
          { k: "min", label: "Lowest", fmt: o => upsPct(o.min_charge) },
          { k: "ups", label: "", fmt: o => `${list.length > 1 ? esc(o.ups) + " " : ""}${o.shutdown ? `<span class="tag bad">shut down</span>` : ""}` },
        ], cuts, { empty: "No power cuts since HomeOwl started watching" })}</div>
      <div class="card"><h2>Events</h2>
        ${table("upsev", [
          { k: "t", label: "When", fmt: e => fmtTime(e.t), sort: e => -e.t },
          { k: "kind", label: "", fmt: e => `<span class="tag ${UPS_EV_TAG[e.kind] ?? ""}">${esc(upsEvLabel(e.kind))}</span>` },
          { k: "text", label: "What", fmt: e => `${esc(e.text)}${e.by ? ` <span class="fine">by ${esc(e.by)}</span>` : ""}${e.output ? `<details class="ups-out"><summary>output</summary><pre class="json">${esc(e.output)}</pre></details>` : ""}` },
        ], events.slice(0, 200), { empty: "Nothing yet" })}</div>
    </div>
    ${admin ? upsAdmin(d) : ""}`;
}

// ---- admin: shutdown rule, actions, scripts, connection
function upsAdmin(d) {
  const e = S.upsEdit || d.settings || {}, scripts = arr(d.scripts), list = arr(d.status?.ups);
  const okScripts = scripts.filter(s => !s.problem).map(s => s.name);
  const actRow = (a, i) => `<tr><td><select data-upsact="${i}" data-f="event">${UPS_EV_ORDER.map(k => `<option value="${k}" ${a.event === k ? "selected" : ""}>${esc(UPS_EVENTS[k])}</option>`).join("")}</select></td>
    <td><select data-upsact="${i}" data-f="script">${[...new Set([...(a.script ? [a.script] : []), ...okScripts])].map(n => `<option ${a.script === n ? "selected" : ""}>${esc(n)}</option>`).join("") || `<option value="">no scripts yet</option>`}</select>
      ${a.script && !okScripts.includes(a.script) ? `<div class="bad-t fine">${esc(scripts.find(s => s.name === a.script)?.problem || "not in the scripts folder")}</div>` : ""}</td>
    <td><input type="number" min="0" max="3600" value="${esc(a.delay || 0)}" data-upsact="${i}" data-f="delay" class="ups-delay"> s</td>
    <td><button class="btn sm" data-upsdel="${i}" aria-label="Remove">✕</button></td></tr>`;
  const sug = arr(d.suggestions);
  const osName = { darwin: "macOS", windows: "Windows", linux: "Linux" }[d.os] || d.os;
  return `<div class="card section"><h2>When the power fails</h2>
    <p class="fine">Like NUT's <span class="mono">upsmon</span>: HomeOwl watches ${list.length > 1 ? `the UPS that powers this computer` : "the UPS"} and runs your scripts on these events. <b>Time to shut down</b> fires once per power cut when:</p>
    <label class="check"><input type="checkbox" id="ups-lb" ${e.low_battery ? "checked" : ""}> the UPS says its battery is low (recommended; this is what <span class="mono">upsmon</span> does)</label>
    <div class="grid g3"><label>or after this long on battery<span class="ups-in"><input type="number" min="0" max="1440" id="ups-after" value="${esc(e.after_minutes || "")}" placeholder="off"> min</span></label>
      <label>or at this battery charge<span class="ups-in"><input type="number" min="0" max="95" id="ups-charge" value="${esc(e.below_charge || "")}" placeholder="off"> %</span></label>
      <label>or with this little runtime left<span class="ups-in"><input type="number" min="0" max="600" id="ups-runtime" value="${esc(e.below_runtime || "")}" placeholder="off"> min</span></label></div>
    <p class="fine">A forced shutdown from the NUT server, or losing the UPS for 15 seconds while it's on battery, always counts.</p>
    ${list.length > 1 ? `<label>The UPS that powers this computer<select id="ups-power"><option value="">the first one</option>${list.map(u => `<option ${e.name === u.name ? "selected" : ""}>${esc(u.name)}</option>`).join("")}</select></label>` : ""}
    <h3>Actions</h3>
    ${arr(e.actions).length ? `<div class="tbl-wrap"><table class="ups-acts"><thead><tr><th>When</th><th>Run</th><th>Wait first</th><th></th></tr></thead><tbody>${arr(e.actions).map(actRow).join("")}</tbody></table></div>
      <p class="fine">“Wait first” runs the script only if the event still holds afterwards, for example on battery for more than 60 seconds. It's skipped if the power comes back first.</p>`
      : `<p class="muted">No actions yet. Add a shutdown script below, then pick it here.</p>`}
    <div class="btnrow"><button class="btn sm" id="ups-addact" ${okScripts.length ? "" : "disabled"}>Add action</button>
      <label class="ups-in fine">Read the UPS every <input type="number" min="2" max="300" id="ups-poll" value="${esc(e.poll || 5)}"> s</label></div>
    <div class="btnrow"><button class="btn primary" id="ups-save">Save</button></div></div>

  <div class="card section"><h2>Scripts</h2>
    <p>For safety, HomeOwl only runs scripts from its own scripts folder on this computer. The web page can't add commands. Put your scripts here (only this computer's user may change them):</p>
    ${copyBox("ups-folder", d.folder || "")}
    ${scripts.length ? table("upsscripts", [
      { k: "name", label: "Script", fmt: s => `<span class="mono">${esc(s.name)}</span>${s.problem ? `<div class="bad-t fine">${esc(s.problem)}</div>` : ""}` },
      { k: "modified", label: "Changed", fmt: s => fmtTime(s.modified) },
      { k: "test", label: "", fmt: s => s.problem ? "" : `<button class="btn sm" data-upstest="${esc(s.name)}">Test…</button>` },
    ], scripts) : `<p class="muted">The folder is empty.</p>`}
    <p class="fine">Scripts get <span class="mono">NOTIFYTYPE</span> (ONBATT, ONLINE, LOWBATT, FSD, COMMBAD, COMMOK, REPLBATT, SHUTDOWN) and <span class="mono">UPSNAME</span> like <span class="mono">upsmon</span>'s NOTIFYCMD, plus
      <span class="mono">HOMEOWL_UPS_STATUS</span>, <span class="mono">HOMEOWL_UPS_CHARGE</span>, <span class="mono">HOMEOWL_UPS_RUNTIME_MIN</span>, <span class="mono">HOMEOWL_UPS_LOAD</span>, and <span class="mono">HOMEOWL_UPS_TEST=1</span> when you press Test. They run as ${esc(d.run_as || "HomeOwl's user")}${d.root ? " (with full rights)" : ""} for at most 2 minutes.</p></div>

  <div class="card section"><h2>Suggested scripts for ${esc(osName)}</h2>
    <p class="fine">HomeOwl runs on ${esc(osName)}${d.os === "darwin" ? " as your user (a LaunchAgent)" : d.root ? " with full rights" : ` as ${esc(d.run_as || "a normal user")}`}. These scripts fit that. They only describe what they would do when you press Test.</p>
    ${sug.map((t, i) => `<div class="ups-sug"><div class="ups-head"><div><h3>${esc(t.title)} <span class="mono fine">${esc(t.name)}</span></h3><p class="fine">${esc(t.description)} Suggested for: <b>${esc(UPS_EVENTS[t.event] || t.event)}</b>${t.event === "onbatt" ? ", Back on mains, Battery low, Lost the UPS" : ""}.</p></div>
        <div>${t.exists ? (t.same ? `<span class="tag good">in the folder</span>` : `<button class="btn sm" data-upssave="${esc(t.name)}" data-over="1">Replace with this</button>`) : `<button class="btn sm primary" data-upssave="${esc(t.name)}">Save to the scripts folder</button>`}</div></div>
      ${t.setup ? `<p class="warn-t fine">${esc(t.setup.split("\n")[0])}</p>${copyBox("ups-setup" + i, t.setup.split("\n").slice(1).join("\n"))}` : ""}
      <details class="adv"><summary>Show the script</summary>${copyBox("ups-sug" + i, t.content.replace(/\r\n/g, "\n"))}</details></div>`).join("")}</div>

  ${S.upsChange ? upsConnectCard(d) : `<div class="card section"><h2>NUT server</h2>
    <p class="fine">Reading <span class="mono">${esc(d.settings?.user ? d.settings.user + "@" : "")}${esc(d.settings?.host || "")}:${esc(d.settings?.port || 3493)}</span>${d.status?.secondary ? ", registered as a secondary" : ""}.</p>
    <div class="btnrow"><button class="btn sm" id="ups-change">Change…</button><button class="btn sm" id="ups-disconnect">Disconnect</button></div></div>`}`;
}

function upsCollect() {
  const e = S.upsEdit; if (!e) return;
  const v = id => $(id) ? $(id).value.trim() : "";
  e.low_battery = !!$("#ups-lb")?.checked;
  e.after_minutes = +v("#ups-after") || 0; e.below_charge = +v("#ups-charge") || 0; e.below_runtime = +v("#ups-runtime") || 0;
  e.poll = +v("#ups-poll") || 5;
  if ($("#ups-power")) e.name = $("#ups-power").value;
}
const upsRedraw = () => { if ($("#ups-body") && S.ups) $("#ups-body").innerHTML = upsBody(S.ups); };

function upsTest(name) {
  const def = arr(S.upsEdit?.actions).find(a => a.script === name)?.event || (/shutdown/.test(name) ? "shutdown" : "onbatt");
  openModal({ title: `Test ${name}?`, body: `<p>This runs <b class="mono">${esc(name)}</b> on this computer now, as if this event happened, with <span class="mono">HOMEOWL_UPS_TEST=1</span>. HomeOwl's suggested scripts then only say what they would do. <b>Your own scripts must check it themselves.</b></p>
    <label>Pretend this happened<select id="ups-testev">${UPS_EV_ORDER.map(k => `<option value="${k}" ${k === def ? "selected" : ""}>${esc(UPS_EVENTS[k])}</option>`).join("")}</select></label>
    <p class="fine">Needs changes to be unlocked (sidebar). The test is written to the Changes log.</p>`,
    buttons: [{ label: "Cancel" }, { label: "Run test", cls: "primary", onClick: async () => {
      const ev = $("#ups-testev")?.value || def; closeModal();
      try {
        const r = await api("/api/ups/script/test", { script: name, event: ev });
        openModal({ title: r.exit === 0 && !r.error ? `${name} ran fine` : `${name} failed`, body: `<p class="fine">Exit code ${esc(r.exit)} · ${esc(r.ms)} ms${r.error ? ` · ${esc(r.error)}` : ""}</p><pre class="json">${esc(r.output || "(no output)")}</pre>`, buttons: [{ label: "Close" }] });
        loadUPS();
      } catch (err) { toast(err.message, "err"); }
    } }] });
}

document.addEventListener("click", async e => {
  if (!e.target.closest("#ups-body")) return;
  const days = e.target.closest("[data-upsdays]");
  if (days) { S.upsDays = +days.dataset.upsdays; upsCollect(); loadUPS(); return; }
  const del = e.target.closest("[data-upsdel]");
  if (del) { upsCollect(); S.upsEdit.actions.splice(+del.dataset.upsdel, 1); upsRedraw(); return; }
  const tst = e.target.closest("[data-upstest]");
  if (tst) { upsTest(tst.dataset.upstest); return; }
  const sv = e.target.closest("[data-upssave]");
  if (sv) {
    try { upsCollect(); S.ups = await api("/api/ups/script/save", { name: sv.dataset.upssave, overwrite: !!sv.dataset.over }); upsRedraw(); toast(`${sv.dataset.upssave} saved to the scripts folder`, "ok"); }
    catch (err) { toast(err.message, "err"); }
    return;
  }
  const id = e.target.id;
  if (id === "ups-addact") {
    upsCollect(); const scripts = arr(S.ups.scripts).filter(s => !s.problem).map(s => s.name);
    const has = new Set(arr(S.upsEdit.actions).map(a => a.event));
    S.upsEdit.actions = [...arr(S.upsEdit.actions), { event: has.has("shutdown") ? "onbatt" : "shutdown", script: scripts.find(n => /shutdown/.test(n) && !has.has("shutdown")) || scripts.find(n => /notify/.test(n)) || scripts[0] || "", delay: 0 }];
    upsRedraw();
  } else if (id === "ups-save") {
    upsCollect(); e.target.disabled = true;
    try { S.ups = await api("/api/ups/settings", { ...S.upsEdit, secondary: !!S.ups.settings?.secondary }); S.upsEdit = JSON.parse(JSON.stringify(S.ups.settings)); upsRedraw(); toast("UPS settings saved", "ok"); }
    catch (err) { toast(err.message, "err"); e.target.disabled = false; }
  } else if (id === "ups-connect") {
    e.target.disabled = true;
    const v = q => $(q)?.value.trim() || "";
    try {
      const r = await api("/api/ups/connect", { host: v("#ups-host"), port: +v("#ups-port") || 3493, user: v("#ups-user"), password: $("#ups-pass")?.value || "", name: v("#ups-name"), secondary: !!$("#ups-secondary")?.checked });
      toast(`Connected: ${arr(r.ups).map(u => u.name).join(", ")}`, "ok"); S.upsChange = false; S.upsEdit = null; setTimeout(loadUPS, 1500);
    } catch (err) { toast(err.message, "err"); e.target.disabled = false; }
  } else if (id === "ups-change") { S.upsChange = true; upsRedraw(); }
  else if (id === "ups-cancel") { S.upsChange = false; upsRedraw(); }
  else if (id === "ups-disconnect") {
    openModal({ title: "Disconnect the NUT server?", body: "<p>HomeOwl stops watching the UPS and forgets the password. While it's disconnected, <b>no scripts run</b>, including the shutdown. The history and your actions are kept.</p>",
      buttons: [{ label: "Cancel" }, { label: "Disconnect", cls: "primary", onClick: async () => { closeModal();
        try { S.ups = await api("/api/ups/disconnect", {}); S.upsEdit = null; upsRedraw(); } catch (err) { toast(err.message, "err"); } } }] });
  }
});
document.addEventListener("change", e => {
  const a = e.target.closest("[data-upsact]");
  if (a && S.upsEdit) { const x = S.upsEdit.actions[+a.dataset.upsact]; if (x) x[a.dataset.f] = a.dataset.f === "delay" ? Math.max(0, +a.value || 0) : a.value; return; }
  if (e.target.id === "ups-pick") { S.upsPick = e.target.value; upsCollect(); upsRedraw(); }
});

// refresh the status every 15 s while the page is open (the monitor itself reads the UPS every few seconds)
setInterval(() => { if (S.view === "ups" && (S.source === "live" || S.source === "none") && S.ups?.configured && !S.upsChange && !document.hidden && $("#modal").hidden && !document.activeElement?.closest?.("#ups-body input, #ups-body select")) { upsCollect(); loadUPS(); } }, 15000);

{ const _render = render; render = function () { _render(); if (S.view === "ups") loadUPS(); }; }
