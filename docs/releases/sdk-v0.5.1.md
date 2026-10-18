# SDK 0.5.1: MCP tools and an English interface

This is the review package for the optional MCP integration and the English translation. It supersedes the earlier 0.5.0 staging candidate.

- `mcpTools` from `jase-tunnel/mcp` reuses selected tools from an MCP Streamable HTTP server without changing `jase-tunnel-v1`.
- Explicit per-tool permissions, optional official MCP client, isolated credentials, deadlines, cancellation and bounded results.
- English README, integration guide, installer messages, web panel, errors and example shop.
- Compatible with Jase Tunnel 0.3.0 or later. Desktop 0.3.3 adds the English native interface, notifications and activity log.

Validated with the complete SDK suite, real MCP 2026/2025 servers, the local-model confirmation flow, native desktop regression tests and installation of the npm tarball in an independent project.

The package is staged on npm with GitHub provenance and requires owner approval with 2FA. Approve **0.5.1**, rather than the superseded 0.5.0 candidate. The workflow never publishes directly.

[Configuration and limitations](https://github.com/Strnozz/jase-tunnel/blob/sdk-v0.5.1/packages/sdk/README.md#tools-from-an-mcp-server-sdk-05).
