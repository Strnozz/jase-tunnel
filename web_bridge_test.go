package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func bridgeRequest(a *application, localOrigin, origin, operation, receipt string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(map[string]any{"origin": origin, "operation": operation, "receipt": receipt, "body": body})
	r := httptest.NewRequest("POST", "http://127.0.0.1:11439/v1/browser-bridge", strings.NewReader(string(raw)))
	if localOrigin != "" {
		r.Header.Set("Origin", localOrigin)
	}
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	return w
}

func TestBrowserWindowAndRelayBoundaries(t *testing.T) {
	a := newTestApp(t)
	r := httptest.NewRequest("GET", "http://127.0.0.1:11439/tunnel/connect", nil)
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "event.source !== opener") || !strings.Contains(w.Body.String(), "event.origin !== origin") {
		t.Fatal("missing connection window and sender checks")
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") || w.Header().Get("X-Frame-Options") != "DENY" || strings.Contains(w.Body.String(), "{{nonce}}") {
		t.Fatal("window must have a nonce CSP and reject embedding")
	}
	for _, origin := range []string{"", "null", "https://shop.example.com", "http://evil.test"} {
		response := bridgeRequest(a, origin, "https://shop.example.com", "discover", "", map[string]any{"protocol": webProtocol})
		if response.Code != 403 || response.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("external page can access the local relay", origin, response.Code)
		}
	}
	for _, origin := range []string{"http://shop.example.com", "https://127.0.0.1", "https://shop.example.com/", "null"} {
		if bridgeRequest(a, "http://127.0.0.1:11439", origin, "discover", "", nil).Code != 403 {
			t.Fatal("relay accepted an invalid website origin", origin)
		}
	}
	for _, operation := range []string{"status", "quit", "add-model", "../v1/status", "https://evil.test"} {
		if bridgeRequest(a, "http://127.0.0.1:11439", "https://shop.example.com", operation, "", nil).Code != 400 {
			t.Fatal("relay exposed a management operation", operation)
		}
	}
	for _, body := range []string{"{}{}", "{", `{"origin":"` + strings.Repeat("a", 9000) + `"}`} {
		request := httptest.NewRequest("POST", "http://127.0.0.1:11439/v1/browser-bridge", strings.NewReader(body))
		request.Header.Set("Origin", "http://127.0.0.1:11439")
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		a.ServeHTTP(response, request)
		if response.Code != 400 {
			t.Fatal("invalid or oversized relay body accepted", response.Code)
		}
	}
}

func TestBrowserRelayKeepsWebsiteIsolationAndNativeConsent(t *testing.T) {
	a := newTestApp(t)
	a.models = []model{{Key: "ollama:fixture", Name: "Private Model", Engine: "ollama", Tools: true, ContextTokens: 8192}}
	paired := make(chan struct{}, 1)
	mockWeb(t, a, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "jase-tunnel.json") {
			return response(fixtureManifest), nil
		}
		if strings.HasSuffix(r.URL.Path, "/pair") {
			paired <- struct{}{}
			return response(`{"protocol":"jase-tunnel-v1","token":"` + strings.Repeat("a", 64) + `","connectionId":"` + strings.Repeat("b", 32) + `","expiresAt":` + jsonInt(time.Now().Add(20*time.Minute).UnixMilli()) + `}`), nil
		}
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	const localOrigin = "http://127.0.0.1:11439"
	const siteOrigin = "https://shop.example.com"
	discover := bridgeRequest(a, localOrigin, siteOrigin, "discover", "", map[string]any{"protocol": webProtocol})
	if discover.Code != 202 || discover.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal(discover.Code, discover.Body.String())
	}
	var receipt struct{ Receipt string }
	_ = json.Unmarshal(discover.Body.Bytes(), &receipt)
	waitWebState(t, a, siteOrigin, receipt.Receipt, "compatible")
	if bridgeRequest(a, localOrigin, "https://other.example.com", "discovery", receipt.Receipt, nil).Code != 403 {
		t.Fatal("window relay let another website use the receipt")
	}
	if bridgeRequest(a, localOrigin, siteOrigin, "discovery", strings.Repeat("e", 64), nil).Code != 410 {
		t.Fatal("window relay accepted an invented receipt")
	}
	pair := bridgeRequest(a, localOrigin, siteOrigin, "pair", receipt.Receipt, map[string]any{"protocol": webProtocol, "ticket": strings.Repeat("c", 64)})
	if pair.Code != 202 {
		t.Fatal(pair.Code, pair.Body.String())
	}
	select {
	case <-paired:
		t.Fatal("window relay bypassed native consent")
	default:
	}
	approval := a.claimWebApproval()
	if approval == nil || approval.Origin != siteOrigin || len(approval.Models) != 1 {
		t.Fatal("missing native model consent")
	}
	state := bridgeRequest(a, localOrigin, siteOrigin, "discovery", receipt.Receipt, nil)
	for _, private := range []string{"Private Model", "ollama:", "models", "token", "files"} {
		if strings.Contains(state.Body.String(), private) {
			t.Fatal("connection window exposed private inventory", private)
		}
	}
	a.answerWebConsent(approval.ID, approval.Models[0].Key)
	waitWebState(t, a, siteOrigin, receipt.Receipt, "connected")
	state = bridgeRequest(a, localOrigin, siteOrigin, "discovery", receipt.Receipt, nil)
	if strings.Contains(state.Body.String(), strings.Repeat("a", 64)) {
		t.Fatal("connection window exposed desktop credentials")
	}
	a.cancel()
}
