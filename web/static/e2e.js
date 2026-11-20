// End-to-end encrypted items (PLAN A9): fetch the ciphertext, take the key
// from the URL fragment (#k=…, never sent to the server), ask for the
// password if there is one, decrypt with WebCrypto and show the content.
// Format: internal/e2e/e2e.go.
(function () {
  "use strict";
  var T = window.tibi, t = T.t;
  var box = document.getElementById("e2e");
  var status = document.getElementById("e2e-status");
  var out = document.getElementById("e2e-content");
  var actions = document.getElementById("e2e-actions");
  var pwForm = document.getElementById("e2e-password");
  var enc = new TextEncoder();

  function fail(msg) {
    status.textContent = msg;
    status.className = "notice error";
    status.hidden = false;
  }

  // What the page shows inline. The type comes from the (attacker-chosen)
  // header, so only plain raster images and media are displayed; anything
  // else is text or a download. blob: documents share this origin.
  var INLINE = /^(image\/(png|jpeg|gif|webp|avif|bmp)|video\/(mp4|webm|ogg|quicktime)|audio\/(mpeg|ogg|opus|mp4|wav|wave|flac|aac|webm))$/;
  function cleanType(t) {
    return String(t || "").split(";")[0].trim().toLowerCase();
  }
  function b64url(s) {
    s = s.replace(/-/g, "+").replace(/_/g, "/");
    while (s.length % 4) s += "=";
    var bin = atob(s), b = new Uint8Array(bin.length);
    for (var i = 0; i < bin.length; i++) b[i] = bin.charCodeAt(i);
    return b;
  }
  function keyFromHash() {
    var m = /(?:^#|&)k=([A-Za-z0-9_-]{43})(?:&|$)/.exec(location.hash);
    if (!m) return null;
    try { return b64url(m[1]); } catch (e) { return null; }
  }
  function concat(a, b) {
    var c = new Uint8Array(a.length + b.length);
    c.set(a, 0);
    c.set(b, a.length);
    return c;
  }

  async function aesKey(k, password, salt, iter) {
    var ikm = k;
    if (password) {
      var pw = await crypto.subtle.importKey("raw", enc.encode(password), "PBKDF2", false, ["deriveBits"]);
      var bits = await crypto.subtle.deriveBits({ name: "PBKDF2", salt: salt, iterations: iter, hash: "SHA-256" }, pw, 256);
      ikm = concat(k, new Uint8Array(bits));
    }
    var base = await crypto.subtle.importKey("raw", ikm, "HKDF", false, ["deriveKey"]);
    return crypto.subtle.deriveKey({ name: "HKDF", hash: "SHA-256", salt: new Uint8Array(0), info: enc.encode("tibi-e2e-v1") },
      base, { name: "AES-GCM", length: 256 }, false, ["decrypt"]);
  }

  // Returns {header, content} or throws "key" / "format".
  async function decrypt(blob, k, password) {
    if (blob.length < 2 || blob[0] !== 1 || (blob[1] & ~1) !== 0) throw "format";
    var n = 2, salt = null, iter = 0;
    if (blob[1] & 1) {
      if (blob.length < 22) throw "format";
      salt = blob.slice(2, 18);
      iter = new DataView(blob.buffer, blob.byteOffset + 18, 4).getUint32(0);
      // Same bounds as internal/e2e: a crafted count would freeze the tab.
      if (iter < 1 || iter > 10000000) throw "format";
      n = 22;
    }
    if (blob.length < n + 12 + 16) throw "format";
    var key = await aesKey(k, password, salt, iter);
    var plain;
    try {
      plain = new Uint8Array(await crypto.subtle.decrypt(
        { name: "AES-GCM", iv: blob.slice(n, n + 12), additionalData: blob.slice(0, n), tagLength: 128 },
        key, blob.slice(n + 12)));
    } catch (e) {
      throw "key";
    }
    if (plain.length < 4) throw "format";
    var hl = new DataView(plain.buffer).getUint32(0);
    if (hl > plain.length - 4) throw "format";
    var raw;
    try { raw = JSON.parse(new TextDecoder().decode(plain.slice(4, 4 + hl))); } catch (e) { throw "format"; }
    if (!raw || typeof raw !== "object" || Array.isArray(raw)) throw "format";
    var str = function (v) { return typeof v === "string" ? v : ""; };
    var header = { name: str(raw.name), type: str(raw.type), lang: str(raw.lang) };
    return { header: header, content: plain.slice(4 + hl) };
  }

  function downloadButton(content, header) {
    var a = document.createElement("a");
    a.className = "button";
    a.textContent = t("e2e.download");
    // Always octet-stream: the download attribute names the file, and a
    // blob: URL with an HTML or SVG type would open as a document here.
    a.href = URL.createObjectURL(new Blob([content], { type: "application/octet-stream" }));
    a.download = header.name || "decrypted";
    return a;
  }

  function show(res) {
    var h = res.header, c = res.content;
    status.hidden = true;
    if (h.name) document.getElementById("e2e-name").textContent = h.name;
    var type = cleanType(h.type);
    var inline = INLINE.test(type);
    var text = null;
    if (!inline) {
      try { text = new TextDecoder("utf-8", { fatal: true }).decode(c); } catch (e) { text = null; }
      if (text !== null && text.indexOf("\0") >= 0) text = null;
    }
    if (inline && type.indexOf("image/") === 0) {
      var img = document.createElement("img");
      img.src = URL.createObjectURL(new Blob([c], { type: type }));
      img.alt = h.name || "";
      out.appendChild(img);
    } else if (inline) {
      var m = document.createElement(type[0] === "v" ? "video" : "audio");
      m.controls = true;
      m.className = "player";
      m.src = URL.createObjectURL(new Blob([c], { type: type }));
      out.appendChild(m);
    } else if (text !== null) {
      var pre = document.createElement("pre"), code = document.createElement("code");
      pre.className = "e2e-code";
      var lang = h.lang || box.getAttribute("data-lang") || "";
      if (window.hljs && lang && lang !== "text" && hljs.getLanguage(lang)) {
        code.innerHTML = hljs.highlight(text, { language: lang, ignoreIllegals: true }).value; // escaped by hljs
        code.className = "hljs language-" + lang;
      } else {
        code.textContent = text;
      }
      pre.appendChild(code);
      out.appendChild(pre);
      actions.appendChild(T.copyButton(text));
    } else {
      var p = document.createElement("p");
      p.className = "muted";
      p.textContent = (h.name || "") + " (" + c.length + " bytes)";
      out.appendChild(p);
    }
    actions.insertBefore(downloadButton(c, h), actions.firstChild);
    actions.hidden = false;
  }

  async function load() {
    var r = await fetch(box.getAttribute("data-raw"));
    if (!r.ok) throw "fetch";
    return new Uint8Array(await r.arrayBuffer());
  }

  async function run() {
    var k = keyFromHash();
    if (!k) return fail(t("e2e.missing_key"));
    if (!window.crypto || !crypto.subtle) return fail(t("e2e.no_webcrypto"));
    var blob;
    try { blob = await load(); } catch (e) { return fail(t("e2e.fetch_failed")); }
    var attempt = async function (password) {
      try {
        show(await decrypt(blob, k, password));
        pwForm.hidden = true;
      } catch (e) {
        fail(e === "format" ? t("e2e.unsupported") : password ? t("e2e.wrong_password") : t("e2e.wrong_key"));
      }
    };
    if (blob.length > 1 && blob[0] === 1 && (blob[1] & 1)) {
      status.textContent = t("e2e.password_needed");
      pwForm.hidden = false;
      pwForm.addEventListener("submit", function (e) {
        e.preventDefault();
        status.className = "notice";
        status.textContent = t("e2e.decrypting");
        attempt(pwForm.elements.password.value);
      });
      pwForm.elements.password.focus();
      return;
    }
    status.textContent = t("e2e.decrypting");
    attempt("");
  }
  run();
})();
