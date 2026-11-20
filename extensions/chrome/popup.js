// @ts-check
import { Client } from "./lib/api.js";

const $ = (id) => /** @type {any} */ (document.getElementById(id));
const msg = (k, ...s) => chrome.i18n.getMessage(k, s);
document.querySelectorAll("[data-i18n]").forEach((el) => { el.textContent = msg(el.getAttribute("data-i18n") || ""); });
$("settings").addEventListener("click", (/** @type {Event} */ e) => { e.preventDefault(); chrome.runtime.openOptionsPage(); });

function status(text) {
  $("status").textContent = text;
  $("status").className = "status error";
  $("status").hidden = false;
}

async function main() {
  const c = await chrome.storage.local.get(["server", "token"]);
  if (!c.server || !c.token) return status(msg("notConfigured"));
  $("upload").addEventListener("click", (/** @type {Event} */ e) => { e.preventDefault(); chrome.tabs.create({ url: c.server + "/upload" }); });
  let page;
  try {
    page = await new Client(c.server, c.token).recent(10);
  } catch (e) {
    return status(msg("connectFailed", e instanceof TypeError ? "CORS / network" : e.message));
  }
  const items = page.items || [];
  if (!items.length) return status(msg("empty"));
  $("status").hidden = true;
  for (const it of items) {
    const li = document.createElement("li");
    if (it.thumbnail_url) {
      const img = document.createElement("img");
      img.src = it.thumbnail_url;
      img.alt = "";
      li.appendChild(img);
    } else {
      const k = document.createElement("span");
      k.className = "kind muted";
      k.textContent = it.kind;
      li.appendChild(k);
    }
    const a = document.createElement("a");
    a.className = "t";
    a.href = it.url;
    a.target = "_blank";
    a.textContent = it.title || it.filename || it.id;
    a.title = it.url;
    li.appendChild(a);
    const b = document.createElement("button");
    b.className = "secondary";
    b.textContent = msg("copy");
    b.addEventListener("click", async () => {
      await navigator.clipboard.writeText(it.url);
      b.textContent = "✓";
    });
    li.appendChild(b);
    $("list").appendChild(li);
  }
}
main();
