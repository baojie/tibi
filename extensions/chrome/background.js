// @ts-check
// Service worker: context menus (image → upload, selection → paste, page →
// screenshot), clipboard via an offscreen document, notifications.
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
    notify("tibi", msg("notConfigured"));
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
      const shot = await chrome.tabs.captureVisibleTab(tab ? tab.windowId : chrome.windows.WINDOW_ID_CURRENT, { format: "png" });
      const stamp = new Date().toISOString().slice(0, 19).replace(/[:T]/g, "-");
      item = await cl.upload(await dataURLBlob(shot), { ...base, filename: "screenshot-" + stamp + ".png" });
    } else {
      return { error: "unknown action" };
    }
    await copy(item.url);
    notify(msg("copied"), item.url);
    return { url: item.url, id: item.id, kind: item.kind };
  } catch (e) {
    const m = e instanceof Error ? e.message : String(e);
    notify(msg("failed"), m);
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
