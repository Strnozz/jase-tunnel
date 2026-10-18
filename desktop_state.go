package main

import "time"

// The native panel reads a sanitized snapshot directly through the in-process
// bridge. No HTTP endpoint, browser cookie or UI credential exposes this journal.
func (a *application) desktopState(after uint64) map[string]any {
	sites := a.desktopSites()
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
	return map[string]any{"version": version, "models": models, "files": files, "connections": connections, "sites": sites, "debug": a.debug.Load(), "thinking": a.think.Load(), "events": events, "cursor": a.activity.next, "generation": a.activity.generation, "statistics": map[string]uint64{"sent": a.activity.sent, "received": a.activity.received, "modelTurns": a.activity.modelTurns, "errors": a.activity.errors}}
}

func (a *application) setDetailedLogging(enabled bool) {
	a.debug.Store(enabled)
	if !enabled {
		a.clearActivity()
	}
	a.record("app", "info", "Detailed logging updated", map[bool]string{true: "Prompt and response text is visible only on this computer.", false: "Text logging disabled. Previous events were cleared from the view; existing log files remain on this computer."}[enabled])
}

func (a *application) setModelThinking(enabled bool) {
	a.think.Store(enabled)
	a.record("model", "info", "Model reasoning updated", map[bool]string{true: "Enabled for models that support it.", false: "Disabled for future requests."}[enabled])
}
