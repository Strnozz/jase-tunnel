package main

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
)

// Local diagnostics. Without --debug the log holds only the Start query / End reply markers
// (no text of the conversation); with --debug, everything the model reads and writes.

func (a *application) debugf(format string, args ...any) { a.debugFor("", format, args...) }

// debugFor journals a diagnostic line in a site's console (and the log with --debug).
func (a *application) debugFor(site, format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	category, level := "model", "info"
	if strings.HasPrefix(text, "→ sito") {
		category = "server"
	} else if strings.HasPrefix(text, "← sito") {
		category = "receive"
	}
	if strings.Contains(text, "✗") || strings.Contains(text, "HTTP 4") || strings.Contains(text, "HTTP 5") {
		level = "error"
	}
	a.recordFor(site, category, level, text, "")
	if a.debug.Load() {
		log.Printf(format, args...)
	}
}

// "richiesta 9f8e7d6c": the first characters of the turn's identifier, never its text.
func queryLabel(t task) string {
	id := t.ID
	if len(id) > 8 {
		id = id[:8]
	}
	return "richiesta " + id
}

func clip(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + fmt.Sprintf(" … [+%d caratteri]", len(runes)-limit)
}

func sizeOf(input any) string {
	if input == nil {
		return ""
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return ""
	}
	return fmt.Sprintf(" (%d byte)", len(encoded))
}

func onOff(on, available bool) string {
	switch {
	case !available:
		return "non previsto dal modello"
	case on:
		return "attivo"
	default:
		return "spento (--think per attivarlo)"
	}
}

func (a *application) logTask(site string, t task) {
	a.recordFor(site, "model", "info", "Richiesta ricevuta", fmt.Sprintf("%s · %s · %d messaggi · %d strumenti", queryLabel(t), t.Model.Name, len(t.Messages), len(t.Tools)))
	if !a.debug.Load() {
		return
	}
	tools := []string{}
	for _, definition := range t.Tools {
		tools = append(tools, definition.Name)
	}
	log.Printf("Modello %s · %d messaggi · temperatura %.2f · max %d token · contesto %d · strumenti: %s%s",
		t.Model.Key, len(t.Messages), t.Temperature, t.MaxTokens, t.ContextTokens, strings.Join(tools, ", "), map[bool]string{true: " · risposta JSON con schema", false: ""}[t.Schema != nil])
	for i, m := range t.Messages {
		// The last message is what this turn answers: in full (bounded); earlier ones shortened.
		limit := 400
		if i == len(t.Messages)-1 {
			limit = 8000
		}
		name := ""
		if m.Name != "" {
			name = " " + m.Name
		}
		log.Printf("  [%d] %s%s: %s", i+1, m.Role, name, clip(m.Content, limit))
		a.recordDetailed(site, fmt.Sprintf("Messaggio %d · %s%s", i+1, m.Role, name), clip(m.Content, limit))
		for _, call := range m.ToolCalls {
			arguments, _ := json.Marshal(call.Arguments)
			log.Printf("      ↳ chiamata %s %s", call.Name, clip(string(arguments), 400))
			a.recordDetailed(site, "Strumento: "+call.Name, clip(string(arguments), 400))
		}
	}
}

func (a *application) logThinking(site string, raw map[string]any) {
	if !a.debug.Load() {
		return
	}
	parts := []string{}
	for _, key := range []string{"thinking", "reasoning_content", "reasoning"} {
		if value, ok := raw[key].(string); ok && strings.TrimSpace(value) != "" {
			parts = append(parts, strings.TrimSpace(value))
		}
	}
	if content, ok := raw["content"].(string); ok {
		for _, block := range thinking.FindAllString(content, -1) {
			parts = append(parts, strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(block, "<think>"), "</think>")))
		}
	}
	if len(parts) == 0 {
		return
	}
	log.Printf("--- thinking ---\n%s\n--- fine thinking ---", strings.Join(parts, "\n"))
	a.recordDetailed(site, "Ragionamento del modello", strings.Join(parts, "\n"))
}

func (a *application) logAnswer(site string, result turn) {
	if !a.debug.Load() {
		return
	}
	if result.Content != "" {
		log.Printf("--- risposta ---\n%s\n--- fine risposta ---", result.Content)
		a.recordDetailed(site, "Risposta del modello", result.Content)
	}
	for _, call := range result.ToolCalls {
		arguments, _ := json.Marshal(call.Arguments)
		log.Printf("Strumento richiesto al sito: %s %s", call.Name, string(arguments))
		a.recordDetailed(site, "Strumento richiesto al sito: "+call.Name, string(arguments))
	}
	if result.Content == "" && len(result.ToolCalls) == 0 {
		log.Print("Risposta vuota del modello")
	}
}
