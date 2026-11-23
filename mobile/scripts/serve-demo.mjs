// Serves the web export (dist/) and proxies the hub's routes to a hub (the mock hub in development), so the browser sees
// one origin and the mock needs no CORS. Dependency-free.
//   MOCK_HUB_URL=http://127.0.0.1:18760 PORT=8082 node scripts/serve-demo.mjs
import http from "node:http";
import net from "node:net";
import { createReadStream, existsSync, statSync } from "node:fs";
import { dirname, extname, join, normalize } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "dist");
const hub = new URL(process.env.MOCK_HUB_URL ?? "http://127.0.0.1:18760");
const port = Number(process.env.PORT ?? 8082);
const TYPES = { ".html": "text/html", ".js": "text/javascript", ".json": "application/json", ".css": "text/css", ".png": "image/png", ".ttf": "font/ttf", ".ico": "image/x-icon" };
const proxied = (url) => /^\/(api|health|mock)(\/|\?|$)/.test(url);

const server = http.createServer((req, res) => {
  if (proxied(req.url)) {
    const up = http.request({ host: hub.hostname, port: hub.port, path: req.url, method: req.method, headers: req.headers }, (r) => {
      res.writeHead(r.statusCode ?? 502, r.headers);
      r.pipe(res);
    });
    up.on("error", () => {
      res.writeHead(502).end("hub unreachable");
    });
    req.pipe(up);
    return;
  }
  const clean = normalize(decodeURIComponent(req.url.split("?")[0])).replace(/^(\.\.[/\\])+/, "");
  let file = join(root, clean);
  if (!file.startsWith(root) || !existsSync(file) || statSync(file).isDirectory()) file = join(root, "index.html"); // single-page fallback
  res.writeHead(200, { "Content-Type": TYPES[extname(file)] ?? "application/octet-stream" });
  createReadStream(file).pipe(res);
});

server.on("upgrade", (req, socket, head) => {
  if (!proxied(req.url)) return socket.destroy();
  const up = net.connect(Number(hub.port), hub.hostname, () => {
    const headers = Object.entries(req.headers).map(([k, v]) => `${k}: ${v}`).join("\r\n");
    up.write(`${req.method} ${req.url} HTTP/1.1\r\n${headers}\r\n\r\n`);
    if (head.length) up.write(head);
    socket.pipe(up).pipe(socket);
  });
  up.on("error", () => socket.destroy());
  socket.on("error", () => up.destroy());
});

server.listen(port, "127.0.0.1", () => console.log(`demo on http://127.0.0.1:${port} -> ${hub.origin}`));
