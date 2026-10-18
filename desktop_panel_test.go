package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestDesktopPanelKeepsSensitiveStatePrivateAndFiltersEvents(t *testing.T) {
	a := newTestApp(t)
	a.settings.Links = []link{{Protocol: webProtocol, Origin: "https://shop.example.com", Broker: "/api/jase-tunnel/v1/", Model: model{Key: "ollama:fixture", Name: "Fixture"}, ExpiresAt: time.Now().Add(10 * time.Minute).UnixMilli(), Token: strings.Repeat("secret", 10)}}
	a.models = []model{{Name: "Fixture", Engine: "ollama", Tools: true}}
	a.files = []modelFile{{ID: "weights-id", Name: "weights.gguf", Path: "/private/weights/weights.gguf"}}
	a.serverContact("https://shop.example.com")
	a.record("server", "info", "Invio", "Metadata")
	a.record("receive", "error", "Errore di ricezione", "Fixture")
	p := a.panelSnapshot(0, allSites)
	if p.Title != "1 tunnel aperto" || !strings.Contains(p.Connection, "shop.example.com (Fixture)") || len(p.Models) != 2 || p.Models[1].ID != "weights-id" {
		t.Fatal("panel lost connection or import availability")
	}
	if len(a.panelSnapshot(1, allSites).Events) != 1 || len(a.panelSnapshot(2, allSites).Events) != 1 || len(a.panelSnapshot(4, allSites).Events) != 1 {
		t.Fatal("event filters exposed unrelated events")
	}
	raw, _ := json.Marshal(p)
	for _, secret := range []string{strings.Repeat("secret", 10), "/private/weights/"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("panel exposed private credential or weight path")
		}
	}
	a.setDetailedLogging(true)
	a.recordDetailed("", "Risposta", "private model answer")
	before := a.panelSnapshot(0, allSites)
	a.setDetailedLogging(false)
	after := a.panelSnapshot(0, allSites)
	selection := panelEventSelection(before.Events, after.Events, len(before.Events)-1, before.Generation == after.Generation)
	if selection != -1 || strings.Contains(eventDetail(after.Events, selection), "private model answer") {
		t.Fatal("selected inspector retained cleared model text")
	}
}

func TestDesktopSelectionFollowsIdentityAcrossRefresh(t *testing.T) {
	weights := panelModel{ID: "weights", Name: "same.gguf", Engine: "GGUF"}
	other := panelModel{ID: "other", Name: "same.gguf", Engine: "GGUF"}
	if index := panelModelSelection([]panelModel{weights}, []panelModel{other, weights}, 0); index != 1 {
		t.Fatal("refresh changed the selected weights")
	}
	if index := panelModelSelection([]panelModel{weights}, []panelModel{other}, 0); index != -1 {
		t.Fatal("disappearing weights selected a different import")
	}
	old := []activityEvent{{ID: 12}, {ID: 13}}
	if index := panelEventSelection(old, []activityEvent{{ID: 13}, {ID: 14}}, 1, true); index != 0 {
		t.Fatal("journal selection did not follow event identity")
	}
	if index := panelEventSelection(old, []activityEvent{{ID: 14}}, 1, true); index != -1 {
		t.Fatal("evicted event selected unrelated private details")
	}
}

func TestDesktopImportSerializesSelectorsAndRechecksDetectedFiles(t *testing.T) {
	a := newTestApp(t)
	a.importMu.Lock()
	called := false
	result := a.importFromDesktop(func(context.Context) (string, bool, error) { called = true; return "", false, nil })
	select {
	case message := <-result:
		if called || !strings.Contains(message, "già in corso") {
			t.Fatal("concurrent import opened another selector")
		}
	case <-time.After(time.Second):
		t.Fatal("import completion blocked")
	}
	a.importMu.Unlock()
	select {
	case message := <-a.importDetectedFromDesktop("removed-id"):
		if !strings.Contains(message, "non più disponibili") {
			t.Fatal("stale detected file was imported")
		}
	case <-time.After(time.Second):
		t.Fatal("missing file did not finish")
	}
}

// Each site has its own console and the site list shows open tunnels first, with the
// model and the expiry; «Scollega sito» closes only that site's tunnel.
func TestEachSiteHasItsConsoleAndItsOwnDisconnect(t *testing.T) {
	a := newTestApp(t)
	revoked := make(chan *http.Request, 1)
	mockWeb(t, a, func(r *http.Request) (*http.Response, error) {
		revoked <- r
		return response(`{"ok":true}`), nil
	})
	m := model{Key: "ollama:qwen", Name: "qwen", Engine: "ollama", ContextTokens: 16384, Tools: true}
	shop := storedGrant(a, m)
	other := link{Protocol: webProtocol, Origin: "https://tools.example.org", Broker: "/api/jase-tunnel/v1/", Model: m, ExpiresAt: shop.ExpiresAt, Token: strings.Repeat("d", 64), ConnectionID: strings.Repeat("e", 32)}
	a.mu.Lock()
	a.settings.Links = append(a.settings.Links, other)
	a.mu.Unlock()
	a.recordFor("https://news.example.net", "app", "info", "Pagina compatibile con Jase Tunnel", "")
	a.recordFor(shop.Origin, "model", "info", "Richiesta ricevuta", "shop")
	a.recordFor(other.Origin, "model", "info", "Richiesta ricevuta", "tools")
	a.record("app", "info", "Jase Tunnel avviato", "")
	p := a.panelSnapshot(0, allSites)
	if p.Title != "2 tunnel aperti" || len(p.Sites) != 5 || !p.Sites[2].Open || !p.Sites[3].Open || p.Sites[4].Label != "news.example.net" || p.Sites[4].Open {
		t.Fatalf("site list: %+v", p.Sites)
	}
	if !strings.HasPrefix(p.Sites[2].Status, "qwen · fino alle ") {
		t.Fatalf("open tunnel without model or expiry: %q", p.Sites[2].Status)
	}
	console := a.panelSnapshot(0, shop.Origin).Events
	if len(console) != 1 || console[0].Detail != "shop" || len(a.panelSnapshot(0, "").Events) != 1 || len(p.Events) != 4 {
		t.Fatal("a console shows another site's events")
	}
	a.revokeSite(shop.Origin)
	if a.webGrantActive(shop) || !a.webGrantActive(other) {
		t.Fatal("disconnecting one site touched the other")
	}
	if a.panelSnapshot(0, allSites).Title != "1 tunnel aperto" {
		t.Fatal("closed tunnel still listed as open")
	}
	// Wait for the background validation before later tests change devBuild.
	select {
	case r := <-revoked:
		if r.Method != "POST" || r.URL.String() != shop.Origin+shop.Broker+"revoke" || r.Header.Get("Authorization") != "Bearer "+shop.Token {
			t.Fatal("disconnect notified the wrong site's broker")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("site never learned about the disconnect")
	}
}
