// Owner's recent items: list or search, select, bulk delete; shown as a
// list or as a grid of same-size cards (thumbnail or start of the text).
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
  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text) e.textContent = text;
    return e;
  }
  // The preview: a thumbnail, the start of a text, or the kind. The list
  // view shows only thumbnails; the grid shows all three.
  function preview(it) {
    var pv = el("a", "pv");
    pv.href = it.url;
    pv.tabIndex = -1;
    var kind = function () { pv.className = "pv none"; pv.textContent = ""; pv.appendChild(el("span", "kind", it.kind)); };
    var text = it.excerpt || it.snippet;
    if (it.thumbnail_url) {
      var img = document.createElement("img");
      img.alt = "";
      img.loading = "lazy";
      img.addEventListener("error", kind); // none can be made
      if (it.visibility === "private") {
        // Private thumbnails need the token: fetch them as blobs.
        fetch(it.thumbnail_url.replace(/^https?:\/\/[^/]+/, ""), { headers: { Authorization: "Bearer " + T.token() } })
          .then(function (r) { if (!r.ok) throw new Error(r.status); return r.blob(); })
          .then(function (b) { img.src = URL.createObjectURL(b); }, kind);
      } else {
        img.src = it.thumbnail_url;
      }
      pv.appendChild(img);
    } else if (text) {
      pv.className = "pv none";
      pv.appendChild(el("pre", "text", text));
    } else {
      kind();
    }
    return pv;
  }
  function row(it) {
    var li = document.createElement("li");
    var cb = document.createElement("input");
    cb.type = "checkbox";
    cb.value = it.id;
    cb.addEventListener("change", update);
    li.appendChild(cb);
    li.appendChild(preview(it));
    var info = el("div", "info");
    var a = el("a", "name", it.title || it.filename || it.id);
    a.href = it.url;
    a.title = a.textContent;
    info.appendChild(a);
    var meta = document.createElement("span");
    meta.className = "muted";
    var flags = [it.kind, size(it.size), new Date(it.created_at * 1000).toLocaleString()];
    if (it.visibility === "private") flags.push(t("meta.private"));
    if (it.snippet) flags.push("“" + it.snippet + "”");
    meta.textContent = flags.join(" · ");
    info.appendChild(meta);
    if (it.source_url) { // where it came from (only the uploader gets this)
      var src = el("a", "src");
      src.href = it.source_url;
      src.target = "_blank";
      src.rel = "noopener noreferrer";
      src.title = it.source_url;
      var host = it.source_url;
      try { host = new URL(it.source_url).host; } catch (e) {}
      src.textContent = t("recent.source") + ": " + host;
      info.appendChild(src);
    }
    li.appendChild(info);
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
  // List or grid, remembered in this browser.
  var tabs = document.querySelectorAll("#view .tab");
  function view(v) {
    if (v !== "grid") v = "list";
    list.className = v === "grid" ? "item-grid" : "item-list";
    tabs.forEach(function (b) {
      b.classList.toggle("active", b.dataset.view === v);
      b.setAttribute("aria-pressed", String(b.dataset.view === v));
    });
    try { localStorage.setItem("tibi.recentView", v); } catch (e) {}
  }
  tabs.forEach(function (b) { b.addEventListener("click", function () { view(b.dataset.view); }); });
  var saved = null;
  try { saved = localStorage.getItem("tibi.recentView"); } catch (e) {}
  view(saved);
  load(true);
})();
