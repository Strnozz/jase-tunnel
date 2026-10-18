# Jase Tunnel SDK

Un modulo server e browser indipendente dal sito Jase. Usa la LLM sul computer della persona; strumenti, dati e autorizzazioni rimangono sul server del progetto. Nessuna API di inferenza esterna, account Tunnel o relay obbligatorio.

## Un comando nel progetto

La distribuzione prevista è un pacchetto npm standard nelle release pubbliche di questo repository:

```sh
npx --yes https://github.com/Strnozz/jase-tunnel/releases/download/sdk-v0.2.0/jase-tunnel-sdk-0.2.0.tgz init
```

Il comando sarà scaricabile dopo la pubblicazione del tag `sdk-v0.2.0`. Non richiede un account npm per l'integratore. Un'eventuale pubblicazione futura sul registro npm permetterà anche `npx @jase-tunnel/sdk init`.

Prima della pubblicazione si può usare il pacchetto locale:

```sh
npx --yes --package /percorso/jase-tunnel-sdk-0.2.0.tgz jase-tunnel init --package /percorso/jase-tunnel-sdk-0.2.0.tgz
```

Node.js 22 o successivo. Nei progetti **Next.js App Router** `init` installa la dipendenza con versione fissata, genera il manifest pubblico e il broker, registra i tool riconosciuti e aggiunge il pannello React al layout. Non modifica il chatbot preesistente: il pannello offre una conversazione che usa gli strumenti del progetto attraverso il modello personale. I file esistenti in conflitto vengono conservati e segnalati prima di scrivere. Layout e registri modificati hanno una copia in `.jase-tunnel/backups/`; questa cartella è esclusa da Git.

`--dry-run` mostra i file previsti senza scrivere o installare. `--cwd <cartella>` seleziona il progetto. `--package <tarball>` permette un pacchetto locale o un'altra distribuzione approvata. L'installazione npm non esegue script di lifecycle.

Per altri server JavaScript viene generata la configurazione; il collegamento al router del framework resta esplicito. Il nucleo accetta le API Fetch standard:

```ts
import site from './jase-tunnel.config';
const response = await site.handle(request);
```

Inoltrare `/.well-known/jase-tunnel.json` e `/api/jase-tunnel/v1/*`; nel frontend montare `JaseTunnel` da `@jase-tunnel/sdk/react`, oppure usare `TunnelClient` da `@jase-tunnel/sdk/browser`. L'installazione automatica completa non è dichiarata per Python, PHP, Ruby, Next Pages Router o router server sconosciuti.

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

L'aggiunta manuale conserva l'elemento nelle scansioni successive. La rimozione lo disabilita, anche dopo una nuova scansione. Un normale export funzione può essere aggiunto fornendo schema e descrizione:

```sh
npx jase-tunnel tools add ./src/catalog.ts#lookup --name lookup_product --schema ./schemas/lookup.json --description "Cerca un prodotto nel catalogo"
```

Per dichiarare un tool direttamente:

```ts
import { defineTool } from '@jase-tunnel/sdk/server';
export const lookupProduct = defineTool({
  name: 'lookup_product',
  description: 'Cerca un prodotto nel catalogo',
  parameters: { type: 'object', properties: { sku: { type: 'string' } }, required: ['sku'], additionalProperties: false },
  readOnly: true,
  async execute({ sku }, { request, signal }) {
    // Conserva qui i controlli d'accesso del tuo servizio.
    return catalog.lookupForRequest(request, sku, { signal });
  }
});
```

Lo schema viene validato prima dell'esecutore. `request` contiene la richiesta e i cookie del servizio, `signal` la cancellazione, `sessionId` un identificatore opaco; il modulo non fornisce credenziali Jase. I permessi applicativi non si possono dedurre dai sorgenti: gli esecutori devono conservare le verifiche del progetto. Un hook `authorize(request)` nella configurazione può limitare l'intero pannello; un tool può avere `authorize(context, args)`.

Uno strumento è eseguito senza conferma soltanto se dichiara `readOnly: true`, `riskLevel: 'read'` o `annotations.readOnlyHint: true` (oppure aggiunta esplicita con `--read-only`). Gli altri richiedono una conferma nel pannello, vincolata a browser, chiamate e scadenza. L'inventario rilevato non comporta l'esecuzione di alcun tool. `readOnly` non sostituisce l'autorizzazione dei dati privati.

## App desktop e collegamento

Serve **Jase Tunnel 0.2.0 o successivo**. La release 0.1.0 accetta soltanto Jase e non viene offerta da questo SDK a un altro servizio. Il sito espone `/.well-known/jase-tunnel.json`; il browser contatta il loopback, il tunnel verifica dominio e manifest e l'utente conferma il dominio e il modello nel pannello nativo. Il browser può richiedere il permesso di accesso locale. Prima del consenso il sito non riceve l'inventario dei modelli.

Il collegamento dura al massimo trenta minuti, resta vincolato a quel modello e può essere revocato dal sito o dal pulsante **Scollega siti** nell'app. La revoca nativa interrompe anche le inferenze attive. Il protocollo legacy di Jase resta separato.

In produzione usare HTTPS e, se il reverse proxy cambia l'autorità della Request, configurare `JASE_TUNNEL_ORIGIN=https://il-tuo-dominio`. Per localhost usare la build desktop di sviluppo (`npm run build:dev`): una release non accetta origini loopback. Se presente, la CSP del sito deve permettere `connect-src http://127.0.0.1:11439`; il modulo non allenta automaticamente le policy del progetto.

Il primo adattatore del broker usa memoria del processo: un'istanza Node persistente e affinità delle richieste sono necessarie. Non è un broker distribuito per funzioni serverless o più worker; un riavvio richiede un nuovo collegamento. Le conversazioni del pannello non vengono salvate su disco. Streaming, MCP remoto e integrazione automatica in chatbot arbitrari non sono dichiarati supportati.

## Integrazione in Jase

Il sottopercorso `legacy` contiene il broker estratto con i contratti precedenti; `protocol`, `platforms` e `releases` espongono protocollo browser, rilevamento del sistema e catalogo GitHub. Jase installa questo stesso pacchetto mantenendo gli adattatori del proprio agente, i controlli sulle conversazioni e il registro strumenti con contesto dinamico.
