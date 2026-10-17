import sharp from "sharp";
import { mkdir, readFile } from "node:fs/promises";
import { join } from "node:path";

// The website favicon is the only source of the app and menu-bar artwork.
export async function companionIcons(source, resources, iconSet) {
  const svg = await readFile(source, "utf8");
  await Promise.all([mkdir(resources, { recursive: true }), mkdir(iconSet, { recursive: true })]);
  const png = (input, pixels, path) => sharp(Buffer.from(input), { density: Math.max(72, Math.ceil(pixels / 64 * 72)) }).resize(pixels, pixels).png().toFile(path);
  const alpha = await sharp(Buffer.from(svg)).resize(40, 40).extractChannel("alpha").toBuffer();
  await Promise.all([
    // Preserve the favicon's geometry and transparency, but render the bar in white.
    sharp({ create: { width: 40, height: 40, channels: 3, background: "white" } })
      .joinChannel(alpha).png().toFile(join(resources, "JaseMenu.png")),
    ...[16, 32, 128, 256, 512].flatMap(size => [1, 2].map(scale =>
      png(svg, size * scale, join(iconSet, `icon_${size}x${size}${scale === 2 ? "@2x" : ""}.png`))))
  ]);
}
