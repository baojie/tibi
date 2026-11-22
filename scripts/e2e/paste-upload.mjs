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

  // 3. Paste an image as "Copy image" in a browser delivers it: the file
  // and <img src> of where it came from.
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
    dt.setData("text/html", '<meta charset="utf-8"><img src="https://example.com/pics/red.png" alt="">');
    document.body.dispatchEvent(new ClipboardEvent("paste", { clipboardData: dt, bubbles: true, cancelable: true }));
  })()`);
  const link = await b.waitFor(`(document.querySelector("#results .result-link") || {}).href`, "result link");

  // 4. The link is an image whose raw bytes equal what was pasted.
  const ok = await b.evaluate(`(async () => {
    const meta = await (await fetch("/api/v1/items/" + ${JSON.stringify(link)}.split("/").pop(),
      { headers: { Authorization: "Bearer " + localStorage.getItem("tibi.token") } })).json();
    const raw = new Uint8Array(await (await fetch(meta.raw_url)).arrayBuffer());
    const same = raw.length === window.__pasted.length && raw.every((x, i) => x === window.__pasted[i]);
    return meta.kind + "|" + meta.mime + "|" + meta.width + "x" + meta.height + "|" + same + "|" + meta.source_url;
  })()`);
  if (ok !== "image|image/png|37x23|true|https://example.com/pics/red.png") fail("uploaded item mismatch: " + ok);
  console.log("OK: pasted image →", link, "(" + ok + ")");

  // 4. Recent items: list by default; the thumbnail view shows same-size
  // cards with the image's thumbnail and the start of a text, and is kept.
  const note = await (await fetch(base + "/api/v1/items?filename=note.txt&source_url=" + encodeURIComponent("https://example.org/notes/shopping"), { method: "POST", headers: { Authorization: "Bearer " + token }, body: "Shopping list\n- milk\n- bread" })).json();
  await b.navigate(base + "/recent");
  await b.waitFor(`document.querySelectorAll("#items li").length >= 2`, "recent list");
  if (await b.evaluate(`document.getElementById("items").className`) !== "item-list") fail("recent does not start as a list");
  const gridOK = async () => {
    await b.waitFor(`document.getElementById("items").className === "item-grid" && document.querySelectorAll("#items li").length >= 2`, "grid view");
    const card = (u) => `[...document.querySelectorAll("#items li")].find((li) => li.querySelector("a.name").href === ${JSON.stringify(u)})`;
    await b.waitFor(`(() => { const i = ${card(link)}.querySelector(".pv img"); return i && i.complete && i.naturalWidth > 0; })()`, "image thumbnail in grid");
    const text = await b.evaluate(`${card(note.url)}.querySelector(".pv .text")?.textContent`);
    if (text !== "Shopping list\n- milk\n- bread") fail("text card shows " + JSON.stringify(text));
    // Where it came from, for the owner: a link to it, named by its host.
    const src = await b.evaluate(`(() => { const a = ${card(note.url)}.querySelector("a.src"); return a && a.textContent + "|" + a.href; })()`);
    if (src !== "来源: example.org|https://example.org/notes/shopping") fail("source link: " + src);
    const sizes = await b.evaluate(`[...new Set([...document.querySelectorAll("#items li")].map((li) => li.offsetWidth + "x" + li.offsetHeight))]`);
    if (sizes.length !== 1) fail("cards differ in size: " + sizes);
    return sizes[0];
  };
  await b.evaluate(`document.querySelector('#view [data-view="grid"]').click()`);
  const size = await gridOK();
  await b.navigate(base + "/recent");
  await gridOK();
  console.log("OK: recent → thumbnail view: image thumbnail and text start in same-size cards (" + size + ") with the source, kept after reload");
  if (process.env.SHOT_DIR) {
    await b.send("Emulation.setDeviceMetricsOverride", { width: 1000, height: 620, deviceScaleFactor: 1, mobile: false });
    const { data } = await b.send("Page.captureScreenshot", { format: "png" });
    (await import("node:fs")).writeFileSync(process.env.SHOT_DIR + "/recent-grid.png", Buffer.from(data, "base64"));
  }
  b.close();
  process.exit(0);
} catch (e) {
  fail(e.message);
}
