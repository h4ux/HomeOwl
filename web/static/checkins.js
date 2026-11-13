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
    const [ci, meta] = await Promise.all([api("/api/checkins"), api("/api/analytics/meta", {}).catch(() => ({}))]);
    S.checkins = ci; S.ciPeople = meta.people || [];
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
    ${c.links.map(l => `<div class="cmd"><code>${esc(l)}</code><button class="btn sm" data-copy="${esc(l)}">Copy</button></div>`).join("") || `<p class="bad-t">No home-network address found.</p>`}
    ${c.lan ? `<p class="fine">Or scan the code with the device's camera. HomeOwl recognises the device by its address on your network — nothing to install.</p>`
      : `<p class="bad-t">Home-network access is off (Settings › Access), so devices can't reach this page.</p>`}</div></div>`;
  const rows = pend.map(x => `<div class="card ci-row">
      <div class="ci-ico" aria-hidden="true">${CI_KIND[x.kind] || "❔"}</div>
      <div class="ci-main"><div><b>${esc(x.name)}</b> <span class="muted">for</span> ${personLabel(x)}</div>
        <div class="fine">Now called <b>${esc(x.seen || "unnamed")}</b>${x.vendor ? ` · ${esc(x.vendor)}` : ""} · <span class="mono">${esc(x.mac)}</span> · ${esc(x.ip)}${x.network ? ` · ${esc(x.network)}` : ""} · ${ago(x.time * 1000)}</div></div>
      <div class="btnrow"><button class="btn sm primary" data-ciok="${esc(x.id)}">Approve…</button><button class="btn sm" data-cino="${esc(x.id)}">Reject</button></div></div>`).join("");
  return `${pend.length ? rows : `<div class="card"><p class="muted" style="margin:0">Nothing waiting. New check-ins show up here (and as a number in the menu).</p></div>`}
    ${how}
    ${past.length ? `<div class="card section"><h2>Recently decided</h2>${table("ci-past", [
      { k: "name", label: "Device", fmt: x => `${CI_KIND[x.kind] || ""} ${esc(x.name)}` }, { k: "mac", label: "MAC", fmt: x => `<span class="mono">${esc(x.mac)}</span>` },
      { k: "status", label: "Decision", fmt: x => `<span class="tag ${x.status === "approved" ? "good" : ""}">${esc(x.status)}</span> <span class="fine">${esc(x.by || "")}</span>` },
      { k: "profile_at", label: "Setup", fmt: x => !x.nd_profile ? "–" : x.profile_at ? `<span class="tag good" title="NextDNS profile ${esc(x.nd_profile)}">profile downloaded</span> <span class="fine">${ago(x.profile_at * 1000)}</span>` : `<span class="fine" title="NextDNS profile ${esc(x.nd_profile)}">offered</span>` },
      { k: "time", label: "Asked", fmt: x => fmtTime(x.time * 1000) }], past)}</div>` : ""}`;
}

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
