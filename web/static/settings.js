/* HomeOwl — Settings (admins): UniFi connection, access (home network + built-in Tailscale), users, AI & MCP, analytics. */
"use strict";

V.settings = () => `<div class="head"><div><h1>Settings</h1><div class="muted">Stored on the computer running HomeOwl.</div></div></div><div id="settings-body"><div class="empty">Loading…</div></div>`;

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
  <div class="grid g2">
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

    <div class="card"><h2>Access</h2>
      <h3>On your home network</h3>
      <label class="check"><input type="checkbox" id="st-lan" ${lan.enabled ? "checked" : ""}> <span>Allow other devices on the home network (sign-in required)</span></label>
      <div class="grid g2"><label>Port<input id="st-port" type="number" min="1024" max="65535" value="${esc(lan.port)}"></label>
        <label class="check" style="align-self:end"><input type="checkbox" id="st-tls" ${lan.tls ? "checked" : ""}> <span>HTTPS (self-signed)</span></label></div>
      ${lan.urls?.length ? `<p class="fine">Open from other devices: ${lan.urls.map(x => `<span class="mono">${esc(x)}</span>`).join(" · ")}${lan.tls ? "<br>Your browser warns about the self-signed certificate the first time — that's expected; the connection is still encrypted." : ""}</p>` : ""}
      ${lan.error ? `<div class="msg err">${esc(lan.error)}</div>` : ""}
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

  <div class="card section"><div class="head" style="margin:0 0 8px"><h2 style="margin:0">Users</h2><button class="btn sm primary" id="st-user-add">+ Add user</button></div>
    <div class="tbl-wrap"><table><thead><tr><th>User</th><th>Role</th><th>Tailscale login</th><th>Last sign-in</th><th></th></tr></thead><tbody>
    ${se.users.map(x => `<tr><td><b>${esc(x.name || x.username)}</b> <span class="mono muted">${esc(x.username)}</span>${x.username === se.me.username ? ` <span class="tag">you</span>` : ""}</td>
      <td>${x.role === "admin" ? `<span class="tag good">admin</span>` : `<span class="tag">view only</span>`}</td><td class="mono fine">${esc(x.tailscale || "–")}</td>
      <td class="fine">${x.last_login ? fmtTime(x.last_login * 1000) : "never"}</td>
      <td class="num"><button class="btn sm" data-useredit="${esc(x.username)}">Edit</button></td></tr>`).join("")}
    </tbody></table></div>
    <p class="fine">Admins can connect UniFi, unlock changes and manage settings. View-only users see everything except settings. <a href="#" id="st-mypass">Change my password</a></p>
  </div>

  <div class="card section"><h2>AI &amp; MCP</h2>
    <p class="muted">Let an AI assistant (Claude Desktop, Claude Code or any MCP client) look at — and if you allow it, manage — your network through HomeOwl. Whatever the mode, passwords and keys are never shared, and every change is logged with the token's name.</p>
    <div class="mcp-modes">${MCP_MODES.map(([k, l, d]) => `<label class="mcp-mode ${mcp.mode === k ? "on" : ""} ${k === "god" ? "god" : ""}"><input type="radio" name="st-mcpmode" value="${k}" ${mcp.mode === k ? "checked" : ""}><b>${l}</b><span class="fine">${d}</span></label>`).join("")}</div>
    <h3 style="margin-top:14px">Access tokens</h3>
    ${mcp.tokens.length ? `<div class="tbl-wrap"><table><thead><tr><th>Name</th><th>Created</th><th>Last used</th><th></th></tr></thead><tbody>${mcp.tokens.map(t => `<tr><td>${esc(t.name)}</td><td class="fine">${fmtTime(t.created * 1000)}</td><td class="fine">${t.last_used ? ago(t.last_used * 1000) : "never"}</td><td class="num"><button class="btn sm" data-mcprevoke="${esc(t.id)}">Revoke</button></td></tr>`).join("")}</tbody></table></div>` : `<p class="fine">No tokens yet.</p>`}
    <div class="ovr" style="grid-template-columns:1fr auto"><input id="st-mcpname" placeholder="Token name, e.g. Claude Desktop on my laptop"><button class="btn sm primary" id="st-mcpnew">Create token</button></div>
    <details><summary class="fine">Tools available in each mode</summary><p class="fine">${(mcp.tools || []).map(t => `<span class="tag ${t.level === "god" ? "bad" : t.level === "guarded" ? "warn" : ""}">${esc(t.name)}</span>`).join(" ")}</p></details>
  </div>

  <div id="an-settings" class="section"></div>

  <div class="card section" id="st-updates"></div>

  <div class="card section"><h2>Files &amp; system</h2><dl class="kv">
    <dt>Version</dt><dd>${esc(se.system.version)} · ${esc(se.system.os)}/${esc(se.system.arch)} · up ${fmtDur(se.system.uptime_s)}</dd>
    <dt>Data folder</dt><dd class="mono">${esc(se.files.data)}</dd><dt>Change log</dt><dd class="mono">${esc(se.files.change_log)}</dd>
    <dt>Analytics database</dt><dd class="mono">${esc(se.files.analytics)}</dd><dt>Logs</dt><dd class="mono">${esc(se.files.log)}</dd><dt>Program</dt><dd class="mono">${esc(se.files.binary)}</dd></dl></div>`;
  if (typeof loadAnSettings === "function") loadAnSettings();
  if (typeof renderUpdateSettings === "function") { renderUpdateSettings(); if (!S.update) loadUpdate().then(renderUpdateSettings); }
}

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
  const bin = se.files.binary || "homeowl";
  const desktop = JSON.stringify({ mcpServers: { homeowl: { command: bin, args: ["mcp", "--url", local, "--token", token] } } }, null, 2);
  return `<p>Copy the token now — it isn't shown again.</p><pre class="codebox" id="mcp-tok">${esc(token)}</pre>
    <h3>Claude Code</h3><pre class="codebox">claude mcp add --transport http homeowl ${esc(remote)} --header "Authorization: Bearer ${esc(token)}"</pre>
    <p class="fine">Over HTTPS with HomeOwl's self-signed certificate, prefer the Tailscale address (real certificate) or the command below.</p>
    <h3>Claude Desktop (claude_desktop_config.json)</h3><pre class="codebox">${esc(desktop)}</pre>
    <p class="fine">On another computer, use HomeOwl's address with <span class="mono">--url</span> and add <span class="mono">--insecure</span> for the self-signed certificate (not needed with the Tailscale address).</p>`;
}

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
