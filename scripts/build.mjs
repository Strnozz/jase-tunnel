import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, writeFileSync, chmodSync, rmSync, readdirSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { tmpdir } from "node:os";
import { mkdtempSync } from "node:fs";
import { companionIcons } from "./companion-icons.mjs";

const args = process.argv.slice(2), option = name => { const at = args.indexOf(name); return at < 0 ? undefined : args[at + 1]; };
// Development packages are separate: a localhost download must never install
// the production app, which deliberately rejects loopback website origins.
const dev = args.includes("--dev");
const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const output = resolve(root, option("--out") ?? (dev ? "dist-dev" : "dist")), go = process.env.JASE_GO ?? "go";
// A Mac bundle must be signed on macOS (codesign); one built elsewhere is reported "damaged"
// by Gatekeeper once downloaded. CI builds only on a macOS runner.
if (process.env.GITHUB_ACTIONS === "true" && process.platform !== "darwin") throw new Error("Desktop builds run on the macOS runner");
if (process.platform !== "darwin") throw new Error("The macOS menu-bar app must be built on macOS with Xcode Command Line Tools.");
const version = readFileSync(join(root, "VERSION"), "utf8").trim();
const run = (command, arguments_, options = {}) => {
  const result = spawnSync(command, arguments_, { stdio: "inherit", ...options });
  if (result.error || result.status !== 0) throw new Error(`${command} failed: ${result.error?.message ?? result.status}`);
};
// An exported copy may live inside another project's checkout. Never attribute its
// binaries to a parent repository; use Git metadata only after it has its own .git.
const ownRepository = existsSync(join(root, ".git"));
const git = arguments_ => spawnSync("git", arguments_, { cwd: root, encoding: "utf8" });
const head = ownRepository ? git(["rev-parse", "HEAD"]) : undefined;
const revision = head?.status === 0 ? head.stdout.trim() : "local";
// Build from the reviewed, checked-in icon resources. Re-encoding them here can
// change bytes across libvips CPU architectures and falsely dirty the checkout.
const status = ownRepository ? git(["status", "--porcelain"]) : undefined;
const dirty = revision === "local" || status?.status !== 0 || !!status?.stdout.trim();
const sources = [...readdirSync(root).filter(name => /\.(?:go|m|syso)$/.test(name) || name === "VERSION" || name === "go.mod" || name === "go.sum").map(name => join(root, name)), ...["assets/jase-logo.png", "assets/windows.manifest", "assets/desktop-icon.png", "scripts/windows-resources.mjs", "scripts/companion-icons.mjs", "scripts/build.mjs", "LICENSE", "NOTICE", "THIRD_PARTY_NOTICES.md", "package.json", "package-lock.json"].map(name => join(root, name))];
sources.push(join(root, "web_bridge.html"));
const sourceHash = createHash("sha256");
for (const path of sources.sort()) sourceHash.update(readFileSync(path));
const source = sourceHash.digest("hex").slice(0, 12);
const build = `${version}+${revision.slice(0, 12)}${dirty ? `.dev.${source}` : ""}`;
const temporary = mkdtempSync(join(tmpdir(), "jase-tunnel-build-"));
mkdirSync(output, { recursive: true });
const artifacts = [];
function compile(os, arch, target) {
  const flags = `-s -w -X main.version=${build}${dev ? " -X main.devBuild=true" : ""}${os === "windows" ? " -H windowsgui" : ""}`;
  run(go, ["build", "-trimpath", "-buildvcs=false", "-ldflags", flags, "-o", target, "."], { cwd: root,
    env: { ...process.env, CGO_ENABLED: os === "darwin" ? "1" : "0", ...(os === "darwin" ? { CC: "/usr/bin/clang", MACOSX_DEPLOYMENT_TARGET: "13.0", CGO_CFLAGS: `-arch ${arch === "amd64" ? "x86_64" : "arm64"} -mmacosx-version-min=13.0`, CGO_LDFLAGS: `-arch ${arch === "amd64" ? "x86_64" : "arm64"} -mmacosx-version-min=13.0` } : {}), GOTOOLCHAIN: "local", GOPROXY: "off", GOSUMDB: "off", GOOS: os, GOARCH: arch } });
}
function record(id, filename) {
  const content = readFileSync(join(output, filename));
  artifacts.push({ id, filename, size: content.length, sha256: createHash("sha256").update(content).digest("hex") });
}
function universal(thinX64, thinArm, target) {
  // Standard Mach-O FAT header; each Go slice retains its own ad-hoc signature.
  const x64 = readFileSync(thinX64), arm = readFileSync(thinArm), alignment = 16384;
  const first = alignment, second = Math.ceil((first + x64.length) / alignment) * alignment;
  const bytes = Buffer.alloc(second + arm.length); bytes.writeUInt32BE(0xcafebabe, 0); bytes.writeUInt32BE(2, 4);
  [[0x01000007, 3, first, x64], [0x0100000c, 0, second, arm]].forEach(([cpu, subtype, offset, content], i) => {
    const at = 8 + i * 20; [cpu, subtype, offset, content.length, 14].forEach((n, j) => bytes.writeUInt32BE(n, at + j * 4)); content.copy(bytes, offset);
  });
  writeFileSync(target, bytes); chmodSync(target, 0o755);
}
const zip = `import pathlib,sys,zipfile\nroot=pathlib.Path(sys.argv[1])\nwith zipfile.ZipFile(sys.argv[2],'w',zipfile.ZIP_DEFLATED) as output:\n for path in sorted(root.rglob('*')): output.write(path,path.relative_to(root.parent))\n`;
try {
  run(go, ["test", "./..."], { cwd: root, env: { ...process.env, GOTOOLCHAIN: "local", GOPROXY: "off", GOSUMDB: "off" } });
  for (const arch of ["amd64", "arm64"]) {
    const name = arch === "amd64" ? "x64" : arch;
    const windows = `Jase-Tunnel-Windows-${name}.exe`; compile("windows", arch, join(output, windows)); record(`windows-${name}`, windows);
    const dir = join(temporary, `linux-${name}`, "Jase-Tunnel"); mkdirSync(dir, { recursive: true }); compile("linux", arch, join(dir, "jase-tunnel")); chmodSync(join(dir, "jase-tunnel"), 0o755);
    writeFileSync(join(dir, "README.txt"), `Jase Tunnel

Extract this directory and run jase-tunnel to open the native panel with models, connections and the activity log. GTK 3 is required (libgtk-3-0 or libgtk-3-0t64 on Ubuntu/Debian).

As you browse, compatible pages announce themselves and you receive a notification (requires notify-send from libnotify-bin). Connections start only after you choose a model in the panel and last up to 30 minutes.

The tray menu offers Open Jase Tunnel, Add weights and Quit Jase Tunnel. Ayatana AppIndicator is recommended. On desktops without tray icons, closing the window minimizes it to the taskbar. Choose Quit Jase Tunnel in the panel or menu to stop the process.

For terminal diagnostics: ./jase-tunnel --headless --foreground
Models run through Ollama or LM Studio on your computer. No account or cloud service is required.
`);
    mkdirSync(join(dir,"LICENSES"),{recursive:true});
    writeFileSync(join(dir,"LICENSES","LICENSE.txt"),readFileSync(join(root,"LICENSE")));
    writeFileSync(join(dir,"LICENSES","NOTICE.txt"),readFileSync(join(root,"NOTICE")));
    writeFileSync(join(dir,"LICENSES","purego.txt"),readFileSync(join(root,"third_party/purego-LICENSE")));
    writeFileSync(join(dir,"LICENSES","THIRD_PARTY_NOTICES.txt"),readFileSync(join(root,"THIRD_PARTY_NOTICES.md")));
    const linux = `Jase-Tunnel-Linux-${name}.tar.gz`; run("tar", ["-czf", join(output, linux), "-C", join(temporary, `linux-${name}`), "Jase-Tunnel"]); record(`linux-${name}`, linux);
  }
  const app = join(temporary, "Jase Tunnel.app"), contents = join(app, "Contents"), binary = join(contents, "MacOS", "Jase Tunnel"); mkdirSync(join(contents, "MacOS"), { recursive: true });
  const intel = join(temporary, "mac-intel"), arm = join(temporary, "mac-arm"); compile("darwin", "amd64", intel); compile("darwin", "arm64", arm); universal(intel, arm, binary);
  const resources = join(contents,"Resources"), iconSet = join(temporary,"Jase.iconset"); mkdirSync(resources,{recursive:true});
  await companionIcons(join(root,"assets/jase-logo.png"),resources,iconSet);
  for (const name of ["LICENSE", "NOTICE"]) writeFileSync(join(resources, `${name}.txt`), readFileSync(join(root, name)));
  run("/usr/bin/iconutil",["-c","icns",iconSet,"-o",join(resources,"Jase.icns")]);
  writeFileSync(join(contents, "Info.plist"), `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>CFBundleExecutable</key><string>Jase Tunnel</string><key>CFBundleIdentifier</key><string>io.github.strnozz.jase-tunnel</string><key>CFBundleName</key><string>Jase Tunnel</string><key>CFBundleDisplayName</key><string>Jase Tunnel</string><key>CFBundleIconFile</key><string>Jase.icns</string><key>CFBundlePackageType</key><string>APPL</string><key>CFBundleShortVersionString</key><string>${version}</string><key>CFBundleVersion</key><string>${version}</string><key>LSMinimumSystemVersion</key><string>13.0</string><key>NSAppTransportSecurity</key><dict><key>NSAllowsLocalNetworking</key><true/></dict><key>NSLocalNetworkUsageDescription</key><string>Connect local models to websites you approve.</string></dict></plist>`);
  const signed = process.platform === "darwin";
  if (signed) {
    run("/usr/bin/codesign", ["--force", "--deep", "--sign", "-", app]);
    run("/usr/bin/codesign", ["--verify", "--deep", "--strict", app]);
    // ditto keeps the bundle exactly as signed (the Apple-recommended archive for apps).
    run("/usr/bin/ditto", ["-c", "-k", "--keepParent", app, join(output, "Jase-Tunnel-macOS.zip")]);
  } else {
    console.warn("Warning: Mac bundle not signed (codesign needs macOS); for local tests only.");
    run("python3", ["-c", zip, app, join(output, "Jase-Tunnel-macOS.zip")]);
  }
  record("macos-universal", "Jase-Tunnel-macOS.zip");
  writeFileSync(join(output, "manifest.json"), JSON.stringify({ protocol: "jase-tunnel-v1", version, build, commit: revision, dirty, run: process.env.JASE_TUNNEL_RELEASE_RUN_URL ?? "local", signing: dev ? "development-loopback" : "development", macSigned: signed, artifacts }, null, 2) + "\n");
  writeFileSync(join(output, "SHA256SUMS"), artifacts.map(a => `${a.sha256}  ${a.filename}`).join("\n") + "\n");
  console.log(`Built ${artifacts.length} downloads for Windows, macOS and Linux in ${output}`);
} finally { rmSync(temporary, { recursive: true, force: true }); }
