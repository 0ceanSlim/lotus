// Settings page: account details plus the media-server section.

(function () {
  "use strict";

  const $ = (id) => document.getElementById(id);

  async function fillAccount() {
    let sess = null;
    try {
      const r = await fetch("/api/v1/session", { cache: "no-store" });
      if (r.ok) sess = await r.json();
    } catch (_) {}
    if (!sess || !sess.publicKey) return;

    $("acct-hex").textContent = sess.publicKey;
    const methods = {
      browser_extension: "Browser extension (NIP-07)",
      bunker: "Remote signer (NIP-46)",
      amber: "Amber (NIP-55)",
      encrypted_key: "Private key, encrypted in this browser",
      google: "Google cloud login",
      pomegranate: "Google secure login",
      none: "Read-only",
    };
    $("acct-method").textContent = methods[sess.signingMethod] || sess.signingMethod || "—";

    try {
      const r = await fetch("/api/v1/keys/convert/public/" + sess.publicKey);
      if (r.ok) {
        const j = await r.json();
        if (j.npub) $("acct-npub").textContent = j.npub;
      }
    } catch (_) {}

    try {
      const r = await fetch("/api/v1/drive/files", { cache: "no-store" });
      if (r.ok) {
        const j = await r.json();
        const fmt = window.lotusFmt;
        $("acct-storage").textContent =
          fmt.bytes(j.total_bytes || 0) + (j.quota_bytes > 0 ? " of " + fmt.bytes(j.quota_bytes) : "") +
          " · " + (j.count || 0) + " files";
      } else {
        $("acct-storage").textContent = "sign in with a signer to see your files";
      }
    } catch (_) {}
  }

  window.LotusSettings = {
    init: function () {
      if (!$("settings")) return;
      fillAccount();
      if (window.LotusMedia) window.LotusMedia.initSettings();
    },
  };
})();
