import sharp from "sharp";
import { mkdir, readFile } from "node:fs/promises";
import { join } from "node:path";

export async function desktopIcon(source, target) {
  await sharp(source, { density: 288 }).resize(256, 256).png().toFile(target);
}

// Keep desktop artwork separate from the website's animated favicon.
export async function companionIcons(source, resources, iconSet) {
  const artwork = await readFile(source);
  await Promise.all([mkdir(resources, { recursive: true }), mkdir(iconSet, { recursive: true })]);
  const png = (input, pixels, path) => sharp(Buffer.from(input), { density: Math.max(72, Math.ceil(pixels / 64 * 72)) }).resize(pixels, pixels).png().toFile(path);
  const alpha = await sharp(artwork).resize(40, 40).extractChannel("alpha").toBuffer();
  await Promise.all([
    // Preserve the logo's geometry and transparency, but render the bar in white.
    sharp({ create: { width: 40, height: 40, channels: 3, background: "white" } })
      .joinChannel(alpha).png().toFile(join(resources, "JaseMenu.png")),
    ...[16, 32, 128, 256, 512].flatMap(size => [1, 2].map(scale =>
      png(artwork, size * scale, join(iconSet, `icon_${size}x${size}${scale === 2 ? "@2x" : ""}.png`))))
  ]);
}
