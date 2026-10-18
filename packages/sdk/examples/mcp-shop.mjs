// Run after npm ci --prefix packages/sdk. Local pairing requires the desktop development build.
import { createServer } from 'node:http';
import { Readable } from 'node:stream';
import { McpServer, createMcpHandler } from '@modelcontextprotocol/server';
import { z } from 'zod';
import { mcpTools } from '../src/mcp.mjs';
import { defineSite } from '../src/server.mjs';
import { withJaseTunnel } from '../src/node.mjs';

const products = [{ name: 'Notebook', price: 8 }, { name: 'Pen', price: 3 }];
const mcp = createMcpHandler(() => {
  const server = new McpServer({ name: 'demo-catalog', version: '1.0.0' });
  server.registerTool('catalog.search', {
    description: 'Search the shop’s public catalog', inputSchema: z.object({ query: z.string() })
  }, async ({ query }) => ({ content: [{ type: 'text', text: JSON.stringify(
    products.filter(p => p.name.toLowerCase().includes(query.toLowerCase()))
  ) }] }));
  return server;
});
const upstream = createServer(async (req, res) => {
  try {
    const response = await mcp.fetch(new Request(`http://127.0.0.1:${upstream.address().port}${req.url}`, {
      method: req.method, headers: req.headers,
      ...(['GET', 'HEAD'].includes(req.method) ? {} : { body: Readable.toWeb(req), duplex: 'half' })
    }));
    res.writeHead(response.status, Object.fromEntries(response.headers));
    if (response.body) Readable.fromWeb(response.body).pipe(res); else res.end();
  } catch { res.writeHead(500); res.end(); }
});
await new Promise(resolve => upstream.listen(0, '127.0.0.1', resolve));
const site = defineSite({ name: 'MCP demo shop', origin: process.env.JASE_TUNNEL_ORIGIN,
  tools: mcpTools({ url: `http://127.0.0.1:${upstream.address().port}/mcp`,
    tools: { search_products: { name: 'catalog.search', public: true, readOnly: true } }
  })
});
const web = createServer(withJaseTunnel(site, (req, res) => {
  res.writeHead(200, { 'content-type': 'text/html; charset=utf-8' });
  res.end('<!doctype html><html lang="en"><meta charset="utf-8"><title>MCP shop</title><body><h1>MCP demo shop</h1><p>Open Jase Tunnel and ask how much a pen costs.</p><script type="module" src="/api/jase-tunnel/v1/client/widget.mjs"></script></body></html>');
}));
web.listen(Number(process.env.PORT ?? 3322), '127.0.0.1', () => console.log(`Shop: http://127.0.0.1:${web.address().port}`));
async function close() {
  web.closeAllConnections(); upstream.closeAllConnections();
  await Promise.all([new Promise(resolve => web.close(resolve)), new Promise(resolve => upstream.close(resolve)), mcp.close()]);
}
process.once('SIGINT', () => void close());
process.once('SIGTERM', () => void close());
