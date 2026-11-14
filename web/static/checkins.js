/* HomeOwl — device check-ins (admins): devices that introduced themselves on the "Hi HomeOwl" page wait here.
   Approving sets the UniFi alias (allow-listed, previewed, undoable) and gives the device to a person. */
"use strict";

S.checkins = null;
V.checkins = () => `<div class="head"><div><h1>Device check-ins ${hint("Family members name their own devices; you approve.")}</h1></div>
  <button class="btn" id="ci-reload">Refresh</button></div><div id="ci-body"><div class="empty">Loading…</div></div>`;

const CI_KIND = { phone: "📱", tablet: "📱", laptop: "💻", computer: "🖥", tv: "📺", console: "🎮", speaker: "🔊", watch: "⌚", camera: "📷", printer: "🖨", other: "❔" };
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
    + (st.wg === false ? ` <span class="tag warn" title="Its profile type has a WireGuard VPN, and each device needs its own config: paste it with Change…">needs WireGuard config</span>` : "");
  const note = st.state === "not_seen" ? `downloaded ${ago(st.downloaded * 1000)}, no queries under its name yet` : st.state === "offered" ? "open the check-in page on the device" : seen;
  return `${tag}${upd}${note ? `<div class="fine muted">${esc(note)}</div>` : ""}`;
}
function setupCard(c) {
  const done = c.items.filter(x => x.status === "approved").sort((a, b) => b.time - a.time);
  if (!done.length) return "";
  return `<div class="card section"><h2>Devices set up ${hint("After approval, a device's check-in page offers encrypted DNS through the NextDNS profile you chose, under the device's name. NextDNS then recognises it at home, on mobile data and on other Wi-Fi, and HomeOwl sees whether it's in use.")}</h2>${table("ci-setup", [
    { k: "name", label: "Device", fmt: x => `${CI_KIND[x.kind] || ""} ${esc(x.name)}<div class="fine muted">${personLabel(x)}</div>` },
    { k: "nd_profile", label: "NextDNS profile", fmt: x => `${x.nd_profile ? esc(ndName(x.nd_profile)) : "–"}${c.setup?.[x.id]?.type ? `<div class="fine muted">type: ${esc(c.setup[x.id].type)}</div>` : ""}` },
    { k: "status", label: "Setup", sort: x => ({ stopped: -1, active: 0, quiet: 1, not_seen: 2, offered: 3, unknown: 4 })[c.setup?.[x.id]?.state] ?? 5, fmt: x => setupState(x, c.setup?.[x.id]) },
    { k: "id", label: "", fmt: x => `<button class="btn sm" data-cisetup="${esc(x.id)}">${x.nd_profile || c.setup?.[x.id]?.type ? "Change…" : "Set up…"}</button>` }], done)}</div>`;
}
// the profile type a device gets: picked for it ("-" = none), else its person's
const ciPersonType = x => arr(S.ciTypes?.types).find(t => arr(t.people).includes(x.person_id));
const ciTypeOf = (x, pick) => pick === "-" ? null : pick ? arr(S.ciTypes?.types).find(t => t.id === pick) : ciPersonType(x);
function changeSetup(id) {
  const x = S.checkins.items.find(x => x.id === id); if (!x) return;
  const nd = S.ciND || { profiles: [] }, types = arr(S.ciTypes?.types), pt = ciPersonType(x), st = S.checkins.setup?.[id] || {};
  if (!nd.profiles.length && !types.length) return toast("Nothing to choose yet: add a NextDNS API key in Settings › Analytics, or a profile type in Settings › Device profiles", "err");
  openModal({ title: `Setup for ${x.name}`, body: `
    ${nd.profiles.length ? `<label>NextDNS profile<select id="cis-nd"><option value="">None</option>${nd.profiles.map(p => `<option value="${esc(p.id)}" ${p.id === x.nd_profile ? "selected" : ""}>${esc(p.name)} (${esc(p.id)})</option>`).join("")}</select></label>` : ""}
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
  el.innerHTML = `<h2>Device profiles ${hint("Profile types add Wi-Fi networks and a VPN to the setup profile that devices install from their check-in page (iPhone, iPad, Mac). Attach a type to people; their devices get it. A device can get another type in Family › Check-ins › Change….")}</h2>
    <p class="fine">Each approved device's setup profile carries its NextDNS profile and, with a type, Wi-Fi networks (they join by themselves) and a VPN. Wi-Fi passwords are read from UniFi when a device downloads its profile; VPN secrets are kept in HomeOwl's secret store and never shown again. Android and Windows get the NextDNS setup only.</p>
    ${r.wlan_error ? `<div class="msg warn">Wi-Fi networks: ${esc(r.wlan_error)}</div>` : ""}
    ${r.types.length ? table("devtypes", [
      { k: "name", label: "Type", fmt: t => `<b>${esc(t.name)}</b>` },
      { k: "people", label: "People", fmt: t => arr(t.people).length ? esc(t.people.map(pname).join(", ")) : `<span class="muted">nobody yet</span>` },
      { k: "wifi", label: "Wi-Fi", fmt: t => arr(t.wifi).length ? esc(t.wifi.map(wname).join(", ")) : "–" },
      { k: "vpn", label: "VPN", fmt: t => t.vpn ? `${esc(t.vpn.name || "Home VPN")} <span class="fine muted">${esc(VPN_TYPES.find(v => v[0] === t.vpn.type)?.[1] || t.vpn.type)}${t.vpn.on_demand ? " · on when away" : ""}</span>` : "–" },
      { k: "icon", label: "Home Screen", fmt: t => t.icon === "app" ? "HomeOwl" : t.icon === "none" ? `<span class="muted">no icon</span>` : "check-in page" },
      { k: "id", label: "", fmt: t => `<div class="btnrow"><button class="btn sm" data-dtedit="${esc(t.id)}">Edit</button><button class="btn sm" data-dtdel="${esc(t.id)}">Delete</button></div>` }], r.types)
      : `<p class="muted">No profile types yet. Devices get their NextDNS setup only.</p>`}
    <div class="btnrow" style="margin-top:10px"><button class="btn primary" data-dtedit="">+ Profile type</button></div>`;
}
function editDevType(id) {
  const r = S.devTypes, t = r.types.find(x => x.id === id) || { name: "", wifi: [], people: [], vpn: null, icon: "checkin" }, v = t.vpn || {};
  const keep = set => set ? "Saved — leave empty to keep" : "";
  openModal({ title: id ? `Edit ${t.name}` : "New profile type", wide: true, body: `
    <label>Name<input id="dt-name" maxlength="40" value="${esc(t.name)}" placeholder="e.g. Kids, Adults"></label>
    <h3>People</h3><p class="fine muted">Their devices get this type. A person has one type; ticking them here moves them.</p>
    <div class="dt-checks">${S.devTypePeople.length ? S.devTypePeople.map(p => `<label class="check"><input type="checkbox" data-dtp="${esc(p.id)}" ${arr(t.people).includes(p.id) ? "checked" : ""}> <span>${esc(p.name)}</span></label>`).join("") : `<span class="fine muted">Add people in Family › People & groups.</span>`}</div>
    <h3>Wi-Fi networks</h3><p class="fine muted">The device joins them by itself. The password goes into the device's profile.</p>
    <div class="dt-checks">${r.wlans.length ? r.wlans.map(w => `<label class="check"><input type="checkbox" data-dtw="${esc(w.id)}" ${arr(t.wifi).includes(w.id) ? "checked" : ""} ${w.usable ? "" : "disabled"}> <span>${esc(w.ssid)}${w.guest ? ` <span class="fine muted">guest</span>` : ""}${w.usable ? "" : ` <span class="fine muted">enterprise — not possible</span>`}</span></label>`).join("") : `<span class="fine muted">No Wi-Fi networks from UniFi.</span>`}</div>
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
      <p class="fine muted" data-dtv="wireguard">Each device needs its own WireGuard config: paste it in Family › Check-ins › Change… for that device. Devices need the WireGuard app.</p>
      <p class="fine muted" data-dtv="ikev2 l2tp">The same user name and password go to every device with this type. Secrets are stored in HomeOwl's secret store and go only into device profiles.</p>
    </div>`,
    buttons: [{ label: "Cancel" }, { label: "Save", cls: "primary", onClick: async () => {
      const kind = $("#dt-vpn").value;
      const body = { id, name: $("#dt-name").value.trim(), people: $$("[data-dtp]").filter(c => c.checked).map(c => c.dataset.dtp), wifi: $$("[data-dtw]").filter(c => c.checked).map(c => c.dataset.dtw),
        icon: $("input[name=dt-icon]:checked")?.value || "checkin",
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
    <p class="fine">After approval, the device's check-in page offers to set it up with encrypted DNS through this profile, under its name, so it's recognised on any network (a profile on iPhone, iPad and Mac; Private DNS on Android).</p>` : "";
  const opts = S.ciPeople.map(p => `<option value="${esc(p.id)}" ${p.id === known?.id ? "selected" : ""}>${esc(p.name)}</option>`).join("");
  openModal({ title: "Approve device check-in", body: `
    <label>UniFi name (alias)<input id="ci-alias" maxlength="40" value="${esc(x.name)}"></label>
    <label>Belongs to<select id="ci-person">${x.person_name && !known ? `<option value="" selected>${esc(x.person_name)} (new person)</option>` : ""}${opts}<option value="-">Nobody</option></select></label>
    ${ndPick}
    ${x.user_id ? "" : `<p class="bad-t">UniFi doesn't list this device as a known client, so its name can't be set — only the person is saved.</p>`}
    <p class="fine">Next you'll see exactly what is sent to UniFi. The name change is logged and can be undone from the Changes page.</p>`,
    buttons: [{ label: "Cancel" }, { label: x.user_id ? "Preview" : "Approve", cls: "primary", onClick: () => previewCheckin(x) }] });
}
async function previewCheckin(x) {
  const alias = $("#ci-alias").value.trim(), person = $("#ci-person").value, ndp = $("#ci-nd")?.value || "";
  if (!alias) return toast("Give the device a name", "err");
  const act = x.user_id ? { kind: "rest", resource: "user", id: x.user_id, fields: { name: alias }, description: `Check-in: name ${x.mac} “${alias}”` } : null;
  let pv = null;
  if (act) { try { pv = (await api("/api/action", { action: act, dry_run: true })).preview; } catch (err) { return toast(err.message, "err"); } }
  const who = person === "-" ? "nobody" : person ? S.ciPeople.find(p => p.id === person)?.name : `${x.person_name} (new person)`;
  openModal({ title: "Approve device check-in", wide: true, body: `${pv ? `<div class="pv"><div class="pv-h"><b>UniFi client name</b> <span class="muted">${esc(pv.target)}</span></div>
      <table class="pv-t"><tr><th>Setting</th><th>Now</th><th>New</th></tr><tr><td class="mono">name</td><td>${esc(fmtVal(pv.before.name))}</td><td><b>${esc(alias)}</b></td></tr></table>
      <div class="fine mono">${esc(pv.method)} ${esc(pv.path)}</div><div class="fine">↩ Can be undone from the Changes page.</div></div>` : ""}
    <p>Device <span class="mono">${esc(x.mac)}</span> → <b>${esc(who)}</b> in People.</p>
    ${ndp ? `<p class="fine">Its check-in page will offer a setup through NextDNS profile <b>${esc(S.ciND.profiles.find(p => p.id === ndp)?.name || ndp)}</b>, named “${esc(alias)}”.</p>` : ""}
    ${act && !S.write ? `<p class="muted"><b>Change mode is locked</b> — approving unlocks it for this session.</p>` : ""}`,
    buttons: [{ label: "Cancel" }, { label: "Approve", cls: "primary", onClick: () => finishCheckin(x, act, person, alias, ndp) }] });
}
async function finishCheckin(x, act, person, alias, ndp) {
  try {
    if (act) {
      if (!S.write) await setWrite(true);
      const r = (await api("/api/action", { action: act, dry_run: false })).result;
      if (!r.ok) throw new Error(`UniFi refused the name change: ${r.error || "unknown error"}`);
    }
    await api("/api/checkins/approve", { id: x.id, person_id: person, name: alias, nd_profile: ndp });
    closeModal(); toast("Approved", "ok"); loadCheckins();
    setTimeout(() => collectLive(true, true).catch(() => { }), 2500);
  } catch (err) { toast(err.message, "err"); }
}

document.addEventListener("click", async e => {
  const ok = e.target.closest("[data-ciok]"); if (ok) { approveCheckin(ok.dataset.ciok); return; }
  const no = e.target.closest("[data-cino]");
  if (no) { try { S.checkins = await api("/api/checkins/reject", { id: no.dataset.cino }); toast("Rejected", "ok"); loadCheckins(); } catch (err) { toast(err.message, "err"); } return; }
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
        <p class="fine muted">${r.networks} networks (VLANs) · RADIUS users: ${r.radius.users} · RADIUS profiles: ${r.radius.profiles} · Wi-Fi devices now: ${r.clients.wifi}${Object.keys(r.clients.identity_fields || {}).length ? ` · login fields on devices: ${esc(Object.entries(r.clients.identity_fields).map(([k, n]) => `${k} (${n})`).join(", "))}` : ""}</p>
        ${arr(r.errors).length ? `<p class="fine bad-t">Couldn't read: ${esc(r.errors.join("; "))}</p>` : ""}</details>` : ""}
    <div class="btnrow" style="margin-top:10px"><button class="btn${r ? "" : " primary"}" id="wi-run">${r ? "Check again" : "Check my console (read-only)"}</button></div>`;
}
document.addEventListener("click", async e => {
  if (e.target.id !== "wi-run") return;
  e.target.disabled = true; e.target.textContent = "Reading…";
  try { S.wifiIdent = await api("/api/wifi-ident"); } catch (err) { S.wifiIdent = { error: err.message }; }
  renderWifiIdent();
});
