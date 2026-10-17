package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestNativeJournalHasNoHTTPInterface(t *testing.T) {
	a := newTestApp(t)
	a.settings.Links = []link{{Origin: "https://jaseengine.it", Token: strings.Repeat("s", 64)}}
	a.files = []modelFile{{Name: "fixture.gguf", Path: "/private/full-path/fixture.gguf"}}
	a.record("model", "info", "Private activity", "private fixture text")
	for _, origin := range []string{"", "http://127.0.0.1:11439", "https://jaseengine.it", "https://evil.test"} {
		for _, path := range []string{"/control/", "/control/state", "/v1/status"} {
			r := localRequest("GET", path, "", origin)
			w := httptest.NewRecorder()
			a.ServeHTTP(w, r)
			if path != "/v1/status" && w.Code == 200 {
				t.Fatal("native journal has an HTTP endpoint")
			}
			for _, secret := range []string{"private fixture text", strings.Repeat("s", 64), "/private/full-path/fixture.gguf"} {
				if strings.Contains(w.Body.String(), secret) {
					t.Fatal("HTTP exposed private native state")
				}
			}
		}
	}
	raw, _ := json.Marshal(a.desktopState(0))
	if !strings.Contains(string(raw), "private fixture text") {
		t.Fatal("native snapshot lost its journal")
	}
	for _, secret := range []string{strings.Repeat("s", 64), "/private/full-path/fixture.gguf"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("snapshot included device credentials or full file paths")
		}
	}
}

func TestNativeDebugClearsConversationAndRejectsLateCallbacks(t *testing.T) {
	a := newTestApp(t)
	a.setDetailedLogging(true)
	a.logTask(task{ID: "fixture", Model: model{Name: "Fixture"}, Messages: []message{{Role: "user", Content: "private fixture text"}}})
	raw, _ := json.Marshal(a.desktopState(0))
	if !strings.Contains(string(raw), "private fixture text") {
		t.Fatal("detailed native log missing")
	}
	a.setDetailedLogging(false)
	a.recordDetailed("Risposta del modello", "private fixture text")
	raw, _ = json.Marshal(a.desktopState(0))
	if strings.Contains(string(raw), "private fixture text") || a.debug.Load() {
		t.Fatal("text remained after disabling logging")
	}
}

func TestNativeJournalIsBoundedConcurrentAndSupportsCursor(t *testing.T) {
	a := newTestApp(t)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 100; n++ {
				a.record("server", "info", "Fixture request", strings.Repeat("x", 9000))
			}
		}()
	}
	wg.Wait()
	state := a.desktopState(0)
	events := state["events"].([]activityEvent)
	if len(events) != activityLimit || state["cursor"] != uint64(500) {
		t.Fatal("unbounded native journal")
	}
	for _, e := range events {
		if len(e.Detail) > 8100 {
			t.Fatal("unbounded event detail")
		}
	}
	if len(a.desktopState(499)["events"].([]activityEvent)) != 1 {
		t.Fatal("cursor did not return only new events")
	}
	before := state["generation"].(uint64)
	a.clearActivity()
	state = a.desktopState(500)
	if len(state["events"].([]activityEvent)) != 0 || state["generation"].(uint64) <= before {
		t.Fatal("native view cannot detect a cleared journal")
	}
}

func TestNativeConnectionsAreDeduplicatedAndThinkingIsControllable(t *testing.T) {
	a := newTestApp(t)
	a.settings.Links = []link{{Origin: "https://jaseengine.it"}, {Origin: "https://jaseengine.it"}}
	a.serverContact("https://jaseengine.it")
	connections := a.desktopState(0)["connections"].([]map[string]any)
	if len(connections) != 1 || connections[0]["connected"] != true {
		t.Fatal("connection state is incorrect")
	}
	a.setModelThinking(true)
	if !a.think.Load() {
		t.Fatal("native thinking switch failed")
	}
	a.setModelThinking(false)
	if a.think.Load() {
		t.Fatal("native thinking switch stayed enabled")
	}
}

// Opening the development app while a release app owns the port must offer a
// replacement, rather than quietly focusing the app that rejects localhost.
func TestDesktopReuseRequiresMatchingBuildAndSiteMode(t *testing.T) {
	previous := devBuild
	t.Cleanup(func() { devBuild = previous })
	state := desktopIdentity{Protocol: protocol, Desktop: true, DesktopUI: "appkit-v1", Version: version}
	devBuild = "false"
	if !currentDesktop(state) {
		t.Fatal("matching release should focus its window")
	}
	devBuild = "true"
	if currentDesktop(state) {
		t.Fatal("development launch reused a localhost-incompatible release")
	}
	state.Development = true
	if !currentDesktop(state) {
		t.Fatal("matching development app should focus its window")
	}
	state.Version = "previous-build"
	if currentDesktop(state) {
		t.Fatal("a new icon or code build cannot replace an older running build")
	}
}
