// PROTOTYPE: overview redesign variants (see internal/web/prototype_overview.go).
// Throwaway: delete with the rest of the prototype once a design is picked.
(function () {
  "use strict";

  // ── Switcher ──────────────────────────────────────────────────────────
  var VARIANTS = [["0", "Current"], ["A", "War room"], ["B", "Editorial"], ["C", "Hextech"]];
  var sw = document.querySelector(".proto-switch");
  if (sw) {
    var cur = sw.dataset.variant || "0";
    var at = 0;
    VARIANTS.forEach(function (v, i) { if (v[0] === cur) at = i; });
    sw.querySelector(".proto-label").textContent = VARIANTS[at][0] + " · " + VARIANTS[at][1] + "  (" + (at + 1) + "/" + VARIANTS.length + ")";
    var go = function (step) {
      var v = VARIANTS[(at + step + VARIANTS.length) % VARIANTS.length][0];
      var q = new URLSearchParams(location.search);
      q.set("variant", v);
      location.search = q.toString();
    };
    // Keep the variant when switching queue.
    document.querySelectorAll("main .seg a[href*='queue=']").forEach(function (a) {
      a.href += "&variant=" + cur;
    });
    sw.addEventListener("click", function (e) {
      var b = e.target.closest("[data-step]");
      if (b) go(+b.dataset.step);
    });
    document.addEventListener("keydown", function (e) {
      var t = document.activeElement;
      if (t && (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.isContentEditable)) return;
      if (e.altKey || e.ctrlKey || e.metaKey) return;
      if (e.key === "ArrowLeft") go(-1);
      if (e.key === "ArrowRight") go(1);
    });
  }

  var dataEl = document.getElementById("pv-data");
  if (!dataEl) return;
  var D = JSON.parse(dataEl.dataset.json);
  D.deaths = D.deaths || [];
  D.champs = D.champs || [];
  D.trans = D.trans || [];
  D.games = D.games || [];
  var SVGNS = "http://www.w3.org/2000/svg";

  // ── Shared helpers ────────────────────────────────────────────────────
  // Summoner's Rift bounds as Data Dragon's map11.png draws them.
  var MINX = -120, MINY = -120, MAXX = 14870, MAXY = 14980;
  function proj(x, y, w, h) {
    return [(x - MINX) / (MAXX - MINX) * w, (1 - (y - MINY) / (MAXY - MINY)) * h];
  }
  // My-side perspective: red side games are turned 180° so my base is
  // always bottom left.
  function mine(d) {
    if (d.b) return [d.x, d.y];
    return [MAXX + MINX - d.x, MAXY + MINY - d.y];
  }
  // Named zone for a my-side position.
  function zone(x, y) {
    var s = x + y;
    if (x < 3600 && y < 3600) return "Own base";
    if (x > 11200 && y > 11200) return "Enemy base";
    if ((x < 2300 && y > 3600) || (y > 12600 && x < 11200)) return y > 12600 && x > 4500 ? "Top lane, their side" : "Top lane";
    if ((y < 2300 && x > 3600) || (x > 12600 && y < 11200)) return x > 12600 && y > 4500 ? "Bot lane, their side" : "Bot lane";
    if (Math.abs(x - y) < 1300) return s > 14800 ? "Mid lane, their side" : "Mid lane";
    if (Math.abs(s - 14800) < 1500) return x < y ? "Top river" : "Bot river";
    if (s < 14800) return x < y ? "Own jungle, top side" : "Own jungle, bot side";
    return x < y ? "Enemy jungle, top side" : "Enemy jungle, bot side";
  }
  function phaseOf(m) { return m < 14 ? "early" : m < 25 ? "mid" : "late"; }
  function pct(x) { return Math.round(x * 100) + "%"; }
  function el(tag, attrs, parent) {
    var e = document.createElementNS(SVGNS, tag);
    for (var k in attrs) e.setAttribute(k, attrs[k]);
    if (parent) parent.appendChild(e);
    return e;
  }
  function mmss(m) { var s = Math.round(m * 60); return Math.floor(s / 60) + ":" + String(s % 60).padStart(2, "0"); }
  // Deterministic pseudo-random, so layouts don't jump between reloads.
  function rng(seed) {
    var s = seed >>> 0 || 1;
    return function () { s = (s * 1664525 + 1013904223) >>> 0; return s / 4294967296; };
  }
  var ROLES = ["top", "jungle", "mid", "bot", "support"];
  var ROLE_NAMES = { top: "Top", jungle: "Jungle", mid: "Mid", bot: "Bot", support: "Support" };
  function roleOf(c) { return ROLES.indexOf(c.role) >= 0 ? c.role : "mid"; }

  var tip = document.createElement("div");
  tip.className = "pv-tip";
  tip.hidden = true;
  document.body.appendChild(tip);
  function showTip(e, html) {
    tip.innerHTML = html;
    tip.hidden = false;
    var x = e.clientX + 14, y = e.clientY + 14;
    var r = tip.getBoundingClientRect();
    if (x + r.width > innerWidth - 8) x = e.clientX - r.width - 14;
    if (y + r.height > innerHeight - 8) y = e.clientY - r.height - 14;
    tip.style.left = x + "px";
    tip.style.top = y + "px";
  }
  function hideTip() { tip.hidden = true; }
  function esc(s) { return String(s).replace(/[&<>"]/g, function (c) { return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]; }); }
  function champTip(c) {
    return "<b>" + esc(c.name) + "</b><br>" + c.games + " games · " + c.wins + "W " + (c.games - c.wins) + "L<br>Adjusted " + pct(c.adj) +
      " · raw " + pct(c.wr) + "<br>KDA " + c.kda.toFixed(2) + " · " + c.csm.toFixed(1) + " CS/m<br><span class=dim>" + ROLE_NAMES[roleOf(c)] + " · last " + esc(c.last) + "</span>";
  }

  document.querySelectorAll("img[data-map]").forEach(function (img) { img.src = D.map; });

  // Sizes a canvas to its box at device resolution; returns [ctx, w, h].
  function fit(canvas) {
    var r = canvas.getBoundingClientRect();
    var dpr = window.devicePixelRatio || 1;
    canvas.width = Math.round(r.width * dpr);
    canvas.height = Math.round(r.height * dpr);
    var ctx = canvas.getContext("2d");
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    return [ctx, r.width, r.height];
  }

  // ── A · Density heatmap ───────────────────────────────────────────────
  (function () {
    var canvas = document.querySelector('[data-viz="heat-density"]');
    if (!canvas) return;
    var state = { phase: "all", result: "all", persp: "mine" };
    var readout = document.querySelector("[data-readout]");
    var N = 120, SIG = 3.2;
    var stops = [[0, [0, 220, 255, 0]], [0.18, [0, 200, 255, 0.35]], [0.45, [60, 255, 170, 0.6]], [0.7, [255, 220, 60, 0.8]], [0.88, [255, 90, 60, 0.9]], [1, [255, 255, 255, 0.95]]];
    function ramp(t) {
      for (var i = 1; i < stops.length; i++) {
        if (t <= stops[i][0]) {
          var a = stops[i - 1], b = stops[i], f = (t - a[0]) / (b[0] - a[0]);
          return a[1].map(function (v, k) { return v + (b[1][k] - v) * f; });
        }
      }
      return stops[stops.length - 1][1];
    }
    function pick() {
      return D.deaths.filter(function (d) {
        if (state.phase !== "all" && phaseOf(d.m) !== state.phase) return false;
        if (state.result === "w" && !d.w) return false;
        if (state.result === "l" && d.w) return false;
        return true;
      });
    }
    function draw() {
      var f = fit(canvas), ctx = f[0], w = f[1], h = f[2];
      var ds = pick();
      var grid = new Float32Array(N * N), max = 0;
      var R = Math.ceil(SIG * 3);
      ds.forEach(function (d) {
        var p = state.persp === "mine" ? mine(d) : [d.x, d.y];
        var q = proj(p[0], p[1], N, N);
        for (var j = -R; j <= R; j++) for (var i = -R; i <= R; i++) {
          var gx = Math.round(q[0]) + i, gy = Math.round(q[1]) + j;
          if (gx < 0 || gy < 0 || gx >= N || gy >= N) continue;
          var dx = gx - q[0], dy = gy - q[1];
          var v = grid[gy * N + gx] += Math.exp(-(dx * dx + dy * dy) / (2 * SIG * SIG));
          if (v > max) max = v;
        }
      });
      var off = document.createElement("canvas");
      off.width = off.height = N;
      var octx = off.getContext("2d"), img = octx.createImageData(N, N);
      for (var k = 0; k < N * N; k++) {
        var c = ramp(max ? Math.pow(grid[k] / max, 0.75) : 0);
        img.data[k * 4] = c[0]; img.data[k * 4 + 1] = c[1]; img.data[k * 4 + 2] = c[2]; img.data[k * 4 + 3] = c[3] * 255;
      }
      octx.putImageData(img, 0, 0);
      ctx.clearRect(0, 0, w, h);
      ctx.imageSmoothingEnabled = true;
      ctx.imageSmoothingQuality = "high";
      ctx.drawImage(off, 0, 0, w, h);
      // Each death as a faint crosshair.
      ctx.strokeStyle = "rgba(255,255,255,.55)";
      ctx.lineWidth = 1;
      ds.forEach(function (d) {
        var p = state.persp === "mine" ? mine(d) : [d.x, d.y];
        var q = proj(p[0], p[1], w, h);
        ctx.beginPath();
        ctx.moveTo(q[0] - 3, q[1]); ctx.lineTo(q[0] + 3, q[1]);
        ctx.moveTo(q[0], q[1] - 3); ctx.lineTo(q[0], q[1] + 3);
        ctx.stroke();
      });
      // Readout.
      var zones = {}, enemy = 0, mins = [];
      ds.forEach(function (d) {
        var p = mine(d);
        zones[zone(p[0], p[1])] = (zones[zone(p[0], p[1])] || 0) + 1;
        if (p[0] + p[1] > 14800) enemy++;
        mins.push(d.m);
      });
      mins.sort(function (a, b) { return a - b; });
      var top = Object.keys(zones).sort(function (a, b) { return zones[b] - zones[a]; }).slice(0, 3);
      readout.innerHTML = ds.length ? [
        ["Deaths", ds.length],
        ["Enemy half", pct(enemy / ds.length)],
        ["Median", mmss(mins[Math.floor(mins.length / 2)])],
        ["Hottest", top.map(function (z) { return esc(z) + " <i>" + zones[z] + "</i>"; }).join(" · ")],
      ].map(function (r) { return "<div><span>" + r[0] + "</span><b>" + r[1] + "</b></div>"; }).join("") : "<div><span>No deaths match</span></div>";
    }
    document.querySelectorAll(".a-seg").forEach(function (seg) {
      seg.addEventListener("click", function (e) {
        var b = e.target.closest("button");
        if (!b) return;
        seg.querySelectorAll("button").forEach(function (x) { x.setAttribute("aria-pressed", x === b); });
        state[seg.dataset.ctl] = b.dataset.v;
        draw();
      });
    });
    draw();
    addEventListener("resize", draw);
  })();

  // ── A · Polar pool sky ────────────────────────────────────────────────
  (function () {
    var svg = document.querySelector('[data-viz="sky-polar"]');
    if (!svg) return;
    var C = 300, R0 = 46, R1 = 262;
    var defs = el("defs", {}, svg);
    var glow = el("filter", { id: "a-glow", x: "-100%", y: "-100%", width: "300%", height: "300%" }, defs);
    el("feGaussianBlur", { stdDeviation: "4", result: "b" }, glow);
    var mg = el("feMerge", {}, glow);
    el("feMergeNode", { in: "b" }, mg);
    el("feMergeNode", { in: "SourceGraphic" }, mg);
    var rnd = rng(7);
    for (var i = 0; i < 160; i++) {
      el("circle", { cx: rnd() * 600, cy: rnd() * 600, r: rnd() * 1.1 + 0.2, class: "a-bgstar", style: "animation-delay:" + (rnd() * 4).toFixed(2) + "s" }, svg);
    }
    var adjs = D.champs.map(function (c) { return c.adj; });
    var lo = Math.min.apply(null, adjs.concat([D.base - 0.03])) - 0.005, hi = Math.max.apply(null, adjs.concat([D.base + 0.03])) + 0.005;
    function rad(adj) { var t = Math.min(1, Math.max(0, (adj - lo) / (hi - lo))); return R1 - t * (R1 - R0); }
    var rings = [];
    for (var v0 = Math.ceil(lo * 20) / 20; v0 <= hi; v0 += 0.05) rings.push(Math.round(v0 * 1000) / 1000);
    var labelled = {};
    D.champs.slice().sort(function (a, b) { return b.games - a.games || b.adj - a.adj; }).slice(0, 8).forEach(function (c) { labelled[c.key] = true; });
    rings.forEach(function (v) {
      el("circle", { cx: C, cy: C, r: rad(v), class: "a-ring" + (Math.abs(v - 0.5) < 1e-6 ? " mid" : "") }, svg);
      var t = el("text", { x: C + 4, y: C - rad(v) - 4, class: "a-ringlbl" }, svg);
      t.textContent = (v * 100).toFixed(1).replace(".0", "") + "%";
    });
    var base = el("circle", { cx: C, cy: C, r: rad(D.base), class: "a-ring base" }, svg);
    base.appendChild(el("title", {})).textContent = "Your overall win rate";
    ROLES.forEach(function (role, ri) {
      var a = (ri / 5) * Math.PI * 2 - Math.PI / 2 - Math.PI / 5;
      el("line", { x1: C, y1: C, x2: C + Math.cos(a) * 290, y2: C + Math.sin(a) * 290, class: "a-spoke" }, svg);
      var am = a + Math.PI / 5;
      var t = el("text", { x: C + Math.cos(am) * 284, y: C + Math.sin(am) * 284 + 4, class: "a-rolelbl", "text-anchor": "middle" }, svg);
      t.textContent = ROLE_NAMES[role].toUpperCase();
    });
    var byRole = {};
    D.champs.forEach(function (c) { (byRole[roleOf(c)] = byRole[roleOf(c)] || []).push(c); });
    var lines = el("g", {}, svg), stars = el("g", {}, svg);
    ROLES.forEach(function (role, ri) {
      var list = (byRole[role] || []).slice().sort(function (a, b) { return b.games - a.games; });
      var a0 = (ri / 5) * Math.PI * 2 - Math.PI / 2 - Math.PI / 5;
      var pts = list.map(function (c, k) {
        var frac = (k + 0.5) / list.length;
        var jit = (rng(c.key.length * 97 + k)() - 0.5) * 0.4 / Math.max(1, list.length);
        var a = a0 + (Math.PI * 2 / 5) * (0.1 + 0.8 * frac + jit);
        var r = rad(c.adj);
        return { c: c, x: C + Math.cos(a) * r, y: C + Math.sin(a) * r };
      });
      var fig = pts.filter(function (p) { return p.c.games >= 2; }).sort(function (a, b) { return b.c.adj - a.c.adj; });
      if (fig.length > 1) el("polyline", { points: fig.map(function (p) { return p.x.toFixed(1) + "," + p.y.toFixed(1); }).join(" "), class: "a-figure" }, lines);
      pts.forEach(function (p) {
        var c = p.c, r = 2.2 + Math.sqrt(c.games) * 2.1;
        var cls = "a-star" + (c.games < 3 ? " faint" : "") + (c.adj >= D.base + 0.03 ? " hot" : c.adj <= D.base - 0.03 ? " cold" : "");
        var g = el("g", { class: cls, tabindex: 0 }, stars);
        el("circle", { cx: p.x, cy: p.y, r: r, filter: c.games >= 3 ? "url(#a-glow)" : "" }, g);
        if (labelled[c.key]) {
          var t = el("text", { x: p.x + r + 4, y: p.y + 3.5, class: "a-starlbl" }, g);
          t.textContent = c.name;
        }
        g.addEventListener("mousemove", function (e) { showTip(e, champTip(c)); });
        g.addEventListener("mouseleave", hideTip);
        g.addEventListener("click", function () { location.href = "/champions/" + encodeURIComponent(c.key); });
      });
    });
    el("circle", { cx: C, cy: C, r: 5, class: "a-core" }, svg);
  })();

  // ── B · Hex-binned small multiples ────────────────────────────────────
  (function () {
    var box = document.querySelector('[data-viz="heat-multiples"]');
    if (!box) return;
    var S = 300, HR = 9.5;
    var HW = Math.sqrt(3) * HR, VH = 1.5 * HR;
    var phases = [["early", "0–14 min", "Laning"], ["mid", "14–25 min", "Mid game"], ["late", "25 min +", "Late game"]];
    var binsAll = phases.map(function (ph) {
      var bins = {};
      D.deaths.forEach(function (d) {
        if (phaseOf(d.m) !== ph[0]) return;
        var p = mine(d), q = proj(p[0], p[1], S, S);
        var row = Math.round(q[1] / VH), col = Math.round((q[0] - (row % 2 ? HW / 2 : 0)) / HW);
        var k = row + ":" + col;
        (bins[k] = bins[k] || { row: row, col: col, n: 0, zones: {} }).n++;
        var z = zone(p[0], p[1]);
        bins[k].zones[z] = (bins[k].zones[z] || 0) + 1;
      });
      return bins;
    });
    var max = 1;
    binsAll.forEach(function (b) { for (var k in b) max = Math.max(max, b[k].n); });
    phases.forEach(function (ph, pi) {
      var fig = document.createElement("figure");
      fig.className = "b-mult";
      var svg = el("svg", { viewBox: "0 0 " + S + " " + S, role: "img", "aria-label": ph[2] + " deaths" }, fig);
      el("rect", { x: 1, y: 1, width: S - 2, height: S - 2, class: "b-rift" }, svg);
      el("line", { x1: 0, y1: 0, x2: S, y2: S, class: "b-river" }, svg);
      el("polyline", { points: "16," + (S - 30) + " 16,16 " + (S - 30) + ",16", class: "b-lane" }, svg);
      el("polyline", { points: "30," + (S - 16) + " " + (S - 16) + "," + (S - 16) + " " + (S - 16) + ",30", class: "b-lane" }, svg);
      el("line", { x1: 40, y1: S - 40, x2: S - 40, y2: 40, class: "b-lane" }, svg);
      el("path", { d: "M0 " + (S - 62) + " A62 62 0 0 1 62 " + S + " L0 " + S + "Z", class: "b-base own" }, svg);
      el("path", { d: "M" + S + " 62 A62 62 0 0 1 " + (S - 62) + " 0 L" + S + " 0Z", class: "b-base" }, svg);
      var bins = binsAll[pi], total = 0, zt = {};
      for (var k in bins) {
        var b = bins[k];
        total += b.n;
        for (var z in b.zones) zt[z] = (zt[z] || 0) + b.zones[z];
        var cx = b.col * HW + (b.row % 2 ? HW / 2 : 0), cy = b.row * VH;
        var pts = [];
        for (var i = 0; i < 6; i++) {
          var a = Math.PI / 180 * (60 * i - 30);
          pts.push((cx + HR * 0.94 * Math.cos(a)).toFixed(1) + "," + (cy + HR * 0.94 * Math.sin(a)).toFixed(1));
        }
        var hex = el("polygon", { points: pts.join(" "), class: "b-hex", style: "fill-opacity:" + (0.18 + 0.82 * b.n / max).toFixed(2) }, svg);
        (function (b) {
          hex.addEventListener("mousemove", function (e) {
            var zs = Object.keys(b.zones).map(function (z) { return esc(z); }).join(", ");
            showTip(e, "<b>" + b.n + " death" + (b.n > 1 ? "s" : "") + "</b><br><span class=dim>" + zs + "</span>");
          });
          hex.addEventListener("mouseleave", hideTip);
        })(b);
        if (b.n >= 3) {
          var t = el("text", { x: cx, y: cy + 3.5, class: "b-hexn", "text-anchor": "middle" }, svg);
          t.textContent = b.n;
        }
      }
      var topz = Object.keys(zt).sort(function (a, b) { return zt[b] - zt[a]; })[0];
      var cap = document.createElement("figcaption");
      cap.innerHTML = "<b>" + ph[2] + "</b> <span>" + ph[1] + "</span><br>" + total + " deaths" + (topz ? ", most in <em>" + esc(topz.toLowerCase()) + "</em> (" + zt[topz] + ")" : "");
      fig.appendChild(cap);
      box.appendChild(fig);
    });
  })();

  // ── B · Annotated pool scatter ────────────────────────────────────────
  (function () {
    var svg = document.querySelector('[data-viz="sky-scatter"]');
    if (!svg) return;
    var W = 900, H = 480, L = 60, Rr = 30, T = 30, B = 50;
    var cs = D.champs;
    if (!cs.length) return;
    var gmax = Math.max.apply(null, cs.map(function (c) { return c.games; }));
    var amin = Math.min.apply(null, cs.map(function (c) { return c.adj; }).concat([D.base - 0.05]));
    var amax = Math.max.apply(null, cs.map(function (c) { return c.adj; }).concat([D.base + 0.05]));
    amin -= 0.01; amax += 0.01;
    var linear = gmax <= 12;
    var colW = (W - L - Rr) / (gmax + 0.2);
    function X(g) { return linear ? L + (g - 0.4) * colW : L + Math.log(g) / Math.log(gmax * 1.15) * (W - L - Rr); }
    var split = linear ? Math.max(2, Math.ceil(gmax * 0.6)) : 5;
    function Y(a) { return T + (1 - (a - amin) / (amax - amin)) * (H - T - B); }
    var defs = el("defs", {}, svg);
    // Quadrants: split at 5 games and at my average.
    var xs = linear ? X(split - 0.5) : X(split), yb = Y(D.base);
    el("rect", { x: xs, y: T, width: W - Rr - xs, height: yb - T, class: "b-quad good" }, svg);
    [["Signature picks", W - Rr - 8, T + 18, "end"], ["Hidden weapons", L + 8, T + 18, "start"], ["Comfort traps", W - Rr - 8, H - B - 10, "end"], ["Experiments", L + 8, H - B - 10, "start"]].forEach(function (q) {
      var t = el("text", { x: q[1], y: q[2], "text-anchor": q[3], class: "b-quadlbl" }, svg);
      t.textContent = q[0];
    });
    el("line", { x1: L, y1: yb, x2: W - Rr, y2: yb, class: "b-ref" }, svg);
    var tb = el("text", { x: L + 4, y: yb - 6, class: "b-reflbl" }, svg);
    tb.textContent = "your average, " + pct(D.base);
    el("line", { x1: xs, y1: T, x2: xs, y2: H - B, class: "b-ref" }, svg);
    (linear ? Array.from({ length: gmax }, function (_, i) { return i + 1; }) : [1, 2, 5, 10, 20, 50, 100]).filter(function (g) { return g <= gmax * 1.15; }).forEach(function (g) {
      var t = el("text", { x: X(g), y: H - B + 20, "text-anchor": "middle", class: "b-axis" }, svg);
      t.textContent = g;
    });
    var tx = el("text", { x: W - Rr, y: H - 8, "text-anchor": "end", class: "b-axis title" }, svg);
    tx.textContent = "games played →";
    for (var a = Math.ceil(amin * 20) / 20; a <= amax; a += 0.05) {
      var ty = el("text", { x: L - 10, y: Y(a) + 4, "text-anchor": "end", class: "b-axis" }, svg);
      ty.textContent = pct(a);
    }
    var placed = [];
    var order = cs.slice().sort(function (a, b) { return a.games - b.games || b.wr - a.wr; });
    // Beeswarm: champions that land on the same spot spread sideways.
    var spots = {};
    order.forEach(function (c) { var k = c.games + ":" + c.adj.toFixed(3); (spots[k] = spots[k] || []).push(c); });
    var offs = {};
    Object.keys(spots).forEach(function (k) {
      var g = spots[k], step = Math.min(c3(g[0]) ? 30 : 9, (linear ? colW * 0.8 : 60) / g.length);
      g.forEach(function (c, i) { offs[c.key] = (i - (g.length - 1) / 2) * step; });
    });
    function c3(c) { return c.games >= 3; }
    order.forEach(function (c, i) {
      var x = X(c.games) + offs[c.key], y = Y(c.adj);
      var g = el("g", { class: "b-pt", tabindex: 0 }, svg);
      if (c.games >= 3) {
        var r = 9 + Math.sqrt(c.games) * 2.6;
        var id = "b-clip-" + i;
        el("circle", { cx: x, cy: y, r: r }, el("clipPath", { id: id }, defs));
        el("circle", { cx: x, cy: y, r: r + 2.5, class: "b-ptring" + (c.adj >= D.base ? " up" : " down") }, g);
        el("image", { href: c.icon, x: x - r, y: y - r, width: r * 2, height: r * 2, "clip-path": "url(#" + id + ")" }, g);
        placed.push({ c: c, x: x, y: y, r: r });
      } else {
        el("circle", { cx: x, cy: y, r: 3.2, class: "b-dot" }, g);
      }
      g.addEventListener("mousemove", function (e) { showTip(e, champTip(c)); });
      g.addEventListener("mouseleave", hideTip);
      g.addEventListener("click", function () { location.href = "/champions/" + encodeURIComponent(c.key); });
    });
    // Call out the four most played with leader lines.
    placed.sort(function (a, b) { return b.c.games - a.c.games; }).slice(0, 4).forEach(function (p, i) {
      var up = p.y > (T + H - B) / 2;
      var ly = up ? p.y - p.r - 26 - i * 4 : p.y + p.r + 30 + i * 4;
      var lx = Math.min(W - Rr - 100, Math.max(L + 10, p.x + (i % 2 ? -40 : 40)));
      el("polyline", { points: [p.x, p.y + (up ? -p.r - 3 : p.r + 3), lx, ly + (up ? 6 : -12)].join(" "), class: "b-leader" }, svg);
      var t = el("text", { x: lx, y: ly, class: "b-callout", "text-anchor": lx < p.x ? "end" : "start" }, svg);
      t.textContent = p.c.name + " · " + p.c.games + "g · " + pct(p.c.adj);
    });
  })();

  // ── B · Result ribbon ────────────────────────────────────────────────
  (function () {
    var box = document.querySelector('[data-viz="ribbon"]');
    if (!box) return;
    var gs = D.games.slice(0, 20).reverse();
    gs.forEach(function (g) {
      var kda = Math.min(8, (g.k + g.a) / Math.max(1, g.d));
      var a = document.createElement("a");
      a.href = "/matches/" + encodeURIComponent(g.id);
      a.className = "b-rb " + (g.win ? "w" : "l");
      a.innerHTML = '<span class="b-rbbar" style="height:' + (8 + kda * 11) + 'px"></span><span class="b-rbname">' + esc(g.champ) + "</span><span class=b-rbdate>" + esc(g.date) + "</span>";
      a.title = g.champ + " " + g.k + "/" + g.d + "/" + g.a + " · " + g.date;
      box.appendChild(a);
    });
  })();

  // ── C · Embers over game time ─────────────────────────────────────────
  (function () {
    var canvas = document.querySelector('[data-viz="heat-embers"]');
    if (!canvas) return;
    var range = document.querySelector("[data-minute]"), out = document.querySelector("[data-minute-out]");
    var play = document.querySelector("[data-play]");
    var maxM = Math.ceil(Math.max(30, Math.max.apply(null, D.deaths.map(function (d) { return d.m; }).concat([0]))));
    range.max = maxM;
    range.value = maxM;
    var M = maxM, timer = null;
    function draw() {
      var f = fit(canvas), ctx = f[0], w = f[1], h = f[2];
      ctx.clearRect(0, 0, w, h);
      ctx.globalCompositeOperation = "lighter";
      D.deaths.forEach(function (d) {
        if (d.m > M) return;
        var age = M - d.m, fresh = Math.max(0, 1 - age / 4);
        var p = mine(d), q = proj(p[0], p[1], w, h);
        var r = 10 + fresh * 16;
        var col = d.w ? "63,216,200" : "255,92,60";
        var g = ctx.createRadialGradient(q[0], q[1], 0, q[0], q[1], r);
        g.addColorStop(0, "rgba(" + col + "," + (0.55 + fresh * 0.45) + ")");
        g.addColorStop(0.35, "rgba(" + col + "," + (0.22 + fresh * 0.2) + ")");
        g.addColorStop(1, "rgba(" + col + ",0)");
        ctx.fillStyle = g;
        ctx.beginPath(); ctx.arc(q[0], q[1], r, 0, Math.PI * 2); ctx.fill();
        ctx.fillStyle = "rgba(255,245,220," + (0.5 + fresh * 0.5) + ")";
        ctx.beginPath(); ctx.arc(q[0], q[1], 1.4 + fresh * 1.2, 0, Math.PI * 2); ctx.fill();
      });
      ctx.globalCompositeOperation = "source-over";
      out.textContent = mmss(M);
    }
    function set(m) { M = m; range.value = Math.round(m); draw(); }
    range.addEventListener("input", function () { stop(); set(+range.value); });
    function stop() { if (timer) { cancelAnimationFrame(timer); timer = null; play.textContent = "▶"; } }
    play.addEventListener("click", function () {
      if (timer) { stop(); return; }
      var t0 = performance.now(), from = M >= maxM ? 0 : M;
      play.textContent = "❚❚";
      (function tick(now) {
        var m = from + (now - t0) / 1000 * (maxM / 8);
        if (m >= maxM) { set(maxM); stop(); return; }
        set(m);
        timer = requestAnimationFrame(tick);
      })(t0);
    });
    draw();
    addEventListener("resize", draw);

    var zones = {};
    D.deaths.forEach(function (d) {
      var p = mine(d), z = zone(p[0], p[1]);
      var s = zones[z] = zones[z] || { n: 0, l: 0, m: 0 };
      s.n++; if (!d.w) s.l++; s.m += d.m;
    });
    var list = document.querySelector("[data-hotspots]");
    Object.keys(zones).sort(function (a, b) { return zones[b].n - zones[a].n; }).slice(0, 5).forEach(function (z) {
      var s = zones[z], li = document.createElement("li");
      li.innerHTML = "<b>" + esc(z) + "</b><span>" + s.n + " deaths · " + pct(s.l / s.n) + " in losses · avg " + mmss(s.m / s.n) + "</span>";
      list.appendChild(li);
    });
  })();

  // ── C · Champion web ──────────────────────────────────────────────────
  (function () {
    var svg = document.querySelector('[data-viz="sky-network"]');
    if (!svg) return;
    var C = 320, R = 232;
    var cs = D.champs.slice().sort(function (a, b) { return b.games - a.games; }).slice(0, 28);
    cs.sort(function (a, b) {
      var ra = ROLES.indexOf(roleOf(a)), rb = ROLES.indexOf(roleOf(b));
      return ra - rb || b.games - a.games;
    });
    var pos = {};
    cs.forEach(function (c, i) {
      var a = (i / cs.length) * Math.PI * 2 - Math.PI / 2;
      pos[c.key] = { x: C + Math.cos(a) * R, y: C + Math.sin(a) * R, a: a, c: c };
    });
    var defs = el("defs", {}, svg);
    el("circle", { cx: C, cy: C, r: R + 40, class: "c-webring" }, svg);
    el("circle", { cx: C, cy: C, r: R - 40, class: "c-webring inner" }, svg);
    // Role arcs.
    ROLES.forEach(function (role) {
      var idx = cs.map(function (c, i) { return roleOf(c) === role ? i : -1; }).filter(function (i) { return i >= 0; });
      if (!idx.length) return;
      var am = ((idx[0] + idx[idx.length - 1]) / 2 / cs.length) * Math.PI * 2 - Math.PI / 2;
      var t = el("text", { x: C + Math.cos(am) * (R + 62), y: C + Math.sin(am) * (R + 62) + 4, "text-anchor": "middle", class: "c-rolelbl" }, svg);
      t.textContent = ROLE_NAMES[role];
    });
    var maxN = Math.max.apply(null, D.trans.map(function (t) { return t.n; }).concat([1]));
    var edges = el("g", {}, svg), nodes = el("g", {}, svg);
    var byKey = {};
    D.champs.forEach(function (c) { byKey[c.name] = c; });
    var edgeEls = [];
    D.trans.forEach(function (t) {
      var a = pos[t.a], b = pos[t.b];
      if (!a || !b) return;
      var mx = (a.x + b.x) / 2, my = (a.y + b.y) / 2;
      var cx = C + (mx - C) * 0.25, cy = C + (my - C) * 0.25;
      var p = el("path", { d: "M" + a.x.toFixed(1) + " " + a.y.toFixed(1) + " Q" + cx.toFixed(1) + " " + cy.toFixed(1) + " " + b.x.toFixed(1) + " " + b.y.toFixed(1), class: "c-edge", style: "stroke-width:" + (0.8 + 4 * t.n / maxN).toFixed(2) + ";stroke-opacity:" + (0.25 + 0.6 * t.n / maxN).toFixed(2) }, edges);
      p.dataset.a = t.a; p.dataset.b = t.b;
      edgeEls.push(p);
      p.addEventListener("mousemove", function (e) { showTip(e, esc(a.c.name) + " ⇄ " + esc(b.c.name) + "<br>" + t.n + " switch" + (t.n > 1 ? "es" : "")); });
      p.addEventListener("mouseleave", hideTip);
    });
    cs.forEach(function (c, i) {
      var p = pos[c.key], r = 11 + Math.sqrt(c.games) * 2.4;
      var hex = [];
      for (var k = 0; k < 6; k++) {
        var a = Math.PI / 3 * k + Math.PI / 6;
        hex.push((p.x + r * Math.cos(a)).toFixed(1) + "," + (p.y + r * Math.sin(a)).toFixed(1));
      }
      var id = "c-clip-" + i;
      el("polygon", { points: hex.join(" ") }, el("clipPath", { id: id }, defs));
      var g = el("g", { class: "c-node " + (c.adj >= D.base + 0.03 ? "gold" : c.adj <= D.base - 0.03 ? "red" : "teal"), tabindex: 0 }, nodes);
      el("polygon", { points: hex.map(function (s) { var q = s.split(","); return (p.x + (q[0] - p.x) * 1.16).toFixed(1) + "," + (p.y + (q[1] - p.y) * 1.16).toFixed(1); }).join(" "), class: "c-frame" }, g);
      el("image", { href: c.icon, x: p.x - r, y: p.y - r, width: r * 2, height: r * 2, "clip-path": "url(#" + id + ")", preserveAspectRatio: "xMidYMid slice" }, g);
      g.addEventListener("mouseenter", function () {
        svg.classList.add("focus");
        edgeEls.forEach(function (e) { e.classList.toggle("on", e.dataset.a === c.key || e.dataset.b === c.key); });
      });
      g.addEventListener("mousemove", function (e) { showTip(e, champTip(c)); });
      g.addEventListener("mouseleave", function () { svg.classList.remove("focus"); hideTip(); });
      g.addEventListener("click", function () { location.href = "/champions/" + encodeURIComponent(c.key); });
    });
    var core = el("text", { x: C, y: C - 6, "text-anchor": "middle", class: "c-core" }, svg);
    core.textContent = cs.length;
    var core2 = el("text", { x: C, y: C + 16, "text-anchor": "middle", class: "c-core2" }, svg);
    core2.textContent = "CHAMPIONS";
  })();
})();
