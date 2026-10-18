# Architecture

Jase Tunnel is a standalone app that connects a person’s local LLM to websites using the `jase-tunnel` SDK, after explicit consent. It has no predefined domains or central service dependency. This repository distributes both the desktop app and website SDK (`packages/sdk`).

## Components

1. **Website:** `npx jase-tunnel init` publishes `/.well-known/jase-tunnel.json`, mounts the broker at `/api/jase-tunnel/v1/` and adds the panel.
2. **Page:** contacts `http://127.0.0.1:11439/tunnel/v1/discover` automatically only when the browser will not prompt. Otherwise contact starts through the panel button.
3. **App:** verifies the manifest on the domain from the `Origin` header, using public IPs only and no redirects. A compatible site can trigger “example.com supports Jase Tunnel.”
4. **Consent:** **Connect** in the notification or page resumes pairing. The page requests a ticket from its own server and delivers it to the app. The app shows the verified domain, data exchange and model selector. Only after confirmation does it exchange the ticket for a grant lasting at most thirty minutes, bound to the chosen model.
5. **Transport:** the app long-polls the website’s broker, runs inference through Ollama or LM Studio, and returns text and tool requests. The website’s server authorizes and executes tools. The app does not execute commands received from the model.

The protocol is `jase-tunnel-v1`; see [WEB-STANDARD.md](WEB-STANDARD.md). No third-party relay or inbound router port is required.

## Optional MCP integration

SDK 0.5 exports `mcpTools` from `jase-tunnel/mcp`. The website’s server uses the optional official client to query a provider-configured endpoint, selects enabled tools and forwards authorized calls. Neither the desktop nor browser receives MCP credentials or changes protocol. The MCP server remains responsible for its own data and permissions.

The provider explicitly sets `authorize`/`public` and `readOnly`; remote annotations do not set those policies. Catalogs and results have size limits, operations expire and respect cancellation, redirects are rejected, and clients close after each operation to avoid sharing transport sessions between visitors. Streamable HTTP tools with text or structured JSON results are supported. Implicit state across connections and additional MCP features require separate integration. See [configuration and example](../packages/sdk/README.md#tools-from-an-mcp-server-sdk-05).

## Desktop interfaces

macOS uses AppKit, Windows uses Win32, and Linux loads GTK 3 through `purego`. Engine and panel share a process. Panels read sanitized snapshots without credentials or weight paths; the activity log has no HTTP API.

| System | Compatible-site notification | Connect action |
| --- | --- | --- |
| macOS | Notification Center (UserNotifications), from the app bundle | Connect button or notification click |
| Windows | Tray balloon / Windows 10–11 notification | Notification click |
| Linux | `notify-send` through the desktop notification service (`libnotify-bin`) | Connect where notification actions are supported |

Limits: one notification per website per day, three every ten minutes, none for an already connected website. Announcements also remain in the panel log when notifications are disabled. macOS requires the app to be registered in Applications or `~/Applications`; a temporary app or development binary records announcements in the panel when Notification Center refuses permission.

The tray menu offers **Open Jase Tunnel**, **Add weights** and **Quit Jase Tunnel**. **Disconnect websites** revokes all grants and cancels inference. Closing the window keeps the tray service running; quitting stops the app engine and its inference requests.

The stored panel identity is `appkit-v1`, `win32-v1` or `gtk3-v1`. Relaunching the same version and mode opens the existing instance. A different version, including 0.2 and earlier, asks before restarting it.

## Local service surfaces

| Endpoint | Caller | Purpose |
| --- | --- | --- |
| `POST /tunnel/v1/discover` | Eligible public HTTPS website | Announces the page; returns a five-minute receipt |
| `GET /tunnel/v1/discovery` | Page with its own receipt | Reads its discovery state |
| `POST /tunnel/v1/pair` | Page with its own receipt | Delivers a ticket and opens consent |
| `GET /` and `GET /v1/status` | Local status page or process | Version and model count |
| `POST /v1/show-window`, `/v1/add-model`, `/v1/quit` | Local status page only | Panel, file chooser and shutdown |

The service checks `Host` to resist DNS rebinding. `/v1/` rejects browser origins other than the local status page. `/tunnel/v1/` never returns model inventories, files, activity logs or other connections.

## Local engines and data

- Ollama: `127.0.0.1:11434`.
- LM Studio: `127.0.0.1:1234`, with its local API enabled. An optional `JASE_LMSTUDIO_TOKEN` stays on this computer. The tunnel maintains at most one of its own instances per model. Increasing context replaces that instance without unloading instances opened by the user, and never exceeds the model’s advertised context capacity.
- Ollama cloud models are excluded. Failure of a local model never selects a remote provider.
- Scanning is limited to standard model directories and optional `OLLAMA_MODELS`; the app reads neither browser tabs nor entire disks.
- Private state lives under `Jase Tunnel` in the user configuration directory. `--state-dir` selects an isolated test location. Startup retains only unexpired grants for the current protocol.
- Logging prompt and response text is optional. `--debug` may write it to local logs.

## Browser limits

A normal desktop app cannot see visited URLs; Jase Tunnel does not read tabs. The page must contact it. Chrome and Edge can require Local Network Access permission before a public page contacts a local service, even when the app is absent. Automatic announcements happen only when permission is already granted, or the browser has no such permission and the visitor previously connected on that website. Otherwise the panel button starts the first contact. Subsequent visits to that website can notify automatically.
