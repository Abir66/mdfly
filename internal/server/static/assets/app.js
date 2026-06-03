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

  // Raw toggle: swap the rendered HTML for the byte-identical source markdown,
  // fetched once directly from the CDN (zero backend load) and cached client-side.
  // Subsequent toggles are pure CSS class flips — no further network.
  on('[data-action="toggle-raw"]', function () {
    var btn = this;
    var center = btn.closest(".center");
    var pre = center.querySelector(".raw-source");
    var showing = center.classList.toggle("raw");
    btn.setAttribute("aria-pressed", showing ? "true" : "false");
    if (showing && pre.dataset.loaded !== "1") {
      pre.dataset.loaded = "1"; // guard before fetch: at most one request
      fetch(btn.getAttribute("data-raw-url"))
        .then(function (r) {
          if (!r.ok) throw new Error("HTTP " + r.status);
          return r.text();
        })
        .then(function (text) {
          pre.textContent = text;
        })
        .catch(function () {
          pre.dataset.loaded = ""; // failed — allow a retry on next toggle
        });
    }
  });

  // Folder expand/collapse: the chevron toggles its folder open/closed without
  // navigating. The rest of the folder row is a plain link that navigates. The
  // tree is recursive, so this is delegated from the nav rather than per-row.
  var tree = document.querySelector(".tree");
  if (tree) {
    tree.addEventListener("click", function (e) {
      var btn = e.target.closest('[data-action="toggle-folder"]');
      if (!btn) return;
      e.preventDefault();
      var folder = btn.closest(".tree-folder");
      var open = folder.classList.toggle("open");
      btn.setAttribute("aria-expanded", open ? "true" : "false");
    });
  }

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
