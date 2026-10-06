// Header state, profile dropdown, theme swapper, toasts and small shared
// formatters. Keeps the login button in sync with /api/v1/session and
// hydrates the signed-in user's name and picture from /api/v1/cache.

(function () {
  "use strict";

  // ── Shared helpers ────────────────────────────────────────────────

  function escapeHtml(s) {
    return String(s == null ? "" : s)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#39;");
  }

  function fmtBytes(n) {
    n = Number(n) || 0;
    if (n < 1024) return n + " B";
    const units = ["KB", "MB", "GB", "TB"];
    let i = -1;
    do {
      n /= 1024;
      i++;
    } while (n >= 1024 && i < units.length - 1);
    return (n >= 10 ? Math.round(n) : Math.round(n * 10) / 10) + " " + units[i];
  }

  function fmtDate(unix) {
    if (!unix) return "";
    const d = new Date(unix * 1000);
    const diff = (Date.now() - d.getTime()) / 1000;
    if (diff < 60) return "just now";
    if (diff < 3600) return Math.floor(diff / 60) + " min ago";
    if (diff < 86400) return Math.floor(diff / 3600) + " h ago";
    if (diff < 7 * 86400) return Math.floor(diff / 86400) + " d ago";
    return d.toLocaleDateString();
  }

  function toast(message, kind) {
    const stack = document.getElementById("toast-stack");
    if (!stack) return;
    const colors = {
      success: "border-success text-success",
      danger: "border-danger text-danger",
      warning: "border-warning text-warning",
      info: "border-border-strong text-text",
    };
    const el = document.createElement("div");
    el.className =
      "px-3 py-2 text-sm border rounded-md shadow-lg pointer-events-auto bg-surface " +
      (colors[kind] || colors.info);
    el.textContent = message;
    stack.appendChild(el);
    setTimeout(() => el.remove(), kind === "danger" ? 6000 : 3000);
  }

  async function copyText(text) {
    try {
      await navigator.clipboard.writeText(text);
      toast("Copied", "success");
    } catch (_) {
      toast("Copy failed", "danger");
    }
  }

  window.lotusFmt = { bytes: fmtBytes, date: fmtDate, escapeHtml };
  window.lotusToast = toast;
  window.lotusCopy = copyText;

  // Declarative copy buttons: data-copy="<selector>" copies that element's text.
  document.addEventListener("click", function (e) {
    const btn = e.target.closest && e.target.closest("[data-copy]");
    if (!btn) return;
    const target = document.querySelector(btn.getAttribute("data-copy"));
    if (target) copyText(target.textContent.trim());
  });

  // ── Login button ──────────────────────────────────────────────────

  const btn = () => document.getElementById("login-btn");
  const content = () => document.getElementById("login-btn-content");

  function renderLoggedOut() {
    const b = btn(), c = content();
    if (!b || !c) return;
    b.title = "Sign in";
    b.disabled = false;
    b.className =
      "flex items-center gap-2 px-3 py-2 text-sm font-medium transition-colors border rounded-md bg-accent text-accent-fg border-accent hover:bg-accent-hover";
    c.innerHTML = '<span>🗝️</span><span class="hidden sm:inline">Sign in</span>';
  }

  function renderLoading() {
    const b = btn(), c = content();
    if (!b || !c) return;
    b.title = "Signing you in…";
    b.disabled = true;
    b.className =
      "flex items-center gap-2 px-3 py-2 text-sm font-medium border rounded-md bg-surface-elevated text-text-secondary border-border cursor-wait";
    c.innerHTML =
      '<span class="inline-block w-4 h-4 rounded-full animate-spin" style="border: 2px solid var(--color-accent-dim); border-top-color: var(--color-accent);"></span>' +
      '<span class="hidden sm:inline">Signing in…</span>';
  }
  window.renderLoginLoading = renderLoading;

  function renderLoggedIn(profile, npub) {
    const b = btn(), c = content();
    if (!b || !c) return;
    const name = (profile && (profile.display_name || profile.name)) || (npub ? npub.slice(0, 12) + "…" : "You");
    b.disabled = false;
    b.title = name;
    b.className =
      "flex items-center gap-2 px-2 py-1 text-sm transition-colors border rounded-md bg-surface-elevated text-text border-border hover:bg-surface-hover";
    const pic = profile && profile.picture
      ? '<img src="' + escapeHtml(profile.picture) + '" alt="" class="object-cover w-6 h-6 rounded-full shrink-0" />'
      : '<span class="inline-flex items-center justify-center w-6 h-6 rounded-full bg-surface-overlay shrink-0">👤</span>';
    c.innerHTML = pic + '<span class="hidden sm:inline-block max-w-[12ch] truncate">' + escapeHtml(name) + "</span>";
  }

  // ── Profile hydration ─────────────────────────────────────────────

  let profileInfo = null;

  function parseContent(metadata) {
    if (!metadata) return null;
    if (typeof metadata.content === "string" && metadata.content) {
      try {
        return JSON.parse(metadata.content);
      } catch (_) {
        return null;
      }
    }
    if (metadata.display_name || metadata.name || metadata.picture) return metadata;
    return null;
  }

  async function npubFor(hex) {
    try {
      const r = await fetch("/api/v1/keys/convert/public/" + hex);
      if (!r.ok) return "";
      const j = await r.json();
      return j.npub || "";
    } catch (_) {
      return "";
    }
  }

  // /api/v1/cache answers immediately with pending:true on a cold cache and
  // fills in as the profile lands, so poll a few times.
  async function hydrateProfile() {
    for (let attempt = 0; attempt < 8; attempt++) {
      let data = null;
      try {
        const r = await fetch("/api/v1/cache");
        if (!r.ok) return;
        data = await r.json();
      } catch (_) {
        return;
      }
      const pubkey = data.publicKey || data.pubkey || "";
      const npub = data.npub || (pubkey ? await npubFor(pubkey) : "");
      const content = parseContent(data.metadata);
      profileInfo = { pubkey, npub, content };
      renderLoggedIn(content, npub);
      applyDropdownProfile(profileInfo);
      if (content || !data.pending) return;
      await new Promise((r) => setTimeout(r, 1500));
    }
  }

  function applyDropdownProfile(info) {
    if (!info) return;
    const nameEl = document.getElementById("user-dropdown-name");
    const npubEl = document.getElementById("user-dropdown-npub");
    const pfp = document.getElementById("user-dropdown-pfp-wrap");
    if (!nameEl || !npubEl || !pfp) return;
    const c = info.content || {};
    nameEl.textContent = c.display_name || c.name || (info.npub ? info.npub.slice(0, 12) + "…" : "You");
    npubEl.textContent = info.npub ? info.npub.slice(0, 14) + "…" + info.npub.slice(-4) : "";
    pfp.innerHTML = c.picture
      ? '<img src="' + escapeHtml(c.picture) + '" alt="" class="object-cover w-10 h-10 rounded-full" />'
      : '<span class="text-lg">👤</span>';
  }

  // ── Dropdown ──────────────────────────────────────────────────────

  const menu = () => document.getElementById("user-dropdown-menu");

  function positionDropdown() {
    const m = menu(), b = btn();
    if (!m || !b) return;
    const r = b.getBoundingClientRect();
    m.style.top = r.bottom + 8 + "px";
    m.style.right = Math.max(8, window.innerWidth - r.right) + "px";
    m.style.left = "auto";
  }

  function clickOutside(e) {
    const m = menu(), b = btn();
    if (m && !m.contains(e.target) && b && !b.contains(e.target)) closeDropdown();
  }

  function closeDropdown() {
    const m = menu();
    if (m) m.classList.add("hidden");
    document.removeEventListener("click", clickOutside);
  }

  window.toggleUserDropdown = function () {
    const m = menu();
    if (!m) return;
    if (m.classList.contains("hidden")) {
      window.dispatchEvent(new CustomEvent("lotus:dropdown-open", { detail: "profile" }));
      positionDropdown();
      m.classList.remove("hidden");
      applyDropdownProfile(profileInfo);
      setTimeout(() => document.addEventListener("click", clickOutside), 0);
    } else {
      closeDropdown();
    }
  };
  window.closeUserDropdown = closeDropdown;
  window.addEventListener("lotus:dropdown-open", function (e) {
    if (e.detail !== "profile") closeDropdown();
  });
  window.addEventListener("resize", function () {
    const m = menu();
    if (m && !m.classList.contains("hidden")) positionDropdown();
  });

  // ── Click router and session sync ─────────────────────────────────

  window.handleLoginClick = function () {
    if (window.__lotusLoggedIn) {
      window.toggleUserDropdown();
    } else if (typeof window.showAuthModal === "function") {
      window.showAuthModal();
    }
  };

  window.updateNavigation = async function () {
    try {
      const r = await fetch("/api/v1/session");
      if (!r.ok) {
        window.__lotusLoggedIn = false;
        renderLoggedOut();
        closeDropdown();
        return;
      }
      window.__lotusLoggedIn = true;
      renderLoggedIn(profileInfo && profileInfo.content, profileInfo && profileInfo.npub);
      if (!profileInfo || !profileInfo.content) hydrateProfile();
    } catch (_) {
      window.__lotusLoggedIn = false;
      renderLoggedOut();
      closeDropdown();
    }
  };

  window.forceNavigationUpdate = function () {
    fetch("/api/v1/session?_=" + Date.now())
      .then((r) => {
        if (r.ok) {
          window.__lotusLoggedIn = true;
          renderLoggedIn(null, null);
          return hydrateProfile();
        }
        window.__lotusLoggedIn = false;
        renderLoggedOut();
        closeDropdown();
      })
      .catch(() => {
        window.__lotusLoggedIn = false;
        renderLoggedOut();
        closeDropdown();
      });
  };

  window.logoutUser = function () {
    fetch("/api/v1/auth/logout", { method: "POST" })
      .catch(() => {})
      .finally(() => {
        window.dispatchEvent(new CustomEvent("lotus:logout"));
        window.location.assign("/");
      });
  };

  // ── Bootstrap ─────────────────────────────────────────────────────

  function bootstrap() {
    window.updateNavigation();
    if (window.LotusTheme) {
      window.LotusTheme.initSwapper(
        document.getElementById("theme-button"),
        document.getElementById("theme-panel")
      );
    }
    document.body.addEventListener("htmx:afterSettle", function () {
      setTimeout(window.updateNavigation, 50);
    });
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", bootstrap);
  } else {
    bootstrap();
  }
})();
