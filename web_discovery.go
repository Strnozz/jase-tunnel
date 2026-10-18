package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
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
	Announced    bool        `json:"-"`
	ConnectionID string      `json:"connectionId,omitempty"`
}

// Limits on what pages can ask of the app. A page cannot choose the Origin its browser
// sends, so per-origin limits keep one site (open in many tabs, or hostile) from using
// up the budget of every other site.
const (
	discoverPerOrigin = 20  // discoveries per site per minute
	discoverGlobal    = 120 // discoveries per minute, all sites together
	pendingPerOrigin  = 30  // unexpired receipts per site
	pendingGlobal     = 300 // unexpired receipts, all sites together
	// After «Decline» the same site cannot reopen the consent dialog for a while.
	consentCooldown = 2 * time.Minute
	// A site brings the window to the front at most this often.
	focusEvery = 30 * time.Second
)

type rateWindow struct {
	start time.Time
	count int
}
type webManager struct {
	mu       sync.Mutex
	requests map[string]*webDiscovery
	checked  map[string]time.Time
	minute   time.Time
	count    int
	checking int
	// Discoveries per site in the current minute.
	perOrigin map[string]*rateWindow
	// Last «Decline» and last window focus, per site.
	rejected map[string]time.Time
	focused  map[string]time.Time
	// Manifests verified in the last five minutes: another page of the same site is
	// recognised without fetching the manifest again.
	verified map[string]verifiedSite
	// Notifications already shown: per site (one a day) and overall (three in ten minutes).
	notified map[string]time.Time
	notices  []time.Time
}

// Site is the verified address shown first; Name comes from the site's own manifest.
type verifiedSite struct {
	manifest webManifest
	at       time.Time
}

type webApproval struct {
	ID     string  `json:"id"`
	Origin string  `json:"origin"`
	Site   string  `json:"site"`
	Name   string  `json:"name"`
	Text   string  `json:"text"`
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
		a.web = &webManager{requests: map[string]*webDiscovery{}, checked: map[string]time.Time{}, perOrigin: map[string]*rateWindow{}, rejected: map[string]time.Time{}, focused: map[string]time.Time{}}
	}
	return a.web
}
func webFail(w http.ResponseWriter, status int, code, message string) {
	respond(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func (a *application) serveWeb(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if len(r.Header.Values("Origin")) != 1 || !webOrigin(origin) {
		webFail(w, 403, "origin_mismatch", "Invalid website origin.")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/tunnel/v1/")
	if path != "discover" && path != "discovery" && path != "pair" {
		webFail(w, 404, "not_found", "Resource not found.")
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
		// announce: the page contacted the tunnel on its own while the person browses, so a
		// compatible site is worth a notification. Without it the person already pressed
		// the page's button and is about to see the consent dialog.
		var input struct {
			Protocol string `json:"protocol"`
			Announce bool   `json:"announce,omitempty"`
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") || decodeWeb(w, r, &input, 4096) != nil || input.Protocol != webProtocol {
			webFail(w, 400, "invalid_request", "Invalid request.")
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
		for key, value := range m.verified {
			if now.Sub(value.at) > 5*time.Minute {
				delete(m.verified, key)
			}
		}
		for key, value := range m.perOrigin {
			if now.Sub(value.start) >= time.Minute {
				delete(m.perOrigin, key)
			}
		}
		window := m.perOrigin[origin]
		if window == nil {
			window = &rateWindow{start: now}
			m.perOrigin[origin] = window
		}
		outstanding := 0
		for _, value := range m.requests {
			if value.Origin == origin {
				outstanding++
			}
		}
		// Fetching a manifest is limited per site: a reload or another page of a site
		// verified moments ago is answered from the verification just made.
		cached, recent := m.verified[origin]
		if m.count >= discoverGlobal || len(m.requests) >= pendingGlobal || window.count >= discoverPerOrigin || outstanding >= pendingPerOrigin || !recent && (m.checking >= 4 || !m.checked[origin].IsZero()) {
			m.mu.Unlock()
			w.Header().Set("Retry-After", "60")
			webFail(w, 429, "rate_limited", "Wait a minute before detecting this website again.")
			return
		}
		receipt := webSecret(32)
		d := &webDiscovery{ID: webSecret(16), Origin: origin, State: "verifying", Announced: input.Announce, ExpiresAt: now.Add(5 * time.Minute).UnixMilli()}
		if recent {
			d.State, d.Name, d.Manifest = "compatible", cached.manifest.Name, cached.manifest
		}
		m.requests[webHash(receipt)] = d
		state := d.State
		m.count++
		window.count++
		if !recent {
			m.checked[origin] = now
			m.checking++
		}
		m.mu.Unlock()
		log.Printf("Discovery from %s (announce %v, verified earlier %v)", origin, input.Announce, recent)
		a.recordFor(origin, "receive", "info", "The page contacted Jase Tunnel", map[bool]string{true: "Automatic announcement while browsing.", false: "The page’s Connect button."}[input.Announce])
		if !recent {
			go a.verifyWebSite(d.ID)
		} else if input.Announce {
			go a.announceCompatible(d.ID, origin, cached.manifest.Name)
		}
		respond(w, 202, map[string]any{"protocol": webProtocol, "discoveryId": d.ID, "receipt": receipt, "expiresAt": d.ExpiresAt, "state": state})
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
		webFail(w, 403, "origin_mismatch", "Invalid receipt for this website.")
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
		if d.State != "compatible" && d.State != "requested" {
			m.mu.Unlock()
			webFail(w, 409, "pairing_in_progress", "Pairing is unavailable. Try detecting the website again.")
			return
		}
		now := time.Now()
		// One consent dialog per site at a time, and none for a while after «Decline»:
		// a page cannot keep reopening the dialog.
		if at := m.rejected[origin]; !at.IsZero() && now.Sub(at) < consentCooldown {
			m.mu.Unlock()
			w.Header().Set("Retry-After", fmt.Sprint(int((consentCooldown-now.Sub(at)).Seconds())+1))
			webFail(w, 429, "consent_cooldown", "You just declined this website. Try again in a few minutes.")
			return
		}
		for _, other := range m.requests {
			if other != d && other.Origin == origin && (other.State == "awaiting_approval" || other.State == "connecting") && other.ExpiresAt > now.UnixMilli() {
				m.mu.Unlock()
				webFail(w, 409, "pairing_in_progress", "A consent request for this website is already open.")
				return
			}
		}
		var input struct {
			Protocol string `json:"protocol"`
			Ticket   string `json:"ticket"`
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") || decodeWeb(w, r, &input, 4096) != nil || input.Protocol != webProtocol || len(input.Ticket) != 64 {
			m.mu.Unlock()
			webFail(w, 400, "invalid_request", "Invalid pairing request.")
			return
		}
		if _, err := hex.DecodeString(input.Ticket); err != nil {
			m.mu.Unlock()
			webFail(w, 400, "invalid_request", "Invalid ticket.")
			return
		}
		d.State = "awaiting_approval"
		d.Ticket = input.Ticket
		d.ExpiresAt = now.Add(120 * time.Second).UnixMilli()
		id := d.ID
		focus := now.Sub(m.focused[origin]) >= focusEvery
		if focus {
			m.focused[origin] = now
		}
		for key, value := range m.focused {
			if now.Sub(value) >= focusEvery {
				delete(m.focused, key)
			}
		}
		m.mu.Unlock()
		a.recordFor(origin, "app", "info", "Consent requested", "The website is requesting a tunnel: choose a model in the Jase Tunnel panel.")
		if a.nativeUI && focus {
			showDesktopUI()
		}
		respond(w, 202, map[string]any{"protocol": webProtocol, "discoveryId": id, "state": "awaiting_approval"})
		return
	}
	m.mu.Unlock()
	webFail(w, 405, "method_not_allowed", "Method not allowed.")
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
	announced := false
	for _, d := range m.requests {
		if d.ID == id {
			if err != nil {
				d.State = "incompatible"
			} else {
				d.State = "compatible"
				d.Name = manifest.Name
				d.Manifest = manifest
				announced = d.Announced
				if m.verified == nil {
					m.verified = map[string]verifiedSite{}
				}
				m.verified[origin] = verifiedSite{manifest: manifest, at: time.Now()}
			}
			break
		}
	}
	m.mu.Unlock()
	if err != nil {
		log.Printf("Discovery of %s: incompatible (%v)", origin, err)
		a.recordFor(origin, "app", "error", "Unsupported website", err.Error())
	}
	if err == nil && announced {
		a.announceCompatible(id, origin, manifest.Name)
	}
}

// The page the person is visiting accepts the tunnel. The notification only announces
// it: a click asks the page to continue, and access still needs the consent dialog.
// At most one notification per site a day and three every ten minutes; the journal keeps
// every announcement, also when notifications are off or unavailable.
func (a *application) announceCompatible(id, origin, name string) {
	site := strings.TrimPrefix(origin, "https://")
	a.recordFor(origin, "app", "info", "Page supports Jase Tunnel", "Identifies itself as “"+name+"” (unverified name).")
	if a.originConnected(origin) {
		log.Printf("Announcement of %s: no notification, already connected", site)
		return
	}
	m := a.webManager()
	m.mu.Lock()
	now := time.Now()
	if m.notified == nil {
		m.notified = map[string]time.Time{}
	}
	for key, value := range m.notified {
		if now.Sub(value) >= 24*time.Hour {
			delete(m.notified, key)
		}
	}
	recent := m.notices[:0]
	for _, value := range m.notices {
		if now.Sub(value) < 10*time.Minute {
			recent = append(recent, value)
		}
	}
	m.notices = recent
	if !m.notified[origin].IsZero() || len(m.notices) >= 3 {
		m.mu.Unlock()
		log.Printf("Announcement of %s: no notification, limit reached", site)
		a.recordFor(origin, "app", "info", "Notification not repeated", "At most one notification per website per day and three every ten minutes.")
		return
	}
	m.notified[origin] = now
	m.notices = append(m.notices, now)
	m.mu.Unlock()
	log.Printf("Announcement of %s: notification requested (native %v)", site, a.notify != nil)
	if a.notify != nil {
		a.notify(id, site+" supports Jase Tunnel", "Connect your local LLM to this page? You will choose a model before any access.")
	}
}

// A click on the notification lets the waiting page continue to the consent dialog.
// It grants nothing by itself.
func (a *application) requestWebConnection(id string) {
	m := a.webManager()
	m.mu.Lock()
	origin := ""
	for _, d := range m.requests {
		if d.ID == id && d.State == "compatible" && d.ExpiresAt > time.Now().UnixMilli() {
			d.State = "requested"
			origin = d.Origin
			break
		}
	}
	m.mu.Unlock()
	if origin == "" {
		a.record("app", "info", "Announcement expired", "Return to the website and use its Jase Tunnel button to connect.")
		showDesktopUI()
		return
	}
	a.recordFor(origin, "app", "info", "Connection requested from notification", "If the page is still open, choose a model to allow here.")
}

func (a *application) originConnected(origin string) bool {
	now := time.Now().UnixMilli()
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, c := range a.settings.Links {
		if c.Origin == origin && c.ExpiresAt > now {
			return true
		}
	}
	return false
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
		approval := &webApproval{ID: d.ID, Origin: d.Origin, Site: strings.TrimPrefix(d.Origin, "https://"), Name: d.Name, Models: models}
		approval.Text = webConsentText(approval)
		return approval
	}
	return nil
}

// Any site can borrow a well-known name in its manifest: the verified address leads,
// the declared name is labelled as the site's own claim.
func webConsentText(p *webApproval) string {
	return "Verified address: " + p.Site + "\nWebsite-provided name (unverified): " + p.Name + "\n\nThe website will send messages and data to your selected model. Responses and tool requests will return to the website’s server. Model weights stay on this computer. The connection lasts up to 30 minutes.\n\nAllow only if you recognize the address " + p.Site + "."
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
		now := time.Now()
		for site, at := range m.rejected {
			if now.Sub(at) >= consentCooldown {
				delete(m.rejected, site)
			}
		}
		m.rejected[current.Origin] = now
		m.mu.Unlock()
		a.recordFor(current.Origin, "app", "info", "Connection declined", "You declined the tunnel in the panel.")
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
	err := a.remote(ctx, grant, "pair", map[string]any{"protocol": webProtocol, "ticket": ticket, "label": "My computer", "models": []model{chosen}}, &result)
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
		a.recordFor(current.Origin, "app", "error", "Connection failed", "The website broker did not accept the ticket: "+err.Error())
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
		if c.ExpiresAt > time.Now().UnixMilli() {
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
	a.recordFor(grant.Origin, "app", "info", "Tunnel aperto", chosen.Name+" · until "+time.UnixMilli(grant.ExpiresAt).Format("15:04"))
	a.startLink(grant)
}

// «Disconnect websites» removes every grant and stops their inferences. Their poll loops end
// at the next check; a site can pair again only through a new consent.
func (a *application) revokeLinks() {
	a.mu.Lock()
	revoked := append([]link{}, a.settings.Links...)
	for _, c := range a.settings.Links {
		if cancel := a.runs[c.Token]; cancel != nil {
			cancel()
		}
	}
	a.settings.Links = []link{}
	_ = a.saveLocked()
	a.mu.Unlock()
	m := a.webManager()
	m.mu.Lock()
	for _, d := range m.requests {
		d.State = "rejected"
		d.Ticket = ""
	}
	m.mu.Unlock()
	a.record("app", "info", "Websites disconnected", "All connections were revoked and running inference was cancelled.")
	for _, c := range revoked {
		a.recordFor(c.Origin, "app", "info", "Tunnel closed", "Disconnected from the panel.")
		go a.notifyRevocation(c)
	}
}

// revokeSite closes the tunnels of one site only (its «Disconnect website» button).
func (a *application) revokeSite(origin string) {
	a.mu.Lock()
	kept, revoked := []link{}, []link{}
	for _, c := range a.settings.Links {
		if c.Origin != origin {
			kept = append(kept, c)
			continue
		}
		revoked = append(revoked, c)
		if cancel := a.runs[c.Token]; cancel != nil {
			cancel()
		}
	}
	a.settings.Links = kept
	_ = a.saveLocked()
	a.mu.Unlock()
	m := a.webManager()
	m.mu.Lock()
	for _, d := range m.requests {
		if d.Origin == origin {
			d.State = "rejected"
			d.Ticket = ""
		}
	}
	m.mu.Unlock()
	if len(revoked) > 0 {
		a.recordFor(origin, "app", "info", "Tunnel closed", "Disconnected from the panel. Running inference was cancelled.")
	}
	for _, c := range revoked {
		go a.notifyRevocation(c)
	}
}

// Best effort: the site's broker stops presenting the grant to the browser at once,
// so the page can announce itself again. The token is already gone from this computer.
func (a *application) notifyRevocation(c link) {
	if !validWebLink(c) {
		return
	}
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", c.Origin+c.Broker+"revoke", strings.NewReader("{}"))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if response, err := a.webHTTPClient().Do(req); err == nil {
		response.Body.Close()
	}
}
func (a *application) webGrantActive(c link) bool {
	if !validWebLink(c) {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, active := range a.settings.Links {
		if active.Token == c.Token && active.ExpiresAt > time.Now().UnixMilli() {
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
