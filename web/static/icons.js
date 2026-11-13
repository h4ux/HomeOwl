/* HomeOwl — device icons: UniFi gear by model, clients by type (guessed from name, hostname, maker and OS),
   with generic fall-backs. Original line icons (24×24, currentColor) — no vendor logos. */
"use strict";

const ICON_PATHS = {
  gateway: '<rect x="2.5" y="8" width="19" height="8" rx="2"/><circle cx="6.5" cy="12" r="1"/><path d="M10 12h8"/><path d="M8 8V5.5M16 8V5.5"/>',
  switch: '<rect x="2" y="8" width="20" height="8" rx="1.5"/><path d="M5 12h1M8 12h1M11 12h1M14 12h1M17 12h1"/><path d="M5 14.5h14" opacity=".5"/>',
  switchmini: '<rect x="5" y="8" width="14" height="8" rx="1.5"/><path d="M8 12h1M11 12h1M14 12h1"/>',
  ap: '<ellipse cx="12" cy="13" rx="8.5" ry="3.5"/><path d="M3.5 13v1.2c0 1.9 3.8 3.5 8.5 3.5s8.5-1.6 8.5-3.5V13"/><circle cx="12" cy="12.8" r=".9"/><path d="M8.5 6.5a5 5 0 0 1 7 0M10.2 8.3a2.5 2.5 0 0 1 3.6 0"/>',
  apwall: '<rect x="7" y="3" width="10" height="18" rx="2"/><path d="M9.5 7.5a3.5 3.5 0 0 1 5 0M10.8 9.2a1.6 1.6 0 0 1 2.4 0"/><rect x="9.5" y="15" width="5" height="3" rx=".5"/>',
  apoutdoor: '<rect x="9" y="3" width="6" height="15" rx="3"/><path d="M12 18v3M8 21h8"/><path d="M5.5 7.5a8 8 0 0 0 0 7M18.5 7.5a8 8 0 0 1 0 7"/>',
  bullet: '<path d="M3 9.5 15 7v8L3 12.5z"/><path d="M15 9h3.5l2.5-1.5v7L18.5 13H15"/><path d="M8 13.5V19H5M5 17v4"/>',
  dome: '<path d="M4 9h16"/><path d="M5 9a7 7 0 0 0 14 0"/><circle cx="12" cy="12" r="2.5"/><path d="M7 5h10l1 4H6z"/>',
  turret: '<rect x="6" y="3" width="12" height="4" rx="1"/><circle cx="12" cy="13" r="6"/><circle cx="12" cy="13" r="2.5"/><path d="M12 7v0"/>',
  doorbell: '<rect x="7" y="2.5" width="10" height="19" rx="5"/><circle cx="12" cy="8" r="2.2"/><circle cx="12" cy="16" r="1.8"/>',
  ptz: '<path d="M6 4h12"/><path d="M12 4v3"/><circle cx="12" cy="13.5" r="6.5"/><circle cx="12" cy="13.5" r="2.5"/><path d="M5.5 13.5h2M16.5 13.5h2"/>',
  camera: '<rect x="3" y="7" width="13" height="10" rx="2"/><path d="m16 11 5-3v8l-5-3"/><circle cx="9.5" cy="12" r="2.5"/>',
  nvr: '<rect x="2.5" y="6" width="19" height="12" rx="2"/><path d="M5.5 10h6M5.5 13h6"/><circle cx="16.5" cy="11.5" r="2.5"/>',
  reader: '<rect x="7" y="2.5" width="10" height="19" rx="2"/><path d="M10 8.5a3 3 0 0 1 4 0M9 6.5a5 5 0 0 1 6 0"/><circle cx="12" cy="15" r="2"/>',
  door: '<path d="M5 21V3h11v18"/><path d="M3 21h16"/><circle cx="13" cy="12" r=".9"/><path d="M19 9v6"/>',
  phone: '<rect x="7" y="2.5" width="10" height="19" rx="2.2"/><path d="M11 18.5h2"/>',
  tablet: '<rect x="4.5" y="2.5" width="15" height="19" rx="2"/><path d="M11 18.5h2"/>',
  laptop: '<rect x="5" y="5" width="14" height="10" rx="1.2"/><path d="M2.5 18.5h19l-1.5-3.5H4z"/>',
  desktop: '<rect x="3" y="4" width="18" height="12" rx="1.5"/><path d="M9 20h6M12 16v4"/>',
  tv: '<rect x="2.5" y="5" width="19" height="12" rx="1.5"/><path d="M8 20h8"/><path d="M9 2.5 12 5l3-2.5" opacity=".6"/>',
  speaker: '<rect x="6" y="2.5" width="12" height="19" rx="3"/><circle cx="12" cy="14.5" r="3.5"/><circle cx="12" cy="7" r="1.3"/>',
  console: '<path d="M7 8h10a4 4 0 0 1 4 4l.5 4a2 2 0 0 1-3.6 1.4L16 15H8l-1.9 2.4A2 2 0 0 1 2.5 16L3 12a4 4 0 0 1 4-4z"/><path d="M7 11v3M5.5 12.5h3"/><circle cx="16" cy="11.5" r=".8"/><circle cx="17.8" cy="13.2" r=".8"/>',
  watch: '<rect x="7" y="6.5" width="10" height="11" rx="2.5"/><path d="M9 6.5 9.6 3h4.8l.6 3.5M9 17.5l.6 3.5h4.8l.6-3.5"/>',
  printer: '<path d="M7 8V3h10v5"/><rect x="3" y="8" width="18" height="9" rx="1.5"/><path d="M7 14h10v7H7z"/>',
  nas: '<rect x="5" y="2.5" width="14" height="19" rx="1.5"/><path d="M8 7h8M8 10.5h8M8 14h8"/><circle cx="12" cy="18" r=".9"/>',
  server: '<rect x="3.5" y="3.5" width="17" height="7" rx="1.5"/><rect x="3.5" y="13.5" width="17" height="7" rx="1.5"/><circle cx="7" cy="7" r=".9"/><circle cx="7" cy="17" r=".9"/><path d="M11 7h6M11 17h6"/>',
  plug: '<rect x="5" y="4" width="14" height="16" rx="4"/><path d="M10 9v2.5M14 9v2.5"/><path d="M12 15v1.5"/>',
  bulb: '<path d="M9 18h6M10 21h4"/><path d="M12 3a6 6 0 0 0-3.5 10.9c.6.5 1 1.2 1 2V16h5v-.1c0-.8.4-1.5 1-2A6 6 0 0 0 12 3z"/>',
  thermostat: '<circle cx="12" cy="12" r="8.5"/><circle cx="12" cy="12" r="5"/><path d="M12 9.5v2.5l1.5 1"/>',
  vacuum: '<circle cx="12" cy="12" r="8.5"/><circle cx="12" cy="12" r="3"/><path d="M5 9.5h3M16 9.5h3"/>',
  iot: '<rect x="6" y="6" width="12" height="12" rx="2"/><rect x="9.5" y="9.5" width="5" height="5" rx=".8"/><path d="M9 3v3M15 3v3M9 18v3M15 18v3M3 9h3M3 15h3M18 9h3M18 15h3"/>',
  router: '<rect x="3" y="12" width="18" height="7" rx="2"/><path d="M7 12 6 5M17 12l1-7"/><circle cx="7.5" cy="15.5" r=".9"/><path d="M11 15.5h6"/>',
  car: '<path d="M5 16V12l2-5h10l2 5v4"/><path d="M3.5 12h17v4.5h-17z"/><circle cx="7.5" cy="16.5" r="1.5"/><circle cx="16.5" cy="16.5" r="1.5"/>',
  ups: '<rect x="5" y="3" width="14" height="18" rx="2"/><path d="M12.8 7 10 12.2h4L11.2 17"/>',
  unknown: '<rect x="3.5" y="3.5" width="17" height="17" rx="4"/><path d="M9.6 9.4a2.5 2.5 0 1 1 3.3 2.4c-.6.2-.9.8-.9 1.4v.6"/><circle cx="12" cy="16.6" r=".8"/>',
};

const ICON_LABEL = {
  gateway: "Gateway", switch: "Switch", switchmini: "Small switch", ap: "Access point", apwall: "In-wall access point", apoutdoor: "Outdoor access point",
  bullet: "Bullet camera", dome: "Dome camera", turret: "Turret camera", doorbell: "Doorbell", ptz: "PTZ camera", camera: "Camera", nvr: "Video recorder",
  reader: "Door reader", door: "Door", phone: "Phone", tablet: "Tablet", laptop: "Laptop", desktop: "Computer", tv: "TV / streaming", speaker: "Speaker",
  console: "Game console", watch: "Watch", printer: "Printer", nas: "NAS / storage", server: "Server", plug: "Smart plug", bulb: "Light", thermostat: "Thermostat",
  vacuum: "Robot vacuum", ups: "UPS / power", iot: "Smart device", router: "Router", car: "Car", unknown: "Unknown device",
};

function iconSvg(kind, size = 20) {
  return `<svg class="dicon" viewBox="0 0 24 24" width="${size}" height="${size}" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${ICON_PATHS[kind] || ICON_PATHS.unknown}</svg>`;
}

/** Icon for a UniFi device (from the Integration or classic API). */
function unifiKind(d) {
  const t = String(d?.type || "").toLowerCase();
  const m = [d?.model, d?.modelName, d?.model_name, d?.shortname, d?.name].filter(Boolean).join(" ").toLowerCase();
  if (/\bups\b|upstower|ups-|usp-?pdu|pdu|\busp\b/.test(m) || t === "usp") return "ups";
  if (/doorbell/.test(m)) return "doorbell";
  if (/ptz/.test(m)) return "ptz";
  if (/bullet/.test(m)) return "bullet";
  if (/turret/.test(m)) return "turret";
  if (/dome|flex cam|g4 instant|g5 flex/.test(m)) return "dome";
  if (/\b(uvc|g3|g4|g5|g6|ai pro|ai 360|camera)\b/.test(m)) return "camera";
  if (/unvr|nvr|cloud key|\bck\b|ckg2/.test(m)) return "nvr";
  if (/\bua-|access reader|ua g2|reader|intercom|access hub/.test(m)) return "reader";
  if (["ugw", "udm", "uxg", "ucg"].includes(t) || /gateway|\budm|\bucg|\buxg|\budr|dream (machine|router|wall)|express|\bux\b/.test(m)) return "gateway";
  if (t === "usw" || /\busw|switch|flex mini|usmini|us-?8|us-?16|us-?24|us-?48/.test(m)) return /mini|flex|lite 8|usw-?flex|ultra|\bus-?8/.test(m) ? "switchmini" : "switch";
  if (/\biw\b|in-wall|inwall|\bu6-?iw|\bu7-?iw|uap-?iw/.test(m)) return "apwall";
  if (/mesh|outdoor|\bm\b|u6-?mesh|u7-?outdoor|ac-?m|uap-?ac-?m/.test(m)) return "apoutdoor";
  if (t === "uap" || /\bu6|\bu7|\buap|nanohd|flexhd|beaconhd|access point|\bac-|\bap\b/.test(m)) return "ap";
  const role = String(d?.role || "");
  if (role === "gw") return "gateway";
  if (role === "sw") return "switch";
  if (role === "ap") return "ap";
  return t ? "router" : "unknown";
}

const CLIENT_RULES = [
  [/doorbell/, "doorbell"],
  [/iphone|android|pixel|galaxy-?s\d|galaxy-?a\d|galaxy-?z|\bsm-[gsanf]|oneplus|redmi|poco|huawei-?p|nokia|motorola|moto-g/, "phone"],
  [/ipad|\btab\b|galaxy-?tab|kindle|fire-?hd|lenovo-?tab|tablet/, "tablet"],
  [/macbook|laptop|thinkpad|surface|notebook|chromebook|xps|zenbook|vivobook|ideapad/, "laptop"],
  [/imac|mac-?mini|macmini|mac-?studio|mac-?pro|desktop|workstation|-pc\b|\bpc-|gaming-?pc/, "desktop"],
  [/apple-?tv|appletv|roku|chromecast|google-?tv|shield|fire-?tv|firetv|firestick|bravia|webos|lgwebos|tizen|samsung-?tv|android-?tv|\btv\b|hisense|tcl|vizio/, "tv"],
  [/sonos|homepod|echo|alexa|nest-?audio|nest-?mini|google-?home|speaker|bose|denon|yamaha|marantz|bluesound/, "speaker"],
  [/playstation|\bps[345]\b|xbox|nintendo|switch-?oled|steam-?deck/, "console"],
  [/watch/, "watch"],
  [/printer|epson|brother|\bhp[a-f0-9]{6}|laserjet|officejet|deskjet|canon|mfc-|ecotank/, "printer"],
  [/synology|qnap|diskstation|truenas|\bnas\b|unraid/, "nas"],
  [/raspberry|raspberrypi|proxmox|ubuntu|debian|server|docker|home-?assistant|homeassistant|\bhass\b|esxi/, "server"],
  [/plug|shelly|sonoff|tapo-?p|kasa|meross|smartlife|switchbot|socket/, "plug"],
  [/hue|bulb|lifx|\bwiz\b|nanoleaf|light|lamp|govee/, "bulb"],
  [/ecobee|thermostat|tado|nest-?thermo|honeywell|sensibo|\bac\b|aircon/, "thermostat"],
  [/roomba|roborock|vacuum|ecovacs|deebot|irobot|dreame/, "vacuum"],
  [/ring|wyze|arlo|blink|camera|\bcam\b|-cam|cam-|reolink|hikvision|eufy|ezviz|imou|tapo-?c|dahua/, "camera"],
  [/tesla|car\b|vehicle/, "car"],
  [/espressif|tuya|esp32|esp8266|esp-|\biot\b|smart/, "iot"],
];

/** Icon kind for a client (phone, laptop, TV…), guessed from its name, hostname, maker and OS fingerprint. */
function clientKind(c) {
  if (!c) return "unknown";
  const chosen = c.mac && typeof S !== "undefined" && S.cliTypes?.[String(c.mac).toLowerCase()]; // set by an admin (clienttypes.go)
  if (chosen && ICON_LABEL[chosen]) return chosen;
  if (c.icon) return c.icon;
  const name = [c.name, c.label, c.hostname, c.device, c.model, c.device_model].filter(Boolean).join(" ").toLowerCase();
  const extra = [c.os_name, c.dev_family, c.dev_vendor, c.oui, c.vendor].filter(Boolean).join(" ").toLowerCase();
  for (const [re, kind] of CLIENT_RULES) if (re.test(name)) return kind;
  for (const [re, kind] of CLIENT_RULES) if (re.test(extra)) return kind;
  if (/apple/.test(extra)) return "phone";          // most unnamed Apple clients are phones
  if (/samsung|xiaomi|oppo|vivo|huawei|google/.test(extra)) return "phone";
  if (c.wired || c.is_wired) return "desktop";
  return "unknown";
}

function devIcon(d, size) { const k = unifiKind(d); return `<span class="dic" title="${ICON_LABEL[k]}">${iconSvg(k, size)}</span>`; }
function cliIcon(c, size) { const k = clientKind(c); return `<span class="dic" title="${ICON_LABEL[k]}">${iconSvg(k, size)}</span>`; }
