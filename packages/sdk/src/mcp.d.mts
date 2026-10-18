import type { Tool, ToolContext } from './server.mjs';

export type McpContext = { request: Request; signal: AbortSignal };
export type McpToolPolicy = {
  /** Original MCP name; defaults to the local key. Use a local alias for dots, hyphens or long names. */
  name?: string;
  description?: string;
  /** Explicit owner declaration. Remote readOnlyHint is never trusted automatically. */
  readOnly?: boolean;
  public?: boolean;
  authorize?(context: ToolContext, args: Record<string, unknown>): boolean | Promise<boolean>;
};
export type McpOptions = {
  /** Fixed server-owned HTTPS endpoint. HTTP is accepted only on loopback. */
  url: string | URL;
  /** Only these tools are exposed; each requires authorize or an explicit public declaration. */
  tools: Record<string, McpToolPolicy>;
  /** Explicit upstream credentials. Incoming cookies/headers are never forwarded automatically. */
  headers?: HeadersInit | ((context: McpContext) => HeadersInit | Promise<HeadersInit>);
  /** Per-operation deadline, including discovery. Default 15000; range 100–30000 ms. */
  timeoutMs?: number;
};
/** Optional MCP Streamable HTTP adapter. Requires @modelcontextprotocol/client ^2.3.1. */
export declare function mcpTools(options: McpOptions): (context: McpContext) => Promise<Tool[]>;
