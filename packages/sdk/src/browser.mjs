import { companionDownload } from '../dist/legacy/platforms.js';
export const TUNNEL_PROTOCOL = 'jase-tunnel-v1';
const local = 'http://127.0.0.1:11439/tunnel/v1/';

export class TunnelClient {
  constructor({ path = '/api/jase-tunnel/v1/', onState = () => {} } = {}) {
    if (!/^\/(?:[a-zA-Z0-9_-]+\/)+$/.test(path)) throw new Error('Percorso del modulo non valido.');
    this.path = path; this.onState = onState; this.receipt = undefined;
  }
  async request(url, options = {}, loopback = false) {
    const response = await fetch(url, { ...options, cache:'no-store', redirect:'error', credentials: loopback ? 'omit' : 'same-origin', ...(loopback ? { mode:'cors', targetAddressSpace:'loopback' } : {}), signal: options.signal ?? AbortSignal.timeout(25_000) });
    const data = await response.json();
    if (!response.ok) throw new Error(data.error?.message ?? 'Collegamento non disponibile.');
    return data;
  }
  async platform() {
    if (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1) return undefined;
    const hints = await navigator.userAgentData?.getHighEntropyValues?.(['platform','architecture','bitness']).catch(()=>undefined);
    return companionDownload(hints?.platform ?? navigator.userAgentData?.platform ?? navigator.platform,hints?.architecture,hints?.bitness,navigator.userAgent);
  }
  session() { return this.request(this.path+'session'); }
  releases() { return this.request(this.path+'download?manifest=1'); }
  async discover() {
    if (window.top !== window || document.visibilityState === 'hidden') throw new Error('Apri il servizio in una scheda visibile.');
    try {
      const result = await this.request(local+'discover',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({protocol:TUNNEL_PROTOCOL})},true);
      this.receipt=result.receipt; this.expiresAt=result.expiresAt;
      return await this.waitFor(['compatible','connected']);
    } catch(error) {
      if (error instanceof TypeError || /fetch|network|load|abort|timeout/i.test(error.message)) throw new Error('Non riesco a contattare Jase Tunnel: verifica che sia aperto e che il browser permetta il collegamento locale.');
      throw error;
    }
  }
  async waitFor(states) {
    while(Date.now()<this.expiresAt) {
      if(document.visibilityState==='hidden') throw new Error('Torna alla scheda e premi Collega.');
      const state=await this.request(local+'discovery',{headers:{Authorization:'Bearer '+this.receipt}},true);
      this.onState(state);
      if(states.includes(state.state)) return state;
      if(['rejected','failed','incompatible','expired'].includes(state.state)) throw new Error(state.state==='rejected'?'Collegamento rifiutato nell’app.':'Il sito non ha completato il collegamento a Jase Tunnel.');
      await new Promise(resolve=>setTimeout(resolve,2000));
    }
    throw new Error('Collegamento scaduto. Premi di nuovo Collega.');
  }
  async connect() {
    if(!this.receipt || Date.now()>=this.expiresAt) await this.discover();
    const ticket=await this.request(this.path+'tickets',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({protocol:TUNNEL_PROTOCOL})});
    await this.request(local+'pair',{method:'POST',headers:{'Content-Type':'application/json',Authorization:'Bearer '+this.receipt},body:JSON.stringify({protocol:TUNNEL_PROTOCOL,ticket:ticket.ticket})},true);
    await this.waitFor(['connected']);
    localStorage.setItem('jase-tunnel-enabled:'+location.origin,'true');
    return this.session();
  }
  async disconnect() {
    await this.request(this.path+'disconnect',{method:'POST',headers:{'Content-Type':'application/json'},body:'{}'});
    localStorage.removeItem('jase-tunnel-enabled:'+location.origin); this.receipt=undefined;
  }
  chat(model,message) { return this.request(this.path+'chat',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({model,message}),signal:AbortSignal.timeout(600_000)}); }
  confirm(id,allow) { return this.request(this.path+'confirm',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({id,allow}),signal:AbortSignal.timeout(600_000)}); }
}
