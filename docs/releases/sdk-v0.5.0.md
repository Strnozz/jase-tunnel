# SDK 0.5.0: optional MCP tools

This release updates only the `jase-tunnel` npm SDK. The desktop app stays compatible with `jase-tunnel-v1`; no new desktop release is required to use MCP tools.

- New `mcpTools` export from `jase-tunnel/mcp`, including TypeScript declarations.
- Optional official `@modelcontextprotocol/client ^2.3.1`, installed only by providers choosing MCP.
- Streamable HTTP with MCP 2026-07-28 negotiation and fallback to compatible 2025 servers.
- Explicit tool selection, local aliases and `authorize` or `public` policies. `readOnly` requires a provider declaration.
- Per-request upstream credentials, without automatic browser cookie forwarding or shared MCP clients between visitors.
- JSON Schema 2020-12, text/JSON results, deadlines, cancellation and size limits. Redirects are rejected.
- A demo shop using the official MCP server and tests of the full local-model tool and confirmation flow.

Additional MCP capabilities, implicit connection state across calls and multimodal content are not included. See the [SDK README](https://github.com/Strnozz/jase-tunnel/blob/sdk-v0.5.0/packages/sdk/README.md).

The SDK workflow stages the package with GitHub provenance. It never publishes directly; the owner must approve with 2FA.
