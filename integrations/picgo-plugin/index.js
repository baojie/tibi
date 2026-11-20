// PicGo / PicList uploader for tibi. Each image is sent as a raw-body
// upload (POST /api/v1/items?filename=…) with the configured token; the
// returned direct URL (/{id}.{ext}) becomes the image link.
const UPLOADER = "tibi";

function config(ctx) {
  const c = ctx.getConfig(`picBed.${UPLOADER}`) || {};
  if (!c.server || !c.token) {
    throw new Error("tibi: set the server URL and API token in the uploader settings");
  }
  return { server: String(c.server).replace(/\/+$/, ""), token: String(c.token).trim(), expires: c.expires || "" };
}

async function handle(ctx) {
  const { server, token, expires } = config(ctx);
  for (const img of ctx.output) {
    const body = img.buffer || (img.base64Image ? Buffer.from(img.base64Image, "base64") : null);
    if (!body) continue;
    const q = new URLSearchParams({ filename: img.fileName || "image.png", source: "picgo" });
    if (expires) q.set("expires", expires);
    const res = await ctx.request({
      method: "POST",
      url: `${server}/api/v1/items?${q}`,
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/octet-stream" },
      data: body,
      resolveWithFullResponse: false,
    });
    const item = typeof res === "string" ? JSON.parse(res) : (res && res.data !== undefined ? res.data : res);
    if (!item || !item.raw_url) {
      throw new Error("tibi: unexpected response " + JSON.stringify(item));
    }
    img.imgUrl = item.raw_url;
    img.url = item.url;
    delete img.buffer;
    delete img.base64Image;
  }
  return ctx;
}

function settings(ctx) {
  const c = ctx.getConfig(`picBed.${UPLOADER}`) || {};
  return [
    { name: "server", type: "input", default: c.server, required: true, message: "https://tibi.example.com", alias: "Server URL" },
    { name: "token", type: "password", default: c.token, required: true, message: "tibi_…", alias: "API token" },
    { name: "expires", type: "input", default: c.expires, required: false, message: "e.g. 7d (empty: server default)", alias: "Expires" },
  ];
}

module.exports = (ctx) => ({
  uploader: UPLOADER,
  register() {
    ctx.helper.uploader.register(UPLOADER, { name: "tibi", handle, config: settings });
  },
});
