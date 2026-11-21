// PROTOTYPE: visual skins for the whole dashboard, same features in each.
// Loaded (not deferred) from layout.html so the skin is applied before the
// page paints. ?skin=<key> or the floating bar picks one; it's remembered
// in localStorage across pages. Delete with prototype-skins.css once a
// look is picked (see internal/web/PROTOTYPE_NOTES.md).
(function () {
  "use strict";
  var SKINS = [
    ["", "Current"],
    ["forge", "Forge"],
    ["broadsheet", "Broadsheet"],
    ["aurora", "Aurora"],
    ["blocks", "Blocks"],
  ];
  var KEY = "draftsmith-proto-skin";
  var root = document.documentElement;
  var cur = "";
  try {
    var q = new URLSearchParams(location.search).get("skin");
    if (q !== null) localStorage.setItem(KEY, q);
    cur = localStorage.getItem(KEY) || "";
  } catch (e) {
    cur = new URLSearchParams(location.search).get("skin") || "";
  }
  function apply(k) {
    cur = k;
    if (k) root.setAttribute("data-skin", k);
    else root.removeAttribute("data-skin");
    try { localStorage.setItem(KEY, k); } catch (e) {}
  }
  apply(cur);

  document.addEventListener("DOMContentLoaded", function () {
    var bar = document.createElement("nav");
    bar.className = "skin-switch";
    bar.setAttribute("aria-label", "Prototype skins");
    bar.innerHTML = '<button type="button" data-step="-1" aria-label="Previous skin">‹</button><span></span><button type="button" data-step="1" aria-label="Next skin">›</button>';
    var label = bar.querySelector("span");
    function at() {
      for (var i = 0; i < SKINS.length; i++) if (SKINS[i][0] === cur) return i;
      return 0;
    }
    function show() {
      var i = at();
      label.textContent = "Skin " + i + " · " + SKINS[i][1] + "  (" + (i + 1) + "/" + SKINS.length + ")";
    }
    function step(d) {
      apply(SKINS[(at() + d + SKINS.length) % SKINS.length][0]);
      show();
    }
    bar.addEventListener("click", function (e) {
      var b = e.target.closest("[data-step]");
      if (b) step(+b.dataset.step);
    });
    document.addEventListener("keydown", function (e) {
      var t = document.activeElement;
      if (t && (t.tagName === "INPUT" || t.tagName === "SELECT" || t.tagName === "TEXTAREA" || t.isContentEditable)) return;
      if (e.altKey || e.ctrlKey || e.metaKey) return;
      if (e.key === "ArrowLeft") step(-1);
      if (e.key === "ArrowRight") step(1);
    });
    show();
    document.body.appendChild(bar);
  });
})();
