import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { runInNewContext } from 'node:vm';
import { TunnelClient } from '../src/browser.mjs';

async function browserFixture(run, { blocked = false, closed = false } = {}) {
  const names = ['window', 'document', 'navigator', 'location', 'fetch'];
  const originals = new Map(names.map(name => [name, Object.getOwnPropertyDescriptor(globalThis, name)]));
  const listeners = new Set(), sent = [], requests = [], saved = new Map();
  let paired = false, opened = 0, params;
  const document = { visibilityState: 'visible' };
  const localOrigin = 'http://127.0.0.1:11439';
  const emit = (data, changes = {}) => {
    for (const fn of listeners) fn({ source: popup, origin: localOrigin, data, ...changes });
  };
  const popup = {
    closed,
    close() { this.closed = true; },
    postMessage(message, origin) {
      assert.equal(origin, localOrigin);
      assert.equal(message.channel, params.get('channel'));
      sent.push(message);
      if (message.type === 'hello') {
        if (!closed) queueMicrotask(() => emit({ ...message, type: 'ready' }));
        return;
      }
      const data = message.operation === 'discover' ? { receipt: 'a'.repeat(64), expiresAt: Date.now() + 60_000, state: 'verifying' } :
        message.operation === 'pair' ? (paired = true, { state: 'awaiting_approval' }) : { state: paired ? 'connected' : 'compatible' };
      const answer = { ...message, type: 'response', status: 200, data };
      queueMicrotask(() => {
        const forged = { ...answer, status: 403, data: { error: { message: 'FORGED' } } };
        emit(forged, { origin: 'https://evil.example.com' });
        emit(forged, { source: {} });
        emit({ ...forged, channel: 'b'.repeat(64) });
        emit(answer);
      });
    }
  };
  const storage = { getItem: key => saved.get(key), setItem: (key, value) => saved.set(key, value), removeItem: key => saved.delete(key) };
  const window = {
    localStorage: storage, sessionStorage: storage,
    addEventListener(name, fn) { if (name === 'message') listeners.add(fn); },
    removeEventListener(name, fn) { if (name === 'message') listeners.delete(fn); },
    open(url) {
      opened++;
      assert.equal(new URL(url).pathname, '/tunnel/connect');
      params = new URLSearchParams(new URL(url).hash.slice(1));
      assert.equal(params.get('origin'), 'https://shop.example.com');
      assert.match(params.get('channel'), /^[a-f0-9]{64}$/);
      if (blocked) return null;
      document.visibilityState = 'hidden'; // Safari focuses the new connection window.
      return popup;
    }
  };
  window.top = window;
  const values = { window, document, location: { origin: 'https://shop.example.com', hostname: 'shop.example.com' },
    navigator: { userAgent: 'Mozilla/5.0 AppleWebKit/605.1.15 Version/26.6 Safari/605.1.15' },
    fetch: async (url, options) => {
      requests.push({ url, options });
      assert.ok(url.startsWith('/api/jase-tunnel/v1/'), 'Safari must never fetch HTTP loopback from the website');
      return Response.json(url.endsWith('tickets') ? { ticket: 'c'.repeat(64) } : { connected: true, models: [{ id: 'model' }] });
    }
  };
  for (const [name, value] of Object.entries(values)) Object.defineProperty(globalThis, name, { configurable: true, writable: true, value });
  try { await run({ sent, requests, saved, listeners, popup, opened: () => opened }); }
  finally {
    for (const [name, descriptor] of originals) {
      if (descriptor) Object.defineProperty(globalThis, name, descriptor);
      else delete globalThis[name];
    }
  }
}

test('Safari connects through a user-opened window, pins source/origin/channel and cleans up', async () => {
  await browserFixture(async fixture => {
    const client = new TunnelClient();
    assert.equal(await client.canAnnounce(), false);
    assert.equal(await client.announce(), null);
    assert.equal(fixture.opened(), 0, 'an automatic announcement must not open a window');
    const pending = client.connect();
    assert.equal(fixture.opened(), 1, 'open synchronously in the original click');
    assert.equal((await pending).connected, true);
    assert.deepEqual(fixture.sent.filter(m => m.type === 'request').map(m => m.operation), ['discover', 'discovery', 'pair', 'discovery']);
    assert.equal(fixture.popup.closed, true);
    assert.equal(fixture.listeners.size, 0);
    assert.equal(client.receipt, undefined);
    assert.equal(fixture.saved.get('jase-tunnel-enabled:https://shop.example.com'), 'true');
  });
});

test('Safari explains blocked/closed windows and does not fall back to a forbidden fetch', async () => {
  await browserFixture(async fixture => {
    await assert.rejects(new TunnelClient().connect(), /Allow this website to open/);
    assert.equal(fixture.requests.length, 0);
  }, { blocked: true });
  await browserFixture(async fixture => {
    await assert.rejects(new TunnelClient().connect(), /window was closed/);
    assert.equal(fixture.requests.length, 0);
    assert.equal(fixture.listeners.size, 0);
  }, { closed: true });
});

test('the local window accepts only its exact opener and the three pairing operations', async () => {
  const html = readFileSync(new URL('../../../web_bridge.html', import.meta.url), 'utf8');
  const script = html.match(/<script nonce="\{\{nonce\}\}">([\s\S]*?)<\/script>/)[1];
  const messages = [], requests = [], listeners = [], elements = new Map();
  const origin = 'https://shop.example.com', channel = 'd'.repeat(64);
  const opener = { postMessage: (message, destination) => { assert.equal(destination, origin); messages.push(message); } };
  const window = { opener, addEventListener: (event, handler) => listeners.push(handler) };
  window.top = window;
  runInNewContext(script, {
    window, location: { hash: '#' + new URLSearchParams({ origin, channel }) }, URL, URLSearchParams, AbortSignal,
    document: { getElementById: name => { if (!elements.has(name)) elements.set(name, {}); return elements.get(name); } },
    setTimeout() {},
    fetch: async (path, options) => { requests.push({ path, body: JSON.parse(options.body) }); return Response.json({ state: 'compatible' }); }
  });
  const receive = listeners[0];
  const data = { protocol: 'jase-tunnel-window-v1', channel, type: 'hello' };
  await receive({ source: {}, origin, data });
  await receive({ source: opener, origin: 'https://evil.example.com', data });
  assert.equal(messages.length, 1, 'only the initial loaded signal');
  await receive({ source: opener, origin, data });
  assert.equal(messages.at(-1).type, 'ready');
  await receive({ source: opener, origin, data: { ...data, type: 'request', id: 1, operation: 'quit' } });
  await receive({ source: opener, origin, data: { ...data, channel: 'e'.repeat(64), type: 'request', id: 1, operation: 'discover' } });
  assert.equal(requests.length, 0);
  await receive({ source: opener, origin, data: { ...data, type: 'request', id: 1, operation: 'discover', body: { protocol: 'jase-tunnel-v1' } } });
  assert.deepEqual(requests, [{ path: '/v1/browser-bridge', body: { origin, operation: 'discover', body: { protocol: 'jase-tunnel-v1' } } }]);
  assert.equal(messages.at(-1).type, 'response');
});
