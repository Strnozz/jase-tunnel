# SDK 0.5.0: strumenti MCP facoltativi

Questa release aggiorna solo il modulo npm `jase-tunnel`. L’app desktop rimane compatibile con `jase-tunnel-v1` e non richiede una nuova release per usare gli strumenti MCP.

- Nuovo import `mcpTools` da `jase-tunnel/mcp`, con dichiarazioni TypeScript.
- Client MCP ufficiale opzionale `@modelcontextprotocol/client ^2.3.1`, installato solo dai servizi che scelgono MCP.
- Streamable HTTP: negoziazione della revisione 2026-07-28 e fallback alla revisione 2025 dei server compatibili.
- Solo strumenti esplicitamente selezionati, con alias locali e politiche `authorize` oppure `public`; `readOnly` richiede una dichiarazione del proprietario.
- Credenziali determinate dal servizio per ogni richiesta, senza inoltro automatico dei cookie del visitatore e senza client MCP condivisi tra visitatori.
- Supporto JSON Schema 2020-12 oltre agli schemi già accettati, risultati testuali/JSON, scadenze, cancellazione e limiti di dimensione. Redirect rifiutati.
- Negozio dimostrativo con server MCP ufficiale e test del percorso completo fino alla conferma e alla risposta del modello locale.

Le funzionalità MCP aggiuntive, i server che richiedono stato implicito fra connessioni e i contenuti multimodali non sono inclusi. Configurazione e limiti sono nel [README dell’SDK](https://github.com/Strnozz/jase-tunnel/blob/sdk-v0.5.0/packages/sdk/README.md#strumenti-da-un-server-mcp-sdk-05).

Il workflow SDK prepara il pacchetto con provenienza GitHub e lo carica nello staging npm. Nessun token provoca una pubblicazione diretta: la versione diventa pubblica soltanto dopo l’approvazione del proprietario con 2FA.
