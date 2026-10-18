// The visitor's panel, without dependencies. A page loads it with one tag:
//   <script type="module" src="/api/jase-tunnel/v1/client/widget.mjs"></script>
// and the React component mounts the same panel. Model output is always inserted as
// text, never as HTML.
import { TunnelClient } from './browser.mjs';

const releasesPage = 'https://github.com/Strnozz/jase-tunnel/releases';
const button = 'border:1px solid #ddd;border-radius:10px;padding:9px 12px;background:#fff;color:#222;cursor:pointer;font:inherit';

function el(tag, props = {}, ...children) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(props)) {
    if (value === undefined || value === null || value === false) continue;
    if (key === 'style') node.style.cssText = value;
    else if (key.startsWith('on')) node.addEventListener(key.slice(2), value);
    else node.setAttribute(key, value === true ? '' : String(value));
  }
  for (const child of children.flat()) if (child !== undefined && child !== null && child !== false) node.append(child instanceof Node ? child : String(child));
  return node;
}

export function mountJaseTunnel({ path = '/api/jase-tunnel/v1/', container } = {}) {
  const state = { open: false, session: null, release: null, platform: undefined, history: [], pending: null, busy: false, destroyed: false };
  const client = new TunnelClient({ path, onState: s => { if (s.state === 'awaiting_approval') { state.open = true; say('Conferma il dominio e scegli il modello nell’app Jase Tunnel.'); } } });

  const status = el('p', { role: 'status' }, 'Questo sito accetta Jase Tunnel: usa la LLM del tuo computer, senza registrazione.');
  const connectButton = el('button', { type: 'button', style: button }, 'Collega il mio modello');
  const downloads = el('div');
  const offline = el('div', {}, el('p', {}, 'Apri Jase Tunnel sul computer, poi premi Collega. Il browser potrebbe chiedere l’accesso al servizio locale.'), connectButton, downloads);
  const modelLine = el('p');
  const history = el('div');
  const pending = el('div');
  const input = el('textarea', { maxlength: 16000, style: 'display:block;width:100%;box-sizing:border-box;min-height:70px;margin-top:6px;font:inherit;border:1px solid #ddd;border-radius:8px;padding:8px' });
  const send = el('button', { type: 'submit', style: button }, 'Invia');
  const form = el('form', {}, el('label', {}, 'Chiedi al tuo modello', input), send);
  const disconnect = el('button', { type: 'button', style: button + ';margin-top:10px' }, 'Scollega');
  const online = el('div', {}, modelLine, history, pending, form, disconnect);
  const close = el('button', { type: 'button', style: button, 'aria-label': 'Chiudi pannello' }, '×');
  const panel = el('section', { 'aria-label': 'Pannello Jase Tunnel', style: 'width:min(360px, calc(100vw - 40px));max-height:75vh;overflow-y:auto;padding:18px;background:#fff;border:1px solid #ddd;border-radius:18px;box-shadow:0 12px 50px #0002;margin-bottom:10px' },
    el('div', { style: 'display:flex;justify-content:space-between;align-items:center' }, el('strong', {}, 'Jase Tunnel'), close), status, offline, online);
  const toggle = el('button', { type: 'button', style: button + ';box-shadow:0 3px 15px #0002', 'aria-expanded': 'false' }, 'Jase Tunnel');
  const root = el('aside', { 'aria-label': 'Jase Tunnel', style: 'position:fixed;right:20px;bottom:20px;z-index:2147483000;font-family:system-ui,sans-serif;font-size:14px;line-height:1.5;color:#222' }, panel, toggle);
  (container ?? document.body).append(root);

  function say(message) { status.textContent = message; render(); }
  function render() {
    if (state.destroyed) return;
    const connected = !!state.session?.connected, model = state.session?.models?.find(m => m.available);
    panel.hidden = !state.open; toggle.setAttribute('aria-expanded', String(state.open));
    offline.hidden = connected; online.hidden = !connected;
    connectButton.disabled = state.busy; connectButton.textContent = state.busy ? 'Collegamento…' : 'Collega il mio modello';
    const available = state.release?.downloads?.filter(d => d.available) ?? [], detected = available.find(d => d.id === state.platform);
    downloads.replaceChildren(...[
      detected && el('p', {}, el('a', { href: path + 'download?target=' + encodeURIComponent(detected.id) }, 'Scarica per ' + detected.label)),
      available.length && el('details', {}, el('summary', {}, detected ? 'Altri sistemi operativi' : 'Scarica Jase Tunnel'), ...available.map(d => el('p', {}, el('a', { href: path + 'download?target=' + encodeURIComponent(d.id) }, d.label)))),
      state.release?.message && el('p', {}, state.release.message),
      !available.length && el('a', { href: releasesPage, target: '_blank', rel: 'noopener noreferrer' }, 'Download e versioni su GitHub')
    ].filter(Boolean));
    modelLine.textContent = model ? `Modello: ${model.label}` : 'Modello non pronto. Apri Ollama o LM Studio e ricollega il tunnel.';
    history.replaceChildren(...state.history.map(item => el('p', { style: 'white-space:pre-wrap;overflow-wrap:anywhere' }, el('strong', {}, item.role + ': '), item.text)));
    pending.replaceChildren(...(state.pending ? [
      el('p', {}, 'Consenti queste chiamate agli strumenti del servizio?'),
      el('pre', { style: 'white-space:pre-wrap;font-size:12px' }, JSON.stringify(state.pending.calls, null, 2)),
      ...[[true, 'Consenti'], [false, 'Rifiuta']].map(([allow, label]) => el('button', { type: 'button', style: button, disabled: state.busy, onclick: () => act(async () => answer(await client.confirm(state.pending.id, allow))) }, label))
    ] : []));
    input.disabled = state.busy || !!state.pending;
    send.disabled = state.busy || !model || !!state.pending; send.textContent = state.busy ? 'Risposta in corso…' : 'Invia';
    disconnect.disabled = state.busy;
  }
  async function act(fn) {
    state.busy = true; render();
    try { await fn(); } catch (error) { status.textContent = error.message; }
    finally { state.busy = false; render(); }
  }
  function answer(result) {
    if (result.confirmation) { state.pending = result.confirmation; say('Lo strumento richiede la tua conferma.'); return; }
    state.pending = null; state.history.push({ role: 'Jase Tunnel', text: result.content }); say('Risposta dal tuo modello locale.');
  }
  function connected(session) { state.session = session; state.open = true; say('Computer collegato.'); }

  toggle.addEventListener('click', () => { state.open = !state.open; render(); });
  close.addEventListener('click', () => { state.open = false; render(); });
  connectButton.addEventListener('click', () => act(async () => connected(await client.connect())));
  disconnect.addEventListener('click', () => act(async () => { await client.disconnect(); state.session = null; state.pending = null; say('Computer scollegato.'); }));
  form.addEventListener('submit', event => {
    event.preventDefault();
    const model = state.session?.models?.find(m => m.available), text = input.value;
    if (!text.trim() || !model || state.pending) return;
    input.value = ''; state.history.push({ role: 'Tu', text });
    void act(async () => answer(await client.chat(model.id, text)));
  });

  client.platform().then(value => { state.platform = value; render(); }).catch(() => {});
  client.releases().then(value => { state.release = value; render(); }).catch(() => {});
  // Announce the page to Jase Tunnel only where no browser prompt can appear (see
  // canAnnounce). The app may then notify the person; no access is granted here.
  client.session().catch(() => null).then(async session => {
    if (session) { state.session = session; render(); }
    if (session?.connected || !await client.canAnnounce()) return;
    const result = await client.announce();
    if (result && !state.destroyed) connected(result);
  });
  render();
  return { destroy() { state.destroyed = true; root.remove(); } };
}

// Loaded from the broker (…/client/widget.mjs): mount at once, with the broker's path.
if (typeof document !== 'undefined') {
  const served = /^(\/(?:[a-zA-Z0-9_-]+\/)+)client\/widget\.mjs$/.exec(new URL(import.meta.url).pathname);
  if (served && new URL(import.meta.url).origin === location.origin) {
    const mount = () => mountJaseTunnel({ path: served[1] });
    document.readyState === 'loading' ? document.addEventListener('DOMContentLoaded', mount, { once: true }) : mount();
  }
}
