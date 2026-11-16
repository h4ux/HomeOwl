/* HomeOwl — device check-ins (admins): devices that introduced themselves on the "Hi HomeOwl" page wait here.
   Approving (here, from a notification or in Devices › Grace period) names it in UniFi (allow-listed, previewed here,
   undoable), gives it to a person and lets it on the network — one approval, server-side (checkin.go). */
"use strict";

S.checkins = null;
V.checkins = () => `<div class="head"><div><h1>New devices ${hint("Family members name their own devices on the check-in page; you approve. Approval, grace period, merging and setup are here too.")}</h1></div>
  <button class="btn" id="ci-reload">Refresh</button></div><div id="ci-body"><div class="empty">Loading…</div></div>`;

const CI_KIND = { phone: "📱", tablet: "📱", laptop: "💻", computer: "🖥", tv: "📺", console: "🎮", speaker: "🔊", watch: "⌚", camera: "📷", printer: "🖨", other: "❔" };
// merging: the same device checked in twice (merge.go). ids: the duplicates, or one device to pick a partner for.
async function mergeCheckins(ids) {
  let ci; try { ci = await api("/api/checkins"); } catch (err) { return toast(err.message, "err"); }
  const done = ci.items.filter(x => x.status === "approved"), by = id => done.find(x => x.id === id);
  let group = ids.map(by).filter(Boolean);
  const single = ids.length === 1;
  if (!group.length) return toast("That device isn't approved any more — refresh.", "err");
  const out = new Set(); let keep = "";
  const keepID = () => { const inn = [...group].sort((a, b) => b.time - a.time).filter(x => !out.has(x.id)); return inn.find(x => x.id === keep)?.id || inn[0]?.id || group[0].id; };
  const st = x => { const v = ci.setup?.[x.id]; return v ? setupState(x, v) : "–"; };
  const draw = () => {
    group.sort((a, b) => b.time - a.time);
    const base = by(ids[0]), other = group.find(x => x.id !== base.id);
    const pick = single ? `<label>Same device as<select id="cim-other"><option value="">Choose…</option>${done.filter(x => x.id !== base.id)
      .sort((a, b) => (b.person_id === base.person_id) - (a.person_id === base.person_id) || a.name.localeCompare(b.name))
      .map(x => `<option value="${esc(x.id)}" ${other?.id === x.id ? "selected" : ""}>${esc(x.name)} · ${esc(x.person_name || "")} · ${esc(x.mac)}</option>`).join("")}</select></label>` : "";
    return `<p class="muted">A phone or computer that gets a new Wi-Fi address (a private address changed, its profile was reinstalled, the network was forgotten) shows up as a new device and gets checked in again. Merging keeps one, moves its person and approval to the address it uses now, and archives the old address. Nothing is deleted.</p>
      ${pick}
      ${group.length > 1 ? `<p><b>Which are the same device, and which one to keep?</b> The newest check-in is usually the one the device uses now. Untick one that's a different device — HomeOwl won't suggest that pair again.</p>
        <div class="cim-list">${group.map((x, i) => `<div class="cim-opt${out.has(x.id) ? " cim-out" : ""}">
          <label class="cim-in" title="The same device"><input type="checkbox" data-cimin="${esc(x.id)}" ${out.has(x.id) ? "" : "checked"}> Same</label>
          <label class="cim-keep"><input type="radio" name="cim-keep" value="${esc(x.id)}" ${x.id === keepID() ? "checked" : ""} ${out.has(x.id) ? "disabled" : ""}>
          <div><b>${esc(x.name)}</b> <span class="fine muted mono">${esc(x.mac)}</span><div class="fine muted">${esc(x.person_name || "")} · checked in ${ago(x.time * 1000)}${S.archived?.has?.(x.mac) ? " · archived" : ""}</div><div class="fine">${st(x)}</div></div></label></div>`).join("")}</div>` : ""}`;
  };
  const dismiss = async l => { if (l.length > 1) S.checkins = await api("/api/checkins/merge-dismiss", { ids: l }); };
  const refresh = () => { closeModal(); loadAlerts?.(); if ($("#ci-body")) $("#ci-body").innerHTML = checkinsBody(S.checkins); else render(); };
  const go = async () => {
    const inn = group.filter(x => !out.has(x.id));
    if (group.length < 2) return toast("Pick the other device first.", "err");
    if (inn.length < 2) return toast("Tick at least two that are the same device — or choose “Not the same device”.", "err");
    const keep = keepID();
    try {
      S.checkins = await api("/api/checkins/merge", { keep, drop: inn.map(x => x.id).filter(id => id !== keep) });
      for (const x of group.filter(x => out.has(x.id))) await dismiss([x.id, keep]); // a different device: don't suggest it again
    } catch (err) { return toast(err.message, "err"); }
    refresh(); toast("Merged", "ok");
  };
  const notSame = async () => {
    try { await dismiss(group.map(x => x.id)); } catch (err) { return toast(err.message, "err"); }
    refresh(); toast("Got it — won't suggest these again", "ok");
  };
  openModal({ title: `Merge ${group[0].name}`, wide: true, body: `<div id="cim-body">${draw()}</div>`,
    buttons: [{ label: "Cancel" }, ...(single ? [] : [{ label: "Not the same device", onClick: notSame }]), { label: "Merge", cls: "primary", onClick: go }] });
  $("#cim-body").addEventListener("change", e => {
    if (e.target.name === "cim-keep") { keep = e.target.value; return; }
    if (e.target.dataset.cimin) { e.target.checked ? out.delete(e.target.dataset.cimin) : out.add(e.target.dataset.cimin); $("#cim-body").innerHTML = draw(); return; }
    if (e.target.id !== "cim-other") return;
    const o = by(e.target.value); group = [by(ids[0]), ...(o ? [o] : [])]; $("#cim-body").innerHTML = draw();
  });
}
document.addEventListener("click", e => { const m = e.target.closest("[data-cimerge]"); if (m) mergeCheckins(m.dataset.cimerge.split(",")); });

async function loadCheckins() {
  if (S.source !== "live") { $("#ci-body").innerHTML = `<div class="empty">Device check-in needs a live HomeOwl (not the demo).</div>`; return; }
  try {
    const [ci, meta, nd] = await Promise.all([api("/api/checkins"), api("/api/analytics/meta", {}).catch(() => ({})),
      S.ciND ? Promise.resolve(S.ciND) : api("/api/checkins/ndprofiles").catch(() => ({ profiles: [], by_person: {} })),
      api("/api/devtypes").catch(() => ({ types: [], wlans: [] })).then(t => { S.ciTypes = t; })]);
    S.checkins = ci; S.ciPeople = meta.people || []; S.ciND = nd;
  } catch (err) { $("#ci-body").innerHTML = `<div class="empty bad-t">${esc(err.message)}</div>`; return; }
  renderCheckinBadge(S.checkins.items.filter(x => x.status === "pending").length);
  if ($("#ci-body")) $("#ci-body").innerHTML = checkinsBody(S.checkins);
  loadSiteReqs();
}
function renderCheckinBadge(n) { const b = $("#nav-checkins"); if (b) { b.textContent = n || ""; b.classList.toggle("alert", !!n); } }

const ciPerson = x => S.ciPeople.find(p => p.id === x.person_id) || (x.person_name ? S.ciPeople.find(p => p.name.toLowerCase() === x.person_name.toLowerCase()) : null);
function personLabel(x) {
  const p = ciPerson(x);
  return p ? esc(p.name) : x.person_name ? `${esc(x.person_name)} <span class="tag warn" title="Not in People yet — approving adds them">new</span>` : "–";
}
function checkinsBody(c) {
  const pend = c.items.filter(x => x.status === "pending").sort((a, b) => b.time - a.time);
  const past = c.items.filter(x => x.status !== "pending").sort((a, b) => b.time - a.time).slice(0, 20);
  const link = c.links[0];
  const how = `<div class="card section ci-how">${link ? `<div class="guest-qr">${QR.svg(link, { size: 150, label: "Link to the check-in page" })}</div>` : ""}
    <div><h2>How family members check in</h2>
    <p>On the phone, tablet or laptop you want to name, connect to the home Wi-Fi and open:</p>
    ${link ? `<div class="cmd"><code>${esc(link)}</code><button class="btn sm" data-copy="${esc(link)}">Copy</button></div>` : `<p class="bad-t">No home-network address found.</p>`}
    ${c.links.length > 1 ? `<details class="ci-more"><summary class="fine">Other addresses (if that one doesn't open)</summary>${c.links.slice(1).map(l => `<div class="cmd"><code>${esc(l)}</code><button class="btn sm" data-copy="${esc(l)}">Copy</button></div>`).join("")}</details>` : ""}
    ${c.lan ? `<p class="fine">Or scan the code with the device's camera. HomeOwl recognises the device by its address on your network — nothing to install.</p>`
      : `<p class="bad-t">Home-network access is off (Settings › Access), so devices can't reach this page.</p>`}</div></div>`;
  const rows = pend.map(x => `<div class="card ci-row">
      <div class="ci-ico" aria-hidden="true">${CI_KIND[x.kind] || "❔"}</div>
      <div class="ci-main"><div><b>${esc(x.name)}</b> <span class="muted">for</span> ${personLabel(x)}</div>
        <div class="fine">Now called <b>${esc(x.seen || "unnamed")}</b>${x.vendor ? ` · ${esc(x.vendor)}` : ""} · <span class="mono">${esc(x.mac)}</span> · ${esc(x.ip)}${x.network ? ` · ${esc(x.network)}` : ""} · ${ago(x.time * 1000)}</div></div>
      <div class="btnrow"><button class="btn sm primary" data-ciok="${esc(x.id)}">Approve…</button><button class="btn sm" data-cino="${esc(x.id)}">Reject</button></div></div>`).join("");
  return `${pend.length ? rows : `<div class="card"><p class="muted" style="margin:0">Nothing waiting. New check-ins show up here (and as a number in the menu).</p></div>`}
    <div id="ci-reqs">${siteReqCard(S.siteReqs)}</div>
    ${setupCard(c)}
    ${how}
    ${past.length ? `<div class="card section"><h2>Recently decided</h2>${table("ci-past", [
      { k: "name", label: "Device", fmt: x => `${CI_KIND[x.kind] || ""} ${esc(x.name)}` }, { k: "mac", label: "MAC", fmt: x => `<span class="mono">${esc(x.mac)}</span>` },
      { k: "status", label: "Decision", fmt: x => `<span class="tag ${x.status === "approved" ? "good" : ""}">${esc(x.status)}</span> <span class="fine">${esc(x.by || "")}</span>` },
      { k: "time", label: "Asked", fmt: x => fmtTime(x.time * 1000) }], past)}</div>` : ""}`;
}

// approved devices and their setup (encrypted DNS through NextDNS under the device's name, devprofile.go):
// is it in use, from where, and which NextDNS profile it gets
const ndName = id => S.ciND?.profiles?.find(p => p.id === id)?.name || id;
function setupState(x, st) {
  st = st || {};
  if (!x.nd_profile && !st.type) return `<span class="fine muted">no setup</span>`;
  const seen = st.last_seen ? `last seen ${ago(st.last_seen * 1000)}${st.where === "home" ? " · at home" : st.where === "away" ? " · away from home" : ""}${st.day ? ` · ${st.day} today` : ""}` : "";
  const tag = {
    active: `<span class="tag good">active</span>`, quiet: `<span class="tag">quiet</span>`,
    stopped: `<span class="tag bad" title="It worked, then NextDNS stopped seeing it by name while the device was in use. The profile was probably removed, or Private DNS switched off. Open the check-in page on the device to set it up again.">stopped</span>`,
    not_seen: `<span class="tag warn" title="It was downloaded, but NextDNS hasn't logged any query under this device's name. It's probably not installed (NextDNS logs reach HomeOwl every few minutes).">not seen</span>`,
    offered: `<span class="tag" title="The device's check-in page offers it; nobody has set it up yet">not set up</span>`,
    unknown: `<span class="tag" title="Connect NextDNS logs in Settings › Analytics so HomeOwl can see whether it's in use">can't check</span>`,
    no_nextdns: st.downloaded ? `<span class="tag" title="Wi-Fi and VPN only. Without NextDNS HomeOwl can't see whether it's in use.">downloaded</span>` : `<span class="tag">not set up</span>`,
  }[st.state] || "";
  const upd = (st.update ? ` <span class="tag warn" title="Its settings changed after it was installed (NextDNS profile, Wi-Fi, VPN). Open the check-in page on the device to install the new ones.">update ready</span>` : "")
    + (st.mac === "fixed" ? ` <span class="tag good" title="It uses its real Wi-Fi address on the home network (from its profile), so it stays recognised even when it would change a private address">real address</span>`
      : st.mac === "waiting" ? ` <span class="tag" title="Its profile gives it its real Wi-Fi address. HomeOwl follows the switch once the device reconnects with it (same name, old address gone).">waiting for real address</span>` : "")
    + (st.wg === false ? ` <span class="tag warn" title="Its profile type has a WireGuard VPN, and each device needs its own config: paste it with Change…">needs WireGuard config</span>` : "")
    + (arr(st.wifi_no_password).length ? ` <span class="tag warn" title="UniFi didn't give HomeOwl the password of ${esc(st.wifi_no_password.join(", "))}, so it's left out of the profile (a profile without it would only ask for it). The UniFi account HomeOwl signs in with may not be allowed to see Wi-Fi passwords: give it a role that can (Site Admin or Super Admin), then install the profile again.">no Wi-Fi password</span>` : "");
  const note = st.state === "not_seen" ? `downloaded ${ago(st.downloaded * 1000)}, no queries under its name yet` : st.state === "offered" ? "open the check-in page on the device" : seen;
  const model = x.nd_profile ? ciModelOf(x) : "";
  return `${tag}${upd}${note ? `<div class="fine muted">${esc(note)}</div>` : ""}${model ? `<div class="fine muted">model: ${esc(ndModelName(model))}${x.nd_model && x.nd_model !== "-" ? " (set by an admin)" : x.user_model ? " (chosen on the device)" : " (found on the device)"}</div>` : ""}`;
}
// the model the setup reports, as the server picks it (devmodel.go ndModelOf)
const ciModelOf = x => x.nd_model === "-" ? "" : x.nd_model || (x.user_model === "-" ? "" : x.user_model) || x.det_model || "";
function setupCard(c) {
  const done = c.items.filter(x => x.status === "approved").sort((a, b) => b.time - a.time);
  if (!done.length) return "";
  return `<div class="card section"><h2>Devices set up ${hint("After approval, a device's check-in page offers encrypted DNS through the NextDNS profile you chose, under the device's name. NextDNS then recognises it at home, on mobile data and on other Wi-Fi, and HomeOwl sees whether it's in use.")}</h2>${table("ci-setup", [
    { k: "name", label: "Device", fmt: x => `${CI_KIND[x.kind] || ""} ${esc(x.name)}<div class="fine muted">${personLabel(x)}</div>` },
    { k: "nd_profile", label: "NextDNS profile", fmt: x => `${x.nd_profile ? esc(ndName(x.nd_profile)) : "–"}${c.setup?.[x.id]?.type ? `<div class="fine muted">type: ${esc(c.setup[x.id].type)}</div>` : ""}` },
    { k: "status", label: "Setup", sort: x => ({ stopped: -1, active: 0, quiet: 1, not_seen: 2, offered: 3, unknown: 4 })[c.setup?.[x.id]?.state] ?? 5, fmt: x => setupState(x, c.setup?.[x.id]) },
    { k: "id", label: "", fmt: x => `<div class="btnrow"><button class="btn sm" data-cisetup="${esc(x.id)}">${x.nd_profile || c.setup?.[x.id]?.type ? "Change…" : "Set up…"}</button>${done.length > 1 ? `<button class="btn sm ghost" data-cimerge="${esc(x.id)}" title="The same device checked in again under another Wi-Fi address">Merge…</button>` : ""}</div>` }], done)}</div>`;
}
// the profile type a device gets: picked for it ("-" = none), else its person's
// Apple models for the NextDNS setup come from the server (applemodels.go), with S.ciND: the profile reports the
// model's marketing name, and NextDNS shows it by the device's name. "Other…" takes any model identifier.
const ndModels = () => arr(S.ciND?.models);
const ndModelKnown = id => ndModels().some(g => arr(g.models).some(m => m.id === id));
const ndModelName = id => ndModels().flatMap(g => arr(g.models)).find(m => m.id === id)?.name || id;
function ndModelPick(pre, cur, found, picked) {
  const other = cur && cur !== "-" && !ndModelKnown(cur);
  return `<label>Device model ${hint("iPhone, iPad and Mac: the setup tells NextDNS the model, so its logs show it next to the device's name. Automatic uses the model the device announced on the home network when it checked in (Apple devices do; others announce nothing, and then no model is sent).")}<select id="${pre}-model">
    <option value="">${picked && picked !== "-" ? `Automatic — ${esc(ndModelName(picked))} (chosen on the device)` : picked === "-" ? "Automatic — none (chosen on the device)" : found ? `Automatic — ${esc(ndModelName(found))} (found on the device)` : "Automatic — none found on the device yet"}</option>
    <option value="-" ${cur === "-" ? "selected" : ""}>None</option>
    ${ndModels().map(g => `<optgroup label="${esc(g.group)}">${arr(g.models).map(m => `<option value="${esc(m.id)}" ${m.id === cur ? "selected" : ""}>${esc(m.name)}</option>`).join("")}</optgroup>`).join("")}
    <option value="*" ${other ? "selected" : ""}>Other — type its model identifier…</option></select></label>
    <label id="${pre}-modelwrap" ${other ? "" : "hidden"}>Model identifier<input id="${pre}-modelid" value="${other ? esc(cur) : ""}" placeholder="iPhone17,1" spellcheck="false" autocomplete="off"></label>`;
}
const ndModelVal = pre => { const v = $(`#${pre}-model`)?.value || ""; return v === "*" ? ($(`#${pre}-modelid`)?.value || "").trim() : v; };
document.addEventListener("change", e => { const m = e.target.id?.match(/^(ci|cis)-model$/); if (m) { const w = $(`#${m[1]}-modelwrap`); if (w) w.hidden = e.target.value !== "*"; } });
const ciPersonType = x => arr(S.ciTypes?.types).find(t => arr(t.people).includes(x.person_id));
const ciTypeOf = (x, pick) => pick === "-" ? null : pick ? arr(S.ciTypes?.types).find(t => t.id === pick) : ciPersonType(x);
function changeSetup(id) {
  const x = S.checkins.items.find(x => x.id === id); if (!x) return;
  const nd = S.ciND || { profiles: [] }, types = arr(S.ciTypes?.types), pt = ciPersonType(x), st = S.checkins.setup?.[id] || {};
  if (!nd.profiles.length && !types.length) return toast("Nothing to choose yet: add a NextDNS API key in Settings › Analytics, or a profile type in Settings › Device profiles", "err");
  openModal({ title: `Setup for ${x.name}`, body: `
    ${nd.profiles.length ? `<label>NextDNS profile<select id="cis-nd"><option value="">None</option>${nd.profiles.map(p => `<option value="${esc(p.id)}" ${p.id === x.nd_profile ? "selected" : ""}>${esc(p.name)} (${esc(p.id)})</option>`).join("")}</select></label>
      ${ndModelPick("cis", x.nd_model || "", x.det_model || "", x.user_model || "")}` : ""}
    ${types.length ? `<label>Profile type (Wi-Fi and VPN)<select id="cis-type">
      <option value="" ${!x.type_id ? "selected" : ""}>${pt ? `${esc(personLabel(x))}'s: ${esc(pt.name)}` : "Its person's (none yet)"}</option>
      ${types.map(t => `<option value="${esc(t.id)}" ${x.type_id === t.id ? "selected" : ""}>${esc(t.name)}</option>`).join("")}
      <option value="-" ${x.type_id === "-" ? "selected" : ""}>None</option></select></label>
      <div id="cis-wgwrap" hidden><label>This device's WireGuard config ${st.wg ? `<span class="tag good">saved</span> <a href="#" id="cis-wgrm">Remove</a>` : ""}
        <textarea id="cis-wg" rows="7" class="mono" spellcheck="false" placeholder="${st.wg ? "Paste a new one to replace it" : "[Interface]\nPrivateKey = …\nAddress = …\n\n[Peer]\nPublicKey = …\nEndpoint = …"}"></textarea></label>
        <p class="fine muted">Each device needs its own WireGuard peer (from your VPN server, e.g. UniFi › VPN › WireGuard › Add client › download). It's kept in HomeOwl's secret store and only goes into this device's profile. The device needs the WireGuard app.</p></div>` : ""}
    <p class="fine">The device keeps its current settings until it installs the new ones: its check-in page will say <b>Update ready</b> (iPhone, iPad, Mac: install the profile again; Android: paste the new Private DNS host name).</p>`,
    buttons: [{ label: "Cancel" }, { label: "Save", cls: "primary", onClick: async () => {
      const body = { id, nd_profile: $("#cis-nd") ? $("#cis-nd").value : x.nd_profile || "" };
      if ($("#cis-model")) body.nd_model = ndModelVal("cis");
      if ($("#cis-type")) body.type_id = $("#cis-type").value;
      const wg = $("#cis-wg")?.value.trim(); if (wg) body.wg = wg; else if ($("#cis-wg")?.dataset.rm) body.wg = "-";
      try { S.checkins = await api("/api/checkins/setup", body); closeModal(); toast("Saved — open the check-in page on the device to install it", "ok"); $("#ci-body").innerHTML = checkinsBody(S.checkins); }
      catch (err) { toast(err.message, "err"); }
    } }] });
  S.ciEditing = x; ciWgToggle(x);
}
function ciWgToggle(x) { const w = $("#cis-wgwrap"); if (w) w.hidden = ciTypeOf(x, $("#cis-type")?.value)?.vpn?.type !== "wireguard"; }
document.addEventListener("change", e => { if (e.target.id === "cis-type" && S.ciEditing) ciWgToggle(S.ciEditing); });
document.addEventListener("click", e => { if (e.target.id === "cis-wgrm") { e.preventDefault(); $("#cis-wg").dataset.rm = "1"; e.target.parentElement.innerHTML = "This device's WireGuard config <span class=\"tag warn\">removed when you save</span>"; } });

// ---- Settings › Device profiles: profile types (profiletypes.go)
const DT_ICONS = [["checkin", "The device's check-in page", "For updating the profile later. Good for most devices."], ["app", "HomeOwl", "Opens HomeOwl itself — for the phones of people with a HomeOwl account. Full screen like an app when HomeOwl has a trusted certificate."], ["none", "No icon", ""]];
const VPN_TYPES = [["", "No VPN"], ["ikev2", "IKEv2 (built in)"], ["l2tp", "L2TP over IPsec (built in)"], ["wireguard", "WireGuard (app)"]];
async function renderDevTypes() {
  const el = $("#st-devtypes"); if (!el) return;
  let r, people = [];
  try { [r, people] = await Promise.all([api("/api/devtypes"), api("/api/analytics/meta", {}).then(m => m.people || []).catch(() => [])]); } catch (e) { el.innerHTML = `<div class="empty bad-t">${esc(e.message)}</div>`; return; }
  S.devTypes = r; S.devTypePeople = people;
  const pname = id => people.find(p => p.id === id)?.name || "?", wname = id => r.wlans.find(w => w.id === id)?.ssid || "?";
  el.innerHTML = `<h2>Device profiles ${hint("Profile types add Wi-Fi networks and a VPN to the setup profile that devices install from their check-in page (iPhone, iPad, Mac). Attach a type to people; their devices get it. A device can get another type in Devices › New devices › Change….")}</h2>
    <p class="fine">Each approved device's setup profile carries its NextDNS profile and, with a type, Wi-Fi networks (they join by themselves) and a VPN. Wi-Fi passwords are read from UniFi when a device downloads its profile; VPN secrets are kept in HomeOwl's secret store and never shown again. Android and Windows get the NextDNS setup, and the home Wi-Fi's name and password on their check-in page.</p>
    ${r.wlan_error ? `<div class="msg warn">Wi-Fi networks: ${esc(r.wlan_error)}</div>` : ""}
    ${r.types.length ? table("devtypes", [
      { k: "name", label: "Type", fmt: t => `<b>${esc(t.name)}</b>${t.default ? ` <span class="tag">everyone else</span>` : ""}` },
      { k: "people", label: "People", fmt: t => arr(t.people).length ? esc(t.people.map(pname).join(", ")) + (t.default ? ` <span class="muted">+ everyone else</span>` : "") : t.default ? "everyone" : `<span class="muted">nobody yet</span>` },
      { k: "wifi", label: "Wi-Fi", fmt: t => `${arr(t.wifi).length ? esc(t.wifi.map(wname).join(", ")) : "–"}${arr(t.wifi).some(id => r.wlans.find(w => w.id === id)?.no_password) ? `<div class="fine warn-t">no password from UniFi: left out of profiles</div>` : ""}${t.no_other_wifi ? `<div class="fine muted">others: don't join by themselves</div>` : ""}` },
      { k: "vpn", label: "VPN", fmt: t => t.vpn ? `${esc(t.vpn.name || "Home VPN")} <span class="fine muted">${esc(VPN_TYPES.find(v => v[0] === t.vpn.type)?.[1] || t.vpn.type)}${t.vpn.on_demand ? " · on when away" : ""}</span>` : "–" },
      { k: "icon", label: "Home Screen", fmt: t => t.icon === "app" ? "HomeOwl" : t.icon === "none" ? `<span class="muted">no icon</span>` : "check-in page" },
      { k: "id", label: "", fmt: t => `<div class="btnrow"><button class="btn sm" data-dtedit="${esc(t.id)}">Edit</button><button class="btn sm" data-dtdel="${esc(t.id)}">Delete</button></div>` }], r.types)
      : `<p class="muted">No profile types yet. Devices get their NextDNS setup only.</p>`}
    <div class="btnrow" style="margin-top:10px"><button class="btn primary" data-dtedit="">+ Profile type</button></div>
    <h3 style="margin-top:18px">Setup Wi-Fi ${hint("A Wi-Fi network with no internet and its own password that you give out. People open HomeOwl's check-in page on it from the link or QR code below (or, if it has a guest portal, UniFi sends them there). New devices join it, check in and wait there for your approval, then install their profile, which moves them to the home Wi-Fi and stops them rejoining the setup Wi-Fi. Android and Windows get the home Wi-Fi's name and password on their page instead. Keep the home Wi-Fi password to yourself so the profile is the way in.")}</h3>
    <p class="fine muted">Devices on this network may check in (other guest networks can't). Leave empty if you don't have one.</p>
    <div class="dt-onboard"><label>Network name<input id="dt-onboard" maxlength="32" list="dt-onboard-list" value="${esc(r.onboard_ssid || "")}" placeholder="e.g. Home Setup" autocomplete="off"></label>
      <datalist id="dt-onboard-list">${r.wlans.filter(w => w.guest || w.security === "open").map(w => `<option value="${esc(w.ssid)}">`).join("")}</datalist>
      <button class="btn" id="dt-onboard-save">Save</button></div>
    ${r.onboard_ssid && !r.wlans.some(w => w.ssid.toLowerCase() === r.onboard_ssid.toLowerCase()) && r.wlans.length ? `<p class="fine warn-t">There's no Wi-Fi called “${esc(r.onboard_ssid)}” on the console yet.</p>` : ""}
    ${setupWiFiBlock(r)}
    <h3 style="margin-top:18px">NextDNS off on these Wi-Fi networks ${hint("A plane's or hotel's sign-in page only loads with ordinary DNS, and the profile's encrypted NextDNS can't reach NextDNS before you sign in. On these networks the profile's NextDNS stands down; everywhere else it stays on. Names must match exactly (Settings › Wi-Fi on the plane shows it). Changing the list shows Update ready on the devices.")}</h3>
    <p class="fine muted">For every device. One Wi-Fi name per line, exactly as the phone shows it.${r.skip_wifi_default ? " This is HomeOwl's starting list (British Airways). Add other airlines' networks exactly as the phone shows them." : ""}</p>
    <textarea id="dt-skip" rows="5" spellcheck="false" class="mono">${esc((r.skip_wifi || []).join("\n"))}</textarea>
    <div class="btnrow" style="margin-top:6px"><button class="btn" id="dt-skip-save">Save</button>${r.skip_wifi_default ? "" : `<button class="btn ghost" id="dt-skip-reset">Back to the starting list</button>`}</div>
    <h3 style="margin-top:18px">Name in NextDNS ${hint("Goes before the device's name in what its setup reports to NextDNS, so the devices HomeOwl set up stand out in NextDNS's logs and device list. HomeOwl still knows them under both names. Changing it shows Update ready on the devices: their next install reports the new name.")}</h3>
    <div class="dt-onboard"><label>Prefix<input id="dt-ndprefix" maxlength="12" value="${esc(r.nd_prefix || "")}" placeholder="e.g. HO" spellcheck="false" autocomplete="off"></label>
      <button class="btn" id="dt-ndprefix-save">Save</button></div>
    <p class="fine muted" id="dt-ndprefix-eg">${esc(ndPrefixExample(r.nd_prefix || ""))}</p>
    <div id="st-approval"></div>`;
  loadApproval();
}
// what NextDNS will call a device with this prefix (dnsskip.go withNDPrefix)
function ndPrefixExample(p) {
  const n = "Alex's iPhone";
  return `NextDNS shows: ${p ? (/[-_:.·]$/.test(p) ? p + n : `${p} ${n}`) : n}`;
}
document.addEventListener("input", e => { if (e.target.id === "dt-ndprefix" && $("#dt-ndprefix-eg")) $("#dt-ndprefix-eg").textContent = ndPrefixExample(e.target.value.trim()); });
// Setup Wi-Fi made by HomeOwl (setupwifi.go): a no-internet VLAN + a Wi-Fi with its own password, then the check-in link by IP
function setupWiFiBlock(r) {
  const exists = r.onboard_ssid && r.wlans.some(w => w.ssid.toLowerCase() === r.onboard_ssid.toLowerCase());
  const canMake = S.source === "live" && S.model?.hasLegacy && S.role === "admin" && !S.readOnly;
  const links = arr(r.setup_links), n = r.setup_net;
  const moves = r.types.some(t => t.default && arr(t.wifi).length);
  const noMove = r.onboard_ssid && !moves ? `<p class="fine warn-t">Devices from the setup Wi-Fi only move to the home Wi-Fi if their profile has it. Make a profile type with the home Wi-Fi ticked (and “Don't join our other Wi-Fi networks”) and tick <b>Everyone else too</b>.</p>` : "";
  if (!exists) return noMove + (canMake ? `<div class="btnrow" style="margin-top:6px"><button class="btn" id="dt-setup-make">Create a setup Wi-Fi on the console…</button></div>` : "");
  let net = "";
  if (r.portal) net = `<ul class="dt-netstate fine"><li>✓ Guest portal: devices on it open the check-in page by themselves and reach nothing but HomeOwl (${esc(r.portal.ip)}). Guest Wi-Fi visitors sign in on HomeOwl's page (${r.portal.mode === "password" ? "with the portal's password" : "a Connect button"}). Undo it from Changes.</li></ul>`;
  else if (canMake) net = `<div class="ap-block" style="margin-top:8px"><div><b>Guest portal: not set up</b><div class="fine muted">With it, phones that join the setup Wi-Fi open the check-in page by themselves and reach nothing else (no internet, no home network). Your guest Wi-Fi keeps working through HomeOwl's sign-in page.</div></div>
    <button class="btn primary" id="dt-portal">Set up…</button></div>`;
  if (n && !r.portal) {
    const on = n.homeowl_ip, iso = n.isolated;
    net += `<ul class="dt-netstate fine">
      <li>${n.internet ? (on ? `⚠ Internet on: anyone with its password (children who set up with it, too) can switch to it and get internet off the home Wi-Fi. HomeOwl has its own address on it now, so it works without internet.${canMake ? ` <button class="btn sm" data-setupnet="nointernet">Turn internet off…</button>` : ""}` : "✓ Internet on: phones stay on this Wi-Fi. Anyone with its password gets internet on it, so turn it off once HomeOwl has its own address on it (below).") : `✗ No internet: phones soon send the check-in page over mobile data and lose it, unless Mobile Data is off.${canMake ? ` <button class="btn sm" data-setupnet="internet">Turn internet on…</button>` : ""}`}</li>
      <li>${on ? `✓ HomeOwl has its own address on it (${esc(on)}), so the page loads directly.` : `✗ HomeOwl has no address on ${esc(n.subnet || "this network")} (VLAN ${esc(String(n.vlan ?? "?"))}), so the page goes through the router. <button class="btn sm ghost" id="dt-setup-vlanhow">How to add one</button>`}</li>
      <li>${iso === undefined ? `– This console has no “isolate network” switch (zone-based firewall): in UniFi › Settings › Security, put ${esc(n.name)} in its own zone with no access to your home network.` : iso ? "✓ Isolated: devices on it can't reach your home network." : on ? `✗ Not isolated: devices on it can reach your home network.${canMake ? ` <button class="btn sm" data-setupnet="isolate">Isolate it…</button>` : ""}` : "✗ Not isolated: devices on it can reach your home network. Isolate it once HomeOwl has its own address on it (isolating cuts off HomeOwl's home address)."}</li></ul>`;
  }
  const join = r.setup_join ? `<div class="dt-qrtile"><div class="guest-qr">${QR.svg(QR.wifi(r.setup_join), { size: 130, label: `Join ${r.setup_join.ssid}` })}</div>
      <div class="fine"><b>1. Join ${esc(r.setup_join.ssid)}</b><br>Scan with the phone's camera.${r.setup_join.password ? `<br>Password: <span class="mono">${esc(r.setup_join.password)}</span>` : ""}</div></div>` : "";
  const open = links.length ? `<div class="dt-qrtile"><div class="guest-qr">${QR.svg(links[0], { size: 130, label: "Link to the check-in page on the setup Wi-Fi" })}</div>
      <div class="fine"><b>2. ${r.portal ? "The check-in page opens by itself" : "Open the check-in page"}</b><br>${r.portal ? "If it doesn't, scan this or open:" : "Scan this or open:"}${links.map(l => `<div class="mono">${esc(l)}</div>`).join("")}${!r.portal && n && !n.internet && !n.homeowl_ip ? "Phones need Mobile Data off while they're on it; the page tells them." : ""}</div></div>` : "";
  S.setupPrint = { join: r.setup_join, link: links[0], portal: !!r.portal };
  return noMove + net + (join || open ? `<div class="dt-setuplink">${join}${open}</div>
    <button class="btn sm" id="dt-setup-print">Print these instructions</button>` : "");
}
// printSetupSheet: one page to leave next to whoever sets up a new device.
function printSetupSheet() {
  const p = S.setupPrint; if (!p) return;
  const j = p.join, step = (n, t, body, qr) => `<div class="ps-step">${qr ? `<div class="ps-qr">${qr}</div>` : ""}<div><div class="ps-n">${n}. ${t}</div>${body}</div></div>`;
  printSheet(`<div class="ps">
    <div class="ps-title">Setting up a new phone, tablet or computer</div>
    <div class="ps-sub">Do this once per device. It takes about five minutes.</div>
    ${j ? step(1, `Join the Wi-Fi “${esc(j.ssid)}”`, `<p>Point the camera at the code and tap the Wi-Fi notice. Or open Settings › Wi-Fi, pick <b>${esc(j.ssid)}</b>${j.password ? " and type the password" : ""}.</p>${j.password ? `<div class="ps-pass">Password: <span class="mono">${esc(j.password)}</span></div>` : ""}`, QR.svg(QR.wifi(j), { size: 200, label: `Join ${j.ssid}` })) : ""}
    ${p.link ? step(j ? 2 : 1, p.portal ? "The check-in page opens by itself" : "Open the check-in page", `<p>${p.portal ? "A HomeOwl page pops up a few seconds after joining. If it doesn't, scan this code or type the address in the browser:" : "Scan this code, or type the address in the browser:"}</p><div class="ps-link mono">${esc(p.link)}</div>`, QR.svg(p.link, { size: 200, label: "Link to the check-in page" })) : ""}
    ${step(j && p.link ? 3 : 2, "Follow the page", `<p>Say what the device is and whose it is, then keep the page open while a grown-up approves it in HomeOwl. Then install the setup profile it offers (on iPhone and iPad: Settings › Profile Downloaded › Install).</p>`)}
    ${step(j && p.link ? 4 : 3, "Done", `<p>The device moves to the home Wi-Fi by itself. You can forget “${esc(j?.ssid || "the setup Wi-Fi")}” afterwards.</p>`)}
    <div class="pc-foot"><img src="icon.png" alt="" width="18" height="18"> Made with HomeOwl · github.com/h4ux/HomeOwl</div></div>`);
}
async function portalPreview() {
  let pv; try { pv = await api("/api/setupwifi/portal-preview", {}); } catch (err) { return toast(err.message, "err"); }
  openModal({ title: "Guest portal for the setup Wi-Fi", wide: true, body: `<p class="muted">UniFi has one guest portal for the whole site. HomeOwl becomes it, at ${esc(pv.ip)}. Review exactly what will be sent to the console. ${S.write ? "" : `<b>Change mode is locked</b> — applying will unlock it for this session.`}</p>` +
    arr(pv.steps).map(p => `<div class="pv"><div class="pv-h"><b>${esc(p.description)}</b> <span class="muted">${esc(p.target || "")}</span></div>
      <table class="pv-t"><tr><th>Setting</th><th>Now</th><th>New</th></tr>${Object.keys(p.after || {}).map(k => `<tr><td class="mono">${esc(k)}</td><td>${esc(fmtVal(p.before?.[k]))}</td><td><b>${esc(fmtVal(p.after[k]))}</b></td></tr>`).join("")}</table>
      <div class="fine mono">${esc(p.method)} ${esc(p.path)}</div><div class="fine">↩ Can be undone from the Changes page.</div></div>`).join("") +
    arr(pv.notes).map(t => `<p class="fine">${esc(t)}</p>`).join(""),
    buttons: [{ label: "Cancel" }, { label: S.write ? "Apply" : "Unlock & apply", cls: "primary", onClick: async () => {
      try { if (!S.write) await setWrite(true); S.devTypes = await api("/api/setupwifi/portal-apply", {}); closeModal(); toast("The guest portal is HomeOwl now", "ok"); renderDevTypes(); }
      catch (err) { toast(err.message, "err"); } } }] });
}
document.addEventListener("click", e => {
  if (e.target.id === "dt-portal") { portalPreview(); return; }
  if (e.target.id === "dt-setup-print") { printSetupSheet(); return; }
  const b = e.target.closest("[data-setupnet]");
  if (b) {
    const n = S.devTypes?.setup_net; if (!n) return;
    const op = b.dataset.setupnet, iso = op === "isolate", off = op === "nointernet";
    runActions([{ kind: "rest", resource: "networkconf", id: n.id, fields: iso ? { network_isolation_enabled: true } : { internet_access_enabled: !off },
      description: iso ? `Setup Wi-Fi: isolate ${n.name} from the home network` : `Setup Wi-Fi: internet ${off ? "off" : "on"} for ${n.name}` }], iso ? "Isolate the setup network" : `Internet ${off ? "off" : "on"} on the setup network`);
    return;
  }
  if (e.target.id === "dt-setup-vlanhow") {
    const n = S.devTypes?.setup_net || {};
    openModal({ title: "Give HomeOwl an address on the setup network", wide: true, body: `
      <p>The computer running HomeOwl joins VLAN ${esc(String(n.vlan ?? "30"))} as well as your home network, over the same cable. It gets an address on ${esc(n.subnet || "the setup network")} from UniFi, and the check-in page loads directly from there, even with the setup network isolated or without internet.</p>
      <ol>
        <li><b>UniFi:</b> the switch port of HomeOwl's computer must carry VLAN ${esc(String(n.vlan ?? "30"))} tagged. Ports allow all VLANs unless you changed it (UniFi › Devices › the switch › Ports › that port › Tagged VLAN Management: Allow All). A computer on Wi-Fi can't do this — it needs a cable.</li>
        <li><b>Mac:</b> System Settings › Network › ⋯ › Manage Virtual Interfaces › + › New VLAN: tag ${esc(String(n.vlan ?? "30"))} on the Ethernet port, Configure IPv4 Using DHCP. Then in the VLAN's Details › TCP/IP, make sure it isn't the default route (drag Ethernet above it in Set Service Order).</li>
        <li><b>Linux:</b> <span class="mono">sudo nmcli con add type vlan con-name homeowl-setup dev eth0 id ${esc(String(n.vlan ?? "30"))} ipv4.method auto ipv4.never-default yes ipv6.method disabled</span> (use your Ethernet interface for eth0).</li>
        <li>Come back here: this list shows ✓ with the new address, the link and QR code switch to it, and you can isolate the network.</li>
      </ol>
      <p class="fine">HomeOwl answers on every address of the computer, so nothing changes in HomeOwl. Devices on the setup network can then reach HomeOwl's sign-in page, as on the home network.</p>` });
  }
});
async function makeSetupWiFi() {
  let sg; try { sg = await api("/api/setupwifi/suggest", {}); } catch (err) { return toast(err.message, "err"); }
  const cur = S.devTypes?.onboard_ssid || "";
  openModal({ title: "Create a setup Wi-Fi", body: `
    <p class="fine">HomeOwl adds a network on its own VLAN and a Wi-Fi on it with its own password, modelled on your home network and Wi-Fi. Nothing is sent until you've seen the preview.</p>
    <label>Wi-Fi name<input id="sw-ssid" maxlength="32" value="${esc(cur || "Home Setup")}" autocomplete="off"></label>
    <label>Password (what you give guests to set up)<input id="sw-pw" maxlength="63" value="${esc(sg.password)}" autocomplete="off" spellcheck="false" class="mono"></label>
    <label>VLAN<input id="sw-vlan" type="number" min="2" max="4009" value="${esc(String(sg.vlan))}"></label>
    <label>Network name in UniFi<input id="sw-net" maxlength="32" value="${esc(sg.network)}" autocomplete="off"></label>
    <label class="check"><input type="checkbox" id="sw-inet" checked> <span>Internet on the setup Wi-Fi <span class="fine muted">Phones keep using a Wi-Fi with internet. Without it they send the check-in page over mobile data and lose it, unless Mobile Data is off. Anyone with this password gets internet (not your home network, once it's isolated).</span></span></label>`,
    buttons: [{ label: "Cancel" }, { label: "Preview", cls: "primary", onClick: previewSetupWiFi }] });
}
async function previewSetupWiFi() {
  const body = { ssid: $("#sw-ssid").value.trim(), password: $("#sw-pw").value, vlan: +$("#sw-vlan").value, network: $("#sw-net").value.trim(), internet: $("#sw-inet").checked };
  let pv; try { pv = await api("/api/setupwifi/preview", body); } catch (err) { return toast(err.message, "err"); }
  openModal({ title: "Create a setup Wi-Fi", wide: true, body: `<p class="muted">Review exactly what will be sent to the console. ${S.write ? "" : `<b>Change mode is locked</b> — applying will unlock it for this session.`}</p>` +
    arr(pv.steps).map(p => `<div class="pv"><div class="pv-h"><b>${esc(p.description)}</b> <span class="muted">${esc(p.target || "")}</span></div>
      <table class="pv-t"><tr><th>Setting</th><th>New</th></tr>${Object.keys(p.after || {}).map(k => `<tr><td class="mono">${esc(k)}</td><td><b>${esc(fmtVal(p.after[k]))}</b></td></tr>`).join("")}</table>
      <div class="fine mono">${esc(p.method)} ${esc(p.path)}</div><div class="fine">↩ Can be undone from the Changes page (it deletes what was made).</div></div>`).join("") +
    `<p class="fine">Until it's isolated, devices on the setup network can reach your home network; Settings shows how to isolate it afterwards. It becomes HomeOwl's setup Wi-Fi.</p>`,
    buttons: [{ label: "Cancel" }, { label: S.write ? "Create" : "Unlock & create", cls: "primary", onClick: async () => {
      try { if (!S.write) await setWrite(true); S.devTypes = await api("/api/setupwifi/apply", body); closeModal(); toast(`Created ${body.ssid}`, "ok"); renderDevTypes(); }
      catch (err) { toast(err.message, "err"); } } }] });
}
document.addEventListener("click", e => { if (e.target.id === "dt-setup-make") makeSetupWiFi(); });
// Approve new devices (approval.go): first-time devices on the home Wi-Fi are blocked until approved
async function loadApproval() {
  try { S.approval = await api("/api/approval"); } catch (e) { S.approval = { error: e.message }; }
  renderApproval();
}
const APV_GRACE = [[0, "no time — block at once"], [15, "15 minutes"], [60, "1 hour"], [240, "4 hours"], [1440, "1 day"], [4320, "3 days"], [10080, "7 days"]];
document.addEventListener("change", e => {
  if (e.target.id !== "apv-grace") return;
  approvalDo("/api/approval/set", { grace_minutes: +e.target.value }, +e.target.value ? `New devices stay online for ${e.target.selectedOptions[0].textContent}` : "New devices are blocked at once");
});
document.addEventListener("click", e => {
  const b = e.target.closest("[data-apvblock]"); if (!b) return;
  approvalDo("/api/approval/block", { mac: b.dataset.apvblock }, "Blocked");
});
function renderApproval() {
  const el = $("#st-approval"), r = S.approval; if (!el || !r) return;
  if (r.error) { el.innerHTML = `<div class="msg warn">${esc(r.error)}</div>`; return; }
  const who = d => esc(d.name || d.hostname || d.mac), meta = d => [d.vendor, d.ssid, ago(d.at * 1000)].filter(Boolean).map(esc).join(" · ");
  const people = S.devTypePeople || [];
  const row = (d, kept) => `<div class="ap-block"><div><b>${who(d)}</b> <span class="fine muted mono">${esc(d.mac)}</span><div class="fine muted">${meta(d)}</div></div>
    <div class="btnrow">${people.length ? `<select data-apvwho="${esc(d.mac)}" aria-label="Whose device"><option value="">Whose? (optional)</option>${people.map(p => `<option value="${esc(p.id)}">${esc(p.name)}</option>`).join("")}</select>` : ""}
      <button class="btn sm primary" data-apv="${esc(d.mac)}">Approve</button>${kept ? "" : `<button class="btn sm" data-apvkeep="${esc(d.mac)}">Keep blocked</button>`}</div></div>`;
  el.innerHTML = `<h3 style="margin-top:18px">Approve new devices ${hint("A device that joins the home Wi-Fi for the first time is blocked in UniFi until you approve it here or from its alert. Checked-in devices (from the setup Wi-Fi with their profile) don't need it. Devices seen in the last day when you turn this on count as approved; older ones wait for you when they come back. Guest networks, the setup Wi-Fi and wired devices aren't covered. Each block is in the change log and can be undone.")}</h3>
    <label class="check"><input type="checkbox" id="apv-on" ${r.on ? "checked" : ""}> <span>Block first-time devices on the home Wi-Fi until I approve them</span></label>
    ${r.on ? `<div class="ap-grace"><label>New devices stay online for<select id="apv-grace">${APV_GRACE.map(([m, l]) => `<option value="${m}" ${m === (r.grace_minutes || 0) ? "selected" : ""}>${l}</option>`).join("")}${APV_GRACE.some(([m]) => m === (r.grace_minutes || 0)) ? "" : `<option value="${r.grace_minutes}" selected>${r.grace_minutes} minutes</option>`}</select></label>
      <span class="fine muted">${r.grace_minutes ? "…so you can approve them in time. Not approved by then: blocked." : "Blocked straight away until you approve them."}</span></div>
      <p class="fine">${r.push ? "✓ New devices are pushed to your phone (ntfy) with <b>Approve</b> and <b>Block</b> buttons. Everyone subscribed to the topic can decide." : `Get new devices on your phone with <b>Approve</b> and <b>Block</b> buttons: add an ntfy topic in <a href="#" data-goto="settings">Settings › Alerts</a> (the ntfy app; both parents can subscribe).`}</p>` : ""}
    ${r.on && arr(r.grace).length ? `<h4 style="margin:10px 0 4px">Online, waiting for a decision</h4><div class="ap-blocks">${r.grace.map(d => `<div class="ap-block"><div><b>${who(d)}</b> <span class="fine muted mono">${esc(d.mac)}</span><div class="fine muted">${meta(d)} · blocked ${d.ends * 1000 > Date.now() ? "in " + fmtDur((d.ends * 1000 - Date.now()) / 1000) : "at its next check"}</div></div>
      <div class="btnrow">${people.length ? `<select data-apvwho="${esc(d.mac)}" aria-label="Whose device"><option value="">Whose? (optional)</option>${people.map(p => `<option value="${esc(p.id)}">${esc(p.name)}</option>`).join("")}</select>` : ""}
      <button class="btn sm primary" data-apv="${esc(d.mac)}">Approve</button><button class="btn sm" data-apvblock="${esc(d.mac)}">Block now</button></div></div>`).join("")}</div>` : ""}
    ${r.on ? `<p class="fine muted">${r.approved} devices approved. <button class="btn sm ghost" id="apv-trim" title="Devices approved when this was turned on, but not seen in the last ${r.recent_hours || 24} hours, wait for approval when they come back. Devices you approved and checked-in devices stay.">Re-check devices not seen lately</button></p>` : ""}
    ${r.waiting.length ? `<h4 style="margin:10px 0 4px">Blocked, waiting for a decision</h4><div class="ap-blocks">${r.waiting.map(d => row(d, false)).join("")}</div>` : r.on && !arr(r.grace).length ? `<p class="fine muted">Nothing waiting.</p>` : ""}
    ${r.kept.length ? `<details class="wi-more"><summary>Kept blocked (${r.kept.length})</summary><div class="ap-blocks">${r.kept.map(d => row(d, true)).join("")}</div></details>` : ""}
    ${r.on || r.waiting.length || r.kept.length ? `<details class="wi-more"><summary>If HomeOwl is down, or blocked a device by mistake</summary><div class="fine">
      <p>Blocks are set in UniFi, so you can always lift one there, without HomeOwl:</p>
      <ol><li>Open the UniFi app (or unifi.ui.com) and go to <b>Client Devices</b>.</li>
      <li>Show <b>Blocked</b> devices (the filter at the top), and open the device.</li>
      <li>Tap <b>Unblock</b>. It can join again straight away.</li></ol>
      <p>When HomeOwl is running again, it sees the device is connected and counts it as approved, so it won't block it again. While HomeOwl is stopped it blocks nothing new. To stop blocking altogether, untick the box above (devices already blocked stay blocked until you approve or unblock them).</p>
      <button class="btn sm" id="apv-print">Print these steps</button></div></details>` : ""}`;
}
function printApprovalHelp() {
  printSheet(`<div class="ps"><div class="ps-title">A device can't get on the Wi-Fi?</div>
    <div class="ps-sub">HomeOwl blocks devices it hasn't seen before until they're approved. If HomeOwl is down, or blocked one by mistake, unblock it in UniFi.</div>
    <div class="ps-step"><div><div class="ps-n">1. Open the UniFi app</div><p>Or unifi.ui.com in a browser, signed in as the network's owner. Go to <b>Client Devices</b>.</p></div></div>
    <div class="ps-step"><div><div class="ps-n">2. Find the device</div><p>Show <b>Blocked</b> devices with the filter at the top, and open the one that can't connect.</p></div></div>
    <div class="ps-step"><div><div class="ps-n">3. Tap Unblock</div><p>It can join the Wi-Fi again straight away. Once HomeOwl is back, it counts the device as approved and leaves it alone.</p></div></div>
    <div class="ps-step"><div><div class="ps-n">Still stuck?</div><p>Forget the Wi-Fi on the device and join again. A phone with “Private Wi-Fi address” on may show up as a new device: approve it in HomeOwl, or unblock it in UniFi.</p></div></div>
    <div class="pc-foot"><img src="icon.png" alt="" width="18" height="18"> Made with HomeOwl · github.com/h4ux/HomeOwl</div></div>`);
}
document.addEventListener("click", e => { if (e.target.id === "apv-print") printApprovalHelp(); });
async function approvalDo(path, body, msg) {
  try { S.approval = await api(path, body); toast(msg, "ok"); renderApproval(); if (S.view === "clients") render(); } catch (err) { toast(err.message, "err"); loadApproval(); }
}
document.addEventListener("click", e => {
  if (e.target.id !== "apv-trim") return;
  api("/api/approval/trim", {}).then(r => { S.approval = r; toast(r.trimmed ? `${plural(r.trimmed, "device")} not seen lately will wait for approval` : "Every approved device was seen lately", "ok"); renderApproval(); }).catch(err => toast(err.message, "err"));
});
document.addEventListener("change", e => {
  if (e.target.id !== "apv-on") return;
  const on = e.target.checked;
  if (!on) return approvalDo("/api/approval/set", { on }, "New devices aren't blocked any more");
  e.target.checked = false;
  openModal({ title: "Block new devices until you approve them?", body: `<p>From now on, a device that joins the home Wi-Fi for the first time is <b>blocked in UniFi</b> and you get an alert to approve it. HomeOwl does this on its own, through the logged UniFi block (with undo).</p>
    <p>Devices seen in the last day count as approved, so nothing in use is cut off. Devices not seen for longer wait for your approval when they come back. Checked-in devices from the setup Wi-Fi aren't blocked. Guests use the guest Wi-Fi.</p>`,
    buttons: [{ label: "Cancel" }, { label: "Turn on", cls: "primary", onClick: async () => { closeModal(); await approvalDo("/api/approval/set", { on: true }, "New devices now wait for your approval"); } }] });
});
document.addEventListener("click", e => {
  const ok = e.target.closest("[data-apv]");
  if (ok) { const mac = ok.dataset.apv; ok.disabled = true; approvalDo("/api/approval/approve", { mac, person_id: $(`[data-apvwho="${mac}"]`)?.value || "" }, "Approved — it can connect now"); return; }
  const keep = e.target.closest("[data-apvkeep]");
  if (keep) approvalDo("/api/approval/keep", { mac: keep.dataset.apvkeep }, "Kept blocked");
});

function editDevType(id) {
  const r = S.devTypes, t = r.types.find(x => x.id === id) || { name: "", wifi: [], people: [], vpn: null, icon: "checkin" }, v = t.vpn || {};
  const keep = set => set ? "Saved — leave empty to keep" : "";
  openModal({ title: id ? `Edit ${t.name}` : "New profile type", wide: true, body: `
    <label>Name<input id="dt-name" maxlength="40" value="${esc(t.name)}" placeholder="e.g. Kids, Adults"></label>
    <h3>People</h3><p class="fine muted">Their devices get this type. A person has one type; ticking them here moves them.</p>
    <div class="dt-checks">${S.devTypePeople.length ? S.devTypePeople.map(p => `<label class="check"><input type="checkbox" data-dtp="${esc(p.id)}" ${arr(t.people).includes(p.id) ? "checked" : ""}> <span>${esc(p.name)}</span></label>`).join("") : `<span class="fine muted">Add people in Family › People & groups.</span>`}</div>
    <label class="check" style="margin-top:8px"><input type="checkbox" id="dt-default" ${t.default ? "checked" : ""}> <span>Everyone else too <span class="fine muted">Devices whose person has no type of their own (or no person yet, like devices from the setup Wi-Fi) get this type. One type can be the default.</span></span></label>
    <h3>Wi-Fi networks</h3><p class="fine muted">The device joins them by itself. The password goes into the device's profile.</p>
    <div class="dt-checks">${r.wlans.length ? r.wlans.map(w => `<label class="check"><input type="checkbox" data-dtw="${esc(w.id)}" ${arr(t.wifi).includes(w.id) ? "checked" : ""} ${w.usable ? "" : "disabled"}> <span>${esc(w.ssid)}${w.guest ? ` <span class="fine muted">guest</span>` : ""}${w.usable ? "" : ` <span class="fine muted">enterprise — not possible</span>`}${w.usable && w.no_password ? ` <span class="fine warn-t">UniFi doesn't give HomeOwl its password, so it's left out of profiles — give HomeOwl's UniFi account a role that can see Wi-Fi passwords</span>` : ""}</span></label>`).join("") : `<span class="fine muted">No Wi-Fi networks from UniFi.</span>`}</div>
    <label class="check"><input type="checkbox" id="dt-noother" ${t.no_other_wifi ? "checked" : ""}> <span>Don't join our other Wi-Fi networks by themselves <span class="fine muted">so the device stays on the ones ticked above: the others are listed as “don't join automatically”, which also turns auto-join off where the device had saved them. Their passwords stay out of the profile, so installing asks for each one: skip it (tap Next or Cancel). The setup Wi-Fi is listed with its password, which its users already know.</span></span></label>
    <label class="check"><input type="checkbox" id="dt-fixed" ${t.private_mac ? "" : "checked"}> <span>Use the device's real Wi-Fi address on these networks <span class="fine muted">Recommended: phones stop changing their address there, so HomeOwl keeps knowing whose device it is. iPhone and iPad (iOS 14+) show a privacy note for that network. Android and Windows: turn off the private/random address for the network by hand.</span></span></label>
    <h3>NextDNS on other Wi-Fi</h3>
    <div class="dt-checks dt-radios"><label class="check"><input type="radio" name="dt-other" value="on" ${t.other_wifi !== "off" ? "checked" : ""}> <span>Everywhere <span class="fine muted">except the networks in Settings › Device profiles (planes…) and the ones below</span></span></label>
      <label class="check"><input type="radio" name="dt-other" value="off" ${t.other_wifi === "off" ? "checked" : ""}> <span>Only at home and on mobile data <span class="fine muted">off on any other Wi-Fi: planes, hotels and friends' Wi-Fi just work. Not for kids.</span></span></label></div>
    <label>More networks where NextDNS is off, for this type <span class="fine muted">(one per line)</span><textarea id="dt-skipt" rows="2" spellcheck="false" class="mono" placeholder="e.g. Example Hotel">${esc(arr(t.skip_wifi).join("\n"))}</textarea></label>
    <h3>Home Screen icon</h3><p class="fine muted">iPhone and iPad get a HomeOwl icon with the profile. Macs don't take one.</p>
    <div class="dt-checks dt-radios">${DT_ICONS.map(([k, l, d]) => `<label class="check"><input type="radio" name="dt-icon" value="${k}" ${(t.icon || "checkin") === k ? "checked" : ""}> <span>${esc(l)}${d ? ` <span class="fine muted">${esc(d)}</span>` : ""}</span></label>`).join("")}</div>
    <h3>VPN</h3>
    <label>Kind<select id="dt-vpn">${VPN_TYPES.map(([k, l]) => `<option value="${k}" ${(v.type || "") === k ? "selected" : ""}>${l}</option>`).join("")}</select></label>
    <div class="dt-vpn">
      <label data-dtv="ikev2 l2tp wireguard">Name on the device<input id="dt-vname" maxlength="40" value="${esc(v.name || "")}" placeholder="Home VPN"></label>
      <label data-dtv="ikev2 l2tp">Server (host name or IP address)<input id="dt-server" value="${esc(v.server || "")}" placeholder="vpn.example.com" autocomplete="off"></label>
      <label data-dtv="ikev2">Remote ID<input id="dt-rid" value="${esc(v.remote_id || "")}" placeholder="Usually the server's name" autocomplete="off"></label>
      <label data-dtv="ikev2">Local ID<input id="dt-lid" value="${esc(v.local_id || "")}" placeholder="Optional" autocomplete="off"></label>
      <label data-dtv="ikev2">Sign-in<select id="dt-auth"><option value="secret" ${v.auth !== "eap" ? "selected" : ""}>Shared secret</option><option value="eap" ${v.auth === "eap" ? "selected" : ""}>User name and password (EAP)</option></select></label>
      <label data-dtv="ikev2-secret l2tp">Shared secret ${t.secret_set ? `<span class="tag good">saved</span>` : ""}<input id="dt-secret" type="password" autocomplete="new-password" placeholder="${keep(t.secret_set)}"></label>
      <label data-dtv="ikev2-eap l2tp">User name<input id="dt-user" value="${esc(v.user || "")}" autocomplete="off"></label>
      <label data-dtv="ikev2-eap l2tp">Password ${t.password_set ? `<span class="tag good">saved</span>` : ""}<input id="dt-pass" type="password" autocomplete="new-password" placeholder="${keep(t.password_set)}"></label>
      <label class="check" data-dtv="ikev2 wireguard"><input type="checkbox" id="dt-od" ${v.on_demand ? "checked" : ""}> <span>Connect by itself when not on the home Wi-Fi</span></label>
      <p class="fine muted" data-dtv="l2tp">iPhone and Mac connect L2TP by hand (Settings › VPN). UniFi gateways can run an L2TP server.</p>
      <p class="fine muted" data-dtv="wireguard">Each device needs its own WireGuard config: paste it in Devices › New devices › Change… for that device. Devices need the WireGuard app.</p>
      <p class="fine muted" data-dtv="ikev2 l2tp">The same user name and password go to every device with this type. Secrets are stored in HomeOwl's secret store and go only into device profiles.</p>
    </div>`,
    buttons: [{ label: "Cancel" }, { label: "Save", cls: "primary", onClick: async () => {
      const kind = $("#dt-vpn").value;
      const body = { id, name: $("#dt-name").value.trim(), people: $$("[data-dtp]").filter(c => c.checked).map(c => c.dataset.dtp), wifi: $$("[data-dtw]").filter(c => c.checked).map(c => c.dataset.dtw),
        icon: $("input[name=dt-icon]:checked")?.value || "checkin", private_mac: !$("#dt-fixed").checked, no_other_wifi: $("#dt-noother").checked, default: $("#dt-default").checked,
        other_wifi: $("input[name=dt-other]:checked")?.value || "on", skip_wifi: $("#dt-skipt").value.split("\n").map(s => s.trim()).filter(Boolean),
        vpn: kind ? { type: kind, name: $("#dt-vname").value.trim(), server: $("#dt-server").value.trim(), remote_id: $("#dt-rid").value.trim(), local_id: $("#dt-lid").value.trim(), auth: $("#dt-auth").value, user: $("#dt-user").value.trim(), on_demand: $("#dt-od").checked } : null,
        secret: $("#dt-secret").value, password: $("#dt-pass").value };
      try { S.devTypes = await api("/api/devtypes/save", body); closeModal(); toast("Saved — devices with this type show Update ready", "ok"); renderDevTypes(); S.ciTypes = null; }
      catch (err) { toast(err.message, "err"); }
    } }] });
  dtVpnFields();
}
function dtVpnFields() {
  const k = $("#dt-vpn")?.value || "", auth = $("#dt-auth")?.value || "secret";
  $$("[data-dtv]").forEach(el => { const on = el.dataset.dtv.split(" "); el.hidden = !(on.includes(k) || on.includes(`${k}-${auth}`)); });
}
document.addEventListener("change", e => { if (e.target.id === "dt-vpn" || e.target.id === "dt-auth") dtVpnFields(); });
document.addEventListener("click", e => {
  const ed = e.target.closest("[data-dtedit]"); if (ed) { editDevType(ed.dataset.dtedit); return; }
  if (e.target.id === "dt-skip-save" || e.target.id === "dt-skip-reset") {
    const reset = e.target.id === "dt-skip-reset", ssids = $("#dt-skip").value.split("\n").map(s => s.trim()).filter(Boolean);
    api("/api/skipwifi/save", reset ? { reset: true } : { ssids }).then(r => { S.devTypes = r; toast(reset ? "Starting list back" : "Saved — devices show Update ready", "ok"); renderDevTypes(); }).catch(err => toast(err.message, "err"));
    return;
  }
  if (e.target.id === "dt-ndprefix-save") {
    const prefix = $("#dt-ndprefix").value.trim();
    api("/api/ndprefix/save", { prefix }).then(r => { S.devTypes = r; toast(prefix ? `NextDNS names start with “${prefix}” — devices show Update ready` : "No prefix — devices show Update ready", "ok"); renderDevTypes(); }).catch(err => toast(err.message, "err"));
    return;
  }
  if (e.target.id === "dt-onboard-save") {
    const ssid = $("#dt-onboard").value.trim();
    api("/api/onboard/save", { ssid }).then(r => { S.devTypes = r; toast(ssid ? `Setup Wi-Fi: ${ssid}` : "No setup Wi-Fi", "ok"); renderDevTypes(); }).catch(err => toast(err.message, "err"));
    return;
  }
  const del = e.target.closest("[data-dtdel]");
  if (del) {
    const t = S.devTypes.types.find(x => x.id === del.dataset.dtdel);
    openModal({ title: `Delete ${t?.name || "this type"}?`, body: `<p>Its devices lose its Wi-Fi and VPN settings the next time they install their profile. Its VPN secrets are removed.</p>`,
      buttons: [{ label: "Cancel" }, { label: "Delete", cls: "danger", onClick: async () => { try { S.devTypes = await api("/api/devtypes/delete", { id: t.id }); closeModal(); renderDevTypes(); S.ciTypes = null; } catch (err) { toast(err.message, "err"); } } }] });
  }
});
document.addEventListener("click", e => { const b = e.target.closest("[data-cisetup]"); if (b) changeSetup(b.dataset.cisetup); });

async function approveCheckin(id) {
  const x = S.checkins.items.find(x => x.id === id); if (!x) return;
  const known = ciPerson(x);
  if (!S.ciND) S.ciND = await api("/api/checkins/ndprofiles").catch(() => ({ profiles: [], by_person: {} }));
  const nd = S.ciND, ndDefault = known ? nd.by_person[known.id] || "" : "";
  const ndPick = nd.profiles.length ? `<label>NextDNS profile for the device's setup
      <select id="ci-nd"><option value="">None — don't offer a setup</option>${nd.profiles.map(p => `<option value="${esc(p.id)}" ${p.id === ndDefault ? "selected" : ""}>${esc(p.name)} (${esc(p.id)})</option>`).join("")}</select></label>
    ${x.det_model || (x.user_model && x.user_model !== "-") ? `<p class="fine">Model — found on the device: <b>${x.det_model ? esc(ndModelName(x.det_model)) : "nothing"}</b> · they chose: <b>${x.user_model && x.user_model !== "-" ? esc(ndModelName(x.user_model)) : "not sure"}</b>${x.det_model && x.user_model && x.user_model !== "-" && x.det_model !== x.user_model ? ` <span class="tag warn">different</span>` : ""}</p>` : ""}
    ${ndModelPick("ci", "", x.det_model || "", x.user_model || "")}
    <p class="fine">After approval, the device's check-in page offers to set it up with encrypted DNS through this profile, under its name, so it's recognised on any network (a profile on iPhone, iPad and Mac; Private DNS on Android).</p>` : "";
  const opts = S.ciPeople.map(p => `<option value="${esc(p.id)}" ${p.id === known?.id ? "selected" : ""}>${esc(p.name)}</option>`).join("");
  openModal({ title: "Approve device check-in", body: `
    <label>UniFi name (alias)<input id="ci-alias" maxlength="40" value="${esc(x.name)}"></label>
    <label>Belongs to<select id="ci-person">${x.person_name && !known ? `<option value="" selected>${esc(x.person_name)} (new person)</option>` : ""}${opts}<option value="-">Nobody</option></select></label>
    <label class="check"><input type="checkbox" id="ci-main" ${x.main ? "checked" : ""}> <span>Their main device: Who's home follows it${x.main ? " <span class=\"fine muted\">(they said so)</span>" : ""}</span></label>
    ${ndPick}
    ${x.user_id ? "" : `<p class="bad-t">UniFi doesn't list this device as a known client, so its name can't be set — only the person is saved.</p>`}
    <p class="fine">Next you'll see exactly what is sent to UniFi. The name change is logged and can be undone from the Changes page.</p>`,
    buttons: [{ label: "Cancel" }, { label: x.user_id ? "Preview" : "Approve", cls: "primary", onClick: () => previewCheckin(x) }] });
}
async function previewCheckin(x) {
  const alias = $("#ci-alias").value.trim(), person = $("#ci-person").value, ndp = $("#ci-nd")?.value || "", main = !!$("#ci-main")?.checked, model = ndModelVal("ci");
  if (!alias) return toast("Give the device a name", "err");
  const act = x.user_id ? { kind: "rest", resource: "user", id: x.user_id, fields: { name: alias }, description: `Check-in: name ${x.mac} “${alias}”` } : null;
  let pv = null;
  if (act) { try { pv = (await api("/api/action", { action: act, dry_run: true })).preview; } catch (err) { return toast(err.message, "err"); } }
  const who = person === "-" ? "nobody" : person ? S.ciPeople.find(p => p.id === person)?.name : `${x.person_name} (new person)`;
  openModal({ title: "Approve device check-in", wide: true, body: `${pv ? `<div class="pv"><div class="pv-h"><b>UniFi client name</b> <span class="muted">${esc(pv.target)}</span></div>
      <table class="pv-t"><tr><th>Setting</th><th>Now</th><th>New</th></tr><tr><td class="mono">name</td><td>${esc(fmtVal(pv.before.name))}</td><td><b>${esc(alias)}</b></td></tr></table>
      <div class="fine mono">${esc(pv.method)} ${esc(pv.path)}</div><div class="fine">↩ Can be undone from the Changes page.</div></div>` : ""}
    <p>Device <span class="mono">${esc(x.mac)}</span> → <b>${esc(who)}</b> in People${main && person !== "-" ? ", and Who's home follows it" : ""}.</p>
    ${ndp ? `<p class="fine">Its check-in page will offer a setup through NextDNS profile <b>${esc(S.ciND.profiles.find(p => p.id === ndp)?.name || ndp)}</b>, named “${esc(alias)}”.</p>` : ""}
    <p class="fine">Approving also lets it on the network if device approval is holding it — there's nothing else to approve.</p>`,
    buttons: [{ label: "Cancel" }, { label: "Approve", cls: "primary", onClick: () => finishCheckin(x, act, person, alias, ndp, main, model) }] });
}
async function finishCheckin(x, act, person, alias, ndp, main, model) {
  try {
    const r = await api("/api/checkins/approve", { id: x.id, person_id: person, name: alias, nd_profile: ndp, nd_model: ndp ? model : "", main });
    closeModal(); toast(r.warning ? `Approved — ${r.warning}` : "Approved", r.warning ? "err" : "ok"); loadCheckins();
    S.apvAt = 0;
    setTimeout(() => collectLive(true, true).catch(() => { }), 2500);
  } catch (err) { toast(err.message, "err"); }
}

document.addEventListener("click", async e => {
  const ok = e.target.closest("[data-ciok]"); if (ok) { approveCheckin(ok.dataset.ciok); return; }
  const no = e.target.closest("[data-cino]");
  if (no) { try { S.checkins = await api("/api/checkins/reject", { id: no.dataset.cino }); toast(S.approval?.on ? "Rejected — and blocked, so it doesn't ask again" : "Rejected", "ok"); loadCheckins(); } catch (err) { toast(err.message, "err"); } return; }
  if (e.target.id === "ci-reload") loadCheckins();
});
{ const _render = render; render = function () { _render(); const a = $("#nav-checkins")?.closest("a"); if (a) a.hidden = S.role !== "admin" || S.source !== "live"; if (S.view === "checkins") loadCheckins(); }; }

// ---- Settings › Device profiles: Wi-Fi sign-in check (wifiident.go) — read-only
const WI_STATE = { ok: ["good", "✓"], warn: ["warn", "!"], bad: ["bad", "✕"], info: ["", "i"] };
function renderWifiIdent() {
  const el = $("#st-wifiident"); if (!el) return;
  const r = S.wifiIdent;
  el.innerHTML = `<h2>Wi-Fi sign-in check ${hint("Can your UniFi console tell whose device it is by the Wi-Fi login it uses, instead of its MAC address? This reads (never changes) the console's Wi-Fi, RADIUS and guest portal settings. No password, secret or login name leaves the console.")}</h2>
    <p class="fine">Checks for private passwords per person (PPSK), Enterprise Wi-Fi with UniFi's RADIUS server, and a captive portal for an onboarding network. Read-only.</p>
    ${r?.error ? `<div class="msg warn">${esc(r.error)}</div>` : ""}
    ${r && !r.error ? `<ul class="wi-list">${arr(r.verdict).map(v => `<li><span class="tag ${WI_STATE[v.state]?.[0] || ""}" aria-hidden="true">${WI_STATE[v.state]?.[1] || "i"}</span><div><b>${esc(v.topic)}</b><div class="fine">${esc(v.text)}</div></div></li>`).join("")}</ul>
      <details class="wi-more"><summary class="fine">What was read${r.version ? ` · UniFi Network ${esc(r.version)}` : ""}</summary>
        <div class="tbl-wrap"><table><thead><tr><th>Wi-Fi network</th><th>Security</th><th>Private passwords</th><th>Network</th></tr></thead><tbody>${arr(r.wlans).map(w => `<tr><td>${esc(w.name)}${w.guest ? ` <span class="tag">guest</span>` : ""}${w.enabled ? "" : ` <span class="tag">off</span>`}</td>
          <td>${esc(w.security === "wpaeap" ? "Enterprise (802.1X)" : w.security === "wpapsk" ? (w.wpa_mode || "WPA") + (w.wpa3 ? " + WPA3" : "") : w.security || "–")}</td>
          <td>${w.ppsk ? `on · ${w.ppsk_keys || 0}` : w.ppsk_keys ? `${w.ppsk_keys} (off)` : "–"}</td><td>${esc(w.network || "–")}</td></tr>`).join("")}</tbody></table></div>
        <p class="fine muted">${r.networks} networks (VLANs) · RADIUS users: ${r.radius.users} · RADIUS profiles: ${r.radius.profiles} · Wi-Fi devices now: ${r.clients.wifi}${Object.keys(r.clients.identity_fields || {}).length ? ` · login fields on devices: ${esc(Object.entries(r.clients.identity_fields).map(([k, n]) => `${k} (${n})`).join(", "))}` : ""}${r.clients.on_ppsk ? ` · on a private-password network: ${r.clients.on_ppsk}` : ""}${(r.clients.ppsk_only_fields || []).length ? ` · fields only those devices have: ${esc(r.clients.ppsk_only_fields.join(", "))}` : ""}</p>
        ${arr(r.errors).length ? `<p class="fine bad-t">Couldn't read: ${esc(r.errors.join("; "))}</p>` : ""}</details>` : ""}
    <div class="btnrow" style="margin-top:10px"><button class="btn${r ? "" : " primary"}" id="wi-run">${r ? "Check again" : "Check my console (read-only)"}</button></div>`;
}
document.addEventListener("click", async e => {
  if (e.target.id !== "wi-run") return;
  e.target.disabled = true; e.target.textContent = "Reading…";
  try { S.wifiIdent = await api("/api/wifi-ident"); } catch (err) { S.wifiIdent = { error: err.message }; }
  renderWifiIdent();
});
