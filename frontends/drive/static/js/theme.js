// Theme swapper. Loaded synchronously in <head> so the saved theme lands on
// <html data-theme> before first paint. The dropdown is wired later by
// navigation.js through window.LotusTheme.initSwapper(button, panel).
//
// Adding a theme: append to THEMES and add a :root[data-theme="<id>"] block
// in static/css/tokens.css.

(function () {
  "use strict";

  const STORAGE_KEY = "lotus-theme";
  const DEFAULT = "lotus";

  const THEMES = [
    { id: "lotus", label: "Lotus", swatch: "#0a0609" },
    { id: "dark", label: "Dark", swatch: "#0a0a0b" },
    { id: "light", label: "Light", swatch: "#f7f3f6" },
    { id: "grain", label: "Grain", swatch: "#0d0f0c" },
    { id: "midnight", label: "Midnight", swatch: "#0c0a1e" },
  ];

  function getTheme() {
    try {
      const v = localStorage.getItem(STORAGE_KEY);
      if (v && THEMES.some((t) => t.id === v)) return v;
    } catch (_) {
      // Private mode or blocked storage: fall through to the default.
    }
    return DEFAULT;
  }

  function setTheme(id) {
    if (!THEMES.some((t) => t.id === id)) return;
    document.documentElement.setAttribute("data-theme", id);
    try {
      localStorage.setItem(STORAGE_KEY, id);
    } catch (_) {}
    document.dispatchEvent(new CustomEvent("lotus:theme-changed", { detail: { theme: id } }));
  }

  setTheme(getTheme());

  function initSwapper(buttonEl, panelEl) {
    if (!buttonEl || !panelEl || buttonEl.dataset.bound) return;
    buttonEl.dataset.bound = "1";

    panelEl.innerHTML = "";
    const current = getTheme();
    for (const t of THEMES) {
      const row = document.createElement("button");
      row.type = "button";
      row.dataset.theme = t.id;
      row.className =
        "flex items-center w-full gap-2 px-3 py-2 text-sm text-left rounded hover:bg-surface-hover" +
        (t.id === current ? " bg-surface-hover" : "");
      row.innerHTML =
        '<span class="inline-block w-3 h-3 border rounded-sm border-border" style="background:' +
        t.swatch +
        '"></span><span>' +
        t.label +
        "</span>";
      row.addEventListener("click", function () {
        setTheme(t.id);
        Array.from(panelEl.children).forEach((c) => c.classList.remove("bg-surface-hover"));
        row.classList.add("bg-surface-hover");
        closePanel();
      });
      panelEl.appendChild(row);
    }

    function openPanel() {
      window.dispatchEvent(new CustomEvent("lotus:dropdown-open", { detail: "theme" }));
      panelEl.classList.remove("hidden");
      setTimeout(() => document.addEventListener("click", clickOutside), 0);
    }
    function closePanel() {
      panelEl.classList.add("hidden");
      document.removeEventListener("click", clickOutside);
    }
    window.addEventListener("lotus:dropdown-open", function (e) {
      if (e.detail !== "theme") closePanel();
    });
    function clickOutside(e) {
      if (!panelEl.contains(e.target) && !buttonEl.contains(e.target)) closePanel();
    }
    buttonEl.addEventListener("click", function (e) {
      e.stopPropagation();
      if (panelEl.classList.contains("hidden")) openPanel();
      else closePanel();
    });
  }

  window.LotusTheme = { THEMES, get: getTheme, set: setTheme, initSwapper };
})();
