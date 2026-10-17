import assert from "node:assert/strict";
import { test } from "node:test";
import { createHash } from "node:crypto";
import { mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { releaseTag, verifyRelease } from "./verify-release.mjs";

const commit="a".repeat(40),run="https://github.com/Strnozz/jase-tunnel/actions/runs/42";
function fixture(t,development=false){
  const directory=mkdtempSync(join(tmpdir(),"jase-tunnel-release-check-"));t.after(()=>rmSync(directory,{recursive:true,force:true}));
  const artifacts=Object.entries({"windows-x64":"Jase-Windows-x64.exe","windows-arm64":"Jase-Windows-arm64.exe","macos-universal":"Jase-macOS.zip","linux-x64":"Jase-Linux-x64.tar.gz","linux-arm64":"Jase-Linux-arm64.tar.gz"}).map(([id,filename])=>{
    const bytes=Buffer.from(`fixture ${id}`);writeFileSync(join(directory,filename),bytes);
    return {id,filename,size:bytes.length,sha256:createHash("sha256").update(bytes).digest("hex")};
  });
  const manifest={protocol:"jase-companion-v1",version:"0.1.0",build:`0.1.0+${commit.slice(0,12)}`,commit,run,dirty:false,macSigned:true,signing:development?"development-loopback":"development",artifacts};
  const save=()=>writeFileSync(join(directory,"manifest.json"),JSON.stringify(manifest));save();
  writeFileSync(join(directory,"SHA256SUMS"),artifacts.map(a=>`${a.sha256}  ${a.filename}`).join("\n")+"\n");
  return {directory,manifest,save,options:{tag:development?"dev-v0.1.0-preview":"v0.1.0",version:"0.1.0",commit,run}};
}
test("both release channels retain exact commit/run and artifact provenance",t=>{
  for(const development of [false,true]){const f=fixture(t,development);assert.equal(verifyRelease(f.directory,f.options).channel,development?"development":"stable")}
});
test("an ordinary release cannot contain a localhost app or a dirty build",t=>{
  for(const changed of [{signing:"development-loopback"},{dirty:true},{macSigned:false},{commit:"b".repeat(40)},{run:run+"0"}]){
    const f=fixture(t);Object.assign(f.manifest,changed);f.save();assert.throws(()=>verifyRelease(f.directory,f.options),/commit, run or channel/);
  }
});
test("changed executables and unexpected files cannot be published",t=>{
  const f=fixture(t);writeFileSync(join(f.directory,f.manifest.artifacts[0].filename),"changed");assert.throws(()=>verifyRelease(f.directory,f.options),/checksum/);
  const other=fixture(t);writeFileSync(join(other.directory,"connections.json"),"private");assert.throws(()=>verifyRelease(other.directory,other.options),/inventory/);
});
test("symlinks and incomplete or repeated platform inventories are rejected",t=>{
  const f=fixture(t),name=f.manifest.artifacts[0].filename;rmSync(join(f.directory,name));
  try{symlinkSync(join(f.directory,f.manifest.artifacts[1].filename),join(f.directory,name));assert.throws(()=>verifyRelease(f.directory,f.options),/release file/)}catch(error){if(error.code!=="EPERM")throw error;t.diagnostic("Symlink creation requires privileges on this system; other inventory checks still run.")}
  const other=fixture(t);other.manifest.artifacts[0]=other.manifest.artifacts[1];other.save();assert.throws(()=>verifyRelease(other.directory,other.options),/repeated/);
});
test("checksums and tags cannot name another version or channel",t=>{
  assert.equal(releaseTag("dev-v0.1.0-20261017","0.1.0"),"development");
  for(const tag of ["v0.2.0","v0.1.0-localhost","../v0.1.0"]){assert.throws(()=>releaseTag(tag,"0.1.0"))}
  const f=fixture(t);writeFileSync(join(f.directory,"SHA256SUMS"),readFileSync(join(f.directory,"SHA256SUMS"),"utf8").replace(/a/g,"b"));assert.throws(()=>verifyRelease(f.directory,f.options),/inventory/);
});
