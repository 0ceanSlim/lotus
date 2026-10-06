// The user's Blossom server list (kind 10063).
//
// Other Nostr clients choose their upload server from this list, so getting
// this server into it, ideally first, is how a user's attachments start
// landing here. Two surfaces share this module: the settings page (view,
// reorder, add, remove, save) and the drive banner (one-click add).
//
// Reads come from grain's resolver; saves build the unsigned event server-side
// (preserving any non-server tags), sign in the browser, and publish through
// grain's outbox routing with the live per-relay toast.

(function () {
  "use strict";

  const KIND = 10063;

  const state = {
    list: [], // ordered base URLs, primary first
    orig: [], // as loaded, to detect changes
    info: {}, // url -> metadata grain returned
    thisServer: "", // normalized cdn_url of this deployment
    pubkey: "",
    loaded: false,
  };
  let drag = null;

  const esc = (s) => window.lotusFmt.escapeHtml(s);
  const display = (u) => String(u || "").replace(/^https?:\/\//, "");

  // Mirrors grain's normalisation so comparisons with its resolved list hold.
  function norm(u) {
    let s = (u || "").trim();
    if (!s) return "";
    if (!/:\/\//.test(s)) s = "https://" + s;
    try {
      const url = new URL(s);
      if (url.protocol !== "http:" && url.protocol !== "https:") return "";
      return url.protocol + "//" + url.host.toLowerCase() + url.pathname.replace(/\/+$/, "");
    } catch (_) {
      return "";
    }
  }

  function sameList(a, b) {
    return a.length === b.length && a.every((v, i) => v === b[i]);
  }

  async function thisServer() {
    if (state.thisServer) return state.thisServer;
    try {
      const r = await fetch("/api/v1/server/info");
      if (r.ok) {
        const info = await r.json();
        state.thisServer = norm(info.url || window.location.origin);
      }
    } catch (_) {}
    if (!state.thisServer) state.thisServer = norm(window.location.origin);
    return state.thisServer;
  }

  // load resolves the user's list. Returns false when there is no session.
  async function load(refresh) {
    await thisServer();
    const r = await fetch("/api/v1/user/media-servers" + (refresh ? "?refresh=1" : ""), { cache: "no-store" });
    if (r.status === 401) return false;
    if (!r.ok) throw new Error("HTTP " + r.status);
    const me = await r.json();
    state.pubkey = me.pubkey || "";
    state.list = (me.blossom || []).map((e) => {
      state.info[e.url] = e;
      return e.url;
    });
    state.orig = state.list.slice();
    state.loaded = true;
    return true;
  }

  const hasThisServer = () => state.list.includes(state.thisServer);
  const thisIsPrimary = () => state.list[0] === state.thisServer;

  function add(url) {
    const u = norm(url);
    if (!u || state.list.includes(u)) return false;
    state.list.push(u);
    return true;
  }
  function remove(idx) {
    state.list.splice(idx, 1);
  }
  function move(from, to) {
    if (from === to || from == null || to == null) return;
    const [item] = state.list.splice(from, 1);
    state.list.splice(to, 0, item);
  }
  function addThisServer(primary) {
    const i = state.list.indexOf(state.thisServer);
    if (i >= 0) state.list.splice(i, 1);
    if (primary) state.list.unshift(state.thisServer);
    else state.list.push(state.thisServer);
  }

  // save builds, signs and publishes the list. status(msg) receives progress.
  async function save(status) {
    status = status || function () {};
    if (sameList(state.list, state.orig)) {
      status("No changes to save.");
      return { accepted: false, unchanged: true };
    }
    status("Building your server list…");
    const resp = await fetch("/api/v1/user/media-servers/build", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      // client_tag false: grain leaves the tag off and lotus stamps its own
      // before signing (see publish.js).
      body: JSON.stringify({ kind: KIND, servers: state.list, client_tag: false }),
    });
    if (!resp.ok) {
      const txt = await resp.text().catch(() => "");
      status("Couldn't build the list: " + (txt || resp.status));
      return null;
    }
    const unsigned = await resp.json();
    const res = await window.lotusPublish.signAndPublish(unsigned, {
      title: "Publishing your Blossom server list…",
      status,
    });
    if (res && res.accepted) {
      state.orig = state.list.slice();
      status("✓ Saved and published to your relays.");
      // Drop grain's cached copy so the next read shows the new list.
      fetch("/api/v1/user/media-servers?refresh=1").catch(() => {});
    } else if (res) {
      status("Signed, but no relay accepted it yet. See the toast for details.");
    } else {
      status("Publish failed. See the toast or the browser console.");
    }
    return res;
  }

  // ── Settings page ─────────────────────────────────────────────────

  const $ = (id) => document.getElementById(id);

  function chip(label, cls) {
    return `<span class="inline-flex items-center px-2 py-0.5 text-xs font-medium rounded ${cls}">${esc(label)}</span>`;
  }

  function row(url, idx) {
    const info = state.info[url] || {};
    const chips = [];
    if (idx === 0) chips.push(chip("Primary", "bg-accent-dim text-accent"));
    if (url === state.thisServer) chips.push(chip("This server", "bg-success-dim text-success"));
    if (info.retention) chips.push(chip(info.retention, "border border-border-strong text-text-secondary"));
    return (
      `<div class="flex items-center gap-2.5 px-3 py-2 border rounded-lg bg-surface-elevated border-border" data-idx="${idx}">` +
      `<span class="text-lg cursor-grab select-none shrink-0 text-text-muted" draggable="true" data-grip title="Drag to reorder">⠿</span>` +
      `<div class="flex-1 min-w-0">` +
      `<a href="${esc(url)}" target="_blank" rel="noopener" class="block text-sm font-medium truncate text-text hover:text-accent">${esc(display(url))}</a>` +
      (chips.length ? `<div class="flex flex-wrap items-center gap-1 mt-1">${chips.join("")}</div>` : "") +
      `</div>` +
      (idx > 0 ? `<button data-up class="px-1 text-sm shrink-0 text-text-muted hover:text-text" title="Move up">↑</button>` : "") +
      `<button data-remove class="px-1 text-lg leading-none shrink-0 text-text-muted hover:text-danger" title="Remove">×</button>` +
      `</div>`
    );
  }

  function renderSettings() {
    const box = $("ms-list");
    if (!box) return;
    if (!state.list.length) {
      box.innerHTML =
        '<div class="p-4 text-sm text-center border border-dashed rounded-lg text-text-muted border-border-strong">No Blossom servers yet. Add this server, or paste a URL below.</div>';
    } else {
      box.innerHTML = state.list.map(row).join("");
    }

    box.querySelectorAll("[data-grip]").forEach((g) => {
      g.ondragstart = (e) => {
        drag = +g.closest("[data-idx]").dataset.idx;
        if (e.dataTransfer) e.dataTransfer.setData("text/plain", "");
      };
    });
    box.querySelectorAll("[data-idx]").forEach((r) => {
      r.ondragover = (e) => e.preventDefault();
      r.ondrop = (e) => {
        e.preventDefault();
        if (drag != null) move(drag, +r.dataset.idx);
        drag = null;
        renderSettings();
      };
    });
    box.querySelectorAll("[data-remove]").forEach((b) => {
      b.onclick = () => {
        remove(+b.closest("[data-idx]").dataset.idx);
        renderSettings();
      };
    });
    box.querySelectorAll("[data-up]").forEach((b) => {
      b.onclick = () => {
        const i = +b.closest("[data-idx]").dataset.idx;
        move(i, i - 1);
        renderSettings();
      };
    });

    // This-server card.
    const card = $("ms-this");
    if (card) {
      $("ms-this-url").textContent = display(state.thisServer);
      const addBtn = $("ms-add-this");
      const primaryBtn = $("ms-primary-this");
      const stateEl = $("ms-this-state");
      if (!hasThisServer()) {
        stateEl.textContent = "not in your list";
        addBtn.classList.remove("hidden");
        primaryBtn.classList.add("hidden");
      } else if (!thisIsPrimary()) {
        stateEl.textContent = "in your list as a mirror";
        addBtn.classList.add("hidden");
        primaryBtn.classList.remove("hidden");
      } else {
        stateEl.textContent = "your primary upload server";
        addBtn.classList.add("hidden");
        primaryBtn.classList.add("hidden");
      }
      card.classList.remove("hidden");
      card.classList.add("flex");
    }

    const saveBtn = $("ms-save");
    if (saveBtn) saveBtn.disabled = sameList(state.list, state.orig);
  }

  function setStatus(msg) {
    const el = $("ms-status");
    if (el) el.textContent = msg || "";
  }

  async function initSettings() {
    const box = $("ms-list");
    if (!box) return;
    box.innerHTML =
      '<div class="flex items-center gap-2 px-3 py-3 text-sm text-text-muted"><span class="inline-block w-4 h-4 border-2 rounded-full border-text-secondary border-t-transparent animate-spin"></span>Fetching your server list from your relays…</div>';
    try {
      const ok = await load();
      if (!ok) {
        box.innerHTML =
          '<div class="p-4 text-sm text-center border border-dashed rounded-lg text-text-muted border-border-strong">Sign in to manage your server list.</div>';
        return;
      }
      renderSettings();
    } catch (e) {
      box.innerHTML = `<div class="p-3 text-sm border border-dashed rounded-lg text-danger border-border-strong">Couldn't load your server list: ${esc(e.message)}</div>`;
      return;
    }

    if (box.dataset.bound) return;
    box.dataset.bound = "1";
    $("ms-add").onclick = () => {
      const input = $("ms-input");
      if (add(input.value)) renderSettings();
      else if (input.value.trim()) setStatus("That doesn't look like a server URL, or it's already listed.");
      input.value = "";
    };
    $("ms-input").onkeydown = (e) => {
      if (e.key === "Enter") $("ms-add").click();
    };
    $("ms-add-this").onclick = () => {
      addThisServer(state.list.length === 0);
      renderSettings();
    };
    $("ms-primary-this").onclick = () => {
      addThisServer(true);
      renderSettings();
    };
    $("ms-save").onclick = async () => {
      const btn = $("ms-save");
      btn.disabled = true;
      try {
        await save(setStatus);
      } finally {
        renderSettings();
      }
    };
  }

  // ── Drive banner ──────────────────────────────────────────────────

  const dismissKey = () => "lotus-server-banner-dismissed:" + state.pubkey;

  async function checkThisServer() {
    const banner = document.getElementById("drive-server-banner");
    if (!banner) return;
    try {
      const ok = await load();
      if (!ok) return;
    } catch (_) {
      return;
    }
    let dismissed = false;
    try {
      dismissed = localStorage.getItem(dismissKey()) === "1";
    } catch (_) {}
    if (hasThisServer() || dismissed) {
      banner.classList.add("hidden");
      banner.classList.remove("flex");
      return;
    }
    banner.classList.remove("hidden");
    banner.classList.add("flex");
    if (banner.dataset.bound) return;
    banner.dataset.bound = "1";
    banner.querySelector('[data-banner="dismiss"]').onclick = () => {
      try {
        localStorage.setItem(dismissKey(), "1");
      } catch (_) {}
      banner.classList.add("hidden");
      banner.classList.remove("flex");
    };
    banner.querySelector('[data-banner="add"]').onclick = async () => {
      const btn = banner.querySelector('[data-banner="add"]');
      const note = banner.querySelector("[data-banner-status]");
      btn.disabled = true;
      addThisServer(state.list.length === 0);
      const res = await save((m) => {
        if (note) note.textContent = m;
      });
      btn.disabled = false;
      if (res && res.accepted) {
        banner.classList.add("hidden");
        banner.classList.remove("flex");
        window.lotusToast("Added to your Blossom server list", "success");
      }
    };
  }

  window.LotusMedia = { state, load, save, add, remove, move, addThisServer, initSettings, checkThisServer };
})();
