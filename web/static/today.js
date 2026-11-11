/* HomeOwl — Home › Today: the one screen for the household. Status line, who's home, cameras, UPS and the internet,
   each linking to its full page. Reuses the loaders' demo data (presence.js, cameras.js, ups.js). Depends on core.js, app.js. */
"use strict";

S.today = null;

V.today = m => {
  const need = needsYou(m), wan = m.wanLinks.find(l => l.active) || m.wanLinks[0];
  const up = m.wanLinks.some(l => l.enabled && (!l.status || l.status === "ok"));
  const speed = m.info.down !== null ? ` · ${Math.round(m.info.down)} / ${Math.round(m.info.up)} Mbps` : "";
  return `<div class="head"><div><h1>Today</h1><div class="muted">${new Date().toLocaleDateString([], { weekday: "long", day: "numeric", month: "long" })}</div></div></div>
  <div class="status ${need.length || !up ? "warn" : "good"}">
    <div class="status-main"><div class="status-big">${need.length ? `${plural(need.length, "thing needs", "things need")} you` : "All good at home"}</div>
      <div class="muted">${m.wanLinks.length ? (up ? `Internet up${wan?.latency !== null && wan?.latency !== undefined ? ` · ${wan.latency} ms` : ""}${speed}` : "Internet is down") : "No internet connection reported"}</div>
      ${need.length ? `<ul class="status-list" style="margin-top:8px">${need.slice(0, 3).map(n => `<li><span>${esc(n.t)}</span>${n.btn}</li>`).join("")}${need.length > 3 ? `<li><a href="#" data-goto="overview">${need.length - 3} more…</a></li>` : ""}</ul>` : ""}</div>
    <div class="td-acts"><button class="btn" data-goto="guest">Guest Wi-Fi QR</button></div>
  </div>
  <div class="td-grid section" id="td-body">${todayCards()}</div>`;
};

function todayCards() {
  const T = S.today || {}, card = (title, go, body) => `<div class="card td-card"><div class="td-h"><h2>${title}</h2><a href="#" data-goto="${go}" class="fine">Open ›</a></div>${body}</div>`;
  const wait = `<div class="fine">Loading…</div>`;
  // who's home
  const pr = T.pr, people = arr(pr?.people).filter(p => arr(p.devices).length);
  const who = !pr ? wait : !people.length ? `<p class="fine">Tag each person's phone and HomeOwl shows who's home.</p>${S.role === "admin" ? `<button class="btn sm" data-goto="presence">Tag devices</button>` : ""}`
    : `<div class="td-people">${people.map(p => { const st = p.state || {}, home = !!st.home;
        return `<span class="td-person${home ? "" : " away"}"><span class="td-av" style="background:${esc(p.color || "var(--accent)")}">${esc((p.name || "?").slice(0, 1))}</span>
          <span><b>${esc(p.name)}</b><span class="fine">${home ? `home${st.since ? " since " + new Date(st.since).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) : ""}` : `away${st.last_seen || st.since ? " · " + ago(st.last_seen || st.since) : ""}`}</span></span></span>`; }).join("")}</div>`;
  // cameras: up to four pictures
  const cam = T.cam, cams = arr(cam?.cameras);
  const camHtml = !cam ? wait : cam.no_protect || !cams.length ? `<p class="fine">${cam.demo ? "No cameras in this demo." : "No UniFi Protect cameras found."}</p>`
    : `<div class="td-cams">${cams.slice(0, 4).map(c => `<a href="#" data-goto="cameras" title="${esc(c.name)}">${camPicture(c, cam.demo)}<span class="td-camname">${esc(c.name)}</span></a>`).join("")}</div>
       ${(() => { const today = arr(cam.events).filter(e => e.start >= new Date().setHours(0, 0, 0, 0)).length; return `<p class="fine" style="margin:8px 0 0">${cams.filter(c => c.state === "CONNECTED").length} of ${cams.length} online · ${plural(today, "event")} today</p>`; })()}`;
  // UPS
  const ups = T.ups, u = arr(ups?.status?.ups)[0];
  const upsHtml = !ups ? wait : !ups.configured || !u ? `<p class="fine">No UPS set up.</p>`
    : `<div class="td-ups"><b class="${u.power === false ? "warn-t" : "ok-t"}">${u.power === false ? "On battery" : "On mains"}</b>
        <div class="ups-bar${u.charge < 40 ? " low" : ""}"><i style="width:${Math.max(0, Math.min(100, u.charge || 0))}%"></i></div>
        <span class="fine">Battery ${u.charge ?? "–"}%${u.runtime ? ` · ${fmtDur(u.runtime)} runtime` : ""}</span></div>`;
  return card("Who's home", "presence", who) + card("Cameras", "cameras", camHtml) + card("UPS", "ups", upsHtml)
    + card("Network", "overview", `<div class="td-net"><span><b>${S.model.devices.filter(d => d.online).length}/${S.model.devices.length}</b><span class="fine">devices online</span></span><span><b>${S.model.clients.length}</b><span class="fine">clients</span></span><span><b>${scoreOf(S.findings)}</b><span class="fine">config score</span></span></div>`);
}

async function loadToday() {
  const live = S.source === "live", T = S.today = S.today || {};
  const draw = () => { const el = $("#td-body"); if (el && S.view === "today") { el.innerHTML = todayCards(); if (live) refreshSnaps(true); } };
  draw();
  const get = async (k, path, demo) => { try { T[k] = live || (k === "ups" && S.source === "none") ? await api(path) : demo(); } catch { T[k] = k === "cam" ? { no_protect: true, cameras: [] } : k === "ups" ? { configured: false } : { people: [] }; } draw(); };
  await Promise.all([get("pr", "/api/presence?days=1", prFromBundle), get("cam", "/api/cameras?days=1", camFromBundle), get("ups", "/api/ups?days=1", upsFromBundle)]);
}
{ const _render = render; render = function () { _render(); if (S.view === "today") loadToday(); }; }
