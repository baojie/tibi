// @ts-check
// tibi API client for the extension. Endpoints and types come from
// api.gen.js, generated from api/openapi.yaml.
import { ENDPOINTS } from "./api.gen.js";

/** @typedef {import("./api.gen.js").Item} Item */
/** @typedef {import("./api.gen.js").ItemPage} ItemPage */
/** @typedef {import("./api.gen.js").Whoami} Whoami */

/** Uploads at or above this size use tus (resumable, chunked). */
export const RESUMABLE_THRESHOLD = 20 << 20;
const CHUNK = 8 << 20;

export class ApiError extends Error {
  /** @param {number} status @param {string} message @param {string} [code] */
  constructor(status, message, code) {
    super(message);
    this.status = status;
    this.code = code || "";
  }
}

/** @param {Response} r */
async function failure(r) {
  let msg = r.status + " " + r.statusText, code = "";
  try {
    const t = await r.text();
    try {
      const j = JSON.parse(t);
      if (j.error) { msg = j.error.message || msg; code = j.error.code || ""; }
    } catch { if (t.trim()) msg = t.trim().replace(/^ERR_[A-Z_]+: /, ""); }
  } catch {}
  return new ApiError(r.status, msg, code);
}

/**
 * @typedef {Object} UploadOptions
 * @property {string} [filename]
 * @property {string} [title]
 * @property {string} [kind]
 * @property {string} [lang]
 * @property {string} [expires]
 * @property {string} [visibility]
 * @property {string} [source]
 * @property {Object<string, string>} [meta]
 */

/** @param {UploadOptions} o @returns {Object<string, string>} */
function optionValues(o) {
  /** @type {Object<string, string>} */
  const v = {};
  for (const [k, val] of Object.entries(o)) {
    if (val === undefined || val === "") continue;
    v[k] = k === "meta" ? JSON.stringify(val) : String(val);
  }
  return v;
}

/** base64 of a UTF-8 string (tus Upload-Metadata). @param {string} s */
function b64(s) {
  let bin = "";
  for (const b of new TextEncoder().encode(s)) bin += String.fromCharCode(b);
  return btoa(bin);
}

export class Client {
  /** @param {string} server @param {string} token @param {typeof fetch} [fetchFn] */
  constructor(server, token, fetchFn) {
    this.server = server.replace(/\/+$/, "");
    this.token = token;
    this.fetch = fetchFn || fetch.bind(globalThis);
  }

  /** @param {keyof typeof ENDPOINTS} op @param {Object<string,string>} [params] @param {Object<string,string|number>} [query] */
  url(op, params = {}, query = {}) {
    const path = ENDPOINTS[op].path.replace(/\{(\w+)\}/g, (_, k) => encodeURIComponent(params[k]));
    const q = new URLSearchParams();
    for (const [k, v] of Object.entries(query)) if (v !== undefined && v !== "") q.set(k, String(v));
    const qs = q.toString();
    return this.server + path + (qs ? "?" + qs : "");
  }

  /** @param {string} method @param {string} url @param {RequestInit} [init] */
  async request(method, url, init = {}) {
    const headers = new Headers(init.headers);
    headers.set("Authorization", "Bearer " + this.token);
    const r = await this.fetch(url, { ...init, method, headers });
    if (!r.ok) throw await failure(r);
    return r;
  }

  /** @returns {Promise<Whoami>} */
  async whoami() {
    return (await this.request("GET", this.url("whoami"))).json();
  }

  /** @param {number} [limit] @returns {Promise<ItemPage>} */
  async recent(limit = 10) {
    return (await this.request("GET", this.url("listItems", {}, { limit }))).json();
  }

  /**
   * Uploads a blob; large ones go through tus.
   * @param {Blob} blob @param {UploadOptions} opts
   * @param {(sent: number, total: number) => void} [progress]
   * @returns {Promise<Item>}
   */
  async upload(blob, opts, progress) {
    if (blob.size >= RESUMABLE_THRESHOLD) return this.uploadResumable(blob, opts, progress);
    const r = await this.request("POST", this.url("upload", {}, optionValues(opts)), {
      body: blob,
      headers: { "Content-Type": blob.type || "application/octet-stream" },
    });
    return r.json();
  }

  /** @param {Blob} blob @param {UploadOptions} opts @param {(sent: number, total: number) => void} [progress] @returns {Promise<Item>} */
  async uploadResumable(blob, opts, progress) {
    const tus = { "Tus-Resumable": "1.0.0" };
    const meta = Object.entries(optionValues(opts)).map(([k, v]) => k + " " + b64(v)).join(",");
    const created = await this.request("POST", this.server + "/api/v1/uploads/", {
      headers: { ...tus, "Upload-Length": String(blob.size), "Upload-Metadata": meta },
    });
    let loc = created.headers.get("Location");
    if (!loc) throw new ApiError(0, "server did not return an upload URL (is Location exposed by CORS?)");
    if (loc.startsWith("/")) loc = this.server + loc;
    let offset = 0, retries = 0;
    while (offset < blob.size) {
      try {
        const r = await this.request("PATCH", loc, {
          body: blob.slice(offset, offset + CHUNK),
          headers: { ...tus, "Upload-Offset": String(offset), "Content-Type": "application/offset+octet-stream" },
        });
        offset = Number(r.headers.get("Upload-Offset"));
        retries = 0;
        if (progress) progress(offset, blob.size);
      } catch (e) {
        if ((e instanceof ApiError && e.status && e.status < 500) || ++retries > 5) throw e;
        await new Promise((res) => setTimeout(res, retries * 1000));
        const h = await this.request("HEAD", loc, { headers: tus });
        offset = Number(h.headers.get("Upload-Offset"));
      }
    }
    return (await this.request("GET", loc.replace(/\/+$/, "") + "/item")).json();
  }
}
