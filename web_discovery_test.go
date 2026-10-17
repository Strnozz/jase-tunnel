package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const fixtureManifest = `{"name":"Fixture Shop","protocols":["jase-tunnel-v1"],"broker":"/api/jase-tunnel/v1/","requirements":{"capabilities":["chat","tool-calls"],"contextTokens":2048}}`

func webRequest(a *application, path, origin, receipt string, body any) *httptest.ResponseRecorder {
	raw := ""
	method := "GET"
	if body != nil {
		data, _ := json.Marshal(body)
		raw = string(data)
		method = "POST"
	}
	r := httptest.NewRequest(method, "http://127.0.0.1:11439/tunnel/v1/"+path, strings.NewReader(raw))
	r.Header.Set("Origin", origin)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if receipt != "" {
		r.Header.Set("Authorization", "Bearer "+receipt)
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	return w
}
func waitWebState(t *testing.T, a *application, origin, receipt, want string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		w := webRequest(a, "discovery", origin, receipt, nil)
		var p map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &p)
		if p["state"] == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("web discovery never reached %s", want)
}
func mockWeb(t *testing.T, a *application, fn func(*http.Request) (*http.Response, error)) {
	t.Helper()
	a.webHTTP = &http.Client{Transport: roundTrip(fn)}
}

func TestWebNetworkBoundaries(t *testing.T) {
	for _, origin := range []string{"https://shop.example.com", "https://tools.example.org"} {
		if !webOrigin(origin) {
			t.Fatal("valid public origin rejected", origin)
		}
	}
	for _, origin := range []string{"https://127.0.0.1", "https://[::1]", "https://shop.local", "https://shop.example.com:8443", "https://a@shop.example.com", "https://shop.example.com/", "https://SHOP.example.com", "null", "http://shop.example.com", "https://shop.example.com/?x=1"} {
		if webOrigin(origin) {
			t.Fatal("unsafe origin allowed", origin)
		}
	}
	for _, value := range []string{"127.0.0.1", "10.1.2.3", "169.254.169.254", "100.64.1.1", "192.0.2.5", "::ffff:127.0.0.1", "2001:db8::1", "2002:7f00:1::"} {
		if publicWebIP(net.ParseIP(value)) {
			t.Fatal("unsafe IP allowed", value)
		}
	}
	for _, value := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if !publicWebIP(net.ParseIP(value)) {
			t.Fatal("public IP refused", value)
		}
	}
	for _, path := range []string{"//evil/", "/api/../", "/api/%2e/", "https://evil/", "/api/?x=1/", "/api/\\evil/"} {
		if webBrokerPath(path) {
			t.Fatal("unsafe broker path allowed", path)
		}
	}
}

func TestWebDiscoveryNeedsLocalConsentAndDoesNotExposeInventory(t *testing.T) {
	a := newTestApp(t)
	a.models = []model{{Key: "ollama:fixture", Name: "Fixture LLM", Engine: "ollama", Tools: true, ContextTokens: 8192}}
	origin := "https://shop.example.com"
	paired := make(chan struct{}, 1)
	mockWeb(t, a, func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Cookie") != "" {
			t.Error("cookie sent to manifest/broker")
		}
		if strings.HasSuffix(r.URL.Path, "jase-tunnel.json") {
			return response(fixtureManifest), nil
		}
		if strings.HasSuffix(r.URL.Path, "/pair") {
			var input struct {
				Models []model `json:"models"`
			}
			_ = json.NewDecoder(r.Body).Decode(&input)
			if len(input.Models) != 1 || input.Models[0].Key != "ollama:fixture" {
				t.Error("unapproved inventory sent")
			}
			paired <- struct{}{}
			return response(`{"protocol":"jase-tunnel-v1","token":"` + strings.Repeat("a", 64) + `","connectionId":"` + strings.Repeat("b", 32) + `","expiresAt":` + jsonInt(time.Now().Add(20*time.Minute).UnixMilli()) + `}`), nil
		}
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	discover := webRequest(a, "discover", origin, "", map[string]any{"protocol": webProtocol})
	if discover.Code != 202 {
		t.Fatal(discover.Body.String())
	}
	var receipt struct {
		Receipt string `json:"receipt"`
	}
	_ = json.Unmarshal(discover.Body.Bytes(), &receipt)
	waitWebState(t, a, origin, receipt.Receipt, "compatible")
	status := webRequest(a, "discovery", origin, receipt.Receipt, nil)
	for _, private := range []string{"Fixture LLM", "ollama:", "models", "token", "files"} {
		if strings.Contains(status.Body.String(), private) {
			t.Fatal("private inventory exposed", private)
		}
	}
	if webRequest(a, "discovery", "https://other.example.com", receipt.Receipt, nil).Code != 403 {
		t.Fatal("receipt accepted from another origin")
	}
	pair := webRequest(a, "pair", origin, receipt.Receipt, map[string]any{"protocol": webProtocol, "ticket": strings.Repeat("c", 64)})
	if pair.Code != 202 {
		t.Fatal(pair.Body.String())
	}
	select {
	case <-paired:
		t.Fatal("paired before local approval")
	default:
	}
	approval := a.claimWebApproval()
	if approval == nil || len(approval.Models) != 1 {
		t.Fatal("no native consent request")
	}
	if a.claimWebApproval() != nil {
		t.Fatal("consent shown twice")
	}
	a.answerWebConsent(approval.ID, approval.Models[0].Key)
	waitWebState(t, a, origin, receipt.Receipt, "connected")
	a.mu.RLock()
	links := append([]link{}, a.settings.Links...)
	a.mu.RUnlock()
	if len(links) != 1 || links[0].Model.Key != "ollama:fixture" {
		t.Fatal("model grant missing")
	}
	active, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.webRuns = map[string]context.CancelFunc{links[0].Token: cancel}
	a.mu.Unlock()
	a.revokeWebLinks()
	a.mu.RLock()
	remaining := len(a.settings.Links)
	a.mu.RUnlock()
	if active.Err() == nil || remaining != 0 {
		t.Fatal("revocation did not cancel active inference")
	}
	a.cancel()
}

func jsonInt(value int64) string { raw, _ := json.Marshal(value); return string(raw) }

func TestWebRejectsInvalidManifestRateAbuseAndGrantEscalation(t *testing.T) {
	a := newTestApp(t)
	mockWeb(t, a, func(*http.Request) (*http.Response, error) {
		return response(strings.Replace(fixtureManifest, `"/api/jase-tunnel/v1/"`, `"//other.example.com/"`, 1)), nil
	})
	origin := "https://shop.example.com"
	d := webRequest(a, "discover", origin, "", map[string]any{"protocol": webProtocol})
	var receipt struct {
		Receipt string `json:"receipt"`
	}
	_ = json.Unmarshal(d.Body.Bytes(), &receipt)
	waitWebState(t, a, origin, receipt.Receipt, "incompatible")
	if webRequest(a, "discover", origin, "", map[string]any{"protocol": webProtocol}).Code != 429 {
		t.Fatal("discovery rate limit missing")
	}
	if webRequest(a, "pair", origin, receipt.Receipt, map[string]any{"protocol": webProtocol, "ticket": strings.Repeat("a", 64)}).Code != 409 {
		t.Fatal("incompatible manifest paired")
	}
	c := link{Protocol: webProtocol, ExpiresAt: time.Now().Add(time.Minute).UnixMilli(), Model: model{Key: "ollama:approved", Engine: "ollama", ContextTokens: 8192}}
	task := task{Model: c.Model, MaxTokens: 100, ContextTokens: 4096, ExpiresAt: time.Now().Add(time.Second).UnixMilli()}
	if !validWebTask(c, task) {
		t.Fatal("approved task rejected")
	}
	task.Model.Key = "ollama:other"
	if validWebTask(c, task) {
		t.Fatal("model switch allowed")
	}
	task.Model = c.Model
	task.ContextTokens = 32768
	if validWebTask(c, task) {
		t.Fatal("context escalation allowed")
	}
}

func TestWebManifestFetchDoesNotFollowRedirects(t *testing.T) {
	// The production transport/client enforce this independently from discovery mocks.
	redirect := webClient.CheckRedirect
	if redirect == nil || redirect(nil, nil) != http.ErrUseLastResponse {
		t.Fatal("remote redirects enabled")
	}
	a := newTestApp(t)
	r := httptest.NewRequest("OPTIONS", "http://127.0.0.1:11439/tunnel/v1/discover", nil)
	r.Header.Set("Origin", "https://shop.example.com")
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 204 || len(a.webManager().requests) != 0 {
		t.Fatal("preflight changed state")
	}
	r = httptest.NewRequest("GET", "http://127.0.0.1:11439/v1/status", nil)
	r.Header.Set("Origin", "https://shop.example.com")
	w = httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("new origin reached legacy inventory")
	}
	// Exercise the actual dialer against a DNS literal without contacting the address.
	transport := webTransport
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := transport.DialContext(ctx, "tcp", "127.0.0.1:443")
	if conn != nil {
		_ = conn.Close()
	}
	if err == nil {
		t.Fatal("SSRF to loopback permitted")
	}
}
