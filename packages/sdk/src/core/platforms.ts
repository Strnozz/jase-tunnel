export const COMPANION_REPOSITORY = "Strnozz/jase-tunnel";
export const COMPANION_RELEASES = `https://github.com/${COMPANION_REPOSITORY}/releases`;

export const COMPANION_DOWNLOADS = [
  { id: "windows-x64", os: "windows", arch: "x64", label: "Windows · Intel / AMD", filename: "Jase-Windows-x64.exe" },
  { id: "windows-arm64", os: "windows", arch: "arm64", label: "Windows · ARM", filename: "Jase-Windows-arm64.exe" },
  { id: "macos-universal", os: "macos", arch: "universal", label: "macOS · Apple Silicon e Intel", filename: "Jase-macOS.zip" },
  { id: "linux-x64", os: "linux", arch: "x64", label: "Linux · Intel / AMD", filename: "Jase-Linux-x64.tar.gz" },
  { id: "linux-arm64", os: "linux", arch: "arm64", label: "Linux · ARM", filename: "Jase-Linux-arm64.tar.gz" }
] as const;
export type CompanionDownload = typeof COMPANION_DOWNLOADS[number];
export function companionDownload(platform: string, architecture = "", bitness = "", userAgent = ""): CompanionDownload | undefined {
  const value = `${platform} ${userAgent}`.toLowerCase();
  if (/android|iphone|ipad|ipod|cros/.test(value)) return;
  if (/mac/.test(value)) return COMPANION_DOWNLOADS.find(d => d.os === "macos");
  const os = /win/.test(value) ? "windows" : /linux|x11/.test(value) ? "linux" : undefined;
  if (!os || bitness === "32" || /i[3-6]86/.test(value)) return;
  const arm = /arm|aarch64/.test(architecture.toLowerCase()) || /aarch64|arm64/.test(value);
  // Chromium supplies architecture through UA Client Hints. Other browsers may hide
  // ARM on Windows; leave that case to the visible platform chooser, never guess x64.
  if (os === "windows" && !architecture && !/win64|wow64|x64|amd64|arm64/.test(value)) return;
  return COMPANION_DOWNLOADS.find(d => d.os === os && d.arch === (arm ? "arm64" : "x64"));
}
