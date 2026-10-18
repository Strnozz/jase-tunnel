import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createServer } from 'node:http';
import { McpServer, createMcpHandler, fromJsonSchema } from '@modelcontextprotocol/server';
import { z } from 'zod';
import { mcpTools } from '../src/mcp.mjs';
import { adaptTool, defineSite, TUNNEL_PROTOCOL } from '../src/server.mjs';

const siteOrigin = 'https://shop.example.com';
const context = (user = 'alice', signal = new AbortController().signal) => ({
  request: new Request(siteOrigin, { headers: { 'x-site-user': user, cookie: 'private-site-cookie' } }),
  signal, sessionId: user, callId: 'fixture'
});

async function serve(t, handle) {
  const server = createServer(async (req, res) => {
    try {
      const chunks = [];
      for await (const chunk of req) chunks.push(chunk);
      const request = new Request(`http://127.0.0.1:${server.address().port}${req.url}`, {
        method: req.method, headers: req.headers,
        ...(chunks.length ? { body: Buffer.concat(chunks), duplex: 'half' } : {})
      });
      const response = await handle(request);
      res.writeHead(response.status, Object.fromEntries(response.headers));
      if (response.body) {
        const reader = response.body.getReader();
        try {
          for (;;) {
            const { done, value } = await reader.read();
            if (done) break;
            if (res.destroyed) { await reader.cancel(); break; }
            res.write(value);
          }
        } finally { reader.releaseLock(); }
      }
      res.end();
    } catch { if (!res.destroyed) { res.writeHead(500); res.end(); } }
  });
  await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve); });
  t.after(async () => { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)); });
  return `http://127.0.0.1:${server.address().port}/mcp`;
}

async function fixture(t, { legacyOnly = false } = {}) {
  const calls = [], requests = [], eras = [];
  const handler = createMcpHandler(ctx => {
    eras.push(ctx.era);
    const server = new McpServer({ name: 'shop-fixture', version: '1.0.0' });
    server.registerTool('catalog.search', {
      description: 'Search products', inputSchema: z.object({ query: z.string() }),
      annotations: { readOnlyHint: true }
    }, async args => {
      const user = ctx.requestInfo.headers.get('x-upstream-user');
      calls.push({ tool: 'catalog.search', args, user });
      return { content: [{ type: 'text', text: 'Found' }], structuredContent: { user, count: 7 }, _meta: { secret: 'never-forward-this' } };
    });
    server.registerTool('orders.update', {
      description: 'Update an order', inputSchema: z.object({ id: z.number().int() }),
      annotations: { readOnlyHint: true }
    }, async args => { calls.push({ tool: 'orders.update', args }); return { content: [{ type: 'text', text: 'Updated' }] }; });
    server.registerTool('admin_hidden', { description: 'Never expose', inputSchema: z.object({}) }, async () => {
      throw new Error('must never run');
    });
    server.registerTool('slow_tool', { description: 'Slow', inputSchema: z.object({}) }, async () => {
      await new Promise(resolve => setTimeout(resolve, 4000));
      return { content: [{ type: 'text', text: 'Late' }] };
    });
    server.registerTool('large_tool', { description: 'Large', inputSchema: z.object({}) }, async () => ({
      content: [{ type: 'text', text: 'x'.repeat(1024 * 1024 + 1) }]
    }));
    server.registerTool('tuple_tool', { description: 'JSON Schema 2020-12', inputSchema: fromJsonSchema({
      $id: 'https://schemas.example.com/tuple', $schema: 'https://json-schema.org/draft/2020-12/schema',
      type: 'object', properties: { values: { type: 'array', prefixItems: [{ type: 'string' }], items: false, minItems: 1 } },
      required: ['values'], additionalProperties: false
    }) }, async args => ({ content: [{ type: 'text', text: JSON.stringify(args) }] }));
    server.registerTool('image_tool', { description: 'Unsupported image', inputSchema: z.object({}) }, async () => ({
      content: [{ type: 'image', data: 'ZmFrZQ==', mimeType: 'image/png' }]
    }));
    return server;
  });
  t.after(() => handler.close());
  const url = await serve(t, async request => {
    const body = request.method === 'POST' ? await request.clone().json() : {};
    requests.push({ method: body.method, cookie: request.headers.get('cookie'), user: request.headers.get('x-site-user') });
    if (legacyOnly && body.method === 'server/discover') return Response.json({ jsonrpc: '2.0', id: body.id, error: { code: -32601, message: 'Method not found' } });
    return handler.fetch(request);
  });
  return { url, calls, requests, eras };
}

test('MCP requires a fixed safe endpoint, explicit tool selection and per-tool permissions before I/O', () => {
  const rule = { lookup: { public: true } };
  assert.throws(() => mcpTools({ url: 'https://mcp.example.com', tools: { lookup: {} } }), /authorize/);
  assert.throws(() => mcpTools({ url: 'https://mcp.example.com', tools: {} }), /1 a 100/);
  for (const url of ['http://mcp.example.com', 'file:///tmp/mcp', 'https://user:secret@mcp.example.com', 'https://mcp.example.com/#x']) {
    assert.throws(() => mcpTools({ url, tools: rule }), /HTTPS/);
  }
  assert.throws(() => mcpTools({ url: 'https://mcp.example.com', tools: rule, timeoutMs: Infinity }), /timeoutMs/);
});

test('real MCP 2026 server: allowlist, aliases, explicit credentials and visitor isolation', async t => {
  const f = await fixture(t);
  const provider = mcpTools({ url: f.url,
    headers: ({ request }) => ({ 'x-upstream-user': request.headers.get('x-site-user') }),
    tools: { search_products: { name: 'catalog.search', public: true, readOnly: true } }
  });
  const [aliceTools, bobTools] = await Promise.all([provider(context('alice')), provider(context('bob'))]);
  assert.deepEqual(aliceTools.map(t => t.name), ['search_products']);
  const results = await Promise.all([
    adaptTool(aliceTools[0]).execute({ query: 'camera' }, context('alice')),
    adaptTool(bobTools[0]).execute({ query: 'book' }, context('bob'))
  ]);
  assert.equal(results[0].structuredContent.user, 'alice');
  assert.equal(results[1].structuredContent.user, 'bob');
  assert.equal(results[0]._meta, undefined);
  assert.ok(f.requests.every(r => r.cookie === null && r.user === null));
  assert.ok(f.eras.every(era => era === 'modern'));
});

test('MCP permissions and argument validation precede execution; remote readOnlyHint never grants trust', async t => {
  const f = await fixture(t);
  const provider = mcpTools({ url: f.url, tools: {
    update_order: { name: 'orders.update', authorize: ctx => ctx.request.headers.get('x-site-user') === 'alice' }
  } });
  const [raw] = await provider(context());
  assert.equal(raw.readOnly, false);
  const tool = adaptTool(raw);
  await assert.rejects(tool.execute({ id: '1' }, context()));
  await assert.rejects(tool.execute({ id: 1 }, context('bob')));
  assert.equal(f.calls.length, 0);
  await tool.execute({ id: 1 }, context());
  assert.equal(f.calls.length, 1);
  await assert.rejects(mcpTools({ url: f.url, tools: { missing_tool: { public: true } } })(context()), /non disponibile/);
});

test('the MCP adapter negotiates a real legacy 2025 HTTP endpoint', async t => {
  const f = await fixture(t, { legacyOnly: true });
  const tools = await mcpTools({ url: f.url, tools: { search_products: { name: 'catalog.search', public: true } } })(context());
  assert.equal((await adaptTool(tools[0]).execute({ query: 'camera' }, context())).content[0].text, 'Found');
  assert.ok(f.requests.some(r => r.method === 'initialize'));
  assert.ok(f.eras.every(era => era === 'legacy'));
});

test('MCP validates 2020-12 schemas with repeated IDs across visitors and rejects unsupported content', async t => {
  const f = await fixture(t);
  const provider = mcpTools({ url: f.url, tools: { tuple_tool: { public: true }, image_tool: { public: true } } });
  const [alice, bob] = await Promise.all([provider(context('alice')), provider(context('bob'))]);
  await assert.rejects(adaptTool(alice[0]).execute({ values: [1] }, context()));
  await assert.rejects(adaptTool(bob[0]).execute({ values: ['one', 'two'] }, context()));
  assert.match((await adaptTool(alice[0]).execute({ values: ['one'] }, context())).content[0].text, /one/);
  await assert.rejects(adaptTool(alice[1]).execute({}, context()), /testuali e JSON/);
});

test('MCP requests respect cancellation, deadlines, response budgets and refuse redirects with credentials', async t => {
  const f = await fixture(t);
  const provider = mcpTools({ url: f.url, timeoutMs: 1500, tools: { slow_tool: { public: true } } });
  const [slow] = await provider(context());
  await assert.rejects(adaptTool(slow).execute({}, context()), error => error.name === 'TimeoutError' || error.name === 'AbortError');
  const controller = new AbortController(); controller.abort();
  await assert.rejects(provider(context('alice', controller.signal)), error => error.name === 'AbortError');
  const inFlight = new AbortController();
  const abortTimer = setTimeout(() => inFlight.abort(), 100);
  try { await assert.rejects(adaptTool(slow).execute({}, context('alice', inFlight.signal)), error => error.name === 'AbortError'); }
  finally { clearTimeout(abortTimer); }
  const [large] = await mcpTools({ url: f.url, tools: { large_tool: { public: true } } })(context());
  await assert.rejects(adaptTool(large).execute({}, context()));
  let followed = 0;
  const destination = await serve(t, () => { followed++; return Response.json({}); });
  const redirect = await serve(t, () => new Response(null, { status: 307, headers: { location: destination } }));
  await assert.rejects(mcpTools({ url: redirect, headers: { Authorization: 'fixture-private-token' }, tools: { lookup: { public: true } } })(context()), /MCP: servizio/);
  assert.equal(followed, 0);
});

test('a real MCP tool passes through Tunnel confirmation and the local-model round trip exactly once', async t => {
  const f = await fixture(t);
  const site = defineSite({ origin: siteOrigin, tools: mcpTools({ url: f.url,
    tools: { update_order: { name: 'orders.update', public: true } }
  }) });
  const request = (action, data, headers = {}) => new Request(`${siteOrigin}/api/jase-tunnel/v1/${action}`, {
    method: data === undefined ? 'GET' : 'POST', headers: { host: 'shop.example.com', ...(data === undefined ? {} : { 'content-type': 'application/json' }), ...headers },
    ...(data === undefined ? {} : { body: JSON.stringify(data) })
  });
  const ticketResponse = await site.handle(request('tickets', { protocol: TUNNEL_PROTOCOL }, { origin: siteOrigin }));
  const cookie = ticketResponse.headers.get('set-cookie').split(';')[0], ticket = (await ticketResponse.json()).ticket;
  const pair = await (await site.handle(request('pair', { protocol: TUNNEL_PROTOCOL, ticket, label: 'Fixture', models: [
    { key: 'ollama:fixture', name: 'Local model', engine: 'ollama', tools: true, contextTokens: 8192 }
  ] }))).json();
  const browser = { cookie, origin: siteOrigin }, desktop = { authorization: 'Bearer ' + pair.token };
  const model = (await (await site.handle(request('session', undefined, { cookie }))).json()).models[0].id;
  const chat = site.handle(request('chat', { model, message: 'Update order' }, browser));
  const task = (await (await site.handle(request('poll', {}, desktop))).json()).task;
  assert.equal(task.tools[0].name, 'update_order');
  assert.doesNotMatch(JSON.stringify(task), /127\.0\.0\.1|Authorization|admin_hidden/);
  await site.handle(request('result', { id: task.id, result: { content: '', toolCalls: [{ name: 'update_order', arguments: { id: 7 } }] } }, desktop));
  const pending = (await (await chat).json()).confirmation;
  assert.ok(pending); assert.equal(f.calls.length, 0);
  const approved = site.handle(request('confirm', { id: pending.id, allow: true }, browser));
  const next = (await (await site.handle(request('poll', {}, desktop))).json()).task;
  assert.equal(f.calls.length, 1); assert.match(next.messages.at(-1).content, /Updated/);
  await site.handle(request('result', { id: next.id, result: { content: 'Done', toolCalls: [] } }, desktop));
  assert.equal((await (await approved).json()).content, 'Done');
  assert.equal((await site.handle(request('confirm', { id: pending.id, allow: true }, browser))).status, 410);
  assert.equal(f.calls.length, 1);
});
