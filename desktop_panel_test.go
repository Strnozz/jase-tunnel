package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDesktopPanelKeepsSensitiveStatePrivateAndFiltersEvents(t *testing.T) {
	a := newTestApp(t)
	a.settings.Links = []link{{Origin: "https://jaseengine.it", Token: strings.Repeat("secret", 10)}}
	a.models = []model{{Name: "Fixture", Engine: "ollama", Tools: true}}
	a.files = []modelFile{{ID: "weights-id", Name: "weights.gguf", Path: "/private/weights/weights.gguf"}}
	a.serverContact("https://jaseengine.it")
	a.record("server", "info", "Invio", "Metadata")
	a.record("receive", "error", "Errore di ricezione", "Fixture")
	p := a.panelSnapshot(0)
	if p.Title != "Collegato a Jase" || len(p.Models) != 2 || p.Models[1].ID != "weights-id" {
		t.Fatal("panel lost connection or import availability")
	}
	if len(a.panelSnapshot(1).Events) != 1 || len(a.panelSnapshot(2).Events) != 1 || len(a.panelSnapshot(4).Events) != 1 {
		t.Fatal("event filters exposed unrelated events")
	}
	raw, _ := json.Marshal(p)
	for _, secret := range []string{strings.Repeat("secret", 10), "/private/weights/"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("panel exposed private credential or weight path")
		}
	}
	a.setDetailedLogging(true)
	a.recordDetailed("Risposta", "private model answer")
	before := a.panelSnapshot(0)
	a.setDetailedLogging(false)
	after := a.panelSnapshot(0)
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
