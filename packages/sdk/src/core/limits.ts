// Request admission for the public API (audits F02 and F06): bounded bodies, per-client
// quotas and global capacity. Everything here answers BEFORE any expensive work starts
// (parsing a large body, a model call, a Goal and its background job).

/** A request refused for capacity or size; the handlers turn it into 413/429/503. */
export class Refused extends Error {
  constructor(readonly status: 413 | 429 | 503, readonly code: string, message: string, readonly retryAfterSeconds?: number) { super(message); }
}
export function refusedResponse(error: Refused): Response {
  return Response.json({ error: { code: error.code, message: error.message } }, { status: error.status,
    headers: { "Cache-Control": "no-store", ...(error.retryAfterSeconds ? { "Retry-After": String(error.retryAfterSeconds) } : {}) } });
}

/** The body as text, read as a stream and abandoned as soon as it exceeds `maxBytes`: an
 *  oversized body is never buffered whole (Content-Length is checked first, but not trusted). */
export async function readBodyText(request: Request, maxBytes: number): Promise<string> {
  const declared = Number(request.headers.get("content-length"));
  if (Number.isFinite(declared) && declared > maxBytes) throw new Refused(413, "payload_too_large", "Request body too large.");
  if (!request.body) return "";
  const reader = request.body.getReader(), chunks: Uint8Array[] = [];
  let size = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    size += value.byteLength;
    if (size > maxBytes) { await reader.cancel().catch(() => undefined); throw new Refused(413, "payload_too_large", "Request body too large."); }
    chunks.push(value);
  }
  return new TextDecoder().decode(Buffer.concat(chunks));
}
/** JSON body within `maxBytes`; `onInvalid` decides how malformed JSON is reported. */
export async function readJsonBody(request: Request, maxBytes: number, onInvalid: () => never): Promise<unknown> {
  const text = await readBodyText(request, maxBytes);
  try { return JSON.parse(text) as unknown; } catch { return onInvalid(); }
}

/** The client address as the reverse proxy states it. Caddy (the production front) replaces
 *  X-Forwarded-For with the peer address for untrusted peers; a direct local request has none. */
// The address the nearest proxy saw. Caddy forwards headers a client sends unless it sets
// them itself: X-Jase-Client-IP counts only where the proxy writes it
// (JASE_TRUST_CLIENT_IP_HEADER=true with `header_up X-Jase-Client-IP {remote_host}`), and
// of X-Forwarded-For only the LAST entry, the one the proxy appended, is ours.
export function clientAddress(request: Request): string {
  const explicit = process.env.JASE_TRUST_CLIENT_IP_HEADER === "true" ? request.headers.get("x-jase-client-ip")?.trim() : undefined;
  const forwarded = request.headers.get("x-forwarded-for")?.split(",").at(-1)?.trim();
  return (explicit || forwarded || "local").slice(0, 64);
}

/** Fixed-window counters per key, in memory, with a bounded number of keys. */
export class WindowLimiter {
  private windows = new Map<string, { start: number; count: number }>();
  constructor(private readonly max: number, private readonly windowMs: number, private readonly maxKeys = 50_000) {}
  /** Seconds to wait, or 0 when the attempt is admitted (and counted). */
  take(key: string, now = Date.now()): number {
    let entry = this.windows.get(key);
    if (!entry || now - entry.start >= this.windowMs) {
      if (!entry && this.windows.size >= this.maxKeys) this.prune(now);
      entry = { start: now, count: 0 }; this.windows.set(key, entry);
    }
    if (entry.count >= this.max) return Math.max(1, Math.ceil((entry.start + this.windowMs - now) / 1000));
    entry.count++;
    return 0;
  }
  private prune(now: number) {
    for (const [key, value] of this.windows) if (now - value.start >= this.windowMs) this.windows.delete(key);
    // Still full: the oldest windows go (an attacker with many addresses cannot grow memory).
    for (const key of this.windows.keys()) { if (this.windows.size < this.maxKeys) break; this.windows.delete(key); }
  }
}

/** At most `limit` holders at once; `tryAcquire` never waits (a full service answers 503). */
export class Capacity {
  private held = 0;
  constructor(private readonly limit: number) {}
  get inUse(): number { return this.held; }
  tryAcquire(): (() => void) | undefined {
    if (this.held >= this.limit) return undefined;
    this.held++;
    let released = false;
    return () => { if (!released) { released = true; this.held--; } };
  }
}
