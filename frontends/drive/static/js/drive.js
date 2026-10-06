// The drive: list, filter, preview, upload and delete the signed-in user's
// blobs on this server. Uploads and deletes are real Blossom requests signed
// with the user's key (kind 24242), exactly as any other client would send
// them; the listing comes from the session-gated /api/v1/drive/files.

(function () {
  "use strict";

  const state = {
    files: [],
    filter: "all",
    query: "",
    quota: 0,
    used: 0,
    bound: false,
    current: null,
  };

  const $ = (sel, root) => (root || document).querySelector(sel);
  const fmt = () => window.lotusFmt;
  const esc = (s) => window.lotusFmt.escapeHtml(s);

  // ── Classification ────────────────────────────────────────────────

  function category(type) {
    type = (type || "").toLowerCase();
    if (type.startsWith("image/")) return "image";
    if (type.startsWith("video/")) return "video";
    if (type.startsWith("audio/")) return "audio";
    return "other";
  }

  function icon(type) {
    const c = category(type);
    if (c === "image") return "🖼️";
    if (c === "video") return "🎬";
    if (c === "audio") return "🎵";
    if ((type || "").includes("pdf")) return "📄";
    if (/zip|compressed|tar|7z|rar/.test(type || "")) return "📦";
    if ((type || "").startsWith("text/")) return "📝";
    return "📎";
  }

  function label(type) {
    const t = (type || "application/octet-stream").split(";")[0];
    const sub = t.split("/")[1] || t;
    return sub.replace(/^x-/, "").replace("vnd.", "").slice(0, 12);
  }

  // ── Signing ───────────────────────────────────────────────────────

  async function signAuth(action, hash, content) {
    const signer = await window.ensureSigner();
    const now = Math.floor(Date.now() / 1000);
    const signed = await signer.signEvent({
      kind: 24242,
      created_at: now,
      content: content || action,
      tags: [
        ["t", action],
        ["x", hash],
        ["expiration", String(now + 600)],
      ],
    });
    return "Nostr " + window.lotusNip98.b64(JSON.stringify(signed));
  }

  // ── Data ──────────────────────────────────────────────────────────

  async function load() {
    const grid = $("#drive-grid");
    if (!grid) return;
    try {
      const r = await fetch("/api/v1/drive/files", { cache: "no-store" });
      if (r.status === 401 || r.status === 403) {
        const j = await r.json().catch(() => ({}));
        grid.innerHTML =
          '<div class="col-span-full py-16 text-center text-text-secondary">' +
          esc(j.message || "Sign in to see your files") +
          '<div class="mt-4"><button type="button" onclick="showAuthModal()" class="px-4 py-2 text-sm rounded-md bg-accent text-accent-fg">Sign in</button></div></div>';
        return;
      }
      if (!r.ok) throw new Error("HTTP " + r.status);
      const data = await r.json();
      state.files = data.files || [];
      state.used = data.total_bytes || 0;
      state.quota = data.quota_bytes || 0;
      render();
      if (window.LotusMedia) window.LotusMedia.checkThisServer();
    } catch (e) {
      grid.innerHTML = '<div class="col-span-full py-16 text-center text-danger">Could not load files: ' + esc(e.message) + "</div>";
    }
  }

  function visibleFiles() {
    const q = state.query.trim().toLowerCase();
    return state.files.filter((f) => {
      if (state.filter !== "all" && category(f.type) !== state.filter) return false;
      if (q && !(f.sha256.includes(q) || (f.type || "").toLowerCase().includes(q))) return false;
      return true;
    });
  }

  // ── Rendering ─────────────────────────────────────────────────────

  function renderHeader() {
    const used = state.used;
    const quota = state.quota;
    $('[data-drive="count"]').textContent = state.files.length.toLocaleString();
    $('[data-drive="used"]').textContent = fmt().bytes(used);
    $('[data-drive="quota-label"]').textContent = quota > 0 ? " of " + fmt().bytes(quota) : " used";
    const pct = quota > 0 ? Math.min(100, (used / quota) * 100) : 0;
    const bar = $('[data-drive="bar"]');
    bar.style.width = pct + "%";
    bar.className = "h-full transition-all rounded-full " + (pct > 90 ? "bg-danger" : pct > 75 ? "bg-warning" : "bg-accent");
  }

  function thumb(f) {
    const c = category(f.type);
    if (c === "image") {
      return '<img src="' + esc(f.url) + '" alt="" loading="lazy" class="object-cover w-full h-full" />';
    }
    if (c === "video") {
      return '<video src="' + esc(f.url) + '" preload="metadata" muted playsinline class="object-cover w-full h-full"></video>';
    }
    return '<span class="text-4xl">' + icon(f.type) + "</span>";
  }

  function card(f) {
    return (
      '<div class="relative overflow-hidden transition-colors border rounded-lg cursor-pointer group bg-surface border-border hover:border-border-strong" data-hash="' + esc(f.sha256) + '">' +
      '<div class="flex items-center justify-center overflow-hidden aspect-square bg-surface-inset-strong">' + thumb(f) + "</div>" +
      '<div class="p-2 text-xs">' +
      '<div class="flex items-center justify-between gap-2"><span class="px-1.5 py-0.5 rounded bg-surface-overlay text-text-secondary truncate">' + esc(label(f.type)) + '</span><span class="text-text-secondary shrink-0">' + fmt().bytes(f.size) + "</span></div>" +
      '<div class="mt-1 text-text-muted">' + esc(fmt().date(f.uploaded)) + "</div>" +
      "</div>" +
      '<button type="button" data-action="copy" title="Copy link" class="absolute top-1.5 right-1.5 hidden px-2 py-1 text-xs rounded bg-surface/90 border border-border group-hover:block hover:bg-surface-hover">🔗</button>' +
      "</div>"
    );
  }

  function render() {
    const grid = $("#drive-grid");
    const empty = $("#drive-empty");
    if (!grid) return;
    renderHeader();
    const files = visibleFiles();
    if (!state.files.length) {
      empty.classList.remove("hidden");
      grid.innerHTML = "";
      return;
    }
    empty.classList.add("hidden");
    if (!files.length) {
      grid.innerHTML = '<div class="col-span-full py-12 text-center text-text-secondary">No files match.</div>';
      return;
    }
    grid.innerHTML = files.map(card).join("");
  }

  // ── Detail modal ──────────────────────────────────────────────────

  function openModal(f) {
    const m = $("#drive-modal");
    if (!m) return;
    state.current = f;
    const c = category(f.type);
    let preview;
    if (c === "image") preview = '<img src="' + esc(f.url) + '" alt="" class="max-h-[60vh] object-contain" />';
    else if (c === "video") preview = '<video src="' + esc(f.url) + '" controls class="max-h-[60vh]"></video>';
    else if (c === "audio") preview = '<audio src="' + esc(f.url) + '" controls class="w-full p-6"></audio>';
    else preview = '<div class="py-16 text-6xl">' + icon(f.type) + "</div>";
    $('[data-modal="preview"]', m).innerHTML = preview;
    $('[data-modal="type"]', m).textContent = f.type || "unknown type";
    $('[data-modal="size"]', m).textContent = fmt().bytes(f.size);
    $('[data-modal="date"]', m).textContent = f.uploaded ? new Date(f.uploaded * 1000).toLocaleString() : "";
    $('[data-modal="url"]', m).textContent = f.url;
    $('[data-modal="open"]', m).href = f.url;
    renderShortLink(f);
    m.classList.remove("hidden");
    m.classList.add("flex");
  }

  function renderShortLink(f) {
    const m = $("#drive-modal");
    const has = !!f.short_url;
    $('[data-modal="short-url"]', m).textContent = f.short_url || "";
    $('[data-modal="short-url"]', m).classList.toggle("hidden", !has);
    $('[data-modal="short-empty"]', m).classList.toggle("hidden", has);
    $('[data-modal="short-create"]', m).classList.toggle("hidden", has);
    $('[data-modal="short-copy"]', m).classList.toggle("hidden", !has);
    $('[data-modal="short-revoke"]', m).classList.toggle("hidden", !has);
  }

  async function createShortLink(f) {
    const btn = $('[data-modal="short-create"]', $("#drive-modal"));
    btn.disabled = true;
    try {
      const r = await fetch("/api/v1/drive/files/" + f.sha256 + "/short", { method: "POST" });
      const j = await r.json().catch(() => ({}));
      if (!r.ok) throw new Error(j.message || "HTTP " + r.status);
      f.short_url = j.short_url;
      f.short_code = j.code;
      renderShortLink(f);
      window.lotusCopy(f.short_url);
    } catch (e) {
      window.lotusToast("Short link failed: " + (e.message || e), "danger");
    } finally {
      btn.disabled = false;
    }
  }

  async function revokeShortLink(f) {
    if (!f.short_code) return;
    if (!confirm("Revoke this short link? Anyone using it will get a 404. The file and its real link stay.")) return;
    try {
      const r = await fetch("/api/v1/drive/short/" + f.short_code, { method: "DELETE" });
      const j = await r.json().catch(() => ({}));
      if (!r.ok) throw new Error(j.message || "HTTP " + r.status);
      delete f.short_url;
      delete f.short_code;
      renderShortLink(f);
      window.lotusToast("Short link revoked", "success");
    } catch (e) {
      window.lotusToast("Revoke failed: " + (e.message || e), "danger");
    }
  }

  function closeModal() {
    const m = $("#drive-modal");
    if (!m) return;
    m.classList.add("hidden");
    m.classList.remove("flex");
    $('[data-modal="preview"]', m).innerHTML = "";
    state.current = null;
  }

  // ── Delete ────────────────────────────────────────────────────────

  async function deleteFile(f) {
    if (!confirm("Delete this file from the server? Any post that embeds its link will break.")) return;
    try {
      const auth = await signAuth("delete", f.sha256, "Delete " + f.sha256.slice(0, 8));
      const r = await fetch("/" + f.sha256, { method: "DELETE", headers: { Authorization: auth } });
      if (!r.ok) {
        const j = await r.json().catch(() => ({}));
        throw new Error(j.message || "HTTP " + r.status);
      }
      state.files = state.files.filter((x) => x.sha256 !== f.sha256);
      state.used = Math.max(0, state.used - (f.size || 0));
      closeModal();
      render();
      window.lotusToast("Deleted", "success");
    } catch (e) {
      window.lotusToast("Delete failed: " + (e.message || e), "danger");
    }
  }

  // ── Upload ────────────────────────────────────────────────────────

  function uploadRow(file) {
    const wrap = $("#drive-uploads");
    wrap.classList.remove("hidden");
    const row = document.createElement("div");
    row.className = "flex items-center gap-3 px-3 py-2 text-sm border rounded-md bg-surface border-border";
    row.innerHTML =
      '<span class="truncate flex-1">' + esc(file.name) + "</span>" +
      '<span class="text-xs text-text-secondary shrink-0">' + fmt().bytes(file.size) + "</span>" +
      '<div class="w-32 h-1.5 overflow-hidden rounded-full bg-surface-inset shrink-0"><div data-bar class="h-full bg-accent" style="width:0%"></div></div>' +
      '<span data-status class="w-24 text-xs text-right text-text-secondary shrink-0">hashing…</span>';
    wrap.appendChild(row);
    return {
      progress(p) {
        $("[data-bar]", row).style.width = Math.round(p * 100) + "%";
      },
      status(text, cls) {
        const s = $("[data-status]", row);
        s.textContent = text;
        s.className = "w-24 text-xs text-right shrink-0 " + (cls || "text-text-secondary");
      },
      done(ok) {
        setTimeout(() => {
          row.remove();
          if (!wrap.children.length) wrap.classList.add("hidden");
        }, ok ? 1500 : 8000);
      },
    };
  }

  function putBlob(file, auth, onProgress) {
    return new Promise((resolve, reject) => {
      const xhr = new XMLHttpRequest();
      xhr.open("PUT", "/upload", true);
      xhr.setRequestHeader("Authorization", auth);
      xhr.setRequestHeader("Content-Type", file.type || "application/octet-stream");
      xhr.upload.onprogress = (e) => {
        if (e.lengthComputable) onProgress(e.loaded / e.total);
      };
      xhr.onload = () => {
        if (xhr.status >= 200 && xhr.status < 300) {
          try {
            resolve(JSON.parse(xhr.responseText));
          } catch (_) {
            reject(new Error("unexpected response"));
          }
        } else {
          let msg = "HTTP " + xhr.status;
          try {
            const j = JSON.parse(xhr.responseText);
            if (j.message) msg = j.message;
          } catch (_) {}
          reject(new Error(msg));
        }
      };
      xhr.onerror = () => reject(new Error("network error"));
      xhr.send(file);
    });
  }

  async function uploadOne(file) {
    const row = uploadRow(file);
    try {
      const hash = await window.lotusNip98.sha256Hex(await file.arrayBuffer());
      row.status("signing…");
      const auth = await signAuth("upload", hash, "Upload " + file.name);
      row.status("uploading…");
      const desc = await putBlob(file, auth, row.progress);
      row.progress(1);
      row.status("✓ done", "text-success");
      row.done(true);
      if (!state.files.some((f) => f.sha256 === desc.sha256)) {
        state.files.unshift({
          sha256: desc.sha256,
          url: desc.url,
          size: desc.size || file.size,
          type: desc.type || file.type,
          uploaded: desc.uploaded || Math.floor(Date.now() / 1000),
        });
        state.used += desc.size || file.size;
      }
      render();
    } catch (e) {
      row.status("✗ " + (e.message || e), "text-danger");
      row.done(false);
      window.lotusToast("Upload failed: " + (e.message || e), "danger");
    }
  }

  async function handleFiles(list) {
    const files = Array.from(list || []);
    if (!files.length) return;
    try {
      await window.ensureSigner();
    } catch (_) {
      return;
    }
    for (const f of files) await uploadOne(f);
  }

  // ── Wiring ────────────────────────────────────────────────────────

  function bind() {
    if (state.bound) return;
    state.bound = true;

    document.addEventListener("click", function (e) {
      const root = $("#drive");
      if (!root) return;

      const filterBtn = e.target.closest("[data-filter]");
      if (filterBtn && root.contains(filterBtn)) {
        state.filter = filterBtn.getAttribute("data-filter");
        $("#drive-filters")
          .querySelectorAll("button")
          .forEach((b) => {
            b.className = b === filterBtn ? "px-3 py-1 rounded bg-accent text-accent-fg" : "px-3 py-1 rounded text-text-secondary hover:text-text";
          });
        render();
        return;
      }

      if (e.target.closest("#drive-upload-btn")) {
        $("#drive-file-input").click();
        return;
      }

      const copyBtn = e.target.closest('[data-action="copy"]');
      if (copyBtn) {
        e.stopPropagation();
        const hash = copyBtn.closest("[data-hash]").getAttribute("data-hash");
        const f = state.files.find((x) => x.sha256 === hash);
        if (f) window.lotusCopy(f.url);
        return;
      }

      const cardEl = e.target.closest("#drive-grid [data-hash]");
      if (cardEl) {
        const f = state.files.find((x) => x.sha256 === cardEl.getAttribute("data-hash"));
        if (f) openModal(f);
        return;
      }

      const modal = $("#drive-modal");
      if (modal && !modal.classList.contains("hidden")) {
        if (e.target.closest('[data-modal="close"]') || e.target === modal) closeModal();
        else if (e.target.closest('[data-modal="copy"]') && state.current) window.lotusCopy(state.current.url);
        else if (e.target.closest('[data-modal="short-create"]') && state.current) createShortLink(state.current);
        else if (e.target.closest('[data-modal="short-copy"]') && state.current) window.lotusCopy(state.current.short_url);
        else if (e.target.closest('[data-modal="short-revoke"]') && state.current) revokeShortLink(state.current);
        else if (e.target.closest('[data-modal="delete"]') && state.current) deleteFile(state.current);
      }
    });

    document.addEventListener("change", function (e) {
      if (e.target && e.target.id === "drive-file-input") {
        handleFiles(e.target.files);
        e.target.value = "";
      }
    });

    document.addEventListener("input", function (e) {
      if (e.target && e.target.id === "drive-search") {
        state.query = e.target.value;
        render();
      }
    });

    document.addEventListener("keydown", function (e) {
      if (e.key === "Escape") closeModal();
    });

    // Drag and drop anywhere on the page while the drive is open.
    let dragDepth = 0;
    const overlay = () => $("#drive-drop");
    document.addEventListener("dragenter", function (e) {
      if (!$("#drive") || !e.dataTransfer || !Array.from(e.dataTransfer.types).includes("Files")) return;
      e.preventDefault();
      dragDepth++;
      overlay().classList.remove("hidden");
      overlay().classList.add("flex");
    });
    document.addEventListener("dragover", function (e) {
      if ($("#drive")) e.preventDefault();
    });
    document.addEventListener("dragleave", function (e) {
      if (!$("#drive")) return;
      dragDepth = Math.max(0, dragDepth - 1);
      if (dragDepth === 0) {
        overlay().classList.add("hidden");
        overlay().classList.remove("flex");
      }
    });
    document.addEventListener("drop", function (e) {
      if (!$("#drive")) return;
      e.preventDefault();
      dragDepth = 0;
      overlay().classList.add("hidden");
      overlay().classList.remove("flex");
      handleFiles(e.dataTransfer.files);
    });
  }

  window.LotusDrive = {
    init: function () {
      if (!document.getElementById("drive")) return;
      bind();
      state.filter = "all";
      state.query = "";
      load();
    },
    reload: load,
  };
})();
