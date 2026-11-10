/* HomeOwl — NextDNS & Tailscale page. Depends on core.js + app.js */
"use strict";

const DNSCFG_KEY = "unifi-audit-dnsvpn";
function dnsCfg() { try { return JSON.parse(localStorage.getItem(DNSCFG_KEY) || "{}"); } catch { return {}; } }
function saveDnsCfg(c) { try { localStorage.setItem(DNSCFG_KEY, JSON.stringify(c)); } catch { } }
const netCidr = n => { const c = cidr(n.subnet); return c ? `${intToIp(c.net)}/${c.bits}` : null; };
const PROFILE_RE = /^[0-9a-f]{6}$/i;

function nextdnsScript(m, c) {
  const gw = m.info.gwIp || (m.info.host || "").replace(/^https?:\/\//, "") || "<gateway-ip>";
  const lines = [];
  const prof = [];
  for (const n of m.networks.filter(n => !n.vpn && n.subnet)) { const id = (c.net?.[n.id] || "").trim(); if (id && id !== c.def) prof.push(`  -profile ${netCidr(n)}=${id} \\`); }
  for (const o of c.dev || []) if (o.mac && o.id) prof.push(`  -profile ${o.mac}=${o.id} \\`);
  lines.push(`# 1) On your computer — open a shell on the gateway (enable SSH first: UniFi → Settings → Control Plane → Console → SSH)`);
  lines.push(`ssh root@${gw}`, "");
  lines.push(`# 2) On the gateway — install (or choose "Upgrade") the NextDNS CLI.`);
  lines.push(`#    When asked "Report device name?" answer YES. Use profile ${c.def || "<default-profile>"} when asked.`);
  lines.push(`sh -c 'sh -c "$(curl -sL https://nextdns.io/install)"'`, "");
  lines.push(`# 3) Per-network / per-device profiles + device names`);
  lines.push(`nextdns config set \\`, `  -report-client-info=true \\`, ...prof, `  -profile ${c.def || "<default-profile>"}`);
  lines.push(`nextdns restart`, "");
  lines.push(`# 4) Check`);
  lines.push(`nextdns config     # shows the active settings`);
  lines.push(`nextdns log        # live query log`);
  lines.push("", `# After a UniFi OS firmware update: re-run step 2 (choose "Upgrade"/"Install") if NextDNS stops logging.`);
  return lines.join("\n");
}

function tailscaleScript(m, c) {
  const nets = m.networks.filter(n => !n.vpn && n.subnet && (c.tsNets || {})[n.id]).map(netCidr).filter(Boolean);
  const routes = nets.join(",") || "<subnet,subnet>";
  const gw = m.info.gwIp || "<gateway-ip>";
  if (c.tsMode === "device") {
    const dev = c.tsDevice || "<router-device>";
    return [`# On the subnet-router device (${dev}) — Linux example`, `echo 'net.ipv4.ip_forward = 1' | sudo tee -a /etc/sysctl.d/99-tailscale.conf`,
      `echo 'net.ipv6.conf.all.forwarding = 1' | sudo tee -a /etc/sysctl.d/99-tailscale.conf`, `sudo sysctl -p /etc/sysctl.d/99-tailscale.conf`,
      `sudo tailscale up --advertise-routes=${routes} --accept-routes${c.tsExit ? " --advertise-exit-node" : ""}`, "",
      `# Then in the Tailscale admin console: Machines → ${dev} → ⋯ → Edit route settings → approve ${routes}`,
      `# (Synology/QNAP/Home Assistant: use their Tailscale package/add-on and set the same advertised routes there.)`].join("\n");
  }
  return [`# Runs Tailscale ON the UniFi gateway using the community project SierraSoftworks/tailscale-unifi (not made by Ubiquiti or Tailscale).`,
    `# Do this while you're on the LAN, not over a VPN.`, `ssh root@${gw}`,
    `curl -sSLq https://raw.githubusercontent.com/SierraSoftworks/tailscale-unifi/main/install.sh | sh`,
    `ip link show tailscale0          # must exist (TUN mode) to route subnets`,
    `tailscale up --advertise-routes=${routes} --snat-subnet-routes=false --accept-routes${c.tsExit ? " --advertise-exit-node" : ""} --reset`, `tailscale status`, "",
    `# Then in the Tailscale admin console: Machines → (gateway) → ⋯ → Edit route settings → approve ${routes}`,
    `# After UniFi firmware updates: ssh in and run 'tailscale status'; if it's gone, re-run the install line.`].join("\n");
}

function tsPolicySnippet(c) {
  const rows = (c.tsRules || []).filter(r => r.target && PROFILE_RE.test(r.id || ""));
  const attrs = rows.map(r => ({ target: [r.target.trim()], attr: [`nextdns:${r.id.trim()}`] }));
  if (c.tsNoInfo) attrs.push({ target: ["*"], attr: ["nextdns:no-device-info"] });
  return JSON.stringify({ nodeAttrs: attrs }, null, 2);
}

const copyBox = (id, text) => `<div class="codebox"><button class="btn sm copy" data-copy="${id}">Copy</button><pre id="${id}" class="json">${esc(text)}</pre></div>`;
document.addEventListener("click", async e => { const b = e.target.closest("[data-copy]"); if (!b) return; const t = $("#" + b.dataset.copy)?.textContent || ""; try { await navigator.clipboard.writeText(t); toast("Copied", "ok"); } catch { toast("Couldn't copy — select the text manually", "err"); } });

V.dns = m => {
  const c = dnsCfg();
  const lans = m.networks.filter(n => !n.vpn && n.enabled);
  const mode = net => net.dns.length ? net.dns.map(dnsProvider) : [dnsProvider(null)];
  const ndDirect = lans.filter(n => n.dns.some(ip => dnsProvider(ip).kind === "nextdns"));
  const filters = lans.filter(n => n.filters?.length);
  const unnamed = m.clients.filter(c2 => !c2.hostname);
  const aliasDiff = m.clients.filter(c2 => c2.name && c2.hostname && c2.name !== c2.hostname);
  const live = S.source === "live" && m.hasLegacy;
  const gwIp = m.info.gwIp || "";
  const tsDevs = m.known.filter(k => k.fixedIp).concat(m.devices.filter(d => d.ip).map(d => ({ label: d.name, fixedIp: d.ip })));
  const tsSel = tsDevs.find(d => d.label === c.tsDevice);
  const domain = (lans.find(n => n.domain)?.domain) || "localdomain";

  return `<div class="head"><div><h1>NextDNS &amp; Tailscale</h1><div class="muted">How DNS reaches NextDNS today, how to get real device names and per-network profiles, and how to connect your networks to Tailscale.</div></div>
    <button class="btn" id="btn-ndtest">${S.remote ? "Test the HomeOwl computer's DNS" : "Test this computer's DNS"}</button></div>
  <div id="ndtest-out"></div>

  <div class="card"><h2>Today: where each network's DNS goes</h2>
    ${table("dnsnow", [
      { k: "name", label: "Network" }, { k: "subnet", label: "Subnet", fmt: r => `<span class="mono">${esc(netCidr(r) || "–")}</span>` },
      { k: "dns", label: "DHCP hands out", fmt: r => r.dns.length ? `<span class="mono">${r.dns.map(esc).join(", ")}</span>` : `<span class="muted">gateway (${esc(r.gwIp || "")})</span>` },
      { k: "prov", label: "Resolver", fmt: r => mode(r).map(p => `<span class="tag ${p.kind === "nextdns" ? "warn" : ""}">${esc(p.name)}</span>`).join(" ") },
      { k: "names", label: "Device names in NextDNS?", fmt: r => r.dns.some(ip => dnsProvider(ip).kind === "nextdns") ? `<span class="bad-t">No — all look like one source</span>` : r.dns.length ? `<span class="muted">n/a</span>` : `<span class="muted">Only if the NextDNS CLI runs on the gateway</span>` },
      { k: "a", label: "", fmt: r => r.dns.length ? actBtn({ kind: "rest", resource: "networkconf", id: r.id, fields: { dhcpd_dns_enabled: false }, description: `“${r.name}”: hand out the gateway as DNS (needed for the NextDNS CLI)` }, "Use gateway DNS") : "" },
    ], lans)}
    <dl class="kv" style="margin-top:12px"><dt>Gateway Encrypted DNS</dt><dd>${esc(m.settings.doh ?? "unknown")}${m.settings.dohNextdns ? ` <span class="tag warn">NextDNS — shows as one device</span>` : ""} ${m.settings.doh && m.settings.doh !== "off" ? actBtn({ kind: "setting", key: "doh", fields: { state: "off" }, description: "Turn the gateway's Encrypted DNS off (the NextDNS CLI replaces it)" }, "Turn off") : ""}</dd>
      <dt>Content filtering / ad blocking</dt><dd>${filters.length || m.settings.adblock ? `<span class="tag bad">on</span> ${filters.map(n => esc(n.name)).join(", ")} — turn off before using the NextDNS CLI (they conflict)` : `<span class="tag good">off</span>`}</dd></dl></div>

  <div class="card section"><h2>Ways to use NextDNS with UniFi</h2>
    <div class="tbl-wrap"><table><thead><tr><th>Method</th><th>Device names</th><th>Per-network profiles</th><th>Notes</th></tr></thead><tbody>
      <tr><td><b>NextDNS CLI on the gateway</b> (recommended)</td><td class="ok-t">Yes</td><td class="ok-t">Yes (by subnet or MAC)</td><td>Runs on the UCG/UDM via SSH. Disable UniFi content filtering &amp; ad blocking. Re-run the installer after firmware updates if logging stops. Queries made by the gateway itself don't go through it.</td></tr>
      <tr><td>UniFi Encrypted DNS (custom DoH / DNS stamp)</td><td class="bad-t">No — one device</td><td class="bad-t">No</td><td>No SSH needed, survives updates, but NextDNS only sees the gateway.</td></tr>
      <tr><td>DHCP DNS = NextDNS linked IP / IPv6 endpoint</td><td class="bad-t">No</td><td>Partly (one profile per IPv6 endpoint)</td><td>Easy, but every client looks the same; linked IP must be kept up to date.</td></tr>
      <tr><td>Tailscale's NextDNS integration</td><td class="ok-t">Yes (Tailscale names)</td><td class="ok-t">Yes (by tag/user/group)</td><td>Only for devices running Tailscale. Sends device name, OS and Tailscale IP by default.</td></tr>
    </tbody></table></div></div>

  <div class="card section"><h2>1 · NextDNS CLI on the gateway — device names + profile per network</h2>
    <div class="grid g2"><div>
      <label>Default NextDNS profile ID <input data-dns="def" value="${esc(c.def || "")}" placeholder="e.g. abc123"></label>
      ${lans.map(n => `<label>${esc(n.name)} <span class="muted mono">${esc(netCidr(n) || "")}</span><input data-dns="net.${esc(n.id)}" value="${esc(c.net?.[n.id] || "")}" placeholder="same as default"></label>`).join("")}
      <h3>Per-device overrides (optional)</h3>
      <div id="dev-ovr">${(c.dev || []).map((o, i) => `<div class="ovr"><select data-dns="dev.${i}.mac">${m.known.concat(m.clients.filter(x => !m.knownByMac[x.mac])).map(k => `<option value="${esc(k.mac)}" ${k.mac === o.mac ? "selected" : ""}>${esc(k.label)} (${esc(k.mac)})</option>`).join("")}</select><input data-dns="dev.${i}.id" value="${esc(o.id || "")}" placeholder="profile"><button class="btn sm" data-dnsdel="${i}">×</button></div>`).join("")}</div>
      <button class="btn sm" id="btn-add-ovr">+ Add device override</button>
      ${c.def && !PROFILE_RE.test(c.def) ? `<p class="bad-t">Profile IDs are 6 characters (letters a–f and digits), shown at the top of my.nextdns.io.</p>` : ""}
    </div><div>
      <h3>Commands</h3>${copyBox("nd-script", nextdnsScript(m, c))}
      <p class="fine">Prerequisites: ${ndDirect.length ? `switch <b>${ndDirect.map(n => esc(n.name)).join(", ")}</b> to “Use gateway DNS” (table above) <i>after</i> the CLI is running, so clients ask the gateway; ` : ""}${m.settings.doh && m.settings.doh !== "off" ? "turn the gateway's Encrypted DNS off; " : ""}disable content filtering / ad blocking. Then click <b>Test this computer's DNS</b> — it should report your profile.</p>
    </div></div>
    <h3 style="margin-top:12px">How your devices will be named</h3>
    <p class="muted">The CLI discovers names on the LAN (e.g. the hostname a device sends when it gets its IP). It does not use the names you typed in UniFi, so devices that don't announce a hostname show up by IP.</p>
    <div class="grid g2"><div class="card kpi"><div class="l">Active clients with no hostname</div><div class="v">${unnamed.length}</div><div class="s">${unnamed.slice(0, 8).map(x => esc(x.label)).join(", ")}${unnamed.length > 8 ? "…" : ""}</div></div>
      <div class="card kpi"><div class="l">UniFi name differs from the device's hostname</div><div class="v">${aliasDiff.length}</div><div class="s">${aliasDiff.slice(0, 6).map(x => `${esc(x.hostname)} → ${esc(x.name)}`).join(", ")}${aliasDiff.length > 6 ? "…" : ""}</div></div></div>
    <p class="fine">To make these match, rename the device itself (phone/computer settings → device name), or give it a dedicated profile with a per-device override above.</p></div>

  <div class="card section"><h2>2 · Connect your networks to Tailscale</h2>
    <div class="grid g2"><div>
      <label>Where should Tailscale run?<select data-dns="tsMode">
        <option value="gateway" ${c.tsMode !== "device" ? "selected" : ""}>On the UniFi gateway (community tailscale-unifi)</option>
        <option value="device" ${c.tsMode === "device" ? "selected" : ""}>On another always-on device (NAS, Raspberry Pi, server)</option></select></label>
      ${c.tsMode === "device" ? `<label>Subnet-router device<select data-dns="tsDevice"><option value="">— choose —</option>${tsDevs.map(d => `<option ${d.label === c.tsDevice ? "selected" : ""}>${esc(d.label)}</option>`).join("")}</select></label>
        <p class="fine">Pick a device with a fixed IP (📌 in Clients). Not listed? Give it a fixed IP first.</p>` : ""}
      <h3>Networks to make reachable from Tailscale</h3>
      ${lans.map(n => `<label class="check"><input type="checkbox" data-dns="tsNets.${esc(n.id)}" ${(c.tsNets || {})[n.id] ? "checked" : ""}> <span>${esc(n.name)} <span class="muted mono">${esc(netCidr(n) || "")}</span>${n.guest ? " <span class='tag warn'>guest</span>" : ""}</span></label>`).join("")}
      <label class="check"><input type="checkbox" data-dns="tsExit" ${c.tsExit ? "checked" : ""}> <span>Also offer it as an exit node (browse via home internet when away)</span></label>
    </div><div><h3>Commands</h3>${copyBox("ts-script", tailscaleScript(m, c))}
      ${c.tsMode === "device" && tsSel ? `<p>So LAN devices without Tailscale can reach 100.x Tailscale addresses, add a route on the UniFi gateway: ${actBtn({ kind: "route", network: "100.64.0.0/10", nexthop: tsSel.fixedIp, name: "Tailscale via " + tsSel.label, description: `Static route 100.64.0.0/10 → ${tsSel.label} (${tsSel.fixedIp})` }, `Add route 100.64.0.0/10 → ${tsSel.fixedIp}`)}</p>` : ""}
      <p class="fine">Don't advertise the Guest network unless you mean to. Overlapping subnets break routing — if a remote location also uses ${esc(netCidr(lans[0]) || "your subnet")}, renumber one side.</p></div></div></div>

  <div class="card section"><h2>3 · NextDNS for your Tailscale devices</h2>
    <ol class="dx"><li>Tailscale admin console → <b>DNS</b> → <b>Add nameserver</b> → <b>NextDNS</b> → paste the IPv6 address from the <i>Endpoints</i> section of your NextDNS profile's Setup tab → Save.</li>
      <li>Optional: turn on <b>Override DNS servers</b> so tailnet devices always use it. Don't add a second global nameserver next to NextDNS.</li>
      <li>Device names, OS and Tailscale IP are sent to NextDNS automatically — nothing else to do for names.</li>
      <li>Different profiles per person or device: paste the snippet below into the tailnet policy file (Access controls).</li>
      <li>So LAN names (e.g. <span class="mono">nas.${esc(domain)}</span>) still resolve away from home: DNS → Add nameserver → Custom → <span class="mono">${esc(gwIp || "gateway IP")}</span> → turn on <b>Restrict to domain</b> → <span class="mono">${esc(domain)}</span>. This needs the gateway's subnet advertised (section 2).</li></ol>
    <div class="grid g2"><div>
      <h3>Profile per tag / user / group</h3>
      <div id="ts-rules">${(c.tsRules || [{}]).map((r, i) => `<div class="ovr"><input data-dns="tsRules.${i}.target" value="${esc(r.target || "")}" placeholder="tag:kids · group:family · alice@example.com"><input data-dns="tsRules.${i}.id" value="${esc(r.id || "")}" placeholder="profile"><button class="btn sm" data-tsdel="${i}">×</button></div>`).join("")}</div>
      <button class="btn sm" id="btn-add-tsrule">+ Add rule</button>
      <label class="check" style="margin-top:10px"><input type="checkbox" data-dns="tsNoInfo" ${c.tsNoInfo ? "checked" : ""}> <span>Don't send device names to NextDNS (privacy)</span></label>
    </div><div><h3>Policy file snippet</h3>${copyBox("ts-policy", tsPolicySnippet(c))}<p class="fine">Merge the <span class="mono">nodeAttrs</span> entries into your existing policy file.</p></div></div></div>`;
};

// ---- input handling (state kept in this browser only)
function setPath(o, path, v) { const ks = path.split("."); let x = o; ks.slice(0, -1).forEach((k, i) => { if (x[k] === undefined) x[k] = /^\d+$/.test(ks[i + 1]) ? [] : {}; x = x[k]; }); x[ks[ks.length - 1]] = v; }
document.addEventListener("change", e => {
  const el = e.target.closest("[data-dns]"); if (!el) return;
  const c = dnsCfg(); setPath(c, el.dataset.dns, el.type === "checkbox" ? el.checked : el.value.trim()); saveDnsCfg(c);
  const y = window.scrollY; render(); window.scrollTo(0, y);
});
document.addEventListener("click", e => {
  const id = e.target.id; let c;
  if (id === "btn-add-ovr") { c = dnsCfg(); (c.dev ||= []).push({ mac: (S.model.known[0] || S.model.clients[0] || {}).mac || "", id: "" }); saveDnsCfg(c); }
  else if (id === "btn-add-tsrule") { c = dnsCfg(); (c.tsRules ||= []).push({}); saveDnsCfg(c); }
  else if (e.target.dataset.dnsdel !== undefined) { c = dnsCfg(); c.dev.splice(+e.target.dataset.dnsdel, 1); saveDnsCfg(c); }
  else if (e.target.dataset.tsdel !== undefined) { c = dnsCfg(); c.tsRules.splice(+e.target.dataset.tsdel, 1); saveDnsCfg(c); }
  else if (id === "btn-ndtest") return ndTest();
  else return;
  const y = window.scrollY; render(); window.scrollTo(0, y);
});
async function ndTest() {
  const out = $("#ndtest-out"); out.innerHTML = `<div class="card">Asking test.nextdns.io…</div>`;
  try {
    const r = await api("/api/nextdns-test", {});
    if (!r.ok) { out.innerHTML = `<div class="card"><span class="bad-t">Couldn't reach test.nextdns.io: ${esc(r.error)}</span></div>`; return; }
    const d = r.data || {}, using = d.status === "ok";
    out.innerHTML = `<div class="card" style="border-color:${using ? "var(--ok)" : "var(--high)"}"><h2>${using ? "✓ The computer running HomeOwl is using NextDNS" : "✗ The computer running HomeOwl is not using NextDNS"}</h2>
      <dl class="kv">${Object.entries(d).map(([k, v]) => `<dt>${esc(k)}</dt><dd class="mono">${esc(typeof v === "object" ? JSON.stringify(v) : v)}</dd>`).join("")}</dl>
      <p class="fine">If Tailscale is connected with NextDNS as its nameserver, this shows the Tailscale path rather than the LAN path.</p></div><div class="section"></div>`;
  } catch (err) { out.innerHTML = `<div class="card bad-t">${esc(err.message)}</div>`; }
}
