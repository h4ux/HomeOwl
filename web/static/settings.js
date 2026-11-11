/* HomeOwl — Settings (admins): UniFi connection, access (home network + built-in Tailscale), users, AI & MCP, analytics. */
"use strict";

V.settings = () => `<div class="head"><div><h1>Settings</h1></div></div><div id="settings-body"><div class="empty">Loading…</div></div>`;

const MCP_MODES = [
  ["off", "Off", "No AI assistant or MCP client can connect."],
  ["read", "Read only", "Look up devices, clients, Wi-Fi, firewall, events and usage analytics. Can't change anything."],
  ["guarded", "Read + guarded changes", "Also the allow-listed changes the app offers (block a client, restart a device, toggle Wi-Fi…), each previewed first, logged and undoable."],
  ["god", "Full access (god mode)", "Also raw requests to any UniFi API endpoint — no allow-list, no preview, no undo. Only for experienced users."],
];

async function loadSettings() {
  const el = $("#settings-body"); if (!el) return;
  let se; try { se = await api("/api/settings"); } catch (err) { el.innerHTML = `<div class="empty bad-t">${esc(err.message)}</div>`; return; }
  S.settings = se;
  const u = se.unifi, lan = se.lan, ts = se.tailscale, st = ts.status || {}, mcp = se.mcp;
  const tsState = !ts.enabled ? `<span class="tag">off</span>` : st.state === "Running" ? `<span class="tag good">online</span>` : st.state === "NeedsLogin" ? `<span class="tag warn">waiting for approval</span>` : `<span class="tag">${esc(st.state || "starting…")}</span>`;
  el.innerHTML = `
  <div class="set-layout"><nav class="set-nav" id="set-nav" aria-label="Settings sections"></nav><div class="set-panes">
  <div class="spane" data-spane="connection">
    <div class="card" id="st-unifi"><h2>UniFi connection</h2>
      <p>${u.connected ? `<span class="tag good">connected</span> <span class="mono">${esc(u.host)}</span>` : `<span class="tag bad">not connected</span>`}</p>
      ${u.last_error && !u.connected ? `<div class="msg err">${esc(u.last_error)}</div>` : ""}
      <form id="st-connect" autocomplete="off">
        <label>Console IP or hostname<input id="st-host" value="${esc((u.host || "").replace(/^https:\/\//, ""))}" placeholder="192.168.1.1" required></label>
        <label>API key<input id="st-key" type="password" placeholder="${u.key_saved ? "Saved — leave empty to keep it" : "Paste the key"}"></label>
        <details class="adv"><summary>Advanced</summary>
          <label>Site (optional — id, name or internal reference)<input id="st-site" value="${esc(u.site || "")}" placeholder="default"></label>
          <label class="check"><input type="checkbox" id="st-verify" ${u.verify_tls ? "checked" : ""}> <span>Verify the console's TLS certificate (leave off for self-signed consoles)</span></label></details>
        <div class="btnrow"><button class="btn primary" type="submit">${u.connected ? "Save &amp; reconnect" : "Connect"}</button>${u.connected ? `<button class="btn" type="button" id="st-disconnect">Disconnect</button>` : ""}</div>
      </form>
      <p class="fine">Create a key in UniFi Network › Settings › Control Plane › Integrations. It's stored in ${esc(se.files.secrets)} and never sent to the browser.</p>
      <label style="margin-top:10px">Refresh UniFi data every (minutes)<input id="st-refresh" type="number" min="1" max="120" value="${esc(se.refresh_minutes)}" style="max-width:120px"></label>
    </div>
  </div>
  <div class="spane" data-spane="access">
    <div class="card"><h2>Access</h2>
      <h3>On your home network</h3>
      <label class="check"><input type="checkbox" id="st-lan" ${lan.enabled ? "checked" : ""}> <span>Allow other devices on the home network (sign-in required)</span></label>
      <div class="grid g2"><label>Port<input id="st-port" type="number" min="1024" max="65535" value="${esc(lan.port)}"></label>
        <label class="check" style="align-self:end"><input type="checkbox" id="st-tls" ${lan.tls ? "checked" : ""}> <span>HTTPS (self-signed)</span></label></div>
      ${lan.urls?.length ? `<p class="fine">Open from other devices: ${lan.urls.map(x => `<span class="mono">${esc(x)}</span>`).join(" · ")}${lan.tls ? "<br>Your browser warns about the self-signed certificate the first time — that's expected; the connection is still encrypted." : ""}</p>` : ""}
      ${lan.error ? `<div class="msg err">${esc(lan.error)}</div>` : ""}
      <h3 style="margin-top:14px">Easy names ${hint("HomeOwl answers these names on your home network itself (Bonjour / mDNS), so nobody needs an IP address. Works on iPhone, iPad, Mac, Windows and most Android phones. Leave a name empty to turn it off.")}</h3>
      ${(() => { const nm = se.names || {}; return `<div class="grid g2">
        <label>Check-in page<span class="name-in"><span class="fine">http://</span><input id="st-nm-ci" value="${esc(nm.checkin || "")}" placeholder="hi" maxlength="40" autocomplete="off"><span class="fine">.local</span></span></label>
        <label>Guest Wi-Fi card<span class="name-in"><span class="fine">http://</span><input id="st-nm-guest" value="${esc(nm.guest || "")}" placeholder="wifi" maxlength="40" autocomplete="off"><span class="fine">.local</span></span></label></div>
      <label class="check"><input type="checkbox" id="st-nm-80" ${nm.port80 ? "checked" : ""}> <span>Also answer on port 80, so the names work without “:${esc(lan.port)}”</span></label>
      ${nm.checkin_url || nm.guest_url ? `<p class="fine">${[nm.checkin_url ? `Check-in: <a class="mono" href="${esc(nm.checkin_url)}" target="_blank" rel="noopener">${esc(nm.checkin_url)}</a>` : "", nm.guest_url ? `Guest card: <a class="mono" href="${esc(nm.guest_url)}" target="_blank" rel="noopener">${esc(nm.guest_url)}</a> <span class="muted">(shows the guest password to anyone on your network)</span>` : ""].filter(Boolean).join("<br>")}</p>` : ""}
      ${(nm.checkin || nm.guest) && !nm.lan ? `<div class="msg warn">Turn on home-network access above, or other devices can't open these names.</div>` : ""}
      ${nm.error ? `<div class="msg err">${esc(nm.error)}</div>` : ""}${nm.port80_error ? `<div class="msg warn">${esc(nm.port80_error)}</div>` : ""}`; })()}
      <h3 style="margin-top:14px">Built-in Tailscale</h3>
      ${!ts.built_in ? `<p class="muted">This build has no built-in Tailscale.</p>` : `
      <label class="check"><input type="checkbox" id="st-ts" ${ts.enabled ? "checked" : ""}> <span>Run HomeOwl as its own device on my tailnet</span></label>
      <div class="grid g2"><label>Name on the tailnet<input id="st-tsname" value="${esc(ts.hostname)}"></label>
        <label>Auth key (optional)<input id="st-tskey" type="password" placeholder="tskey-auth-… — or approve with a link"></label></div>
      <label class="check"><input type="checkbox" id="st-tsauto" ${ts.auto_login ? "checked" : ""}> <span>Sign people in automatically when their Tailscale login is linked to a HomeOwl user</span></label>
      <dl class="kv"><dt>Status</dt><dd>${tsState} ${st.error ? `<span class="bad-t fine">${esc(st.error)}</span>` : ""}</dd>
        ${st.login_url ? `<dt>Approve</dt><dd><a href="${esc(st.login_url)}" target="_blank" rel="noopener">Add HomeOwl to your tailnet →</a></dd>` : ""}
        ${st.url ? `<dt>Address</dt><dd><a class="mono" href="${esc(st.url)}" target="_blank" rel="noopener">${esc(st.url)}</a>${st.https ? "" : ` <span class="fine">(turn on HTTPS certificates in the Tailscale admin console for https)</span>`}</dd>` : ""}
        ${st.tailnet ? `<dt>Tailnet</dt><dd>${esc(st.tailnet)}</dd>` : ""}</dl>
      ${ts.enabled ? `<button class="btn sm" id="st-tslogout">Remove from tailnet</button>` : ""}`}
      <div class="btnrow" style="margin-top:12px"><button class="btn primary" id="st-access-save">Save access settings</button></div>
    </div>
  </div>

  <div class="spane" data-spane="users"><div class="card"><div class="head" style="margin:0 0 8px"><h2 style="margin:0">Users</h2><button class="btn sm primary" id="st-user-add">+ Add user</button></div>
    ${table("users", [
      { k: "name", label: "", fmt: x => `${esc(x.name || x.username)} <span class="mono fine">${esc(x.username)}</span>${x.username === se.me.username ? ` <span class="tag">you</span>` : ""}` },
      { k: "role", label: "", fmt: x => x.role === "admin" ? `<span class="tag good">admin</span>` : `<span class="tag">view only</span>` },
      { k: "tailscale", label: "Tailscale login", fmt: x => x.tailscale ? `<span class="mono">${esc(x.tailscale)}</span>` : "–" },
      { k: "last_login", label: "Last sign-in", fmt: x => x.last_login ? fmtTime(x.last_login * 1000) : "never", sort: x => -(x.last_login || 0) },
      { k: "edit", label: "", fmt: x => `<button class="btn sm" data-useredit="${esc(x.username)}">Edit</button>` },
    ], se.users)}
    <p class="fine">Admins can connect UniFi, unlock changes and manage settings. View-only users see everything except settings. <a href="#" id="st-mypass">Change my password</a></p>
  </div></div>

  <div class="spane" data-spane="ai"><div class="card"><h2>AI &amp; MCP</h2>
    <p class="muted">Let an AI assistant (Claude Desktop, Claude Code or any MCP client) look at — and if you allow it, manage — your network through HomeOwl. Whatever the mode, passwords and keys are never shared, and every change is logged with the token's name.</p>
    <div class="mcp-modes">${MCP_MODES.map(([k, l, d]) => `<label class="mcp-mode ${mcp.mode === k ? "on" : ""} ${k === "god" ? "god" : ""}"><input type="radio" name="st-mcpmode" value="${k}" ${mcp.mode === k ? "checked" : ""}><b>${l}</b><span class="fine">${d}</span></label>`).join("")}</div>
    <h3 style="margin-top:14px">Access tokens</h3>
    ${mcp.tokens.length ? table("mcptokens", [
      { k: "name", label: "" },
      { k: "created", label: "Created", fmt: t => fmtTime(t.created * 1000) },
      { k: "last_used", label: "Last used", fmt: t => t.last_used ? ago(t.last_used * 1000) : "never" },
      { k: "id", label: "", fmt: t => `<button class="btn sm" data-mcprevoke="${esc(t.id)}">Revoke</button>` },
    ], mcp.tokens) : `<p class="fine">No tokens yet.</p>`}
    <div class="ovr" style="grid-template-columns:1fr auto"><input id="st-mcpname" placeholder="Token name, e.g. Claude Desktop on my laptop"><button class="btn sm primary" id="st-mcpnew">Create token</button></div>
    ${mcp.mode !== "off" ? `<p class="fine"><a href="#" id="st-mcpprompt">Connection prompt for ChatGPT, Claude, Grok…</a></p>` : ""}
    <details><summary class="fine">Tools available in each mode</summary><p class="fine">${(mcp.tools || []).map(t => `<span class="tag ${t.level === "god" ? "bad" : t.level === "guarded" ? "warn" : ""}">${esc(t.name)}</span>`).join(" ")}</p></details>
  </div></div>

  <div class="spane" data-spane="analytics"><div id="an-settings"></div></div>

  <div class="spane" data-spane="home"><div class="card" id="st-cameras"></div>
    <div class="card section"><h2>Doors, UPS and guest Wi-Fi</h2><p class="fine">These are set up on their own pages.</p>
      <div class="btnrow"><a class="btn sm" href="#" data-goto="doors">Doors</a><a class="btn sm" href="#" data-goto="ups">UPS</a><a class="btn sm" href="#" data-goto="guest">Guest Wi-Fi</a><a class="btn sm" href="#" data-goto="checkins">Device check-in</a></div></div></div>

  <div class="spane" data-spane="system"><div class="card" id="st-startup"></div>
  <div class="card section" id="st-menubar" hidden></div>
  <div class="card section" id="st-updates"></div>

  <div class="card section"><h2>Files &amp; system</h2><dl class="kv">
    <dt>Version</dt><dd>${esc(se.system.version)} · ${esc(se.system.os)}/${esc(se.system.arch)} · up ${fmtDur(se.system.uptime_s)}</dd>
    <dt>Data folder</dt><dd class="mono">${esc(se.files.data)}</dd><dt>Change log</dt><dd class="mono">${esc(se.files.change_log)}</dd>
    <dt>Analytics database</dt><dd class="mono">${esc(se.files.analytics)}</dd><dt>Logs</dt><dd class="mono">${esc(se.files.log)}</dd><dt>Program</dt><dd class="mono">${esc(se.files.binary)}</dd></dl></div></div>
  </div></div>`;
  renderStartup(); renderMenubar(); setTabs();
  if (typeof loadAnSettings === "function") loadAnSettings();
  if (typeof renderUpdateSettings === "function") { renderUpdateSettings(); if (!S.update) loadUpdate().then(renderUpdateSettings); }
  if (typeof renderCameraSettings === "function") renderCameraSettings();
}

// Settings side menu: one section at a time, remembered in this browser; a dot shows what needs attention
const SET_TABS = [["connection", "Connection"], ["access", "Access"], ["users", "Users"], ["analytics", "Analytics"], ["home", "Home"], ["ai", "AI & MCP"], ["system", "System"]];
try { S.setTab = localStorage.getItem("homeowl-settings-tab") || "connection"; } catch { S.setTab = "connection"; }
function setTabs() {
  const nav = $("#set-nav"), se = S.settings; if (!nav || !se) return;
  if (!SET_TABS.some(([k]) => k === S.setTab)) S.setTab = "connection";
  const ts = se.tailscale || {}, dot = {
    connection: se.unifi?.connected ? "ok" : "bad",
    access: ts.enabled && ts.status?.state && ts.status.state !== "Running" ? "warn" : "",
    users: "", analytics: "", home: "", ai: se.mcp?.mode === "god" ? "warn" : "",
    system: S.update?.available ? "warn" : "",
  };
  const count = { users: (se.users || []).length };
  nav.innerHTML = SET_TABS.map(([k, l]) => `<a href="#" data-stab="${k}" class="${S.setTab === k ? "act" : ""}" aria-current="${S.setTab === k ? "page" : "false"}">${esc(l)}${count[k] ? ` <span class="fine">${count[k]}</span>` : dot[k] ? ` <span class="sdot ${dot[k]}" aria-label="${dot[k] === "ok" ? "fine" : "needs attention"}"></span>` : ""}</a>`).join("");
  $$(".spane").forEach(p => { p.hidden = p.dataset.spane !== S.setTab; });
}
document.addEventListener("click", e => {
  const t = e.target.closest("[data-stab]"); if (!t) return;
  e.preventDefault(); S.setTab = t.dataset.stab; try { localStorage.setItem("homeowl-settings-tab", S.setTab); } catch {}
  setTabs(); window.scrollTo(0, 0);
});

// Start HomeOwl with this computer
function renderStartup() {
  const el = $("#st-startup"), st = S.settings?.startup; if (!el) return;
  if (!st || !st.available) { el.innerHTML = `<h2>Start with this computer</h2><p class="muted">Not available in this build.</p>`; return; }
  el.innerHTML = `<h2>Start with this computer</h2>
    <label class="check"><input type="checkbox" id="st-startup-on" ${st.enabled ? "checked" : ""} ${st.can_change ? "" : "disabled"}> <span>Start HomeOwl ${esc(st.kind)}, in the background</span></label>
    ${st.error ? `<p class="bad-t fine">${esc(st.error)}</p>` : ""}
    <p class="fine">${st.as_service ? "This HomeOwl is running as that background service now." : st.enabled ? "It's set up; it starts by itself next time." : "Off: HomeOwl runs only while you keep it open."}
      ${st.enabled && st.as_service ? " Turning it off stops this HomeOwl too." : ""}</p>
    ${st.can_change ? "" : `<p class="fine">This needs administrator rights. Run this in a terminal on the computer running HomeOwl:</p>${copyBox("st-startup-cmd", st.command)}`}`;
}
document.addEventListener("change", async e => {
  if (e.target.id !== "st-startup-on") return;
  const on = e.target.checked;
  try {
    const r = await api("/api/startup", { enabled: on }); S.settings.startup = r; renderStartup();
    toast(r.stopping ? "HomeOwl stops now and won't start by itself anymore" : on ? "HomeOwl will start with this computer" : "HomeOwl won't start by itself anymore", "ok");
  } catch (err) { toast(err.message, "err"); e.target.checked = !on; }
});

// the menu bar icon (macOS): the same owls HomeOwl draws in the menu bar
const MB_OWL = { outline: '<svg width="18" height="18" aria-hidden="true" xmlns="http://www.w3.org/2000/svg" viewBox="66 166 220 220"><path d="M176 380 C120 380 104 336 104 290 C104 236 136 200 176 200 C216 200 248 236 248 290 C248 336 232 380 176 380Z" fill="none" stroke="currentColor" stroke-width="17"/><path d="M120 218 L112 178 L148 206Z M232 218 L240 178 L204 206Z" fill="currentColor" stroke="currentColor" stroke-width="8" stroke-linejoin="round"/> <circle cx="150" cy="270" r="23" fill="none" stroke="currentColor" stroke-width="13"/><circle cx="202" cy="270" r="23" fill="none" stroke="currentColor" stroke-width="13"/> <circle cx="152" cy="273" r="9" fill="currentColor"/><circle cx="200" cy="273" r="9" fill="currentColor"/><path d="M166 302 L186 302 L176 320Z" fill="currentColor"/></svg>', filled: '<svg width="18" height="18" aria-hidden="true" xmlns="http://www.w3.org/2000/svg" viewBox="66 166 220 220"><defs><mask id="owlm"><rect x="0" y="0" width="512" height="512" fill="#fff"/> <circle cx="150" cy="270" r="27" fill="#000"/><circle cx="202" cy="270" r="27" fill="#000"/><path d="M164 300 L188 300 L176 322Z" fill="#000"/></mask></defs> <g mask="url(#owlm)"><path d="M176 380 C120 380 104 336 104 290 C104 236 136 200 176 200 C216 200 248 236 248 290 C248 336 232 380 176 380Z" fill="currentColor"/><path d="M120 218 L112 178 L148 206Z M232 218 L240 178 L204 206Z" fill="currentColor" stroke="currentColor" stroke-width="8" stroke-linejoin="round"/></g> <circle cx="152" cy="273" r="11" fill="currentColor"/><circle cx="200" cy="273" r="11" fill="currentColor"/></svg>' };
const MB_STYLES = {
  outline: ["Owl outline", "Follows the light or dark menu bar. Default."],
  dot: ["Owl + status dot", "Adds an amber dot when something needs a look, red when the internet or power is down."],
  filled: ["Owl face, filled", "A solid owl, still following the menu bar's colour."],
  logo: ["Colour logo", "HomeOwl's colour icon."],
  count: ["Owl + count", "Shows how many things need a look, e.g. 2."],
};
function mbPreview(st) {
  if (st === "logo") return `<img src="icon.png" alt="" width="18" height="18">`;
  const owl = st === "filled" ? MB_OWL.filled : MB_OWL.outline;
  return `${owl}${st === "dot" ? `<i class="mbp-dot"></i>` : st === "count" ? `<b>2</b>` : ""}`;
}
function renderMenubar() {
  const el = $("#st-menubar"), m = S.settings?.menubar; if (!el) return;
  el.hidden = !m || !m.available; if (el.hidden) return;
  el.innerHTML = `<h2>Menu bar</h2>
    <label class="check"><input type="checkbox" id="st-mb-on" ${m.show ? "checked" : ""}> <span>Show HomeOwl in the Mac's menu bar</span></label>
    <p class="fine">Click the owl for internet, network, UPS and who's home, the guest Wi-Fi code, updates and start at login.</p>
    <div class="mbp-list" role="radiogroup" aria-label="Menu bar icon">${arr(m.styles).map(st => `<button class="mbp ${st === m.icon ? "act" : ""}" data-mbstyle="${esc(st)}" role="radio" aria-checked="${st === m.icon}">
      <span class="mbp-bar"><span class="mbp-ico">${mbPreview(st)}</span></span><span class="mbp-name">${esc(MB_STYLES[st]?.[0] || st)}</span><span class="fine muted">${esc(MB_STYLES[st]?.[1] || "")}</span></button>`).join("")}</div>`;
}
async function setMenubar(body) {
  try { S.settings.menubar = await api("/api/menubar-settings", body); renderMenubar(); }
  catch (err) { toast(err.message, "err"); renderMenubar(); }
}
document.addEventListener("change", e => { if (e.target.id === "st-mb-on") setMenubar({ show: e.target.checked }); });
document.addEventListener("click", e => { const b = e.target.closest("[data-mbstyle]"); if (b) setMenubar({ icon: b.dataset.mbstyle }); });

function fmtDur(s) { s = +s || 0; return s < 3600 ? `${Math.round(s / 60)} min` : s < 86400 ? `${(s / 3600).toFixed(1)} h` : `${Math.round(s / 86400)} days`; }

function userModal(x) {
  const create = !x; x = x || { username: "", name: "", role: "viewer", tailscale: "" };
  const legacy = (S.settings?.legacy_logins || []).filter(Boolean);
  openModal({ title: create ? "Add a user" : `Edit ${x.name || x.username}`,
    body: `<div class="grid g2"><label>Username<input id="um-user" value="${esc(x.username)}" ${create ? "" : "disabled"}></label><label>Name<input id="um-name" value="${esc(x.name || "")}"></label></div>
      <label>Role<select id="um-role"><option value="viewer" ${x.role === "viewer" ? "selected" : ""}>View only</option><option value="admin" ${x.role === "admin" ? "selected" : ""}>Admin</option></select></label>
      <label>${create ? "Password" : "New password (leave empty to keep)"}<input id="um-pass" type="password" autocomplete="new-password" minlength="10"></label>
      <label>Tailscale login (optional)<input id="um-ts" value="${esc(x.tailscale || "")}" list="um-legacy" placeholder="name@github"></label>
      <datalist id="um-legacy">${legacy.map(l => `<option value="${esc(l)}">`).join("")}</datalist>
      <p class="fine">With built-in Tailscale, this login signs the user in automatically.</p>`,
    buttons: [...(create ? [] : [{ label: "Delete user", cls: "danger", onClick: async () => {
      try { await api("/api/users/delete", { username: x.username }); closeModal(); toast("User deleted", "ok"); loadSettings(); } catch (e) { toast(e.message, "err"); } } }]),
      { label: "Cancel" }, { label: "Save", cls: "primary", onClick: async () => {
        try { await api("/api/users/save", { create, username: $("#um-user").value, name: $("#um-name").value, role: $("#um-role").value, password: $("#um-pass").value, tailscale: $("#um-ts").value });
          closeModal(); toast("Saved", "ok"); loadSettings(); } catch (e) { toast(e.message, "err"); } } }] });
}

function mcpSnippets(token) {
  const se = S.settings, local = `https://127.0.0.1:${se.lan.port}/mcp`;
  const remote = se.tailscale.status?.url ? se.tailscale.status.url + "/mcp" : (se.lan.urls?.[0] ? se.lan.urls[0] + "/mcp" : local);
  const tok = token || "<paste your HomeOwl token>", bin = se.files.binary || "homeowl";
  const mode = (MCP_MODES.find(m => m[0] === se.mcp.mode) || MCP_MODES[0])[1];
  const selfSigned = remote.startsWith("https:") && !se.tailscale.status?.url;
  const desktop = JSON.stringify({ mcpServers: { homeowl: { command: bin, args: ["mcp", "--url", local, "--token", tok] } } }, null, 2);
  const prompt = `Please connect yourself to my HomeOwl MCP server so you can look at my home network.

- Server URL: ${remote}
- Transport: Streamable HTTP
- Authentication: HTTP header  Authorization: Bearer ${tok}
- Access mode: ${mode}${selfSigned ? "\n- The server uses a self-signed certificate. If your client can't accept it, use HomeOwl's stdio bridge instead: command \"" + bin + "\" with args [\"mcp\", \"--url\", \"" + remote + "\", \"--token\", \"" + tok + "\", \"--insecure\"]" : ""}

Steps:
1. Add this server to your MCP settings under the name "homeowl" (for Claude Code: claude mcp add --transport http homeowl ${remote} --header "Authorization: Bearer ${tok}"; for Claude Desktop, Cursor, Codex and others: your MCP config file).
2. Restart or reload if your client needs it, then call the network_overview tool and summarise what you see in a few lines.
3. Keep the token private: don't repeat it in chat or save it anywhere except the MCP settings.

Rules while using HomeOwl: ask me before any change to my network (block, restart, setting changes), say what will change first, and prefer read-only tools.`;
  const tabs = [
    ["prompt", "AI prompt", `<p class="fine">Paste this into an assistant that can change its own settings — Claude Code, Codex, Cursor, or a desktop app with an agent. It sets up the connection and checks it.</p>
      <pre class="codebox" id="mcp-prompt">${esc(prompt)}</pre><button class="btn sm primary" data-copytext="mcp-prompt">Copy prompt</button>`],
    ["code", "Claude Code", `<pre class="codebox" id="mcp-cc">claude mcp add --transport http homeowl ${esc(remote)} --header "Authorization: Bearer ${esc(tok)}"</pre><button class="btn sm" data-copytext="mcp-cc">Copy</button>`],
    ["desktop", "Claude Desktop", `<p class="fine">Settings › Developer › Edit config, add this, restart Claude Desktop.${selfSigned ? "" : " From another computer, use HomeOwl's address with --url."}</p><pre class="codebox" id="mcp-cd">${esc(desktop)}</pre><button class="btn sm" data-copytext="mcp-cd">Copy</button>`],
    ["web", "ChatGPT, Grok & web apps", `<p class="fine">Chat apps in the browser (ChatGPT, claude.ai, Grok) connect from their own servers on the internet, not from your computer, so they can't reach HomeOwl at a home or Tailscale address. Use a desktop assistant with the prompt instead, or put HomeOwl behind a public HTTPS address you trust and add it as a custom connector with:</p>
      <pre class="codebox" id="mcp-web">URL: ${esc(remote)}\nHeader: Authorization: Bearer ${esc(tok)}</pre><button class="btn sm" data-copytext="mcp-web">Copy</button>`],
  ];
  return `${token ? `<p>Copy the token now — it isn't shown again.</p><pre class="codebox" id="mcp-tok">${esc(token)}</pre>` : `<p class="fine">Replace the placeholder with a token you created (tokens are shown only once).</p>`}
    <div class="seg mcp-tabs">${tabs.map(([k, l], i) => `<button class="${i ? "" : "on"}" data-mcptab="${k}">${l}</button>`).join("")}</div>
    ${tabs.map(([k, , h], i) => `<div class="mcp-tab" data-mcppane="${k}" ${i ? "hidden" : ""}>${h}</div>`).join("")}`;
}

document.addEventListener("click", async e => {
  const t = e.target.closest("[data-mcptab]");
  if (t) { document.querySelectorAll("[data-mcptab]").forEach(b => b.classList.toggle("on", b === t)); document.querySelectorAll("[data-mcppane]").forEach(p => { p.hidden = p.dataset.mcppane !== t.dataset.mcptab; }); return; }
  const c = e.target.closest("[data-copytext]");
  if (c) { try { await navigator.clipboard.writeText($("#" + c.dataset.copytext).textContent); toast("Copied", "ok"); } catch { toast("Select the text and copy it", "err"); } }
});

document.addEventListener("submit", async e => {
  if (e.target.id !== "st-connect") return;
  e.preventDefault(); const btn = e.target.querySelector("button[type=submit]"); btn.disabled = true; btn.textContent = "Connecting…";
  try {
    const r = await api("/api/connect", { host: $("#st-host").value, key: $("#st-key").value, site: $("#st-site").value, verify_tls: $("#st-verify").checked });
    if (r.warning) toast(r.warning, "err"); else toast("Connected to UniFi", "ok");
    S.write = false; await collectLive(false, true); S.view = "overview"; render();
  } catch (err) { toast(err.message, "err"); loadSettings(); }
});

document.addEventListener("click", async e => {
  const id = e.target.id;
  const post = async (path, body, okMsg) => { try { const r = await api(path, body); if (okMsg) toast(okMsg, "ok"); loadSettings(); return r; } catch (err) { toast(err.message, "err"); } };
  if (id === "st-disconnect") { await post("/api/disconnect", {}, "Disconnected"); load(EMPTY_BUNDLE, "none"); S.view = "settings"; render(); }
  if (id === "st-access-save") {
    const body = { refresh_minutes: +$("#st-refresh").value || 5, lan: { enabled: $("#st-lan").checked, port: +$("#st-port").value, tls: $("#st-tls").checked } };
    body.names = { checkin: $("#st-nm-ci").value.trim(), guest: $("#st-nm-guest").value.trim(), port80: $("#st-nm-80").checked };
    if ($("#st-ts")) body.tailscale = { enabled: $("#st-ts").checked, hostname: $("#st-tsname").value.trim(), auto_login: $("#st-tsauto").checked, auth_key: $("#st-tskey").value.trim() };
    const oldPort = S.settings.lan.port;
    await post("/api/settings", body, "Access settings saved");
    if (body.lan.port !== oldPort && location.port === String(oldPort)) setTimeout(() => { location.port = body.lan.port; }, 1200);
    if (body.tailscale?.enabled) setTimeout(loadSettings, 4000);
  }
  if (id === "st-tslogout") openModal({ title: "Remove HomeOwl from your tailnet?", body: "<p>HomeOwl leaves the tailnet and built-in Tailscale is turned off. You can add it again later.</p>",
    buttons: [{ label: "Cancel" }, { label: "Remove", cls: "primary", onClick: async () => { closeModal(); await post("/api/tailscale/logout", {}, "Removed from the tailnet"); } }] });
  if (id === "st-user-add") userModal(null);
  const ue = e.target.closest("[data-useredit]"); if (ue) userModal(S.settings.users.find(x => x.username === ue.dataset.useredit));
  if (id === "st-mypass") { e.preventDefault(); openModal({ title: "Change my password", body: `<label>Current password<input id="mp-cur" type="password" autocomplete="current-password"></label><label>New password<input id="mp-new" type="password" autocomplete="new-password" minlength="10"></label>`,
    buttons: [{ label: "Cancel" }, { label: "Change", cls: "primary", onClick: async () => { try { await api("/api/me/password", { current: $("#mp-cur").value, new: $("#mp-new").value }); closeModal(); toast("Password changed — sign in again", "ok"); setTimeout(() => location.reload(), 1500); } catch (err) { toast(err.message, "err"); } } }] }); }
  if (id === "st-mcpprompt") { e.preventDefault(); openModal({ title: "Connect an AI assistant", wide: true, body: mcpSnippets(null), buttons: [{ label: "Done" }] }); }
  if (id === "st-mcpnew") {
    try { const r = await api("/api/mcp/token", { name: $("#st-mcpname").value.trim() }); openModal({ title: "MCP token created", wide: true, body: mcpSnippets(r.token), buttons: [{ label: "Done", onClick: () => { closeModal(); loadSettings(); } }] }); }
    catch (err) { toast(err.message, "err"); }
  }
  const rv = e.target.closest("[data-mcprevoke]"); if (rv) post("/api/mcp/revoke", { id: rv.dataset.mcprevoke }, "Token revoked");
});

document.addEventListener("change", e => {
  if (e.target.name !== "st-mcpmode") return;
  const mode = e.target.value;
  const apply = () => api("/api/settings", { mcp: { mode } }).then(() => { toast(`MCP: ${MCP_MODES.find(m => m[0] === mode)[1]}`, "ok"); loadSettings(); }).catch(err => toast(err.message, "err"));
  if (mode === "god") openModal({ title: "Turn on full access?", body: "<p>An AI client with a token will be able to call <b>any</b> UniFi API endpoint, including ones that delete networks, change the firewall or reset devices. There's no preview and no undo for these requests (they are logged).</p><p>Use it only while you need it.</p>",
    buttons: [{ label: "Cancel", onClick: () => { closeModal(); loadSettings(); } }, { label: "Turn on full access", cls: "danger", onClick: () => { closeModal(); apply(); } }] });
  else apply();
});

// load settings when the view opens
const _renderBase = render;
render = function () { _renderBase(); if (S.view === "settings") loadSettings(); };
