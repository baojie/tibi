// Show the owner's navigation in browsers that hold an API token.
(function () {
  "use strict";
  var t = null;
  try { t = localStorage.getItem("tibi.token"); } catch (e) {}
  var nav = document.querySelector(".owner-nav");
  if (nav && t) nav.hidden = false;
})();
