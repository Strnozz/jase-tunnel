package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func discoverReceipt(t *testing.T, a *application, origin string, body map[string]any) (int, string) {
	t.Helper()
	w := webRequest(a, "discover", origin, "", body)
	var out struct{ Receipt string }
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out.Receipt
}

// One busy or hostile site uses up its own budget, not the budget of every site.
func TestDiscoveryLimitsArePerSite(t *testing.T) {
	a := newTestApp(t)
	mockWeb(t, a, func(*http.Request) (*http.Response, error) { return response(fixtureManifest), nil })
	noisy := "https://noisy.example.com"
	code, receipt := discoverReceipt(t, a, noisy, map[string]any{"protocol": webProtocol})
	if code != 202 {
		t.Fatalf("first discovery refused: %d", code)
	}
	waitWebState(t, a, noisy, receipt, "compatible")
	refused := false
	for i := 0; i < discoverPerOrigin+5; i++ {
		if code, _ := discoverReceipt(t, a, noisy, map[string]any{"protocol": webProtocol}); code == 429 {
			refused = true
			break
		}
	}
	if !refused {
		t.Fatal("one site can discover without limit")
	}
	quiet := "https://quiet.example.org"
	if code, _ := discoverReceipt(t, a, quiet, map[string]any{"protocol": webProtocol}); code != 202 {
		t.Fatalf("another site blocked by the noisy one: HTTP %d", code)
	}
}

// A newer site module may send fields this app does not know yet.
func TestWebEndpointsIgnoreUnknownFields(t *testing.T) {
	a := newTestApp(t)
	mockWeb(t, a, func(*http.Request) (*http.Response, error) { return response(fixtureManifest), nil })
	origin := "https://shop.example.com"
	code, receipt := discoverReceipt(t, a, origin, map[string]any{"protocol": webProtocol, "futureField": map[string]any{"x": 1}})
	if code != 202 {
		t.Fatalf("discovery with an extra field refused: %d", code)
	}
	waitWebState(t, a, origin, receipt, "compatible")
	pair := webRequest(a, "pair", origin, receipt, map[string]any{"protocol": webProtocol, "ticket": strings.Repeat("c", 64), "futureField": true})
	if pair.Code != 202 {
		t.Fatalf("pair with an extra field refused: %s", pair.Body.String())
	}
}

// A page cannot keep reopening the consent dialog: one at a time per site, and a pause
// after «Decline».
func TestConsentDialogCannotBeReopenedInALoop(t *testing.T) {
	a := newTestApp(t)
	a.models = []model{{Key: "ollama:fixture", Name: "Fixture LLM", Engine: "ollama", Tools: true, ContextTokens: 8192}}
	mockWeb(t, a, func(*http.Request) (*http.Response, error) { return response(fixtureManifest), nil })
	origin := "https://shop.example.com"
	ticket := map[string]any{"protocol": webProtocol, "ticket": strings.Repeat("c", 64)}
	_, first := discoverReceipt(t, a, origin, map[string]any{"protocol": webProtocol})
	waitWebState(t, a, origin, first, "compatible")
	_, second := discoverReceipt(t, a, origin, map[string]any{"protocol": webProtocol})
	if webRequest(a, "pair", origin, first, ticket).Code != 202 {
		t.Fatal("first consent request refused")
	}
	if code := webRequest(a, "pair", origin, second, ticket).Code; code != 409 {
		t.Fatalf("second consent dialog for the same site: HTTP %d", code)
	}
	approval := a.claimWebApproval()
	if approval == nil {
		t.Fatal("consent dialog missing")
	}
	a.answerWebConsent(approval.ID, "")
	_, third := discoverReceipt(t, a, origin, map[string]any{"protocol": webProtocol})
	if code := webRequest(a, "pair", origin, third, ticket).Code; code != 429 {
		t.Fatalf("site reopened the dialog right after «Decline»: HTTP %d", code)
	}
	other := "https://tools.example.org"
	_, receipt := discoverReceipt(t, a, other, map[string]any{"protocol": webProtocol})
	waitWebState(t, a, other, receipt, "compatible")
	if webRequest(a, "pair", other, receipt, ticket).Code != 202 {
		t.Fatal("rejecting one site blocked another")
	}
}

// A busy engine that misses one refresh keeps its models for a while; an engine that
// stays silent disappears.
func TestModelsSurviveATransientEngineFailure(t *testing.T) {
	a := newTestApp(t)
	previous := engineTransport
	t.Cleanup(func() { engineTransport = previous })
	ollamaUp := true
	engineTransport = roundTrip(func(r *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(r.URL.String(), ollamaURL) || !ollamaUp {
			return nil, errors.New("engine unavailable")
		}
		switch r.URL.Path {
		case "/api/tags":
			return response(`{"models":[{"name":"fixture:latest","size":1}]}`), nil
		case "/api/show":
			return response(`{"capabilities":["completion","tools"],"model_info":{"llama.context_length":16384}}`), nil
		}
		return nil, errors.New("unexpected path")
	})
	a.refreshModels()
	if len(a.models) != 1 || a.models[0].Key != "ollama:fixture:latest" || !a.models[0].Tools {
		t.Fatalf("models not listed: %+v", a.models)
	}
	ollamaUp = false
	a.refreshModels()
	if len(a.models) != 1 {
		t.Fatal("one missed refresh removed the models")
	}
	a.mu.Lock()
	a.engineSeen["ollama"] = time.Now().Add(-engineGrace - time.Second)
	a.mu.Unlock()
	a.refreshModels()
	if len(a.models) != 0 {
		t.Fatal("models of a closed engine stay listed")
	}
}
