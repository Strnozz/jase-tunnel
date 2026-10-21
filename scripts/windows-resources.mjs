import sharp from "sharp";
import { readFile, writeFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

// A small COFF resource object is sufficient for Go's internal linker on both
// Windows architectures. No Windows SDK, C compiler or runtime dependency.
export function resourceObject(resources, arch) {
  const machine = { amd64: 0x8664, arm64: 0xaa64 }[arch];
  if (!machine) throw new Error(`Unsupported Windows architecture: ${arch}`);
  const root = new Map();
  for (const { type, id, bytes } of resources) {
    if (!root.has(type)) root.set(type, new Map());
    root.get(type).set(id, new Map([[0x409, { bytes }]]));
  }
  let cursor = 0;
  const directories = [], leaves = [];
  function allocate(node) {
    node.offset = cursor;
    cursor += 16 + node.size * 8;
    directories.push(node);
    for (const [, child] of [...node].sort(([a], [b]) => a - b)) {
      if (child instanceof Map) allocate(child);
      else leaves.push(child);
    }
  }
  allocate(root);
  for (const leaf of leaves) { leaf.entry = cursor; cursor += 16; }
  for (const leaf of leaves) {
    leaf.offset = cursor;
    cursor = (cursor + leaf.bytes.length + 3) & ~3;
  }
  const data = Buffer.alloc(cursor);
  for (const node of directories) {
    data.writeUInt16LE(node.size, node.offset + 14);
    [...node].sort(([a], [b]) => a - b).forEach(([id, child], i) => {
      const at = node.offset + 16 + i * 8;
      data.writeUInt32LE(id, at);
      data.writeUInt32LE(child instanceof Map ? (child.offset | 0x80000000) >>> 0 : child.entry, at + 4);
    });
  }
  for (const leaf of leaves) {
    data.writeUInt32LE(leaf.offset, leaf.entry); // relocated to the section's RVA
    data.writeUInt32LE(leaf.bytes.length, leaf.entry + 4);
    leaf.bytes.copy(data, leaf.offset);
  }
  const relocations = Buffer.alloc(leaves.length * 10);
  leaves.forEach((leaf, i) => {
    relocations.writeUInt32LE(leaf.entry, i * 10);
    relocations.writeUInt16LE(arch === "amd64" ? 3 : 2, i * 10 + 8); // ADDR32NB
  });
  const header = Buffer.alloc(60);
  header.writeUInt16LE(machine, 0);
  header.writeUInt16LE(1, 2);
  header.writeUInt32LE(60 + data.length + relocations.length, 8);
  header.writeUInt32LE(1, 12);
  header.write(".rsrc", 20);
  header.writeUInt32LE(data.length, 36);
  header.writeUInt32LE(60, 40);
  header.writeUInt32LE(60 + data.length, 44);
  header.writeUInt16LE(leaves.length, 52);
  header.writeUInt32LE(0x40000040, 56);
  const symbol = Buffer.alloc(18);
  symbol.write(".rsrc");
  symbol.writeInt16LE(1, 12);
  symbol[16] = 3; // static section symbol
  const strings = Buffer.alloc(4); strings.writeUInt32LE(4);
  return Buffer.concat([header, data, relocations, symbol, strings]);
}

export async function windowsResources(root) {
  const source = await readFile(join(root, "assets/jase-logo.png"));
  const sizes = [16, 20, 24, 32, 40, 48, 64, 128, 256];
  const icons = await Promise.all(sizes.map(size => sharp(source).resize(size, size).png().toBuffer()));
  await writeFile(join(root, "assets/desktop-icon.png"), icons.at(-1));
  const group = Buffer.alloc(6 + sizes.length * 14);
  group.writeUInt16LE(1, 2); group.writeUInt16LE(sizes.length, 4);
  const icoHeader = Buffer.alloc(6 + sizes.length * 16);
  icoHeader.writeUInt16LE(1, 2); icoHeader.writeUInt16LE(sizes.length, 4);
  let offset = icoHeader.length;
  sizes.forEach((size, i) => {
    const at = 6 + i * 14, icoAt = 6 + i * 16;
    group[at] = group[at + 1] = size % 256;
    group.writeUInt16LE(1, at + 4); group.writeUInt16LE(32, at + 6);
    group.writeUInt32LE(icons[i].length, at + 8); group.writeUInt16LE(i + 1, at + 12);
    group.copy(icoHeader, icoAt, at, at + 12);
    icoHeader.writeUInt32LE(offset, icoAt + 12); offset += icons[i].length;
  });
  await writeFile(join(root, "assets/jase.ico"), Buffer.concat([icoHeader, ...icons]));
  const resources = icons.map((bytes, i) => ({ type: 3, id: i + 1, bytes }));
  const manifest = (await readFile(join(root, "assets/windows.manifest"), "utf8")).replace(/\r\n/g, "\n");
  resources.push({ type: 14, id: 1, bytes: group }, { type: 24, id: 1, bytes: Buffer.from(manifest) });
  for (const arch of ["amd64", "arm64"]) {
    await writeFile(join(root, `rsrc_windows_${arch}.syso`), resourceObject(resources, arch));
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  await windowsResources(resolve(dirname(fileURLToPath(import.meta.url)), ".."));
}
