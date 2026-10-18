# jase-tunnel

Il modulo per i servizi web che vogliono usare la LLM locale dei propri visitatori tramite l’app [Jase Tunnel](https://github.com/Strnozz/jase-tunnel). Strumenti, dati e autorizzazioni restano sul server del servizio; l’inferenza avviene sul computer del visitatore, dopo il suo consenso. Nessuna API di inferenza esterna, account o relay.

## Un comando nel progetto

```sh
npx jase-tunnel init
```

Node.js 22 o successivo. `init`:

1. installa la dipendenza `jase-tunnel` con versione fissata (gli script di installazione npm non vengono eseguiti);
2. rileva gli strumenti IA esportati dal progetto e li registra **disabilitati** in `jase-tunnel.tools.json`, poi scrive `jase-tunnel.config`;
3. collega manifest e broker al server;
4. aggiunge il pannello alle pagine.

| Progetto | Server | Pannello |
| --- | --- | --- |
| **Next.js App Router** | Route `/.well-known/jase-tunnel.json` e `/api/jase-tunnel/v1/[...path]` | `<JaseTunnel />` nel layout |
| **Express** | Una riga `app.use(...)` subito dopo `express()`, prima degli altri middleware | Script nei file HTML e nei template |
| **`node:http`** | Il gestore passato a `createServer` viene avvolto | Script nei file HTML e nei template |

Se il progetto è configurato per Vercel, Netlify, Cloudflare Workers o altre funzioni serverless, `init` avvisa che il broker non può funzionare lì (vedi in fondo).

Il server carica il modulo con `import()` dinamico, quindi la stessa riga funziona in CommonJS e in ESM. I file HTML e i template riconosciuti sono `.html`, `.ejs`, `.hbs`, `.njk`, `.liquid` e `.mustache` con `</body>`; se non ce ne sono, `init` stampa il tag da aggiungere:

```html
<script type="module" src="/api/jase-tunnel/v1/client/widget.mjs"></script>
```

I file esistenti in conflitto vengono conservati e segnalati prima di scrivere. Server, layout, pagine e registri modificati hanno una copia in `.jase-tunnel/backups/`, esclusa da Git. Ripetere `init` non duplica le modifiche. `--dry-run` mostra i file previsti senza scrivere o installare. `--cwd <cartella>` seleziona il progetto. `--package <pacchetto o tarball>` installa un’altra versione o un pacchetto locale.

Per i progetti TypeScript con Express o `node:http` la configurazione è `jase-tunnel.config.ts`, importata senza estensione: funziona con `tsx`, `ts-node` o una compilazione CommonJS. Con una compilazione ESM in `dist/` va corretto il percorso dell’import. Per altri server il nucleo accetta le API Fetch standard:

```ts
import site from './jase-tunnel.config';
const response = await site.handle(request); // inoltra il manifest e /api/jase-tunnel/v1/*
```

Per Express o `node:http` collegati a mano: `nodeMiddleware(site)` e `withJaseTunnel(site, handler)` da `jase-tunnel/node`. Nel frontend: `JaseTunnel` da `jase-tunnel/react`, `mountJaseTunnel` da `jase-tunnel/widget` oppure `TunnelClient` da `jase-tunnel/browser`. L’installazione automatica non è dichiarata per Python, PHP, Ruby, Next Pages Router o router server sconosciuti.

## Sicurezza degli strumenti: il modello è del visitatore

Il modello gira sul computer del visitatore. Il visitatore può sostituirlo, o rispondere al broker al posto suo, e quindi **decide quali strumenti chiamare e con quali argomenti**. Ogni strumento abilitato è, di fatto, un’API pubblica del tuo sito, chiamabile da chiunque abbia Jase Tunnel:

- uno strumento scritto per un agente interno (dove il modello era del server) va rivisto prima di abilitarlo;
- ogni strumento deve avere `authorize(context, args)`, che controlla la sessione della richiesta e gli argomenti, oppure essere dichiarato `public: true` se è sicuro per qualsiasi visitatore. Senza nessuno dei due il modulo lo rifiuta all’avvio del server;
- la conferma nel pannello protegge il visitatore da un modello manipolato da contenuti di terzi, **non** il sito dal visitatore, che conferma da sé;
- `readOnly` decide solo se serve la conferma: non autorizza nulla.

Gli strumenti trovati da `init` o `tools scan` restano disabilitati finché non li abiliti:

```sh
npx jase-tunnel tools list
npx jase-tunnel tools enable lookup_product            # lo strumento ha authorize
npx jase-tunnel tools enable opening_hours --public    # sicuro per chiunque
```

`tools enable` rifiuta uno strumento rilevato senza `authorize` se non aggiungi `--public`.

## Autodetect degli strumenti

La scansione analizza i sorgenti JavaScript/TypeScript come sintassi; **non importa o esegue il progetto** e non legge variabili d'ambiente, credenziali, database, cartelle dati, dipendenze, test o percorsi simbolici. Non usa una LLM per interpretare i sorgenti e non invia codice a un servizio.

Riconosce dichiarazioni esportate `tool(...)` di AI SDK, `tool(fn, config)` di LangChain e `defineTool(...)` del modulo; inoltre oggetti o raccolte esportate con descrizione, schema e `execute`/`invoke`. I tool locali non esportati, i factory che richiedono contesto e i registri MCP costruiti dinamicamente richiedono un adattatore. Non trasforma funzioni ordinarie o route HTTP arbitrarie in strumenti IA.

Gli schemi supportati sono JSON Schema oggetto e Zod 4 convertibile in JSON Schema. Nomi duplicati e schemi incompleti producono un errore esplicito. Le dichiarazioni restano in `jase-tunnel.tools.json`; gli import del server sono in `.jase-tunnel/tools.ts` (o `.js`).

```sh
npx jase-tunnel tools list
npx jase-tunnel tools scan
npx jase-tunnel tools add ./src/ai/tools.ts#searchProducts
npx jase-tunnel tools add ./src/ai/tools.ts#tools.lookup --name lookup_product
npx jase-tunnel tools remove lookup_product
```

L'aggiunta manuale abilita l'elemento e lo conserva nelle scansioni successive (`--public` se lo strumento non ha `authorize`). La rimozione lo disabilita, anche dopo una nuova scansione; una nuova scansione non riabilita mai nulla. Un normale export funzione può essere aggiunto fornendo schema e descrizione:

```sh
npx jase-tunnel tools add ./src/catalog.ts#lookup --name lookup_product --schema ./schemas/lookup.json --description "Cerca un prodotto nel catalogo" --public
```

Per dichiarare un tool direttamente:

```ts
import { defineTool } from 'jase-tunnel/server';
export const lookupProduct = defineTool({
  name: 'lookup_product',
  description: 'Cerca un prodotto nel catalogo',
  parameters: { type: 'object', properties: { sku: { type: 'string' } }, required: ['sku'], additionalProperties: false },
  readOnly: true,
  // Obbligatorio (oppure public: true): chi chiede, e con quali argomenti?
  async authorize({ request }, { sku }) {
    const user = await session.fromRequest(request);
    return !!user && catalog.visibleTo(user, sku);
  },
  async execute({ sku }, { request, signal }) {
    return catalog.lookupForRequest(request, sku, { signal });
  }
});
```

Lo schema viene validato prima di `authorize` e dell'esecutore. `request` contiene la richiesta e i cookie del servizio, `signal` la cancellazione, `sessionId` un identificatore opaco; il modulo non aggiunge credenziali proprie. I permessi applicativi non si possono dedurre dai sorgenti: gli esecutori devono conservare le verifiche del progetto. Un hook `authorize(request)` nella configurazione può limitare l'intero pannello; ogni tool ha il proprio `authorize(context, args)` o `public: true`.

Uno strumento è eseguito senza conferma soltanto se dichiara `readOnly: true`, `riskLevel: 'read'` o `annotations.readOnlyHint: true` (oppure con `--read-only`). Gli altri richiedono una conferma nel pannello, vincolata a browser, chiamate e scadenza. L'inventario rilevato non comporta l'esecuzione di alcun tool. `readOnly` non sostituisce l'autorizzazione dei dati privati.

## App desktop e collegamento

Serve **Jase Tunnel 0.3.0 o successivo** sul computer del visitatore; il pannello propone il download dalle release di GitHub. Il visitatore non deve fare altro: quando il browser lo consente senza mostrare richieste di permesso, la pagina si annuncia all’app, che verifica dominio e manifest e mostra una notifica. «Collega» nella notifica fa proseguire la pagina fino al consenso nell’app, dove il visitatore sceglie il modello. Prima del consenso il sito non riceve l’inventario dei modelli.

Chrome ed Edge chiedono il permesso di accesso locale prima che una pagina contatti l’app. Il pannello non si annuncia da solo se questo farebbe comparire la richiesta: al primo contatto il visitatore usa il pulsante «Collega il mio modello», poi su quel sito le notifiche arrivano da sole.

Il collegamento dura al massimo trenta minuti, resta vincolato a quel modello e può essere revocato dal pannello del sito o dal pulsante **Scollega siti** nell’app. La revoca interrompe anche le inferenze attive.

In produzione usare HTTPS e, se il reverse proxy cambia l’autorità della Request, configurare `JASE_TUNNEL_ORIGIN=https://il-tuo-dominio`.

Le richieste senza il cookie del modulo (primo collegamento, associazione dell’app) sono limitate per indirizzo del client e, con un margine più ampio, tutte insieme. L’adattatore Node usa l’indirizzo del socket. Dietro un reverse proxy che imposta `X-Forwarded-For` (nginx, un load balancer, Next.js in produzione) imposta `JASE_TUNNEL_TRUST_PROXY=true` o `trustProxy: true` nella configurazione: altrimenti tutti i client condividono lo stesso limite. Non attivarlo senza un proxy che sovrascrive l’header, perché il client potrebbe sceglierlo da sé. Per un servizio in `localhost` serve una build di sviluppo dell’app (`npm run build:dev` nel repository): una release accetta solo siti HTTPS pubblici. Se presente, la CSP del sito deve permettere `connect-src http://127.0.0.1:11439` e lo script del pannello dal proprio dominio; il modulo non allenta automaticamente le policy del progetto.

Il broker usa la memoria del processo: servono un’istanza Node persistente e affinità delle richieste. **Non funziona su Vercel, Netlify, Cloudflare Workers o altre funzioni serverless**, né con più worker senza affinità: le richieste in attesa vivono in un solo processo. Un riavvio richiede un nuovo collegamento. Le conversazioni del pannello non vengono salvate su disco. Streaming, MCP remoto e integrazione automatica in chatbot arbitrari non sono dichiarati supportati.

## Licenza

[Apache License 2.0](LICENSE); avvisi in [`NOTICE`](NOTICE). Il nome «Jase Tunnel» e il logo seguono la [politica del marchio](https://github.com/Strnozz/jase-tunnel/blob/main/TRADEMARK_POLICY.md) del progetto.
