import { adaptTool } from './tools.mjs';

const MAX_BYTES = 1024 * 1024;
const MAX_TOOLS = 100;
const MAX_RESULT = 30_000;
const version = '0.5.0';

function abortable(promise, signal) {
  signal.throwIfAborted();
  return new Promise((resolve, reject) => {
    const abort = () => reject(signal.reason);
    signal.addEventListener('abort', abort, { once: true });
    promise.then(resolve, reject).finally(() => signal.removeEventListener('abort', abort));
  });
}

/** Explicitly selected remote tools; the endpoint and policies belong to the site. */
export function mcpTools(options) {
  const url = new URL(options.url);
  const loopback = ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname);
  if (url.username || url.password || url.hash || !(url.protocol === 'https:' || url.protocol === 'http:' && loopback)) {
    throw new Error('MCP: usa HTTPS (HTTP è ammesso solo su loopback), senza credenziali o frammenti nell’URL.');
  }
  const timeoutMs = options.timeoutMs ?? 15_000;
  if (!Number.isSafeInteger(timeoutMs) || timeoutMs < 100 || timeoutMs > 30_000) throw new Error('MCP: timeoutMs deve essere tra 100 e 30000.');
  if (!options.tools || typeof options.tools !== 'object' || Array.isArray(options.tools)) throw new Error('MCP: specifica gli strumenti da abilitare.');
  const selected = Object.entries(options.tools).map(([name, rule]) => {
    if (!rule || typeof rule !== 'object' || Array.isArray(rule)) throw new Error('MCP: regola dello strumento non valida.');
    const remoteName = rule.name ?? name;
    if (typeof remoteName !== 'string' || !remoteName || remoteName.length > 128) throw new Error('MCP: nome remoto non valido.');
    // Check local names and require an explicit public/authorize policy before any I/O.
    adaptTool({ name, description: rule.description ?? name, parameters: { type: 'object' }, public: rule.public === true,
      authorize: rule.authorize, execute() {} });
    return { name, remoteName, rule: { ...rule } };
  });
  if (!selected.length || selected.length > MAX_TOOLS || new Set(selected.map(t => t.remoteName)).size !== selected.length) {
    throw new Error('MCP: abilita da 1 a 100 strumenti remoti distinti.');
  }

  async function connected(context, run) {
    const signal = AbortSignal.any([context.signal, AbortSignal.timeout(timeoutMs)]);
    let client;
    try {
      signal.throwIfAborted();
      const headers = new Headers(await abortable(Promise.resolve(typeof options.headers === 'function'
        ? options.headers({ request: context.request, signal }) : options.headers), signal));
      for (const name of headers.keys()) {
        if (['host', 'content-length', 'connection'].includes(name) || name.startsWith('mcp-')) throw new Error('MCP: header riservato.');
      }
      const { Client, StreamableHTTPClientTransport } = await import('@modelcontextprotocol/client');
      let bytes = 0;
      const limitedFetch = async (input, init = {}) => {
        const target = new URL(typeof input === 'string' || input instanceof URL ? input : input.url);
        if (target.href !== url.href) throw new Error('MCP: destinazione non consentita.');
        const response = await fetch(input, { ...init, redirect: 'error',
          signal: AbortSignal.any([signal, ...(init.signal ? [init.signal] : [])]) });
        if (!response.body) return response;
        const body = response.body.pipeThrough(new TransformStream({ transform(chunk, controller) {
          bytes += chunk.byteLength;
          if (bytes > MAX_BYTES) throw new Error('MCP: risposta troppo grande.');
          controller.enqueue(chunk);
        } }));
        return new Response(body, { status: response.status, statusText: response.statusText, headers: response.headers });
      };
      client = new Client({ name: 'jase-tunnel', version }, {
        capabilities: {}, versionNegotiation: { mode: 'auto' }, listMaxPages: 20
      });
      const transport = new StreamableHTTPClientTransport(url, { fetch: limitedFetch, requestInit: { headers, redirect: 'error' } });
      await client.connect(transport, { signal, timeout: timeoutMs });
      return await run(client, { signal, timeout: timeoutMs });
    } catch (error) {
      if (error?.code === 'ERR_MODULE_NOT_FOUND' && error.message?.includes('@modelcontextprotocol/client')) {
        throw new Error('MCP: installa @modelcontextprotocol/client@^2.3.1 nel progetto del sito.');
      }
      // Never put upstream URLs, headers, credentials or raw transport errors in model results.
      if (signal.aborted) throw signal.reason;
      throw new Error('MCP: servizio, autorizzazione o formato della risposta non disponibili.', { cause: error });
    } finally {
      // A fresh client per operation prevents credentials and transport sessions crossing visitors.
      if (client) await client.close().catch(() => {});
    }
  }

  return async function tools(context) {
    const catalog = await connected(context, (client, requestOptions) => client.listTools({}, requestOptions));
    if (catalog.tools.length > 1000) throw new Error('MCP: catalogo troppo grande.');
    const names = catalog.tools.map(t => t.name);
    if (new Set(names).size !== names.length) throw new Error('MCP: nomi remoti duplicati.');
    return selected.map(({ name, remoteName, rule }) => {
      const remote = catalog.tools.find(t => t.name === remoteName);
      if (!remote) throw new Error(`MCP: strumento configurato non disponibile: ${name}.`);
      const parameters = { $schema: 'https://json-schema.org/draft/2020-12/schema', ...remote.inputSchema };
      const tool = {
        name, description: rule.description ?? remote.description ?? remote.title ?? name, parameters,
        // Remote readOnlyHint is descriptive, never a substitute for the owner's policy.
        readOnly: rule.readOnly === true, public: rule.public === true, authorize: rule.authorize,
        async execute(args, callContext) {
          const result = await connected(callContext, (client, requestOptions) => client.callTool(
            { name: remoteName, arguments: args }, { ...requestOptions, toolDefinition: remote }));
          if (!Array.isArray(result.content) || result.content.some(c => c.type !== 'text' || typeof c.text !== 'string')) {
            throw new Error('MCP: sono supportati solo risultati testuali e JSON strutturati.');
          }
          const value = { content: result.content.map(c => ({ type: 'text', text: c.text })),
            ...(result.structuredContent !== undefined ? { structuredContent: result.structuredContent } : {}),
            ...(result.isError === true ? { isError: true } : {}) };
          if (JSON.stringify(value).length > MAX_RESULT) throw new Error('MCP: risultato troppo grande.');
          return value;
        }
      };
      adaptTool(tool); // Reject incompatible schemas before exposing tools to the model.
      return tool;
    });
  };
}
