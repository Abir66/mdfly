// mdfly Viewer chrome — vanilla, no framework.
// S22 ships the sidebar collapse (persisted) and the mobile drawer toggle.
// Raw toggle and per-page enrichment arrive in later slices; the inline <head>
// snippet applies the persisted rail state before first paint to avoid flash.
(function () {
  "use strict";

  var RAIL_KEY = "mdfly:sidebar-rail";
  var root = document.documentElement; // state classes live on <html>
  var viewer = document.querySelector(".viewer");
  if (!viewer) return;

  function on(sel, fn) {
    var el = document.querySelector(sel);
    if (el) el.addEventListener("click", fn);
  }

  // Desktop: collapse the sidebar to a rail, persisting the choice. The inline
  // <head> snippet applies the persisted state before paint; this toggles it.
  on('[data-action="toggle-rail"]', function () {
    var railed = root.classList.toggle("rail");
    try {
      localStorage.setItem(RAIL_KEY, railed ? "1" : "0");
    } catch (e) {}
  });

  // Mobile: open/close the file-tree drawer.
  on('[data-action="toggle-drawer"]', function () {
    root.classList.toggle("drawer-open");
  });

  // Tap the scrim to dismiss the drawer.
  viewer.addEventListener("click", function (e) {
    if (e.target === viewer && root.classList.contains("drawer-open")) {
      root.classList.remove("drawer-open");
    }
  });
})();
