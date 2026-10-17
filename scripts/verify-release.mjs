import { createHash } from "node:crypto";
import { lstatSync, readFileSync, readdirSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const artifacts = {
  "windows-x64": "Jase-Windows-x64.exe", "windows-arm64": "Jase-Windows-arm64.exe",
  "macos-universal": "Jase-macOS.zip", "linux-x64": "Jase-Linux-x64.tar.gz", "linux-arm64": "Jase-Linux-arm64.tar.gz"
};
export function releaseTag(tag, version) {
  const match = /^(dev-)?v(\d+\.\d+\.\d+)(?:-([A-Za-z0-9.-]+))?$/.exec(tag);
  if (tag.length > 100 || !match || match[2] !== version || (!match[1] && match[3])) throw new Error("Release tag must match VERSION (vX.Y.Z or dev-vX.Y.Z[-suffix])");
  return match[1] ? "development" : "stable";
}
export function verifyRelease(directory, { tag, version, commit, run }) {
  const channel = releaseTag(tag, version);
  if (!/^[a-f0-9]{40}$/.test(commit) || !/^https:\/\/github\.com\/Strnozz\/jase-tunnel\/actions\/runs\/\d+$/.test(run)) throw new Error("Invalid release provenance");
  const expected = [...Object.values(artifacts), "manifest.json", "SHA256SUMS"].sort();
  if (JSON.stringify(readdirSync(directory).sort()) !== JSON.stringify(expected)) throw new Error("Unexpected release inventory");
  for (const name of expected) {
    const info = lstatSync(join(directory,name));
    if (!info.isFile() || info.isSymbolicLink() || info.size < 1 || info.size > 150 * 1024 ** 2) throw new Error("Invalid release file");
  }
  if (lstatSync(join(directory,"manifest.json")).size > 64000) throw new Error("Oversized release manifest");
  const manifest = JSON.parse(readFileSync(join(directory,"manifest.json"),"utf8"));
  if (manifest.protocol !== "jase-companion-v1" || manifest.version !== version || manifest.commit !== commit || manifest.run !== run
      || manifest.dirty !== false || manifest.macSigned !== true || manifest.signing !== (channel === "development" ? "development-loopback" : "development")
      || manifest.build !== `${version}+${commit.slice(0,12)}`) throw new Error("Release build does not match its commit, run or channel");
  if (!Array.isArray(manifest.artifacts) || manifest.artifacts.length !== 5 || new Set(manifest.artifacts.map(a=>a?.id)).size !== 5) throw new Error("Missing or repeated release platform");
  const sums=[];
  for (const item of manifest.artifacts) {
    if (!item || !Object.hasOwn(artifacts,item.id) || item.filename !== artifacts[item.id] || !/^[a-f0-9]{64}$/.test(item.sha256)) throw new Error("Invalid artifact metadata");
    const data=readFileSync(join(directory,item.filename));
    if (data.length !== item.size || createHash("sha256").update(data).digest("hex") !== item.sha256) throw new Error("Artifact checksum mismatch");
    sums.push(`${item.sha256}  ${item.filename}`);
  }
  if (readFileSync(join(directory,"SHA256SUMS"),"utf8") !== sums.join("\n")+"\n") throw new Error("Checksum inventory mismatch");
  return { channel, version, commit, run, artifacts: manifest.artifacts };
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  const root=resolve(dirname(fileURLToPath(import.meta.url)),"..");
  const args=process.argv.slice(2),option=name=>{const at=args.indexOf(name);return at<0?undefined:args[at+1]};
  const version=readFileSync(join(root,"VERSION"),"utf8").trim();
  const tag=option("--tag") ?? process.env.JASE_TUNNEL_RELEASE_TAG ?? "";
  if (args.includes("--tag-only")) console.log(releaseTag(tag,version));
  else console.log(JSON.stringify(verifyRelease(resolve(root,option("--dir") ?? "dist"), {tag,version,
    commit:option("--commit") ?? process.env.JASE_TUNNEL_RELEASE_COMMIT ?? "",run:option("--run") ?? process.env.JASE_TUNNEL_RELEASE_RUN_URL ?? ""}),null,2));
}
