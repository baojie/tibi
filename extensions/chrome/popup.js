// @ts-check
import { Client } from "./lib/api.js";

const $ = (id) => /** @type {any} */ (document.getElementById(id));
const msg = (k, ...s) => chrome.i18n.getMessage(k, s);
document.querySelectorAll("[data-i18n]").forEach((el) => { el.textContent = msg(el.getAttribute("data-i18n") || ""); });
$("settings").addEventListener("click", (/** @type {Event} */ e) => { e.preventDefault(); chrome.runtime.openOptionsPage(); });

const PAGE = 20;

function status(text) {
  $("status").textContent = text;
  $("status").className = "status error";
  $("status").hidden = false;
}

/** @param {string} tag @param {string} [cls] @param {string} [text] */
function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text) e.textContent = text;
  return e;
}

/** The kind as a label, for items without a picture or text to show. */
function kindLabel(it) {
  return el("span", "kind", it.kind);
}

/** One card: a preview (thumbnail, start of the text, or the kind) and a footer. @param {import("./lib/api.gen.js").Item} it */
function card(it) {
  const c = el("div", "card");
  const pv = /** @type {HTMLAnchorElement} */ (el("a", "pv"));
  pv.href = it.url;
  pv.target = "_blank";
  pv.title = it.url;
  if (it.thumbnail_url) {
    const img = /** @type {HTMLImageElement} */ (el("img"));
    img.src = it.thumbnail_url;
    img.alt = "";
    img.loading = "lazy";
    img.addEventListener("error", () => img.replaceWith(kindLabel(it))); // e.g. none can be made
    pv.append(img);
  } else if (it.excerpt) {
    pv.append(el("pre", "text", it.excerpt));
  } else {
    pv.append(kindLabel(it));
  }
  const foot = el("div", "foot");
  foot.append(el("span", "t", it.title || it.filename || it.id));
  const b = el("button", "secondary", msg("copy"));
  b.addEventListener("click", async () => {
    await navigator.clipboard.writeText(it.url);
    b.textContent = "✓";
  });
  foot.append(b);
  c.append(pv, foot);
  return c;
}

async function main() {
  const c = await chrome.storage.local.get(["server", "token"]);
  if (!c.server || !c.token) return status(msg("notConfigured"));
  $("upload").addEventListener("click", (/** @type {Event} */ e) => { e.preventDefault(); chrome.tabs.create({ url: c.server + "/upload" }); });
  const client = new Client(c.server, c.token);
  /** @type {string|null|undefined} */
  let cursor;
  let loading = false;
  // Loads the next page; scrolling to the end (or the button) asks for more.
  const more = async () => {
    if (loading || cursor === null) return;
    loading = true;
    $("more").disabled = true;
    let page;
    try {
      page = await client.recent(PAGE, cursor);
    } catch (e) {
      loading = false;
      $("more").disabled = false;
      return status(msg("connectFailed", e instanceof TypeError ? "CORS / network" : e.message));
    }
    const items = page.items || [];
    if (cursor === undefined && !items.length) status(msg("empty"));
    else $("status").hidden = true;
    for (const it of items) $("list").append(card(it));
    cursor = page.next_cursor || null;
    $("more").hidden = cursor === null;
    $("more").disabled = false;
    loading = false;
  };
  $("more").addEventListener("click", more);
  new IntersectionObserver((es) => { if (es.some((e) => e.isIntersecting)) more(); }).observe($("more"));
  await more();
}
main();
