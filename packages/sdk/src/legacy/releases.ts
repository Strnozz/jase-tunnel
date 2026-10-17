import { COMPANION_DOWNLOADS, COMPANION_RELEASES, COMPANION_REPOSITORY } from "./platforms";

export type TunnelReleaseChannel = "stable" | "development";
export type TunnelRelease = Readonly<{
  status: "available" | "not-published" | "unavailable";
  channel: TunnelReleaseChannel;
  version?: string;
  repository: string;
  releases: string;
  message?: string;
  downloads: readonly (typeof COMPANION_DOWNLOADS[number] & { available: boolean; url?: string })[];
}>;
type GitHubFetch = (url: string, init?: RequestInit) => Promise<Response>;
type ReleaseAsset = { name?: unknown; state?: unknown; size?: unknown; browser_download_url?: unknown };
type Release = { tag_name?: unknown; draft?: unknown; prerelease?: unknown; published_at?: unknown; assets?: unknown };
const API = `https://api.github.com/repos/${COMPANION_REPOSITORY}/releases`;

export function tunnelReleaseChannel(): TunnelReleaseChannel {
  const configured = process.env.JASE_TUNNEL_RELEASE_CHANNEL;
  if (configured === "development" || configured === "stable") return configured;
  return process.env.NODE_ENV === "development" ? "development" : "stable";
}

async function releaseJSON(response: Response): Promise<unknown> {
  if (!response.body) throw new Error("Empty release response");
  const reader = response.body.getReader(), chunks: Uint8Array[] = [];
  let size = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    size += value.byteLength;
    if (size > 1024 * 1024) { await reader.cancel(); throw new Error("Release metadata too large"); }
    chunks.push(value);
  }
  const bytes = new Uint8Array(size); let offset = 0;
  for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.byteLength; }
  return JSON.parse(new TextDecoder().decode(bytes));
}

function releaseMatches(value: unknown, channel: TunnelReleaseChannel): value is Release & { tag_name: string; assets: ReleaseAsset[] } {
  if (!value || typeof value !== "object" || Array.isArray(value)) return false;
  const r = value as Release;
  const tag = channel === "development" ? /^dev-v\d+\.\d+\.\d+(?:-[A-Za-z0-9.-]+)?$/ : /^v\d+\.\d+\.\d+$/;
  return r.draft === false && r.prerelease === (channel === "development") && typeof r.tag_name === "string"
    && r.tag_name.length <= 100 && tag.test(r.tag_name) && typeof r.published_at === "string"
    && Number.isFinite(Date.parse(r.published_at)) && Array.isArray(r.assets);
}

// Only metadata passes through Jase. Executables travel from GitHub to the
// browser; coalescing and caching bound the unauthenticated API requests.
export class TunnelReleaseCatalog {
  private cache = new Map<TunnelReleaseChannel, { expires: number; value: TunnelRelease }>();
  private pending = new Map<TunnelReleaseChannel, Promise<TunnelRelease>>();
  constructor(private readonly get: GitHubFetch = fetch, private readonly now = Date.now) {}

  async load(channel: TunnelReleaseChannel): Promise<TunnelRelease> {
    const cached = this.cache.get(channel);
    if (cached && cached.expires > this.now()) return cached.value;
    const pending = this.pending.get(channel);
    if (pending) return pending;
    const work = this.lookup(channel).then(value => {
      this.cache.set(channel, { value, expires: this.now() + (value.status === "available" ? 300_000 : 60_000) });
      return value;
    }).finally(() => this.pending.delete(channel));
    this.pending.set(channel, work);
    return work;
  }

  private empty(channel: TunnelReleaseChannel, unavailable = false): TunnelRelease {
    return { status: unavailable ? "unavailable" : "not-published", channel, repository: COMPANION_REPOSITORY, releases: COMPANION_RELEASES,
      message: unavailable ? "Non riesco a verificare i download di Jase Tunnel su GitHub. Riprova tra poco."
        : channel === "development" ? "La versione di Jase Tunnel per localhost deve ancora essere pubblicata su GitHub."
        : "Gli eseguibili di Jase Tunnel devono ancora essere pubblicati su GitHub.",
      downloads: COMPANION_DOWNLOADS.map(d => ({ ...d, available: false })) };
  }

  private async lookup(channel: TunnelReleaseChannel): Promise<TunnelRelease> {
    try {
      const response = await this.get(channel === "stable" ? `${API}/latest` : `${API}?per_page=20`, {
        redirect: "error", cache: "no-store", signal: AbortSignal.timeout(8_000),
        headers: { Accept: "application/vnd.github+json", "X-GitHub-Api-Version": "2026-03-10", "User-Agent": "Jase-Tunnel-Web-Module" }
      });
      if (response.status === 404) return this.empty(channel);
      if (!response.ok) return this.empty(channel, true);
      const data = await releaseJSON(response);
      const candidates = channel === "development" ? (Array.isArray(data) ? data : []) : [data];
      const release = candidates.filter(r => releaseMatches(r, channel))
        .sort((a, b) => Date.parse(b.published_at as string) - Date.parse(a.published_at as string))[0];
      if (!release) return this.empty(channel);
      const base = `https://github.com/${COMPANION_REPOSITORY}/releases/download/${release.tag_name}/`;
      const downloads = COMPANION_DOWNLOADS.map(d => {
        const url = base + d.filename;
        const available = release.assets.some(a => a && typeof a === "object" && a.name === d.filename && a.state === "uploaded"
          && typeof a.size === "number" && Number.isSafeInteger(a.size) && a.size > 0 && a.size <= 150 * 1024 ** 2
          && a.browser_download_url === url);
        return { ...d, available, ...(available ? { url } : {}) };
      });
      if (!downloads.some(d => d.available)) return this.empty(channel);
      return { status: "available", channel, version: release.tag_name, repository: COMPANION_REPOSITORY, releases: COMPANION_RELEASES, downloads };
    } catch { return this.empty(channel, true); }
  }
}

const root = globalThis as typeof globalThis & { __jaseTunnelReleases?: TunnelReleaseCatalog };
export const tunnelReleases = () => root.__jaseTunnelReleases ??= new TunnelReleaseCatalog();
