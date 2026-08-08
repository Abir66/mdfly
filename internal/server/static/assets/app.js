// mdfly Viewer chrome — vanilla, no framework.
// S22 ships the sidebar collapse (persisted) and the mobile drawer toggle.
// The inline <head> snippet applies the persisted rail state before first
// paint to avoid flash.
(function () {
  "use strict";

  var RAIL_KEY = "mdfly:sidebar-rail";
  var MOBILE = "(max-width: 768px)"; // matches the app.css drawer breakpoint
  var root = document.documentElement; // state classes live on <html>
  var viewer = document.querySelector(".viewer");
  if (!viewer) return;

  function on(sel, fn) {
    var el = document.querySelector(sel);
    if (el) el.addEventListener("click", fn);
  }

  // The sidebar-header control. On desktop it collapses the sidebar to a rail,
  // persisting the choice (the inline <head> snippet applies it before paint).
  // On mobile the rail concept doesn't apply — the sidebar is a drawer, so the
  // same control just closes it.
  on('[data-action="toggle-rail"]', function () {
    if (window.matchMedia(MOBILE).matches) {
      root.classList.remove("drawer-open");
      return;
    }
    var railed = root.classList.toggle("rail");
    try {
      localStorage.setItem(RAIL_KEY, railed ? "1" : "0");
    } catch (e) {}
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

  // The footer timestamp is served as UTC so it reads correctly without scripts;
  // rewrite it here in the reader's own zone and locale.
  var stamp = document.querySelector("time[data-local-time]");
  if (stamp) {
    var when = new Date(stamp.getAttribute("datetime"));
    if (!isNaN(when)) {
      stamp.textContent =
        when.toLocaleDateString(undefined, { month: "short", day: "numeric", year: "numeric" }) +
        " at " +
        when.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit", timeZoneName: "short" });
    }
  }
})();
