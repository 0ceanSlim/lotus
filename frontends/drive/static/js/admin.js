// Operator dashboard. Loads the running config, fills the forms, and posts
// per-section patches signed with a NIP-98 authorization from the operator's
// key. The server validates, applies to the running services, and writes
// config.yml; the response carries the new config so the forms stay in sync.

(function () {
  "use strict";

  const $ = (sel, root) => (root || document).querySelector(sel);
  const $$ = (sel, root) => Array.from((root || document).querySelectorAll(sel));
  const esc = (s) => window.lotusFmt.escapeHtml(s);

  let cfg = null;
  let rules = [];

  // Profile links follow the configured template; {npub} and {hex} expand.
  function profileUrl(npub, hex) {
    const tpl = (cfg && cfg.server && cfg.server.profile_url) || "https://njump.me/{npub}";
    return tpl.replace("{npub}", npub || "").replace("{hex}", hex || "");
  }

  const RESOURCES = ["UPLOAD", "GET", "DELETE", "LIST", "MIRROR"];

  function field(name) {
    return $('[data-field="' + name + '"]');
  }
  function setField(name, value) {
    const el = field(name);
    if (!el) return;
    if (el.type === "checkbox") el.checked = !!value;
    else el.value = value == null ? "" : value;
  }
  function getField(name) {
    const el = field(name);
    if (!el) return null;
    return el.type === "checkbox" ? el.checked : el.value;
  }

  function fill() {
    const s = cfg.server || {};
    setField("server.name", s.name);
    setField("server.icon", s.icon);
    setField("server.description", s.description);
    setField("server.contact", s.contact);
    setField("server.cost", s.cost || "free");
    setField("server.membership_url", s.membership_url);
    setField("server.terms_url", s.terms_url);
    setField("server.privacy_url", s.privacy_url);
    setField("server.profile_url", s.profile_url);
    setField("public_listing", cfg.public_listing !== false && cfg.public_listing !== null ? cfg.public_listing : false);
    setField("max_upload_mb", Math.round((cfg.max_upload_size_bytes || 0) / 1048576));
    setField("quota_gb", Math.round(((cfg.max_storage_per_pubkey_bytes || 0) / 1073741824) * 10) / 10);
    setField("nostr_users_url", cfg.nostr_users_url);
    setField("mime_types", (cfg.allowed_mime_types || []).join("\n"));
    $$("[data-ro]").forEach((el) => (el.textContent = cfg[el.getAttribute("data-ro")] || "—"));
    rules = (cfg.access_control_rules || []).map((r) => ({ action: r.action, pubkey: r.pubkey, resource: r.resource }));
    renderRules();
  }

  // ── Access rules editor ───────────────────────────────────────────

  function renderRules() {
    const box = $("#acr-rows");
    if (!box) return;
    if (!rules.length) {
      box.innerHTML = '<div class="p-3 text-xs border border-dashed rounded-md text-text-muted border-border-strong">No rules. Nothing is allowed until you add some.</div>';
      return;
    }
    box.innerHTML = rules
      .map(
        (r, i) =>
          `<div class="grid grid-cols-[6rem_1fr_7rem_2rem] gap-2 items-center" data-rule="${i}">` +
          `<select data-rule-action class="px-2 py-1.5 text-sm border rounded-md bg-surface-elevated border-border">` +
          `<option value="ALLOW"${r.action === "ALLOW" ? " selected" : ""}>Allow</option><option value="DENY"${r.action === "DENY" ? " selected" : ""}>Deny</option></select>` +
          `<input data-rule-pubkey value="${esc(r.pubkey)}" placeholder="ALL, npub… or hex" class="px-2 py-1.5 font-mono text-xs border rounded-md bg-surface-elevated border-border" />` +
          `<select data-rule-resource class="px-2 py-1.5 text-sm border rounded-md bg-surface-elevated border-border">` +
          RESOURCES.map((x) => `<option value="${x}"${r.resource === x ? " selected" : ""}>${x}</option>`).join("") +
          `</select>` +
          `<button type="button" data-rule-remove class="text-lg leading-none text-text-muted hover:text-danger" title="Remove">×</button>` +
          `</div>`
      )
      .join("");
    $$("[data-rule]", box).forEach((row) => {
      const i = +row.getAttribute("data-rule");
      $("[data-rule-action]", row).onchange = (e) => (rules[i].action = e.target.value);
      $("[data-rule-pubkey]", row).oninput = (e) => (rules[i].pubkey = e.target.value.trim());
      $("[data-rule-resource]", row).onchange = (e) => (rules[i].resource = e.target.value);
      $("[data-rule-remove]", row).onclick = () => {
        rules.splice(i, 1);
        renderRules();
      };
    });
  }

  // ── Save ──────────────────────────────────────────────────────────

  function patchFor(section) {
    switch (section) {
      case "identity":
        return {
          server: {
            name: getField("server.name").trim(),
            icon: getField("server.icon").trim(),
            description: getField("server.description").trim(),
            contact: getField("server.contact").trim(),
            cost: getField("server.cost"),
            membership_url: getField("server.membership_url").trim(),
            terms_url: getField("server.terms_url").trim(),
            privacy_url: getField("server.privacy_url").trim(),
            profile_url: getField("server.profile_url").trim(),
          },
          public_listing: !!getField("public_listing"),
        };
      case "limits":
        return {
          max_upload_size_bytes: Math.max(0, Math.round(Number(getField("max_upload_mb")) || 0) * 1048576),
          max_storage_per_pubkey_bytes: Math.max(0, Math.round((Number(getField("quota_gb")) || 0) * 1073741824)),
        };
      case "access":
        return { nostr_users_url: getField("nostr_users_url").trim(), access_control_rules: rules.map((r) => ({ ...r })) };
      case "mime":
        return {
          allowed_mime_types: getField("mime_types")
            .split(/\r?\n/)
            .map((s) => s.trim())
            .filter(Boolean),
        };
      case "operator": {
        const v = getField("admin_pubkey").trim();
        if (!v) throw new Error("Enter the new operator's npub or hex pubkey.");
        if (!confirm("Transfer this server to " + v + "? You will lose admin access.")) return null;
        return { admin_pubkey: v };
      }
    }
    return null;
  }

  async function save(section) {
    const sec = $('[data-section="' + section + '"]');
    const status = $("[data-section-status]", sec);
    const btn = $('[data-save="' + section + '"]', sec);
    const say = (m, cls) => {
      status.textContent = m;
      status.className = "text-xs " + (cls || "text-text-muted");
    };
    let patch;
    try {
      patch = patchFor(section);
    } catch (e) {
      say(e.message, "text-danger");
      return;
    }
    if (!patch) return;

    btn.disabled = true;
    try {
      say("Waiting for your signer…");
      await window.ensureSigner();
      const body = JSON.stringify(patch);
      const auth = await window.lotusNip98.header("POST", window.location.origin + "/api/v1/admin/config", body);
      say("Saving…");
      const r = await fetch("/api/v1/admin/config", {
        method: "POST",
        headers: { "Content-Type": "application/json", Authorization: auth },
        body,
      });
      const j = await r.json().catch(() => ({}));
      if (!r.ok) throw new Error(j.message || "HTTP " + r.status);
      cfg = j.config;
      fill();
      say("✓ Saved", "text-success");
      window.lotusToast("Saved and applied", "success");
      if (section === "operator") setTimeout(() => window.location.assign("/"), 1200);
    } catch (e) {
      say(e.message || String(e), "text-danger");
    } finally {
      btn.disabled = false;
    }
  }

  // ── Stats and users ───────────────────────────────────────────────

  async function loadStats() {
    try {
      const r = await fetch("/stats");
      if (!r.ok) return;
      const s = await r.json();
      $('[data-stat="storage"]').textContent = window.lotusFmt.bytes(s.bytes_stored);
      $('[data-stat="blobs"]').textContent = Number(s.blob_count || 0).toLocaleString();
      $('[data-stat="users"]').textContent = Number(s.pubkey_count || 0).toLocaleString();
    } catch (_) {}
  }

  async function loadUsers() {
    const body = $("#admin-users");
    if (!body) return;
    body.innerHTML = '<tr><td colspan="5" class="py-3 text-text-muted">Loading…</td></tr>';
    try {
      const r = await fetch("/api/v1/admin/users", { cache: "no-store" });
      const j = await r.json();
      if (!r.ok) throw new Error(j.message || "HTTP " + r.status);
      $('[data-stat="members"]').textContent = Number(j.members || 0).toLocaleString();
      if (!j.users.length) {
        body.innerHTML = '<tr><td colspan="5" class="py-3 text-text-muted">No uploads yet.</td></tr>';
        return;
      }
      const fmt = window.lotusFmt;
      body.innerHTML = j.users
        .map((u) => {
          const name = u.npub ? u.npub.slice(0, 12) + "…" + u.npub.slice(-4) : u.pubkey.slice(0, 16);
          const quota = j.quota_bytes > 0 ? ' <span class="text-text-muted">/ ' + fmt.bytes(j.quota_bytes) + "</span>" : "";
          return (
            `<tr class="border-t border-border">` +
            `<td class="py-2 pr-3"><a href="${esc(profileUrl(u.npub, u.pubkey))}" target="_blank" rel="noopener" class="font-mono text-xs hover:text-accent" title="${esc(u.pubkey)}">${esc(name)}</a>` +
            (u.member ? ' <span class="px-1.5 py-0.5 text-xs rounded bg-accent-dim text-accent">member</span>' : "") +
            `</td>` +
            `<td class="py-2 pr-3">${u.files}</td>` +
            `<td class="py-2 pr-3">${fmt.bytes(u.bytes)}${quota}</td>` +
            `<td class="py-2 pr-3 text-text-secondary">${esc(fmt.date(u.last_upload))}</td>` +
            `<td class="py-2">${u.can_upload ? '<span class="text-success">allowed</span>' : '<span class="text-text-muted">no</span>'}</td>` +
            `</tr>`
          );
        })
        .join("");
    } catch (e) {
      body.innerHTML = `<tr><td colspan="5" class="py-3 text-danger">${esc(e.message)}</td></tr>`;
    }
  }

  // ── Init ──────────────────────────────────────────────────────────

  async function init() {
    if (!$("#admin")) return;
    try {
      const r = await fetch("/api/v1/admin/config", { cache: "no-store" });
      const j = await r.json();
      if (!r.ok) throw new Error(j.message || "HTTP " + r.status);
      cfg = j.config;
      $("#admin-operator-npub").textContent = j.admin_npub || cfg.admin_pubkey || "—";
      fill();
    } catch (e) {
      $("#admin-status").textContent = "Could not load config: " + e.message;
      return;
    }
    loadStats();
    loadUsers();

    const root = $("#admin");
    if (root.dataset.bound) return;
    root.dataset.bound = "1";
    $$("[data-save]").forEach((b) => (b.onclick = () => save(b.getAttribute("data-save"))));
    $("#acr-add").onclick = () => {
      rules.push({ action: "ALLOW", pubkey: "", resource: "UPLOAD" });
      renderRules();
      const inputs = $$("[data-rule-pubkey]");
      if (inputs.length) inputs[inputs.length - 1].focus();
    };
    $("#admin-users-refresh").onclick = loadUsers;
  }

  window.LotusAdmin = { init };
})();
