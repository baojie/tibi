// End-to-end check in a real browser (no dependencies; Node ≥ 22):
// open the web upload page, connect a token, paste an image, and verify
// that the returned link serves exactly that image. Also checks the page
// renders in Chinese for Accept-Language: zh-CN.
//
//   node scripts/e2e/paste-upload.mjs <tibi base URL> <token> [chrome path]
import { spawn } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const [base, token, chromePath = "google-chrome"] = process.argv.slice(2);
if (!base || !token) {
  console.error("usage: paste-upload.mjs BASE_URL TOKEN [CHROME]");
  process.exit(2);
}
const profile = mkdtempSync(join(tmpdir(), "tibi-e2e-"));
const port = 9300 + Math.floor(Math.random() * 500);
const chrome = spawn(chromePath, [
  "--headless=new", "--no-sandbox", "--disable-gpu", `--remote-debugging-port=${port}`,
  `--user-data-dir=${profile}`, "--lang=zh-CN", "about:blank",
], { stdio: "ignore" });
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const fail = (msg) => { console.error("FAIL:", msg); cleanup(1); };
function cleanup(code) {
  chrome.kill("SIGKILL");
  try { rmSync(profile, { recursive: true, force: true }); } catch {}
  process.exit(code);
}
setTimeout(() => fail("timed out"), 60000).unref();

// Find the page target.
let target;
for (let i = 0; i < 50 && !target; i++) {
  await sleep(200);
  try {
    const list = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
    target = list.find((t) => t.type === "page");
  } catch {}
}
if (!target) fail("chrome did not start");

const ws = new WebSocket(target.webSocketDebuggerUrl);
await new Promise((r, j) => { ws.onopen = r; ws.onerror = j; });
let seq = 0;
const pending = new Map();
const events = [];
ws.onmessage = (m) => {
  const msg = JSON.parse(m.data);
  if (msg.id && pending.has(msg.id)) { pending.get(msg.id)(msg); pending.delete(msg.id); }
  else if (msg.method) events.push(msg);
};
const send = (method, params = {}) => new Promise((resolve, reject) => {
  const id = ++seq;
  pending.set(id, (msg) => (msg.error ? reject(new Error(JSON.stringify(msg.error))) : resolve(msg.result)));
  ws.send(JSON.stringify({ id, method, params }));
});
const evaluate = async (expr) => {
  const r = await send("Runtime.evaluate", { expression: expr, awaitPromise: true, returnByValue: true });
  if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description || "evaluation failed");
  return r.result.value;
};
async function waitFor(expr, what) {
  for (let i = 0; i < 100; i++) {
    const v = await evaluate(expr).catch(() => null);
    if (v) return v;
    await sleep(100);
  }
  fail("waiting for " + what);
}

await send("Page.enable");
await send("Runtime.enable");
await send("Network.enable");
await send("Network.setExtraHTTPHeaders", { headers: { "Accept-Language": "zh-CN,zh;q=0.9" } });

// 1. The page asks for a token, in Chinese.
await send("Page.navigate", { url: base + "/upload" });
await waitFor(`document.readyState === "complete" && !document.getElementById("token-setup").hidden`, "token form");
const lang = await evaluate(`document.documentElement.lang + "|" + document.querySelector("#token-setup h2").textContent`);
if (!lang.startsWith("zh-CN|") || !lang.includes("连接")) fail("page not in Chinese: " + lang);

// 2. Connect the token through the form.
await evaluate(`(() => {
  const f = document.getElementById("token-setup");
  f.elements.token.value = ${JSON.stringify(token)};
  f.requestSubmit();
})()`);
await waitFor(`!document.getElementById("uploader").hidden`, "uploader after token");

// 3. Paste an image (what a screenshot paste delivers to the page).
const url = await evaluate(`(async () => {
  const c = document.createElement("canvas");
  c.width = 37; c.height = 23;
  const g = c.getContext("2d");
  g.fillStyle = "#c0392b"; g.fillRect(0, 0, 37, 23);
  g.fillStyle = "#2980b9"; g.fillRect(10, 5, 10, 10);
  const blob = await new Promise((r) => c.toBlob(r, "image/png"));
  window.__pasted = new Uint8Array(await blob.arrayBuffer());
  const dt = new DataTransfer();
  dt.items.add(new File([blob], "image.png", { type: "image/png" }));
  document.body.dispatchEvent(new ClipboardEvent("paste", { clipboardData: dt, bubbles: true, cancelable: true }));
  return "pasted";
})()`);
if (url !== "pasted") fail("paste");
const link = await waitFor(`(document.querySelector("#results .result-link") || {}).href`, "result link");
console.log("link:", link);

// 4. The link is an image page whose raw bytes equal what was pasted.
const ok = await evaluate(`(async () => {
  const meta = await (await fetch("/api/v1/items/" + ${JSON.stringify(link)}.split("/").pop(),
    { headers: { Authorization: "Bearer " + localStorage.getItem("tibi.token") } })).json();
  const raw = new Uint8Array(await (await fetch(meta.raw_url)).arrayBuffer());
  const same = raw.length === window.__pasted.length && raw.every((b, i) => b === window.__pasted[i]);
  return meta.kind + "|" + meta.mime + "|" + meta.width + "x" + meta.height + "|" + same;
})()`);
if (ok !== "image|image/png|37x23|true") fail("uploaded item mismatch: " + ok);

console.log("OK: pasted image →", link, "(" + ok + ")");
cleanup(0);
