# jase-tunnel

The SDK for websites that want to use visitors’ local LLMs through the [Jase Tunnel desktop app](https://github.com/Strnozz/jase-tunnel). Tools, data and authorization stay on the provider’s server; inference runs on the visitor’s computer after consent. No external inference API, Tunnel account or relay is required. The website keeps its own authentication requirements.

## One command in your project

```sh
npx jase-tunnel init
```

Requires Node.js 22 or later. `init`:

1. installs a pinned `jase-tunnel` dependency, without running npm installation scripts;
2. discovers exported AI tools, registers them as **disabled** in `jase-tunnel.tools.json`, and writes `jase-tunnel.config`;
3. mounts the manifest and broker;
4. adds the panel to your pages.

| Project | Server | Panel |
| --- | --- | --- |
| **Next.js App Router** | `/.well-known/jase-tunnel.json` and `/api/jase-tunnel/v1/[...path]` routes | `<JaseTunnel />` in the layout |
| **Express** | Middleware immediately after `express()`, before other middleware | Script in HTML files and templates |
| **`node:http`** | Wraps the handler passed to `createServer` | Script in HTML files and templates |

`init` warns when a project targets Vercel, Netlify, Cloudflare Workers or other serverless functions; the broker cannot run there. See the hosting limits below.

The server uses dynamic `import()`, supporting CommonJS and ESM. Recognized templates are `.html`, `.ejs`, `.hbs`, `.njk`, `.liquid` and `.mustache` files containing `</body>`. Otherwise, `init` prints the tag to add:

```html
<script type="module" src="/api/jase-tunnel/v1/client/widget.mjs"></script>
```

Conflicting existing files are reported before any writes. Modified server files, layouts, pages and registries are backed up in `.jase-tunnel/backups/`, excluded from Git. Repeating `init` does not duplicate changes. `--dry-run` lists planned changes without writing or installing; `--cwd <directory>` selects a project; `--package <package or tarball>` selects another SDK version or local package.

TypeScript Express and `node:http` projects use `jase-tunnel.config.ts`, imported without an extension. This works with `tsx`, `ts-node` or CommonJS compilation. ESM compilation into `dist/` requires adjusting the import path. Other servers can use standard Fetch APIs:

```ts
import site from './jase-tunnel.config';
const response = await site.handle(request); // forward the manifest and /api/jase-tunnel/v1/*
```

Manual Node integration: `nodeMiddleware(site)` and `withJaseTunnel(site, handler)` from `jase-tunnel/node`. Frontend APIs: `JaseTunnel` from `jase-tunnel/react`, `mountJaseTunnel` from `jase-tunnel/widget`, or `TunnelClient` from `jase-tunnel/browser`. Automatic installation is not claimed for Python, PHP, Ruby, Next Pages Router or unknown server routers.

## Tool security: the visitor controls the model

The model runs on the visitor’s computer. The visitor can replace it or respond directly to the broker, so **the visitor decides which tools to call and with which arguments**. Every enabled tool is effectively a public website API callable by anyone using Jase Tunnel:

- Review tools written for internal agents before enabling them.
- Every tool needs `authorize(context, args)`, checking the request’s session and arguments, or `public: true` when safe for any visitor. Without either policy, the SDK rejects it at startup.
- Panel confirmation protects visitors from models influenced by malicious content. It does not protect the provider from visitors, who can approve their own calls.
- `readOnly` controls confirmation; it never grants authorization.

Discovered tools stay disabled until explicitly enabled:

```sh
npx jase-tunnel tools list
npx jase-tunnel tools enable lookup_product            # tool has authorize
npx jase-tunnel tools enable opening_hours --public    # safe for everyone
```

`tools enable` rejects a discovered tool without `authorize` unless you add `--public`.

## Tool discovery

The scanner parses JavaScript/TypeScript syntax. It **never imports or executes the project**, follows symbolic links, or reads environment variables, credentials, databases, data directories, dependencies or tests. It does not use an LLM or send source code elsewhere.

It recognizes exported AI SDK `tool(...)`, LangChain `tool(fn, config)` and SDK `defineTool(...)` declarations, plus exported objects or collections with descriptions, schemas and `execute`/`invoke` functions. Unexported tools and context-dependent factories need an adapter. For an MCP server, use the optional adapter below; scanning never connects to servers automatically. Ordinary functions and arbitrary HTTP routes are not automatically treated as AI tools.

Schemas must describe JSON objects, or be Zod 4 schemas convertible to JSON Schema. Duplicate names and incomplete schemas produce explicit errors. Declarations live in `jase-tunnel.tools.json`; server imports live in `.jase-tunnel/tools.ts` or `.mjs`.

```sh
npx jase-tunnel tools list
npx jase-tunnel tools scan
npx jase-tunnel tools add ./src/ai/tools.ts#searchProducts
npx jase-tunnel tools add ./src/ai/tools.ts#tools.lookup --name lookup_product
npx jase-tunnel tools remove lookup_product
```

Manual additions are enabled and preserved during future scans; add `--public` when appropriate if no `authorize` exists. Removal disables a tool across subsequent scans. A scan never re-enables it. To register an ordinary exported function, provide its schema and description:

```sh
npx jase-tunnel tools add ./src/catalog.ts#lookup --name lookup_product --schema ./schemas/lookup.json --description "Search the product catalog" --public
```

To declare a tool directly:

```ts
import { defineTool } from 'jase-tunnel/server';
export const lookupProduct = defineTool({
  name: 'lookup_product',
  description: 'Search the product catalog',
  parameters: { type: 'object', properties: { sku: { type: 'string' } }, required: ['sku'], additionalProperties: false },
  readOnly: true,
  // Required unless public: true. These services belong to your application.
  async authorize({ request }, { sku }) {
    const user = await session.fromRequest(request);
    return !!user && catalog.visibleTo(user, sku);
  },
  async execute({ sku }, { request, signal }) {
    return catalog.lookupForRequest(request, sku, { signal });
  }
});
```

Schemas are validated before authorization or execution. `request` includes the website’s request and cookies, `signal` provides cancellation, and `sessionId` is opaque. The SDK adds no credentials of its own. Existing executors must retain application permission checks; these cannot be inferred from source code. Site-level `authorize(request)` can restrict the whole panel, while each tool keeps its own `authorize(context, args)` or `public: true` policy.

For local project-owned tools, `readOnly: true`, `riskLevel: 'read'`, `annotations.readOnlyHint: true` or `--read-only` skips confirmation. Other calls require confirmation bound to the browser, calls and expiry. Discovery executes no tools. `readOnly` never replaces private-data authorization.

## Tools from an MCP server (SDK 0.5)

Reuse MCP tools without rewriting their executors. The adapter runs on the website’s server; the desktop continues to speak `jase-tunnel-v1` and requires the same consent.

MCP is optional. Install the official client only in projects that use it:

```sh
npm install @modelcontextprotocol/client@^2.3.1
```

Add the remote provider alongside local tools in the configuration generated by `init`:

```ts
import { defineSite } from 'jase-tunnel/server';
import { mcpTools } from 'jase-tunnel/mcp';
import { tools } from './.jase-tunnel/tools';

const catalog = mcpTools({
  url: process.env.CATALOG_MCP_URL!, // fixed provider-owned HTTPS endpoint
  tools: {
    search_products: {
      name: 'catalog.search', // remote name; the key is the local model’s tool name
      public: true,          // only for a catalog that anyone may access
      readOnly: true         // explicit provider policy
    }
  }
});

export default defineSite({
  name: 'My shop', origin: process.env.JASE_TUNNEL_ORIGIN,
  tools: async context => [...tools, ...await catalog(context)]
});
```

Only listed tools are exposed. Each policy needs `authorize(context, args)` or `public: true`. Remote `readOnlyHint` annotations never enable or authorize anything. Without explicit `readOnly: true`, the panel asks for confirmation.

For private data, use `headers: async ({ request, signal }) => ...` to obtain upstream credentials from the visitor’s **verified** session, and use `authorize` to check arguments. The adapter never automatically forwards browser cookies or headers and never shares MCP clients or transport sessions between visitors. Do not give public private-data tools an administrative token. Consent to local inference does not grant service permissions.

Supports **Streamable HTTP**, negotiating MCP `2026-07-28` and falling back to compatible `2025` servers, with text and structured JSON results. HTTPS is required except for loopback HTTP services. The endpoint and tool selection come from provider configuration, never visitor input. Redirects are rejected. Each operation has a 15-second deadline (`timeoutMs`, maximum 30 seconds), a total 1 MiB response budget and a 30,000-character result limit.

A fresh connection is closed after each operation. Servers requiring implicit connection state across calls are unsupported; application handles must travel explicitly in arguments and remain authorized by the MCP server. Stdio, legacy HTTP+SSE, sampling, elicitation, Tasks, resources, prompts and multimodal results are not included. Tunnel conversation and hosting limits still apply.

In the repository, run `node packages/sdk/examples/mcp-shop.mjs` after `npm ci --prefix packages/sdk` to open a demo shop at `http://127.0.0.1:3322`, using a real local MCP server and the Tunnel panel. Localhost pairing requires the desktop development build. The demo writes no persistent data; Ctrl-C closes both servers.

## Desktop app and connection

Visitors need **Jase Tunnel 0.3.0 or later**. The panel offers downloads from GitHub releases. Where the browser permits it without prompting, the page announces itself; the app verifies the domain and manifest and displays a notification. **Connect** in the notification resumes pairing in the page. The visitor chooses a model and confirms in the app. Before consent the website receives no model inventory.

Chrome and Edge may require local network access permission before a page contacts the app. The panel does not announce itself automatically when this would trigger a prompt. Use **Connect my model** on the first visit; subsequent visits to the same website can notify automatically.

Connections last up to thirty minutes, stay bound to the selected model and can be revoked from the website or **Disconnect websites** in the app. Revocation also cancels active inference.

Production requires HTTPS. If a proxy changes the request’s host, set `JASE_TUNNEL_ORIGIN=https://your-domain.example`. Set `JASE_TUNNEL_TRUST_PROXY=true` or `trustProxy: true` only behind a proxy that overwrites `X-Forwarded-For`: it lets limits distinguish clients, while trusting an unsanitized header lets callers choose their own identity. Without it, proxied clients share one limit. Localhost requires a development desktop build; stable builds reject local website domains. If the site has a CSP, allow `connect-src http://127.0.0.1:11439` and the panel script from your own origin. The SDK does not loosen policies automatically.

The broker holds state in memory: use **one persistent Node process with request affinity**. **Vercel, Netlify, Cloudflare Workers and other serverless functions are unsupported**, as are multiple workers without affinity. A restart requires pairing again. Panel conversations are not saved to disk. Model-response streaming and automatic integration with arbitrary chatbots are not claimed.

## License

[Apache License 2.0](LICENSE); attribution in [NOTICE](NOTICE). The Jase Tunnel name and logo follow the project’s [trademark policy](https://github.com/Strnozz/jase-tunnel/blob/main/TRADEMARK_POLICY.md).
