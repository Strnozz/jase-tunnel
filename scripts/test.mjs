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
process.exit(result.status ?? 1);
