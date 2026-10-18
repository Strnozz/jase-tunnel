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
type task struct {
	ID       string    `json:"id"`
	Model    model     `json:"model"`
	Messages []message `json:"messages"`
	Tools    []tool    `json:"tools"`
	// Structured output is not part of jase-tunnel-v1: a task that asks for it is
	// refused by validWebTask instead of being run without the schema.
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

// The site a model turn belongs to, so inference events reach that site's console.
type siteKey struct{}

func siteOf(ctx context.Context) string {
	site, _ := ctx.Value(siteKey{}).(string)
	return site
}

// How often a running inference asks the site whether the request is still wanted.
var statusEvery = 5 * time.Second

// remote calls the broker the site declared in its manifest, for an unexpired grant.
func (a *application) remote(ctx context.Context, c link, path string, input, output any) error {
	if strings.ContainsAny(path, "?#") || strings.Contains(path, "..") || !webOrigin(c.Origin) || !webBrokerPath(c.Broker) || c.ExpiresAt <= time.Now().UnixMilli() {
		return errors.New("invalid site origin or endpoint")
	}
	if path != "pair" && !a.webGrantActive(c) {
		return unauthorized
	}
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	endpoint := c.Origin + c.Broker + path
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, body)
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
		a.debugFor(c.Origin, "→ sito POST %s%s", endpoint, sizeOf(input))
	} else {
		a.recordFor(c.Origin, "server", "info", "In attesa di richieste dal sito", c.Origin)
	}
	response, err := a.webHTTPClient().Do(req)
	if err != nil {
		if path != "poll" || ctx.Err() == nil {
			a.debugFor(c.Origin, "✗ sito %s: %v", path, err)
		}
		return err
	}
	defer response.Body.Close()
	a.serverContact(c.Origin)
	if path == "poll" && response.StatusCode == 200 {
		a.recordFor(c.Origin, "receive", "info", "Ricezione dal sito", fmt.Sprintf("%s · HTTP 200 · %s", c.Origin, time.Since(started).Round(time.Millisecond)))
	}
	if path != "poll" || response.StatusCode != 200 {
		a.debugFor(c.Origin, "← sito %s: HTTP %d in %s", path, response.StatusCode, time.Since(started).Round(time.Millisecond))
	}
	if response.StatusCode == 401 {
		return unauthorized
	}
	if response.StatusCode != 200 {
		return fmt.Errorf("site endpoint HTTP %d", response.StatusCode)
	}
	if output == nil {
		_, err = io.Copy(io.Discard, io.LimitReader(response.Body, 160000))
		return err
	}
	return json.NewDecoder(io.LimitReader(response.Body, 1100000)).Decode(output)
}
func (a *application) isActive(ctx context.Context, c link, id string) (bool, error) {
	if !a.webGrantActive(c) {
		return false, unauthorized
	}
	req, err := http.NewRequestWithContext(ctx, "GET", c.Origin+c.Broker+"active?id="+url.QueryEscape(id), nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	a.debugFor(c.Origin, "→ sito GET %s%sactive", c.Origin, c.Broker)
	response, err := a.webHTTPClient().Do(req)
	if err != nil {
		a.debugFor(c.Origin, "✗ sito: controllo della richiesta: %v", err)
		return false, err
	}
	defer response.Body.Close()
	a.serverContact(c.Origin)
	a.debugFor(c.Origin, "← sito active: HTTP %d", response.StatusCode)
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
			if !a.webGrantActive(c) {
				if c.ExpiresAt <= time.Now().UnixMilli() {
					a.recordFor(c.Origin, "app", "info", "Tunnel scaduto", "Il collegamento dura al massimo 30 minuti: il sito può chiederne uno nuovo.")
				}
				a.forget(c.Token)
				return
			}
			var response struct {
				Task *task `json:"task"`
			}
			err := a.remote(a.ctx, c, "poll", nil, &response)
			if errors.Is(err, unauthorized) {
				if a.webGrantActive(c) {
					a.recordFor(c.Origin, "app", "info", "Tunnel chiuso dal sito", "Il sito ha revocato il collegamento.")
				}
				a.forget(c.Token)
				return
			}
			// «Scollega siti» may have removed the grant while the long poll was waiting.
			if !a.webGrantActive(c) {
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
	if !a.webGrantActive(c) || !validWebTask(c, t) {
		a.recordFor(c.Origin, "model", "error", "Turno rifiutato", "Il server ha richiesto un modello o limiti diversi dal consenso.")
		return
	}
	started, label, outcome := time.Now(), queryLabel(t), "annullata"
	log.Printf("===== Start query · %s =====", label)
	a.logTask(c.Origin, t)
	defer func() {
		log.Printf("===== End reply · %s · %s · %.1fs =====", label, outcome, time.Since(started).Seconds())
		level := "info"
		if strings.Contains(outcome, "errore") || strings.Contains(outcome, "NON consegnata") {
			level = "error"
		}
		a.recordFor(c.Origin, "model", level, "Richiesta terminata", fmt.Sprintf("%s · %s · %.1fs", label, outcome, time.Since(started).Seconds()))
	}()
	deadline := time.UnixMilli(t.ExpiresAt)
	if deadline.After(time.Now().Add(10 * time.Minute)) {
		deadline = time.Now().Add(10 * time.Minute)
	}
	ctx, cancel := context.WithDeadline(context.WithValue(a.ctx, siteKey{}, c.Origin), deadline)
	defer cancel()
	a.mu.Lock()
	if a.runs == nil {
		a.runs = map[string]context.CancelFunc{}
	}
	a.runs[c.Token] = cancel
	a.mu.Unlock()
	defer func() { a.mu.Lock(); delete(a.runs, c.Token); a.mu.Unlock() }()
	// Stop local inference when the browser cancels, a server task expires, or the site
	// becomes unreachable. A request can never turn into detached background work.
	// One failed check is a network blip, not a cancellation: stop only on the site's
	// answer "not active", a revoked grant, or three failed checks in a row (15 s; the
	// module's broker gives up on a silent computer after 30 s).
	every := statusEvery
	go func() {
		ticker := time.NewTicker(every)
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
	if ctx.Err() != nil || !a.webGrantActive(c) {
		<-a.inference
		return
	}
	answer, err := a.infer(ctx, t)
	<-a.inference
	if ctx.Err() != nil || !a.webGrantActive(c) {
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
		outcome = "risposta NON consegnata al sito: " + err.Error()
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
		}
	}
}
