# Jase Tunnel

Un programma desktop autonomo che crea un tunnel tra la LLM eseguita sul tuo computer (Ollama o LM Studio) e i servizi web che lo accettano. Nessun account, nessun relay esterno, nessuna estensione del browser.

## Come funziona

**Per chi naviga:** scarichi Jase Tunnel e lo apri (gli eseguibili non sono firmati: il sistema chiede una conferma, vedi [Eseguibili non firmati](#eseguibili-non-firmati)). Mentre navighi, quando una pagina accetta il tunnel ricevi una notifica del sistema: «example.com accetta Jase Tunnel». Scegli «Collega», confermi dominio e modello nel pannello dell’app e gli strumenti IA di quel sito usano la tua LLM. Il collegamento dura al massimo trenta minuti e **Scollega siti** lo interrompe subito.

**Per chi gestisce un servizio:** un comando nella cartella del progetto installa e configura il modulo.

```sh
npx jase-tunnel init
```

`init` installa il pacchetto `jase-tunnel`, rileva gli strumenti IA già esportati dal progetto (lasciandoli disabilitati finché non li abiliti), pubblica il manifest `/.well-known/jase-tunnel.json`, collega il broker al server e aggiunge il pannello alle pagine. Configura da solo Next.js App Router, Express e i server `node:http`. Dettagli in [packages/sdk](packages/sdk/README.md).

Il modello gira sul computer del visitatore, quindi è il visitatore a decidere quali strumenti chiamare e con quali argomenti: ogni strumento abilitato è, di fatto, un’API pubblica del sito. Il modulo accetta solo strumenti con un controllo `authorize(context, args)` o dichiarati `public`.

L’app non legge le schede del browser e non contiene domini predefiniti: sono le pagine compatibili a contattarla, e ogni accesso alla LLM richiede il consenso nell’app. Protocollo e confini sono descritti in [docs/WEB-STANDARD.md](docs/WEB-STANDARD.md) e [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

### Il primo contatto con un sito

Chrome ed Edge chiedono al visitatore il permesso prima che una pagina contatti un’app sul computer, anche se l’app non è installata. Per non disturbare chi non usa Jase Tunnel, il modulo si annuncia da solo solo dove quella richiesta non compare: permesso già concesso, oppure browser senza quel permesso e persona già collegata su quel sito. La prima volta su un sito si usa il pulsante **Jase Tunnel** della pagina; da lì in poi, su quel sito, la notifica arriva da sola.

## Cosa contiene

- Motore Go per Windows, macOS e Linux, su Intel/AMD e ARM.
- Interfacce native: AppKit su macOS, Win32 su Windows, GTK 3 su Linux. Icona nella barra dei menu o nell’area di notifica, con **Apri Jase Tunnel**, **Aggiungi pesi** e **Chiudi Jase Tunnel**.
- Notifiche dei siti compatibili: Notification Center su macOS, notifica dell’area di notifica su Windows, `notify-send` su Linux. Al massimo una per sito al giorno e tre ogni dieci minuti; ogni annuncio resta anche nel registro del pannello.
- Rilevamento di Ollama, LM Studio e dei file nei percorsi predefiniti dei modelli; selezione manuale di altri pesi.
- Stato dei collegamenti e registro locale di invii, ricezioni e inferenze, con filtri e dettagli selezionabili.
- Il modulo per i siti (`packages/sdk`, pacchetto npm `jase-tunnel`): installatore, broker, pannello web e adattatori per Next.js, Express e `node:http`.
- Adattatore MCP facoltativo (`jase-tunnel/mcp`, SDK 0.5): il sito seleziona gli strumenti di un server MCP, conserva autorizzazioni e credenziali sul proprio server e li mette a disposizione del modello locale del visitatore. [Configurazione](packages/sdk/README.md#strumenti-da-un-server-mcp-sdk-05).

Su tutti e tre i sistemi l’app apre il pannello e rimane riconoscibile mentre lavora in background. **Chiudi Jase Tunnel** termina anche il servizio e le inferenze del tunnel, lasciando aperti Ollama e LM Studio. Riaprire l’eseguibile porta in primo piano l’istanza già avviata.

Su macOS sposta **Jase Tunnel** nella cartella Applicazioni (o in `~/Applications`) prima di aprirlo: il Centro notifiche riconosce solo le app installate lì. Al primo sito compatibile macOS chiede il permesso di mostrare notifiche.

Su Windows l’icona compare nell’area di notifica vicino all’orologio; Windows può raggrupparla nel menu delle icone nascoste. Il doppio clic apre il pannello e il tasto destro mostra il menu. Chiudere la finestra mantiene il tunnel e l’icona attivi.

Su Linux occorre GTK 3 (pacchetto `libgtk-3-0` o `libgtk-3-0t64` su Ubuntu/Debian); per le notifiche serve `notify-send` (`libnotify-bin`). L’app usa Ayatana AppIndicator, se installato, oppure l’icona GTK tradizionale. Senza un’area di notifica, chiudere la finestra la riduce a icona.

Ollama o LM Studio devono essere installati e attivi: i motori e i pesi non sono inclusi.

## Eseguibili non firmati

**I pacchetti di Jase Tunnel non sono firmati.** Su macOS manca la firma Developer ID con notarizzazione Apple (il bundle ha solo una firma ad hoc); su Windows manca la firma Authenticode. Il sistema operativo non può quindi verificare chi ha prodotto l’eseguibile né che non sia stato modificato dopo la pubblicazione, e lo presenta come software di uno sviluppatore non identificato.

**Aprendo Jase Tunnel accetti questo rischio di sicurezza sul tuo sistema.** L’app resta in esecuzione, ascolta su `127.0.0.1:11439` e usa i modelli locali: installala solo se ti fidi di questo progetto, scaricala solo dalle [release ufficiali](https://github.com/Strnozz/jase-tunnel/releases) e mai da copie ricevute da altri.

Per aprirla la prima volta:

- **macOS:** apri l’app, chiudi l’avviso, poi vai in **Impostazioni di Sistema → Privacy e sicurezza** e scegli **Apri comunque** accanto a Jase Tunnel. Fino a macOS 14 basta anche il clic destro sull’app → **Apri** → **Apri**.
- **Windows:** nella finestra «Windows ha protetto il PC» di SmartScreen scegli **Ulteriori informazioni** → **Esegui comunque**. Alcuni antivirus possono segnalare o mettere in quarantena l’eseguibile.
- **Linux:** l’archivio `.tar.gz` non ha firma; estrailo e avvia l’eseguibile.

Prima di aprirla puoi confrontare l’impronta del file con `SHA256SUMS` della stessa release: `shasum -a 256 <file>` su macOS, `sha256sum <file>` su Linux, `Get-FileHash <file>` in PowerShell. Il controllo rileva un download corrotto o sostituito durante il trasferimento, ma non protegge da una release pubblicata da un account compromesso: solo una firma lo farebbe.

## Sviluppo

Il motore e la UI Windows usano la libreria standard Go e le API del sistema. La UI Linux usa `purego` per caricare GTK senza CGO; la versione è fissata in `go.mod`/`go.sum` e la licenza è in `third_party/`. Go minimo: 1.25; toolchain verificata: 1.27.2.

```sh
go mod download
go test -race ./...
```

I test con rilevamento delle race richiedono un compilatore C. Il workflow include una prova del pannello GTK in una sessione Linux virtuale con Xvfb e verifiche delle API native Windows. Su macOS servono gli Xcode Command Line Tools per l’interfaccia AppKit.

Solo le build di sviluppo accettano siti HTTP su `localhost`. Per provare l’app con un servizio in locale:

```sh
go run -ldflags "-X main.devBuild=true" . --foreground --port 11440 --state-dir .deploy-check/state
```

La porta alternativa evita di interferire con un’app già avviata; il modulo dei siti usa la porta `11439`, quindi per una prova completa chiudi prima l’altra istanza e ometti `--port`. La cartella di stato contiene credenziali private ed è esclusa da Git. Le notifiche di macOS richiedono il bundle dell’app (`npm run build:dev`); un binario avviato con `go run` registra gli annunci solo nel pannello.

## Compilazione dei pacchetti

Per generare tutti i pacchetti serve macOS con gli Xcode Command Line Tools, Go e Node.js 22 o successivo. Node e `sharp` servono solo alla compilazione delle icone: l’utente finale non deve installarli.

```sh
npm ci
npm test
npm run build:dev
```

`JASE_GO` può indicare un compilatore Go installato in un percorso diverso. La build produce in `dist-dev/` cinque pacchetti: Windows x64/ARM64, Linux x64/ARM64 e un’app macOS universale, con `manifest.json` e `SHA256SUMS`. Per la versione che accetta solo siti HTTPS pubblici:

```sh
npm run build
```

Questa versione scrive in `dist/`. `--out <cartella>` permette un percorso differente. Il bundle macOS ha una firma ad hoc; firma Developer ID, notarizzazione Apple e firma Windows non sono configurate (vedi [Eseguibili non firmati](#eseguibili-non-firmati)).

## Release su GitHub

Gli eseguibili sono pubblicati in [Strnozz/jase-tunnel](https://github.com/Strnozz/jase-tunnel/releases); il pannello web dei siti propone il download da qui. Il workflow `release.yml` si attiva sulla pubblicazione di un tag che corrisponde a `VERSION`:

- `v0.3.2`: release stabile per i siti HTTPS pubblici.
- `dev-v0.3.2-preview1`: prerelease di sviluppo, che accetta anche siti in `localhost`.

Prima verifica il codice sui tre sistemi, poi compila i cinque pacchetti sul runner Mac. Controlla commit, run, canale, inventario e SHA-256 e carica i file in una bozza; solo dopo il caricamento completo rende pubblica la release. Una release già pubblica non viene sovrascritta.

Il workflow `sdk.yml` pubblica il modulo per i siti sui tag `sdk-vX.Y.Z`: allega il pacchetto alla release GitHub e, se il segreto `NPM_TOKEN` è configurato nel repository, lo pubblica sul registro npm. Senza la pubblicazione su npm il comando `npx jase-tunnel init` non è disponibile; in alternativa si usa `npx --yes <url del pacchetto .tgz> init`.

Per la pubblicazione diretta, `NPM_TOKEN` deve essere un token granulare con permesso di scrittura sui pacchetti e **Bypass two-factor authentication** abilitato. Se npm risponde `E_STAGE_REQUIRED`, il token consente solo lo staging: il workflow prepara lo stesso pacchetto con `npm stage publish`, poi il proprietario lo approva con 2FA sul sito npm o con `npm stage approve <stage-id>`. Fino all'approvazione la versione non è pubblica.

Per riprendere una pubblicazione dopo la creazione della release GitHub, avvia manualmente **Check and package Jase Tunnel SDK** da Actions, indicando il tag esistente in `release_tag`. Il workflow ricompila e confronta il pacchetto e le impronte con gli allegati già pubblicati prima di riprovare npm; non sostituisce i file della release.

## Struttura

| Percorso | Contenuto |
| --- | --- |
| `*.go`, `desktop_darwin.m` | Motore, protocollo, interfacce native e test |
| `packages/sdk` | Pacchetto npm `jase-tunnel`: installatore, broker, pannello web e adattatori |
| `assets/icon.svg` | Sorgente delle icone |
| `assets/desktop-icon.png` | Icona incorporata negli eseguibili Windows/Linux |
| `scripts/` | Compilazione, generazione icone, verifica delle release e test |
| `.github/workflows/` | Verifiche su Windows, macOS e Linux e pubblicazione |
| `docs/` | Architettura e protocollo `jase-tunnel-v1` |
| `VERSION` | Versione dell’app |
| `LICENSE`, `NOTICE` | Licenza Apache 2.0 e avvisi di attribuzione |
| `TRADEMARK_POLICY.md` | Uso del nome e del logo Jase Tunnel |

## Licenza e marchio

Jase Tunnel è open source con la [Apache License 2.0](LICENSE): app desktop, modulo per i siti e specifica del protocollo si possono usare, modificare e ridistribuire, anche in prodotti commerciali, conservando gli avvisi di [`LICENSE`](LICENSE) e [`NOTICE`](NOTICE) e indicando i file modificati. La licenza include una concessione esplicita dei brevetti dei contributori.

Il nome «Jase Tunnel» e il logo non fanno parte della licenza del codice. Il loro uso è regolato dalla [politica del marchio](TRADEMARK_POLICY.md): le versioni modificate possono chiamarsi `Jase Tunnel - Exp<NomeDistribuzione>` dichiarando le modifiche, oppure usare un nome del tutto diverso; le implementazioni indipendenti possono dichiararsi compatibili con il protocollo `jase-tunnel-v1`.

I componenti di terze parti incorporati nelle build sono elencati in [`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md).
