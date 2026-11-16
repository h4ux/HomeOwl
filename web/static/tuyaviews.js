/* HomeOwl — Tuya page views: Plan (the house from above, each switch where it sits), Dollhouse (floor by floor,
   lit windows, the garden beside it) and Rooms (a card per room with its own look). The layout is saved per house
   (tuyaplan.go). Depends on core.js, app.js, tuyaicons.js and tuya.js. */
"use strict";

const TV_VIEWS = { cards: "Cards", plan: "Plan", doll: "Dollhouse", rooms: "Rooms" };
try { S.tuyaView = localStorage.getItem("homeowl-tuya-view") || "cards"; } catch { S.tuyaView = "cards"; }
if (!TV_VIEWS[S.tuyaView]) S.tuyaView = "cards";
S.tvArr = null; // arranging a house's plan: { home, rooms, dots }
// rooms that are outside, until an admin says otherwise (Rooms…)
const TV_OUT_RE = /garden|yard|pool|outside|outdoor|parking|terrace|balcony|patio|porch|roof|gate|driveway|deck|lawn|jacuzzi|bbq|courtyard/i;
const TV_NO_ROOM = "Other";

function tvViewBar() {
  return `<div class="seg tv-views" role="group" aria-label="View">${Object.entries(TV_VIEWS).map(([k, l]) => `<button type="button" data-tvview="${k}" class="${S.tuyaView === k ? "on" : ""}">${l}</button>`).join("")}</div>`;
}

// what can be tapped on a device: each switch (a 3-gang switch is three), or the device itself (a sensor)
function tvItems(x) {
  const sw = arr(x.switches), rd = arr(x.readings)[0], read = rd ? `${rd.value}${rd.unit ? " " + rd.unit : ""}` : "";
  if (sw.length) return sw.map(s => ({ x, s, key: `${x.id}/${s.code}`, label: sw.length > 1 ? s.label || s.code : x.name, on: !!s.on, read }));
  return [{ x, s: null, key: `${x.id}/-`, label: x.name, on: false, read }];
}
const tvRoomOf = x => x.room || TV_NO_ROOM;
const tvCan = it => tuyaAdmin() && it.x.online && it.s;
const tvSwAttrs = it => tvCan(it) && !S.tvArr ? `data-tuyasw="${esc(it.x.id)}" data-code="${esc(it.s.code)}" data-on="${it.on ? "1" : ""}" role="switch" aria-checked="${it.on}" tabindex="0"` : "";
const tvTitle = it => `${it.x.name}${it.s && it.label !== it.x.name ? ` · ${it.label}` : ""}${!it.x.online ? " · offline" : it.s ? ` · ${it.on ? "on" : "off"}` : ""}${it.read ? ` · ${it.read}` : ""}`;
const tvShort = (t, n) => t.length > n ? t.slice(0, n - 1) + "…" : t;

function tvRoomSummary(items) {
  const on = items.filter(i => i.on).length, off = new Set(items.filter(i => !i.x.online).map(i => i.x.id)).size, read = items.find(i => i.read && !i.s)?.read;
  return [on ? `${on} on` : items.some(i => i.s) ? "all off" : "", read || "", off ? `${off} offline` : ""].filter(Boolean).join(" · ");
}

// the house's rooms (Smart Life's list and the rooms devices are in) and what's in each
function tvRooms(h, devs) {
  const byRoom = new Map();
  arr(h.rooms).forEach(r => byRoom.set(r, []));
  devs.forEach(x => { const r = tvRoomOf(x); if (!byRoom.has(r)) byRoom.set(r, []); byRoom.get(r).push(...tvItems(x)); });
  return byRoom;
}
const tvPlanOf = h => S.tvArr?.home === h.id ? S.tvArr : (h.plan || {});
const tvIsOut = (h, r) => { const o = tvPlanOf(h).rooms?.[r]?.out; return typeof o === "boolean" ? o : TV_OUT_RE.test(r); };

// where each room sits on the plan: saved, or laid out in rows (inside) with a band below (outside)
function tvLayout(h, rooms) {
  const P = tvPlanOf(h).rooms || {}, L = {};
  rooms.forEach(r => { if (P[r]?.w > 0) L[r] = { ...P[r] }; });
  const lay = (list, y0, hgt, out) => {
    const cols = Math.min(4, Math.max(1, Math.ceil(Math.sqrt(list.length * 1.4)))), w = Math.floor(1000 / cols);
    list.forEach((r, i) => { L[r] = { ...(P[r] || {}), x: (i % cols) * w, y: y0 + Math.floor(i / cols) * hgt, w: i % cols === cols - 1 ? 1000 - (cols - 1) * w : w, h: hgt, out }; });
  };
  const bottom = list => Math.max(0, ...list.map(r => r.y + r.h));
  lay(rooms.filter(r => !L[r] && !tvIsOut(h, r)), bottom(Object.values(L).filter(r => !r.out)), 210, false);
  const rest = rooms.filter(r => !L[r]);
  lay(rest, bottom(Object.values(L)) + (Object.keys(L).length ? 40 : 0), 150, true);
  Object.keys(L).forEach(r => { L[r].out = tvIsOut(h, r); });
  return L;
}
// where each switch sits in its room: saved (fractions of the room), or in a tidy grid under the room's name
function tvDots(h, room, items) {
  const D = tvPlanOf(h).dots || {}, n = items.length, cols = Math.max(1, Math.ceil(Math.sqrt(n * Math.max(1, room.w / room.h) * 1.3))), rows = Math.ceil(n / cols);
  return items.map((it, i) => D[it.key] ? { it, fx: D[it.key][0], fy: D[it.key][1] } : { it, fx: (i % cols + .5) / cols, fy: .3 + .62 * (Math.floor(i / cols) + .5) / rows });
}

function tvPlanSvg(h, byRoom) {
  const rooms = [...byRoom.keys()], L = tvLayout(h, rooms), arrange = S.tvArr?.home === h.id;
  const vals = Object.values(L), H = Math.max(200, ...vals.map(r => r.y + r.h)) + 8, ins = vals.filter(r => !r.out);
  const wall = ins.length ? (() => { const x0 = Math.min(...ins.map(r => r.x)), y0 = Math.min(...ins.map(r => r.y)), x1 = Math.max(...ins.map(r => r.x + r.w)), y1 = Math.max(...ins.map(r => r.y + r.h)); return `<rect class="tv-wall" x="${x0 + 3}" y="${y0 + 3}" width="${x1 - x0 - 6}" height="${y1 - y0 - 6}" rx="4"/>`; })() : "";
  const room = r => {
    const g = L[r], items = byRoom.get(r) || [], lit = items.some(i => i.on), dots = tvDots(h, g, items);
    const dot = ({ it, fx, fy }) => {
      const cx = g.x + 14 + fx * (g.w - 28), cy = g.y + 14 + fy * (g.h - 28);
      return `<g class="tv-dot${it.on ? " lit" : ""}${!it.x.online ? " dead" : ""}${it.s ? "" : " sensor"}${arrange ? " drag" : ""}" transform="translate(${cx.toFixed(1)} ${cy.toFixed(1)})" ${tvSwAttrs(it)} ${arrange ? `data-tvdot="${esc(it.key)}" data-room="${esc(r)}"` : ""}>
        <title>${esc(tvTitle(it))}</title>${it.on ? `<circle class="tv-glow" r="26"/>` : ""}<circle r="14"/>${it.s ? "" : `<text class="tv-read" y="4" text-anchor="middle">${esc(tvShort(it.read || "•", 6))}</text>`}
        <text class="tv-dl" y="31" text-anchor="middle">${esc(tvShort(it.label, 14))}</text></g>`;
    };
    return `<g class="tv-room" data-tvroom="${esc(r)}" data-home="${esc(h.id)}">
      <rect class="tv-rm${lit ? " lit" : ""}${g.out ? " out" : ""}${arrange ? " drag" : ""}" x="${g.x + 3}" y="${g.y + 3}" width="${g.w - 6}" height="${g.h - 6}" rx="${g.out ? 14 : 2}" ${arrange ? `data-tvmove="${esc(r)}"` : ""}/>
      <text class="tv-rl" x="${g.x + 16}" y="${g.y + 28}">${esc(tvShort(r, Math.max(6, Math.floor(g.w / 11))))}</text>
      <text class="tv-rs" x="${g.x + 16}" y="${g.y + 46}">${esc(tvRoomSummary(items))}</text>
      ${dots.map(dot).join("")}
      ${arrange ? `<rect class="tv-handle" x="${g.x + g.w - 26}" y="${g.y + g.h - 26}" width="20" height="20" rx="4" data-tvsize="${esc(r)}"/>` : ""}</g>`;
  };
  return `<svg class="tv-plan" viewBox="0 0 1000 ${H}" role="group" aria-label="${esc(h.name)} floor plan">${wall}${rooms.map(room).join("")}</svg>`;
}

const TV_FLOOR = { "-2": "Basement 2", "-1": "Basement", 0: "Ground floor", 1: "1st floor", 2: "2nd floor", 3: "3rd floor", 4: "4th floor", 5: "5th floor", 6: "6th floor" };
function tvDollSvg(h, byRoom) {
  const P = tvPlanOf(h).rooms || {}, rooms = [...byRoom.keys()], outs = rooms.filter(r => tvIsOut(h, r)), ins = rooms.filter(r => !tvIsOut(h, r));
  const floorOf = r => P[r]?.floor || 0, floors = [...new Set(ins.map(floorOf))].sort((a, b) => b - a);
  if (!floors.length) floors.push(0);
  const HX = 30, HW = outs.length ? 620 : 940, ROOF = 96;
  // each floor's rooms share its width by how much is in them; a floor grows to fit its windows
  const plan = floors.map(f => {
    const list = ins.filter(r => floorOf(r) === f), tot = list.reduce((n, r) => n + Math.max(2, (byRoom.get(r) || []).length), 0) || 1;
    let x = HX; const rs = list.map(r => { const w = HW * Math.max(2, (byRoom.get(r) || []).length) / tot, o = { r, x, w }; x += w; return o; });
    const fh = Math.max(150, ...rs.map(o => { const per = Math.max(1, Math.floor((o.w - 24) / 36)); return 76 + Math.ceil((byRoom.get(o.r) || []).length / per) * 34; }));
    return { f, rs, fh };
  });
  let y = ROOF; plan.forEach(p => { p.y = y; y += p.fh; });
  const ground = (plan.filter(p => p.f >= 0).pop() || plan[0]).y + (plan.filter(p => p.f >= 0).pop() || plan[0]).fh;
  const gardenH = 190, H = Math.max(y, ground) + 24;
  const win = (it, x, y) => `<g class="tv-win${it.on ? " lit" : ""}${!it.x.online ? " dead" : ""}${it.s ? "" : " sensor"}" transform="translate(${x} ${y})" ${tvSwAttrs(it)}><title>${esc(tvTitle(it))}</title><rect width="28" height="24" rx="3"/>${it.s ? `<line x1="14" y1="2" x2="14" y2="22"/><line x1="2" y1="12" x2="26" y2="12"/>` : `<text x="14" y="16" text-anchor="middle">${esc(tvShort(it.read || "•", 4))}</text>`}</g>`;
  const room = (o, p) => {
    const items = byRoom.get(o.r) || [], per = Math.max(1, Math.floor((o.w - 24) / 36)), lit = items.some(i => i.on);
    return `<g class="tv-room" data-tvroom="${esc(o.r)}" data-home="${esc(h.id)}"><rect class="tv-rm${lit ? " lit" : ""}" x="${o.x}" y="${p.y}" width="${o.w}" height="${p.fh}"/>
      <text class="tv-rl" x="${o.x + 12}" y="${p.y + 24}">${esc(tvShort(o.r, Math.max(5, Math.floor(o.w / 10))))}</text><text class="tv-rs" x="${o.x + 12}" y="${p.y + 41}">${esc(tvShort(tvRoomSummary(items), Math.max(5, Math.floor(o.w / 7))))}</text>
      ${items.map((it, i) => win(it, o.x + 12 + (i % per) * 36, p.y + 56 + Math.floor(i / per) * 34)).join("")}</g>`;
  };
  const roofY = ROOF, house = `<path class="tv-roof" d="M${HX - 18} ${roofY} L${HX + HW / 2} ${roofY - 76} L${HX + HW + 18} ${roofY} Z"/>
    <rect class="tv-wall" x="${HX}" y="${plan[0].y}" width="${HW}" height="${y - plan[0].y}"/>`;
  const floorsSvg = plan.map(p => `${p.rs.map(o => room(o, p)).join("")}<text class="tv-fl" x="${HX + HW - 8}" y="${p.y + p.fh - 8}" text-anchor="end">${esc(TV_FLOOR[p.f] || `Floor ${p.f}`)}</text>`).join("");
  // the garden beside the house: each outside place a group of lights on posts
  const GX = HX + HW + 50, GW = 1000 - GX - 10, gw = outs.length ? GW / outs.length : 0;
  const garden = outs.map((r, i) => {
    const items = byRoom.get(r) || [], x0 = GX + i * gw, step = Math.min(56, (gw - 20) / Math.max(1, items.length));
    return `<g class="tv-room" data-tvroom="${esc(r)}" data-home="${esc(h.id)}"><rect class="tv-yard" x="${x0 + 4}" y="${ground - gardenH + 20}" width="${gw - 8}" height="${gardenH - 20}" rx="12"/>
      <text class="tv-rl" x="${x0 + 14}" y="${ground - gardenH + 44}">${esc(tvShort(r, Math.max(5, Math.floor(gw / 10))))}</text><text class="tv-rs" x="${x0 + 14}" y="${ground - gardenH + 61}">${esc(tvShort(tvRoomSummary(items), Math.max(5, Math.floor(gw / 7))))}</text>
      ${items.map((it, j) => { const cx = x0 + 14 + step * (j + .5), top = ground - 40 - (j % 2) * 26; return `<line class="tv-post" x1="${cx}" y1="${ground}" x2="${cx}" y2="${top + 12}"/>
        <g class="tv-dot${it.on ? " lit" : ""}${!it.x.online ? " dead" : ""}${it.s ? "" : " sensor"}" transform="translate(${cx} ${top})" ${tvSwAttrs(it)}><title>${esc(tvTitle(it))}</title>${it.on ? `<circle class="tv-glow" r="22"/>` : ""}<circle r="12"/></g>`; }).join("")}</g>`;
  }).join("");
  return `<svg class="tv-plan tv-doll" viewBox="0 0 1000 ${H}" role="group" aria-label="${esc(h.name)} by floor">${house}${floorsSvg}<line class="tv-ground" x1="0" y1="${ground}" x2="1000" y2="${ground}"/>${garden}</svg>`;
}

// each room as a card with its own look, its switches as glass pills on top
function tvRoomLook(h, r) {
  const p = tvPlanOf(h).rooms?.[r] || {};
  if (p.look === "photo" && p.photo) return { cls: "look-photo", style: `background-image:url('/api/tuya/bg?home=${encodeURIComponent(h.id)}&room=${encodeURIComponent(r)}&v=${p.photo}')` };
  if (p.look && TUYA_LOOKS[p.look]) return { cls: `look-${p.look}`, style: "" };
  return tuyaLookOf({ id: `${h.id}/${r}` });
}
function tvRoomCards(h, byRoom) {
  const MAX = 8;
  const cards = [...byRoom.entries()].filter(([, items]) => items.length).map(([r, items]) => {
    const lk = tvRoomLook(h, r), more = items.length - MAX;
    return `<div class="tv-card ${lk.cls}" style="${esc(lk.style)}" data-tvroom="${esc(r)}" data-home="${esc(h.id)}">
      <div><b>${esc(r)}</b><small>${esc(tvRoomSummary(items) || plural(items.length, "device"))}</small></div>
      <div class="tv-pills">${items.slice(0, MAX).map(it => `<button type="button" class="tv-pill${it.on ? " on" : ""}${!it.x.online ? " dead" : ""}" ${tvSwAttrs(it)} title="${esc(tvTitle(it))}">${esc(tvShort(it.label, 18))}${it.s ? "" : it.read ? ` · ${esc(it.read)}` : ""}</button>`).join("")}
        ${more > 0 ? `<button type="button" class="tv-pill more">+${more} more</button>` : ""}</div></div>`;
  }).join("");
  return `<div class="tv-cards">${cards || `<p class="muted">No devices in these rooms.</p>`}</div>`;
}

// a house in the chosen view; the room's own devices open from any room
function tvHouse(h, devs) {
  const byRoom = tvRooms(h, devs);
  if (!byRoom.size) return `<p class="muted">No devices here.</p>`;
  const arrange = S.tvArr?.home === h.id;
  const tools = tuyaAdmin() ? `<div class="tv-tools">${S.tuyaView === "plan" ? (arrange
      ? `<span class="fine muted">Drag rooms and switches; drag a room's corner to resize it.</span><button class="btn sm" data-tvarr="reset" data-home="${esc(h.id)}">Automatic layout</button><button class="btn sm" data-tvarr="cancel">Cancel</button><button class="btn sm primary" data-tvarr="done" data-home="${esc(h.id)}">Done</button>`
      : `<button class="btn sm ghost" data-tvarr="start" data-home="${esc(h.id)}">Arrange</button>`) : ""}${arrange ? "" : `<button class="btn sm ghost" data-tvrooms="${esc(h.id)}">Rooms…</button>`}</div>` : "";
  const body = S.tuyaView === "plan" ? `<div class="tv-wrap${arrange ? " arranging" : ""}" data-tvplan="${esc(h.id)}">${tvPlanSvg(h, byRoom)}</div>`
    : S.tuyaView === "doll" ? `<div class="tv-wrap">${tvDollSvg(h, byRoom)}</div>` : tvRoomCards(h, byRoom);
  return tools + body;
}

// a room's devices, as cards
function tvRoomModal(home, room) {
  const devs = arr(S.tuya?.devices).filter(x => x.home === home && tvRoomOf(x) === room && (!S.tuyaKind || (x.kind || "Other") === S.tuyaKind));
  S.tvRoomOpen = [home, room];
  openModal({ title: room, wide: true, body: `<div class="tv-roommodal">${devs.length ? `<div class="tuya-grid">${devs.map(tuyaCard).join("")}</div>` : `<p class="muted">Nothing in this room.</p>`}</div>` });
}

// floors, inside or outside, and each room's look
function tvRoomsModal(home) {
  const h = arr(S.tuya?.homes).find(x => x.id === home); if (!h) return;
  const devs = arr(S.tuya.devices).filter(x => x.home === home), rooms = [...tvRooms(h, devs).keys()], P = h.plan?.rooms || {};
  const looks = (r) => `<select data-tvlook="${esc(r)}"><option value="">Automatic</option>${Object.entries(TUYA_LOOKS).map(([k, l]) => `<option value="${k}" ${P[r]?.look === k ? "selected" : ""}>${esc(l)}</option>`).join("")}
    <option value="photo" ${P[r]?.look === "photo" ? "selected" : ""}>Photo…</option></select>`;
  openModal({ title: `${h.name}: rooms`, wide: true, body: `<p class="fine muted">Which floor each room is on (Dollhouse), whether it's outside (drawn in the garden), and its picture (Rooms).</p>
    <div class="tw"><table class="tv-rt"><thead><tr><th>Room</th><th>Floor</th><th>Outside</th><th>Look</th></tr></thead><tbody>${rooms.map(r => `<tr>
      <td><b>${esc(r)}</b></td>
      <td><select data-tvfloor="${esc(r)}">${[3, 2, 1, 0, -1].map(f => `<option value="${f}" ${(P[r]?.floor || 0) === f ? "selected" : ""}>${TV_FLOOR[f]}</option>`).join("")}</select></td>
      <td><input type="checkbox" data-tvout="${esc(r)}" ${tvIsOut(h, r) ? "checked" : ""} aria-label="${esc(r)} is outside"></td>
      <td>${looks(r)}<input type="file" data-tvphoto="${esc(r)}" accept="image/jpeg,image/png,image/webp,image/heic" hidden></td></tr>`).join("")}</tbody></table></div>`,
    buttons: [{ label: "Cancel" }, { label: "Save", cls: "primary", onClick: async () => {
      const rs = {}, cur = h.plan?.rooms || {};
      rooms.forEach(r => {
        const base = cur[r]?.w > 0 ? { x: cur[r].x, y: cur[r].y, w: cur[r].w, h: cur[r].h } : { x: 0, y: 0, w: 0, h: 0 };
        rs[r] = { ...base, floor: +$(`[data-tvfloor="${CSS.escape(r)}"]`).value, out: $(`[data-tvout="${CSS.escape(r)}"]`).checked };
        if (base.w && rs[r].out !== !!cur[r].out) Object.assign(rs[r], { x: 0, y: 0, w: 0, h: 0 }); // moved in or out: laid out again
      });
      if (!await tuyaDo("home-plan", { id: home, plan: { rooms: rs, dots: h.plan?.dots || {} } })) return;
      for (const r of rooms) {
        const sel = $(`[data-tvlook="${CSS.escape(r)}"]`), was = P[r]?.look || "", file = $(`[data-tvphoto="${CSS.escape(r)}"]`)?.files?.[0];
        if (sel.value === "photo" && file) { try { await tuyaDo("room-look", { id: home, room: r, look: "photo", photo: await tuyaShrink(file) }); } catch (e) { toast(e.message, "err"); } }
        else if (sel.value !== "photo" && sel.value !== was) await tuyaDo("room-look", { id: home, room: r, look: sel.value });
      }
      closeModal(); toast("Saved", "ok");
    } }] });
  $$("[data-tvlook]").forEach(s => s.addEventListener("change", () => { const f = $(`[data-tvphoto="${CSS.escape(s.dataset.tvlook)}"]`); f.hidden = s.value !== "photo"; if (s.value === "photo") f.click(); }));
}

// arranging the plan: drag a room, its corner, or a switch; snapped to a 10-unit grid
function tvArrStart(home) {
  const h = arr(S.tuya?.homes).find(x => x.id === home); if (!h) return;
  const devs = arr(S.tuya.devices).filter(x => x.home === home), rooms = [...tvRooms(h, devs).keys()];
  S.tvArr = { home, rooms: {}, dots: { ...(h.plan?.dots || {}) } };
  const L = tvLayout({ ...h, plan: h.plan || {} }, rooms);
  Object.entries(L).forEach(([r, g]) => { S.tvArr.rooms[r] = { x: g.x, y: g.y, w: g.w, h: g.h, floor: g.floor || 0, out: !!g.out }; });
  tvRedraw();
}
const tvRedraw = () => { if ($("#tuya-body") && S.tuya) $("#tuya-body").innerHTML = tuyaBody(S.tuya); };
let tvDrag = null;
document.addEventListener("pointerdown", e => {
  const wrap = e.target.closest?.(".tv-wrap.arranging"); if (!wrap || !S.tvArr) return;
  // the drawing is redrawn as you drag, so each point is measured against the current one
  const pt = (ev) => { const m = wrap.querySelector("svg").getScreenCTM().inverse(); return { x: ev.clientX * m.a + ev.clientY * m.c + m.e, y: ev.clientX * m.b + ev.clientY * m.d + m.f }; };
  const t = e.target.closest("[data-tvsize],[data-tvdot],[data-tvmove]"); if (!t) return;
  const p0 = pt(e), A = S.tvArr;
  if (t.dataset.tvsize) tvDrag = { kind: "size", r: t.dataset.tvsize, p0, g0: { ...A.rooms[t.dataset.tvsize] } };
  else if (t.dataset.tvdot) tvDrag = { kind: "dot", key: t.dataset.tvdot, r: t.dataset.room };
  else tvDrag = { kind: "move", r: t.dataset.tvmove, p0, g0: { ...A.rooms[t.dataset.tvmove] } };
  tvDrag.pt = pt; tvDrag.wrap = wrap;
  wrap.setPointerCapture(e.pointerId); e.preventDefault();
});
document.addEventListener("pointermove", e => {
  if (!tvDrag || !S.tvArr) return;
  const d = tvDrag, p = d.pt(e), A = S.tvArr, snap = v => Math.round(v / 10) * 10;
  if (d.kind === "move") { const g = A.rooms[d.r]; g.x = Math.max(0, Math.min(1000 - g.w, snap(d.g0.x + p.x - d.p0.x))); g.y = Math.max(0, snap(d.g0.y + p.y - d.p0.y)); }
  else if (d.kind === "size") { const g = A.rooms[d.r]; g.w = Math.max(80, Math.min(1000 - g.x, snap(d.g0.w + p.x - d.p0.x))); g.h = Math.max(80, snap(d.g0.h + p.y - d.p0.y)); }
  else { const g = A.rooms[d.r]; A.dots[d.key] = [Math.max(0, Math.min(1, (p.x - g.x - 14) / (g.w - 28))), Math.max(0, Math.min(1, (p.y - g.y - 14) / (g.h - 28)))]; }
  const h = arr(S.tuya.homes).find(x => x.id === A.home), devs = arr(S.tuya.devices).filter(x => x.home === A.home && (!S.tuyaKind || (x.kind || "Other") === S.tuyaKind));
  d.wrap.innerHTML = tvPlanSvg(h, tvRooms(h, devs));
});
document.addEventListener("pointerup", () => { tvDrag = null; });

document.addEventListener("click", async e => {
  const t = e.target;
  const v = t.closest("[data-tvview]");
  if (v) { S.tuyaView = v.dataset.tvview; S.tvArr = null; try { localStorage.setItem("homeowl-tuya-view", S.tuyaView); } catch { } tvRedraw(); return; }
  const ar = t.closest("[data-tvarr]");
  if (ar) {
    const op = ar.dataset.tvarr;
    if (op === "start") return tvArrStart(ar.dataset.home);
    if (op === "cancel") { S.tvArr = null; return tvRedraw(); }
    if (op === "reset") { const A = S.tvArr; S.tvArr = null; ar.disabled = true; await tuyaDo("home-plan", { id: A.home, plan: { rooms: Object.fromEntries(Object.entries(A.rooms).map(([r, g]) => [r, { x: 0, y: 0, w: 0, h: 0, floor: g.floor, out: g.out }])), dots: {} } }, "Automatic layout"); return; }
    if (op === "done") { const A = S.tvArr; ar.disabled = true; if (await tuyaDo("home-plan", { id: A.home, plan: { rooms: A.rooms, dots: A.dots } }, "Layout saved")) { S.tvArr = null; tvRedraw(); } else ar.disabled = false; return; }
  }
  const rm = t.closest("[data-tvrooms]"); if (rm) return tvRoomsModal(rm.dataset.tvrooms);
  if (S.tvArr || t.closest("[data-tuyasw]")) return; // switching is tuya.js's
  const room = t.closest("[data-tvroom]");
  if (room) tvRoomModal(room.dataset.home, room.dataset.tvroom);
});
// keyboard: Enter or Space on a switch drawn in the plan
document.addEventListener("keydown", e => {
  if ((e.key === "Enter" || e.key === " ") && e.target.matches?.("g[data-tuyasw]")) { e.preventDefault(); e.target.dispatchEvent(new MouseEvent("click", { bubbles: true })); }
});
