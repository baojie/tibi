// A throttling HTTP proxy, so an upload on localhost lasts long enough to
// be interrupted on camera.   node slow.mjs LISTEN_PORT TARGET_PORT BYTES_PER_SEC
import { createServer, request } from "node:http";
import { Transform } from "node:stream";

const [listen, target, rate] = process.argv.slice(2).map(Number);
const throttle = () => {
  let start = Date.now(), sent = 0;
  return new Transform({
    transform(chunk, _, done) {
      sent += chunk.length;
      const wait = (sent / rate) * 1000 - (Date.now() - start);
      setTimeout(() => done(null, chunk), Math.max(0, wait));
    },
  });
};
createServer((req, res) => {
  const up = request({ host: "127.0.0.1", port: target, method: req.method, path: req.url, headers: req.headers }, (r) => {
    // tus answers with an absolute Location; keep later requests on the proxy.
    if (r.headers.location) r.headers.location = r.headers.location.replace(`:${target}/`, `:${listen}/`);
    res.writeHead(r.statusCode, r.headers);
    r.pipe(res);
  });
  up.on("error", () => res.destroy());
  req.on("aborted", () => up.destroy());
  req.pipe(throttle()).pipe(up);
}).listen(listen, "127.0.0.1");
