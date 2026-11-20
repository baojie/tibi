// End-to-end check in a real browser: open the web upload page, connect a
// token, paste an image, and verify that the returned link serves exactly
// that image. Also checks the page renders in Chinese for zh-CN.
//
//   node scripts/e2e/paste-upload.mjs <tibi base URL> <token> [chrome path]
import { launch } from "./cdp.mjs";

const [base, token, chromePath] = process.argv.slice(2);
if (!base || !token) {
  console.error("usage: paste-upload.mjs BASE_URL TOKEN [CHROME]");
  process.exit(2);
}
const b = await launch(chromePath, { lang: "zh-CN" });
setTimeout(() => { console.error("FAIL: timed out"); b.close(); process.exit(1); }, 60000).unref();
const fail = (msg) => { console.error("FAIL:", msg); b.close(); process.exit(1); };
try {
  await b.send("Network.setExtraHTTPHeaders", { headers: { "Accept-Language": "zh-CN,zh;q=0.9" } });

  // 1. The page asks for a token, in Chinese.
  await b.navigate(base + "/upload");
  await b.waitFor(`!document.getElementById("token-setup").hidden`, "token form");
  const lang = await b.evaluate(`document.documentElement.lang + "|" + document.querySelector("#token-setup h2").textContent`);
  if (!lang.startsWith("zh-CN|") || !lang.includes("连接")) fail("page not in Chinese: " + lang);

  // 2. Connect the token through the form.
  await b.evaluate(`(() => {
    const f = document.getElementById("token-setup");
    f.elements.token.value = ${JSON.stringify(token)};
    f.requestSubmit();
  })()`);
  await b.waitFor(`!document.getElementById("uploader").hidden`, "uploader after token");

  // 3. Paste an image (what a screenshot paste delivers to the page).
  await b.evaluate(`(async () => {
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
  })()`);
  const link = await b.waitFor(`(document.querySelector("#results .result-link") || {}).href`, "result link");

  // 4. The link is an image whose raw bytes equal what was pasted.
  const ok = await b.evaluate(`(async () => {
    const meta = await (await fetch("/api/v1/items/" + ${JSON.stringify(link)}.split("/").pop(),
      { headers: { Authorization: "Bearer " + localStorage.getItem("tibi.token") } })).json();
    const raw = new Uint8Array(await (await fetch(meta.raw_url)).arrayBuffer());
    const same = raw.length === window.__pasted.length && raw.every((x, i) => x === window.__pasted[i]);
    return meta.kind + "|" + meta.mime + "|" + meta.width + "x" + meta.height + "|" + same;
  })()`);
  if (ok !== "image|image/png|37x23|true") fail("uploaded item mismatch: " + ok);
  console.log("OK: pasted image →", link, "(" + ok + ")");
  b.close();
  process.exit(0);
} catch (e) {
  fail(e.message);
}
