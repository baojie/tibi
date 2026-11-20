// Owner upload page: token setup, drag-and-drop, file picker, paste
// (images and text), text pastes, results with copy buttons.
(function () {
  "use strict";
  var T = window.tibi, t = T.t;
  var setup = document.getElementById("token-setup");
  var uploader = document.getElementById("uploader");
  var status = document.getElementById("upload-status");
  var results = document.getElementById("results");

  function showUploader(name) {
    setup.hidden = true;
    uploader.hidden = false;
    document.getElementById("token-status").textContent = t("token.connected", name);
    var nav = document.querySelector(".owner-nav");
    if (nav) nav.hidden = false;
  }
  function check() {
    if (!T.token()) { setup.hidden = false; uploader.hidden = true; return; }
    T.api("GET", "/api/v1/whoami").then(function (w) { showUploader(w.name); })
      .catch(function () { T.token(null); setup.hidden = false; });
  }
  setup.addEventListener("submit", function (e) {
    e.preventDefault();
    var v = setup.elements.token.value.trim();
    T.token(v);
    T.api("GET", "/api/v1/whoami").then(function (w) {
      document.getElementById("token-error").hidden = true;
      setup.reset();
      showUploader(w.name);
    }).catch(function () {
      T.token(null);
      document.getElementById("token-error").hidden = false;
    });
  });
  document.getElementById("token-forget").addEventListener("click", function () { T.token(null); location.reload(); });

  function options(form) {
    form.append("expires", document.getElementById("opt-expires").value);
    if (document.getElementById("opt-private").checked) form.append("visibility", "private");
    if (document.getElementById("opt-burn").checked) form.append("burn_after_read", "1");
    form.append("source", "web");
  }
  function send(form, label) {
    status.textContent = t("upload.uploading", label);
    return new Promise(function (resolve, reject) {
      var x = new XMLHttpRequest();
      x.open("POST", "/api/v1/items");
      x.setRequestHeader("Authorization", "Bearer " + T.token());
      x.upload.onprogress = function (e) {
        if (e.lengthComputable) status.textContent = t("upload.uploading", label) + " " + Math.round(100 * e.loaded / e.total) + "%";
      };
      x.onload = function () {
        var j = null;
        try { j = JSON.parse(x.responseText); } catch (e) {}
        if (x.status === 201) resolve(j);
        else reject(new Error((j && j.error && j.error.message) || x.statusText));
      };
      x.onerror = function () { reject(new Error("network error")); };
      x.send(form);
    });
  }
  function show(res) {
    status.textContent = "";
    document.getElementById("results-title").hidden = false;
    var items = Array.isArray(res) ? res : res.collection ? [res.collection].concat(res.items) : [res];
    items.forEach(function (it) {
      var li = document.createElement("li");
      if (it.thumbnail_url) {
        var img = document.createElement("img");
        img.src = it.thumbnail_url;
        img.alt = "";
        li.appendChild(img);
      }
      var a = document.createElement("a");
      a.href = it.url;
      a.textContent = it.url;
      a.className = "result-link";
      li.appendChild(a);
      li.appendChild(T.copyButton(it.url));
      if (it.kind === "image") li.appendChild(T.copyButton(it.markdown, "Markdown"));
      results.insertBefore(li, results.firstChild);
    });
  }
  function fail(err) { status.textContent = t("upload.failed", err.message); }

  function uploadFiles(files) {
    files = Array.prototype.slice.call(files);
    if (!files.length) return;
    if (document.getElementById("opt-album").checked && files.length > 1) {
      var f = new FormData();
      options(f);
      f.append("collection", "new");
      files.forEach(function (file) { f.append("file", file, file.name || "pasted"); });
      return send(f, files.length + " files").then(show, fail);
    }
    return files.reduce(function (p, file) {
      return p.then(function () {
        var f = new FormData();
        options(f);
        f.append("file", file, file.name || "pasted.png");
        return send(f, file.name || "image").then(show, fail);
      });
    }, Promise.resolve());
  }

  var zone = document.getElementById("dropzone");
  var input = document.getElementById("file-input");
  input.addEventListener("change", function () { uploadFiles(input.files); input.value = ""; });
  ["dragenter", "dragover"].forEach(function (ev) {
    zone.addEventListener(ev, function (e) { e.preventDefault(); zone.classList.add("over"); });
  });
  ["dragleave", "drop"].forEach(function (ev) {
    zone.addEventListener(ev, function () { zone.classList.remove("over"); });
  });
  zone.addEventListener("drop", function (e) { e.preventDefault(); uploadFiles(e.dataTransfer.files); });

  var textForm = document.getElementById("text-form");
  document.addEventListener("paste", function (e) {
    if (uploader.hidden) return;
    var cd = e.clipboardData;
    if (cd && cd.files && cd.files.length) { // screenshots, copied images/files
      e.preventDefault();
      uploadFiles(cd.files);
      return;
    }
    if (e.target === textForm.elements.text) return; // normal text paste
    var text = cd && cd.getData("text/plain");
    if (text) {
      e.preventDefault();
      textForm.elements.text.value = text;
      textForm.elements.text.focus();
    }
  });
  textForm.addEventListener("submit", function (e) {
    e.preventDefault();
    var text = textForm.elements.text.value;
    if (!text.trim()) return;
    var q = new URLSearchParams({ kind: "text", source: "web", expires: document.getElementById("opt-expires").value });
    if (textForm.elements.lang.value.trim()) q.set("lang", textForm.elements.lang.value.trim());
    if (document.getElementById("opt-private").checked) q.set("visibility", "private");
    if (document.getElementById("opt-burn").checked) q.set("burn_after_read", "1");
    status.textContent = t("upload.uploading", "text");
    T.api("POST", "/api/v1/items?" + q.toString(), text, { "Content-Type": "text/plain; charset=utf-8" })
      .then(function (res) { textForm.reset(); show(res); }, fail);
  });
  check();
})();
