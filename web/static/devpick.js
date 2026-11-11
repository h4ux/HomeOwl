/* HomeOwl — device picker shared by Family › People & groups and Who's home: the devices you picked as chips,
   suggestions (name matches, check-ins, device types for a group) and one search over every device.
   A "device" can carry several keys (its UniFi client and its NextDNS name), so it's picked once. Depends on core.js, icons.js. */
"use strict";

// groups people usually make, with the device types that belong in them
const GROUP_TPL = [
  { name: "TV & streaming", kinds: ["tv", "speaker", "console"], re: /tv|stream|media|living|lounge|speaker|music/ },
  { name: "Servers", kinds: ["server", "nas", "desktop"], re: /server|nas|lab|storage|backup/ },
  { name: "Smart home", kinds: ["iot", "plug", "bulb", "thermostat", "vacuum", "camera", "doorbell"], re: /smart|iot|home|light|sensor/ },
  { name: "Printers", kinds: ["printer"], re: /print/ },
];

// one list of devices from the analytics meta: UniFi clients, with the NextDNS device of the same name or address folded in
function dpFromMeta(meta) {
  const online = new Set(arr(S.model?.clients).map(c => c.mac));
  const items = [], byMac = new Map(), byName = new Map(), byIp = new Map();
  const profName = id => (meta.nd_profiles || []).find(x => x.id === id)?.name || meta.profiles?.[id] || id;
  arr(meta.clients).forEach(c => {
    const name = c.name || c.hostname || arr(S.model?.clients).find(x => x.mac === c.mac)?.label || c.mac;
    const it = { id: `mac|${c.mac}`, name, keys: [`mac|${c.mac}`], mac: c.mac, ip: c.ip, vendor: c.vendor, network: c.network, online: online.has(c.mac), hostname: c.hostname, wifi: true, last: c.last_seen };
    items.push(it); byMac.set(c.mac, it);
    [c.name, c.hostname].filter(Boolean).forEach(n => byName.set(n.toLowerCase(), it));
    if (c.ip) byIp.set(c.ip, byIp.has(c.ip) ? null : it);
  });
  const ipCount = {}; arr(meta.dns_devices).forEach(d => { if (d.local_ip) ipCount[d.local_ip] = (ipCount[d.local_ip] || 0) + 1; });
  arr(meta.dns_devices).forEach(d => {
    const key = d.profile ? `ndev|${d.profile}/${d.device}` : `dns|${d.device}`;
    const match = byName.get(String(d.device).toLowerCase()) || (d.local_ip && ipCount[d.local_ip] === 1 ? byIp.get(d.local_ip) : null);
    if (match && !match.dns) { match.keys.push(key); match.dns = profName(d.profile); match.legacy = d.device; match.queries = d.queries; match.model ||= d.model; return; }
    items.push({ id: key, name: d.device, keys: [key], ip: d.local_ip, model: d.model, dns: d.profile ? profName(d.profile) : "NextDNS", legacy: d.device, queries: d.queries });
  });
  return items;
}

// what to suggest for a person or group: names that contain theirs, devices checked in for them, and (groups) devices of the right type
function dpSuggest(items, { name, id, group, checkins }) {
  const out = new Map(), n = String(name || "").toLowerCase().trim();
  const tokens = n.split(/[^\p{L}\p{N}]+/u).filter(t => t.length >= 3 && !/^(the|and|room|devices?)$/.test(t));
  arr(checkins).forEach(c => {
    if (c.status === "rejected") return;
    if ((id && c.person_id === id) || (n && (c.person_name || "").toLowerCase() === n)) {
      const it = items.find(x => x.mac === c.mac); if (it) out.set(it.id, `checked in as “${c.name}”`);
    }
  });
  if (tokens.length) items.forEach(it => { const hay = `${it.name} ${it.hostname || ""}`.toLowerCase(); if (!out.has(it.id) && tokens.some(t => hay.includes(t))) out.set(it.id, "name matches"); });
  if (group) {
    const tpl = GROUP_TPL.filter(g => g.re.test(n) || g.name.toLowerCase() === n);
    const kinds = new Set(tpl.flatMap(g => g.kinds));
    if (kinds.size) items.forEach(it => { const k = clientKind(it); if (!out.has(it.id) && kinds.has(k)) out.set(it.id, ICON_LABEL[k] || k); });
  }
  return out;
}

/* devPicker(el, opts) draws into el and keeps its own state.
   opts: items, selected (Set of item ids), suggest() → Map(id → why), ownerOf(item) → {name} | null (someone else's),
         single (pick one and call onPick), onPick(item), onChange(selected) */
function devPicker(el, o) {
  const st = { q: "", more: false, sel: o.selected || new Set() };
  const sub = it => [it.wifi ? (it.online ? "online" : "UniFi") : "", it.dns ? `NextDNS · ${it.dns}` : "", it.model, it.ip, it.vendor].filter(Boolean).join(" · ");
  const ico = it => cliIcon(it, 18);
  const owner = it => { const w = o.ownerOf?.(it); return w ? ` <span class="tag warn">${esc(w.name)}'s</span>` : ""; };
  const row = (it, why) => `<div class="dp-row${st.sel.has(it.id) ? " on" : ""}">${ico(it)}<span class="dp-name"><b>${esc(it.name)}</b>${owner(it)}<span class="fine">${esc(why ? `${why} · ${sub(it)}` : sub(it))}</span></span>
    ${st.sel.has(it.id) && !o.single ? `<button type="button" class="btn sm" data-dpdel="${esc(it.id)}">Remove</button>` : `<button type="button" class="btn sm${why ? " primary" : ""}" data-dpadd="${esc(it.id)}">${o.single ? "Choose" : "Add"}</button>`}</div>`;
  const draw = () => {
    const sug = [...(o.suggest?.() || new Map())].filter(([id]) => !st.sel.has(id) && !o.ownerOf?.(o.items.find(i => i.id === id))).map(([id, why]) => [o.items.find(i => i.id === id), why]).filter(([it]) => it);
    const q = st.q.toLowerCase().trim();
    const sugIds = new Set(q ? [] : sug.slice(0, 8).map(([it]) => it.id));
    const hits = o.items.filter(it => !sugIds.has(it.id)).filter(it => !q || `${it.name} ${it.hostname || ""} ${it.ip || ""} ${it.mac || ""} ${it.vendor || ""} ${it.model || ""} ${it.dns || ""} ${ICON_LABEL[clientKind(it)]}`.toLowerCase().includes(q))
      .sort((a, b) => ((o.rank?.(b) || 0) - (o.rank?.(a) || 0)) || (b.online - a.online) || ((b.queries || 0) - (a.queries || 0)) || String(a.name).localeCompare(b.name));
    const max = q || st.more ? 200 : 6;
    const sel = o.items.filter(it => st.sel.has(it.id));
    el.innerHTML = `${o.single ? "" : `<div class="dp-sel">${sel.length ? sel.map(it => `<span class="dp-chip">${ico(it)}${esc(it.name)}<button type="button" class="x" data-dpdel="${esc(it.id)}" aria-label="Remove ${esc(it.name)}">×</button></span>`).join("")
        : `<span class="fine muted">No devices yet. Add some below.</span>`}${o.extraSel || ""}</div>`}
      ${sug.length ? `<div class="dp-sug"><div class="dp-h"><span>Suggested</span>${sug.length > 1 && !o.single ? `<button type="button" class="btn sm" data-dpall>Add all ${sug.length}</button>` : ""}</div>${sug.slice(0, 8).map(([it, why]) => row(it, why)).join("")}</div>` : ""}
      <input class="dp-q" type="search" placeholder="Search by name, IP, maker or type (phone, TV…)" value="${esc(st.q)}" aria-label="Search devices">
      <div class="dp-list">${hits.slice(0, max).map(it => row(it)).join("") || `<div class="fine muted" style="padding:8px">No device matches “${esc(st.q)}”.</div>`}
        ${hits.length > max ? `<button type="button" class="btn sm dp-more" data-dpmore>Show all ${hits.length}</button>` : ""}</div>`;
  };
  const find = id => o.items.find(i => i.id === id);
  const add = id => { const it = find(id); if (!it) return; if (o.single) return o.onPick?.(it); st.sel.add(id); o.onChange?.(st.sel); draw(); };
  el.addEventListener("click", e => {
    const a = e.target.closest("[data-dpadd]"), d = e.target.closest("[data-dpdel]");
    if (a) return add(a.dataset.dpadd);
    if (d) { st.sel.delete(d.dataset.dpdel); o.onChange?.(st.sel); return draw(); }
    if (e.target.closest("[data-dpall]")) { (o.suggest?.() || new Map()).forEach((_, id) => { if (!o.ownerOf?.(find(id))) st.sel.add(id); }); o.onChange?.(st.sel); return draw(); }
    if (e.target.closest("[data-dpmore]")) { st.more = true; draw(); }
  });
  el.addEventListener("input", e => { if (!e.target.classList.contains("dp-q")) return; st.q = e.target.value; const pos = e.target.selectionStart; draw(); const q = $(".dp-q", el); q.focus(); q.setSelectionRange(pos, pos); });
  draw();
  return { redraw: draw, selected: () => st.sel };
}
