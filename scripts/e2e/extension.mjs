// The Chrome extension in a real Chrome, against a tibi server whose
// cors_origins holds only the extension's origin.
//
//   node scripts/e2e/extension.mjs TIBI_BINARY [chrome]
//
// Chrome ≥ 137 ignores --load-extension, so the extension is loaded over
// a DevTools pipe (Extensions.loadUnpacked). Context-menu clicks cannot be
// scripted: the test invokes the extension's action on the tab (which
// grants activeTab, as a menu click does) and then calls the handler the
// menu calls. The web page is on localhost and tibi on 127.0.0.1, so the
// extension has no host access to tibi: its API calls rely on CORS alone.
import { spawn, execFileSync } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { createHash } from "node:crypto";
import { crc32, deflateSync } from "node:zlib";

const [tibi, chromePath = "google-chrome"] = process.argv.slice(2);
if (!tibi) { console.error("usage: extension.mjs TIBI_BINARY [CHROME]"); process.exit(2); }
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const work = mkdtempSync(join(tmpdir(), "tibi-ext-"));
const procs = [];
const cleanup = () => { for (const p of procs) try { p.kill("SIGKILL"); } catch {} ; try { rmSync(work, { recursive: true, force: true }); } catch {} };
const fail = (m) => { console.error("FAIL:", m); cleanup(); process.exit(1); };
setTimeout(() => fail("timed out"), 120000).unref();

const ext = resolve("extensions/chrome");

// --- Chrome over a DevTools pipe (fd 3 → Chrome, fd 4 ← Chrome) ---
const chrome = spawn(chromePath, ["--headless=new", "--no-sandbox", "--disable-gpu", "--remote-debugging-pipe",
  "--enable-unsafe-extension-debugging", `--user-data-dir=${join(work, "profile")}`, "--lang=en-US", "--window-size=900,700", "about:blank"],
  { stdio: ["ignore", "ignore", "ignore", "pipe", "pipe"], env: { ...process.env, LANG: "en_US.UTF-8", LANGUAGE: "en" } });
procs.push(chrome);
let seq = 0, buf = "";
const pending = new Map();
chrome.stdio[4].on("data", (d) => {
  buf += d.toString();
  let i;
  while ((i = buf.indexOf("\0")) >= 0) {
    const msg = JSON.parse(buf.slice(0, i));
    buf = buf.slice(i + 1);
    if (msg.id && pending.has(msg.id)) { pending.get(msg.id)(msg); pending.delete(msg.id); }
  }
});
const send = (method, params = {}, sessionId) => new Promise((res, rej) => {
  const id = ++seq;
  pending.set(id, (m) => (m.error ? rej(new Error(method + ": " + JSON.stringify(m.error))) : res(m.result)));
  chrome.stdio[3].write(JSON.stringify({ id, method, params, sessionId }) + "\0");
});
const evalIn = async (sessionId, expr) => {
  const r = await send("Runtime.evaluate", { expression: expr, awaitPromise: true, returnByValue: true }, sessionId);
  if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description || r.exceptionDetails.text);
  return r.result.value;
};
const waitIn = async (sessionId, expr, what, ms = 15000) => {
  for (let t = 0; t < ms; t += 100) {
    const v = await evalIn(sessionId, expr).catch(() => null);
    if (v) return v;
    await sleep(100);
  }
  throw new Error("timed out waiting for " + what);
};
// A new tab at url, with a session attached to it.
const openTab = async (url) => {
  const { targetId } = await send("Target.createTarget", { url: "about:blank" });
  const { sessionId } = await send("Target.attachToTarget", { targetId, flatten: true });
  await send("Page.enable", {}, sessionId);
  await send("Page.navigate", { url }, sessionId);
  await waitIn(sessionId, `document.readyState === "complete" && location.href !== "about:blank"`, "load of " + url);
  return { targetId, sessionId };
};

// --- tibi servers ---
const port = () => 18200 + Math.floor(Math.random() * 700);
async function startTibi(name, cors) {
  const p = port();
  const env = { ...process.env, TIBI_DATA_DIR: join(work, name), TIBI_LISTEN: `127.0.0.1:${p}`, TIBI_BASE_URL: `http://127.0.0.1:${p}`, TIBI_CORS_ORIGINS: cors, TIBI_METRICS_ALLOW_LOCAL: "true" };
  const token = execFileSync(tibi, ["admin", "token", "create", "--name", "chrome", "--scopes", "upload", "--config", ""], { env }).toString().trim();
  const srv = spawn(tibi, ["serve", "--config", ""], { env, stdio: "ignore" });
  procs.push(srv);
  for (let i = 0; i < 40; i++) {
    try { if ((await fetch(env.TIBI_BASE_URL + "/healthz")).ok) break; } catch {}
    await sleep(250);
  }
  return { base: env.TIBI_BASE_URL, token };
}

// --- a web page on another origin, with an image and text ---
// A 320×200 PNG (a sky gradient with a sun), so thumbnails can be made.
const png = (() => {
  const w = 320, h = 200, raw = Buffer.alloc((w * 3 + 1) * h);
  for (let y = 0; y < h; y++) for (let x = 0; x < w; x++) {
    const o = y * (w * 3 + 1) + 1 + x * 3, sun = (x - 230) ** 2 + (y - 70) ** 2 < 900;
    raw[o] = sun ? 255 : 40 + y; raw[o + 1] = sun ? 200 : 90 + y / 2; raw[o + 2] = sun ? 60 : 200 - y / 3;
  }
  const chunk = (t, d) => {
    const len = Buffer.alloc(4), crc = Buffer.alloc(4), td = Buffer.concat([Buffer.from(t), d]);
    len.writeUInt32BE(d.length); crc.writeUInt32BE(crc32(td));
    return Buffer.concat([len, td, crc]);
  };
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(w); ihdr.writeUInt32BE(h, 4); ihdr[8] = 8; ihdr[9] = 2;
  return Buffer.concat([Buffer.from("\x89PNG\r\n\x1a\n", "latin1"), chunk("IHDR", ihdr), chunk("IDAT", deflateSync(raw)), chunk("IEND", Buffer.alloc(0))]);
})();
const site = createServer((req, res) => {
  if (req.url === "/sky.png") { res.writeHead(200, { "Content-Type": "image/png" }); return res.end(png); }
  res.writeHead(200, { "Content-Type": "text/html; charset=utf-8" });
  res.end(`<!doctype html><title>Test page</title><body style="background:#2266aa"><h1>Hello</h1><p id="p">first line<br>second line</p><img id="img" src="/sky.png"></body>`);
});
await new Promise((r) => site.listen(0, "127.0.0.1", r));
const siteURL = `http://localhost:${site.address().port}`;

try {
  const { id } = await send("Extensions.loadUnpacked", { path: ext });
  const origin = "chrome-extension://" + id;
  console.log("extension loaded:", origin);
  const good = await startTibi("good", origin);
  const other = await startTibi("other", "chrome-extension://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa");

  // 1. Options page: a server without our origin → CORS help naming it.
  const opt = await openTab(origin + "/options.html");
  const save = (server, token) => evalIn(opt.sessionId, `(() => {
    document.getElementById("server").value = ${JSON.stringify(server)};
    document.getElementById("token").value = ${JSON.stringify(token)};
    document.getElementById("f").requestSubmit(); })()`);
  await save(other.base, other.token);
  const help = await waitIn(opt.sessionId, `!document.getElementById("cors").hidden && document.getElementById("cors").textContent`, "CORS help");
  if (!help.includes(origin)) fail("CORS help without the origin: " + help);
  console.log("OK: server without the extension's origin → CORS help shown");
  // ... and with it: connected.
  await save(good.base, good.token);
  const st = await waitIn(opt.sessionId, `(() => { const s = document.getElementById("status"); return s.className.includes("ok") && s.textContent; })()`, "connected");
  console.log("OK: options →", st);

  // 25 older text items, so the popup has more than one page (step 6).
  for (let i = 0; i < 25; i++) {
    const r = await fetch(good.base + "/api/v1/items?filename=note" + i + ".txt", { method: "POST", headers: { Authorization: "Bearer " + good.token }, body: "note " + i + "\nsecond line" });
    if (r.status !== 201) fail("paste: " + r.status);
  }

  // The service worker, where the menu handler runs.
  const sw = (await send("Target.getTargets")).targetInfos.find((t) => t.type === "service_worker" && t.url.startsWith(origin));
  if (!sw) fail("no service worker");
  const { sessionId: swS } = await send("Target.attachToTarget", { targetId: sw.targetId, flatten: true });
  const page = await openTab(siteURL + "/");
  await send("Target.activateTarget", { targetId: page.targetId });
  await send("Browser.grantPermissions", { origin: siteURL, permissions: ["clipboardReadWrite", "clipboardSanitizedWrite"] });
  const tabTarget = (await send("Target.getTargets", { filter: [{ type: "tab" }] })).targetInfos.find((t) => t.url.startsWith(siteURL));
  if (!tabTarget) fail("no tab target for the page");
  const grant = () => send("Extensions.triggerAction", { id, targetId: tabTarget.targetId });
  const run = async (info) => {
    await grant();
    // The tab as the menu would pass it (title and URL visible via activeTab).
    const tab = JSON.stringify(await evalIn(swS, `chrome.tabs.query({ active: true }).then((t) => t.find((x) => x.url && x.url.startsWith("${siteURL}")))`));
    const r = await evalIn(swS, `self.tibiHandle(${JSON.stringify(info)}, ${tab})`);
    if (!r || r.error) fail(JSON.stringify(info.menuItemId) + ": " + JSON.stringify(r));
    // Read the clipboard back from the page.
    await send("Page.bringToFront", {}, page.sessionId);
    const copied = await evalIn(page.sessionId, `(async () => { window.focus(); return navigator.clipboard.readText(); })()`);
    if (copied !== r.url) fail("clipboard holds " + JSON.stringify(copied) + ", not " + r.url);
    if (!r.copied) fail("handler says the copy failed");
    // The in-page toast shows the link (desktop notifications may be muted).
    const t = await evalIn(page.sessionId, `(() => { const r = document.getElementById("tibi-toast")?.shadowRoot; const a = r && r.querySelector("a");
      return r && { href: a && a.href, title: r.querySelector(".head span").textContent }; })()`);
    if (!r.toasted || !t || t.href !== r.url || t.title !== "Link copied") fail("toast: " + JSON.stringify(t) + " for " + r.url);
    return r;
  };
  const item = async (id) => (await fetch(good.base + "/api/v1/items/" + id, { headers: { Authorization: "Bearer " + good.token } })).json();
  const raw = async (u) => Buffer.from(await (await fetch(u)).arrayBuffer());

  // 2. Right-click image → upload, link in clipboard.
  const img = await run({ menuItemId: "image", srcUrl: siteURL + "/sky.png", pageUrl: siteURL + "/" });
  const im = await item(img.id);
  if (im.kind !== "image" || im.filename !== "sky.png" || im.source !== "chrome" || !(await raw(im.raw_url)).equals(png)) fail("image item: " + JSON.stringify(im));
  console.log("OK: image →", img.url, "(copied; kind image, sky.png, source chrome)");
  if (process.env.SHOT_DIR) { // the toast, for a human look
    const { data } = await send("Page.captureScreenshot", { format: "png" }, page.sessionId);
    writeFileSync(join(process.env.SHOT_DIR, "ext-toast.png"), Buffer.from(data, "base64"));
  }

  // 3. Selected text → paste (line breaks kept).
  await evalIn(page.sessionId, `(() => { const r = document.createRange(); r.selectNodeContents(document.getElementById("p")); getSelection().removeAllRanges(); getSelection().addRange(r); })()`);
  const txt = await run({ menuItemId: "selection", selectionText: "first line second line", pageUrl: siteURL + "/" });
  const tx = await item(txt.id);
  const body = (await raw(tx.raw_url)).toString();
  if (tx.kind !== "text" || body !== "first line\nsecond line" || tx.title !== "Test page") fail("text item: " + JSON.stringify(tx) + " " + JSON.stringify(body));
  console.log("OK: selection →", txt.url, "(copied; line breaks kept, titled with the page)");

  // 4. Screenshot of the visible tab (the toast from step 3 is removed first).
  const shot = await run({ menuItemId: "screenshot", pageUrl: siteURL + "/" });
  const sh = await item(shot.id);
  if (sh.kind !== "image" || sh.mime !== "image/png" || !(sh.width > 100) || !/^screenshot-.*\.png$/.test(sh.filename)) fail("screenshot item: " + JSON.stringify(sh));
  // Where the toast sits (bottom right) the page's blue must show.
  const px = await evalIn(swS, `(async () => {
    const b = await createImageBitmap(await (await fetch("data:image/png;base64,${(await raw(sh.raw_url)).toString("base64")}")).blob());
    const c = new OffscreenCanvas(b.width, b.height).getContext("2d");
    c.drawImage(b, 0, 0);
    return [...c.getImageData(b.width - 60, b.height - 40, 1, 1).data];
  })()`);
  if (Math.abs(px[0] - 0x22) > 8 || Math.abs(px[1] - 0x66) > 8 || Math.abs(px[2] - 0xaa) > 8) fail("screenshot shows the toast: pixel " + px);
  console.log(`OK: screenshot → ${shot.url} (${sh.width}x${sh.height} PNG, no toast in it)`);

  // 4b. A failed upload shows the error in the toast.
  await grant();
  const tabNow = JSON.stringify(await evalIn(swS, `chrome.tabs.query({ active: true }).then((t) => t.find((x) => x.url && x.url.startsWith("${siteURL}")))`));
  const bad = await evalIn(swS, `self.tibiHandle({ menuItemId: "image", srcUrl: "http://127.0.0.1:9/missing.png", pageUrl: "${siteURL}/" }, ${tabNow})`);
  const et = await evalIn(page.sessionId, `(() => { const r = document.getElementById("tibi-toast")?.shadowRoot;
    return r && { title: r.querySelector(".head span").textContent, msg: r.querySelector(".msg")?.textContent, link: !!r.querySelector("a") }; })()`);
  if (!bad.error || !et || et.title !== "Upload failed" || !et.msg || et.link) fail("error toast: " + JSON.stringify({ bad, et }));
  console.log("OK: failed upload → error toast:", et.msg);

  // 5. A large file goes through tus (cross-origin PATCH/HEAD, exposed headers).
  const big = await evalIn(swS, `(async () => {
    const b = new Uint8Array(21 << 20);
    for (let i = 0; i < b.length; i += 4096) b[i] = (i / 4096) & 255;
    const c = await chrome.storage.local.get(["server", "token"]);
    const it = await new self.tibiClient(c.server, c.token).upload(new Blob([b]), { filename: "big.bin", source: "chrome" });
    const h = await crypto.subtle.digest("SHA-256", b);
    return { id: it.id, size: it.size, sha: [...new Uint8Array(h)].map((x) => x.toString(16).padStart(2, "0")).join("") };
  })()`);
  const bigRaw = await raw((await item(big.id)).raw_url);
  if (big.size !== 21 << 20 || createHash("sha256").update(bigRaw).digest("hex") !== big.sha) fail("tus upload: " + JSON.stringify(big));
  const ups = (await (await fetch(good.base + "/metrics")).text()).match(/^tibi_resumable_received_bytes_total (\d+)/m);
  if (!ups || Number(ups[1]) < 21 << 20) fail("big upload did not go through tus: " + (ups && ups[1]));
  console.log("OK: 21 MiB upload via tus, SHA-256 matches");

  // 6. Popup: cards, 20 per page, more on scrolling to the end (the 25
  // notes made at the start give two pages).
  const total = (await (await fetch(good.base + "/api/v1/items?limit=100", { headers: { Authorization: "Bearer " + good.token } })).json()).items.length;
  const pop = await openTab(origin + "/popup.html");
  const cards = `document.querySelectorAll("#list .card").length`;
  const first = await waitIn(pop.sessionId, `${cards} >= 20 && ${cards}`, "popup list");
  if (first !== 20) fail("first page has " + first + " cards");
  await evalIn(pop.sessionId, `document.getElementById("more").scrollIntoView()`);
  const all = await waitIn(pop.sessionId, `${cards} === ${total} && document.getElementById("more").hidden && ${cards}`, "second page");
  const links = await evalIn(pop.sessionId, `[...document.querySelectorAll("#list .card a.pv")].map((a) => a.href)`);
  for (const u of [img.url, txt.url, shot.url]) if (!links.includes(u)) fail("popup misses " + u + ": " + links);
  // A text card shows the start of the text; an image card its thumbnail
  // (made on request); all cards are the same size.
  const card = (u) => `[...document.querySelectorAll("#list .card")].find((c) => c.querySelector("a.pv").href === ${JSON.stringify(u)})`;
  const text = await evalIn(pop.sessionId, `${card(txt.url)}.querySelector("pre.text")?.textContent`);
  if (text !== "first line\nsecond line") fail("text card shows " + JSON.stringify(text));
  for (const u of [shot.url, img.url]) {
    await evalIn(pop.sessionId, `${card(u)}.scrollIntoView()`);
    await waitIn(pop.sessionId, `(() => { const i = ${card(u)}.querySelector("img"); return i && i.complete && i.naturalWidth > 0; })()`, "thumbnail of " + u);
  }
  const sizes = await evalIn(pop.sessionId, `[...new Set([...document.querySelectorAll("#list .card")].map((c) => c.offsetWidth + "x" + c.offsetHeight))]`);
  if (sizes.length !== 1) fail("cards differ in size: " + sizes);
  console.log(`OK: popup shows ${all} cards (20 + ${all - 20} on scrolling), text excerpt and thumbnail, all ${sizes[0]}`);
  await evalIn(pop.sessionId, `scrollTo(0, 0)`);
  if (process.env.SHOT_DIR) { // screenshots for a human look
    for (const [name, t] of [["popup", pop], ["options", opt]]) {
      await send("Emulation.setDeviceMetricsOverride", { width: name === "popup" ? 404 : 700, height: name === "popup" ? 560 : 420, deviceScaleFactor: 1, mobile: false }, t.sessionId);
      const { data } = await send("Page.captureScreenshot", { format: "png" }, t.sessionId);
      writeFileSync(join(process.env.SHOT_DIR, "ext-" + name + ".png"), Buffer.from(data, "base64"));
    }
  }
  cleanup();
  site.close();
  process.exit(0);
} catch (e) {
  fail(e.stack || e.message);
}
