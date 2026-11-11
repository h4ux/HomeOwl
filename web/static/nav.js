/* HomeOwl — navigation: an icon strip picks the area, the area's pages are listed beside it, and related pages share tabs.
   Loaded right after core.js (it builds the sidebar before app.js uses it); wraps render() once every script has loaded. */
"use strict";

const NAV_ICON = {
  network: '<path d="M5 12.5a10 10 0 0 1 14 0M8.5 16a5 5 0 0 1 7 0M2 9a14.5 14.5 0 0 1 20 0"/><path d="M12 19.5h.01"/>',
  home: '<path d="M3 11 12 4l9 7"/><path d="M5 10v10h14V10"/><path d="M10 20v-5h4v5"/>',
  family: '<circle cx="9" cy="8" r="3"/><path d="M3 20c0-3.3 2.7-5 6-5s6 1.7 6 5"/><path d="M16 5.2a3 3 0 0 1 0 5.6M21 20c0-2.6-1.4-4.2-3.6-4.8"/>',
  records: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>',
  system: '<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z"/>',
};

// area → pages; a page with several views shows them as tabs. pill = id of a counter badge other scripts fill in.
const NAV = [
  { id: "network", name: "Network", pages: [
    { label: "Overview", views: [["overview", "Overview"]] },
    { label: "Suggestions", views: [["findings", "Suggestions"]], pill: "nav-findings" },
    { label: "Devices", views: [["devices", "Devices"], ["topology", "Topology"], ["ports", "Switch ports"]], pill: "nav-devices" },
    { label: "Firmware", views: [["firmware", "Firmware"]], pill: "nav-firmware" },
    { label: "Clients", views: [["clients", "Clients"]] },
    { label: "Wi-Fi", views: [["wifi", "Wi-Fi & radios"], ["neighbors", "Neighbour Wi-Fi"]] },
    { label: "Networks", views: [["networks", "Networks & VLANs"], ["security", "Security & exposure"], ["dns", "NextDNS & Tailscale"]] },
  ] },
  { id: "home", name: "Home", pages: [
    { label: "Cameras", views: [["cameras", "Cameras"]] },
    { label: "Doors", views: [["doors", "Doors"]] },
    { label: "UPS", views: [["ups", "UPS"]] },
    { label: "Guest Wi-Fi", views: [["guest", "Guest Wi-Fi"]] },
  ] },
  { id: "family", name: "Family", pages: [
    { label: "Who's home", views: [["presence", "Who's home"]] },
    { label: "Analytics", views: [["analytics", "Analytics"]] },
    { label: "Check-ins", views: [["checkins", "Check-ins"]], pill: "nav-checkins", hidden: true },
  ] },
  { id: "records", name: "Records", pages: [
    { label: "History & events", views: [["history", "History & events"]] },
    { label: "Changes", views: [["changes", "Changes"]] },
    { label: "Config history", views: [["confighistory", "Config history"]] },
  ] },
  { id: "system", name: "System", pages: [
    { label: "Settings", views: [["settings", "Settings"]], id: "nav-settings", hidden: true },
    { label: "Cheatsheet", views: [["cheatsheet", "Cheatsheet"]] },
    { label: "Raw data", views: [["raw", "Raw data"]] },
  ] },
];
const navPageOf = v => { for (const a of NAV) for (const p of a.pages) if (p.views.some(([x]) => x === v)) return { area: a, page: p }; return { area: NAV[0], page: NAV[0].pages[0] }; };

// build the sidebar once: every area's pages exist in the DOM (other scripts fill their badges), only the current area shows
(function buildNav() {
  const rail = $("#rail"), nav = $("#nav"); if (!rail || !nav) return;
  rail.innerHTML = NAV.map(a => `<a href="#" class="rail-btn" data-area="${a.id}" data-goto="${a.pages[0].views[0][0]}" title="${esc(a.name)}" aria-label="${esc(a.name)}">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${NAV_ICON[a.id]}</svg><span>${esc(a.name)}</span></a>`).join("");
  nav.innerHTML = NAV.map(a => `<div class="nav-area" data-navarea="${a.id}"><div class="nav-h">${esc(a.name)}</div>
    ${a.pages.map(p => `<a data-view="${p.views[0][0]}" data-views="${p.views.map(([v]) => v).join(" ")}"${p.id ? ` id="${p.id}"` : ""}${p.hidden ? " hidden" : ""}>${esc(p.label)}${p.pill ? ` <span class="pill" id="${p.pill}"></span>` : ""}</a>`).join("")}</div>`).join("");
})();

function syncNav() {
  const { area, page } = navPageOf(S.view);
  // a hidden area page (e.g. Settings for viewers) shouldn't leave the rail pointing at a page you can't open
  $$("#rail .rail-btn").forEach(b => { const on = b.dataset.area === area.id; b.classList.toggle("act", on); b.setAttribute("aria-current", on ? "true" : "false");
    const a = NAV.find(x => x.id === b.dataset.area), first = a.pages.find(p => !$(`#nav a[data-view="${p.views[0][0]}"]`)?.hidden) || a.pages[0]; b.dataset.goto = first.views[0][0]; });
  $$("#nav .nav-area").forEach(el => { el.hidden = el.dataset.navarea !== area.id; });
  $$("#nav a[data-views]").forEach(a => a.classList.toggle("active", a.dataset.views.split(" ").includes(S.view)));
  // tabs across related pages
  const main = $("#main"); if (!main) return;
  $("#page-tabs")?.remove();
  if (page.views.length > 1) {
    main.insertAdjacentHTML("afterbegin", `<nav class="page-tabs" id="page-tabs" aria-label="${esc(page.label)}">${page.views.map(([v, l]) => `<a href="#" data-goto="${v}" class="${v === S.view ? "act" : ""}" aria-current="${v === S.view ? "page" : "false"}">${esc(l)}</a>`).join("")}</nav>`);
  }
}
document.addEventListener("DOMContentLoaded", () => { const _render = render; render = function () { _render(); syncNav(); }; });

// http://…/#guest opens that page (the menu bar's buttons use it)
{
  const h = location.hash.slice(1);
  if (h && NAV.some(a => a.pages.some(p => p.views.some(([v]) => v === h)))) { S.view = h; history.replaceState(null, "", location.pathname + location.search); }
}
