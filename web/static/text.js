// Text view: line anchors (#L12, #L12-L20, shift-click for ranges), wrap
// toggle, copy-all and the Markdown rendered/source tabs. No inline scripts.
(function () {
  "use strict";
  var code = document.getElementById("code");

  function store(k, v) {
    try { if (v === undefined) return localStorage.getItem(k); localStorage.setItem(k, v); } catch (e) { return null; }
  }

  // --- Markdown tabs ---
  var tabs = document.querySelectorAll(".tab[data-view]");
  function show(view) {
    var r = document.getElementById("view-rendered"), s = document.getElementById("view-source");
    if (!r || !s) return;
    r.hidden = view !== "rendered";
    s.hidden = view !== "source";
    tabs.forEach(function (t) {
      var on = t.getAttribute("data-view") === view;
      t.classList.toggle("active", on);
      t.setAttribute("aria-selected", on ? "true" : "false");
    });
  }
  tabs.forEach(function (t) {
    t.addEventListener("click", function () { show(t.getAttribute("data-view")); });
  });

  // --- Line selection ---
  var anchor = null;
  function parse(hash) {
    var m = /^#L(\d+)(?:-L(\d+))?$/.exec(hash || "");
    if (!m) return null;
    var a = +m[1], b = m[2] ? +m[2] : a;
    return a <= b ? [a, b] : [b, a];
  }
  function mark() {
    if (!code) return;
    code.querySelectorAll(".hl").forEach(function (el) { el.classList.remove("hl"); });
    var r = parse(location.hash);
    if (!r) return;
    if (tabs.length) show("source");
    for (var i = r[0]; i <= r[1]; i++) {
      var el = document.getElementById("L" + i);
      if (el) el.classList.add("hl");
    }
    var first = document.getElementById("L" + r[0]);
    if (first) first.scrollIntoView({ block: "center" });
    anchor = r[0];
  }
  if (code) {
    code.addEventListener("click", function (e) {
      var a = e.target.closest("a[href^='#L']");
      if (!a) return;
      var n = +a.getAttribute("href").slice(2);
      if (e.shiftKey && anchor) {
        e.preventDefault();
        var lo = Math.min(anchor, n), hi = Math.max(anchor, n);
        history.replaceState(null, "", "#L" + lo + "-L" + hi);
        var keep = anchor;
        mark();
        anchor = keep;
      }
    });
  }
  window.addEventListener("hashchange", mark);
  mark();

  // --- Wrap toggle ---
  var wrapBtn = document.getElementById("wrap-toggle");
  function setWrap(on) {
    document.querySelectorAll(".code").forEach(function (c) { c.classList.toggle("wrap", on); });
    if (wrapBtn) wrapBtn.setAttribute("aria-pressed", on ? "true" : "false");
  }
  if (wrapBtn) {
    setWrap(store("tibi.wrap") === "1");
    wrapBtn.addEventListener("click", function () {
      var on = wrapBtn.getAttribute("aria-pressed") !== "true";
      setWrap(on);
      store("tibi.wrap", on ? "1" : "0");
    });
  }

  // --- Copy all (fetches the raw text so truncated views copy everything) ---
  document.querySelectorAll("[data-copy-url]").forEach(function (b) {
    b.addEventListener("click", function () {
      fetch(b.getAttribute("data-copy-url")).then(function (r) { return r.text(); }).then(function (t) {
        return navigator.clipboard.writeText(t);
      }).then(function () {
        var old = b.textContent;
        b.textContent = "Copied";
        setTimeout(function () { b.textContent = old; }, 1200);
      });
    });
  });
})();
