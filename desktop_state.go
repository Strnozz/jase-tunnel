package main

import "time"

// The native panel reads a sanitized snapshot directly through the in-process
// bridge. No HTTP endpoint, browser cookie or UI credential exposes this journal.
func (a *application) desktopState(after uint64) map[string]any {
	a.mu.RLock()
	models := append([]model{}, a.models...)
	files := append([]modelFile{}, a.files...)
	origins := []string{}
	seenOrigins := map[string]bool{}
	for _, c := range a.settings.Links {
		if !seenOrigins[c.Origin] {
			origins = append(origins, c.Origin)
			seenOrigins[c.Origin] = true
		}
	}
	a.mu.RUnlock()
	a.activity.mu.Lock()
	defer a.activity.mu.Unlock()
	events := []activityEvent{}
	for _, e := range a.activity.events {
		if e.ID > after {
			events = append(events, e)
		}
	}
	connections := []map[string]any{}
	for _, origin := range origins {
		seen := a.activity.contacts[origin]
		connections = append(connections, map[string]any{"origin": origin, "connected": !seen.IsZero() && time.Since(seen) < 90*time.Second})
	}
	return map[string]any{"version": version, "models": models, "files": files, "connections": connections, "debug": a.debug.Load(), "thinking": a.think.Load(), "events": events, "cursor": a.activity.next, "generation": a.activity.generation, "statistics": map[string]uint64{"sent": a.activity.sent, "received": a.activity.received, "modelTurns": a.activity.modelTurns, "errors": a.activity.errors}}
}

func (a *application) setDetailedLogging(enabled bool) {
	a.debug.Store(enabled)
	if !enabled {
		a.clearActivity()
	}
	a.record("app", "info", "Log dettagliato aggiornato", map[bool]string{true: "I testi di prompt e risposte sono visibili solo su questo computer.", false: "Testi disattivati. I precedenti eventi sono stati rimossi dalla vista; eventuali file di log già scritti restano sul computer."}[enabled])
}

func (a *application) setModelThinking(enabled bool) {
	a.think.Store(enabled)
	a.record("model", "info", "Ragionamento del modello aggiornato", map[bool]string{true: "Attivo per i modelli che lo supportano.", false: "Disattivato per le prossime richieste."}[enabled])
}
