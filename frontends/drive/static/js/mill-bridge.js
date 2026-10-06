// Lotus → mill bridge.
//
// Mill (window.MILL) owns the sign-in modal and produces a signer for every
// supported method. This bridge:
//
//   1. Exposes window.showAuthModal() / hideAuthModal() for the header button.
//   2. On mill:connected, mints the server session via /api/v1/auth/login.
//      A write-mode login carries a NIP-98 authorization signed by the same
//      key, so the server only trusts a pubkey that proved it holds the key.
//   3. Keeps the signer on window.lotusSigner for uploads, deletes and
//      publishes, and restores it across reloads via MILL.restore().

(function () {
  "use strict";

  // mill method id → grain's session SigningMethod enum.
  const METHOD_MAP = {
    nip07: "browser_extension",
    nip46: "bunker",
    nip55: "amber",
    privatekey: "encrypted_key",
    newkey: "encrypted_key",
    readonly: "none",
    google: "google",
    pomegranate: "pomegranate",
  };

  // ── Branding from /api/v1/server/info ────────────────────────────

  let brandCache = null;
  async function getBrand() {
    if (brandCache !== null) return brandCache;
    try {
      const r = await fetch("/api/v1/server/info");
      const info = r.ok ? await r.json() : null;
      brandCache = {
        name: (info && info.name) || "",
        icon: (info && info.icon) || "",
        terms: (info && info.terms_url) || "",
        privacy: (info && info.privacy_url) || "",
      };
    } catch (_) {
      brandCache = { name: "", icon: "", terms: "", privacy: "" };
    }
    return brandCache;
  }
  getBrand();

  // ── NIP-98 ────────────────────────────────────────────────────────

  async function sha256Hex(input) {
    const bytes = typeof input === "string" ? new TextEncoder().encode(input) : input;
    const digest = await crypto.subtle.digest("SHA-256", bytes);
    return Array.from(new Uint8Array(digest))
      .map((b) => b.toString(16).padStart(2, "0"))
      .join("");
  }

  // base64 that survives non-Latin-1 characters in event content.
  function b64(str) {
    return btoa(unescape(encodeURIComponent(str)));
  }

  // nip98Header signs a kind-27235 event for one request. body must be the
  // exact string that goes on the wire: the server hashes the bytes it
  // receives and compares them with the payload tag.
  async function nip98Header(method, url, body) {
    const signer = window.lotusSigner;
    if (!signer || typeof signer.signEvent !== "function") {
      throw new Error("no signer connected");
    }
    const tags = [
      ["u", url],
      ["method", method.toUpperCase()],
    ];
    if (body) tags.push(["payload", await sha256Hex(body)]);
    const signed = await signer.signEvent({
      kind: 27235,
      created_at: Math.floor(Date.now() / 1000),
      content: "",
      tags,
    });
    return "Nostr " + b64(JSON.stringify(signed));
  }

  window.lotusNip98 = { header: nip98Header, sha256Hex, b64 };

  // ── Sign-in modal ─────────────────────────────────────────────────

  // mill applies a theme as inline --mill-* properties on its host element,
  // which beats any stylesheet. Handing it var() references to lotus's own
  // tokens makes the modal follow the active theme, live, including swaps.
  function millTheme() {
    const map = {
      "--mill-bg": "--color-surface-base",
      "--mill-surface": "--color-surface",
      "--mill-card": "--color-surface-elevated",
      "--mill-card-hover": "--color-surface-hover",
      "--mill-inset": "--color-surface-inset",
      "--mill-inset-strong": "--color-surface-inset-strong",
      "--mill-overlay": "--color-backdrop",
      "--mill-border": "--color-border",
      "--mill-border-light": "--color-border-strong",
      "--mill-accent": "--color-accent",
      "--mill-accent-hover": "--color-accent-hover",
      "--mill-accent-dim": "--color-accent-dim",
      "--mill-teal": "--color-accent-2",
      "--mill-teal-dim": "--color-accent-2-dim",
      "--mill-text": "--color-text",
      "--mill-text-secondary": "--color-text-secondary",
      "--mill-muted": "--color-text-muted",
      "--mill-danger": "--color-danger",
      "--mill-danger-dim": "--color-danger-dim",
      "--mill-warning": "--color-warning",
      "--mill-warning-dim": "--color-warning-dim",
      "--mill-success": "--color-success",
      "--mill-success-dim": "--color-success-dim",
      "--mill-radius": "--radius",
      "--mill-shadow": "--shadow-lg",
      "--mill-font": "--font-sans",
      "--mill-font-mono": "--font-mono",
    };
    const theme = {};
    for (const k in map) theme[k] = "var(" + map[k] + ")";
    return theme;
  }

  async function showAuthModal() {
    if (!window.MILL) {
      console.error("[mill-bridge] MILL global not loaded");
      return;
    }
    const brand = await getBrand();
    const appName = brand.name || "Lotus";

    const footerLinks = [];
    if (brand.terms) footerLinks.push({ label: "Terms", href: brand.terms });
    if (brand.privacy) footerLinks.push({ label: "Privacy", href: brand.privacy });

    window.MILL.open({
      theme: millTheme(),
      layout: "grid",
      appName,
      amberCallback: window.location.origin + "/api/v1/auth/amber-callback",
      onConnected: handleConnected,
      header: {
        logo: brand.icon || false,
        logoHeight: brand.icon ? 40 : undefined,
        eyebrow: false,
        title: appName,
        message: "Sign in with your Nostr identity to open your drive.",
        align: "center",
      },
      tip: false,
      footer: footerLinks.length ? { links: footerLinks } : undefined,
      // Desktop leads with an extension and a remote signer; a pasted key,
      // a brand-new key and read-only sit under "More options".
      methods: ["nip07", "nip46"],
      moreMethods: ["privatekey", "newkey", "readonly"],
      platforms: {
        android: { methods: ["nip55", "nip46"], moreMethods: ["privatekey", "newkey", "readonly"] },
        ios: { methods: ["nip46", "privatekey"], moreMethods: ["newkey", "readonly"] },
      },
    });
  }

  function hideAuthModal() {
    window.MILL?.close();
  }

  async function handleConnected(result) {
    // result: { method, pubkey, signer, perms?, bunkerUrl?, nsec? }
    window.lotusSigner = result.signer || null;
    window.lotusSignerMethod = result.method;
    if (window.lotusSigner) {
      window.dispatchEvent(new CustomEvent("lotus:signer-ready"));
    }

    const signingMethod = METHOD_MAP[result.method] ?? "none";
    const canSign = !!(window.lotusSigner && typeof window.lotusSigner.signEvent === "function");
    const requestedMode = result.method === "readonly" || !canSign ? "read_only" : "write";

    window.MILL?.close();
    if (typeof window.renderLoginLoading === "function") window.renderLoginLoading();

    const body = JSON.stringify({
      public_key: result.pubkey,
      requested_mode: requestedMode,
      signing_method: signingMethod,
    });
    const headers = { "Content-Type": "application/json" };

    try {
      if (requestedMode === "write") {
        headers.Authorization = await nip98Header("POST", window.location.origin + "/api/v1/auth/login", body);
      }
      const resp = await fetch("/api/v1/auth/login", { method: "POST", headers, body });
      if (!resp.ok) {
        let msg = resp.statusText;
        try {
          const j = await resp.json();
          if (j && j.message) msg = j.message;
        } catch (_) {}
        console.error("[mill-bridge] login failed:", resp.status, msg);
        if (window.lotusToast) window.lotusToast("Sign-in failed: " + msg, "danger");
        if (typeof window.forceNavigationUpdate === "function") window.forceNavigationUpdate();
        return;
      }
      // A page can take over what happens after sign-in (the setup claim
      // does). Otherwise reload into the drive: the header and routes depend
      // on the session.
      if (typeof window.lotusAfterLogin === "function") {
        const next = window.lotusAfterLogin;
        window.lotusAfterLogin = null;
        if (typeof window.forceNavigationUpdate === "function") window.forceNavigationUpdate();
        await next();
        return;
      }
      window.location.assign("/drive");
    } catch (err) {
      console.error("[mill-bridge] login request errored:", err);
      if (window.lotusToast) window.lotusToast("Sign-in failed: " + (err.message || err), "danger");
      if (typeof window.forceNavigationUpdate === "function") window.forceNavigationUpdate();
    }
  }

  window.addEventListener("lotus:logout", () => {
    try {
      window.lotusSigner?.disconnect?.();
    } catch (_) {}
    try {
      window.MILL?.clearRestoreState?.();
    } catch (_) {}
    window.lotusSigner = null;
    window.lotusSignerMethod = null;
  });

  window.showAuthModal = showAuthModal;
  window.hideAuthModal = hideAuthModal;

  // ── Signer restore across reloads ─────────────────────────────────
  //
  // The session cookie survives a reload and records the signing method and
  // pubkey; mill persists each method's restore state, so MILL.restore()
  // rebuilds the signer without reopening the picker. If nothing is
  // persisted (fresh browser), callers fall back to the modal.

  let sessionCache = null;
  async function getCachedSession() {
    if (sessionCache !== null) return sessionCache;
    try {
      const r = await fetch("/api/v1/session", { cache: "no-store" });
      sessionCache = r.ok ? await r.json() : false;
    } catch (_) {
      sessionCache = false;
    }
    return sessionCache;
  }

  async function restoreSigner() {
    if (window.lotusSigner && typeof window.lotusSigner.signEvent === "function") return true;
    if (!window.MILL || typeof window.MILL.restore !== "function") return false;
    const sess = await getCachedSession();
    if (!sess || !sess.publicKey || !sess.signingMethod || sess.signingMethod === "none") return false;
    try {
      const signer = await window.MILL.restore({ method: sess.signingMethod, pubkey: sess.publicKey });
      if (!signer || typeof signer.signEvent !== "function") return false;
      window.lotusSigner = signer;
      window.lotusSignerMethod = signer.method || sess.signingMethod;
      window.dispatchEvent(new CustomEvent("lotus:signer-ready"));
      return true;
    } catch (_) {
      return false;
    }
  }
  window.restoreSigner = restoreSigner;

  // ensureSigner returns a usable signer or opens the modal and throws, so
  // an action that needs a signature can simply await it.
  window.ensureSigner = async function () {
    if (await restoreSigner()) return window.lotusSigner;
    showAuthModal();
    throw new Error("sign in to continue");
  };

  async function autoReconnectLoop() {
    if (await restoreSigner()) return;
    const sess = await getCachedSession();
    if (!sess || sess.signingMethod !== "browser_extension") return;
    // NIP-07 extensions inject window.nostr asynchronously.
    for (const d of [100, 200, 400, 800, 1500]) {
      await new Promise((r) => setTimeout(r, d));
      if (await restoreSigner()) return;
    }
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", autoReconnectLoop);
  } else {
    autoReconnectLoop();
  }
})();
