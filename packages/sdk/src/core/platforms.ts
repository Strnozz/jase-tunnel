export const TUNNEL_REPOSITORY = "Strnozz/jase-tunnel";
export const TUNNEL_RELEASES = `https://github.com/${TUNNEL_REPOSITORY}/releases`;

// The desktop packages published by this repository's release workflow. The browser
// module detects the visitor's system and picks one of these identifiers.
export const TUNNEL_DOWNLOADS = [
  { id: "windows-x64", os: "windows", arch: "x64", label: "Windows · Intel / AMD", filename: "Jase-Tunnel-Windows-x64.exe" },
  { id: "windows-arm64", os: "windows", arch: "arm64", label: "Windows · ARM", filename: "Jase-Tunnel-Windows-arm64.exe" },
  { id: "macos-universal", os: "macos", arch: "universal", label: "macOS · Apple Silicon e Intel", filename: "Jase-Tunnel-macOS.zip" },
  { id: "linux-x64", os: "linux", arch: "x64", label: "Linux · Intel / AMD", filename: "Jase-Tunnel-Linux-x64.tar.gz" },
  { id: "linux-arm64", os: "linux", arch: "arm64", label: "Linux · ARM", filename: "Jase-Tunnel-Linux-arm64.tar.gz" }
] as const;
export type TunnelDownload = typeof TUNNEL_DOWNLOADS[number];
