// First-run claim. Opens mill, waits for the session the bridge mints, then
// POSTs the pubkey to /setup. If the visitor is already signed in, the claim
// goes straight through.

(function () {
  "use strict";

  const $ = (id) => document.getElementById(id);

  function hideAll() {
    ["setup-success", "setup-conflict", "setup-error"].forEach((id) => $(id) && $(id).classList.add("hidden"));
  }
  function showError(msg) {
    hideAll();
    $("setup-error").textContent = msg;
    $("setup-error").classList.remove("hidden");
  }

  async function sessionPubkey() {
    try {
      const r = await fetch("/api/v1/session", { cache: "no-store" });
      if (!r.ok) return null;
      const s = await r.json();
      return s.mode === "write" ? s.publicKey || null : null;
    } catch (_) {
      return null;
    }
  }

  async function postClaim() {
    const pubkey = await sessionPubkey();
    if (!pubkey) {
      showError("No signer connected. Sign in with a method that can sign, not read-only.");
      return;
    }
    const resp = await fetch("/setup", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ pubkey }),
    });
    if (resp.status === 200) {
      hideAll();
      $("setup-success").classList.remove("hidden");
      setTimeout(() => window.location.assign("/admin"), 700);
      return;
    }
    if (resp.status === 409) {
      const body = await resp.json().catch(() => ({}));
      hideAll();
      $("setup-conflict-npub").textContent = body.owner_npub || body.owner_hex || "(unknown)";
      $("setup-conflict").classList.remove("hidden");
      return;
    }
    const body = await resp.json().catch(() => ({}));
    showError("Claim failed: " + (body.message || resp.statusText));
  }

  window.LotusSetup = {
    init: function () {
      const btn = $("setup-claim-btn");
      if (!btn || btn.dataset.bound) return;
      btn.dataset.bound = "1";
      btn.addEventListener("click", async () => {
        hideAll();
        btn.disabled = true;
        try {
          if (await sessionPubkey()) {
            await postClaim();
            return;
          }
          // The bridge calls lotusAfterLogin instead of redirecting to the
          // drive once the login round-trip succeeds.
          window.lotusAfterLogin = postClaim;
          if (typeof window.showAuthModal !== "function") {
            showError("Sign-in modal unavailable. Reload the page.");
            return;
          }
          window.showAuthModal();
        } catch (e) {
          showError(e.message || String(e));
        } finally {
          btn.disabled = false;
        }
      });
    },
  };
})();
