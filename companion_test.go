package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (fn roundTrip) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }
func response(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}
func newTestApp(t *testing.T) *application {
	ctx, cancel := context.WithCancel(context.Background())
	a := &application{stateDir: t.TempDir(), port: 11439, ctx: ctx, cancel: cancel, running: map[string]bool{}, inference: make(chan struct{}, 1)}
	t.Cleanup(func() { stopTestApp(t, a) })
	return a
}
func stopTestApp(t *testing.T, a *application) {
	t.Helper()
	a.cancel()
	deadline := time.Now().Add(2 * time.Second)
	for {
		a.mu.RLock()
		workers := len(a.running)
		a.mu.RUnlock()
		if workers == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Error("test application polling workers did not stop")
			return
		}
		time.Sleep(time.Millisecond)
	}
}

// storedGrant saves an unexpired consent for m, as answerWebConsent would.
func storedGrant(a *application, m model) link {
	c := link{Protocol: webProtocol, Origin: "https://shop.example.com", Broker: "/api/jase-tunnel/v1/", Model: m, ExpiresAt: time.Now().Add(10 * time.Minute).UnixMilli(), Token: strings.Repeat("b", 64), ConnectionID: strings.Repeat("c", 32)}
	a.mu.Lock()
	a.settings.Links = append(a.settings.Links, c)
	a.mu.Unlock()
	return c
}
func localRequest(method, path, body, origin string) *http.Request {
	r := httptest.NewRequest(method, "http://127.0.0.1:11439"+path, strings.NewReader(body))
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if method == "POST" {
		r.Header.Set("Content-Type", "application/json")
	}
	return r
}

func TestLoopbackOriginAndRebindingProtection(t *testing.T) {
	a := newTestApp(t)
	// Websites reach the tunnel only through /tunnel/v1/: the management API refuses them.
	for _, origin := range []string{"https://evil.test", "null", "https://shop.example.com"} {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, localRequest("GET", "/v1/status", "", origin))
		if w.Code != 403 || w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("management API answered %s", origin)
		}
	}
	w := httptest.NewRecorder()
	r := localRequest("GET", "/v1/status", "", "")
	r.Host = "evil.test:11439"
	a.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("accepted rebinding Host")
	}
	w = httptest.NewRecorder()
	a.ServeHTTP(w, localRequest("GET", "/v1/status", "", ""))
	if w.Code != 200 || !strings.Contains(w.Body.String(), webProtocol) {
		t.Fatal("a local process cannot identify the running tunnel")
	}
	// The status page works under both loopback names.
	for _, local := range []string{"http://127.0.0.1:11439", "http://localhost:11439"} {
		w = httptest.NewRecorder()
		r := localRequest("POST", "/v1/show-window", "{}", local)
		r.Host = strings.TrimPrefix(local, "http://")
		a.ServeHTTP(w, r)
		if w.Code != 404 {
			t.Fatalf("status page at %s rejected: HTTP %d", local, w.Code)
		}
	}
	w = httptest.NewRecorder()
	a.ServeHTTP(w, localRequest("POST", "/v1/quit", "{}", ""))
	if w.Code != 403 {
		t.Fatal("accepted a CSRF request")
	}
}

// The single-site pairing and import endpoints of releases before 0.3 no longer exist.
func TestPreviousSiteEndpointsAreGone(t *testing.T) {
	a := newTestApp(t)
	for _, path := range []string{"/v1/pair", "/v1/open-jase", "/v1/import-model"} {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, localRequest("POST", path, `{}`, "http://127.0.0.1:11439"))
		if w.Code != 404 {
			t.Fatalf("%s still answers: HTTP %d", path, w.Code)
		}
		w = httptest.NewRecorder()
		a.ServeHTTP(w, localRequest("POST", path, `{}`, "https://shop.example.com"))
		if w.Code != 403 {
			t.Fatalf("%s accepted a website", path)
		}
	}
}
func TestRemoteCallsReachOnlyAStoredGrantsBroker(t *testing.T) {
	a := newTestApp(t)
	calls := 0
	mockWeb(t, a, func(*http.Request) (*http.Response, error) { calls++; return response(`{"task":null}`), nil })
	m := model{Key: "ollama:qwen", Name: "qwen", Engine: "ollama", ContextTokens: 16384, Tools: true}
	unsaved := link{Protocol: webProtocol, Origin: "https://shop.example.com", Broker: "/api/jase-tunnel/v1/", Model: m, ExpiresAt: time.Now().Add(time.Minute).UnixMilli(), Token: strings.Repeat("e", 64)}
	for _, c := range []link{{Origin: "https://evil.test"}, {Protocol: webProtocol, Origin: "https://shop.example.com", Broker: "//evil.test/", Model: m, ExpiresAt: unsaved.ExpiresAt, Token: unsaved.Token}, unsaved} {
		if err := a.remote(a.ctx, c, "poll", nil, nil); err == nil {
			t.Fatalf("remote call allowed for %+v", c)
		}
	}
	if calls != 0 {
		t.Fatal("request left the computer without a stored grant")
	}
}
func TestLongPollReconnectsAfterEachResponse(t *testing.T) {
	a := newTestApp(t)
	mockWeb(t, a, func(r *http.Request) (*http.Response, error) {
		if !r.Close || r.URL.String() != "https://shop.example.com/api/jase-tunnel/v1/poll" {
			t.Fatal("long poll retained a connection to a retiring server or left the broker")
		}
		return response(`{"task":null}`), nil
	})
	c := storedGrant(a, model{Key: "ollama:qwen", Name: "qwen", Engine: "ollama", ContextTokens: 16384, Tools: true})
	var output any
	if err := a.remote(a.ctx, c, "poll", nil, &output); err != nil {
		t.Fatal(err)
	}
}

// Connections saved by releases before 0.3 named a single site: a restart discards
// them with their credentials and keeps only unexpired grants, in a private file.
func TestStartupKeepsOnlyCurrentGrants(t *testing.T) {
	a := newTestApp(t)
	grant := storedGrant(a, model{Key: "ollama:qwen", Name: "qwen", Engine: "ollama", ContextTokens: 16384, Tools: true})
	a.mu.Lock()
	a.settings.Links = append(a.settings.Links, link{Origin: "https://previous.example.com", Token: strings.Repeat("d", 64), ConnectionID: strings.Repeat("f", 32)})
	a.mu.Unlock()
	a.dropInvalidLinks()
	bytes, err := os.ReadFile(filepath.Join(a.stateDir, "connections.json"))
	if err != nil || !strings.Contains(string(bytes), grant.Token) || strings.Contains(string(bytes), strings.Repeat("d", 64)) {
		t.Fatal("previous connection survived or grant lost")
	}
	info, _ := os.Stat(filepath.Join(a.stateDir, "connections.json"))
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal("private file permissions")
	}
}

func TestOneFailedStatusCheckDoesNotCancelInference(t *testing.T) {
	a := newTestApp(t)
	m := model{Key: "ollama:qwen", Name: "qwen", Engine: "ollama", ContextTokens: 16384, Tools: true}
	a.models = []model{m}
	every := statusEvery
	t.Cleanup(func() { statusEvery = every })
	statusEvery = 20 * time.Millisecond
	var checks atomic.Int32
	mockWeb(t, a, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/active") {
			if checks.Add(1) == 1 {
				return nil, io.ErrUnexpectedEOF // a network blip
			}
			return response(`{"active":true}`), nil
		}
		return response(`{"ok":true}`), nil
	})
	previous := engineTransport
	t.Cleanup(func() { engineTransport = previous })
	var answered atomic.Bool
	engineTransport = roundTrip(func(r *http.Request) (*http.Response, error) {
		select {
		case <-time.After(200 * time.Millisecond):
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		answered.Store(true)
		return response(`{"message":{"content":"done"}}`), nil
	})
	a.handleTask(storedGrant(a, m), task{ID: "t", Model: m, Messages: []message{{Role: "user", Content: "hi"}}, MaxTokens: 10, ContextTokens: 16384, ExpiresAt: time.Now().Add(time.Minute).UnixMilli()})
	if !answered.Load() || checks.Load() < 2 {
		t.Fatalf("inference cancelled after one failed check (checks %d)", checks.Load())
	}
}

func TestLogMarksEachQueryAndShowsThinkingOnlyInDebug(t *testing.T) {
	var output strings.Builder
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	previous := engineTransport
	t.Cleanup(func() { engineTransport = previous })
	engineTransport = roundTrip(func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["think"] != true {
			t.Errorf("--think not applied: %v", body["think"])
		}
		return response(`{"message":{"thinking":"reasoning about the fixture","content":"final fixture answer"},"prompt_eval_count":12,"eval_count":5,"eval_duration":1000000000}`), nil
	})
	run := func(debug bool) string {
		output.Reset()
		a := newTestApp(t)
		a.debug.Store(debug)
		a.think.Store(true)
		m := model{Key: "ollama:qwen", Name: "qwen", Engine: "ollama", ContextTokens: 16384, Tools: true, Thinking: true}
		a.models = []model{m}
		mockWeb(t, a, func(r *http.Request) (*http.Response, error) { return response(`{"ok":true,"active":true}`), nil })
		a.handleTask(storedGrant(a, m), task{ID: strings.Repeat("9", 64),
			Model: m, Messages: []message{{Role: "user", Content: "private fixture question"}}, MaxTokens: 10, ContextTokens: 16384, ExpiresAt: time.Now().Add(time.Minute).UnixMilli()})
		return output.String()
	}
	quiet := run(false)
	for _, want := range []string{"===== Start query · richiesta 99999999 =====", "===== End reply · richiesta 99999999 · risposta inviata"} {
		if !strings.Contains(quiet, want) {
			t.Fatalf("missing %q in\n%s", want, quiet)
		}
	}
	if strings.Contains(quiet, "private fixture question") || strings.Contains(quiet, "reasoning about the fixture") {
		t.Fatal("conversation text logged without --debug")
	}
	full := run(true)
	for _, want := range []string{"private fixture question", "--- thinking ---\nreasoning about the fixture", "--- risposta ---\nfinal fixture answer", "→ sito POST https://shop.example.com/api/jase-tunnel/v1/result", "5.0 token/s"} {
		if !strings.Contains(full, want) {
			t.Fatalf("missing %q in\n%s", want, full)
		}
	}
	if strings.Index(full, "Start query") > strings.Index(full, "--- thinking ---") || strings.Index(full, "--- risposta ---") > strings.Index(full, "End reply") {
		t.Fatal("markers do not enclose the query")
	}
}
