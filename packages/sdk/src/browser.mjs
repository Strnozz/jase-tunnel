export const TUNNEL_PROTOCOL = 'jase-tunnel-v1';
const local = 'http://127.0.0.1:11439/tunnel/v1/';
const discoveryKey = 'jase-tunnel-discovery';
const optInKey = () => 'jase-tunnel-enabled:' + location.origin;
const storage = kind => { try { return window[kind]; } catch { return undefined; } };

// WebKit still blocks HTTPS -> HTTP loopback as mixed content. Its explicit
// connection window talks to the app on its own origin, then exchanges only the
// discovery/pairing messages with this exact opener. Never open it automatically.
function needsConnectionWindow() {
  return /AppleWebKit/.test(navigator.userAgent) && !/Chrome|Chromium|Edg\//.test(navigator.userAgent);
}

class ConnectionWindow {
  constructor() {
    this.pending = new Map(); this.sequence = 0; this.closed = false;
    this.channel = Array.from(crypto.getRandomValues(new Uint8Array(32)), b => b.toString(16).padStart(2, '0')).join('');
    this.origin = new URL(local).origin;
    const hash = new URLSearchParams({ channel: this.channel, origin: location.origin });
    this.popup = window.open(this.origin + '/tunnel/connect#' + hash, '_blank', 'popup,width=520,height=420');
    if (!this.popup) throw new Error('Allow this website to open the Jase Tunnel connection window, then click Connect again.');
    this.ready = new Promise((resolve, reject) => { this.resolveReady = resolve; this.rejectReady = reject; });
    this.receive = event => {
      const message = event.data;
      if (event.source !== this.popup || event.origin !== this.origin || message?.protocol !== 'jase-tunnel-window-v1' || message.channel !== this.channel) return;
      if (message.type === 'loaded') this.hello();
      else if (message.type === 'ready') { clearTimeout(this.startup); this.resolveReady(); }
      else if (message.type === 'response') {
        const pending = this.pending.get(message.id);
        if (!pending) return;
        this.pending.delete(message.id); clearTimeout(pending.timer);
        if (Number.isInteger(message.status) && message.status >= 200 && message.status < 300) pending.resolve(message.data);
        else pending.reject(new Error(message.data?.error?.message ?? 'Connection unavailable.'));
      }
    };
    this.leave = () => this.close(new Error('The website was closed. Click Connect again.'));
    window.addEventListener('message', this.receive);
    window.addEventListener('pagehide', this.leave);
    this.startup = setTimeout(() => this.close(new Error('Cannot open the local connection window. Open Jase Tunnel 0.3.4 or later, then reconnect. The website must allow this window to communicate with its opener.')), 10_000);
    this.watch = setInterval(() => {
      if (this.popup.closed) this.close(new Error('The connection window was closed. Return to the website and click Connect again.'));
      else this.hello();
    }, 300);
    this.lifetime = setTimeout(() => this.close(new Error('Connection expired. Click Connect again.')), 5 * 60_000);
    this.hello();
  }
  hello() {
    if (!this.closed) this.popup.postMessage({ protocol: 'jase-tunnel-window-v1', channel: this.channel, type: 'hello' }, this.origin);
  }
  async request(url, options) {
    await this.ready;
    if (this.closed) throw new Error('The connection window was closed. Click Connect again.');
    const operation = url.slice(local.length);
    if (!['discover', 'discovery', 'pair'].includes(operation)) throw new Error('Unsupported connection operation.');
    const receipt = new Headers(options.headers).get('Authorization')?.replace(/^Bearer /, '');
    const id = ++this.sequence;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => { this.pending.delete(id); reject(new Error('The connection window did not respond. Open Jase Tunnel and reconnect.')); }, 25_000);
      this.pending.set(id, { resolve, reject, timer });
      this.popup.postMessage({ protocol: 'jase-tunnel-window-v1', channel: this.channel, type: 'request', id, operation, receipt, body: options.body ? JSON.parse(options.body) : undefined }, this.origin);
    });
  }
  close(error = new Error('Connection finished.')) {
    if (this.closed) return;
    this.closed = true;
    clearTimeout(this.startup); clearInterval(this.watch); clearTimeout(this.lifetime);
    window.removeEventListener('message', this.receive);
    window.removeEventListener('pagehide', this.leave);
    this.rejectReady(error);
    for (const pending of this.pending.values()) { clearTimeout(pending.timer); pending.reject(error); }
    this.pending.clear();
    if (!this.popup.closed) this.popup.close();
  }
}

// The desktop package for this visitor, as an identifier of the release manifest.
export function detectPlatform(platform = '', architecture = '', bitness = '', userAgent = '') {
  const value = `${platform} ${userAgent}`.toLowerCase();
  if (/android|iphone|ipad|ipod|cros/.test(value)) return undefined;
  if (/mac/.test(value)) return 'macos-universal';
  const os = /win/.test(value) ? 'windows' : /linux|x11/.test(value) ? 'linux' : undefined;
  if (!os || bitness === '32' || /i[3-6]86/.test(value)) return undefined;
  const arm = /arm|aarch64/.test(architecture.toLowerCase()) || /aarch64|arm64/.test(value);
  // Chromium supplies architecture through UA Client Hints. Other browsers may hide
  // ARM on Windows; leave that case to the visible platform chooser, never guess x64.
  if (os === 'windows' && !architecture && !/win64|wow64|x64|amd64|arm64/.test(value)) return undefined;
  return `${os}-${arm ? 'arm64' : 'x64'}`;
}

// Chrome and Edge ask the visitor before a page contacts an app on this computer
// (Local Network Access). Their permission names have changed across versions; other
// browsers may expose none. 'unknown' never proves that access is allowed.
export async function loopbackPermission() {
  for (const name of ['loopback-network', 'local-network-access', 'local-network']) {
    try { return (await navigator.permissions.query({ name })).state; } catch {}
  }
  return 'unknown';
}

export class TunnelClient {
  constructor({ path = '/api/jase-tunnel/v1/', onState = () => {} } = {}) {
    if (!/^\/(?:[a-zA-Z0-9_-]+\/)+$/.test(path)) throw new Error('Invalid module path.');
    this.path = path; this.onState = onState; this.receipt = undefined;
  }
  async request(url, options = {}, loopback = false) {
    if (loopback && this.connectionWindow) return this.connectionWindow.request(url, options);
    const response = await fetch(url, { ...options, cache:'no-store', redirect:'error', credentials: loopback ? 'omit' : 'same-origin', ...(loopback ? { mode:'cors', targetAddressSpace:'loopback' } : {}), signal: options.signal ?? AbortSignal.timeout(25_000) });
    const data = await response.json();
    if (!response.ok) throw new Error(data.error?.message ?? 'Connection unavailable.');
    return data;
  }
  async platform() {
    if (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1) return undefined;
    const hints = await navigator.userAgentData?.getHighEntropyValues?.(['platform','architecture','bitness']).catch(()=>undefined);
    return detectPlatform(hints?.platform ?? navigator.userAgentData?.platform ?? navigator.platform, hints?.architecture, hints?.bitness, navigator.userAgent);
  }
  session() { return this.request(this.path+'session'); }
  releases() { return this.request(this.path+'download?manifest=1'); }

  // The page may announce itself without a click only where that cannot raise a browser
  // prompt for visitors who never asked: the loopback permission is already granted, or
  // the browser has no such permission and the visitor connected on this site before.
  async canAnnounce() {
    if (window.top !== window) return false;
    if (needsConnectionWindow()) return false;
    const optedIn = storage('localStorage')?.getItem(optInKey()) === 'true';
    // A page served from this computer contacts the tunnel without any browser prompt.
    if (optedIn && ['localhost', '127.0.0.1', '[::1]'].includes(location.hostname)) return true;
    const permission = await loopbackPermission();
    return permission === 'granted' || permission === 'unknown' && optedIn;
  }
  remember(result) {
    this.receipt = result.receipt; this.expiresAt = result.expiresAt;
    try { storage('sessionStorage')?.setItem(discoveryKey, JSON.stringify({ receipt: result.receipt, expiresAt: result.expiresAt })); } catch {}
  }
  forget() {
    this.receipt = undefined; this.expiresAt = undefined;
    try { storage('sessionStorage')?.removeItem(discoveryKey); } catch {}
  }
  // Moving to another page of the same site keeps the announcement made by the first one.
  resume() {
    try {
      const saved = JSON.parse(storage('sessionStorage')?.getItem(discoveryKey) ?? 'null');
      if (saved && /^[a-f0-9]{64}$/.test(saved.receipt) && saved.expiresAt > Date.now() + 5_000) { this.receipt = saved.receipt; this.expiresAt = saved.expiresAt; return true; }
    } catch {}
    return false;
  }
  // Tell Jase Tunnel that this page accepts it. The app verifies the site and may notify
  // the person; when they choose «Connect» there, the page continues to the consent
  // dialog. Resolves with the session once connected, or null when nothing happened.
  async announce() {
    if (needsConnectionWindow()) return null;
    const fresh = async () => this.remember(await this.request(local+'discover',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({protocol:TUNNEL_PROTOCOL,announce:true})},true));
    try {
      let state;
      if (this.resume()) {
        // The app may have restarted since that page: its receipt is then unknown.
        try { state = await this.waitFor(['requested','connected'], true); }
        catch { this.forget(); await fresh(); }
      } else await fresh();
      state ??= await this.waitFor(['requested','connected'], true);
      if (state.state === 'connected') return this.session();
      return await this.connect();
    } catch { return null; }
  }
  async discover() {
    if (window.top !== window || document.visibilityState === 'hidden' && !this.connectionWindow) throw new Error('Open the website in a visible tab.');
    try {
      if (needsConnectionWindow() && !this.connectionWindow) this.connectionWindow = new ConnectionWindow();
      this.remember(await this.request(local+'discover',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({protocol:TUNNEL_PROTOCOL})},true));
      return await this.waitFor(['compatible','requested','connected']);
    } catch(error) {
      this.connectionWindow?.close(); this.connectionWindow = undefined;
      if (error instanceof TypeError || /fetch|network|load|abort|timeout/i.test(error.message)) throw new Error('Cannot reach Jase Tunnel. Check that it is running and that your browser allows local connections.');
      throw error;
    }
  }
  // quiet: an announcement waits while the tab is hidden instead of failing.
  async waitFor(states, quiet = false) {
    while(Date.now()<this.expiresAt) {
      if(document.visibilityState==='hidden' && !this.connectionWindow) {
        if(!quiet) throw new Error('Return to the tab and click Connect.');
        await new Promise(resolve=>document.addEventListener('visibilitychange',resolve,{once:true}));
        continue;
      }
      const state=await this.request(local+'discovery',{headers:{Authorization:'Bearer '+this.receipt}},true);
      this.onState(state);
      if(states.includes(state.state)) return state;
      if(['rejected','failed','incompatible','expired'].includes(state.state)) { this.forget(); throw new Error(state.state==='rejected'?'Connection declined in the app.':'The website did not complete the Jase Tunnel connection.'); }
      await new Promise(resolve=>setTimeout(resolve,2000));
    }
    this.forget();
    throw new Error('Connection expired. Click Connect again.');
  }
  async connect() {
    try {
      // Must happen before the first await, while the Connect click is still active.
      if (needsConnectionWindow() && !this.connectionWindow) {
        if (window.top !== window || document.visibilityState === 'hidden') throw new Error('Open the website in a visible tab.');
        this.connectionWindow = new ConnectionWindow();
      }
      if(!this.receipt || Date.now()>=this.expiresAt) await this.discover();
      const ticket=await this.request(this.path+'tickets',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({protocol:TUNNEL_PROTOCOL})});
      await this.request(local+'pair',{method:'POST',headers:{'Content-Type':'application/json',Authorization:'Bearer '+this.receipt},body:JSON.stringify({protocol:TUNNEL_PROTOCOL,ticket:ticket.ticket})},true);
      await this.waitFor(['connected']);
      this.forget();
      try { storage('localStorage')?.setItem(optInKey(),'true'); } catch {}
      return await this.session();
    } catch (error) { this.forget(); throw error; }
    finally { this.connectionWindow?.close(); this.connectionWindow = undefined; }
  }
  async disconnect() {
    this.connectionWindow?.close(); this.connectionWindow = undefined;
    await this.request(this.path+'disconnect',{method:'POST',headers:{'Content-Type':'application/json'},body:'{}'});
    try { storage('localStorage')?.removeItem(optInKey()); } catch {}
    this.forget();
  }
  chat(model,message) { return this.request(this.path+'chat',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({model,message}),signal:AbortSignal.timeout(600_000)}); }
  confirm(id,allow) { return this.request(this.path+'confirm',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({id,allow}),signal:AbortSignal.timeout(600_000)}); }
}
