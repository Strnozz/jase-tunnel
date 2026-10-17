# Jase Tunnel

App desktop che collega i modelli eseguiti sul computer dell’utente agli strumenti IA di Jase. Questa cartella contiene i sorgenti del tunnel e tutto il necessario per compilarlo come progetto autonomo, senza il repository del sito.

## Esperienza da mantenere

**Scarichi Jase Tunnel, lo apri e l’app ti indica se puoi collegarti al sito.** Un solo programma, senza estensioni obbligatorie, registrazione o servizi di tunnel esterni a pagamento.

L’obiettivo successivo è permettere anche ad altri siti di dichiarare la propria compatibilità e mostrarla nell’app. Questa copia contiene il collegamento già implementato con Jase: il riconoscimento di altri siti e le relative notifiche sono ancora da sviluppare. L’app attuale non legge le schede del browser.

La prima proposta del flusso e del protocollo per piattaforme indipendenti è in [docs/WEB-STANDARD.md](docs/WEB-STANDARD.md): dichiarazione del sito, scoperta, notifica, consenso e collegamento alla LLM scelta dall’utente. È una specifica di progetto, ancora da implementare.

## Cosa contiene oggi

- Motore Go per Windows, macOS e Linux, su Intel/AMD e ARM.
- Interfaccia macOS nativa AppKit, icona dell’app derivata dal favicon e J bianca nella barra dei menu.
- Interfaccia Windows nativa Win32, J nell’area di notifica, doppio clic per aprire il pannello e tasto destro per il menu.
- Interfaccia Linux nativa GTK 3, con supporto AppIndicator o icona GTK nei desktop compatibili.
- Menu con **Apri Jase Tunnel**, **Aggiungi pesi** e **Chiudi Jase Tunnel**.
- Rilevamento di Ollama, LM Studio e dei file nei percorsi predefiniti dei modelli; selezione manuale di altri pesi.
- Associazione alla sessione del browser senza account e comunicazione diretta con il server Jase.
- Stato dei collegamenti e registro locale di invii, ricezioni e inferenze sui tre sistemi, con filtri e dettagli selezionabili.
- Test del trasporto, degli adattatori dei modelli e dei confini di riservatezza.

Su tutti e tre i sistemi l’app apre il pannello e rimane riconoscibile mentre lavora in background. La voce **Chiudi Jase Tunnel** termina anche il servizio e le inferenze del tunnel, lasciando aperti Ollama e LM Studio. Riaprire l’eseguibile porta in primo piano l’istanza già avviata. L’interfaccia legge il registro direttamente nello stesso processo: non lo espone al browser.

Su Windows l’icona compare nell’area di notifica vicino all’orologio; Windows può raggrupparla nel menu delle icone nascoste. Il doppio clic apre il pannello e il tasto destro mostra le stesse tre voci del Mac. Chiudere la finestra mantiene il tunnel e l’icona attivi. L’icona viene ripristinata se Esplora risorse si riavvia.

Su Linux occorre GTK 3 (pacchetto `libgtk-3-0` o `libgtk-3-0t64` su Ubuntu/Debian). L’app usa Ayatana AppIndicator, se installato, oppure l’icona GTK tradizionale. Il desktop deve supportare le icone di notifica: se non esiste un’area di notifica disponibile, chiudere la finestra la riduce a icona mantenendola raggiungibile dalla barra applicazioni. Non viene nascosta senza un modo per riaprirla.

Ollama o LM Studio devono essere installati e attivi: i motori e i pesi non sono inclusi.

## Sviluppo

Il motore e la UI Windows usano la libreria standard Go e le API del sistema. La UI Linux usa `purego` per caricare GTK senza CGO; la versione è fissata in `go.mod`/`go.sum` e la licenza è in `third_party/`. Go minimo: 1.25; toolchain verificata: 1.27.2. Al primo avvio dello sviluppo scaricare la dipendenza di compilazione:

```sh
go mod download
```

I test con rilevamento delle race richiedono un compilatore C:

```sh
go test -race ./...
```

Il workflow include anche una prova del pannello GTK in una sessione Linux virtuale con Xvfb e verifiche delle API native Windows. Le prove specifiche di un sistema si eseguono sul relativo runner, non attraverso la compilazione incrociata su Mac.

Su macOS servono anche gli Xcode Command Line Tools per l’interfaccia AppKit. Per avviare una prova isolata con un sito Jase già aperto in locale:

```sh
go run . --foreground --dev --site http://localhost:3000 --port 11440 --state-dir .deploy-check/state
```

La porta alternativa evita di interferire con un’app già avviata; per il collegamento dal sito si usa normalmente la porta `11439`, chiudendo prima l’altra istanza. La cartella di stato contiene credenziali private ed è esclusa da Git. I test automatici usano fixture e server temporanei, senza accedere al database del sito.

## Compilazione dei pacchetti

Per generare tutti i pacchetti serve macOS con gli Xcode Command Line Tools, Go e Node.js 22 o successivo. Node e `sharp` servono solo alla compilazione delle icone: l’utente finale non deve installarli.

```sh
npm ci
npm test
npm run build:dev
```

`JASE_GO` può indicare un compilatore Go installato in un percorso diverso. La build produce in `dist-dev/` cinque pacchetti: Windows x64/ARM64, Linux x64/ARM64 e un’app macOS universale. Include `manifest.json` e `SHA256SUMS`. Per la versione che accetta il sito di produzione:

```sh
npm run build
```

Questa versione scrive in `dist/`. `--out <cartella>` permette un percorso differente. Il bundle macOS ha una firma ad hoc; firma Developer ID, notarizzazione Apple e firma Windows non sono configurate.

Finché la cartella non ha un proprio repository Git, i pacchetti dichiarano commit `local` e includono l’impronta dei sorgenti. Dopo l’inizializzazione del repository useranno il suo commit, senza ereditare quello dell’eventuale cartella contenitore. La compilazione locale non pubblica i pacchetti.

## Collegamento al sito

Il sito deve implementare il protocollo `jase-companion-v1`, incluso il broker sul server. Quel componente appartiene al sito Jase e non è incluso in questa app. I dettagli e i confini attuali sono in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

La versione ordinaria accetta `https://jaseengine.it`; `--dev` abilita anche le origini HTTP su loopback. La copia conserva le restrizioni e i controlli di sicurezza esistenti. Il futuro supporto ad altri siti richiederà un’integrazione esplicita, mantenendo l’esperienza di una sola app da scaricare.

## Struttura

| Percorso | Contenuto |
| --- | --- |
| `*.go`, `desktop_darwin.m` | Motore, adattatori, interfaccia nativa e test |
| `assets/icon.svg` | Favicon sorgente delle icone |
| `assets/desktop-icon.png` | Icona derivata dal favicon, incorporata negli eseguibili Windows/Linux |
| `scripts/` | Compilazione, generazione icone e test |
| `.github/workflows/check.yml` | Verifiche su Windows, macOS e Linux nel futuro repository |
| `VERSION` | Versione dell’app |

Il modulo Go conserva il nome interno `jase/companion` per mantenere questa estrazione identica ai sorgenti originali. La cartella originale `companion/` nel progetto Jase resta al suo posto; le modifiche future nelle due copie non si sincronizzano automaticamente.
