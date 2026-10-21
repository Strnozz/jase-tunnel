import { spawnSync } from "node:child_process";
import { mkdir, readFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { desktopIcon } from "./companion-icons.mjs";
import { windowsResources } from "./windows-resources.mjs";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const dev = process.argv.includes("--dev");
const output = join(root, dev ? "dist-dev" : "dist");
await mkdir(output, { recursive: true });
await desktopIcon(join(root, "assets/jase-logo.png"), join(root, "assets/desktop-icon.png"));
await windowsResources(root);
const version = (await readFile(join(root, "VERSION"), "utf8")).trim();
for (const arch of ["amd64", "arm64"]) {
  const target = join(output, `Jase-Tunnel-Windows-${arch === "amd64" ? "x64" : arch}.exe`);
  const result = spawnSync(process.env.JASE_GO ?? "go", ["build", "-trimpath", "-buildvcs=false", "-ldflags",
    `-s -w -H windowsgui -X main.version=${version}+local${dev ? " -X main.devBuild=true" : ""}`, "-o", target, "."], {
    cwd: root, stdio: "inherit", env: { ...process.env, CGO_ENABLED: "0", GOOS: "windows", GOARCH: arch }
  });
  if (result.error || result.status !== 0) throw result.error ?? new Error(`Go build failed (${result.status})`);
  console.log(target);
}
