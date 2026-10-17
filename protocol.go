package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type message struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	Name      string     `json:"name,omitempty"`
	ToolCalls []toolCall `json:"toolCalls,omitempty"`
}
type toolCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}
type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}
type trace struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
	Turn int    `json:"turn"`
}
type task struct {
	ID            string         `json:"id"`
	Trace         *trace         `json:"trace,omitempty"`
	Model         model          `json:"model"`
	Messages      []message      `json:"messages"`
	Tools         []tool         `json:"tools"`
	Schema        map[string]any `json:"schema,omitempty"`
	Temperature   float64        `json:"temperature"`
	MaxTokens     int            `json:"maxTokens"`
	ContextTokens int            `json:"contextTokens"`
	ExpiresAt     int64          `json:"expiresAt"`
}
type turn struct {
	Content   string     `json:"content"`
	ToolCalls []toolCall `json:"toolCalls"`
}

var unauthorized = errors.New("device authorization expired")

// How often a running inference asks Jase whether the request is still wanted.
var statusEvery = 5 * time.Second

func (a *application) remote(ctx context.Context, c link, path string, input, output any) error {
	if strings.ContainsAny(path, "?#") || strings.Contains(path, "..") || (c.Protocol == webProtocol && (!webOrigin(c.Origin) || !webBrokerPath(c.Broker) || c.ExpiresAt <= time.Now().UnixMilli())) || (c.Protocol != webProtocol && !allowedOrigin(c.Origin)) {
		return errors.New("invalid Jase origin or endpoint")
	}
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	method := "POST"
	if strings.HasPrefix(path, "active") {
		method = "GET"
	}
	base := "/api/companion/"
	client := a.client
	if c.Protocol == webProtocol {
		base = c.Broker
		client = a.webHTTPClient()
		if path != "pair" && !a.webGrantActive(c) {
			return unauthorized
		}
	}
	endpoint := c.Origin + base + path
	// active's query is constructed separately, never supplied by a model.
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	// End the long poll's connection after its response. During a graceful server
	// restart an old keep-alive socket must not pin polling to the retiring process.
	if path == "poll" {
		req.Close = true
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	started := time.Now()
	if path != "poll" {
		a.debugf("→ Jase %s /api/companion/%s%s", method, path, sizeOf(input))
	} else {
		a.record("server", "info", "In attesa di richieste da Jase", "POST /api/companion/poll")
	}
	response, err := client.Do(req)
	if err != nil {
		if path != "poll" || ctx.Err() == nil {
			a.debugf("✗ Jase %s: %v", path, err)
		}
		return err
	}
	defer response.Body.Close()
	a.serverContact(c.Origin)
	if path == "poll" && response.StatusCode == 200 {
		a.record("receive", "info", "Ricezione dal server", fmt.Sprintf("/api/companion/poll · HTTP 200 · %s", time.Since(started).Round(time.Millisecond)))
	}
	if path != "poll" || response.StatusCode != 200 {
		a.debugf("← Jase %s: HTTP %d in %s", path, response.StatusCode, time.Since(started).Round(time.Millisecond))
	}
	if response.StatusCode == 401 {
		return unauthorized
	}
	if response.StatusCode != 200 {
		return fmt.Errorf("Jase endpoint HTTP %d", response.StatusCode)
	}
	if output == nil {
		_, err = io.Copy(io.Discard, io.LimitReader(response.Body, 160000))
		return err
	}
	return json.NewDecoder(io.LimitReader(response.Body, 1100000)).Decode(output)
}
func (a *application) isActive(ctx context.Context, c link, id string) (bool, error) {
	if c.Protocol == webProtocol && !a.webGrantActive(c) {
		return false, unauthorized
	}
	if c.Protocol != webProtocol && !allowedOrigin(c.Origin) {
		return false, errors.New("invalid origin")
	}
	base := "/api/companion/"
	client := a.client
	if c.Protocol == webProtocol {
		base = c.Broker
		client = a.webHTTPClient()
	}
	req, err := http.NewRequestWithContext(ctx, "GET", c.Origin+base+"active?id="+url.QueryEscape(id), nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	a.debugf("→ Jase GET /api/companion/active")
	response, err := client.Do(req)
	if err != nil {
		a.debugf("Jase ✗ controllo della richiesta: %v", err)
		return false, err
	}
	defer response.Body.Close()
	a.serverContact(c.Origin)
	a.debugf("← Jase active: HTTP %d", response.StatusCode)
	if response.StatusCode == 401 {
		return false, unauthorized
	}
	if response.StatusCode != 200 {
		return false, errors.New("request status unavailable")
	}
	var result struct {
		Active bool `json:"active"`
	}
	err = json.NewDecoder(io.LimitReader(response.Body, 1024)).Decode(&result)
	return result.Active, err
}
func (a *application) startLink(c link) {
	a.mu.Lock()
	if a.running[c.Token] {
		a.mu.Unlock()
		return
	}
	a.running[c.Token] = true
	a.mu.Unlock()
	go func() {
		defer func() { a.mu.Lock(); delete(a.running, c.Token); a.mu.Unlock() }()
		for a.ctx.Err() == nil {
			if c.Protocol == webProtocol && !a.webGrantActive(c) {
				a.forget(c.Token)
				return
			}
			var response struct {
				Task *task `json:"task"`
			}
			err := a.remote(a.ctx, c, "poll", nil, &response)
			if errors.Is(err, unauthorized) {
				a.forget(c.Token)
				return
			}
			if err != nil {
				select {
				case <-time.After(4 * time.Second):
				case <-a.ctx.Done():
					return
				}
				continue
			}
			if response.Task != nil {
				a.handleTask(c, *response.Task)
			}
		}
	}()
}
func (a *application) forget(token string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	kept := a.settings.Links[:0]
	for _, c := range a.settings.Links {
		if c.Token != token {
			kept = append(kept, c)
		}
	}
	a.settings.Links = kept
	_ = a.saveLocked()
}
func (a *application) handleTask(c link, t task) {
	if c.Protocol == webProtocol && (!a.webGrantActive(c) || !validWebTask(c, t)) {
		a.record("model", "error", "Turno rifiutato", "Il server ha richiesto un modello o limiti diversi dal consenso.")
		return
	}
	started, label, outcome := time.Now(), queryLabel(t), "annullata"
	log.Printf("===== Start query · %s =====", label)
	a.logTask(t)
	defer func() {
		log.Printf("===== End reply · %s · %s · %.1fs =====", label, outcome, time.Since(started).Seconds())
		level := "info"
		if strings.Contains(outcome, "errore") || strings.Contains(outcome, "NON consegnata") {
			level = "error"
		}
		a.record("model", level, "Richiesta terminata", fmt.Sprintf("%s · %s · %.1fs", label, outcome, time.Since(started).Seconds()))
	}()
	deadline := time.UnixMilli(t.ExpiresAt)
	if deadline.After(time.Now().Add(10 * time.Minute)) {
		deadline = time.Now().Add(10 * time.Minute)
	}
	ctx, cancel := context.WithDeadline(a.ctx, deadline)
	defer cancel()
	if c.Protocol == webProtocol {
		a.mu.Lock()
		if a.webRuns == nil {
			a.webRuns = map[string]context.CancelFunc{}
		}
		a.webRuns[c.Token] = cancel
		a.mu.Unlock()
		defer func() { a.mu.Lock(); delete(a.webRuns, c.Token); a.mu.Unlock() }()
	}
	// Stop local inference when the browser cancels, a server task expires, or Jase
	// becomes unreachable. A request can never turn into detached background work.
	// One failed check is a network blip, not a cancellation: stop only on Jase's answer
	// "not active", a revoked device, or three failed checks in a row (15 s; Jase itself
	// gives up on a silent computer after 30 s).
	go func() {
		ticker := time.NewTicker(statusEvery)
		defer ticker.Stop()
		failures := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				active, err := a.isActive(ctx, c, t.ID)
				if err == nil && !active || errors.Is(err, unauthorized) {
					cancel()
					return
				}
				if err != nil {
					if failures++; failures >= 3 {
						cancel()
						return
					}
				} else {
					failures = 0
				}
			}
		}
	}()
	var result any
	select {
	case a.inference <- struct{}{}:
	case <-ctx.Done():
		return
	}
	if ctx.Err() != nil || c.Protocol == webProtocol && !a.webGrantActive(c) {
		<-a.inference
		return
	}
	answer, err := a.infer(ctx, t)
	<-a.inference
	if ctx.Err() != nil || c.Protocol == webProtocol && !a.webGrantActive(c) {
		return
	}
	if err != nil {
		outcome = "errore del modello: " + err.Error()
		result = map[string]string{"error": "local_model_error"}
	} else {
		outcome = "risposta inviata"
		result = answer
	}
	if err := a.remote(ctx, c, "result", map[string]any{"id": t.ID, "result": result}, nil); err != nil {
		outcome = "risposta NON consegnata a Jase: " + err.Error()
	}
}
func (a *application) publishModels() {
	a.mu.RLock()
	connections := append([]link{}, a.settings.Links...)
	models := append([]model{}, a.models...)
	a.mu.RUnlock()
	for _, c := range connections {
		if c.Protocol == webProtocol {
			continue
		}
		ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
		err := a.remote(ctx, c, "models", map[string]any{"models": models}, nil)
		cancel()
		if errors.Is(err, unauthorized) {
			a.forget(c.Token)
		}
	}
}
func (a *application) watchModels() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			a.refreshModels()
			a.publishModels()
		}
	}
}
