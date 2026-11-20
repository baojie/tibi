// Runs the plugin against a real tibi server with a minimal PicGo-like
// context: node test.mjs BASE_URL TOKEN
import { createRequire } from "node:module";
const require = createRequire(import.meta.url);
const plugin = require("./index.js");
const [server, token] = process.argv.slice(2);
if (!server || !token) { console.error("usage: node test.mjs BASE_URL TOKEN"); process.exit(2); }

const png = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==", "base64");
let registered;
const ctx = {
  output: [{ fileName: "picgo-test.png", buffer: png }],
  getConfig: (k) => (k === "picBed.tibi" ? { server, token } : undefined),
  helper: { uploader: { register: (name, u) => { registered = u; } } },
  // PicGo's ctx.request resolves to the parsed body.
  request: async ({ method, url, headers, data }) => {
    const r = await fetch(url, { method, headers, body: data });
    const j = await r.json();
    if (!r.ok) throw new Error(j.error && j.error.message);
    return j;
  },
};
plugin(ctx).register();
await registered.handle(ctx);
const url = ctx.output[0].imgUrl;
if (!/\.png$/.test(url)) { console.error("FAIL: imgUrl", url); process.exit(1); }
const got = Buffer.from(await (await fetch(url)).arrayBuffer());
if (!got.equals(png)) { console.error("FAIL: bytes differ"); process.exit(1); }
console.log("OK: PicGo plugin uploaded", url);
