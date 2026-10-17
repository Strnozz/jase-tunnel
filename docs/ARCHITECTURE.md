# Collegamento attuale e direzione del progetto

## Componenti

1. La pagina Jase ottiene un ticket di associazione legato alla sessione anonima del browser e alla sua origine.
2. La pagina consegna il ticket al tunnel in ascolto su `127.0.0.1:11439`.
3. Il tunnel scambia il ticket direttamente con il server Jase e conserva la credenziale nella cartella privata dell’app.
4. Il tunnel riceve i turni del modello dal server tramite richieste con attesa lunga, esegue l’inferenza su Ollama o LM Studio e restituisce testo e richieste di strumenti.
5. Gli strumenti vengono eseguiti e verificati dal server del sito, che invia al modello i dati necessari per il turno successivo. L’app non accede direttamente al database e non esegue comandi ricevuti dal modello.

Il protocollo corrente è `jase-companion-v1`. L’identità della sessione non dipende dall’indirizzo IP. Non è richiesto un relay di terzi e il router non deve esporre porte in ingresso.

## Superfici del client

| Endpoint locale | Uso |
| --- | --- |
| `GET /v1/status` | Protocollo, versione, disponibilità dei modelli e identificativi pubblici dei collegamenti |
| `POST /v1/pair` | Associazione con ticket e origine |
| `POST /v1/add-model` | Selettore locale di file o cartella |
| `POST /v1/import-model` | Importazione di un file rilevato, tramite identificativo |
| `POST /v1/show-window` | Apertura del pannello nativo macOS |
| `POST /v1/open-jase` | Apertura di Jase nel browser scelto |
| `POST /v1/quit` | Chiusura dell’app |

Le modifiche richiedono origine autorizzata e contenuto JSON. Il servizio verifica anche l’header `Host`. I percorsi completi dei pesi e le credenziali non sono esposti negli stati forniti alla pagina; il registro dell’interfaccia nativa non ha endpoint HTTP.

Il server del sito deve fornire il broker sotto `/api/companion/`, con associazione, pubblicazione dei modelli, consegna dei turni, verifica delle richieste ancora attive e ricezione dei risultati. La presente estrazione contiene il client di quel protocollo e i suoi test; non contiene il backend o il frontend Jase.

## Motori locali e dati

- Ollama: `127.0.0.1:11434`.
- LM Studio: `127.0.0.1:1234`, con API locale abilitata; l’eventuale `JASE_LMSTUDIO_TOKEN` rimane sul computer.
- I modelli cloud di Ollama sono esclusi. Non viene scelto un provider remoto se il modello locale non risponde.
- La scansione è limitata ai percorsi predefiniti dei modelli e all’eventuale `OLLAMA_MODELS`, senza leggere le schede del browser o percorrere interi dischi.
- Lo stato privato è nella sottocartella `Jase` della cartella di configurazione dell’utente. `--state-dir` permette una posizione separata per le prove.
- I dettagli di prompt e risposte nel registro sono facoltativi. `--debug` può scriverli nei log locali.

## Evoluzione richiesta: una sola app per i siti compatibili

Il requisito di prodotto è: l’utente scarica Jase Tunnel, lo avvia e riceve un’indicazione chiara della possibilità di collegarsi al sito, senza estensioni obbligatorie o account.

Per riconoscere un sito senza leggere tutte le schede del browser, il sito deve collaborare dichiarando la propria compatibilità e avviando il contatto con l’app. In assenza di contatto, il tunnel può indicare che non ha rilevato un collegamento; non può dedurre con certezza che il sito aperto sia incompatibile.

La prima proposta per dichiarazione, notifiche e autorizzazione di nuove origini è descritta in [WEB-STANDARD.md](WEB-STANDARD.md), insieme ai vincoli del primo contatto nei browser. Queste funzioni restano da implementare. La versione attuale mantiene l’elenco di origini esistente: `https://jaseengine.it` in uso ordinario, più le origini loopback HTTP in sviluppo. Il futuro supporto a più siti dovrà mantenere distinti collegamenti e credenziali, senza aprire indiscriminatamente l’accesso ai modelli locali.
