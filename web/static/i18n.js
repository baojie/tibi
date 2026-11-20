// Shared helpers for page scripts: translated strings (from the page's
// data-i18n attribute) and the API token kept in this browser.
window.tibi = (function () {
  "use strict";
  var msgs = {};
  try { msgs = JSON.parse(document.body.getAttribute("data-i18n") || "{}"); } catch (e) {}
  function t(key) {
    var s = msgs[key] || key, args = Array.prototype.slice.call(arguments, 1), i = 0;
    return s.replace(/%[sd]/g, function () { return i < args.length ? String(args[i++]) : ""; });
  }
  function token(v) {
    try {
      if (v === undefined) return localStorage.getItem("tibi.token");
      if (v === null) localStorage.removeItem("tibi.token"); else localStorage.setItem("tibi.token", v);
    } catch (e) { return null; }
  }
  function api(method, path, body, headers) {
    var h = headers || {};
    h.Authorization = "Bearer " + token();
    return fetch(path, { method: method, headers: h, body: body }).then(function (r) {
      if (r.status === 204) return null;
      return r.json().then(function (j) {
        if (!r.ok) throw new Error((j && j.error && j.error.message) || r.statusText);
        return j;
      });
    });
  }
  function copyButton(text, label) {
    var b = document.createElement("button");
    label = label || t("action.copy");
    b.type = "button";
    b.className = "button secondary";
    b.textContent = label;
    b.addEventListener("click", function () {
      navigator.clipboard.writeText(text).then(function () {
        b.textContent = t("action.copied");
        setTimeout(function () { b.textContent = label; }, 1200);
      });
    });
    return b;
  }
  return { t: t, token: token, api: api, copyButton: copyButton };
})();
