package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestIndependentSDKDesktopRoundTrip(t *testing.T) {
	if os.Getenv("JASE_SDK_INTEGRATION") != "true" {
		t.Skip("requires built packages/sdk and Node.js")
	}
	cmd := exec.Command("node", "packages/sdk/test/fixture-service.mjs")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	scanner := bufio.NewScanner(pipe)
	if !scanner.Scan() {
		t.Fatal("fixture server did not start")
	}
	origin := scanner.Text()
	if !strings.HasPrefix(origin, "http://127.0.0.1:") {
		t.Fatal("unexpected fixture origin")
	}
	oldDev := devBuild
	devBuild = "true"
	defer func() { devBuild = oldDev }()
	a := newTestApp(t)
	defer a.cancel()
	a.models = []model{{Key: "ollama:fixture", Name: "Fixture local model", Engine: "ollama", Tools: true, ContextTokens: 8192}}
	var turns atomic.Int32
	oldEngine := engineTransport
	engineTransport = roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "127.0.0.1:11434" || r.URL.Path != "/api/chat" {
			t.Error("unexpected inference endpoint")
		}
		if turns.Add(1) == 1 {
			return response(`{"message":{"content":"","tool_calls":[{"function":{"name":"opening_hours","arguments":{"day":"today"}}}]}}`), nil
		}
		return response(`{"message":{"content":"Apre alle 09:00."}}`), nil
	})
	defer func() { engineTransport = oldEngine }()
	client := &http.Client{Timeout: 5 * time.Second}
	post := func(path string, input any, cookie string) (*http.Response, map[string]any) {
		raw, _ := json.Marshal(input)
		r, _ := http.NewRequest("POST", origin+"/api/jase-tunnel/v1/"+path, bytes.NewReader(raw))
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		if cookie != "" {
			r.Header.Set("Cookie", cookie)
		}
		res, err := client.Do(r)
		if err != nil {
			t.Error(err)
			return nil, nil
		}
		defer res.Body.Close()
		var out map[string]any
		if err = json.NewDecoder(res.Body).Decode(&out); err != nil {
			t.Error(err)
		}
		return res, out
	}
	discovery := webRequest(a, "discover", origin, "", map[string]any{"protocol": webProtocol})
	if discovery.Code != 202 {
		t.Fatal(discovery.Body.String())
	}
	var state map[string]any
	_ = json.Unmarshal(discovery.Body.Bytes(), &state)
	receipt := state["receipt"].(string)
	waitWebState(t, a, origin, receipt, "compatible")
	res, ticket := post("tickets", map[string]any{"protocol": webProtocol}, "")
	if res == nil || res.StatusCode != 200 {
		t.Fatal(ticket)
	}
	cookie := strings.Split(res.Header.Get("Set-Cookie"), ";")[0]
	pair := webRequest(a, "pair", origin, receipt, map[string]any{"protocol": webProtocol, "ticket": ticket["ticket"]})
	if pair.Code != 202 {
		t.Fatal(pair.Body.String())
	}
	approval := a.claimWebApproval()
	if approval == nil {
		t.Fatal("missing consent")
	}
	a.answerWebConsent(approval.ID, approval.Models[0].Key)
	waitWebState(t, a, origin, receipt, "connected")
	request, _ := http.NewRequest("GET", origin+"/api/jase-tunnel/v1/session", nil)
	request.Header.Set("Cookie", cookie)
	session, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var data struct {
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	}
	_ = json.NewDecoder(session.Body).Decode(&data)
	_ = session.Body.Close()
	if len(data.Models) != 1 {
		t.Fatal("session model missing")
	}
	res, answer := post("chat", map[string]any{"model": data.Models[0].ID, "message": "A che ora apre oggi il negozio?"}, cookie)
	if res == nil || res.StatusCode != 200 || answer["content"] != "Apre alle 09:00." {
		t.Fatalf("tool round trip failed: %v", answer)
	}
	if turns.Load() != 2 {
		t.Fatalf("wanted two LLM turns, got %d", turns.Load())
	}
	stateRes, err := client.Get(origin + "/fixture-state")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(stateRes.Body)
	_ = stateRes.Body.Close()
	if !bytes.Contains(raw, []byte(`"executions":1`)) {
		t.Fatal("tool did not execute exactly once", string(raw))
	}
	a.revokeLinks()
	stopTestApp(t, a)
}
