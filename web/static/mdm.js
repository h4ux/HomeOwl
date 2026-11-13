/* HomeOwl — Settings › Device profiles › Device management (MDM): getting Apple's MDM push certificate through
   mdmcert.download, step by step (mdmpush.go). Depends on core.js, app.js. */
"use strict";

async function renderMdm() {
  const el = $("#st-mdm"); if (!el) return;
  let m; try { m = await api("/api/mdm"); } catch (e) { el.innerHTML = `<div class="empty bad-t">${esc(e.message)}</div>`; return; }
  const step = (n, title, body) => `<li class="mdm-step ${m.step > n ? "done" : m.step === n ? "now" : ""}"><b>${title}</b>${m.step === n || (n === 4 && m.step === 3) ? `<div class="mdm-body">${body}</div>` : ""}</li>`;
  const exp = m.not_after ? new Date(m.not_after * 1000).toLocaleDateString([], { day: "numeric", month: "long", year: "numeric" }) : "";
  el.innerHTML = `<h2>Device management (MDM) ${hint("With MDM, HomeOwl can list the apps on enrolled iPhones, iPads and Macs, install App Store apps (the person accepts) and remove apps it installed. Apple requires a push certificate for that, signed for an organization.")}</h2>
    ${m.renewing ? `<div class="msg fine">Renewing: the current certificate (${esc(m.topic)}, ${m.days_left} days left) keeps working until you upload the renewed one. At identity.apple.com choose <b>Renew</b> on it, not Create.</div>` : ""}
    ${m.step === 5 ? `<div class="mdm-ok"><span class="tag ${m.days_left < 30 ? "warn" : "good"}">push certificate ready</span>
        <dl class="kv"><dt>Topic</dt><dd class="mono">${esc(m.topic)}</dd><dt>Expires</dt><dd>${esc(exp)} · ${m.days_left} days left</dd><dt>Fingerprint</dt><dd class="mono">${esc(m.cert_sha || "")}</dd></dl>
        <p class="fine">Renew it every year at <a href="https://identity.apple.com" target="_blank" rel="noopener">identity.apple.com</a> with the <b>same Apple ID</b>: choose <b>Renew</b> on this certificate (not Create), with a new signed request. HomeOwl reminds you 30, 7 and 1 days before. Enrolling devices comes in the next release.</p>
        <div class="btnrow"><button class="btn" id="mdm-renew">Renew…</button></div></div>`
    : `${m.renewing ? "" : `<div class="msg warn fine">mdmcert.download signs requests only for organizations (businesses, schools, non-profits), with an email address on the organization's own domain, registered and verified at <a href="https://mdmcert.download/registration" target="_blank" rel="noopener">mdmcert.download/registration</a>. Apple's terms don't allow it for individuals.</div>`}
      <ol class="mdm-steps">
      ${step(1, "Send a request to mdmcert.download", `<p class="fine">HomeOwl makes the push key (it stays here) and sends only a signing request, with your organization's email.</p>
        <div class="ovr" style="grid-template-columns:1fr 90px auto"><input id="mdm-email" type="email" placeholder="it@your-organization.example" value="${esc(m.email || "")}" autocomplete="email">
          <input id="mdm-country" maxlength="2" placeholder="US" value="${esc(m.country || "US")}" aria-label="Organization's country (2 letters)"><button class="btn primary" id="mdm-send">Send</button></div>
        <details class="fine"><summary>API key</summary><p>HomeOwl uses the key shared by open-source MDM servers.${m.custom_key ? " A custom key is saved." : ""} If mdmcert.download gave you one, paste it: <input id="mdm-key" placeholder="${m.custom_key ? "saved — leave empty to keep" : "optional"}" autocomplete="off"></p></details>`)}
      ${step(2, "Upload the file mdmcert.download emails you", `<p class="fine">Sent ${m.requested_at ? esc(ago(m.requested_at * 1000)) : ""} to <b>${esc(m.email)}</b>. The email has an attachment like <span class="mono">mdm_signed_request…plist.b64.p7</span>. Upload it here; HomeOwl decrypts it.</p>
        <div class="btnrow"><label class="btn primary">Choose the file<input type="file" id="mdm-p7" hidden></label><button class="btn ghost sm" id="mdm-again">Send again</button></div>`)}
      ${step(3, "Get the certificate from Apple", `<ol class="fine"><li><button class="btn sm" id="mdm-dl">Download the signed request</button></li>
          <li>Sign in at <a href="https://identity.apple.com" target="_blank" rel="noopener">identity.apple.com</a> with the Apple ID you'll keep using (a managed or work Apple ID is best: renewals need the same one).</li>
          <li>${m.renewing ? `Find the current certificate (topic <span class="mono">${esc(m.topic)}</span>), choose <b>Renew</b>` : `Choose <b>Create a Certificate</b>, accept the terms`}, upload the request and download the certificate (<span class="mono">.pem</span>).</li></ol>`)}
      ${step(4, "Upload Apple's certificate", `<div class="btnrow"><label class="btn primary">Choose the .pem file<input type="file" id="mdm-pem" accept=".pem,.cer,.crt" hidden></label></div>`)}
      </ol>
      ${m.renewing ? `<p class="fine"><a href="#" id="mdm-cancel">Cancel the renewal</a></p>` : m.step > 1 ? `<p class="fine"><a href="#" id="mdm-reset">Start over</a></p>` : ""}`}`;
}

const mdmFile = input => new Promise((ok, bad) => { const f = input.files[0]; if (!f) return; const r = new FileReader(); r.onload = () => ok(r.result); r.onerror = () => bad(r.error); r.readAsText(f); });
const mdmDo = async (path, body, msg) => { try { await api(path, body); if (msg) toast(msg, "ok"); } catch (e) { toast(e.message, "err"); } renderMdm(); };

document.addEventListener("click", async e => {
  const id = e.target.id;
  if (id === "mdm-send") { const body = { email: $("#mdm-email").value.trim(), country: $("#mdm-country").value.trim() }; const k = $("#mdm-key")?.value.trim(); if (k) body.api_key = k; e.target.disabled = true; mdmDo("/api/mdm/request", body, "Sent — check that mailbox"); }
  if (id === "mdm-again") mdmDo("/api/mdm/restart", {});
  if (id === "mdm-cancel") { e.preventDefault(); mdmDo("/api/mdm/restart", { cancel: true }); }
  if (id === "mdm-dl") {
    try { const r = await api("/api/mdm/push-request", {}); const a = document.createElement("a"); a.href = URL.createObjectURL(new Blob([r.request], { type: "application/octet-stream" })); a.download = "homeowl-push-request.plist.b64"; a.click(); setTimeout(() => URL.revokeObjectURL(a.href), 2000); }
    catch (err) { toast(err.message, "err"); }
  }
  if (id === "mdm-renew") openModal({ title: "Renew the push certificate?", body: `<p>HomeOwl sends a new request to mdmcert.download (same push key). At identity.apple.com choose <b>Renew</b> on the current certificate, not Create, so the topic stays and enrolled devices keep working. The current certificate keeps working until you upload the new one.</p>`,
    buttons: [{ label: "Cancel" }, { label: "Start renewal", cls: "primary", onClick: () => { closeModal(); mdmDo("/api/mdm/restart", { renew: true }); } }] });
  if (id === "mdm-reset") { e.preventDefault(); openModal({ title: "Start over?", body: `<p>The steps start again. The push key stays, so a certificate made from it can still be uploaded.</p>`, buttons: [{ label: "Cancel" }, { label: "Start over", cls: "danger", onClick: () => { closeModal(); mdmDo("/api/mdm/reset", { all: false }); } }] }); }
});
document.addEventListener("change", async e => {
  if (e.target.id === "mdm-p7") mdmDo("/api/mdm/decrypt", { file: await mdmFile(e.target) }, "Decrypted — now get the certificate from Apple");
  if (e.target.id === "mdm-pem") {
    const pem = await mdmFile(e.target);
    try { await api("/api/mdm/pushcert", { pem }); toast("Push certificate saved", "ok"); }
    catch (err) {
      if (/another topic/.test(err.message)) openModal({ title: "Switch to a new topic?", body: `<p>${esc(err.message)}</p>`, buttons: [{ label: "Cancel" }, { label: "Switch", cls: "danger", onClick: () => { closeModal(); mdmDo("/api/mdm/pushcert", { pem, new_topic: true }, "Push certificate saved"); } }] });
      else toast(err.message, "err");
    }
    renderMdm();
  }
});
