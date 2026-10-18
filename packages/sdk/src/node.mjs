// Node http and Express (or any Connect-style) adapter for a TunnelSite. Only the
// manifest and the module's own path are handled; every other request reaches the
// application unchanged.
import { Readable } from 'node:stream';

const manifestPath = '/.well-known/jase-tunnel.json';

export function toRequest(req, signal) {
  const headers = new Headers();
  for (const [key, value] of Object.entries(req.headers)) {
    if (value === undefined || key.startsWith(':')) continue;
    if (Array.isArray(value)) for (const item of value) headers.append(key, item);
    else headers.set(key, value);
  }
  const protocol = req.socket?.encrypted ? 'https' : 'http';
  const url = `${protocol}://${req.headers.host ?? 'localhost'}${req.originalUrl ?? req.url ?? '/'}`;
  if (req.method === 'GET' || req.method === 'HEAD') return new Request(url, { method: req.method, headers, signal });
  // A body parser mounted earlier may have consumed the stream already.
  if (req.readableEnded && req.body !== undefined) return new Request(url, { method: req.method, headers, signal, body: typeof req.body === 'string' ? req.body : JSON.stringify(req.body) });
  return new Request(url, { method: req.method, headers, signal, body: Readable.toWeb(req), duplex: 'half' });
}

async function send(response, res) {
  const headers = {};
  response.headers.forEach((value, key) => { if (key !== 'set-cookie') headers[key] = value; });
  const cookies = response.headers.getSetCookie?.() ?? [];
  if (cookies.length) headers['set-cookie'] = cookies;
  const body = Buffer.from(await response.arrayBuffer());
  if (res.destroyed) return;
  res.writeHead(response.status, headers);
  res.end(body);
}

export function nodeMiddleware(site) {
  return (req, res, next) => {
    const pathname = (req.originalUrl ?? req.url ?? '/').split('?')[0];
    if (pathname !== manifestPath && !pathname.startsWith(site.path)) return next?.();
    // A long poll ends when the browser or the desktop app goes away.
    const controller = new AbortController();
    res.on('close', () => { if (!res.writableEnded) controller.abort(); });
    site.handle(toRequest(req, controller.signal)).then(response => send(response, res)).catch(error => {
      if (next) return next(error);
      if (!res.headersSent) { res.statusCode = 500; res.end(); }
    });
  };
}

// For a plain http.createServer(handler): the module's requests first, then the handler.
export function withJaseTunnel(site, handler) {
  const middleware = nodeMiddleware(site);
  return (req, res) => middleware(req, res, error => error ? (res.statusCode = 500, res.end()) : handler(req, res));
}
