// Take the release screenshots described in a scenes file.
//
//   node scripts/screenshots/shot.mjs SCENES.json [chrome]
//
// A scene is either a web page ({url}) or a terminal transcript ({term}:
// a text file whose lines starting with "$ " are commands), rendered in a
// terminal-window frame. Optional: width, height (web; terminals size to
// their content), lang, wait (JS expression to wait for), run (JS to
// evaluate after load, then wait again), title (terminal window title).
import { readFileSync, writeFileSync } from "node:fs";
import { launch, sleep } from "../e2e/cdp.mjs";

const [scenesPath, chromePath = "google-chrome"] = process.argv.slice(2);
const scenes = JSON.parse(readFileSync(scenesPath, "utf8"));

const esc = (s) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");

function terminalPage(text, title) {
  const lines = text.replace(/\s+$/, "").split("\n").map((l) => {
    if (l.startsWith("$ ")) return `<span class="p">$</span> <span class="c">${esc(l.slice(2))}</span>`;
    if (l.startsWith("# ")) return `<span class="m">${esc(l)}</span>`;
    return esc(l).replace(/(https?:\/\/[^\s"]+)/g, '<span class="u">$1</span>');
  });
  return `<!doctype html><meta charset="utf-8"><style>
    html,body{margin:0;background:#e9ecf1}
    body{padding:28px;font:15px/1.55 "DejaVu Sans Mono","Noto Sans Mono CJK SC",monospace}
    .w{background:#1d2129;border-radius:10px;box-shadow:0 10px 30px rgba(0,0,0,.25);overflow:hidden}
    .t{background:#2c313c;color:#aab1bf;font:13px sans-serif;padding:9px 14px;display:flex;gap:7px;align-items:center}
    .t i{width:12px;height:12px;border-radius:50%;display:inline-block}
    .t span{margin-left:10px}
    pre{margin:0;padding:16px 20px 20px;color:#d7dae0;white-space:pre-wrap;word-break:break-all;font:inherit}
    .p{color:#7ec16e;font-weight:bold}.c{color:#fff;font-weight:bold}.u{color:#6cb6ff}.m{color:#7f8796}
  </style><div class="w"><div class="t"><i style="background:#ff5f57"></i><i style="background:#febc2e"></i><i style="background:#28c840"></i><span>${esc(title || "terminal")}</span></div><pre>${lines.join("\n")}</pre></div>`;
}

for (const s of scenes) {
  const b = await launch(chromePath, { lang: s.lang || "en-US" });
  try {
    await b.send("Network.setExtraHTTPHeaders", { headers: { "Accept-Language": s.lang || "en-US,en" } });
    const width = s.width || 1000;
    await b.send("Emulation.setScrollbarsHidden", { hidden: true });
    await b.send("Emulation.setDeviceMetricsOverride", { width, height: s.height || 700, deviceScaleFactor: 1, mobile: false });
    if (s.term) {
      await b.send("Page.setDocumentContent", {
        frameId: (await b.send("Page.getFrameTree")).frameTree.frame.id,
        html: terminalPage(readFileSync(s.term, "utf8"), s.title),
      });
      await sleep(300);
    } else {
      await b.navigate(s.url);
    }
    if (s.wait) await b.waitFor(s.wait, s.out + ": " + s.wait);
    if (s.run) {
      await b.evaluate(s.run);
      if (s.waitAfter) await b.waitFor(s.waitAfter, s.out + ": " + s.waitAfter);
    }
    if (s.then) {
      await b.evaluate(s.then);
      if (s.waitThen) await b.waitFor(s.waitThen, s.out + ": " + s.waitThen);
    }
    await sleep(s.settle || 500);
    let height = s.height || 700;
    if (s.term) {
      height = await b.evaluate("Math.ceil(document.querySelector('.w').getBoundingClientRect().bottom) + 28");
      await b.send("Emulation.setDeviceMetricsOverride", { width, height, deviceScaleFactor: 1, mobile: false });
      await sleep(200);
    }
    const shot = await b.send("Page.captureScreenshot", { format: "png", clip: { x: 0, y: 0, width, height, scale: 1 } });
    writeFileSync(s.out, Buffer.from(shot.data, "base64"));
    console.log("wrote", s.out);
  } finally {
    b.close();
  }
}
