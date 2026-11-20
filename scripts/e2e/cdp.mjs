// Minimal Chrome DevTools Protocol driver (Node ≥ 22, no dependencies).
import { spawn } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

export const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

export async function launch(chromePath = "google-chrome", { lang = "en-US" } = {}) {
  const profile = mkdtempSync(join(tmpdir(), "tibi-e2e-"));
  const port = 9300 + Math.floor(Math.random() * 600);
  const chrome = spawn(chromePath, ["--headless=new", "--no-sandbox", "--disable-gpu", `--remote-debugging-port=${port}`,
    `--user-data-dir=${profile}`, `--lang=${lang}`, "about:blank"], { stdio: "ignore" });
  const close = () => {
    chrome.kill("SIGKILL");
    try { rmSync(profile, { recursive: true, force: true }); } catch {}
  };
  let target;
  for (let i = 0; i < 50 && !target; i++) {
    await sleep(200);
    try { target = (await (await fetch(`http://127.0.0.1:${port}/json/list`)).json()).find((t) => t.type === "page"); } catch {}
  }
  if (!target) { close(); throw new Error("chrome did not start"); }
  const ws = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((r, j) => { ws.onopen = r; ws.onerror = j; });
  let seq = 0;
  const pending = new Map();
  ws.onmessage = (m) => {
    const msg = JSON.parse(m.data);
    if (msg.id && pending.has(msg.id)) { pending.get(msg.id)(msg); pending.delete(msg.id); }
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
  const waitFor = async (expr, what, ms = 15000) => {
    for (let t = 0; t < ms; t += 100) {
      const v = await evaluate(expr).catch(() => null);
      if (v) return v;
      await sleep(100);
    }
    throw new Error("timed out waiting for " + what);
  };
  const navigate = async (url) => {
    await send("Page.navigate", { url });
    await waitFor(`document.readyState === "complete"`, "page load");
  };
  await send("Page.enable");
  await send("Runtime.enable");
  await send("Network.enable");
  return { send, evaluate, waitFor, navigate, close };
}
