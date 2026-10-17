package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type webManifest struct {
	Name         string   `json:"name"`
	Protocols    []string `json:"protocols"`
	Broker       string   `json:"broker"`
	Requirements struct {
		Capabilities  []string `json:"capabilities"`
		ContextTokens int      `json:"contextTokens"`
	} `json:"requirements"`
}
type webDiscovery struct {
	ID           string      `json:"id"`
	Origin       string      `json:"origin"`
	Name         string      `json:"name"`
	State        string      `json:"state"`
	ExpiresAt    int64       `json:"expiresAt"`
	Manifest     webManifest `json:"-"`
	Ticket       string      `json:"-"`
	Shown        bool        `json:"-"`
	ConnectionID string      `json:"connectionId,omitempty"`
}
type webManager struct {
	mu       sync.Mutex
	requests map[string]*webDiscovery
	checked  map[string]time.Time
	minute   time.Time
	count    int
	checking int
}
type webApproval struct {
	ID     string  `json:"id"`
	Origin string  `json:"origin"`
	Name   string  `json:"name"`
	Models []model `json:"models"`
}

func webSecret(bytes int) string {
	raw := make([]byte, bytes)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	return hex.EncodeToString(raw)
}
func webHash(value string) string {
	raw := sha256.Sum256([]byte(value))
	return hex.EncodeToString(raw[:])
}
func (a *application) webManager() *webManager {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.web == nil {
		a.web = &webManager{requests: map[string]*webDiscovery{}, checked: map[string]time.Time{}}
	}
	return a.web
}
func webFail(w http.ResponseWriter, status int, code, message string) {
	respond(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func (a *application) serveWeb(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if len(r.Header.Values("Origin")) != 1 || !webOrigin(origin) {
		webFail(w, 403, "origin_mismatch", "Origine del sito non valida.")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/tunnel/v1/")
	if path != "discover" && path != "discovery" && path != "pair" {
		webFail(w, 404, "not_found", "Risorsa non trovata.")
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Vary", "Origin")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	if r.Method == "OPTIONS" {
		w.WriteHeader(204)
		return
	}
	if path == "discover" && r.Method == "POST" {
		var input struct {
			Protocol string `json:"protocol"`
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") || decode(w, r, &input, 4096) != nil || input.Protocol != webProtocol {
			webFail(w, 400, "invalid_request", "Richiesta non valida.")
			return
		}
		m := a.webManager()
		m.mu.Lock()
		now := time.Now()
		for key, value := range m.requests {
			if value.ExpiresAt <= now.UnixMilli() {
				delete(m.requests, key)
			}
		}
		for key, value := range m.checked {
			if now.Sub(value) > time.Minute {
				delete(m.checked, key)
			}
		}
		if now.Sub(m.minute) >= time.Minute {
			m.minute = now
			m.count = 0
		}
		if m.count >= 20 || m.checking >= 4 || len(m.requests) >= 100 || !m.checked[origin].IsZero() {
			m.mu.Unlock()
			w.Header().Set("Retry-After", "60")
			webFail(w, 429, "rate_limited", "Attendi un minuto prima di rilevare di nuovo questo sito.")
			return
		}
		receipt := webSecret(32)
		d := &webDiscovery{ID: webSecret(16), Origin: origin, State: "verifying", ExpiresAt: now.Add(5 * time.Minute).UnixMilli()}
		m.requests[webHash(receipt)] = d
		m.checked[origin] = now
		m.count++
		m.checking++
		m.mu.Unlock()
		go a.verifyWebSite(d.ID)
		respond(w, 202, map[string]any{"protocol": webProtocol, "discoveryId": d.ID, "receipt": receipt, "expiresAt": d.ExpiresAt, "state": "verifying"})
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	m := a.webManager()
	m.mu.Lock()
	d := m.requests[webHash(token)]
	if d == nil || len(token) != 64 || d.ExpiresAt <= time.Now().UnixMilli() {
		m.mu.Unlock()
		webFail(w, 410, "discovery_expired", "Rilevamento scaduto. Riprova dalla pagina.")
		return
	}
	if d.Origin != origin {
		m.mu.Unlock()
		webFail(w, 403, "origin_mismatch", "Ricevuta non valida per questo sito.")
		return
	}
	if path == "discovery" && r.Method == "GET" {
		out := map[string]any{"protocol": webProtocol, "discoveryId": d.ID, "state": d.State, "expiresAt": d.ExpiresAt}
		if d.ConnectionID != "" {
			out["connectionId"] = d.ConnectionID
		}
		m.mu.Unlock()
		respond(w, 200, out)
		return
	}
	if path == "pair" && r.Method == "POST" {
		if d.State != "compatible" {
			m.mu.Unlock()
			webFail(w, 409, "pairing_in_progress", "Associazione non disponibile. Riprova il rilevamento.")
			return
		}
		var input struct {
			Protocol string `json:"protocol"`
			Ticket   string `json:"ticket"`
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") || decode(w, r, &input, 4096) != nil || input.Protocol != webProtocol || len(input.Ticket) != 64 {
			m.mu.Unlock()
			webFail(w, 400, "invalid_request", "Associazione non valida.")
			return
		}
		if _, err := hex.DecodeString(input.Ticket); err != nil {
			m.mu.Unlock()
			webFail(w, 400, "invalid_request", "Ticket non valido.")
			return
		}
		d.State = "awaiting_approval"
		d.Ticket = input.Ticket
		d.ExpiresAt = time.Now().Add(120 * time.Second).UnixMilli()
		id := d.ID
		m.mu.Unlock()
		a.record("app", "info", "Consenso richiesto dal sito", origin+" · Scegli il modello nel pannello Jase Tunnel.")
		if a.nativeUI {
			showDesktopUI()
		}
		respond(w, 202, map[string]any{"protocol": webProtocol, "discoveryId": id, "state": "awaiting_approval"})
		return
	}
	m.mu.Unlock()
	webFail(w, 405, "method_not_allowed", "Metodo non consentito.")
}

func (a *application) verifyWebSite(id string) {
	m := a.webManager()
	m.mu.Lock()
	var origin string
	for _, d := range m.requests {
		if d.ID == id {
			origin = d.Origin
			break
		}
	}
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", origin+"/.well-known/jase-tunnel.json", nil)
	var manifest webManifest
	if err == nil {
		var res *http.Response
		res, err = a.webHTTPClient().Do(req)
		if err == nil {
			defer res.Body.Close()
			if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") {
				err = errors.New("manifest unavailable")
			} else {
				var data []byte
				data, err = io.ReadAll(io.LimitReader(res.Body, 16385))
				if err == nil && len(data) > 16384 {
					err = errors.New("manifest too large")
				}
				if err == nil {
					err = json.Unmarshal(data, &manifest)
				}
			}
		}
	}
	if err == nil {
		err = validateWebManifest(manifest)
	}
	m.mu.Lock()
	m.checking--
	for _, d := range m.requests {
		if d.ID == id {
			if err != nil {
				d.State = "incompatible"
			} else {
				d.State = "compatible"
				d.Name = manifest.Name
				d.Manifest = manifest
			}
			break
		}
	}
	m.mu.Unlock()
	if err == nil {
		a.record("app", "info", "Sito compatibile con Jase Tunnel", origin+" · Torna alla pagina e premi Collega.")
	}
}
func validateWebManifest(m webManifest) error {
	if len(m.Name) < 1 || len([]rune(m.Name)) > 80 || strings.ContainsAny(m.Name, "\x00\r\n") || !webBrokerPath(m.Broker) || m.Requirements.ContextTokens < 2048 || m.Requirements.ContextTokens > 262144 {
		return errors.New("invalid manifest")
	}
	supported := false
	for _, p := range m.Protocols {
		if p == webProtocol {
			supported = true
		}
	}
	if !supported {
		return errors.New("unsupported protocol")
	}
	chat := false
	for _, c := range m.Requirements.Capabilities {
		if c == "chat" {
			chat = true
		} else if c != "tool-calls" {
			return errors.New("unsupported capability")
		}
	}
	if !chat {
		return errors.New("chat required")
	}
	return nil
}
func (a *application) claimWebApproval() *webApproval {
	m := a.webManager()
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.requests {
		if d.State != "awaiting_approval" || d.Shown || d.ExpiresAt <= time.Now().UnixMilli() {
			continue
		}
		a.mu.RLock()
		models := []model{}
		for _, model := range a.models {
			if model.Tools && model.ContextTokens >= d.Manifest.Requirements.ContextTokens {
				models = append(models, model)
			}
		}
		a.mu.RUnlock()
		d.Shown = true
		return &webApproval{ID: d.ID, Origin: d.Origin, Name: d.Name, Models: models}
	}
	return nil
}
func webConsentText(p *webApproval) string {
	return p.Name + "\n" + p.Origin + "\n\nIl sito invierà messaggi e dati al modello scelto. Risposte e richieste di strumenti torneranno al server del sito. I pesi restano sul computer. Il collegamento dura al massimo 30 minuti.\n\nConsenti soltanto se riconosci il sito."
}
func (a *application) answerWebConsent(id, key string) {
	m := a.webManager()
	m.mu.Lock()
	var current *webDiscovery
	for _, d := range m.requests {
		if d.ID == id && d.State == "awaiting_approval" && d.Shown && d.ExpiresAt > time.Now().UnixMilli() {
			current = d
			break
		}
	}
	if current == nil {
		m.mu.Unlock()
		return
	}
	if key == "" {
		current.State = "rejected"
		current.Ticket = ""
		m.mu.Unlock()
		return
	}
	a.mu.RLock()
	var chosen model
	for _, model := range a.models {
		if model.Key == key && model.Tools && model.ContextTokens >= current.Manifest.Requirements.ContextTokens {
			chosen = model
			break
		}
	}
	a.mu.RUnlock()
	if chosen.Key == "" {
		current.State = "failed"
		current.Ticket = ""
		m.mu.Unlock()
		return
	}
	ticket := current.Ticket
	current.Ticket = ""
	current.State = "connecting"
	grant := link{Origin: current.Origin, Protocol: webProtocol, Broker: current.Manifest.Broker, Model: chosen, ExpiresAt: time.Now().Add(30 * time.Minute).UnixMilli()}
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
	defer cancel()
	var result struct {
		Protocol     string `json:"protocol"`
		Token        string `json:"token"`
		ConnectionID string `json:"connectionId"`
		ExpiresAt    int64  `json:"expiresAt"`
	}
	err := a.remote(ctx, grant, "pair", map[string]any{"protocol": webProtocol, "ticket": ticket, "label": "Il mio computer", "models": []model{chosen}}, &result)
	if err == nil && (result.Protocol != webProtocol || len(result.Token) != 64 || len(result.ConnectionID) != 32 || result.ExpiresAt <= time.Now().UnixMilli()) {
		err = errors.New("invalid grant")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if current.State != "connecting" || current.ExpiresAt <= time.Now().UnixMilli() {
		return
	}
	if err != nil {
		current.State = "failed"
		return
	}
	grant.Token = result.Token
	grant.ConnectionID = result.ConnectionID
	if result.ExpiresAt < grant.ExpiresAt {
		grant.ExpiresAt = result.ExpiresAt
	}
	a.mu.Lock()
	kept := []link{}
	for _, c := range a.settings.Links {
		if c.Protocol != webProtocol || c.ExpiresAt > time.Now().UnixMilli() {
			kept = append(kept, c)
		}
	}
	if len(kept) >= 20 {
		a.mu.Unlock()
		current.State = "failed"
		return
	}
	a.settings.Links = append(kept, grant)
	err = a.saveLocked()
	a.mu.Unlock()
	if err != nil {
		current.State = "failed"
		return
	}
	current.State = "connected"
	current.ConnectionID = grant.ConnectionID
	a.record("app", "info", "Sito collegato al modello scelto", grant.Origin+" · "+chosen.Name)
	a.startLink(grant)
}
func (a *application) revokeWebLinks() {
	a.mu.Lock()
	kept := []link{}
	for _, c := range a.settings.Links {
		if c.Protocol == webProtocol {
			if cancel := a.webRuns[c.Token]; cancel != nil {
				cancel()
			}
		}
		if c.Protocol != webProtocol {
			kept = append(kept, c)
		}
	}
	a.settings.Links = kept
	_ = a.saveLocked()
	a.mu.Unlock()
	m := a.webManager()
	m.mu.Lock()
	for _, d := range m.requests {
		d.State = "rejected"
		d.Ticket = ""
	}
	m.mu.Unlock()
	a.record("app", "info", "Siti scollegati", "I collegamenti del protocollo web sono stati revocati.")
}
func (a *application) webGrantActive(c link) bool {
	if c.Protocol != webProtocol {
		return true
	}
	if !validWebLink(c) {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, active := range a.settings.Links {
		if active.Protocol == webProtocol && active.Token == c.Token && active.ExpiresAt > time.Now().UnixMilli() {
			return true
		}
	}
	return false
}
func validWebTask(c link, t task) bool {
	if t.Model.Key != c.Model.Key || t.Model.Engine != c.Model.Engine || t.ContextTokens < 2048 || t.ContextTokens > c.Model.ContextTokens || t.MaxTokens < 1 || t.MaxTokens > 16384 || len(t.Messages) > 300 || len(t.Tools) > 100 || len(t.Schema) > 0 || t.ExpiresAt > c.ExpiresAt || t.ExpiresAt <= time.Now().UnixMilli() {
		return false
	}
	return true
}
