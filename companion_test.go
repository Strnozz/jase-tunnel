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
	a := &application{stateDir: t.TempDir(), port: 11439, ctx: ctx, cancel: cancel, client: &http.Client{}, running: map[string]bool{}, inference: make(chan struct{}, 1), paired: make(chan struct{})}
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
	for _, origin := range []string{"https://evil.test", "null", "https://jaseengine.it.evil.test"} {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, localRequest("GET", "/v1/status", "", origin))
		if w.Code != 403 {
			t.Fatalf("accepted hostile origin %s", origin)
		}
	}
	w := httptest.NewRecorder()
	r := localRequest("GET", "/v1/status", "", "https://jaseengine.it")
	r.Host = "evil.test:11439"
	a.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("accepted rebinding Host")
	}
	w = httptest.NewRecorder()
	a.ServeHTTP(w, localRequest("GET", "/v1/status", "", "https://jaseengine.it"))
	if w.Code != 200 || w.Header().Get("Access-Control-Allow-Origin") != "https://jaseengine.it" {
		t.Fatal("Jase cannot detect local helper")
	}
	w = httptest.NewRecorder()
	a.ServeHTTP(w, localRequest("POST", "/v1/quit", "{}", ""))
	if w.Code != 403 {
		t.Fatal("accepted a CSRF request")
	}
}
func TestOriginIsRestrictedToJaseAndLoopback(t *testing.T) {
	// A release accepts only Jase; loopback sites only in a development build.
	for _, origin := range []string{"http://localhost:3117", "http://127.0.0.1:3117"} {
		if allowedOrigin(origin) {
			t.Fatalf("release build accepted %s", origin)
		}
	}
	devBuild = "true"
	t.Cleanup(func() { devBuild = "false" })
	for _, origin := range []string{"https://jaseengine.it", "http://localhost:3117", "http://127.0.0.1:3117"} {
		if !allowedOrigin(origin) {
			t.Fatalf("rejected %s", origin)
		}
	}
	for _, origin := range []string{"https://evil.test", "http://jaseengine.it", "https://user@jaseengine.it", "https://jaseengine.it/path", "https://jaseengine.it?token=1", "http://192.168.1.1:80", "http://localhost:3117/"} {
		if allowedOrigin(origin) {
			t.Fatalf("accepted %s", origin)
		}
	}
}
func TestPairingCannotBeSentToAnOutsideService(t *testing.T) {
	a := newTestApp(t)
	w := httptest.NewRecorder()
	a.ServeHTTP(w, localRequest("POST", "/v1/pair", `{"origin":"https://evil.test","ticket":"`+strings.Repeat("a", 64)+`"}`, "https://jaseengine.it"))
	if w.Code != 400 {
		t.Fatal("accepted remote pairing destination")
	}
	if err := a.remote(a.ctx, link{Origin: "https://evil.test"}, "poll", nil, nil); err == nil {
		t.Fatal("external service allowed")
	}
}
func TestLongPollReconnectsAfterEachResponse(t *testing.T) {
	a := newTestApp(t)
	a.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		if !r.Close {
			t.Fatal("long poll retained a connection to a retiring server")
		}
		return response(`{"task":null}`), nil
	})
	var output any
	if err := a.remote(a.ctx, link{Origin: "https://jaseengine.it"}, "poll", nil, &output); err != nil {
		t.Fatal(err)
	}
}
func TestPairingStoresOnlyDeviceCredentialsLocally(t *testing.T) {
	a := newTestApp(t)
	a.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "jaseengine.it" {
			t.Errorf("unexpected remote service %s", r.URL)
		}
		if r.URL.Path == "/api/companion/pair" {
			return response(`{"protocol":"jase-companion-v1","token":"` + strings.Repeat("b", 64) + `","connectionId":"` + strings.Repeat("c", 32) + `"}`), nil
		}
		select {
		case <-time.After(20 * time.Millisecond):
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		return response(`{"task":null}`), nil
	})
	w := httptest.NewRecorder()
	a.ServeHTTP(w, localRequest("POST", "/v1/pair", `{"origin":"https://jaseengine.it","ticket":"`+strings.Repeat("a", 64)+`"}`, "https://jaseengine.it"))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	bytes, err := os.ReadFile(filepath.Join(a.stateDir, "connections.json"))
	if err != nil || !strings.Contains(string(bytes), strings.Repeat("b", 64)) {
		t.Fatal("missing private reconnect credential")
	}
	if strings.Contains(w.Body.String(), strings.Repeat("b", 64)) {
		t.Fatal("device credential exposed to browser")
	}
	status := httptest.NewRecorder()
	a.ServeHTTP(status, localRequest("GET", "/v1/status", "", "https://jaseengine.it"))
	if !strings.Contains(status.Body.String(), strings.Repeat("c", 32)) || strings.Contains(status.Body.String(), strings.Repeat("b", 64)) {
		t.Fatal("reconnect discovery must expose only public connection identifiers")
	}
	info, _ := os.Stat(filepath.Join(a.stateDir, "connections.json"))
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal("private file permissions")
	}
}
func TestOllamaDiscoveryExcludesCloudAndEmbeddingOnlyModels(t *testing.T) {
	previous, limit := engineTransport, contextLimit
	t.Cleanup(func() { engineTransport, contextLimit = previous, limit })
	contextLimit = func() int { return 32768 }
	engineTransport = roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "127.0.0.1:11434" {
			t.Fatalf("nonlocal model service %s", r.URL)
		}
		if r.URL.Path == "/api/tags" {
			return response(`{"models":[{"name":"local-qwen","size":123},{"name":"remote:cloud","remote_host":"https://ollama.com"}]}`), nil
		}
		return response(`{"capabilities":["completion","tools"],"model_info":{"qwen.context_length":65536}}`), nil
	})
	models := ollamaModels(context.Background())
	if len(models) != 1 || models[0].Key != "ollama:local-qwen" || !models[0].Tools || models[0].ContextTokens != 32768 {
		t.Fatalf("bad discovery: %+v", models)
	}
}
func TestOllamaToolCallAndStructuredOutput(t *testing.T) {
	a := newTestApp(t)
	m := model{Key: "ollama:qwen", Name: "qwen", Engine: "ollama", Tools: true}
	a.models = []model{m}
	previous := engineTransport
	t.Cleanup(func() { engineTransport = previous })
	engineTransport = roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != ollamaURL+"/api/chat" {
			t.Fatal("wrong local endpoint")
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "qwen" || body["stream"] != false || body["format"] == nil {
			t.Fatal("missing local model or schema")
		}
		return response(`{"message":{"content":"<think>private reasoning</think>","tool_calls":[{"function":{"name":"web_search","arguments":{"query":"fixture"}}}]}}`), nil
	})
	answer, err := a.infer(a.ctx, task{Model: m, Messages: []message{{Role: "user", Content: "test"}}, MaxTokens: 100, ContextTokens: 8192, Schema: map[string]any{"type": "object"}})
	if err != nil || len(answer.ToolCalls) != 1 || answer.ToolCalls[0].Name != "web_search" || answer.Content != "" {
		t.Fatalf("bad local result: %+v %v", answer, err)
	}
}
func TestLMStudioKeepsDistinctToolCallIdentifiers(t *testing.T) {
	a := newTestApp(t)
	m := model{Key: "lmstudio:qwen", Name: "qwen", Engine: "lmstudio", Tools: true}
	a.models = []model{m}
	previous := engineTransport
	t.Cleanup(func() { engineTransport = previous })
	engineTransport = roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v1/models" {
			return response(`{"models":[{"key":"qwen","loaded_instances":[{"id":"qwen-loaded","config":{"context_length":32768}}]}]}`), nil
		}
		if r.URL.String() != studioURL+"/v1/chat/completions" {
			t.Fatal("wrong local endpoint")
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "qwen-loaded" {
			t.Fatal("chat did not select the prepared instance")
		}
		messages := body["messages"].([]any)
		calls := messages[1].(map[string]any)["tool_calls"].([]any)
		first := calls[0].(map[string]any)["id"]
		second := calls[1].(map[string]any)["id"]
		if first == second || messages[2].(map[string]any)["tool_call_id"] != first || messages[3].(map[string]any)["tool_call_id"] != second {
			t.Fatal("tool result attached to wrong call")
		}
		return response(`{"choices":[{"message":{"content":"done"}}]}`), nil
	})
	_, err := a.infer(a.ctx, task{Model: m, Messages: []message{{Role: "user", Content: "test"}, {Role: "assistant", ToolCalls: []toolCall{{Name: "web_search", Arguments: map[string]any{}}, {Name: "web_search", Arguments: map[string]any{}}}}, {Role: "tool", Name: "web_search", Content: "first"}, {Role: "tool", Name: "web_search", Content: "second"}}, MaxTokens: 100, ContextTokens: 8192})
	if err != nil {
		t.Fatal(err)
	}
}
func TestLMStudioLoadsInstalledModelWithEnoughContext(t *testing.T) {
	previous := engineTransport
	t.Cleanup(func() { engineTransport = previous })
	loads := 0
	engineTransport = roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "127.0.0.1:1234" {
			t.Fatal("model load left the computer")
		}
		if r.URL.Path == "/api/v1/models" {
			return response(`{"models":[{"key":"qwen","loaded_instances":[{"id":"too-small","config":{"context_length":4096}}]}]}`), nil
		}
		if r.URL.Path != "/api/v1/models/load" {
			t.Fatal("unexpected model endpoint")
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "qwen" || body["context_length"] != float64(32768) || body["echo_load_config"] != true {
			t.Fatal("model loaded with the wrong context")
		}
		loads++
		return response(`{"status":"loaded","instance_id":"qwen-ready","load_config":{"context_length":32768}}`), nil
	})
	id, err := studioInstance(context.Background(), "qwen", 32768)
	if err != nil || id != "qwen-ready" || loads != 1 {
		t.Fatalf("model preparation failed: %s %v", id, err)
	}
	if _, err := studioInstance(context.Background(), "uninstalled", 32768); err == nil || loads != 1 {
		t.Fatal("attempted to load an uninstalled model")
	}
}
func TestLocalModelRequestsNeverFollowRedirects(t *testing.T) {
	previous := engineTransport
	t.Cleanup(func() { engineTransport = previous })
	calls := 0
	engineTransport = roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://evil.test"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	var output any
	if localJSON(context.Background(), ollamaURL, "/api/chat", map[string]any{}, &output, 1000) == nil || calls != 1 {
		t.Fatal("redirect followed")
	}
	if localJSON(context.Background(), "https://evil.test", "/api/chat", nil, &output, 1000) == nil {
		t.Fatal("external model endpoint allowed")
	}
}
func TestDefaultDirectoryScanKeepsPathsLocalAndDoesNotTraverseSymlinkDirectories(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "qwen.gguf"), []byte("fixture"), 0600)
	_ = os.WriteFile(filepath.Join(root, "mmproj.gguf"), []byte("not a chat model"), 0600)
	_ = os.Mkdir(filepath.Join(root, "safetensors"), 0700)
	_ = os.WriteFile(filepath.Join(root, "safetensors", "config.json"), []byte("{}"), 0600)
	_ = os.WriteFile(filepath.Join(root, "safetensors", "model.safetensors"), []byte("fixture"), 0600)
	_ = os.WriteFile(filepath.Join(outside, "private.gguf"), []byte("outside"), 0600)
	if err := os.Symlink(outside, filepath.Join(root, "other")); err != nil {
		t.Skip("symlinks unavailable")
	}
	files := scanModelFiles([]string{root})
	if len(files) != 2 {
		t.Fatalf("unexpected model files: %+v", files)
	}
	encoded, _ := json.Marshal(files)
	if strings.Contains(string(encoded), root) || strings.Contains(string(encoded), outside) || strings.Contains(string(encoded), "private.gguf") {
		t.Fatal("paths or outside files exposed")
	}
}

func TestContextFollowsMemoryAndThinkingIsOffOnlyWhereDeclared(t *testing.T) {
	previous, limit := engineTransport, contextLimit
	t.Cleanup(func() { engineTransport, contextLimit = previous, limit })
	contextLimit = func() int { return 16384 }
	engineTransport = roundTrip(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.Path == "/api/tags":
			return response(`{"models":[{"name":"thinker"},{"name":"plain"}]}`), nil
		case r.URL.Path == "/api/show":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["model"] == "thinker" {
				return response(`{"capabilities":["completion","tools","thinking"],"model_info":{"x.context_length":131072}}`), nil
			}
			return response(`{"capabilities":["completion","tools"],"model_info":{"x.context_length":131072}}`), nil
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, think := body["think"]
		if think != (body["model"] == "thinker") {
			t.Errorf("think option for %v: %v", body["model"], think)
		}
		return response(`{"message":{"content":"ok"}}`), nil
	})
	a := newTestApp(t)
	a.models = ollamaModels(context.Background())
	if len(a.models) != 2 {
		t.Fatalf("models: %+v", a.models)
	}
	for _, m := range a.models {
		if m.ContextTokens != 16384 || !m.Tools {
			t.Fatalf("context not capped to memory: %+v", m)
		}
		if _, err := a.infer(a.ctx, task{Model: m, Messages: []message{{Role: "user", Content: "hi"}}, MaxTokens: 10, ContextTokens: 16384}); err != nil {
			t.Fatal(err)
		}
	}
	encoded, _ := json.Marshal(a.models[0])
	if strings.Contains(string(encoded), "hinking") {
		t.Fatal("local capability sent to Jase")
	}
}

func TestOneFailedStatusCheckDoesNotCancelInference(t *testing.T) {
	a := newTestApp(t)
	m := model{Key: "ollama:qwen", Name: "qwen", Engine: "ollama", Tools: true}
	a.models = []model{m}
	every := statusEvery
	t.Cleanup(func() { statusEvery = every })
	statusEvery = 20 * time.Millisecond
	var checks atomic.Int32
	a.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
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
	a.handleTask(link{Origin: "https://jaseengine.it", Token: strings.Repeat("b", 64)}, task{ID: "t", Model: m, Messages: []message{{Role: "user", Content: "hi"}}, MaxTokens: 10, ContextTokens: 16384, ExpiresAt: time.Now().Add(time.Minute).UnixMilli()})
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
		m := model{Key: "ollama:qwen", Name: "qwen", Engine: "ollama", Tools: true, Thinking: true}
		a.models = []model{m}
		a.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) { return response(`{"ok":true,"active":true}`), nil })
		a.handleTask(link{Origin: "https://jaseengine.it", Token: strings.Repeat("b", 64)}, task{ID: strings.Repeat("9", 64), Trace: &trace{Kind: "goal", Ref: "1a2b3c4d", Turn: 3},
			Model: m, Messages: []message{{Role: "user", Content: "private fixture question"}}, MaxTokens: 10, ContextTokens: 16384, ExpiresAt: time.Now().Add(time.Minute).UnixMilli()})
		return output.String()
	}
	quiet := run(false)
	for _, want := range []string{"===== Start query · Goal 1a2b3c4d · turno 3 · richiesta 99999999 =====", "===== End reply · Goal 1a2b3c4d · turno 3 · richiesta 99999999 · risposta inviata"} {
		if !strings.Contains(quiet, want) {
			t.Fatalf("missing %q in\n%s", want, quiet)
		}
	}
	if strings.Contains(quiet, "private fixture question") || strings.Contains(quiet, "reasoning about the fixture") {
		t.Fatal("conversation text logged without --debug")
	}
	full := run(true)
	for _, want := range []string{"private fixture question", "--- thinking ---\nreasoning about the fixture", "--- risposta ---\nfinal fixture answer", "→ Jase POST /api/companion/result", "5.0 token/s"} {
		if !strings.Contains(full, want) {
			t.Fatalf("missing %q in\n%s", want, full)
		}
	}
	if strings.Index(full, "Start query") > strings.Index(full, "--- thinking ---") || strings.Index(full, "--- risposta ---") > strings.Index(full, "End reply") {
		t.Fatal("markers do not enclose the query")
	}
}
