# Architettura

Jase Tunnel è un programma autonomo: collega la LLM eseguita sul computer della persona ai siti che adottano il modulo `jase-tunnel`, dopo un consenso esplicito. Il programma non contiene domini predefiniti e non dipende da un servizio centrale. Questo repository distribuisce sia l’app desktop sia il modulo per i siti (`packages/sdk`).

## Componenti

1. **Il sito** installa il modulo con `npx jase-tunnel init`. Il modulo pubblica il manifest `/.well-known/jase-tunnel.json`, monta il broker sotto `/api/jase-tunnel/v1/` e aggiunge il pannello alle pagine.
2. **La pagina** si annuncia all’app su `http://127.0.0.1:11439/tunnel/v1/discover`, quando il browser lo consente senza mostrare richieste di permesso al visitatore. Altrimenti il contatto parte dal pulsante del pannello.
3. **L’app** verifica il manifest sul dominio dichiarato dall’header `Origin`, solo verso IP pubblici e senza redirect. Se il sito è compatibile, mostra una notifica di sistema: «example.com accetta Jase Tunnel».
4. **Il consenso:** «Collega» nella notifica (o il pulsante della pagina) fa proseguire la pagina. La pagina chiede un ticket al proprio server e lo consegna all’app. L’app mostra dominio, dati scambiati e selettore del modello. Solo dopo la conferma scambia il ticket con il broker del sito e ottiene una concessione valida al massimo trenta minuti, vincolata al modello scelto.
5. **Il trasporto:** l’app riceve i turni dal broker con richieste ad attesa lunga, esegue l’inferenza su Ollama o LM Studio e restituisce testo e richieste di strumenti. Gli strumenti vengono eseguiti e autorizzati dal server del sito; l’app non esegue comandi ricevuti dal modello.

Il protocollo è `jase-tunnel-v1`, descritto in [WEB-STANDARD.md](WEB-STANDARD.md). Non serve un relay di terzi e il router non deve esporre porte in ingresso.

## Integrazione MCP facoltativa

L’SDK 0.5 espone `mcpTools` da `jase-tunnel/mcp`. Il server del sito interroga un endpoint MCP configurato dal proprietario con il client ufficiale opzionale, seleziona gli strumenti abilitati e inoltra le chiamate autorizzate. L’app desktop e il browser non ricevono le credenziali MCP e non cambiano protocollo. Il server MCP resta responsabile dei propri dati e permessi.

Le politiche `authorize`/`public` e `readOnly` sono dichiarazioni del proprietario: non vengono ricavate automaticamente dalle annotazioni del server remoto. Cataloghi e risultati sono limitati, le operazioni scadono e rispettano la cancellazione, i redirect sono rifiutati e i client vengono chiusi dopo ciascuna operazione per evitare sessioni condivise fra visitatori. Sono supportati strumenti Streamable HTTP con risultati testuali e JSON strutturati; stato implicito tra connessioni e funzionalità MCP ulteriori richiedono un’integrazione distinta. [Configurazione ed esempio](../packages/sdk/README.md#strumenti-da-un-server-mcp-sdk-05).

## Interfacce desktop

macOS usa AppKit, Windows le API Win32 e Linux GTK 3, caricata da `purego`. Il motore e il pannello restano nello stesso processo. Tutti i pannelli leggono copie dello stato prive di token o percorsi dei pesi; il registro non ha API HTTP.

| Sistema | Notifica di sito compatibile | Clic su «Collega» |
| --- | --- | --- |
| macOS | Notification Center (UserNotifications), dal bundle firmato dell’app | Pulsante «Collega» o clic sulla notifica |
| Windows | Fumetto dell’icona nell’area di notifica (notifica di sistema su Windows 10/11) | Clic sulla notifica |
| Linux | `notify-send` verso il servizio di notifica del desktop (pacchetto `libnotify-bin`) | «Collega», dove il desktop supporta le azioni |

Al massimo una notifica per sito al giorno, tre ogni dieci minuti e nessuna per un sito già collegato. Ogni annuncio resta anche nel registro del pannello, così l’informazione non si perde con le notifiche disattivate. Su macOS il Centro notifiche accetta solo un’app registrata in Applicazioni o `~/Applications`: avviata da una cartella temporanea, macOS rifiuta l’autorizzazione e l’annuncio resta solo nel pannello, come per un binario di sviluppo senza bundle.

Il menu dell’icona contiene Apri Jase Tunnel, Aggiungi pesi e Chiudi Jase Tunnel. **Scollega siti** revoca tutte le concessioni e interrompe le inferenze in corso. Chiudere la sola finestra lascia il servizio nell’area di notifica; uscire dall’app annulla motore e inferenze.

L’identità del pannello nello stato locale è `appkit-v1`, `win32-v1` o `gtk3-v1`: il riavvio dell’eseguibile riapre l’istanza con la stessa versione e modalità. Una versione differente, comprese le 0.2 e precedenti, chiede conferma prima del riavvio.

## Superfici del servizio locale

| Endpoint | Chi lo usa | Uso |
| --- | --- | --- |
| `POST /tunnel/v1/discover` | Qualsiasi sito HTTPS pubblico | Annuncio della pagina; restituisce una ricevuta di cinque minuti |
| `GET /tunnel/v1/discovery` | La pagina con la propria ricevuta | Stato della propria scoperta |
| `POST /tunnel/v1/pair` | La pagina con la propria ricevuta | Consegna del ticket; apre il consenso nell’app |
| `GET /` e `GET /v1/status` | Pagina di stato locale o processo locale | Versione e numero di modelli |
| `POST /v1/show-window`, `/v1/add-model`, `/v1/quit` | Solo la pagina di stato locale | Pannello, selettore dei pesi, chiusura |

Il servizio verifica l’header `Host` contro il DNS rebinding. Le API `/v1/` rifiutano qualsiasi `Origin` diverso dalla pagina di stato locale. Le API `/tunnel/v1/` non restituiscono inventario dei modelli, file, registro o altre connessioni.

## Motori locali e dati

- Ollama: `127.0.0.1:11434`.
- LM Studio: `127.0.0.1:1234`, con API locale abilitata; l’eventuale `JASE_LMSTUDIO_TOKEN` rimane sul computer. Il tunnel tiene al massimo un’istanza propria per modello: se serve più contesto, sostituisce quella caricata in precedenza senza scaricare le istanze aperte dall’utente. Il contesto richiesto non può superare quello pubblicato per il modello.
- I modelli cloud di Ollama sono esclusi. Non viene scelto un provider remoto se il modello locale non risponde.
- La scansione è limitata ai percorsi predefiniti dei modelli e all’eventuale `OLLAMA_MODELS`, senza leggere le schede del browser o percorrere interi dischi.
- Lo stato privato è nella sottocartella `Jase Tunnel` della cartella di configurazione dell’utente. `--state-dir` permette una posizione separata per le prove. All’avvio restano solo le concessioni non scadute del protocollo corrente.
- I dettagli di prompt e risposte nel registro sono facoltativi. `--debug` può scriverli nei log locali.

## Limiti del browser

Una normale app desktop non riceve gli URL visitati, e Jase Tunnel non legge le schede. La pagina deve contattare l’app. Chrome ed Edge chiedono al visitatore il permesso prima che una pagina pubblica contatti un servizio sul computer (Local Network Access), anche quando l’app non è installata. Per non mostrare quella richiesta a tutti i visitatori, il modulo si annuncia da solo solo in due casi: il permesso è già concesso, oppure il browser non prevede quel permesso e la persona si è già collegata su quel sito. Al primo contatto serve il pulsante del pannello; dopo, su quel sito le notifiche arrivano da sole.
