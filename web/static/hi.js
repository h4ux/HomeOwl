/* HomeOwl — device check-in ("Hi HomeOwl"): runs on the family member's own phone, tablet or laptop, without signing in. */
"use strict";

const $ = s => document.querySelector(s);
const esc = s => String(s ?? "").replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
const KINDS = [["phone", "📱 Phone"], ["tablet", "📱 Tablet"], ["laptop", "💻 Laptop"], ["computer", "🖥 Computer"], ["tv", "📺 TV / streamer"],
  ["console", "🎮 Game console"], ["speaker", "🔊 Speaker"], ["watch", "⌚ Watch"], ["camera", "📷 Camera"], ["printer", "🖨 Printer"], ["other", "Something else"]];

function guessKind() {
  const ua = navigator.userAgent;
  if (/iPad|Tablet/i.test(ua) || (/Macintosh/.test(ua) && navigator.maxTouchPoints > 1)) return "tablet";
  if (/iPhone|Android.+Mobile|Mobile/i.test(ua)) return "phone";
  if (/Android/i.test(ua)) return "tablet";
  if (/Macintosh|Windows|Linux|CrOS/i.test(ua)) return "laptop";
  return "other";
}

async function call(method, body) {
  const r = await fetch("/api/hi", { method, headers: { "Content-Type": "application/json" }, body: body ? JSON.stringify(body) : undefined, cache: "no-store" });
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(j.error || `Something went wrong (HTTP ${r.status})`);
  return j;
}

function done(again) {
  $("#hi-box").innerHTML = `<h2 style="margin:10px 0 6px">Thanks! 🦉</h2>
    <p>An admin will approve it soon. After that, this device shows up with its new name in UniFi and in HomeOwl.</p>
    ${again ? `<p class="fine">This device is already waiting for approval — sending again replaces that request.</p><button class="btn" id="hi-again">Change my answer</button>` : ""}`;
}

async function start() {
  let st;
  try { st = await call("GET"); } catch (err) { $("#hi-box").innerHTML = `<div class="msg err">${esc(err.message)}</div>`; return; }
  if (st.waiting && !start.force) return done(true);
  const d = st.device, kind = guessKind();
  $("#hi-box").innerHTML = `<p class="muted">Tell HomeOwl what this device is and whose it is. An admin approves it, then it's named on the network.</p>
    <div class="hi-seen fine">The network sees this device as <b>${esc(d.name || d.hostname || "an unnamed device")}</b>${d.vendor ? ` (${esc(d.vendor)})` : ""}${d.network ? ` on ${esc(d.network)}` : ""}.</div>
    <form id="hi-form" autocomplete="off">
      <label>Whose device is it?
        <select id="hi-person" required>
          <option value="">Choose…</option>
          ${st.people.filter(p => p.kind !== "group").map(p => `<option value="${esc(p.id)}">${esc(p.name)}</option>`).join("")}
          ${st.people.some(p => p.kind === "group") ? `<optgroup label="Shared by everyone">${st.people.filter(p => p.kind === "group").map(p => `<option value="${esc(p.id)}">${esc(p.name)}</option>`).join("")}</optgroup>` : ""}
          <option value="__new">${st.people.length ? "Someone else…" : "Type a name…"}</option>
        </select></label>
      <label id="hi-newwrap" hidden>Name<input id="hi-newname" maxlength="40" placeholder="e.g. Alex"></label>
      <label>What is it?<select id="hi-kind">${KINDS.map(([k, l]) => `<option value="${k}" ${k === kind ? "selected" : ""}>${l}</option>`).join("")}</select></label>
      <label>Name for this device<input id="hi-name" maxlength="40" required placeholder="e.g. Alex's iPad"></label>
      <button class="btn primary" type="submit">Send to HomeOwl</button>
      <div id="hi-msg" class="msg" role="status"></div>
    </form>`;
  if (!st.people.length) { $("#hi-person").value = "__new"; $("#hi-newwrap").hidden = false; }
}

function suggestName() {
  const sel = $("#hi-person"), who = sel.value === "__new" ? $("#hi-newname").value.trim() : sel.selectedOptions[0]?.textContent || "";
  const kind = (KINDS.find(k => k[0] === $("#hi-kind").value)?.[1] || "").replace(/^\S+\s/, "");
  const nameEl = $("#hi-name");
  if (who && !nameEl.dataset.typed && $("#hi-kind").value !== "other") nameEl.value = `${who}'s ${kind}`;
}

document.addEventListener("change", e => {
  if (e.target.id === "hi-person") { $("#hi-newwrap").hidden = e.target.value !== "__new"; if (e.target.value === "__new") $("#hi-newname").focus(); }
  if (e.target.id === "hi-person" || e.target.id === "hi-kind") suggestName();
});
document.addEventListener("input", e => {
  if (e.target.id === "hi-name") e.target.dataset.typed = "1";
  if (e.target.id === "hi-newname") suggestName();
});
document.addEventListener("click", e => { if (e.target.id === "hi-again") { start.force = true; start(); } });
document.addEventListener("submit", async e => {
  if (e.target.id !== "hi-form") return;
  e.preventDefault();
  const msg = $("#hi-msg"), sel = $("#hi-person").value;
  msg.className = "msg"; msg.textContent = "Sending…";
  try {
    await call("POST", { name: $("#hi-name").value, kind: $("#hi-kind").value, person_id: sel === "__new" ? "" : sel, person_name: sel === "__new" ? $("#hi-newname").value : "" });
    done(false);
  } catch (err) { msg.className = "msg err"; msg.textContent = err.message; }
});
start();
