# Jase Tunnel

A standalone desktop app that connects the LLM running on your computer (Ollama or LM Studio) to websites that support it. No account, third-party relay or browser extension.

## How it works

**For visitors:** download and open Jase Tunnel. The executables are unsigned, so your operating system will ask for confirmation; see [Unsigned executables](#unsigned-executables). When a compatible page announces itself, you receive a system notification: “example.com supports Jase Tunnel.” Choose **Connect**, then approve the domain and model in the app. The website’s AI tools can now use your local LLM. Connections last up to thirty minutes; **Disconnect websites** ends them immediately.

**For website providers:** run one command in your project directory:

```sh
npx jase-tunnel init
```

`init` installs `jase-tunnel`, discovers the project’s exported AI tools and registers them as **disabled** until you enable them. It publishes `/.well-known/jase-tunnel.json`, mounts the broker and adds the panel to your pages. Next.js App Router, Express and `node:http` servers are configured automatically. See the [SDK README](packages/sdk/README.md).

The model runs on the visitor’s computer, so the visitor controls which tools are called and with which arguments. Every enabled tool is effectively a public website API. Tools must check `authorize(context, args)` or be explicitly declared `public`.

The app does not read browser tabs or contain predefined domains. Compatible pages contact it, and access to a model requires approval in the app. See [the protocol](docs/WEB-STANDARD.md) and [architecture](docs/ARCHITECTURE.md).

### First contact with a website

Chrome and Edge ask visitors before a page contacts an app on their computer, even when that app is not installed. To avoid prompting people who do not use Jase Tunnel, automatic announcements run only when no browser prompt can appear: permission is already granted, or the browser has no such permission and the visitor previously connected on that website. Use the page’s **Jase Tunnel** button the first time; subsequent visits to that website can trigger notifications automatically.

## What is included

- A Go engine for Windows, macOS and Linux, on Intel/AMD and ARM.
- Native interfaces: AppKit on macOS, Win32 on Windows and GTK 3 on Linux. Menu-bar or tray icon with **Open Jase Tunnel**, **Add weights** and **Quit Jase Tunnel**.
- Website notifications through Notification Center, the Windows tray or Linux `notify-send`. At most one per website per day and three every ten minutes. Every announcement also appears in the panel log.
- Detection of Ollama, LM Studio and weights in standard model directories, with manual file selection.
- Connection status and a private activity log with filters and selectable details.
- The website SDK (`packages/sdk`, npm package `jase-tunnel`): installer, broker, panel and Next.js, Express and `node:http` adapters.
- Optional MCP tools through `jase-tunnel/mcp` (SDK 0.5). The provider selects a server’s tools, keeps credentials and authorization on its own server, and makes those tools available to the visitor’s local model. See [MCP configuration](packages/sdk/README.md#tools-from-an-mcp-server-sdk-05).

On all three systems the panel remains available while the app works in the background. **Quit Jase Tunnel** stops the tunnel service and its running inference requests, leaving Ollama and LM Studio open. Launching the executable again brings the existing instance forward.

On macOS, move **Jase Tunnel** to Applications or `~/Applications` before opening it: Notification Center accepts registered apps in those locations. The first compatible website triggers the notification permission request.

On Windows, the tray icon may appear under hidden icons. Double-click opens the panel; right-click opens its menu. Closing the window keeps the tunnel running in the tray.

On Linux, install GTK 3 (`libgtk-3-0` or `libgtk-3-0t64` on Ubuntu/Debian) and `notify-send` (`libnotify-bin`) for notifications. The app uses Ayatana AppIndicator when available, or the traditional GTK tray icon. Without a tray, closing the window minimizes it to the taskbar.

Ollama or LM Studio must be installed and running. Engines and model weights are not included.

**Safari:** use app **0.3.4 or later** and website SDK **0.5.2 or later**. Clicking **Connect my model** opens a small local connection window; keep it open while choosing the model and confirming the domain in the app. It closes after pairing. Safari blocks the direct HTTPS-to-HTTP loopback request used by earlier versions. This explicit window requires no certificate installation or change to browser security settings. Safari does not announce pages automatically: reconnect through the website button. Chrome and Edge keep the direct connection and may ask for local network permission.

## Unsigned executables

**Jase Tunnel packages are unsigned.** The macOS bundle has an ad-hoc signature, without Apple Developer ID signing or notarization. Windows executables have no Authenticode signature. Your operating system cannot verify the publisher or integrity through a publisher signature and may identify the software as coming from an unknown developer.

**Opening Jase Tunnel means accepting this security risk on your computer.** The app runs in the background, listens on `127.0.0.1:11439` and uses local models. Install it only if you trust the project, and download it from the [official releases](https://github.com/Strnozz/jase-tunnel/releases).

For the first launch:

- **macOS:** open the app, dismiss the warning, then go to **System Settings → Privacy & Security** and select **Open Anyway** next to Jase Tunnel. On macOS 14 and earlier, right-click → **Open** → **Open** is also available.
- **Windows:** in the SmartScreen warning, select **More info → Run anyway**. Antivirus software may flag or quarantine the executable.
- **Linux:** extract the unsigned `.tar.gz` archive and run the executable.

Compare the download against `SHA256SUMS` from the same release: `shasum -a 256 <file>` on macOS, `sha256sum <file>` on Linux or `Get-FileHash <file>` in PowerShell. Checksums detect corruption or replacement during download; they do not protect against a release published by a compromised account. A trusted publisher signature would provide that additional assurance.

## Development

The Windows engine and UI use Go’s standard library and native APIs. Linux uses `purego` to load GTK without CGO; its version is pinned in `go.mod` and `go.sum`, with the license in `third_party/`. Minimum Go version: 1.25; verified toolchain: 1.27.2.

```sh
go mod download
go test -race ./...
```

Race detection requires a C compiler. CI also exercises the GTK panel in a Linux Xvfb session and checks the native Windows APIs. macOS requires Xcode Command Line Tools.

Only development builds accept HTTP websites on `localhost`. To run one locally:

```sh
go run -ldflags "-X main.devBuild=true" . --foreground --port 11440 --state-dir .deploy-check/state
```

The alternative port avoids an existing app instance. The website module uses port `11439`, so a full pairing test requires closing the other instance and omitting `--port`. The state directory contains private credentials and is excluded from Git. macOS notifications require an app bundle (`npm run build:dev`); a plain `go run` process records announcements in the panel instead.

## Building packages

All platform packages are built on macOS with Xcode Command Line Tools, Go and Node.js 22 or later. Node and `sharp` generate icons during the build; end users do not need them.

```sh
npm ci
npm test
npm run build:dev
```

`JASE_GO` can select a different Go compiler. The build writes five downloads to `dist-dev/`: Windows x64/ARM64, Linux x64/ARM64 and a universal macOS app, with `manifest.json` and `SHA256SUMS`. For the build that accepts only public HTTPS websites:

```sh
npm run build
```

This writes to `dist/`; `--out <directory>` selects another location. The macOS bundle has an ad-hoc signature. Developer ID signing, Apple notarization and Windows signing are not configured; see [Unsigned executables](#unsigned-executables).

## GitHub and npm releases

Executable downloads are hosted in [Strnozz/jase-tunnel](https://github.com/Strnozz/jase-tunnel/releases). Website panels select their platform’s download from public releases. `release.yml` runs when a tag matches `VERSION`:

- `v0.3.4`: the stable-channel build for public HTTPS websites.
- `dev-v0.3.4-preview1`: a development prerelease that also accepts `localhost`.

CI checks all three operating systems, builds the five packages on macOS, verifies the commit, run, channel, inventory and SHA-256 checksums, then uploads them to a **draft release for owner approval**. Publishing the draft makes the downloads available to website panels. Existing public releases are never overwritten.

`sdk.yml` prepares the website SDK for tags `sdk-vX.Y.Z`, attaches the immutable package to its GitHub release and uses the repository’s `NPM_TOKEN` to stage it on npm. **The workflow never publishes directly.** The owner approves the exact staged package with 2FA on npm or with `npm stage approve <stage-id>`. Until approval, the new version is unavailable through the public npm registry. The token must have the package permissions required for staging.

To retry after the GitHub SDK release was created, run **Check and package Jase Tunnel SDK** manually in Actions and supply the existing `release_tag`. The workflow rebuilds and compares the package and checksums against the published attachments before retrying npm. It never replaces release files.

Before a version is public on npm, providers can use its `.tgz` package through `npx --yes --package <package.tgz> jase-tunnel init --package <package.tgz>`.

## Repository layout

| Path | Contents |
| --- | --- |
| `*.go`, `desktop_darwin.m` | Engine, protocol, native interfaces and tests |
| `packages/sdk` | npm package: installer, broker, web panel and adapters |
| `assets/icon.svg` | Icon source |
| `assets/desktop-icon.png` | Icon embedded in Windows/Linux executables |
| `scripts/` | Builds, icon generation, release verification and tests |
| `.github/workflows/` | Windows, macOS and Linux checks and releases |
| `docs/` | Architecture and `jase-tunnel-v1` protocol |
| `VERSION` | Desktop app version |
| `LICENSE`, `NOTICE` | Apache 2.0 license and attribution notices |
| `TRADEMARK_POLICY.md` | Rules for the Jase Tunnel name and logo |

## License and trademarks

Jase Tunnel is open source under the [Apache License 2.0](LICENSE). The desktop app, website SDK and protocol specification may be used, modified and redistributed, including commercially, provided that the required [`LICENSE`](LICENSE) and [`NOTICE`](NOTICE) notices are retained and modified files are identified. The license includes an express patent grant from contributors.

The name “Jase Tunnel” and its logo are separate from the code license. Their use follows the [trademark policy](TRADEMARK_POLICY.md). Modified distributions may use `Jase Tunnel - Exp<DistributionName>` with a clear disclosure of modifications, or choose a different name. Independent implementations may describe themselves as compatible with `jase-tunnel-v1`.

Third-party components included in builds are listed in [`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md).
