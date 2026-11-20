// End-to-end encryption in a real browser: links with #k= decrypt; missing
// or wrong keys and wrong passwords show clear errors.
//
//   node scripts/e2e/decrypt.mjs TEXT_URL PASSWORD_URL PASSWORD IMAGE_URL [chrome]
// TEXT_URL holds Go code ("package main" …), IMAGE_URL a 2×1 PNG; all URLs
// include their #k= fragment.
import { launch } from "./cdp.mjs";

const [textURL, pwURL, password, imageURL, chromePath] = process.argv.slice(2);
if (!imageURL) { console.error("usage: decrypt.mjs TEXT_URL PASSWORD_URL PASSWORD IMAGE_URL [CHROME]"); process.exit(2); }
const b = await launch(chromePath);
setTimeout(() => { console.error("FAIL: timed out"); b.close(); process.exit(1); }, 90000).unref();
const fail = (msg) => { console.error("FAIL:", msg); b.close(); process.exit(1); };
const status = () => b.evaluate(`(() => { const s = document.getElementById("e2e-status"); return s.hidden ? "" : s.textContent; })()`);
const load = async (url) => { await b.navigate("about:blank"); await b.navigate(url); };

try {
  // Pin the language: headless Chrome sends the machine's locale otherwise.
  await b.send("Network.setExtraHTTPHeaders", { headers: { "Accept-Language": "en-US" } });

  // 1. The full link decrypts and highlights.
  await load(textURL);
  const code = await b.waitFor(`(() => { const c = document.querySelector("#e2e-content code"); return c && c.innerHTML; })()`, "decrypted code");
  if (!code.includes("package") || !code.includes('class="hljs-keyword"')) fail("text not decrypted/highlighted: " + code.slice(0, 120));
  if (await status()) fail("status still shown after decryption");
  // What is on screen, not just the hidden property (CSS can override it).
  const pwShown = await b.evaluate(`getComputedStyle(document.getElementById("e2e-password")).display`);
  if (pwShown !== "none") fail("password box visible on a link without a password: display=" + pwShown);
  console.log("OK: text decrypted and highlighted");

  // 2. Missing key.
  await load(textURL.split("#")[0]);
  await b.waitFor(`document.getElementById("e2e-status").textContent.includes("missing its key")`, "missing-key message");
  console.log("OK: missing key →", await status());

  // 3. Wrong key (one character changed).
  const [u, frag] = textURL.split("#k=");
  const bad = u + "#k=" + (frag[0] === "A" ? "B" : "A") + frag.slice(1);
  await load(bad);
  await b.waitFor(`document.getElementById("e2e-status").textContent.includes("key in the link is wrong")`, "wrong-key message");
  if (await b.evaluate(`document.getElementById("e2e-content").childElementCount`)) fail("content shown with a wrong key");
  console.log("OK: wrong key →", await status());

  // 4. Password: asked for, wrong one refused, right one decrypts.
  await load(pwURL);
  await b.waitFor(`!document.getElementById("e2e-password").hidden`, "password form");
  const submit = (pw) => b.evaluate(`(() => { const f = document.getElementById("e2e-password"); f.elements.password.value = ${JSON.stringify(pw)}; f.requestSubmit(); })()`);
  await submit("not the password");
  await b.waitFor(`document.getElementById("e2e-status").textContent.includes("wrong password")`, "wrong-password message", 30000);
  console.log("OK: wrong password →", await status());
  await submit(password);
  await b.waitFor(`(() => { const c = document.querySelector("#e2e-content code"); return c && c.textContent.includes("with a password"); })()`, "password-protected text", 30000);
  console.log("OK: password-protected item decrypted");

  // 5. An image decrypts to a viewable picture with a download button.
  await load(imageURL);
  const img = await b.waitFor(`(() => { const i = document.querySelector("#e2e-content img"); return i && i.complete && i.naturalWidth ? i.naturalWidth + "x" + i.naturalHeight + "|" + i.src.slice(0, 5) : ""; })()`, "decrypted image");
  if (img !== "2x1|blob:") fail("image: " + img);
  const dl = await b.evaluate(`(() => { const a = document.querySelector("#e2e-actions a"); return a ? a.download : ""; })()`);
  if (dl !== "pixel.png") fail("download name: " + dl);
  console.log("OK: image decrypted (" + img + "), download as", dl);
  b.close();
  process.exit(0);
} catch (e) {
  fail(e.message);
}
