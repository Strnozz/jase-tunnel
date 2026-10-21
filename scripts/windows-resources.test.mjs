import test from "node:test";
import assert from "node:assert/strict";
import { readFile, mkdtemp, mkdir, copyFile, rm } from "node:fs/promises";
import { join, dirname } from "node:path";
import { tmpdir } from "node:os";
import { fileURLToPath } from "node:url";
import sharp from "sharp";
import { resourceObject, windowsResources } from "./windows-resources.mjs";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");

test("COFF objects carry correctly relocated resources for both Windows architectures", () => {
  for (const [arch, machine, relocation] of [["amd64", 0x8664, 3], ["arm64", 0xaa64, 2]]) {
    const payload = Buffer.from("manifest fixture");
    const object = resourceObject([{ type: 24, id: 1, bytes: payload }], arch);
    assert.equal(object.readUInt16LE(0), machine);
    assert.equal(object.readUInt16LE(2), 1);
    assert.equal(object.toString("ascii", 20, 25), ".rsrc");
    const section = object.subarray(object.readUInt32LE(40), object.readUInt32LE(40) + object.readUInt32LE(36));
    // Walk type -> id -> language, as the Windows resource loader does.
    let directory = 0;
    for (const id of [24, 1, 0x409]) {
      assert.equal(section.readUInt16LE(directory + 14), 1);
      assert.equal(section.readUInt32LE(directory + 16), id);
      directory = section.readUInt32LE(directory + 20) & 0x7fffffff;
    }
    const offset = section.readUInt32LE(directory), length = section.readUInt32LE(directory + 4);
    assert.deepEqual(section.subarray(offset, offset + length), payload);
    const relocations = object.readUInt32LE(44);
    assert.equal(object.readUInt32LE(relocations), directory);
    assert.equal(object.readUInt32LE(relocations + 4), 0); // section symbol
    assert.equal(object.readUInt16LE(relocations + 8), relocation);
  }
  assert.throws(() => resourceObject([], "386"), /Unsupported/);
});

test("checked-in resources match the supplied logo and manifest", async () => {
  const temporary = await mkdtemp(join(tmpdir(), "jase-icons-"));
  try {
    await mkdir(join(temporary, "assets"));
    for (const name of ["jase-logo.png", "windows.manifest"]) {
      await copyFile(join(root, "assets", name), join(temporary, "assets", name));
    }
    await windowsResources(temporary);
    for (const name of ["assets/jase.ico", "assets/desktop-icon.png", "rsrc_windows_amd64.syso", "rsrc_windows_arm64.syso"]) {
      assert.deepEqual(await readFile(join(root, name)), await readFile(join(temporary, name)), `${name} is stale; run npm run icons`);
    }
    const ico = await readFile(join(temporary, "assets/jase.ico"));
    assert.equal(ico.readUInt16LE(2), 1);
    assert.equal(ico.readUInt16LE(4), 9);
    for (let i = 0; i < 9; i++) {
      const at = 6 + i * 16, size = ico[at] || 256;
      const length = ico.readUInt32LE(at + 8), offset = ico.readUInt32LE(at + 12);
      const frame = await sharp(ico.subarray(offset, offset + length)).metadata();
      assert.equal(frame.width, size);
      assert.equal(frame.height, size);
      assert.equal(frame.hasAlpha, true);
    }
  } finally {
    await rm(temporary, { recursive: true, force: true });
  }
});
