import test from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import sharp from "sharp";
import { resourceObject } from "./windows-resources.mjs";

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
  const ico = await readFile(join(root, "assets/jase.ico"));
  assert.equal(ico.readUInt16LE(2), 1);
  assert.equal(ico.readUInt16LE(4), 9);
  const resources = [], group = Buffer.alloc(6 + 9 * 14);
  group.writeUInt16LE(1, 2); group.writeUInt16LE(9, 4);
  for (let i = 0; i < 9; i++) {
    const at = 6 + i * 16, size = ico[at] || 256;
    const length = ico.readUInt32LE(at + 8), offset = ico.readUInt32LE(at + 12);
    const frame = ico.subarray(offset, offset + length);
    resources.push({ type: 3, id: i + 1, bytes: frame });
    ico.copy(group, 6 + i * 14, at, at + 12);
    group.writeUInt16LE(i + 1, 6 + i * 14 + 12);
    const actual = await sharp(frame).ensureAlpha().raw().toBuffer({ resolveWithObject: true });
    const expected = await sharp(join(root, "assets/jase-logo.png")).resize(size, size).ensureAlpha().raw().toBuffer();
    assert.equal(actual.info.width, size);
    assert.equal(actual.info.height, size);
    // SIMD resampling on x64 and ARM can differ by a rounding unit. Compare
    // visible pixels, not PNG compression streams; resources stay immutable.
    let total = 0, maximum = 0;
    for (let pixel = 0; pixel < expected.length; pixel += 4) {
      for (let channel = 0; channel < 4; channel++) {
        const a = actual.data[pixel + channel] * (channel === 3 ? 1 : actual.data[pixel + 3] / 255);
        const b = expected[pixel + channel] * (channel === 3 ? 1 : expected[pixel + 3] / 255);
        const difference = Math.abs(a - b);
        total += difference; maximum = Math.max(maximum, difference);
      }
    }
    assert.ok(maximum <= 3 && total / expected.length < 0.1, `${size}px icon does not match the logo; run npm run icons`);
    if (size === 256) assert.deepEqual(await readFile(join(root, "assets/desktop-icon.png")), frame);
  }
  const manifest = (await readFile(join(root, "assets/windows.manifest"), "utf8")).replace(/\r\n/g, "\n");
  resources.push({ type: 14, id: 1, bytes: group }, { type: 24, id: 1, bytes: Buffer.from(manifest) });
  for (const arch of ["amd64", "arm64"]) {
    assert.deepEqual(await readFile(join(root, `rsrc_windows_${arch}.syso`)), resourceObject(resources, arch), `stale ${arch} resources; run npm run icons`);
  }
});
