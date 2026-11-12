/* HomeOwl — core: utilities, data model, analysis rules, charts, tables, diagrams. */
"use strict";

// ------------------------------------------------------------------ utilities
const $ = (s, el = document) => el.querySelector(s);
const $$ = (s, el = document) => [...el.querySelectorAll(s)];
const arr = x => Array.isArray(x) ? x : [];
const esc = s => String(s ?? "").replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
const num = x => (x === null || x === undefined || x === "" || isNaN(+x)) ? null : +x;
const secretLen = v => v && typeof v === "object" && v.__redacted__ ? v.length : (typeof v === "string" ? v.length : null);
const plural = (n, w, p) => `${n} ${n === 1 ? w : (p || w + "s")}`;
function fmtBytes(b) { b = num(b); if (b === null) return "–"; const u = ["B", "KB", "MB", "GB", "TB", "PB"]; let i = 0; while (b >= 1024 && i < u.length - 1) { b /= 1024; i++; } return `${b.toFixed(b >= 100 || i === 0 ? 0 : 1)} ${u[i]}`; }
function fmtDur(s) { s = num(s); if (s === null) return "–"; const d = Math.floor(s / 86400), h = Math.floor(s % 86400 / 3600), m = Math.floor(s % 3600 / 60); return d ? `${d}d ${h}h` : h ? `${h}h ${m}m` : `${m}m`; }
function fmtMbps(v) { v = num(v); return v === null ? "–" : `${v >= 100 ? v.toFixed(0) : v.toFixed(1)} Mbps`; }
function fmtSpeed(mbps) { mbps = num(mbps); return !mbps ? "–" : mbps >= 1000 ? `${mbps / 1000} G` : `${mbps} M`; }
const toMs = t => { t = num(t); return t === null ? null : t < 1e12 ? t * 1000 : t; };
function ago(t) { const ms = toMs(t); if (!ms) return "–"; const s = (Date.now() - ms) / 1000; return s < 0 ? "just now" : s < 90 ? `${Math.round(s)}s ago` : `${fmtDur(s)} ago`; }
const fmtTime = t => { const ms = toMs(t); return ms ? new Date(ms).toLocaleString([], { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }) : "–"; };
const bandOf = r => ({ ng: "2.4", na: "5", "6e": "6", "2g": "2.4", "5g": "5", "6g": "6" }[r] || (r ? String(r) : null));
const bandLabel = b => b ? `${b} GHz` : "–";
function ipToInt(ip) { const p = String(ip).split(".").map(Number); return p.length === 4 && p.every(x => x >= 0 && x < 256) ? ((p[0] << 24) >>> 0) + (p[1] << 16) + (p[2] << 8) + p[3] : null; }
function cidr(c) { if (!c) return null; const [ip, bits] = String(c).split("/"); const i = ipToInt(ip), b = +bits; if (i === null || isNaN(b)) return null; const mask = b === 0 ? 0 : (~0 << (32 - b)) >>> 0; const net = (i & mask) >>> 0; return { ip, bits: b, net, bcast: (net | (~mask >>> 0)) >>> 0, size: 2 ** (32 - b) }; }
const intToIp = i => [i >>> 24, (i >> 16) & 255, (i >> 8) & 255, i & 255].join(".");
const inSubnet = (ip, c) => { const i = ipToInt(ip), n = cidr(c); return i !== null && n && i >= n.net && i <= n.bcast; };
const DEV_STATE = { 0: "Disconnected", 1: "Connected", 2: "Pending adoption", 4: "Upgrading", 5: "Provisioning", 6: "Heartbeat missed", 7: "Adopting", 9: "Adoption error", 10: "Adoption failed", 11: "Isolated" };
const DPI_CAT = { 0: "Instant messaging", 1: "P2P", 3: "File transfer", 4: "Streaming media", 5: "Mail & collaboration", 6: "VoIP", 7: "Database", 8: "Games", 9: "Network management", 10: "Remote access", 11: "Proxy / VPN bypass", 12: "Stock market", 13: "Web", 14: "Security updates", 15: "Web IM", 17: "Business", 18: "Network protocols", 19: "Network protocols", 20: "Network protocols", 23: "Private protocols", 24: "Social networks", 255: "Unknown" };

// DNS provider recognition
function dnsProvider(ip) {
  const s = String(ip || "").toLowerCase();
  if (!s) return { name: "Gateway", kind: "gateway" };
  if (/^45\.90\.(28|30)\./.test(s) || /^2a07:a8c[01]:/.test(s)) return { name: "NextDNS (linked IP / endpoint)", kind: "nextdns" };
  if (/^(1\.1\.1\.[13]|1\.0\.0\.[13]|2606:4700:4700::)/.test(s)) return { name: "Cloudflare", kind: "public" };
  if (/^(8\.8\.8\.8|8\.8\.4\.4|2001:4860:4860::)/.test(s)) return { name: "Google", kind: "public" };
  if (/^(9\.9\.9\.\d+|149\.112\.112\.\d+|2620:fe::)/.test(s)) return { name: "Quad9", kind: "public" };
  if (/^94\.140\.1[45]\./.test(s)) return { name: "AdGuard DNS", kind: "public" };
  if (/^(10\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.)/.test(s)) return { name: "Local resolver (Pi-hole / AdGuard Home / other)", kind: "local" };
  if (/^100\.(6[4-9]|[7-9]\d|1[01]\d|12[0-7])\./.test(s)) return { name: "Tailscale address", kind: "tailscale" };
  return { name: "Custom", kind: "custom" };
}

// ------------------------------------------------------------------ state
const S = { bundle: null, model: null, findings: [], view: "today", source: "live", sevFilter: "all", areaFilter: "all", token: null, write: false, readOnly: false, clientTab: "active" };

// ------------------------------------------------------------------ normalise
function collectArrays(o) { if (Array.isArray(o)) return o; if (o && typeof o === "object") return Object.values(o).filter(Array.isArray).flat(); return []; }

function normalise(b) {
  const L = b.legacy || {}, I = b.integration || {}, V = b.v2 || {};
  const settings = Object.fromEntries(arr(L.settings).map(s => [s.key, s]));
  const sys = arr(L.sysinfo)[0] || {};
  const health = Object.fromEntries(arr(L.health).map(h => [h.subsystem, h]));
  const portProfiles = Object.fromEntries(arr(L.port_profiles).map(p => [p._id, p.name]));

  // ---- devices
  let devices = [];
  const mapPorts = d => arr(d.port_table).map((p, i) => ({
    idx: num(p.port_idx) ?? i + 1, name: p.name, up: !!p.up, speed: num(p.speed), fdx: p.full_duplex !== false,
    poeCap: !!(p.port_poe || p.poe_caps), poeOn: p.poe_enable ?? (p.poe_mode ? p.poe_mode !== "off" : false), poeMode: p.poe_mode, poeW: num(p.poe_power), poeClass: p.poe_class,
    rxErr: num(p.rx_errors) || 0, txErr: num(p.tx_errors) || 0, rxDrop: num(p.rx_dropped) || 0, rx: num(p.rx_bytes), tx: num(p.tx_bytes), rxPkts: num(p.rx_packets) || 0,
    profile: portProfiles[p.portconf_id] || (p.portconf_id ? "custom" : "default"), isUplink: !!p.is_uplink, media: p.media, sfp: !!p.sfp_found, stp: p.stp_state,
  }));
  if (arr(L.devices).length) {
    devices = arr(L.devices).map(d => {
      const statsBy = Object.fromEntries(arr(d.radio_table_stats).map(r => [r.name, r]));
      const radios = arr(d.radio_table).map(r => { const st = statsBy[r.name] || {}; return { name: r.name, radio: r.radio, band: bandOf(r.radio), channel: num(st.channel ?? r.channel), width: num(r.ht), txMode: r.tx_power_mode, txPower: num(st.tx_power), util: num(st.cu_total), clients: num(st.num_sta), minRssi: !!r.min_rssi_enabled }; });
      const role = ["udm", "ugw", "uxg"].includes(d.type) ? "gw" : d.type === "usw" ? "sw" : d.type === "uap" ? "ap" : "other";
      const ports = mapPorts(d);
      return {
        id: d._id || d.mac, mac: (d.mac || "").toLowerCase(), name: d.name || d.model_name || d.model || d.mac, model: d.model_name || d.model, modelCode: d.model, type: d.type,
        role, ip: d.ip, version: d.displayable_version || d.version, upgradable: !!d.upgradable, upgradeTo: d.upgrade_to_firmware,
        state: num(d.state), stateLabel: DEV_STATE[d.state] ?? `State ${d.state}`, online: d.state === 1, adopted: d.adopted !== false,
        uptime: num(d.uptime), lastSeen: num(d.last_seen), uplinkMac: (d.uplink?.uplink_mac || "").toLowerCase() || null, uplinkPort: num(d.uplink?.uplink_remote_port),
        uplinkSpeed: num(d.uplink?.speed), wireless: d.uplink?.type === "wireless", clients: num(d.num_sta) ?? 0,
        cpu: num(d["system-stats"]?.cpu), mem: num(d["system-stats"]?.mem), satisfaction: num(d.satisfaction),
        informUrl: d.inform_url, informIp: d.inform_ip, connectIp: d.connect_request_ip, disconnectReason: d.disconnection_reason || d.disconnect_reason,
        temps: arr(d.temperatures).map(t => `${t.name}: ${t.value}°C`).join(", ") || (d.general_temperature ? `${d.general_temperature}°C` : null),
        poeMax: num(d.total_max_power), radios, ports, portsUp: ports.filter(p => p.up).length, poeW: ports.reduce((a, p) => a + (p.poeW || 0), 0),
        sources: { "stat/device": d },
      };
    });
  } else if (arr(I.devices).length) {
    devices = arr(I.devices).map(d => {
      const st = (I.device_stats || {})[d.id] || {};
      const feats = arr(d.features).map(String);
      const role = /^(UDM|UCG|UXG|UDR|UX|EFG|UDW)/i.test(d.model || "") ? "gw" : feats.includes("accessPoint") ? "ap" : feats.includes("switching") ? "sw" : "other";
      const s = String(d.state || "").toUpperCase();
      return {
        id: d.id, mac: (d.macAddress || "").toLowerCase(), name: d.name || d.model, model: d.model, modelCode: d.model, role, ip: d.ipAddress,
        version: d.firmwareVersion, upgradable: !!d.firmwareUpdatable, online: s === "ONLINE", state: s === "ONLINE" ? 1 : null, stateLabel: s || "unknown",
        uptime: num(st.uptimeSec), uplinkId: st.uplink?.deviceId || d.uplink?.deviceId || null, clients: 0, cpu: num(st.cpuUtilizationPct), mem: num(st.memoryUtilizationPct),
        radios: arr(st.interfaces?.radios).map(r => ({ band: r.frequencyGHz ? String(r.frequencyGHz).replace(/\.0$/, "") : null, channel: num(r.channel), width: num(r.channelWidthMHz), util: null })),
        ports: [], portsUp: 0, poeW: 0, sources: { integration: d },
      };
    });
  }
  // merge extra sources (devices that don't show up fully, e.g. a UPS stuck "getting ready")
  const byMac = Object.fromEntries(devices.map(d => [d.mac, d]));
  const extra = [
    ["stat/device-basic", arr(L.devices_basic)], ["v2/device", collectArrays(V.devices)],
    ["integration", arr(I.devices)], ["integration/detail", Object.values(I.device_detail || {})],
  ];
  for (const [src, list] of extra) for (const x of list) {
    const mac = String(x.mac || x.macAddress || "").toLowerCase(); if (!mac) continue;
    let d = byMac[mac];
    if (!d) {
      const s = x.state ?? x.status;
      const sNum = typeof s === "number" ? s : null, sStr = typeof s === "string" ? s.toUpperCase() : null;
      d = byMac[mac] = {
        id: x._id || x.id || mac, mac, name: x.name || x.model_name || x.model || x.shortname || mac, model: x.model_name || x.model || x.shortname, modelCode: x.model, type: x.type,
        role: /ups/i.test(`${x.model} ${x.name} ${x.type}`) ? "ups" : x.type === "uap" ? "ap" : x.type === "usw" ? "sw" : "other", ip: x.ip || x.ipAddress, version: x.version || x.firmwareVersion,
        state: sNum, stateLabel: sNum !== null ? (DEV_STATE[sNum] ?? `State ${sNum}`) : (sStr || "Not in device list"), online: sNum === 1 || sStr === "ONLINE",
        adopted: x.adopted !== false, partial: true, clients: 0, radios: [], ports: [], portsUp: 0, poeW: 0, lastSeen: num(x.last_seen), sources: {},
      };
      devices.push(d);
    }
    d.sources[src] = x;
    if (src.startsWith("integration") && x.state) d.intState = String(x.state).toUpperCase();
  }
  devices.forEach(d => { d.isUps = /ups/i.test(`${d.model} ${d.modelCode} ${d.name} ${d.type}`); if (d.isUps && d.role === "other") d.role = "ups"; if (d.role === "other") d.role = homeRole(d); });

  // ---- clients
  let clients = [];
  if (arr(L.clients).length) {
    clients = arr(L.clients).map(c => ({
      mac: (c.mac || "").toLowerCase(), name: c.name || c.hostname || null, label: c.name || c.hostname || c.oui || c.mac, hostname: c.hostname, ip: c.ip,
      wired: !!c.is_wired, network: c.network, networkId: c.network_id, ssid: c.essid, band: c.is_wired ? null : bandOf(c.radio), proto: c.radio_proto,
      signal: num(c.signal), satisfaction: num(c.satisfaction), tx: num(c.tx_bytes), rx: num(c.rx_bytes), uptime: num(c.uptime),
      apMac: (c.ap_mac || "").toLowerCase(), swMac: (c.sw_mac || "").toLowerCase(), swPort: num(c.sw_port), vendor: c.oui, guest: !!c.is_guest, channel: num(c.channel),
      userId: c.user_id, blocked: !!c.blocked, fixedIp: c.use_fixedip ? c.fixed_ip : null, txRate: num(c.tx_rate), rxRate: num(c.rx_rate), firstSeen: num(c.first_seen), lastSeen: num(c.last_seen), online: true,
    }));
  } else if (arr(I.clients).length) {
    const now = Date.now();
    clients = arr(I.clients).map(c => ({ mac: (c.macAddress || "").toLowerCase(), name: c.name || null, label: c.name || c.macAddress, ip: c.ipAddress, wired: String(c.type).toUpperCase() === "WIRED", uplinkId: c.uplinkDeviceId, uptime: c.connectedAt ? (now - Date.parse(c.connectedAt)) / 1000 : null, online: true }));
  }
  devices.forEach(d => { if (!d.clients) d.clients = clients.filter(c => c.apMac === d.mac || c.uplinkId === d.id || (c.wired && c.swMac === d.mac)).length; });

  // ---- networks
  let networks = [], wans = [];
  if (arr(L.networks).length) {
    for (const n of arr(L.networks)) {
      if (n.purpose === "wan") { wans.push({ name: n.name, type: n.wan_type, group: n.wan_networkgroup, ip: n.wan_ip, lb: n.wan_load_balance_type, enabled: n.enabled !== false }); continue; }
      const dns = [n.dhcpd_dns_1, n.dhcpd_dns_2, n.dhcpd_dns_3, n.dhcpd_dns_4].filter(Boolean);
      networks.push({
        id: n._id, name: n.name, purpose: n.purpose, vlan: n.vlan_enabled || n.vlan ? num(n.vlan) : 1, subnet: n.ip_subnet, gwIp: n.ip_subnet ? n.ip_subnet.split("/")[0] : null,
        dhcp: !!n.dhcpd_enabled, dhcpStart: n.dhcpd_start, dhcpStop: n.dhcpd_stop, lease: num(n.dhcpd_leasetime), dns: n.dhcpd_dns_enabled ? dns : [], domain: n.domain_name,
        isolated: !!n.network_isolation_enabled, internet: n.internet_access_enabled !== false, mdns: !!n.mdns_enabled, igmp: !!n.igmp_snooping, enabled: n.enabled !== false,
        guest: n.purpose === "guest", vpn: /vpn/.test(n.purpose || ""), isDefault: n.attr_no_delete === true || n.attr_hidden_id === "LAN",
        filters: Object.entries(n).filter(([k, v]) => /content_filter|ad_?block/i.test(k) && v && !/^(none|disabled|off|false)$/i.test(String(v))).map(([k, v]) => `${k}=${typeof v === "object" ? JSON.stringify(v) : v}`),
      });
    }
  } else if (arr(I.networks).length) {
    for (const n of arr(I.networks)) {
      const v4 = n.ipv4Configuration || {};
      networks.push({
        id: n.id, name: n.name, purpose: n.management || n.purpose || "corporate", vlan: num(n.vlanId) ?? 1, subnet: v4.hostIpAddress && v4.prefixLength ? `${v4.hostIpAddress}/${v4.prefixLength}` : null, gwIp: v4.hostIpAddress,
        dhcp: /SERVER/i.test(v4.dhcpConfiguration?.mode || ""), dhcpStart: v4.dhcpConfiguration?.ipAddressRange?.start, dhcpStop: v4.dhcpConfiguration?.ipAddressRange?.stop,
        dns: arr(v4.dhcpConfiguration?.dnsServerIpAddressesOverride), isolated: !!n.isolationEnabled, internet: n.internetAccessEnabled !== false, enabled: n.enabled !== false,
        guest: /guest|hotspot/i.test(n.purpose || n.zone || ""), vpn: false, isDefault: !!n.default,
      });
    }
  }
  if (!wans.length && arr(I.wans).length) wans = arr(I.wans).map(w => ({ name: w.name, enabled: true }));
  const netById = Object.fromEntries(networks.map(n => [n.id, n]));
  const defaultNet = networks.find(n => n.isDefault) || networks.find(n => n.purpose === "corporate") || networks[0];
  const netForIp = ip => networks.find(n => n.subnet && inSubnet(ip, n.subnet));
  clients.forEach(c => { if (!c.networkId && c.ip) { const n = netForIp(c.ip); if (n) { c.networkId = n.id; c.network = n.name; } } if (c.networkId && !c.network) c.network = netById[c.networkId]?.name; });
  networks.forEach(n => { n.clients = clients.filter(c => c.networkId === n.id).length; });

  // ---- known clients (incl. offline)
  const active = new Map(clients.map(c => [c.mac, c]));
  const hist = Object.fromEntries(arr(V.clients_history).map(h => [String(h.mac || "").toLowerCase(), h]));
  let known = arr(L.known_clients).map(u => {
    const mac = (u.mac || "").toLowerCase(), h = hist[mac] || {};
    return { id: u._id, mac, name: u.name || null, label: u.name || u.hostname || h.display_name || u.oui || mac, hostname: u.hostname, vendor: u.oui, networkId: u.network_id, network: netById[u.network_id]?.name,
      fixedIp: u.use_fixedip ? u.fixed_ip : null, blocked: !!u.blocked, firstSeen: num(u.first_seen), lastSeen: num(active.get(mac)?.lastSeen ?? u.last_seen ?? h.last_seen), note: u.note, online: active.has(mac),
      ip: active.get(mac)?.ip || u.last_ip || h.last_ip || null, wired: u.is_wired ?? active.get(mac)?.wired };
  });
  if (!known.length) known = Object.values(hist).map(h => { const mac = String(h.mac).toLowerCase(); return { id: h.id, mac, name: h.name || null, label: h.display_name || h.name || h.hostname || mac, vendor: h.oui, lastSeen: num(h.last_seen), online: active.has(mac), blocked: !!h.blocked, ip: h.last_ip || active.get(mac)?.ip, fixedIp: h.use_fixedip ? h.fixed_ip : null }; });
  known.forEach(k => { const c = active.get(k.mac); if (c) { c.userId = c.userId || k.id; c.blocked = c.blocked || k.blocked; c.fixedIp = c.fixedIp || k.fixedIp; } });
  const knownByMac = Object.fromEntries(known.map(k => [k.mac, k]));

  // ---- wlans
  let wlans = [];
  const secLabelLegacy = w => {
    if (w.security === "open") return w.wpa3_enhanced_owe || w.owe ? "Enhanced Open" : "Open";
    if (w.security === "wep") return "WEP";
    if (w.security === "wpaeap") return w.wpa3_support ? "WPA3-Enterprise" : "WPA2-Enterprise";
    if (w.wpa3_support && !w.wpa3_transition) return "WPA3";
    if (w.wpa3_support && w.wpa3_transition) return "WPA2/WPA3";
    if (w.wpa_mode === "wpa1") return "WPA1";
    if (w.wpa_mode === "auto") return "WPA/WPA2";
    return "WPA2";
  };
  if (arr(L.wlans).length) {
    wlans = arr(L.wlans).map(w => ({
      id: w._id, name: w.name, enabled: w.enabled !== false, sec: secLabelLegacy(w), tkip: w.wpa_enc === "tkip" || w.wpa_enc === "auto", pmf: w.pmf_mode || "disabled",
      networkId: w.networkconf_id || defaultNet?.id, hidden: !!w.hide_ssid, guest: !!w.is_guest,
      bands: arr(w.wlan_bands).length ? w.wlan_bands : (w.wlan_band === "2g" ? ["2g"] : w.wlan_band === "5g" ? ["5g"] : ["2g", "5g"]),
      fastRoaming: !!w.fast_roaming_enabled, bss: !!w.bss_transition, l2iso: !!w.l2_isolation, macFilter: !!w.mac_filter_enabled,
      pskLen: secretLen(w.x_passphrase), ppsk: secretLen(w.private_preshared_keys) || 0,
      minrate: w.minrate_setting_preference === "manual" || !!w.minrate_ng_enabled || !!w.minrate_na_enabled, enterprise: w.security === "wpaeap", legacy: true,
    }));
  } else if (arr(I.wifi).length) {
    const map = { OPEN: "Open", WPA2_PERSONAL: "WPA2", WPA3_PERSONAL: "WPA3", WPA2_WPA3_PERSONAL: "WPA2/WPA3", WPA2_ENTERPRISE: "WPA2-Enterprise", WPA3_ENTERPRISE: "WPA3-Enterprise", WPA2_WPA3_ENTERPRISE: "WPA3-Enterprise" };
    wlans = arr(I.wifi).map(w => { const t = String(w.securityConfiguration?.type || "").toUpperCase(); return {
      id: w.id, name: w.name, enabled: w.enabled !== false, sec: map[t] || t || "?", pmf: w.securityConfiguration?.pmfMode?.toLowerCase?.() || null, networkId: w.network?.networkId || defaultNet?.id,
      hidden: !!w.hideName, guest: /guest|hotspot/i.test(w.network?.type || ""), bands: arr(w.broadcastingFrequenciesGHz).map(f => +f === 2.4 ? "2g" : +f === 5 ? "5g" : "6g"),
      l2iso: !!w.clientIsolationEnabled, fastRoaming: !!w.fastRoamingEnabled, bss: !!w.bssTransitionEnabled, pskLen: secretLen(w.securityConfiguration?.passphrase), enterprise: /ENTERPRISE/.test(t) }; });
  }
  wlans.forEach(w => { w.network = netById[w.networkId]?.name; w.clients = clients.filter(c => c.ssid === w.name).length; });

  // ---- firewall
  const zones = arr(V.firewall_zones).length ? arr(V.firewall_zones).map(z => ({ id: z._id, name: z.name, key: z.zone_key, networks: arr(z.network_ids) }))
    : arr(I.firewall_zones).map(z => ({ id: z.id, name: z.name, key: (z.metadata?.origin || z.name || "").toLowerCase(), networks: arr(z.networkIds) }));
  const zoneById = Object.fromEntries(zones.map(z => [z.id, z]));
  const rules = arr(L.firewall_rules).map(r => ({
    name: r.name, enabled: r.enabled !== false, action: r.action, ruleset: r.ruleset, index: num(r.rule_index), proto: r.protocol || "all",
    src: r.src_networkconf_id ? (netById[r.src_networkconf_id]?.name || "network") : r.src_address || (arr(r.src_firewallgroup_ids).length ? "group" : "any"),
    dst: r.dst_networkconf_id ? (netById[r.dst_networkconf_id]?.name || "network") : r.dst_address || (arr(r.dst_firewallgroup_ids).length ? "group" : "any"),
    dstPort: r.dst_port || "", log: !!r.logging, srcNet: r.src_networkconf_id, dstNet: r.dst_networkconf_id,
  }));
  let policies = arr(V.firewall_policies).map(p => ({
    id: p._id, name: p.name, enabled: p.enabled !== false, action: String(p.action || "").toUpperCase(), predefined: !!p.predefined, index: num(p.index),
    srcZone: zoneById[p.source?.zone_id]?.name || p.source?.zone_id, dstZone: zoneById[p.destination?.zone_id]?.name || p.destination?.zone_id,
    srcZoneKey: zoneById[p.source?.zone_id]?.key, dstZoneKey: zoneById[p.destination?.zone_id]?.key, dstPort: p.destination?.port || "", proto: p.protocol || "all", log: !!p.logging,
  }));
  if (!policies.length) policies = arr(I.firewall_policies).map(p => ({
    id: p.id, name: p.name, enabled: p.enabled !== false, action: String(p.action?.type || p.action || "").toUpperCase(), predefined: !!(p.metadata?.origin && p.metadata.origin !== "USER_DEFINED"),
    srcZone: zoneById[p.source?.zoneId]?.name, dstZone: zoneById[p.destination?.zoneId]?.name, srcZoneKey: zoneById[p.source?.zoneId]?.key, dstZoneKey: zoneById[p.destination?.zoneId]?.key, dstPort: "", proto: "", log: !!p.loggingEnabled,
  }));
  const portForwards = arr(L.port_forwards).map(p => ({
    id: p._id, name: p.name, enabled: p.enabled !== false, wan: p.pfwd_interface || "wan", src: p.src || "any",
    srcLimited: !!(p.src_limiting_enabled || (p.src && p.src !== "any") || p.src_firewallgroup_id), dstPort: String(p.dst_port || ""), fwd: p.fwd, fwdPort: String(p.fwd_port || p.dst_port || ""), proto: p.proto || "tcp_udp", log: !!p.log,
  }));

  const st = {
    ips: settings.ips?.ips_mode ?? (settings.ips ? (settings.ips.enabled ? "ips" : "disabled") : null), upnp: settings.usg?.upnp_enabled ?? settings.upnp?.enabled ?? null,
    ssh: settings.mgmt?.x_ssh_enabled ?? null, sshPw: settings.mgmt?.x_ssh_auth_password_enabled ?? null, autoUpgrade: settings.mgmt?.auto_upgrade ?? null,
    dpi: settings.dpi?.enabled ?? null, geoip: settings.usg?.geo_ip_filtering_enabled ?? settings.geo_ip_filtering?.enabled ?? null, doh: settings.doh?.state ?? null,
    teleport: settings.teleport?.enabled ?? null, country: settings.country?.code ?? null, ledOn: settings.mgmt?.led_enabled ?? null,
    dohRaw: settings.doh || null, dohNextdns: /nextdns/i.test(JSON.stringify(settings.doh || {})),
    adblock: Object.values(settings).some(s => Object.entries(s || {}).some(([k, v]) => /ad_?block/i.test(k) && v === true)),
    has: k => !!settings[k],
  };

  const w = health.wan || {}, www = health.www || {};
  const gw = devices.find(d => d.role === "gw");
  const info = {
    host: b.meta?.host, site: b.meta?.site?.name || b.meta?.site?.legacy || "default", collectedAt: b.meta?.collected_at,
    hostname: sys.hostname || sys.name, networkVersion: sys.version || I.info?.applicationVersion, consoleVersion: sys.console_display_version || sys.udm_version,
    updateAvailable: !!sys.update_available, timezone: sys.timezone, gwModel: gw?.model, gwName: gw?.name, gwIp: gw?.ip,
    wanIp: w.wan_ip, isp: w.isp_name || w.isp_organization, wanStatus: w.status, latency: num(www.latency), down: num(www.xput_down), up: num(www.xput_up),
    speedtestAt: www.speedtest_lastrun, drops: num(www.drops), wanUptime: num(www.uptime),
  };
  const wanLinks = buildWanLinks(gw?.sources?.["stat/device"] || {}, wans, w, www);
  const vpn = { remoteUser: networks.filter(n => /remote-user-vpn/.test(n.purpose)).length + arr(I.vpn_servers).length, siteToSite: networks.filter(n => /site-vpn/.test(n.purpose)).length + arr(I.vpn_tunnels).length };

  // ---- neighbours
  const apNameByMac = Object.fromEntries(devices.map(d => [d.mac, d.name]));
  const ownBssids = new Set(arr(L.devices).flatMap(d => arr(d.vap_table).map(v => String(v.bssid || "").toLowerCase())));
  const neighbors = arr(L.rogue_aps).filter(r => !ownBssids.has(String(r.bssid || "").toLowerCase())).map(r => ({
    ssid: r.essid || "(hidden)", bssid: r.bssid, band: bandOf(r.radio) || (num(r.channel) > 14 ? "5" : "2.4"), channel: num(r.channel), signal: num(r.signal ?? (r.rssi !== undefined ? r.rssi - 95 : null)),
    heardBy: apNameByMac[String(r.ap_mac || "").toLowerCase()] || r.ap_mac, security: r.security || (r.is_open ? "open" : ""), lastSeen: num(r.last_seen), oui: r.oui,
  }));

  // ---- history, events, IPS, DPI
  const rep = rows => arr(rows).map(r => ({ t: toMs(r.time), wanRx: num(r["wan-rx_bytes"]), wanTx: num(r["wan-tx_bytes"]), bytes: num(r.bytes), wlan: num(r.wlan_bytes),
    sta: num(r.num_sta) ?? ((num(r["lan-num_sta"]) || 0) + (num(r["wlan-num_sta"]) || 0)), cpu: num(r.cpu), mem: num(r.mem) })).filter(r => r.t).sort((a, b) => a.t - b.t);
  const history = { hourly: rep(L.hourly_site), daily: rep(L.daily_site), gw: rep(L.hourly_gw) };
  const events = [
    ...arr(L.events).map(e => ({ t: toMs(e.time), key: e.key, msg: e.msg, sub: e.subsystem, blob: JSON.stringify(e).toLowerCase(), src: "event" })),
    ...collectArrays(V.system_log).map(e => ({ t: toMs(e.timestamp || e.time), key: e.key || e.category, msg: e.message || e.title || e.msg || e.key, sub: e.category || e.severity, blob: JSON.stringify(e).toLowerCase(), src: "system-log" })),
  ].filter(e => e.t).sort((a, b) => b.t - a.t);
  const alarms = arr(L.alarms).map(a => ({ t: toMs(a.time), key: a.key, msg: a.msg, blob: JSON.stringify(a).toLowerCase() }));
  const ips = arr(L.ips_events).map(e => ({ t: toMs(e.timestamp || e.time), sig: e.inner_alert_signature || e.msg || e.signature, cat: e.inner_alert_category || e.catname || e.category, action: e.inner_alert_action || e.action,
    src: e.src_ip, dst: e.dest_ip || e.dst_ip, country: e.src_ip_country || e.srcipCountry || e.country, proto: e.proto })).sort((a, b) => b.t - a.t);
  const dpiCats = arr(arr(L.site_dpi)[0]?.by_cat).map(c => ({ cat: DPI_CAT[c.cat] || `Category ${c.cat}`, rx: num(c.rx_bytes) || 0, tx: num(c.tx_bytes) || 0 }));
  const clientDpi = Object.fromEntries(arr(L.client_dpi).map(x => [String(x.mac).toLowerCase(), arr(x.by_cat).map(c => ({ cat: DPI_CAT[c.cat] || `Category ${c.cat}`, rx: num(c.rx_bytes) || 0, tx: num(c.tx_bytes) || 0 }))]));

  const coverage = arr(b.meta?.log).reduce((a, l) => { (a[l.source] ||= { ok: 0, total: 0 }); a[l.source].total++; if (l.ok) a[l.source].ok++; return a; }, {});
  return {
    info, devices, clients, known, knownByMac, networks, wans, wanLinks, wlans, rules, policies, zones, portForwards, settings: st, vpn, coverage, neighbors, history, events, alarms, ips, dpiCats, clientDpi,
    routes: arr(L.routes), netById, defaultNet, netForIp, hasLegacy: !!(arr(L.networks).length || arr(L.devices).length),
    devByMac: Object.fromEntries(devices.map(d => [d.mac, d])),
  };
}

// ------------------------------------------------------------------ internet connections
// One entry per WAN port. The gateway reports wan1, wan2… plus geo_info / uptime_stats / last_wan_status keyed
// "WAN", "WAN2"…; the health "wan" subsystem only describes the active one, so it fills gaps for that one.
// Protect and Access hardware that Network lists as plain devices: cameras, video recorders, door hubs
function homeRole(d) {
  const t = String(d.type || "").toLowerCase(), m = `${d.modelCode || ""} ${d.model || ""}`, v = String(d.version || "");
  if (/unvr|envr|\bnvr\b|cloud ?key|^uck|\buck/i.test(m) || t === "unvr" || t === "uck") return "nvr";
  if (t === "uvc" || /^uvc\./i.test(v) || /^uvc|\bg[3-6]\b|\bai (pro|360|bullet|dome|turret|theta)\b|\b(bullet|dome|turret|doorbell|ptz)\b/i.test(m)) return "camera";
  if (t === "uah" || /^ua[- ]|\bua (g2|g3|pro|hub|lite|ultra|intercom)\b|access (hub|reader)|intercom/i.test(m)) return "access";
  return "other";
}
function buildWanLinks(g, wans, hw, www) {
  const geo = g.geo_info || {}, upt = { ...(hw.uptime_stats || {}), ...(g.uptime_stats || {}) }, last = g.last_wan_status || {};
  const links = [];
  for (let i = 1; i <= 4; i++) {
    const port = g["wan" + i], key = i === 1 ? "WAN" : "WAN" + i;
    const conf = wans.find(x => (x.group || "WAN") === key);
    if (!port && !geo[key] && !conf) continue;
    if (port && port.enabled === false && !conf) continue;
    const p = port || {}, gi = geo[key] || {}, u = upt[key] || {};
    const st = String(last[key] || "").toLowerCase();
    let status = p.up === false || /down|offline|fail/.test(st) ? "down" : p.up === true || /up|online|active/.test(st) ? "ok" : null;
    links.push({
      key, name: conf?.name || (i === 1 ? "Primary (WAN1)" : `WAN${i}`), ip: p.ip || conf?.ip || null, isp: gi.isp_name || gi.isp_organization || null,
      status, latency: num(u.latency_average), availability: num(u.availability), speed: num(p.speed), type: conf?.type || null,
      lb: conf?.lb || null, enabled: conf ? conf.enabled : true, rx: num(p["rx_bytes-r"]), tx: num(p["tx_bytes-r"]), active: g.active_geo_info ? g.active_geo_info === key : null,
    });
  }
  // the active connection (or the only one we know about) gets the health data
  const act = links.find(l => l.ip && l.ip === hw.wan_ip) || links.find(l => l.active) || (links.length ? links[0] : null);
  if (act) {
    act.active = true; act.ip ||= hw.wan_ip || null; act.isp ||= hw.isp_name || hw.isp_organization || null;
    act.status ||= hw.status || null; if (act.latency === null) act.latency = num(www.latency);
  } else if (hw.wan_ip || hw.isp_name || hw.status) {
    links.push({ key: "WAN", name: "Internet", ip: hw.wan_ip || null, isp: hw.isp_name || hw.isp_organization || null, status: hw.status || null,
      latency: num(www.latency), availability: null, speed: null, type: null, lb: null, enabled: true, rx: null, tx: null, active: true });
  }
  links.forEach(l => { if (l.active === null) l.active = false; });
  return links;
}

// Provider badge: the cached favicon when an admin has given the provider's website, else a lettered badge.
const ISP_LOGOS = {};
function ispInitials(n) { const w = String(n || "?").replace(/\(.*?\)/g, "").split(/[\s\-–,.&]+/).filter(x => x && !/^(the|of|and|ltd|inc|llc|plc|sa|ag|gmbh)$/i.test(x)); return (w.length > 1 ? w[0][0] + w[1][0] : (w[0] || "?").slice(0, 2)).toUpperCase(); }
function ispHue(n) { let h = 0; for (const c of String(n || "")) h = (h * 31 + c.charCodeAt(0)) % 360; return h; }
function ispBadge(isp, size = 32) {
  const k = String(isp || "").trim().toLowerCase();
  const logo = ISP_LOGOS[k];
  const mono = `<span class="isp-badge" style="--h:${ispHue(k)};width:${size}px;height:${size}px;font-size:${Math.round(size * .38)}px" aria-hidden="true">${esc(ispInitials(isp))}</span>`;
  return logo ? `<img class="isp-logo" src="api/isp-logo?name=${encodeURIComponent(k)}&v=${encodeURIComponent(logo)}" width="${size}" height="${size}" alt="">` : mono;
}

// ------------------------------------------------------------------ channel planning
function channelPlan(m) {
  const aps = m.devices.filter(d => d.online && d.radios.some(r => r.band === "2.4"));
  const noise = ch => m.neighbors.filter(n => n.band === "2.4" && n.channel && Math.abs(n.channel - ch) <= 4)
    .reduce((a, n) => a + Math.pow(10, ((n.signal ?? -85) + 100) / 10) * (1 - Math.abs(n.channel - ch) / 5), 0);
  const cands = [1, 6, 11].map(ch => ({ ch, noise: noise(ch) })).sort((a, b) => a.noise - b.noise);
  const plan = [];
  aps.sort((a, b) => (b.clients || 0) - (a.clients || 0)).forEach((ap, i) => {
    const r = ap.radios.find(r => r.band === "2.4"); const ch = cands[i % 3].ch;
    plan.push({ ap: ap.name, deviceId: ap.id, radio: r.name, from: r.channel, to: ch, change: r.channel !== ch });
  });
  return { cands, plan };
}
function radioAction(p, fields, desc) { return { kind: "radio", device_id: p.deviceId, radio: p.radio, fields, description: desc }; }

// ------------------------------------------------------------------ analysis rules
const SEV_ORDER = ["critical", "high", "medium", "low", "info"];
const SEV_WEIGHT = { critical: 30, high: 12, medium: 4, low: 1, info: 0 };
const SEV_CAP = { critical: 60, high: 36, medium: 24, low: 8, info: 0 };
const RISKY_PORTS = { 21: "FTP", 22: "SSH", 23: "Telnet", 25: "SMTP", 135: "MS-RPC", 139: "NetBIOS", 445: "SMB", 1433: "MSSQL", 1521: "Oracle", 2375: "Docker API", 3306: "MySQL", 3389: "RDP", 5432: "PostgreSQL", 5900: "VNC", 5985: "WinRM", 6379: "Redis", 8080: "HTTP admin", 8443: "HTTPS admin", 9200: "Elasticsearch", 27017: "MongoDB", 10000: "Webmin" };
function portsIn(spec) { const out = []; String(spec || "").split(",").forEach(p => { const [a, b] = p.split("-").map(Number); if (!isNaN(a)) { if (!isNaN(b) && b - a < 70000) { for (let i = a; i <= Math.min(b, a + 5000); i++) out.push(i); } else out.push(a); } }); return out; }
const IOT_RE = /iot|smart|cam|camera|thing|device|nest|hue|alexa|echo|ring|tuya|sonos|tv|printer|appliance/i;
// an SSID for smart-home things: by its own name or the network it's on
const isIoTWlan = (m, w) => IOT_RE.test(w.name) || IOT_RE.test(m.netById[w.networkId]?.name || "");

function analyse(m) {
  const F = [];
  const L = m.hasLegacy;
  const add = (sev, area, title, why, fix, items = [], actions = []) => F.push({ id: F.length + 1, sev, area, title, why, fix, items: items.filter(Boolean), actions: L ? actions.filter(Boolean) : [] });
  const wl = (w, fields, d) => ({ kind: "rest", resource: "wlanconf", id: w.id, fields, description: d });
  const setting = (key, fields, d) => m.settings.has(key) ? { kind: "setting", key, fields, description: d } : null;
  const aps = m.devices.filter(d => d.role === "ap" || (d.role === "gw" && d.radios.length));
  const multiAP = aps.length >= 2;
  const enabledW = m.wlans.filter(w => w.enabled);
  const lans = m.networks.filter(n => !n.vpn && n.enabled);

  // ---- Wi-Fi security
  for (const w of enabledW) {
    const net = m.netById[w.networkId];
    const iot = isIoTWlan(m, w); // smart-home devices often can't do WPA3 or 802.11r: don't suggest those there
    if (w.sec === "Open" && !w.guest && !net?.guest) add("critical", "Wi-Fi", `“${w.name}” is an open network`, "Anyone in range can join and reach devices on this network; traffic is unencrypted.", "Settings → WiFi → " + w.name + " → Security: WPA2/WPA3 Personal with a strong passphrase.", [w.name], [wl(w, { enabled: false }, `Turn off “${w.name}” until it has a password`)]);
    else if (w.sec === "Open") add("medium", "Wi-Fi", `Guest Wi-Fi “${w.name}” is unencrypted`, "Guests' traffic can be sniffed by anyone nearby.", "Use WPA2/WPA3 with a simple shared password, or Enhanced Open (OWE) if your clients support it.", [w.name]);
    if (w.sec === "WEP") add("critical", "Wi-Fi", `“${w.name}” uses WEP`, "WEP can be cracked in minutes.", "Switch to WPA2/WPA3 Personal.", [w.name]);
    if (w.sec === "WPA1" || w.sec === "WPA/WPA2" || w.tkip) add("high", "Wi-Fi", `“${w.name}” allows legacy WPA/TKIP`, "WPA1 and TKIP are deprecated and weaken the whole SSID; they also cap affected clients at 54 Mbps.", "Settings → WiFi → " + w.name + " → Security: WPA2 (AES/CCMP) or WPA2/WPA3.", [w.name]);
    if (w.sec === "WPA2" && !w.enterprise && !iot) add("low", "Wi-Fi", `“${w.name}” could use WPA3`, "WPA3 (SAE) resists offline password-guessing attacks. Transition mode keeps older WPA2 devices working.", "Settings → WiFi → " + w.name + " → Security → WPA2/WPA3.", [w.name], [wl(w, { wpa3_support: true, wpa3_transition: true, pmf_mode: "optional" }, `Enable WPA2/WPA3 transition on “${w.name}”`)]);
    if (w.pmf === "disabled" && w.sec !== "Open" && !/WPA3/.test(w.sec)) add("medium", "Wi-Fi", `PMF is off on “${w.name}”`, "Protected Management Frames stop de-authentication / disconnect attacks.", "Settings → WiFi → " + w.name + " → Advanced → PMF: Optional.", [w.name], [wl(w, { pmf_mode: "optional" }, `PMF → optional on “${w.name}”`)]);
    if (w.pskLen !== null && w.pskLen < 12 && !w.enterprise && w.sec !== "Open") add("high", "Wi-Fi", `Short Wi-Fi password on “${w.name}” (${w.pskLen} characters)`, "WPA2 passphrases under 12 characters can be brute-forced offline from a captured handshake.", "Use 16+ random characters or a 4–5 word passphrase (change it in the UniFi app — this tool never handles passwords). Consider Private PSK for IoT.", [w.name]);
    if (w.hidden) add("low", "Wi-Fi", `“${w.name}” is hidden`, "Hiding the SSID adds no security (it is visible in probe requests) and makes devices probe for it everywhere, leaking the name and hurting roaming.", "Settings → WiFi → " + w.name + " → Advanced → uncheck Hide WiFi Name.", [w.name], [wl(w, { hide_ssid: false }, `Unhide “${w.name}”`)]);
    if ((w.guest || net?.guest) && !w.l2iso && !net?.isolated && !(net?.guest)) add("high", "Wi-Fi", `Guest SSID “${w.name}” is not isolated`, "Guests can reach your main devices.", "Put it on a Guest network (Settings → Networks → purpose Guest / Isolate Network) and enable Client Device Isolation on the SSID.", [w.name], [wl(w, { l2_isolation: true }, `Client isolation on “${w.name}”`)]);
    else if ((w.guest || net?.guest) && !w.l2iso) add("low", "Wi-Fi", `Client isolation is off on guest SSID “${w.name}”`, "Guests can see each other's devices.", "Settings → WiFi → " + w.name + " → Advanced → Client Device Isolation.", [w.name], [wl(w, { l2_isolation: true }, `Client isolation on “${w.name}”`)]);
    if (IOT_RE.test(w.name) && net && (net === m.defaultNet || (!net.isolated && net.purpose === "corporate" && lans.length === 1))) {
      const iotNet = lans.find(n => n !== net && IOT_RE.test(n.name));
      add("medium", "Segmentation", `IoT SSID “${w.name}” shares your main network`, "Cameras, smart plugs and TVs rarely get security updates; on the main LAN a compromised one can reach your laptops and NAS.",
        iotNet ? `Point “${w.name}” at your existing “${iotNet.name}” network (Settings → WiFi → ${w.name} → Network) and isolate that network. Devices will reconnect and get new IPs — update any fixed IPs / port forwards.` : "Create an IoT VLAN (Settings → Networks → New, VLAN 20, Isolate Network), then point the SSID at it.", [w.name]);
    }
    if (multiAP && !w.fastRoaming && !w.enterprise && w.clients > 0 && !iot) add("info", "Wi-Fi", `Fast roaming is off on “${w.name}”`, "802.11r speeds up hand-off between APs for calls and video. Some old IoT devices dislike it, so enable it only on SSIDs for phones/laptops.", "Settings → WiFi → " + w.name + " → Advanced → Fast Roaming.", [w.name], [wl(w, { fast_roaming_enabled: true }, `802.11r on “${w.name}”`)]);
    if (w.macFilter) add("info", "Wi-Fi", `MAC filtering on “${w.name}”`, "MAC addresses are trivially spoofed and modern phones randomise them, so this mostly causes support headaches.", "Prefer per-device keys (Private PSK) or an isolated VLAN.", [w.name]);
  }
  const ssidPerBand = { "2g": 0, "5g": 0, "6g": 0 };
  enabledW.forEach(w => w.bands.forEach(b => { if (b in ssidPerBand) ssidPerBand[b]++; }));
  const maxS = Math.max(...Object.values(ssidPerBand));
  if (maxS > 4) add("medium", "Wi-Fi", `${maxS} SSIDs broadcast on one band`, "Each SSID sends its own beacons; beyond ~4 they consume a noticeable share of airtime, especially on 2.4 GHz.", "Merge SSIDs (use VLANs / Private PSK instead of extra names) or limit some SSIDs to 5 GHz or to specific APs.");
  if (multiAP && !enabledW.some(w => w.minrate)) add("low", "Radios", "Minimum data rate control is off", "Without it, clients cling to a distant AP at slow rates instead of roaming, dragging down the airtime for everyone.", "Settings → WiFi → [SSID] → Advanced → Minimum Data Rate: 2.4 GHz 12 Mbps, 5 GHz 12–24 Mbps (raise gradually).");

  // ---- radios
  const allR = aps.flatMap(a => a.radios.map(r => ({ ...r, ap: a.name, deviceId: a.id, online: a.online })));
  const r24 = allR.filter(r => r.band === "2.4");
  const wide24 = r24.filter(r => r.width && r.width > 20);
  if (wide24.length) add("medium", "Radios", `2.4 GHz uses ${wide24[0].width} MHz channels`, "40 MHz on 2.4 GHz overlaps two of the three non-overlapping channels, increasing interference for you and your neighbours.", "UniFi Devices → [AP] → Settings → Radios → 2.4 GHz → Channel Width: 20 MHz.", wide24.map(r => r.ap), wide24.filter(r => r.online).map(r => radioAction(r, { ht: 20 }, `${r.ap}: 2.4 GHz width → 20 MHz`)));
  const { plan } = channelPlan(m);
  const planActs = plan.filter(p => p.change).map(p => radioAction(p, { channel: p.to }, `${p.ap}: 2.4 GHz channel ${p.from} → ${p.to}`));
  const bad24 = r24.filter(r => r.channel && ![1, 6, 11].includes(r.channel));
  if (bad24.length) add("medium", "Radios", "2.4 GHz channel not on 1/6/11", "Channels between 1, 6 and 11 overlap two neighbours at once.", "Set 2.4 GHz channels to 1, 6 or 11 only. The suggested plan picks the quietest channels based on the neighbour scan.", bad24.map(r => `${r.ap} (ch ${r.channel})`), planActs);
  for (const band of ["2.4", "5", "6"]) {
    const by = {}; allR.filter(r => r.band === band && r.channel && r.online).forEach(r => (by[r.channel] ||= []).push(r.ap));
    const clash = Object.entries(by).filter(([, v]) => v.length > 1);
    if (clash.length && aps.length > 1) add(band === "2.4" ? "medium" : "low", "Radios", `APs share the same ${band} GHz channel`, "Neighbouring APs on one channel take turns to transmit, halving throughput where they overlap.", `Spread ${band} GHz channels${band === "2.4" ? " across 1 / 6 / 11 (suggested plan uses the neighbour scan)" : " (e.g. 36 and 149 with 80 MHz)"}.`, clash.map(([c, v]) => `ch ${c}: ${v.join(", ")}`), band === "2.4" && !bad24.length ? planActs : []);
  }
  const busy = allR.filter(r => r.util !== null && r.util >= 50);
  if (busy.length) add("medium", "Radios", "Busy radios (≥50% channel utilisation)", "A congested channel means slower Wi-Fi and higher latency for every client on that AP.", "Change channel (see Neighbors), narrow width on 5 GHz, steer clients to 5/6 GHz, or add an AP.", busy.map(r => `${r.ap} ${r.band} GHz: ${r.util}%`));
  const highTx = r24.filter(r => r.txMode === "high" && r.online);
  if (aps.length >= 2 && highTx.length >= Math.min(2, aps.length)) add("low", "Radios", "APs transmit 2.4 GHz at High power", "With several APs, full power makes clients hold on to far APs (sticky clients) and increases co-channel interference.", "Set 2.4 GHz to Medium (or Low for APs close together).", [...new Set(highTx.map(r => r.ap))], highTx.map(r => radioAction(r, { tx_power_mode: "medium" }, `${r.ap}: 2.4 GHz power → medium`)));
  const r160 = allR.filter(r => r.band === "5" && r.width >= 160);
  if (r160.length) add("info", "Radios", "5 GHz uses 160 MHz channels", "160 MHz uses DFS spectrum and is easily disrupted by radar events/neighbours; 80 MHz is usually more stable at home.", "Consider 80 MHz unless you've measured a clear benefit.", r160.map(r => r.ap), r160.filter(r => r.online).map(r => radioAction(r, { ht: 80 }, `${r.ap}: 5 GHz width → 80 MHz`)));

  // ---- networks / segmentation
  if (lans.length <= 1) add("medium", "Segmentation", "Everything is on one network", "A single flat network lets any compromised device (IoT, a guest's laptop) reach every other device.", "Create at least: Main (trusted), IoT (isolated, VLAN 20) and Guest (VLAN 30). Settings → Networks → New Virtual Network.");
  const customRules = m.rules.filter(r => r.enabled).length + m.policies.filter(p => !p.predefined && p.enabled).length;
  const openNets = lans.filter(n => !n.isolated && !n.guest && n !== m.defaultNet);
  if (lans.length > 1 && openNets.length) {
    const named = openNets.filter(n => IOT_RE.test(n.name) || /guest|lab|dmz|untrust/i.test(n.name));
    const rulesFor = n => m.rules.some(r => r.enabled && (r.srcNet === n.id || r.dstNet === n.id) && /drop|reject/.test(r.action)) || m.policies.some(p => p.enabled && !p.predefined && /BLOCK|REJECT/.test(p.action));
    const unprotected = named.filter(n => !rulesFor(n));
    if (unprotected.length) add("medium", "Segmentation", "Untrusted networks can reach your main LAN", "UniFi routes between VLANs by default. A VLAN without isolation or block rules separates broadcast traffic only, not access.", "Settings → Networks → [network] → Isolate Network, or add a Block policy from that network's zone to Internal. Isolation also blocks casting/printing from that network to the main LAN.", unprotected.map(n => n.name), unprotected.map(n => ({ kind: "rest", resource: "networkconf", id: n.id, fields: { network_isolation_enabled: true }, description: `Isolate “${n.name}”` })));
    else if (!customRules) add("low", "Segmentation", "No custom firewall rules between VLANs", "All internal networks can talk to each other freely.", "Decide which networks should reach which, and add Block policies for the rest.", openNets.map(n => n.name));
  }
  for (const n of lans) {
    const c = cidr(n.subnet);
    if (c && ["192.168.1.0", "192.168.0.0", "10.0.0.0"].includes(intToIp(c.net)) && c.bits >= 16) { add("info", "Networks", `“${n.name}” uses a very common subnet (${n.subnet})`, "Hotels, offices and friends' homes use the same range, which breaks routing when you VPN home.", "If you use (or plan) a VPN, renumber to something unusual, e.g. 10.42.10.0/24. Plan downtime — every static IP changes.", [n.name]); break; }
  }
  for (const n of lans) {
    if (!n.dhcp || !n.dhcpStart || !n.dhcpStop) continue;
    const size = (ipToInt(n.dhcpStop) ?? 0) - (ipToInt(n.dhcpStart) ?? 0) + 1;
    n.poolSize = size > 0 ? size : null;
    const leases = m.known.filter(k => k.networkId === n.id && k.lastSeen && Date.now() - toMs(k.lastSeen) < 86400000 * (n.lease ? n.lease / 86400 : 1)).length;
    const used = Math.max(n.clients, leases);
    if (n.poolSize && used / n.poolSize > 0.8) add("medium", "Networks", `DHCP pool nearly full on “${n.name}”`, `${used} recent clients for ${n.poolSize} addresses. New devices may fail to get an address.`, "Widen the DHCP range or shorten the lease time.", [n.name]);
  }
  for (let i = 0; i < lans.length; i++) for (let j = i + 1; j < lans.length; j++) {
    const a = cidr(lans[i].subnet), b = cidr(lans[j].subnet);
    if (a && b && a.net <= b.bcast && b.net <= a.bcast) add("high", "Networks", "Overlapping subnets", "Two networks claim the same addresses; routing between them is unpredictable.", "Renumber one of them.", [`${lans[i].name} (${lans[i].subnet})`, `${lans[j].name} (${lans[j].subnet})`]);
  }
  // IP conflicts
  const ipOwners = {}; [...m.devices.filter(d => d.ip).map(d => ({ ip: d.ip, mac: d.mac, who: d.name })), ...m.clients.filter(c => c.ip).map(c => ({ ip: c.ip, mac: c.mac, who: c.label }))].forEach(x => (ipOwners[x.ip] ||= new Map()).set(x.mac, x.who));
  const conflicts = Object.entries(ipOwners).filter(([, v]) => v.size > 1);
  if (conflicts.length) add("high", "Networks", "IP address conflict", "Two devices are using the same IP; both will lose connectivity intermittently. A classic cause of devices that 'won't load' or keep dropping.", "Give one of them a different fixed IP (outside the DHCP range) or switch it back to DHCP.", conflicts.map(([ip, v]) => `${ip}: ${[...v.values()].join(" vs ")}`));
  // fixed IPs inside the DHCP pool
  const fixedInPool = m.known.filter(k => k.fixedIp).filter(k => { const n = m.netForIp(k.fixedIp); if (!n || !n.dhcpStart) return false; const i = ipToInt(k.fixedIp); return i >= ipToInt(n.dhcpStart) && i <= ipToInt(n.dhcpStop); });
  if (fixedInPool.length) add("low", "Networks", "Fixed IPs inside the DHCP range", "UniFi reserves them, but devices with manually-set static IPs in the pool can collide with DHCP leases.", "Keep fixed/static IPs outside the DHCP range (e.g. .2–.49) and shrink the pool accordingly.", fixedInPool.map(k => `${k.label}: ${k.fixedIp}`));

  // ---- security & exposure
  const ips = m.settings.ips;
  if (ips === "disabled" || ips === false) add("medium", "Security", "Intrusion Prevention (IPS) is off", "IPS blocks known exploits, malware call-backs and scanning from/to your network. Modern gateways handle it at full home-internet speed.", "Settings → CyberSecure (or Security) → Intrusion Prevention → Notify and Block.", [], [setting("ips", { ips_mode: "ips" }, "IPS → Notify and Block")]);
  else if (ips === "ids") add("low", "Security", "IDS is only detecting, not blocking", "Detection-only mode logs threats but lets them through.", "Switch Intrusion Prevention from Notify to Notify and Block.", [], [setting("ips", { ips_mode: "ips" }, "IPS → Notify and Block")]);
  if (m.ips.length) add("info", "Security", `${plural(m.ips.length, "threat event")} in the last 30 days`, "The gateway's IPS logged these. Check History & Events for sources and whether they were blocked.", "Repeated hits from one internal device can mean it's compromised — consider isolating it.", [...new Set(m.ips.map(i => i.sig).filter(Boolean))].slice(0, 6));
  if (m.settings.upnp) add("medium", "Security", "UPnP is enabled", "Any device on the LAN (including compromised IoT) can silently open inbound ports on your router.", "Settings → Internet → UPnP off. If a console/game needs it, use a manual port forward instead.", [], [setting("usg", { upnp_enabled: false }, "Turn UPnP off")]);
  for (const p of m.portForwards.filter(p => p.enabled)) {
    const risky = [...new Set([...portsIn(p.dstPort), ...portsIn(p.fwdPort)])].filter(x => RISKY_PORTS[x]);
    const off = { kind: "rest", resource: "portforward", id: p.id, fields: { enabled: false }, description: `Disable port forward “${p.name}”` };
    if (risky.length) add("high", "Exposure", `Port forward “${p.name}” exposes ${[...new Set(risky.map(x => RISKY_PORTS[x]))].join(", ")} to the internet`, "These services are constantly scanned and brute-forced; exposing them directly is a common breach path.", "Disable it and use the UniFi VPN (WireGuard / Teleport) to reach the service remotely; if it must stay, restrict the source IPs.", [`${p.dstPort} → ${p.fwd}:${p.fwdPort}`], [off]);
    else if (!p.srcLimited) add("medium", "Exposure", `Port forward “${p.name}” is open to any source`, "The whole internet can reach " + p.fwd + ".", "Restrict the source (allowed IPs / region blocking) or replace with VPN access; keep the target device patched.", [`${p.dstPort} → ${p.fwd}:${p.fwdPort}`], [off]);
  }
  const disabledPF = m.portForwards.filter(p => !p.enabled);
  if (disabledPF.length) add("info", "Exposure", `${plural(disabledPF.length, "disabled port forward")}`, "Unused rules make the config harder to read and are easy to re-enable by accident.", "Delete forwards you no longer need (in the UniFi app).", disabledPF.map(p => p.name));
  const extAllow = m.policies.filter(p => p.enabled && !p.predefined && p.action === "ALLOW" && /external|wan|internet/i.test(p.srcZoneKey || p.srcZone || "") && !p.dstPort);
  if (extAllow.length) add("high", "Exposure", "Firewall allows inbound traffic from the internet on all ports", "An Allow policy from the External zone without a port restriction exposes everything behind it.", "Restrict the policy to specific ports and source IPs, or remove it.", extAllow.map(p => p.name));
  if (m.settings.ssh) add(m.settings.sshPw !== false ? "medium" : "low", "Security", "SSH to UniFi devices is enabled" + (m.settings.sshPw !== false ? " with password login" : ""), "SSH gives root on every AP and switch. Fine for troubleshooting, but a standing risk if the password is reused or weak.", "Settings → System → Advanced → Device SSH Authentication: off when not needed, or keys only.", [], [setting("mgmt", { x_ssh_enabled: false }, "Turn device SSH off")]);
  if (m.settings.doh === "off" || m.settings.doh === "disabled") add("info", "Security", "Encrypted DNS (DoH) is off", "Your ISP (and anyone on the path) can see and tamper with DNS lookups.", "Settings → Internet / CyberSecure → Encrypted DNS: Auto or a provider such as Cloudflare/Quad9.", [], [setting("doh", { state: "auto" }, "Encrypted DNS → Auto")]);
  if (m.settings.geoip === false && m.portForwards.some(p => p.enabled)) add("low", "Exposure", "Region blocking is off while ports are forwarded", "Blocking countries you never connect from cuts most automated scanning of your forwarded services.", "Settings → CyberSecure → Region Blocking: block inbound from regions you don't use.");
  if (m.wans.filter(w => w.enabled).length === 1 && m.devices.length >= 4) add("info", "Internet", "Single WAN connection", "If the ISP line drops, everything is offline.", "Optional: add an LTE/5G backup or a second ISP as failover WAN.");

  // ---- devices, firmware, failures
  const upg = m.devices.filter(d => d.upgradable && d.online);
  if (upg.length) add("medium", "Firmware", `${plural(upg.length, "device")} with firmware updates available`, "Firmware updates fix security vulnerabilities and Wi-Fi bugs. Each device reboots (1–3 min offline).", "UniFi Devices → select → Update (APs one at a time, during a quiet hour).", upg.map(d => `${d.name}: ${d.version || "?"} → ${d.upgradeTo || "newer"}`), upg.filter(d => d.role !== "gw").map(d => ({ kind: "devcmd", cmd: "upgrade", mac: d.mac, label: d.name, description: `Upgrade ${d.name} to ${d.upgradeTo || "latest"} (reboots)` })));
  if (m.info.updateAvailable) add("medium", "Firmware", "UniFi Network application update available", "New versions include security fixes and features.", "Settings → Control Plane → Updates.");
  if (m.settings.autoUpgrade === false) add("info", "Firmware", "Automatic device updates are off", "Patches only land when you remember to apply them.", "Settings → Control Plane → Updates → schedule automatic device updates for a quiet hour.", [], [setting("mgmt", { auto_upgrade: true }, "Turn automatic device updates on")]);
  for (const d of m.devices.filter(d => !d.online)) {
    const dx = diagnoseDevice(d, m);
    add(d.state === 2 ? "low" : "high", "Devices", `${d.name} — ${d.stateLabel}${d.intState && d.intState !== d.stateLabel?.toUpperCase() ? ` (${d.intState.toLowerCase().replace(/_/g, " ")})` : ""}`, dx.summary, "Debug shows its switch port, PoE, recent events and what to try.", dx.items.slice(0, 4), dx.actions);
    F[F.length - 1].dev = d.mac;
  }
  const hot = m.devices.filter(d => (d.cpu ?? 0) >= 85 || (d.mem ?? 0) >= 90);
  if (hot.length) add("low", "Devices", "Devices under heavy load", "High CPU/memory causes slow management and dropped connections.", "Check for IPS + heavy traffic on the gateway, or restart devices with memory leaks; update firmware.", hot.map(d => `${d.name}: CPU ${d.cpu ?? "–"}% / RAM ${d.mem ?? "–"}%`), hot.filter(d => d.role !== "gw").map(d => ({ kind: "devcmd", cmd: "restart", mac: d.mac, label: d.name, description: `Restart ${d.name}` })));
  const longUp = m.devices.filter(d => d.uptime > 180 * 86400);
  if (longUp.length) add("info", "Firmware", "Devices running for more than 6 months", "Very long uptime usually means the device hasn't received firmware updates.", "Check firmware versions; updating will reboot them.", longUp.map(d => `${d.name} (${fmtDur(d.uptime)})`));

  // ---- switch ports
  const sws = m.devices.filter(d => d.ports.length);
  const errPorts = sws.flatMap(s => s.ports.filter(p => p.up && (p.rxErr + p.txErr) > 100 && (p.rxErr + p.txErr) / Math.max(1, p.rxPkts) > 0.0005).map(p => `${s.name} port ${p.idx}: ${p.rxErr + p.txErr} errors`));
  if (errPorts.length) add("medium", "Ports", "Switch ports with transmission errors", "CRC/frame errors almost always mean a damaged cable, bad keystone/patch panel, or a failing port.", "Re-terminate or replace the cable; try another switch port.", errPorts);
  const slow = sws.flatMap(s => s.ports.filter(p => p.up && p.speed && p.speed < 1000 && !p.sfp).map(p => {
    const dev = m.devices.find(d => d.uplinkMac === s.mac && d.uplinkPort === p.idx);
    return dev ? `${s.name} port ${p.idx} → ${dev.name} at ${p.speed} Mbps` : null;
  })).filter(Boolean);
  if (slow.length) add("medium", "Ports", "UniFi device linked at 10/100 Mbps", "Access points and switches should link at 1 Gbps or more. 100 Mbps usually means a broken pair in the cable or a bad connector.", "Check/replace the cable; avoid old Cat5 and couplers.", slow);
  const half = sws.flatMap(s => s.ports.filter(p => p.up && !p.fdx).map(p => `${s.name} port ${p.idx}`));
  if (half.length) add("low", "Ports", "Ports running half-duplex", "Half duplex causes collisions and very poor throughput — usually a negotiation problem or bad cable.", "Set both ends to auto-negotiate; replace the cable.", half);
  const poeHot = sws.filter(s => s.poeMax && s.poeW / s.poeMax > 0.85);
  if (poeHot.length) add("medium", "Ports", "PoE budget almost used", "When the PoE budget is exceeded the switch cuts power to some ports — devices on those ports go offline.", "Move a PoE device to another switch or use a PoE injector.", poeHot.map(s => `${s.name}: ${s.poeW.toFixed(1)} / ${s.poeMax} W`));

  // ---- neighbours
  const n24 = m.neighbors.filter(n => n.band === "2.4");
  if (n24.length >= 8 && plan.some(p => p.change)) add("info", "Radios", `${n24.length} neighbouring 2.4 GHz networks`, "A busy neighbourhood — choosing the least-used of 1/6/11 for each AP helps.", "See Neighbors for the scan; the suggested plan is attached.", plan.filter(p => p.change).map(p => `${p.ap}: ch ${p.from} → ${p.to}`), bad24.length ? [] : planActs);

  // ---- clients / WAN
  const wlc = m.clients.filter(c => !c.wired);
  // smart plugs, sensors and cameras far from an AP send little and are fine at a weak signal: count people's devices
  const iotSSIDs = new Set(m.wlans.filter(w => isIoTWlan(m, w)).map(w => w.name));
  const weak = wlc.filter(c => c.signal !== null && c.signal < -75 && !iotSSIDs.has(c.ssid) && !IOT_RE.test(c.network || ""));
  if (weak.length >= 3 || (wlc.length && weak.length / wlc.length > 0.2)) add("low", "Coverage", `${plural(weak.length, "client")} with weak signal (< −75 dBm)`, "Weak clients transmit at low data rates and slow down every other client on the same AP.", "Move/add APs near those devices, enable minimum data rates, or wire stationary devices.", weak.slice(0, 12).map(c => `${c.label} (${c.signal} dBm)`));
  const unhappy = m.clients.filter(c => c.satisfaction !== null && c.satisfaction < 50);
  if (unhappy.length >= 2) add("low", "Coverage", `${plural(unhappy.length, "client")} with poor experience score`, "UniFi rates these connections below 50% (retries, latency, signal).", "Look at the Clients view for the AP / band they use; usually a coverage or interference problem.", unhappy.slice(0, 12).map(c => `${c.label} (${c.satisfaction}%)`));
  const unnamed = m.clients.filter(c => !c.name);
  if (unnamed.length >= 5) add("info", "Hygiene", `${plural(unnamed.length, "client")} without a name`, "Unknown devices make it hard to spot intruders or troubleshoot.", "Name them in Clients (⋯ → Rename) or in the UniFi app.");
  if (m.info.wanStatus && m.info.wanStatus !== "ok") add("high", "Internet", `WAN health is “${m.info.wanStatus}”`, "The gateway reports a problem with the internet connection.", "Check the WAN cable/modem and ISP status; review Internet → WAN statistics.");
  if (m.info.latency !== null && m.info.latency > 60) add("low", "Internet", `High internet latency (${m.info.latency} ms)`, "Latency above ~60 ms is noticeable in calls and games.", "Run a speed test at a quiet time; enable Smart Queues if your upload is saturated.");

  // ---- NextDNS / Tailscale
  const ndNets = lans.filter(n => arr(n.dns).some(ip => dnsProvider(ip).kind === "nextdns"));
  if (ndNets.length) add("info", "DNS", "NextDNS is handed out directly by DHCP — no device names", "Clients talk to NextDNS's IPs themselves, so NextDNS can't tell your devices apart (they share your WAN / linked IP).", "Run the NextDNS CLI on the gateway (the NextDNS & Tailscale page generates the exact commands), then switch these networks' DHCP DNS back to the gateway.", ndNets.map(n => n.name));
  if (m.settings.dohNextdns) add("info", "DNS", "NextDNS is the gateway's Encrypted DNS — devices appear as one", "UniFi's Encrypted DNS sends every query from the gateway itself, so NextDNS logs show a single device instead of your clients.", "For per-device names and per-VLAN profiles, use the NextDNS CLI on the gateway instead (see NextDNS & Tailscale page) and turn Encrypted DNS off.");

  if (!m.hasLegacy) add("info", "Data", "Limited data: only the official Integration API answered", "Wi-Fi security, firewall, port-forward and IPS checks — and all actions — need the classic Network API, which this key/console didn't allow.", "Update UniFi Network to the latest version.");

  F.sort((a, b) => SEV_ORDER.indexOf(a.sev) - SEV_ORDER.indexOf(b.sev));
  return F;
}
const scoreOf = F => Math.max(0, Math.round(100 - SEV_ORDER.reduce((a, s) => a + Math.min(SEV_CAP[s], F.filter(f => f.sev === s).length * SEV_WEIGHT[s]), 0)));
const grade = s => s >= 90 ? "A" : s >= 80 ? "B" : s >= 65 ? "C" : s >= 50 ? "D" : "F";

// ------------------------------------------------------------------ device debugging
function uplinkInfo(d, m) {
  const parent = (d.uplinkMac && m.devByMac[d.uplinkMac]) || null;
  let port = null;
  if (parent && d.uplinkPort !== null && d.uplinkPort !== undefined) port = parent.ports.find(p => p.idx === d.uplinkPort) || null;
  if (!parent) { // try to find the switch port where its MAC was last seen as a client / LLDP
    for (const s of m.devices.filter(x => x.ports.length)) {
      const raw = s.sources["stat/device"] || {};
      for (const p of arr(raw.port_table)) {
        const tbl = JSON.stringify(p.mac_table || p.lldp_table || []).toLowerCase();
        if (tbl.includes(d.mac)) return { parent: s, port: s.ports.find(x => x.idx === num(p.port_idx)), how: "seen on port" };
      }
    }
  }
  return { parent, port, how: parent ? "uplink" : null };
}
function deviceEvents(d, m) { return [...m.events, ...m.alarms.map(a => ({ ...a, src: "alarm" }))].filter(e => e.blob.includes(d.mac) || (d.name && e.blob.includes(String(d.name).toLowerCase()))).sort((a, b) => b.t - a.t); }

function diagnoseDevice(d, m) {
  const items = [], actions = [], steps = [];
  const up = uplinkInfo(d, m);
  const st = d.state, is = d.intState || "";
  if (d.online) items.push("Device is connected and managed normally.");
  if (d.partial) { items.push("UniFi sees it but isn't managing it. Try restarting it, or remove and adopt it again."); steps.push("Technical: it is missing from stat/device and only listed in " + Object.keys(d.sources).join(", ") + "."); }
  if (st === 0 || is === "OFFLINE" || is === "CONNECTION_INTERRUPTED") {
    items.push(`Console lost contact${d.lastSeen ? " " + ago(d.lastSeen) : ""}.`);
    steps.push("Check power and the network cable at both ends.", "If powered by PoE, power-cycle its switch port (button below).", "If it stays offline, SSH in and run `info` to see its inform URL, then `set-inform http://<console-ip>:8080/inform`.");
  }
  if (st === 2 || is === "PENDING_ADOPTION") { items.push("Waiting to be adopted."); actions.push({ kind: "devcmd", cmd: "adopt", mac: d.mac, label: d.name, description: `Adopt ${d.name}` }); }
  if (st === 9 || st === 10) { items.push("Adoption failed — usually a firmware/compatibility mismatch or the device is still adopted to another console."); steps.push("Factory reset the device (hold reset 10 s), then adopt again.", "Update UniFi Network first if this model is new."); }
  if (st === 6) { items.push("Heartbeat missed — device is reachable intermittently. Typical causes: IP conflict, flaky cable, loop, or overloaded uplink."); steps.push("Check for IP conflicts (Suggestions) and port errors on its uplink."); }
  if (st === 5) { items.push("Stuck provisioning — the configuration push isn't completing."); actions.push({ kind: "devcmd", cmd: "force-provision", mac: d.mac, label: d.name, description: `Force provision ${d.name}` }); }
  if (st === 4 || is === "UPDATING") { items.push("Firmware upgrade in progress — give it ~10 minutes before intervening."); }
  if (st === 11) { items.push("Isolated — a wireless (mesh) AP lost its uplink AP. Bring it closer or wire it."); }
  if (is === "GETTING_READY" || is === "ADOPTING") { items.push(`Integration API says “${is.toLowerCase().replace(/_/g, " ")}”. If this lasts more than 15 minutes the device isn't completing its handshake with the console.`); steps.push("Restart the device (or its PoE port).", "Make sure its firmware is current and supported by your Network version (Settings → Control Plane → Updates)."); }
  if (up.port) {
    const p = up.port;
    if (!p.up) { items.push(`Its uplink — ${up.parent.name} port ${p.idx} — has NO LINK. That's a cable/power/port problem, not a configuration one.`); }
    else { items.push(`Uplink: ${up.parent.name} port ${p.idx}, ${fmtSpeed(p.speed)}bps${p.fdx ? "" : " half-duplex"}${p.rxErr + p.txErr ? `, ${p.rxErr + p.txErr} errors` : ""}.`); if (p.speed && p.speed < 1000) items.push("Link is below 1 Gbps — suspect the cable."); }
    if (p.poeCap) {
      items.push(p.poeOn ? `PoE on (${p.poeMode || "auto"}), drawing ${p.poeW ?? 0} W.` : "PoE is OFF on that port — if the device is PoE-powered it has no power.");
      if (p.poeOn) actions.push({ kind: "devcmd", cmd: "power-cycle", mac: up.parent.mac, port_idx: p.idx, label: `${up.parent.name} port ${p.idx}`, description: `Power-cycle PoE on ${up.parent.name} port ${p.idx} (powers ${d.name} off and on)` });
    }
  } else if (!d.online) items.push("Couldn't determine which switch port it's on.");
  if (d.ip) { const others = [...m.clients.filter(c => c.ip === d.ip && c.mac !== d.mac).map(c => c.label), ...m.devices.filter(x => x.ip === d.ip && x.mac !== d.mac).map(x => x.name)]; if (others.length) items.push(`IP ${d.ip} is ALSO used by ${others.join(", ")} — IP conflict.`); }
  if (d.disconnectReason) items.push(`Last disconnect reason: ${d.disconnectReason}.`);
  if (d.informUrl && m.info.gwIp && !String(d.informUrl).includes(m.info.gwIp) && !String(d.informUrl).includes(new URL(m.info.host || "http://x").hostname)) items.push(`Inform URL is ${d.informUrl} — doesn't point at this console.`);
  if (d.upgradable) items.push(`Firmware ${d.version} → ${d.upgradeTo || "newer"} available.`);
  const ev = deviceEvents(d, m);
  if (ev.length) items.push(`${plural(ev.length, "related event")}; latest: “${ev[0].msg}” (${fmtTime(ev[0].t)}).`);
  if (d.isUps) steps.push("UPS: check it shows the latest firmware and that your UniFi Network/OS versions support this UPS model; reseat its network cable; if it stays 'getting ready', restart it from its front-panel menu.");
  if (d.online || [0, 6, 4, 5].includes(st) || d.partial) {
    if (d.role !== "gw") actions.push({ kind: "devcmd", cmd: "restart", mac: d.mac, label: d.name, description: `Restart ${d.name}` });
  }
  const summary = items.slice(0, 2).join(" ") || "No diagnostic data.";
  return { items, actions, steps, summary, uplink: up, events: ev };
}

// ------------------------------------------------------------------ charts (plain SVG)
const PAL = ["var(--c1)", "var(--c2)", "var(--c3)", "var(--c4)", "var(--c5)", "var(--c6)", "var(--c7)"];
function hbar(items, { unit = "", fmt = v => v, max } = {}) {
  if (!items.length) return `<div class="empty">No data</div>`;
  const W = 520, rowH = 26, lw = 160, H = items.length * rowH + 6, mx = max || Math.max(...items.map(i => i.value), 1);
  return `<svg class="chart" viewBox="0 0 ${W} ${H}" width="100%" role="img">${items.map((it, i) => {
    const w = Math.max(2, (W - lw - 74) * it.value / mx), y = i * rowH + 4;
    return `<text class="lbl" x="${lw - 8}" y="${y + 13}" text-anchor="end">${esc(String(it.label).slice(0, 24))}</text>
      <rect x="${lw}" y="${y + 2}" width="${w}" height="16" rx="3" style="fill:${it.color || PAL[0]}"><title>${esc(it.label)}: ${esc(fmt(it.value))}${unit}</title></rect>
      <text x="${lw + w + 6}" y="${y + 14}">${esc(fmt(it.value))}${unit}</text>`;
  }).join("")}</svg>`;
}
function donut(segs, center = "", sub = "") {
  segs = segs.filter(s => s.value > 0);
  const total = segs.reduce((a, s) => a + s.value, 0);
  if (!total) return `<div class="empty">No data</div>`;
  const R = 54, r = 34, cx = 64, cy = 64; let a0 = -Math.PI / 2;
  const paths = segs.map((s, i) => {
    const a1 = a0 + 2 * Math.PI * s.value / total, large = a1 - a0 > Math.PI ? 1 : 0;
    const p = (rad, a) => `${cx + rad * Math.cos(a)},${cy + rad * Math.sin(a)}`;
    const d = segs.length === 1 ? `M${cx - R},${cy}A${R},${R} 0 1 1 ${cx + R},${cy}A${R},${R} 0 1 1 ${cx - R},${cy}M${cx - r},${cy}A${r},${r} 0 1 0 ${cx + r},${cy}A${r},${r} 0 1 0 ${cx - r},${cy}Z`
      : `M${p(R, a0)}A${R},${R} 0 ${large} 1 ${p(R, a1)}L${p(r, a1)}A${r},${r} 0 ${large} 0 ${p(r, a0)}Z`;
    a0 = a1; return `<path d="${d}" style="fill:${s.color || PAL[i % PAL.length]}" fill-rule="evenodd"><title>${esc(s.label)}: ${s.value}</title></path>`;
  }).join("");
  return `<div style="display:flex;align-items:center;gap:16px;flex-wrap:wrap"><svg class="chart" width="128" height="128" viewBox="0 0 128 128">${paths}
    <text x="64" y="64" text-anchor="middle" class="lbl" style="font-size:20px;font-weight:700">${esc(center)}</text><text x="64" y="80" text-anchor="middle">${esc(sub)}</text></svg>
    <div class="legend" style="flex-direction:column;gap:4px">${segs.map((s, i) => `<span><i style="background:${s.color || PAL[i % PAL.length]}"></i>${esc(s.label)} · <b>${s.value}</b></span>`).join("")}</div></div>`;
}
function columns(items, { color = PAL[0], fmt = v => v } = {}) {
  if (!items.length || !items.some(i => i.value)) return `<div class="empty">No data</div>`;
  const W = 520, H = 170, pad = 24, bw = (W - pad) / items.length, mx = Math.max(...items.map(i => i.value), 1);
  return `<svg class="chart" viewBox="0 0 ${W} ${H}" width="100%">${items.map((it, i) => {
    const h = (H - 40) * it.value / mx, x = pad + i * bw;
    return `<rect x="${x + 2}" y="${H - 22 - h}" width="${Math.max(1, bw - 4)}" height="${h}" rx="2" style="fill:${it.color || color}"><title>${esc(it.label)}: ${esc(fmt(it.value))}</title></rect>
      ${it.value && bw > 18 ? `<text x="${x + bw / 2}" y="${H - 26 - h}" text-anchor="middle">${esc(fmt(it.value))}</text>` : ""}
      ${bw > 14 || i % Math.ceil(14 / bw) === 0 ? `<text x="${x + bw / 2}" y="${H - 7}" text-anchor="middle">${esc(it.label)}</text>` : ""}`;
  }).join("")}</svg>`;
}
function lineChart(series, { fmt = v => v, height = 190, legend = true } = {}) {
  const pts = series.flatMap(s => s.points.filter(p => p.v !== null && p.v !== undefined));
  if (!pts.length) return `<div class="empty">No history returned</div>`;
  const W = 620, H = height, L = 58, R = 12, T = 10, B = 26;
  let t0 = Math.min(...pts.map(p => p.t)), t1 = Math.max(...pts.map(p => p.t)); const vmax = Math.max(...pts.map(p => p.v), 1) * 1.08;
  if (t1 - t0 < 60000) { t0 -= 3600000; t1 += 3600000; }
  const x = t => L + (W - L - R) * (t - t0) / Math.max(1, t1 - t0), y = v => T + (H - T - B) * (1 - v / vmax);
  let g = "";
  for (let i = 0; i <= 4; i++) { const v = vmax * i / 4; g += `<line x1="${L}" x2="${W - R}" y1="${y(v)}" y2="${y(v)}" style="stroke:var(--line)"/><text x="${L - 6}" y="${y(v) + 4}" text-anchor="end">${esc(fmt(v))}</text>`; }
  const span = t1 - t0, fmtT = t => new Date(t).toLocaleString([], span > 3 * 86400000 ? { month: "short", day: "numeric" } : { weekday: "short", hour: "2-digit" });
  for (let i = 0; i <= 5; i++) { const t = t0 + span * i / 5; g += `<text x="${x(t)}" y="${H - 8}" text-anchor="middle">${esc(fmtT(t))}</text>`; }
  const lines = series.map((s, i) => { const p = s.points.filter(p => p.v !== null && p.v !== undefined); if (!p.length) return "";
    const d = p.map((q, j) => `${j ? "L" : "M"}${x(q.t).toFixed(1)},${y(q.v).toFixed(1)}`).join("");
    const dots = p.length <= 3 ? p.map(q => `<circle cx="${x(q.t).toFixed(1)}" cy="${y(q.v).toFixed(1)}" r="4" style="fill:${s.color || PAL[i]}"><title>${esc(fmt(q.v))}</title></circle>`).join("") : "";
    return `${s.fill && p.length > 1 ? `<path d="${d}L${x(p[p.length - 1].t)},${y(0)}L${x(p[0].t)},${y(0)}Z" style="fill:${s.color || PAL[i]};opacity:.12"/>` : ""}<path d="${d}" fill="none" style="stroke:${s.color || PAL[i]}" stroke-width="1.8"/>${dots}`; }).join("");
  return `<svg class="chart" viewBox="0 0 ${W} ${H}" width="100%">${g}${lines}</svg>${legend ? `<div class="legend">${series.map((s, i) => `<span><i style="background:${s.color || PAL[i]}"></i>${esc(s.name)}</span>`).join("")}</div>` : ""}`;
}
function gauge(score, size = 128) {
  const g = grade(score), col = score >= 80 ? "var(--ok)" : score >= 65 ? "var(--med)" : score >= 50 ? "var(--high)" : "var(--crit)";
  const R = 52, C = 2 * Math.PI * R, off = C * (1 - score / 100);
  return `<svg width="${size}" height="${size}" viewBox="0 0 128 128" class="chart" role="img" aria-label="Configuration score ${score}, grade ${g}"><circle cx="64" cy="64" r="${R}" fill="none" style="stroke:var(--line)" stroke-width="12"/>
    <circle cx="64" cy="64" r="${R}" fill="none" style="stroke:${col}" stroke-width="12" stroke-linecap="round" stroke-dasharray="${C}" stroke-dashoffset="${off}" transform="rotate(-90 64 64)"/>
    <text x="64" y="66" text-anchor="middle" class="lbl" style="font-size:30px;font-weight:800">${score}</text><text x="64" y="86" text-anchor="middle">grade ${g}</text></svg>`;
}

// ------------------------------------------------------------------ sortable tables
// Lists are compact tables: click a column header to sort (again to reverse). Columns: { k, label, fmt, sort, num, more }.
// `more` columns aren't columns: they show as a small grey line under the row's name. Columns empty in every row are dropped.
// `pin(row)` keeps rows (e.g. broken devices) at the top in red whatever the sort. On phones rows stack as cards.
const TABLES = {};
const emptyCell = h => !h || h === "–" || /^<span class="(muted|fine)">–<\/span>$/.test(h);
const cellHtml = (c, r) => String(c.fmt ? c.fmt(r) : esc(r[c.k] ?? "–")).trim();
function table(id, cols, rows, { search = false, empty = "Nothing here", rowAttr, title, pin } = {}) {
  const ti = Math.max(0, title ? cols.findIndex(c => c.k === title) : 0);
  const prev = TABLES[id];
  const vis = cols.map((c, i) => i).filter(i => i === ti || (!cols[i].more && rows.some(r => !emptyCell(cellHtml(cols[i], r)))));
  TABLES[id] = { cols, rows, sort: prev?.cols?.length === cols.length ? prev.sort : null, dir: prev?.dir || 1, q: prev?.q || "", rowAttr, ti, vis, pin };
  if (!rows.length) return `<div class="empty">${esc(empty)}</div>`;
  return `${search && rows.length > 5 ? `<div class="li-tools"><input class="search" placeholder="Filter…" data-tsearch="${id}" aria-label="Filter" value="${esc(TABLES[id].q)}"></div>` : ""}
    <div class="tw"><table class="tl" id="t-${id}">${thead(id)}<tbody>${tbody(id)}</tbody></table></div>`;
}
function thead(id) {
  const t = TABLES[id];
  return `<thead><tr>${t.vis.map(i => { const c = t.cols[i], on = t.sort === i;
    const lbl = c.label ? (t.rows.length > 1 ? `<button class="th-sort${on ? " on" : ""}" data-tsort="${id}" data-col="${i}" aria-label="Sort by ${esc(c.label)}">${esc(c.label)}<span aria-hidden="true">${on ? (t.dir > 0 ? " ↑" : " ↓") : ""}</span></button>` : esc(c.label)) : "";
    return `<th class="${c.num ? "n" : ""}" ${on ? `aria-sort="${t.dir > 0 ? "ascending" : "descending"}"` : ""}>${lbl}</th>`; }).join("")}</tr></thead>`;
}
function tbody(id) {
  const t = TABLES[id]; let rows = t.rows;
  if (t.q) { const q = t.q.toLowerCase(); rows = rows.filter(r => JSON.stringify(r, (k, v) => k === "sources" || k === "blob" ? undefined : v).toLowerCase().includes(q)); }
  if (t.sort !== null) { const c = t.cols[t.sort], key = c.sort || (r => r[c.k]); rows = [...rows].sort((a, b) => { const x = key(a), y = key(b); return (x === y ? 0 : x === null || x === undefined ? 1 : y === null || y === undefined ? -1 : x > y ? 1 : -1) * t.dir; }); }
  if (t.pin) rows = [...rows.filter(t.pin), ...rows.filter(r => !t.pin(r))];
  if (!rows.length) return `<tr><td colspan="${t.vis.length}" class="empty">Nothing matches.</td></tr>`;
  const more = t.cols.filter(c => c.more);
  return rows.map(r => {
    let attr = t.rowAttr ? t.rowAttr(r) : "", cls = t.pin?.(r) ? "pin" : "";
    attr = attr.replace(/\bclass="([^"]*)"/, (_, c) => { cls += " " + c; return ""; });
    const sub = more.map(c => cellHtml(c, r)).filter(h => !emptyCell(h));
    return `<tr class="${cls.trim()}" ${attr}>${t.vis.map(i => { const c = t.cols[i], h = cellHtml(c, r);
      if (i === t.ti) return `<td class="tt">${h}${sub.length ? `<div class="sub">${sub.join(" · ")}</div>` : ""}</td>`;
      const btn = /<button|class="btn/.test(h);
      const nil = emptyCell(h);
      return `<td class="${c.num ? "n" : ""}${btn ? " acts" : ""}${nil ? " nil" : ""}" ${c.label && !btn ? `data-l="${esc(c.label)}"` : ""}>${nil ? (c.label ? `<span class="muted">–</span>` : "") : `<span class="tv">${h}</span>`}</td>`; }).join("")}</tr>`;
  }).join("");
}
function redrawTable(id) { const el = $(`#t-${id}`); if (el) el.innerHTML = thead(id) + `<tbody>${tbody(id)}</tbody>`; }
document.addEventListener("click", e => {
  const b = e.target.closest("[data-tsort]"); if (!b) return;
  const t = TABLES[b.dataset.tsort]; if (!t) return; const i = +b.dataset.col;
  if (t.sort === i) t.dir = -t.dir; else { t.sort = i; t.dir = t.cols[i].num ? -1 : 1; }
  redrawTable(b.dataset.tsort);
});
document.addEventListener("input", e => { const id = e.target.dataset.tsearch; if (!id || !TABLES[id]) return; TABLES[id].q = e.target.value; const el = $(`#t-${id} tbody`); if (el) el.innerHTML = tbody(id); });

const yes = (b, good = true) => b === null || b === undefined ? `<span class="muted">–</span>` : `<span class="tag ${b === good ? "good" : "bad"}">${b ? "on" : "off"}</span>`;
// ⓘ next to a page title: the page's explanation, shown on click instead of a sentence under every title
const hint = t => `<button class="hint" type="button" data-hint="${esc(t)}" aria-label="About this page" aria-expanded="false">i</button>`;
document.addEventListener("click", e => {
  const b = e.target.closest(".hint"); const open = $(".hint-pop");
  if (open && (!b || open.dataset.for !== b.dataset.hint)) { open.remove(); $$(".hint[aria-expanded=true]").forEach(x => x.setAttribute("aria-expanded", "false")); if (!b) return; }
  if (!b) return; e.preventDefault();
  if (open) return;
  const pop = document.createElement("div"); pop.className = "hint-pop"; pop.dataset.for = b.dataset.hint; pop.textContent = b.dataset.hint; pop.setAttribute("role", "note");
  b.closest("h1, h2, .head")?.insertAdjacentElement("afterend", pop); b.setAttribute("aria-expanded", "true");
});
// charts that support a list fold away under "Insights"; open or closed is remembered per page in this browser
const insightsOpen = k => { try { return localStorage.getItem("homeowl-ins-" + k) === "1"; } catch { return false; } };
const insights = (k, html, label = "Insights") => `<details class="insights section" data-ins="${k}" ${insightsOpen(k) ? "open" : ""}><summary>${esc(label)}</summary><div class="ins-body">${html}</div></details>`;
document.addEventListener("toggle", e => { const k = e.target.dataset?.ins; if (k) try { localStorage.setItem("homeowl-ins-" + k, e.target.open ? "1" : "0"); } catch { } }, true);
// a thin bar split by severity, with counts: the "how bad is it" picture next to the status line
function sevStrip(F) {
  const c = SEV_ORDER.map(s => [s, F.filter(f => f.sev === s).length]).filter(([, n]) => n), tot = c.reduce((a, [, n]) => a + n, 0);
  if (!tot) return "";
  return `<div class="sevstrip" role="img" aria-label="${c.map(([s, n]) => n + " " + s).join(", ")}">${c.map(([s, n]) => `<span class="ss-${s}" style="flex:${n}"></span>`).join("")}</div>
    <div class="sevleg">${c.map(([s, n]) => `<span><i class="ss-${s}"></i>${n} ${s}</span>`).join("")}</div>`;
}
// an empty page that says what you'll get and offers the one button that gets you there
const emptyState = ({ title, text = "", points = [], btn = "", note = "" }) => `<div class="card empty-state"><h2>${esc(title)}</h2>${text ? `<p class="muted">${esc(text)}</p>` : ""}
  ${points.length ? `<ul class="es-points">${points.map(p => `<li>${esc(p)}</li>`).join("")}</ul>` : ""}${btn ? `<div class="es-btn">${btn}</div>` : ""}${note ? `<p class="fine">${esc(note)}</p>` : ""}</div>`;
// open Settings on one section
document.addEventListener("click", e => { const b = e.target.closest("[data-setgo]"); if (!b) return; e.preventDefault(); S.setTab = b.dataset.setgo; try { localStorage.setItem("homeowl-settings-tab", S.setTab); } catch { } S.view = "settings"; render(); window.scrollTo(0, 0); });
const sevTag = s => `<span class="sev ${s}">${s}</span>`;

// ------------------------------------------------------------------ diagrams
function physicalDiagram(m) {
  const devs = m.devices.filter(d => !d.partial || d.uplinkMac); if (!devs.length) return `<div class="empty">No devices returned</div>`;
  const byMac = Object.fromEntries(devs.map(d => [d.mac, d])), byId = Object.fromEntries(devs.map(d => [d.id, d]));
  const root = devs.find(d => d.role === "gw") || devs[0];
  const parent = new Map();
  devs.forEach(d => { if (d === root) return; let p = (d.uplinkMac && byMac[d.uplinkMac]) || (d.uplinkId && byId[d.uplinkId]) || root; if (p === d) p = root; parent.set(d, p); });
  devs.forEach(d => { const seen = new Set([d]); let p = parent.get(d); while (p && p !== root) { if (seen.has(p)) { parent.set(d, root); break; } seen.add(p); p = parent.get(p); } });
  const kids = new Map(devs.map(d => [d, []])); devs.forEach(d => { if (parent.get(d)) kids.get(parent.get(d)).push(d); });
  const order = { gw: 0, sw: 1, other: 2, ups: 2, nvr: 2, ap: 3, access: 4, camera: 4 }; kids.forEach(v => v.sort((a, b) => order[a.role] - order[b.role] || a.name.localeCompare(b.name)));
  const NW = 168, NH = 62, XG = 18, YG = 64, pos = new Map(); let slot = 0;
  const place = (d, depth) => { const k = kids.get(d); if (!k.length) { pos.set(d, { x: slot++ * (NW + XG), y: depth }); return; } k.forEach(c => place(c, depth + 1)); const xs = k.map(c => pos.get(c).x); pos.set(d, { x: (Math.min(...xs) + Math.max(...xs)) / 2, y: depth }); };
  place(root, 1);
  const maxDepth = Math.max(...[...pos.values()].map(p => p.y));
  const W = Math.max(slot * (NW + XG), NW + 40) + 40, H = (maxDepth + 1) * (NH + YG) + 40;
  const P = d => { const p = pos.get(d); return { x: p.x + 20, y: p.y * (NH + YG) + 20 }; };
  const icon = { gw: "⬢", sw: "▤", ap: "◉", ups: "⚡", camera: "◎", nvr: "▣", access: "⊟", other: "◇" };
  const col = { gw: "var(--c1)", sw: "var(--c2)", ap: "var(--c4)", ups: "var(--c5)", camera: "var(--c6)", nvr: "var(--c6)", access: "var(--c3)", other: "var(--c7)" };
  let edges = "", nodes = "";
  const rp = P(root), inet = { x: rp.x, y: 20 };
  edges += `<path class="edge" d="M${inet.x + NW / 2},${inet.y + 40} L${rp.x + NW / 2},${rp.y}"/>`;
  nodes += `<g class="node wan"><rect x="${inet.x + NW / 2 - 70}" y="${inet.y}" width="140" height="40" rx="20"/><text class="t1" x="${inet.x + NW / 2}" y="${inet.y + 17}" text-anchor="middle">Internet</text><text class="t2" x="${inet.x + NW / 2}" y="${inet.y + 31}" text-anchor="middle">${esc(m.info.isp || m.info.wanIp || m.wans.map(w => w.name).join(" + ") || "WAN")}</text></g>`;
  devs.forEach(d => {
    const p = P(d), par = parent.get(d);
    if (par) { const q = P(par); edges += `<path class="edge ${d.wireless ? "wl" : ""}" d="M${q.x + NW / 2},${q.y + NH} C${q.x + NW / 2},${q.y + NH + YG / 2} ${p.x + NW / 2},${p.y - YG / 2} ${p.x + NW / 2},${p.y}"/>${d.uplinkPort ? `<text class="t2" x="${p.x + NW / 2 + 5}" y="${p.y - 6}">port ${d.uplinkPort}</text>` : ""}`; }
    nodes += `<g class="node ${d.role} ${d.online ? "" : "off"}" data-dev="${esc(d.mac)}" style="cursor:pointer"><title>${esc(d.name)} — ${esc(d.model || "")}\n${esc(d.ip || "")}  ${esc(d.mac)}\n${esc(d.stateLabel)} · firmware ${esc(d.version || "?")}${d.upgradable ? " (update available)" : ""}\nClick for details</title>
      <rect x="${p.x}" y="${p.y}" width="${NW}" height="${NH}" rx="10"/>
      <text class="ic" x="${p.x + 12}" y="${p.y + 22}" style="fill:${col[d.role] || col.other}">${icon[d.role] || icon.other}</text>
      <text class="t1" x="${p.x + 30}" y="${p.y + 21}">${esc(d.name.slice(0, 18))}</text>
      <text class="t2" x="${p.x + 30}" y="${p.y + 36}">${esc((d.model || "").slice(0, 22))}</text>
      <text class="t2" x="${p.x + 30}" y="${p.y + 51}">${d.online ? esc(d.ip || "") + (d.clients ? ` · ${plural(d.clients, "client")}` : "") : `<tspan style="fill:var(--crit)">${esc(d.stateLabel)}</tspan>`}</text>
      ${d.upgradable ? `<circle class="badge" cx="${p.x + NW - 8}" cy="${p.y + 8}" r="7"/><text class="badge-t" x="${p.x + NW - 8}" y="${p.y + 11}" text-anchor="middle">↑</text>` : ""}</g>`;
  });
  const orphans = m.devices.filter(d => d.partial && !d.uplinkMac);
  return `<div class="diagram"><svg width="${W}" height="${H}" viewBox="0 0 ${W} ${H}">${edges}${nodes}</svg></div>
    ${orphans.length ? `<p class="fine">Not placed (uplink unknown): ${orphans.map(d => `<a href="#" data-dev="${esc(d.mac)}">${esc(d.name)}</a> (${esc(d.stateLabel)})`).join(", ")}</p>` : ""}
    <div class="legend"><span><i style="background:var(--c1)"></i>Gateway</span><span><i style="background:var(--c2)"></i>Switch</span><span><i style="background:var(--c4)"></i>Access point</span><span><i style="background:var(--c5)"></i>UPS</span><span>- - - wireless uplink</span><span><i style="background:var(--high)"></i>firmware update</span><span>red dashed = offline · click a device to debug</span></div>`;
}
function logicalDiagram(m) {
  const nets = m.networks.filter(n => n.enabled), ws = m.wlans.filter(w => w.enabled);
  if (!nets.length) return `<div class="empty">No networks returned</div>`;
  const NW = 196, NH = 56, COLX = [20, 250, 490, 760], RG = 16;
  const wans = m.wans.length ? m.wans : [{ name: "WAN" }];
  const rows = Math.max(nets.length, ws.length, wans.length);
  const H = rows * (NH + RG) + 40, W = COLX[3] + NW + 20;
  const y = (i, n) => 20 + i * (NH + RG) + (rows - n) * (NH + RG) / 2;
  let e = "", g = "";
  const box = (cls, x, yy, t1, t2, t3, title = "") => `<g class="node ${cls}"><title>${esc(title)}</title><rect x="${x}" y="${yy}" width="${NW}" height="${NH}" rx="10"/><text class="t1" x="${x + 12}" y="${yy + 19}">${esc(t1)}</text><text class="t2" x="${x + 12}" y="${yy + 34}">${esc(t2)}</text><text class="t2" x="${x + 12}" y="${yy + 48}">${esc(t3 || "")}</text></g>`;
  const curve = (x1, y1, x2, y2, cls = "") => `<path class="edge ${cls}" d="M${x1},${y1} C${(x1 + x2) / 2},${y1} ${(x1 + x2) / 2},${y2} ${x2},${y2}"/>`;
  const gy = y(0, 1);
  wans.forEach((w, i) => { const yy = y(i, wans.length); g += box("wan", COLX[0], yy, w.name || "WAN", w.type ? `${w.type}` : (m.info.isp || ""), w.ip || m.info.wanIp || ""); e += curve(COLX[0] + NW, yy + NH / 2, COLX[1], gy + NH / 2); });
  g += box("gw", COLX[1], gy, m.info.gwName || "Gateway", m.info.gwModel || "", m.info.gwIp || "");
  const netY = {};
  nets.forEach((n, i) => {
    const yy = y(i, nets.length); netY[n.id] = yy;
    const flags = [n.guest ? "guest" : null, n.isolated ? "isolated" : null, n.vpn ? "vpn" : null].filter(Boolean).join(" · ");
    g += box(n.guest ? "net ssid" : "net", COLX[2], yy, n.vpn ? `${n.name}  ·  VPN` : `${n.name}  ·  VLAN ${n.vlan ?? 1}`, n.subnet || "", `${plural(n.clients || 0, "client")}${flags ? " · " + flags : ""}`, `${n.name}\nGateway ${n.gwIp || "?"}\nDHCP ${n.dhcp ? (n.dhcpStart + " – " + n.dhcpStop) : "off"}`);
    e += curve(COLX[1] + NW, gy + NH / 2, COLX[2], yy + NH / 2);
  });
  ws.forEach((w, i) => {
    const yy = y(i, ws.length);
    g += box("ssid", COLX[3], yy, `📶 ${w.name}`, `${w.sec} · ${w.bands.map(b => bandOf(b)).join("/")} GHz`, `${plural(w.clients || 0, "client")}${w.hidden ? " · hidden" : ""}${w.guest ? " · guest" : ""}`);
    const ny = netY[w.networkId] ?? netY[m.defaultNet?.id];
    if (ny !== undefined) e += curve(COLX[2] + NW, ny + NH / 2, COLX[3], yy + NH / 2, "wl");
  });
  const head = ["Internet", "Gateway", "Networks / VLANs", "Wi-Fi (SSIDs)"].map((t, i) => `<text class="t2" x="${COLX[i]}" y="12" style="font-weight:700;text-transform:uppercase;letter-spacing:.05em">${t}</text>`).join("");
  return `<div class="diagram"><svg width="${W}" height="${H + 10}" viewBox="0 0 ${W} ${H + 10}">${head}${e}${g}</svg></div>`;
}
