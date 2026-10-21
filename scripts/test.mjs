import { spawnSync } from "node:child_process";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const result = spawnSync(process.env.JASE_GO ?? "go", ["test", "-race", "./..."], {
  cwd: root,
  stdio: "inherit",
  env: { ...process.env, GOTOOLCHAIN: "local", GOPROXY: "off", GOSUMDB: "off" }
});
if (result.error) console.error(result.error.message);
if (result.status !== 0) process.exit(result.status ?? 1);
const release = spawnSync(process.execPath, ["--test", "scripts/verify-release.test.mjs", "scripts/windows-resources.test.mjs", "scripts/companion-icons.test.mjs"], { cwd: root, stdio: "inherit" });
if (release.error) console.error(release.error.message);
process.exit(release.status ?? 1);
