// @ts-check
// Service worker: context menus (image → upload, selection → paste, page →
// screenshot), clipboard via an offscreen document, and the result shown
// twice: an in-page toast with the link (seen even when the desktop is in
// do-not-disturb) and a desktop notification.
import { Client } from "./lib/api.js";

const msg = (k, ...s) => chrome.i18n.getMessage(k, s);

chrome.runtime.onInstalled.addListener(() => {
  chrome.contextMenus.removeAll(() => {
    chrome.contextMenus.create({ id: "image", title: msg("menuImage"), contexts: ["image"] });
    chrome.contextMenus.create({ id: "selection", title: msg("menuSelection"), contexts: ["selection"] });
    chrome.contextMenus.create({ id: "screenshot", title: msg("menuScreenshot"), contexts: ["page", "frame", "link"] });
  });
  chrome.storage.local.get(["server", "token"]).then((c) => {
    if (!c.server || !c.token) chrome.runtime.openOptionsPage();
  });
});

async function client() {
  const c = await chrome.storage.local.get(["server", "token"]);
  if (!c.server || !c.token) return null;
  return new Client(c.server, c.token);
}

function notify(title, message) {
  return chrome.notifications.create({ type: "basic", iconUrl: "icons/icon128.png", title, message }).catch(() => {});
}

// A toast in the page's top frame: title, the link (clickable) and a Copy
// button, or an error. Runs in the page (activeTab, granted by the menu
// click); fails quietly where scripts cannot run (chrome://, the web store).
// Resolves to whether it was shown.
async function toast(tabId, t) {
  if (tabId === undefined || tabId < 0) return false;
  try {
    const [res] = await chrome.scripting.executeScript({ target: { tabId }, func: showToast, args: [t] });
    return !!(res && res.result);
  } catch {
    return false;
  }
}

/**
 * Injected into the page, so self-contained. Shadow DOM keeps the page's CSS
 * out; a constructed stylesheet is not subject to the page's style-src CSP.
 * @param {{title: string, url?: string, error?: string, copy: string, copied: string, close: string}} t
 */
function showToast(t) {
  document.getElementById("tibi-toast")?.remove();
  const host = document.createElement("div");
  host.id = "tibi-toast";
  const root = host.attachShadow({ mode: "open" });
  const css = new CSSStyleSheet();
  css.replaceSync(`
    :host { all: initial; position: fixed; right: 16px; bottom: 16px; z-index: 2147483647; }
    .box { --fg: #1d1d1f; --muted: #6e6e73; --bg: #fff; --accent: #8a3b12; --line: #e5e5ea; --err: #b3261e;
      font: 14px/1.45 system-ui, sans-serif; color: var(--fg); background: var(--bg); border: 1px solid var(--line);
      border-left: 4px solid var(--accent); border-radius: 8px; box-shadow: 0 6px 24px rgba(0,0,0,.18);
      padding: 10px 12px; width: min(360px, calc(100vw - 32px)); box-sizing: border-box; }
    .box.error { border-left-color: var(--err); }
    @media (prefers-color-scheme: dark) { .box { --fg: #f2f2f7; --muted: #a1a1a6; --bg: #1c1c1e; --accent: #e8925f; --line: #3a3a3c; --err: #f2b8b5; } }
    .head { display: flex; align-items: center; gap: 8px; font-weight: 600; }
    .head span { flex: 1; }
    .x { all: unset; cursor: pointer; color: var(--muted); font-size: 18px; line-height: 1; padding: 0 2px; }
    .row { display: flex; align-items: center; gap: 8px; margin-top: 6px; }
    a { flex: 1; min-width: 0; color: var(--accent); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
    .copy { all: unset; cursor: pointer; font-size: 13px; border: 1px solid var(--accent); color: var(--accent); border-radius: 6px; padding: 2px 10px; }
    .msg { margin-top: 6px; color: var(--err); overflow-wrap: anywhere; }`);
  root.adoptedStyleSheets = [css];
  const el = (tag, cls, text) => { const e = document.createElement(tag); if (cls) e.className = cls; if (text) e.textContent = text; return e; };
  const box = el("div", t.error ? "box error" : "box");
  box.setAttribute("role", "status");
  const head = el("div", "head");
  const x = el("button", "x", "×");
  x.title = t.close;
  x.addEventListener("click", () => host.remove());
  head.append(el("span", "", t.title), x);
  box.append(head);
  if (t.url) {
    const row = el("div", "row");
    const a = el("a", "", t.url);
    a.href = t.url;
    a.target = "_blank";
    a.rel = "noopener";
    const b = el("button", "copy", t.copy);
    b.addEventListener("click", () => navigator.clipboard.writeText(t.url || "").then(() => { b.textContent = t.copied; }, () => {}));
    row.append(a, b);
    box.append(row);
  }
  if (t.error) box.append(el("div", "msg", t.error));
  root.append(box);
  (document.body || document.documentElement).append(host);
  // Gone after 10 s unless the pointer is on it.
  let timer = setTimeout(() => host.remove(), 10000);
  host.addEventListener("mouseenter", () => clearTimeout(timer));
  host.addEventListener("mouseleave", () => { timer = setTimeout(() => host.remove(), 4000); });
  return true;
}

// Toast + desktop notification for one result.
async function report(tabId, t) {
  const labels = { copy: msg("copy"), copied: msg("copiedButton"), close: msg("close") };
  const shown = await toast(tabId, { ...t, ...labels });
  notify(t.title, t.url || t.error || "");
  return shown;
}

// Clipboard: service workers have no DOM, so an offscreen document copies.
async function copy(text) {
  const url = chrome.runtime.getURL("offscreen.html");
  const have = await chrome.runtime.getContexts({ contextTypes: ["OFFSCREEN_DOCUMENT"], documentUrls: [url] });
  if (!have.length) {
    await chrome.offscreen.createDocument({ url, reasons: ["CLIPBOARD"], justification: "Copy the uploaded link" });
  }
  const ok = await chrome.runtime.sendMessage({ target: "offscreen", type: "copy", text });
  return ok;
}

function extOf(type) {
  const m = /^image\/(png|jpeg|gif|webp|avif|bmp|svg\+xml|heic|heif)/.exec(type || "");
  if (!m) return "";
  return { jpeg: "jpg", "svg+xml": "svg" }[m[1]] || m[1];
}

function nameFromURL(src, type) {
  try {
    const u = new URL(src);
    if (u.protocol === "http:" || u.protocol === "https:") {
      const last = decodeURIComponent(u.pathname.split("/").pop() || "");
      if (/\.[a-z0-9]{2,5}$/i.test(last)) return last;
      if (last) return last + (extOf(type) ? "." + extOf(type) : "");
    }
  } catch {}
  return "image." + (extOf(type) || "png");
}

// Image bytes: the service worker can fetch any URL once the user granted
// host access; otherwise only same-site/CORS-enabled images. Last resort:
// read it inside the page (works for same-origin and blob: images).
async function fetchImage(src, tabId) {
  try {
    const r = await fetch(src, { credentials: "include" });
    if (r.ok) return await r.blob();
  } catch {}
  if (tabId !== undefined) {
    try {
      const [res] = await chrome.scripting.executeScript({
        target: { tabId },
        func: async (u) => {
          const r = await fetch(u);
          const b = await r.blob();
          const buf = new Uint8Array(await b.arrayBuffer());
          let s = "";
          for (let i = 0; i < buf.length; i += 0x8000) s += String.fromCharCode.apply(null, Array.from(buf.subarray(i, i + 0x8000)));
          return { type: b.type, data: btoa(s) };
        },
        args: [src],
      });
      if (res && res.result) {
        const bin = atob(res.result.data), b = new Uint8Array(bin.length);
        for (let i = 0; i < bin.length; i++) b[i] = bin.charCodeAt(i);
        return new Blob([b], { type: res.result.type });
      }
    } catch {}
  }
  const err = new Error(msg("needImageAccess"));
  err.name = "NeedAccess";
  throw err;
}

// Full selection text with line breaks (info.selectionText flattens them).
async function selectionText(info, tabId) {
  if (tabId !== undefined) {
    try {
      const [res] = await chrome.scripting.executeScript({
        target: { tabId, frameIds: info.frameId ? [info.frameId] : undefined },
        func: () => String(getSelection()),
      });
      if (res && res.result && res.result.trim()) return res.result;
    } catch {}
  }
  return info.selectionText || "";
}

async function dataURLBlob(u) {
  return (await fetch(u)).blob();
}

/**
 * Runs one menu action; exported on self for tests.
 * @param {{menuItemId: string|number, srcUrl?: string, selectionText?: string, pageUrl?: string, frameId?: number}} info
 * @param {chrome.tabs.Tab} [tab]
 */
async function handle(info, tab) {
  const cl = await client();
  if (!cl) {
    await report(tab && tab.id, { title: "tibi", error: msg("notConfigured") });
    chrome.runtime.openOptionsPage();
    return { error: "not configured" };
  }
  const pageURL = info.pageUrl || (tab && tab.url) || "";
  const meta = pageURL && /^https?:/.test(pageURL) ? { page_url: pageURL } : undefined;
  const base = { source: "chrome", meta };
  try {
    let item;
    if (info.menuItemId === "image") {
      const blob = await fetchImage(info.srcUrl || "", tab && tab.id);
      item = await cl.upload(blob, { ...base, filename: nameFromURL(info.srcUrl || "", blob.type) });
    } else if (info.menuItemId === "selection") {
      const text = await selectionText(info, tab && tab.id);
      const title = tab && tab.title ? tab.title.slice(0, 200) : "";
      item = await cl.upload(new Blob([text], { type: "text/plain; charset=utf-8" }), { ...base, kind: "text", title });
    } else if (info.menuItemId === "screenshot") {
      // An earlier toast must not end up in the screenshot.
      if (tab && tab.id !== undefined) {
        await chrome.scripting.executeScript({ target: { tabId: tab.id }, func: () => {
          const h = document.getElementById("tibi-toast");
          if (!h) return;
          h.remove();
          return new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
        } }).catch(() => {});
      }
      const shot = await chrome.tabs.captureVisibleTab(tab ? tab.windowId : chrome.windows.WINDOW_ID_CURRENT, { format: "png" });
      const stamp = new Date().toISOString().slice(0, 19).replace(/[:T]/g, "-");
      item = await cl.upload(await dataURLBlob(shot), { ...base, filename: "screenshot-" + stamp + ".png" });
    } else {
      return { error: "unknown action" };
    }
    let copied = false;
    try { copied = !!(await copy(item.url)); } catch {}
    const toasted = await report(tab && tab.id, { title: msg(copied ? "copied" : "uploadedNotCopied"), url: item.url });
    return { url: item.url, id: item.id, kind: item.kind, copied, toasted };
  } catch (e) {
    const m = e instanceof Error ? e.message : String(e);
    await report(tab && tab.id, { title: msg("failed"), error: m });
    return { error: m };
  }
}

chrome.contextMenus.onClicked.addListener((info, tab) => {
  // Most images live on other sites and send no CORS headers: ask for
  // access to that site now, while the click still counts as a user
  // gesture (no prompt if already granted, e.g. via the options page).
  let ask = Promise.resolve(false);
  if (info.menuItemId === "image" && /^https?:/.test(info.srcUrl || "")) {
    ask = chrome.permissions.request({ origins: [new URL(info.srcUrl || "").origin + "/*"] }).catch(() => false);
  }
  ask.finally(() => handle(info, tab));
});
// Test hooks (the browser test drives these through DevTools).
// @ts-ignore
self.tibiHandle = handle;
// @ts-ignore
self.tibiClient = Client;
