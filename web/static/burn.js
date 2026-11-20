// Burn-after-read: keep the URL fragment across the reveal. A form post
// drops it, and for an end-to-end encrypted item the fragment is the key
// (#k=…). Fragments are never sent to the server; the browser only keeps
// it in the URL of the page the reveal returns.
(function () {
  "use strict";
  var f = document.getElementById("burn-form");
  if (f && location.hash) f.action = f.getAttribute("action") + location.hash;
})();
