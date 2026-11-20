// Copy buttons: <button data-copy="text">. No inline scripts (CSP).
document.addEventListener("click", function (e) {
  var b = e.target.closest("[data-copy]");
  if (!b) return;
  var text = b.getAttribute("data-copy");
  var done = function () {
    var old = b.textContent;
    var msgs = {};
    try { msgs = JSON.parse(document.body.getAttribute("data-i18n") || "{}"); } catch (e) {}
    b.textContent = msgs["action.copied"] || "Copied";
    setTimeout(function () { b.textContent = old; }, 1200);
  };
  if (navigator.clipboard && window.isSecureContext) {
    navigator.clipboard.writeText(text).then(done);
  } else {
    var input = b.previousElementSibling && b.previousElementSibling.querySelector("input");
    if (input) { input.select(); document.execCommand("copy"); done(); }
  }
});
