import test from "node:test";
import assert from "node:assert/strict";
import { mkdtemp, rm } from "node:fs/promises";
import { join, dirname } from "node:path";
import { tmpdir } from "node:os";
import { fileURLToPath } from "node:url";
import sharp from "sharp";
import { companionIcons } from "./companion-icons.mjs";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");

test("macOS app icons use the Jase logo and the menu bar uses its white silhouette", async () => {
  const temporary = await mkdtemp(join(tmpdir(), "jase-mac-icons-"));
  try {
    const source = join(root, "assets/jase-logo.png");
    const resources = join(temporary, "Resources"), iconSet = join(temporary, "Jase.iconset");
    await companionIcons(source, resources, iconSet);
    const menu = await sharp(join(resources, "JaseMenu.png")).raw().toBuffer({ resolveWithObject: true });
    const alpha = await sharp(source).resize(40, 40).extractChannel("alpha").raw().toBuffer();
    assert.equal(menu.info.width, 40);
    assert.equal(menu.info.height, 40);
    assert.equal(menu.info.channels, 4);
    for (let i = 0; i < alpha.length; i++) {
      assert.deepEqual([...menu.data.subarray(i * 4, i * 4 + 3)], [255, 255, 255]);
      assert.equal(menu.data[i * 4 + 3], alpha[i]);
    }
    assert.ok(alpha.some(value => value === 0), "menu background must stay transparent");
    assert.ok(alpha.some(value => value > 0), "menu must contain the logo");
    for (const [size, scale] of [[16, 1], [16, 2], [32, 1], [32, 2], [128, 1], [128, 2], [256, 1], [256, 2], [512, 1], [512, 2]]) {
      const actual = await sharp(join(iconSet, `icon_${size}x${size}${scale === 2 ? "@2x" : ""}.png`)).raw().toBuffer();
      const expected = await sharp(source).resize(size * scale, size * scale).raw().toBuffer();
      assert.deepEqual(actual, expected);
    }
  } finally {
    await rm(temporary, { recursive: true, force: true });
  }
});
