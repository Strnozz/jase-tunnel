package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var panelFilters = []struct{ Label, Category string }{
	{"Tutte le attività", "all"}, {"Chiamate al server", "server"},
	{"Ricezioni dal server", "receive"}, {"Modello e interprete", "model"}, {"Errori", "error"},
}

var trayLabels = []string{"Apri Jase Tunnel", "Aggiungi pesi", "Chiudi Jase Tunnel"}

type panelModel struct{ ID, Name, Engine, Status string }
type panelSnapshot struct {
	Title, Connection, Statistics, Privacy string
	Models                                 []panelModel
	Events                                 []activityEvent
	Generation                             uint64
	Debug, Thinking                        bool
}

// Every desktop uses the same sanitized in-process snapshot. The private
// journal never becomes an HTTP endpoint or a browser-accessible document.
func (a *application) panelSnapshot(filter int) panelSnapshot {
	s := a.desktopState(0)
	p := panelSnapshot{Title: "Jase Tunnel è in esecuzione", Connection: "Apri Jase IA e scegli «Usa il mio modello»", Debug: s["debug"].(bool), Thinking: s["thinking"].(bool), Generation: s["generation"].(uint64)}
	origins := []string{}
	for _, c := range s["connections"].([]map[string]any) {
		if c["connected"].(bool) {
			origins = append(origins, c["origin"].(string))
		}
	}
	if len(origins) > 0 {
		p.Title, p.Connection = "Collegato a Jase", strings.Join(origins, " · ")
	}
	stats := s["statistics"].(map[string]uint64)
	p.Statistics = fmt.Sprintf("↑ %d invii   ·   ↓ %d ricezioni   ·   ◇ %d turni del modello   ·   %d errori", stats["sent"], stats["received"], stats["modelTurns"], stats["errors"])
	p.Privacy = "Registro privato sul computer · Ultimi 300 eventi. Chiudendo la finestra il tunnel resta nell’area di notifica."
	if p.Debug {
		p.Privacy = "Il registro può contenere prompt e risposte. Disattivando i testi si svuota la vista; eventuali file di log già scritti restano sul computer."
	}
	for _, m := range s["models"].([]model) {
		engine, status := "Ollama", "Pronto"
		if m.Engine != "ollama" {
			engine = "LM Studio"
		}
		if !m.Tools {
			status = "Strumenti non disponibili"
		}
		p.Models = append(p.Models, panelModel{Name: m.Name, Engine: engine, Status: status})
	}
	for _, f := range s["files"].([]modelFile) {
		p.Models = append(p.Models, panelModel{ID: f.ID, Name: f.Name, Engine: f.Format, Status: "Pesi da aggiungere"})
	}
	if filter < 0 || filter >= len(panelFilters) {
		filter = 0
	}
	category := panelFilters[filter].Category
	for _, e := range s["events"].([]activityEvent) {
		if category == "all" || e.Category == category || (category == "error" && e.Level == "error") {
			p.Events = append(p.Events, e)
		}
	}
	return p
}

func eventSummary(e activityEvent) string {
	symbol := map[string]string{"server": "↑", "receive": "↓", "model": "◇", "app": "•"}[e.Category]
	if e.Level == "error" {
		symbol = "!"
	}
	return e.Time.Local().Format("15:04:05") + "  " + symbol + "  " + e.Title
}

func eventDetail(events []activityEvent, index int) string {
	if index < 0 || index >= len(events) {
		return "Seleziona un evento per leggerne i dettagli."
	}
	e := events[index]
	return e.Title + "\n\n" + e.Detail
}

func panelModelSelection(old, current []panelModel, selected int) int {
	if selected < 0 || selected >= len(old) {
		return -1
	}
	previous := old[selected]
	for i, m := range current {
		if m.ID == previous.ID && m.Name == previous.Name && m.Engine == previous.Engine {
			return i
		}
	}
	return -1
}

func panelEventSelection(old, current []activityEvent, selected int, sameGeneration bool) int {
	if !sameGeneration || selected < 0 || selected >= len(old) {
		return -1
	}
	for i, e := range current {
		if e.ID == old[selected].ID {
			return i
		}
	}
	return -1
}

// The selector runs under the import lock as well, so browser and native actions
// cannot open competing selectors or overwrite an import's temporary Modelfile.
func (a *application) importFromDesktop(selectPath func(context.Context) (string, bool, error)) <-chan string {
	result := make(chan string, 1)
	go func() {
		defer close(result)
		if !a.importMu.TryLock() {
			result <- "Un’importazione è già in corso. Attendi che termini."
			return
		}
		defer a.importMu.Unlock()
		path, folder, err := selectPath(a.ctx)
		if err == nil && path == "" {
			err = errors.New("Selezione annullata.")
		}
		if err == nil {
			err = a.importModelAt(a.ctx, path, folder)
		}
		message, level := "Pesi aggiunti. Il modello viene rilevato dal motore locale.", "info"
		if err == nil {
			a.refreshModels()
			a.publishModels()
		} else {
			message, level = err.Error(), "error"
		}
		a.record("app", level, "Importazione dei pesi", message)
		result <- message
	}()
	return result
}

func (a *application) importDetectedFromDesktop(id string) <-chan string {
	return a.importFromDesktop(func(context.Context) (string, bool, error) {
		a.mu.RLock()
		defer a.mu.RUnlock()
		for _, f := range a.files {
			if f.ID == id {
				return f.Path, f.Folder, nil
			}
		}
		return "", false, errors.New("Pesi non più disponibili. Selezionali con Aggiungi pesi.")
	})
}
