// Champion and item icons come from Riot's Data Dragon. If one fails to
// load (new champion, odd id, offline), drop the image so the initials
// placeholder behind it shows instead of a broken image.
document.addEventListener("error", function (e) {
  var el = e.target;
  if (el && el.tagName === "IMG") {
    if (el.parentElement && el.parentElement.classList.contains("icon")) {
      el.remove();
    } else {
      el.style.visibility = "hidden";
    }
  }
}, true);

// The overlay window (serve --overlay) binds draftsmithHide, which hides it
// until the next champ select. In a browser there's nothing to hide.
(function () {
  var btn = document.querySelector(".ov-hide");
  if (!btn || typeof window.draftsmithHide !== "function") return;
  btn.hidden = false;
  btn.addEventListener("click", function () { window.draftsmithHide(); });
})();

// Champ select: follow the server's event stream, swapping in each board
// it sends, and count the phase timer down between updates.
(function () {
  var root = document.querySelector("[data-stream]");
  if (!root) return;
  var renderedAt = Date.now();

  function tick() {
    var els = root.querySelectorAll("[data-left]");
    for (var i = 0; i < els.length; i++) {
      var left = Math.max(0, +els[i].getAttribute("data-left") - (Date.now() - renderedAt));
      var s = Math.ceil(left / 1000);
      els[i].textContent = s >= 60 ? Math.floor(s / 60) + ":" + ("0" + (s % 60)).slice(-2) : String(s);
      els[i].classList.toggle("low", s <= 10);
    }
  }
  tick();
  setInterval(tick, 250);

  if (!window.EventSource) return;
  var src = new EventSource(root.getAttribute("data-stream"));
  src.addEventListener("board", function (e) {
    // Keep rune pages the way I left them (open or folded) across updates.
    var open = {}, had = {};
    root.querySelectorAll("details[data-key]").forEach(function (d) {
      had[d.getAttribute("data-key")] = true;
      open[d.getAttribute("data-key")] = d.open;
    });
    root.innerHTML = e.data;
    root.querySelectorAll("details[data-key]").forEach(function (d) {
      var k = d.getAttribute("data-key");
      if (had[k]) d.open = open[k];
    });
    renderedAt = Date.now();
    tick();
    root.classList.remove("offline");
  });
  // EventSource reconnects by itself (say, after serve restarts); until
  // then, show that the board may be out of date.
  src.addEventListener("error", function () { root.classList.add("offline"); });
  src.addEventListener("open", function () { root.classList.remove("offline"); });
})();

// Rune pages: Import puts the page in the League client. It's a click
// only; nothing imports by itself. The board is replaced on every update,
// so the click is caught on the document.
document.addEventListener("click", function (e) {
  var btn = e.target.closest && e.target.closest("button[data-import]");
  if (!btn) return;
  var status = btn.parentElement.querySelector("[data-import-status]");
  btn.disabled = true;
  if (status) { status.textContent = "Importing…"; status.className = "muted small"; }
  var body = new URLSearchParams({ champ: btn.getAttribute("data-import") });
  fetch("/champ-select/runes", { method: "POST", headers: { "X-Draftsmith": "import" }, body: body })
    .then(function (r) { return r.text().then(function (t) { return { ok: r.ok, text: t.trim() }; }); })
    .then(function (res) {
      if (status) { status.textContent = res.ok ? "Imported ✓" : res.text; status.className = "small " + (res.ok ? "pos" : "neg"); }
    })
    .catch(function () { if (status) { status.textContent = "Couldn't reach the dashboard"; status.className = "small neg"; } })
    .then(function () { btn.disabled = false; });
});
