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
	if strings.HasPrefix(text, "→ website") {
		category = "server"
	} else if strings.HasPrefix(text, "← website") {
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

// "request 9f8e7d6c": the first characters of the turn's identifier, never its text.
func queryLabel(t task) string {
	id := t.ID
	if len(id) > 8 {
		id = id[:8]
	}
	return "request " + id
}

func clip(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + fmt.Sprintf(" … [+%d characters]", len(runes)-limit)
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
		return "not supported by the model"
	case on:
		return "active"
	default:
		return "off (use --think to enable)"
	}
}

func (a *application) logTask(site string, t task) {
	a.recordFor(site, "model", "info", "Request received", fmt.Sprintf("%s · %s · %d messages · %d tools", queryLabel(t), t.Model.Name, len(t.Messages), len(t.Tools)))
	if !a.debug.Load() {
		return
	}
	tools := []string{}
	for _, definition := range t.Tools {
		tools = append(tools, definition.Name)
	}
	log.Printf("Model %s · %d messages · temperature %.2f · max %d token · context %d · tools: %s%s",
		t.Model.Key, len(t.Messages), t.Temperature, t.MaxTokens, t.ContextTokens, strings.Join(tools, ", "), map[bool]string{true: " · JSON response with schema", false: ""}[t.Schema != nil])
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
		a.recordDetailed(site, fmt.Sprintf("Message %d · %s%s", i+1, m.Role, name), clip(m.Content, limit))
		for _, call := range m.ToolCalls {
			arguments, _ := json.Marshal(call.Arguments)
			log.Printf("      ↳ call %s %s", call.Name, clip(string(arguments), 400))
			a.recordDetailed(site, "Tool: "+call.Name, clip(string(arguments), 400))
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
	log.Printf("--- thinking ---\n%s\n--- end thinking ---", strings.Join(parts, "\n"))
	a.recordDetailed(site, "Model reasoning", strings.Join(parts, "\n"))
}

func (a *application) logAnswer(site string, result turn) {
	if !a.debug.Load() {
		return
	}
	if result.Content != "" {
		log.Printf("--- response ---\n%s\n--- end response ---", result.Content)
		a.recordDetailed(site, "Model response", result.Content)
	}
	for _, call := range result.ToolCalls {
		arguments, _ := json.Marshal(call.Arguments)
		log.Printf("Tool requested from website: %s %s", call.Name, string(arguments))
		a.recordDetailed(site, "Tool requested from website: "+call.Name, string(arguments))
	}
	if result.Content == "" && len(result.ToolCalls) == 0 {
		log.Print("Empty model response")
	}
}
