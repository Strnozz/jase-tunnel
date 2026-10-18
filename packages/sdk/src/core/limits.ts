// Request admission for the module's public API: bounded bodies and per-client quotas.
// Everything here answers BEFORE any expensive work starts (parsing a large body, a
// model call, a tool).

/** A request refused for capacity or size; the handlers turn it into 413/429/503. */
export class Refused extends Error {
  constructor(readonly status: 413 | 429 | 503, readonly code: string, message: string, readonly retryAfterSeconds?: number) { super(message); }
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
