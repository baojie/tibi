// Owner's recent items: list or search, select, bulk delete.
(function () {
  "use strict";
  var T = window.tibi, t = T.t;
  if (!T.token()) { document.getElementById("need-token").hidden = false; return; }
  var box = document.getElementById("recent");
  box.hidden = false;
  var list = document.getElementById("items");
  var more = document.getElementById("load-more");
  var status = document.getElementById("recent-status");
  var del = document.getElementById("delete-selected");
  var cursor = null, query = "";

  function size(n) {
    var u = ["B", "KiB", "MiB", "GiB"], i = 0;
    while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
    return (i ? n.toFixed(1) : n) + " " + u[i];
  }
  function row(it) {
    var li = document.createElement("li");
    var cb = document.createElement("input");
    cb.type = "checkbox";
    cb.value = it.id;
    cb.addEventListener("change", update);
    li.appendChild(cb);
    if (it.thumbnail_url) {
      var img = document.createElement("img");
      img.alt = "";
      img.loading = "lazy";
      if (it.visibility === "private") {
        // Private thumbnails need the token: fetch them as blobs.
        fetch(it.thumbnail_url.replace(/^https?:\/\/[^/]+/, ""), { headers: { Authorization: "Bearer " + T.token() } })
          .then(function (r) { return r.blob(); }).then(function (b) { img.src = URL.createObjectURL(b); });
      } else {
        img.src = it.thumbnail_url;
      }
      li.appendChild(img);
    }
    var a = document.createElement("a");
    a.href = it.url;
    a.textContent = it.title || it.filename || it.id;
    li.appendChild(a);
    var meta = document.createElement("span");
    meta.className = "muted";
    var flags = [it.kind, size(it.size), new Date(it.created_at * 1000).toLocaleString()];
    if (it.visibility === "private") flags.push(t("meta.private"));
    if (it.snippet) flags.push("“" + it.snippet + "”");
    meta.textContent = flags.join(" · ");
    li.appendChild(meta);
    return li;
  }
  function load(reset) {
    if (reset) { list.textContent = ""; cursor = null; }
    var path = query ? "/api/v1/search?q=" + encodeURIComponent(query) + "&limit=30" : "/api/v1/items?limit=30";
    if (cursor) path += "&cursor=" + encodeURIComponent(cursor);
    T.api("GET", path).then(function (page) {
      page.items.forEach(function (it) { list.appendChild(row(it)); });
      cursor = page.next_cursor;
      more.hidden = !cursor;
      status.textContent = list.children.length ? "" : t("recent.empty");
      update();
    }).catch(function (err) { status.textContent = err.message; });
  }
  function selected() {
    return Array.prototype.filter.call(list.querySelectorAll("input[type=checkbox]"), function (c) { return c.checked; });
  }
  function update() { del.disabled = selected().length === 0; }
  document.getElementById("select-all").addEventListener("change", function (e) {
    list.querySelectorAll("input[type=checkbox]").forEach(function (c) { c.checked = e.target.checked; });
    update();
  });
  del.addEventListener("click", function () {
    var boxes = selected();
    if (!boxes.length || !confirm(t("recent.confirm_delete", boxes.length))) return;
    Promise.all(boxes.map(function (c) {
      return T.api("DELETE", "/api/v1/items/" + c.value).then(function () { c.parentNode.remove(); });
    })).catch(function (err) { status.textContent = err.message; }).then(update);
  });
  document.getElementById("search-form").addEventListener("submit", function (e) {
    e.preventDefault();
    query = e.target.elements.q.value.trim();
    load(true);
  });
  more.addEventListener("click", function () { load(false); });
  load(true);
})();
