// Text view: line anchors (#L12, #L12-L20, shift-click for ranges), wrap
// toggle, copy-all and the Markdown rendered/source tabs. No inline scripts.
(function () {
  "use strict";
  var msgs = {};
  try { msgs = JSON.parse(document.body.getAttribute("data-i18n") || "{}"); } catch (e) {}
  function t(k, a) { return (msgs[k] || k).replace("%s", a || ""); }

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

  // --- Line selection: #L12, #L12-L20, and #f2-L5-L8 in collections ---
  var anchor = null; // {prefix, line}
  function parse(hash) {
    var m = /^#((?:f\d+-)?)L(\d+)(?:-L(\d+))?$/.exec(hash || "");
    if (!m) return null;
    var a = +m[2], b = m[3] ? +m[3] : a;
    return { prefix: m[1], from: Math.min(a, b), to: Math.max(a, b) };
  }
  function mark() {
    document.querySelectorAll(".code .hl").forEach(function (el) { el.classList.remove("hl"); });
    var r = parse(location.hash);
    if (!r) return;
    if (tabs.length && !r.prefix) show("source");
    for (var i = r.from; i <= r.to; i++) {
      var el = document.getElementById(r.prefix + "L" + i);
      if (el) el.classList.add("hl");
    }
    var first = document.getElementById(r.prefix + "L" + r.from);
    if (first) first.scrollIntoView({ block: "center" });
    anchor = { prefix: r.prefix, line: r.from };
  }
  document.addEventListener("click", function (e) {
    var a = e.target.closest && e.target.closest(".code a[href^='#']");
    if (!a) return;
    var r = parse(a.getAttribute("href"));
    if (!r) return;
    if (e.shiftKey && anchor && anchor.prefix === r.prefix) {
      e.preventDefault();
      var lo = Math.min(anchor.line, r.from), hi = Math.max(anchor.line, r.from);
      var keep = anchor;
      history.replaceState(null, "", "#" + r.prefix + "L" + lo + "-L" + hi);
      mark();
      anchor = keep;
    }
  });
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
        b.textContent = t("action.copied");
        setTimeout(function () { b.textContent = old; }, 1200);
      });
    });
  });
})();

// Fork: shown only when this browser holds an API token (saved by the web
// upload page under "tibi.token"). Editing creates a new item via
// POST /api/v1/items/{id}/fork; the original never changes.
(function () {
  "use strict";
  var msgs = {};
  try { msgs = JSON.parse(document.body.getAttribute("data-i18n") || "{}"); } catch (e) {}
  function t(k, a) { return (msgs[k] || k).replace("%s", a || ""); }
  var btn = document.getElementById("fork-button");
  var form = document.getElementById("fork-editor");
  if (!btn || !form) return;
  var token = null;
  try { token = localStorage.getItem("tibi.token"); } catch (e) {}
  if (!token) return;
  btn.hidden = false;
  var status = document.getElementById("fork-status");
  btn.addEventListener("click", function () {
    fetch(btn.getAttribute("data-raw"), { headers: { Authorization: "Bearer " + token } })
      .then(function (r) { return r.text(); })
      .then(function (t) {
        form.elements.text.value = t;
        form.hidden = false;
        form.elements.text.focus();
      });
  });
  document.getElementById("fork-cancel").addEventListener("click", function () { form.hidden = true; });
  form.addEventListener("submit", function (e) {
    e.preventDefault();
    status.textContent = t("fork.saving");
    fetch(btn.getAttribute("data-fork"), {
      method: "POST",
      headers: { Authorization: "Bearer " + token, "Content-Type": "text/plain; charset=utf-8" },
      body: form.elements.text.value
    }).then(function (r) {
      return r.json().then(function (j) {
        if (!r.ok) throw new Error((j.error && j.error.message) || r.statusText);
        location.href = j.url;
      });
    }).catch(function (err) { status.textContent = t("fork.failed", err.message); });
  });
})();
