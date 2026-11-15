/* HomeOwl — Smart › Tuya: icons for home devices (original 24×24 line icons, currentColor) and the house
   backgrounds. Depends on icons.js (a few shared shapes). */
"use strict";

const TUYA_ICONS = {
  wallswitch: ["Wall switch", '<rect x="5" y="3" width="14" height="18" rx="2.5"/><rect x="9" y="7" width="6" height="10" rx="1.5"/><path d="M9 12h6"/>'],
  bulb: ["Light", ICON_PATHS.bulb],
  lamp: ["Lamp", '<path d="M8 3h8l3 7H5z"/><path d="M12 10v10M8 21h8"/>'],
  pendant: ["Ceiling light", '<path d="M12 2v5"/><path d="M5 15a7 7 0 0 1 14 0z"/><path d="M10 18a2 2 0 0 0 4 0"/>'],
  lantern: ["Outdoor light", '<path d="M9 4h6l2 3H7z"/><rect x="7.5" y="7" width="9" height="11" rx="1"/><path d="M12 10v5M10 21h4M12 18v3"/>'],
  strip: ["Light strip", '<path d="M3 15c3-6 6 6 9 0s6 6 9 0"/><circle cx="6" cy="12.5" r=".8"/><circle cx="12" cy="15" r=".8"/><circle cx="18" cy="12.5" r=".8"/>'],
  plug: ["Plug", ICON_PATHS.plug],
  powerstrip: ["Power strip", '<rect x="2.5" y="8" width="17" height="8" rx="2"/><path d="M6 11v2M9.5 11v2M13 11v2M16.5 11v2"/><path d="M19.5 12h2"/>'],
  boiler: ["Water heater", '<rect x="6" y="2.5" width="12" height="18" rx="4"/><path d="M12 9c1.7 1.6 2.2 2.8 2.2 3.9a2.2 2.2 0 0 1-4.4 0c0-.9.4-1.6 1-2.2.2.7.6 1 1.2 1"/><path d="M9 20.5v1.5M15 20.5v1.5"/>'],
  heater: ["Heater", '<rect x="3" y="6" width="18" height="12" rx="2"/><path d="M7.5 6v12M12 6v12M16.5 6v12" opacity=".7"/><path d="M5.5 18v2M18.5 18v2"/>'],
  ac: ["Air conditioner", '<rect x="2.5" y="5" width="19" height="8" rx="2"/><path d="M6 10h12"/><path d="M8 16v3M12 16v5M16 16v3"/>'],
  fan: ["Fan", '<circle cx="12" cy="12" r="1.6"/><path d="M12 10.4c0-4 1-7 4-7s1.5 4-2.5 7.5"/><path d="M13.4 12.8c3.5 2 5.5 4.5 4 7s-4-.5-5-5"/><path d="M10.6 12.6c-3.5 2-6.5 2.5-7.5 0s3.5-2.8 7-1.6"/>'],
  thermostat: ["Thermostat", ICON_PATHS.thermostat],
  temp: ["Temperature sensor", '<path d="M10 4a2 2 0 0 1 4 0v9.5a4 4 0 1 1-4 0z"/><path d="M12 9v7"/>'],
  garage: ["Garage door", '<path d="M3 10 12 4l9 6v10H3z"/><path d="M7 20v-7h10v7M7 15.5h10M7 18h10"/>'],
  gate: ["Gate", '<path d="M3 20V7M21 20V7"/><path d="M3 9h18M3 15h18"/><path d="M7.5 9v6M12 9v6M16.5 9v6" opacity=".7"/>'],
  curtain: ["Curtains", '<path d="M3 3h18"/><path d="M5 3v18c2-3 3-8 3-18M19 3v18c-2-3-3-8-3-18"/>'],
  jacuzzi: ["Jacuzzi / pool", '<path d="M3 13h18v3a4 4 0 0 1-4 4H7a4 4 0 0 1-4-4z"/><path d="M8 10V9a2 2 0 0 1 4 0"/><circle cx="15" cy="8" r="1"/><circle cx="17.5" cy="5" r=".8"/>'],
  garden: ["Garden / irrigation", '<path d="M12 21v-7"/><path d="M12 14c0-3 2-5 5-5 0 3-2 5-5 5zM12 16c0-2.5-1.7-4-4-4 0 2.3 1.7 4 4 4z"/><path d="M6 21h12"/>'],
  water: ["Water / pump", '<path d="M12 3c3 4 5 6.5 5 9a5 5 0 0 1-10 0c0-2.5 2-5 5-9z"/>'],
  lock: ["Lock", '<rect x="5" y="10.5" width="14" height="10" rx="2"/><path d="M8 10.5V8a4 4 0 0 1 8 0v2.5"/><circle cx="12" cy="15.5" r="1.2"/>'],
  contact: ["Door sensor", '<rect x="4" y="5" width="6" height="14" rx="1.5"/><rect x="13" y="7" width="4" height="10" rx="1.2"/><path d="M20 9v6"/>'],
  motion: ["Motion sensor", '<circle cx="12" cy="13" r="2.5"/><path d="M8 9a6 6 0 0 0 0 8M16 9a6 6 0 0 1 0 8M5.2 6.2a10 10 0 0 0 0 13.6M18.8 6.2a10 10 0 0 1 0 13.6"/>'],
  alarm: ["Smoke / gas alarm", '<circle cx="12" cy="12" r="8.5"/><circle cx="12" cy="12" r="3"/><path d="M12 3.5v2M12 18.5v2M3.5 12h2M18.5 12h2"/>'],
  camera: ["Camera", ICON_PATHS.camera],
  tv: ["TV", ICON_PATHS.tv],
  speaker: ["Speaker", ICON_PATHS.speaker],
  vacuum: ["Robot vacuum", ICON_PATHS.vacuum],
  hub: ["Hub / other", ICON_PATHS.iot],
};

// a fitting icon when none was picked: its name first (a switch named "Boiler" is the boiler), then its Tuya category
const TUYA_NAME_ICONS = [[/boiler|water ?heater|dud|geyser/i, "boiler"], [/jacuzzi|spa\b|hot ?tub|pool/i, "jacuzzi"], [/garage/i, "garage"],
  [/\bgate\b/i, "gate"], [/sprinkler|irrigation|garden/i, "garden"], [/pump/i, "water"], [/curtain|blind|shutter/i, "curtain"],
  [/\bac\b|air ?con|a\/c/i, "ac"], [/\bfan\b/i, "fan"], [/heater|radiator/i, "heater"], [/parking|outdoor|porch|garden light|yard|exterior/i, "lantern"],
  [/strip|led tape/i, "strip"], [/lamp/i, "lamp"], [/ceiling|chandelier|pendant/i, "pendant"], [/light|lights|bulb/i, "bulb"], [/\btv\b/i, "tv"]];
const TUYA_CAT_ICONS = { kg: "wallswitch", tdq: "wallswitch", cz: "plug", pc: "powerstrip", dj: "bulb", dd: "strip", xdd: "pendant", fwd: "lantern", dc: "strip",
  wsdcg: "temp", mcs: "contact", pir: "motion", ywbj: "alarm", rqbj: "alarm", sj: "water", cl: "curtain", ckmkzq: "garage", wk: "thermostat", kt: "ac",
  fs: "fan", qn: "heater", sp: "camera", ms: "lock", sd: "vacuum", wg2: "hub", zndb: "powerstrip", dlq: "wallswitch", rs: "boiler" };
function tuyaIconOf(x) {
  if (x.icon && TUYA_ICONS[x.icon]) return x.icon;
  for (const [re, k] of TUYA_NAME_ICONS) if (re.test(x.name || "")) return k;
  return TUYA_CAT_ICONS[x.category] || "hub";
}
function tuyaSvg(k, size = 22) {
  return `<svg class="dicon" viewBox="0 0 24 24" width="${size}" height="${size}" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${(TUYA_ICONS[k] || TUYA_ICONS.hub)[1]}</svg>`;
}

// house backgrounds: presets (style.css .look-*) or the house's own photo
const TUYA_LOOKS = { dusk: "Dusk", sea: "Sea", pine: "Pine", sand: "Sand", night: "Night", rose: "Rose", sky: "Sky", slate: "Slate" };
function tuyaLookOf(h) {
  if (h.look === "photo" && h.photo) return { cls: "look-photo", style: `background-image:url('/api/tuya/bg?home=${encodeURIComponent(h.id)}&v=${h.photo}')` };
  if (h.look && TUYA_LOOKS[h.look]) return { cls: `look-${h.look}`, style: "" };
  const keys = Object.keys(TUYA_LOOKS); let n = 0; for (const c of String(h.id)) n = (n * 31 + c.charCodeAt(0)) >>> 0;
  return { cls: `look-${keys[n % keys.length]}`, style: "" }; // each house its own colour until one is picked
}

// a photo, shrunk in the browser to a banner (≤1600×900, JPEG) so it fits the upload limit
async function tuyaShrink(file) {
  const url = URL.createObjectURL(file);
  try {
    const img = await new Promise((ok, bad) => { const i = new Image(); i.onload = () => ok(i); i.onerror = () => bad(new Error("That isn't a picture HomeOwl can read")); i.src = url; });
    for (const [w, q] of [[1600, .8], [1280, .72], [960, .65]]) {
      const scale = Math.min(1, w / img.naturalWidth), c = document.createElement("canvas");
      c.width = Math.round(img.naturalWidth * scale); c.height = Math.round(Math.min(img.naturalHeight * scale, w * 9 / 16 * 1.6));
      const sy = (img.naturalHeight * scale - c.height) / 2;
      c.getContext("2d").drawImage(img, 0, -sy, c.width, img.naturalHeight * scale);
      const d = c.toDataURL("image/jpeg", q);
      if (d.length * .75 < 380 * 1024) return d;
    }
    throw new Error("That picture is too big even when shrunk");
  } finally { URL.revokeObjectURL(url); }
}
