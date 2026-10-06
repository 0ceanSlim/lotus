// Server page: fills limits from /api/v1/server/info and totals from /stats.

(function () {
  "use strict";

  function set(sel, text) {
    document.querySelectorAll(sel).forEach((el) => (el.textContent = text));
  }

  // Each supported BUD links to its spec in the Blossom repo.
  const BUD_TITLES = {
    "BUD-01": "Server requirements and blob retrieval",
    "BUD-02": "Blob upload and management",
    "BUD-03": "User server list",
    "BUD-04": "Mirroring blobs",
    "BUD-05": "Media optimization",
    "BUD-06": "Upload requirements",
    "BUD-07": "Payment required",
    "BUD-08": "Nostr file metadata tags",
    "BUD-09": "Blob report",
    "BUD-10": "Blossom URI schema",
    "BUD-11": "Nostr authorization",
    "BUD-12": "Blob management endpoints",
  };

  function renderBuds(buds) {
    const wrap = document.querySelector('[data-fact="buds"]');
    if (!wrap) return;
    wrap.innerHTML = "";
    buds.forEach((id) => {
      const num = String(id).replace(/^BUD-/, "");
      const a = document.createElement("a");
      a.href = "https://github.com/hzrd149/blossom/blob/master/buds/" + num + ".md";
      a.target = "_blank";
      a.rel = "noopener";
      a.title = BUD_TITLES[id] || id;
      a.className =
        "inline-block px-2 py-0.5 font-mono text-xs border rounded border-border-strong text-text hover:bg-surface-hover hover:border-accent hover:text-accent";
      a.textContent = id;
      wrap.appendChild(a);
    });
  }

  async function loadInfo() {
    try {
      const r = await fetch("/api/v1/server/info");
      if (!r.ok) return;
      const info = await r.json();
      const fmt = window.lotusFmt;
      set('[data-fact="max-upload"]', info.max_upload_bytes > 0 ? fmt.bytes(info.max_upload_bytes) : "no limit");
      set('[data-fact="quota"]', info.quota_bytes > 0 ? fmt.bytes(info.quota_bytes) : "unlimited");
      if (info.retention) set('[data-fact="retention"]', info.retention);
      if (info.cost) set('[data-fact="cost"]', info.cost);
      const mimes = info.allowed_mime_types || [];
      set('[data-fact="mime-types"]', mimes.length === 1 && mimes[0] === "*" ? "any" : mimes.join(", ") || "any");
      renderBuds(info.buds || []);
      const admin = document.querySelector('[data-fact="admin"]');
      if (admin) {
        if (info.admin_npub) {
          admin.textContent = info.admin_npub.slice(0, 12) + "…" + info.admin_npub.slice(-4);
          admin.href = info.admin_profile_url || "#";
          admin.title = info.admin_npub;
          admin.target = "_blank";
          admin.rel = "noopener";
        } else {
          admin.textContent = "not set";
          admin.removeAttribute("href");
        }
      }
    } catch (_) {}
  }

  async function loadStats() {
    try {
      const r = await fetch("/stats");
      if (!r.ok) return;
      const s = await r.json();
      set('[data-stat="storage"]', window.lotusFmt.bytes(s.bytes_stored));
      set('[data-stat="blobs"]', Number(s.blob_count || 0).toLocaleString());
      set('[data-stat="users"]', Number(s.pubkey_count || 0).toLocaleString());
    } catch (_) {}
  }

  window.LotusLanding = {
    init: function () {
      if (!document.getElementById("server-facts")) return;
      loadInfo();
      loadStats();
    },
  };
})();
