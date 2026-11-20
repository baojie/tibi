// @ts-check
import { Client } from "./lib/api.js";

const $ = (id) => /** @type {any} */ (document.getElementById(id));
const msg = (k, ...s) => chrome.i18n.getMessage(k, s);
document.querySelectorAll("[data-i18n]").forEach((el) => { el.textContent = msg(el.getAttribute("data-i18n") || ""); });
document.title = msg("optionsTitle");
const origin = location.origin; // chrome-extension://<id>
$("origin").textContent = origin;

function show(text, ok) {
  const s = $("status");
  s.textContent = text;
  s.className = "status " + (ok ? "ok" : "error");
  s.hidden = false;
}

chrome.storage.local.get(["server", "token"]).then((c) => {
  $("server").value = c.server || "";
  $("token").value = c.token || "";
});

$("f").addEventListener("submit", async (/** @type {Event} */ e) => {
  e.preventDefault();
  const server = $("server").value.trim().replace(/\/+$/, "");
  const token = $("token").value.trim();
  await chrome.storage.local.set({ server, token });
  try {
    const me = await new Client(server, token).whoami();
    show(msg("connectedAs", me.name || "?"), true);
    $("cors").hidden = true;
  } catch (err) {
    // A CORS refusal surfaces as a TypeError ("Failed to fetch").
    const cors = err instanceof TypeError;
    show(msg("connectFailed", cors ? "CORS / network" : err.message), false);
    $("cors").hidden = !cors;
  }
});

const all = { origins: ["<all_urls>"] };
chrome.permissions.contains(all).then((has) => { $("allsites").checked = has; });
$("allsites").addEventListener("change", async () => {
  const box = $("allsites");
  box.checked = box.checked ? await chrome.permissions.request(all) : !(await chrome.permissions.remove(all));
});
