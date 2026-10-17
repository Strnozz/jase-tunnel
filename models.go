package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type model struct {
	Key           string `json:"key"`
	Name          string `json:"name"`
	Engine        string `json:"engine"`
	SizeBytes     int64  `json:"sizeBytes,omitempty"`
	ContextTokens int    `json:"contextTokens,omitempty"`
	Tools         bool   `json:"tools"`
	// Local only: Ollama declares the "thinking" capability. Jase turns thinking off for
	// these and sends no think option to the others.
	Thinking bool `json:"-"`
}

const ollamaURL = "http://127.0.0.1:11434"
const studioURL = "http://127.0.0.1:1234"

var thinking = regexp.MustCompile(`(?s)<think>.*?</think>`)
var engineTransport http.RoundTripper = &http.Transport{Proxy: nil}

func localJSON(ctx context.Context, base, path string, input, output any, maximum int64) error {
	if base != ollamaURL && base != studioURL {
		return errors.New("model endpoint must be loopback")
	}
	var body io.Reader
	method := "GET"
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
		method = "POST"
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if base == studioURL {
		if token := os.Getenv("JASE_LMSTUDIO_TOKEN"); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	client := &http.Client{Transport: engineTransport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("local model HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if err != nil {
		return err
	}
	if int64(len(raw)) > maximum {
		return errors.New("model response too large")
	}
	return json.Unmarshal(raw, output)
}
func (a *application) refreshModels() {
	ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
	defer cancel()
	models := []model{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		found := ollamaModels(ctx)
		mu.Lock()
		models = append(models, found...)
		mu.Unlock()
	}()
	go func() {
		defer wg.Done()
		found := studioModels(ctx)
		mu.Lock()
		models = append(models, found...)
		mu.Unlock()
	}()
	wg.Wait()
	sort.Slice(models, func(i, j int) bool {
		if models[i].Engine != models[j].Engine {
			return models[i].Engine < models[j].Engine
		}
		return models[i].Key < models[j].Key
	})
	files := scanModelFiles(defaultModelDirectories())
	a.mu.Lock()
	a.models = models
	a.files = files
	a.mu.Unlock()
}
func ollamaModels(ctx context.Context) []model {
	var list struct {
		Models []struct {
			Name        string `json:"name"`
			Size        int64  `json:"size"`
			RemoteHost  string `json:"remote_host"`
			RemoteModel string `json:"remote_model"`
		} `json:"models"`
	}
	short, cancel := context.WithTimeout(ctx, 2*time.Second)
	err := localJSON(short, ollamaURL, "/api/tags", nil, &list, 1000000)
	cancel()
	if err != nil {
		return nil
	}
	result := make([]model, 0)
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4)
	for i, item := range list.Models {
		if i >= 100 {
			break
		}
		if item.RemoteHost != "" || item.RemoteModel != "" || strings.Contains(strings.ToLower(item.Name), ":cloud") {
			continue
		}
		wg.Add(1)
		go func(name string, size int64) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-slots }()
			var info struct {
				Capabilities []string       `json:"capabilities"`
				RemoteHost   string         `json:"remote_host"`
				RemoteModel  string         `json:"remote_model"`
				ModelInfo    map[string]any `json:"model_info"`
			}
			call, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
			err := localJSON(call, ollamaURL, "/api/show", map[string]string{"model": name}, &info, 1000000)
			cancel()
			if err != nil || info.RemoteHost != "" || info.RemoteModel != "" {
				return
			}
			tools, thinks := false, false
			for _, capability := range info.Capabilities {
				if capability == "tools" {
					tools = true
				}
				if capability == "thinking" {
					thinks = true
				}
			}
			contextTokens := contextLimit()
			for key, value := range info.ModelInfo {
				if strings.HasSuffix(key, ".context_length") {
					if n, ok := value.(float64); ok && n >= 2048 {
						contextTokens = int(n)
						if limit := contextLimit(); contextTokens > limit {
							contextTokens = limit
						}
					}
				}
			}
			mu.Lock()
			result = append(result, model{Key: "ollama:" + name, Name: name, Engine: "ollama", SizeBytes: size, ContextTokens: contextTokens, Tools: tools && contextTokens >= 16384, Thinking: thinks})
			mu.Unlock()
		}(item.Name, item.Size)
	}
	wg.Wait()
	return result
}
func studioModels(ctx context.Context) []model {
	var list struct {
		Models []struct {
			Type         string `json:"type"`
			Key          string `json:"key"`
			Name         string `json:"display_name"`
			Size         int64  `json:"size_bytes"`
			Context      int    `json:"max_context_length"`
			Capabilities struct {
				Tools bool `json:"trained_for_tool_use"`
			} `json:"capabilities"`
		} `json:"models"`
	}
	short, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if localJSON(short, studioURL, "/api/v1/models", nil, &list, 1000000) != nil {
		return nil
	}
	result := []model{}
	for i, m := range list.Models {
		if i >= 100 {
			break
		}
		if m.Type != "llm" || m.Key == "" {
			continue
		}
		if m.Name == "" {
			m.Name = m.Key
		}
		if limit := contextLimit(); m.Context == 0 || m.Context > limit {
			m.Context = limit
		}
		result = append(result, model{Key: "lmstudio:" + m.Key, Name: m.Name, Engine: "lmstudio", SizeBytes: m.Size, ContextTokens: m.Context, Tools: m.Capabilities.Tools && m.Context >= 16384})
	}
	return result
}

// Chat Completions cannot choose its context length. Reuse a sufficient loaded
// instance, or load this already-installed model locally with the task's budget.
func studioInstance(ctx context.Context, name string, contextTokens int) (string, error) {
	var list struct {
		Models []struct {
			Key       string `json:"key"`
			Instances []struct {
				ID     string `json:"id"`
				Config struct {
					Context int `json:"context_length"`
				} `json:"config"`
			} `json:"loaded_instances"`
		} `json:"models"`
	}
	if err := localJSON(ctx, studioURL, "/api/v1/models", nil, &list, 1000000); err != nil {
		return "", err
	}
	found := false
	for _, m := range list.Models {
		if m.Key != name {
			continue
		}
		found = true
		for _, instance := range m.Instances {
			if instance.ID != "" && instance.Config.Context >= contextTokens {
				return instance.ID, nil
			}
		}
	}
	if !found {
		return "", errors.New("model no longer installed")
	}
	var loaded struct {
		ID     string `json:"instance_id"`
		Status string `json:"status"`
		Config struct {
			Context int `json:"context_length"`
		} `json:"load_config"`
	}
	err := localJSON(ctx, studioURL, "/api/v1/models/load", map[string]any{"model": name, "context_length": contextTokens, "echo_load_config": true}, &loaded, 16000)
	if err != nil {
		return "", err
	}
	if loaded.Status != "loaded" || loaded.ID == "" || loaded.Config.Context < contextTokens {
		return "", errors.New("local model has insufficient context")
	}
	return loaded.ID, nil
}
func (a *application) infer(ctx context.Context, t task) (turn, error) {
	if len(t.Messages) == 0 || len(t.Messages) > 300 || len(t.Tools) > 100 || t.MaxTokens < 1 || t.MaxTokens > 16384 || t.ContextTokens < 2048 || t.ContextTokens > 262144 {
		return turn{}, errors.New("invalid model request")
	}
	a.mu.RLock()
	found, thinks := false, false
	for _, m := range a.models {
		if m.Key == t.Model.Key && m.Engine == t.Model.Engine {
			found, thinks = true, m.Thinking
			break
		}
	}
	a.mu.RUnlock()
	if !found {
		return turn{}, errors.New("model no longer available")
	}
	name := strings.TrimPrefix(t.Model.Key, t.Model.Engine+":")
	if name == t.Model.Key || name == "" {
		return turn{}, errors.New("invalid model key")
	}
	messages := []map[string]any{}
	callIDs := map[string][]string{}
	for messageIndex, m := range t.Messages {
		if m.Role != "system" && m.Role != "user" && m.Role != "assistant" && m.Role != "tool" {
			return turn{}, errors.New("invalid message role")
		}
		entry := map[string]any{"role": m.Role, "content": m.Content}
		if m.Role == "tool" {
			if t.Model.Engine == "ollama" {
				entry["tool_name"] = m.Name
			} else {
				ids := callIDs[m.Name]
				if len(ids) == 0 {
					return turn{}, errors.New("tool result has no matching call")
				}
				entry["tool_call_id"] = ids[0]
				callIDs[m.Name] = ids[1:]
			}
		}
		if len(m.ToolCalls) > 0 {
			calls := []map[string]any{}
			for callIndex, call := range m.ToolCalls {
				id := fmt.Sprintf("call_%d_%d", messageIndex, callIndex)
				callIDs[call.Name] = append(callIDs[call.Name], id)
				arguments := any(call.Arguments)
				if t.Model.Engine == "lmstudio" {
					encoded, _ := json.Marshal(call.Arguments)
					arguments = string(encoded)
				}
				calls = append(calls, map[string]any{"id": id, "type": "function", "function": map[string]any{"name": call.Name, "arguments": arguments}})
			}
			entry["tool_calls"] = calls
		}
		messages = append(messages, entry)
	}
	tools := []map[string]any{}
	for _, definition := range t.Tools {
		tools = append(tools, map[string]any{"type": "function", "function": definition})
	}
	request := map[string]any{"model": name, "messages": messages, "stream": false}
	if len(tools) > 0 {
		request["tools"] = tools
	}
	var raw map[string]any
	if t.Model.Engine == "ollama" {
		if thinks {
			request["think"] = a.think.Load()
		}
		request["keep_alive"] = "5m"
		request["options"] = map[string]any{"temperature": t.Temperature, "num_predict": t.MaxTokens, "num_ctx": t.ContextTokens}
		if t.Schema != nil {
			request["format"] = t.Schema
		}
		var response struct {
			Message         map[string]any `json:"message"`
			PromptEvalCount int            `json:"prompt_eval_count"`
			EvalCount       int            `json:"eval_count"`
			EvalDuration    int64          `json:"eval_duration"`
		}
		a.record("model", "info", "Modello avviato", name+" · Ollama")
		a.debugf("LLM → Ollama %s · contesto %d · max %d token · thinking %s", name, t.ContextTokens, t.MaxTokens, onOff(thinks && a.think.Load(), thinks))
		started := time.Now()
		if err := localJSON(ctx, ollamaURL, "/api/chat", request, &response, 600000); err != nil {
			a.debugf("LLM ✗ %v", err)
			return turn{}, err
		}
		raw = response.Message
		speed := ""
		if response.EvalDuration > 0 {
			speed = fmt.Sprintf(" · %.1f token/s", float64(response.EvalCount)/(float64(response.EvalDuration)/1e9))
		}
		a.debugf("LLM ← %s · prompt %d token · generati %d token%s", time.Since(started).Round(time.Millisecond), response.PromptEvalCount, response.EvalCount, speed)
	} else if t.Model.Engine == "lmstudio" {
		instance, err := studioInstance(ctx, name, t.ContextTokens)
		if err != nil {
			return turn{}, err
		}
		request["model"] = instance
		request["temperature"] = t.Temperature
		request["max_tokens"] = t.MaxTokens
		if t.Schema != nil {
			request["response_format"] = map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "jase_response", "schema": t.Schema}}
		}
		var response struct {
			Choices []struct {
				Message map[string]any `json:"message"`
			} `json:"choices"`
			Usage struct {
				Prompt     int `json:"prompt_tokens"`
				Completion int `json:"completion_tokens"`
			} `json:"usage"`
		}
		a.record("model", "info", "Modello avviato", name+" · LM Studio")
		a.debugf("LLM → LM Studio %s · contesto %d · max %d token", name, t.ContextTokens, t.MaxTokens)
		started := time.Now()
		if err := localJSON(ctx, studioURL, "/v1/chat/completions", request, &response, 600000); err != nil {
			a.debugf("LLM ✗ %v", err)
			return turn{}, err
		}
		a.debugf("LLM ← %s · prompt %d token · generati %d token", time.Since(started).Round(time.Millisecond), response.Usage.Prompt, response.Usage.Completion)
		if len(response.Choices) == 0 {
			return turn{}, errors.New("empty model response")
		}
		raw = response.Choices[0].Message
	} else {
		return turn{}, errors.New("unsupported local engine")
	}
	result := turn{ToolCalls: []toolCall{}}
	a.logThinking(raw)
	if content, ok := raw["content"].(string); ok {
		result.Content = strings.TrimSpace(thinking.ReplaceAllString(content, ""))
	}
	if calls, ok := raw["tool_calls"].([]any); ok {
		for i, value := range calls {
			if i >= 6 {
				break
			}
			call, ok := value.(map[string]any)
			if !ok {
				continue
			}
			fn, ok := call["function"].(map[string]any)
			if !ok {
				continue
			}
			toolName, ok := fn["name"].(string)
			if !ok {
				continue
			}
			args := fn["arguments"]
			if encoded, ok := args.(string); ok {
				if json.Unmarshal([]byte(encoded), &args) != nil {
					continue
				}
			}
			arguments, ok := args.(map[string]any)
			if !ok {
				continue
			}
			result.ToolCalls = append(result.ToolCalls, toolCall{Name: toolName, Arguments: arguments})
		}
	}
	a.logAnswer(result)
	if result.Content == "" && len(result.ToolCalls) == 0 {
		return turn{}, errors.New("empty model response")
	}
	return result, nil
}

func ollamaExecutable() string {
	if path, err := exec.LookPath("ollama"); err == nil {
		return path
	}
	home, _ := os.UserHomeDir()
	candidates := []string{"/Applications/Ollama.app/Contents/Resources/ollama", "/usr/local/bin/ollama", "/opt/homebrew/bin/ollama", filepath.Join(home, "AppData", "Local", "Programs", "Ollama", "ollama.exe")}
	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}
func (a *application) importModel(ctx context.Context, folder bool) error {
	file, err := pickModel(ctx, folder)
	if err != nil {
		return err
	}
	if file == "" {
		return errors.New("Selezione annullata.")
	}
	return a.importModelAt(ctx, file, folder)
}
func (a *application) importModelAt(ctx context.Context, file string, folder bool) error {
	info, err := os.Stat(file)
	if err != nil {
		return errors.New("Modello non accessibile.")
	}
	if !folder && (info.IsDir() || !strings.EqualFold(filepath.Ext(file), ".gguf")) {
		return errors.New("Scegli un file GGUF o una cartella Safetensors compatibile.")
	}
	if folder {
		if !info.IsDir() {
			return errors.New("Scegli una cartella di modello.")
		}
		if _, err = os.Stat(filepath.Join(file, "config.json")); err != nil {
			return errors.New("La cartella deve contenere il modello completo e config.json.")
		}
	}
	engine := ollamaExecutable()
	if engine == "" {
		return errors.New("Per aggiungere questo file serve Ollama installato sul computer. I modelli di LM Studio vengono rilevati dal suo servizio locale.")
	}
	modelfile := filepath.Join(a.stateDir, "import.Modelfile")
	// Forward slashes work on Windows too and need no escaping inside the quoted value
	// (strconv.Quote would double every backslash of C:\Users\…).
	if err = os.WriteFile(modelfile, []byte("FROM "+strconv.Quote(filepath.ToSlash(file))+"\n"), 0600); err != nil {
		return errors.New("Impossibile preparare l’importazione.")
	}
	defer os.Remove(modelfile)
	name := "jase-local-" + randomSecret()[:12]
	command := exec.CommandContext(ctx, engine, "create", name, "-f", modelfile)
	hideChildConsole(command)
	command.Env = append(os.Environ(), "OLLAMA_HOST=127.0.0.1:11434", "OLLAMA_NO_CLOUD=1")
	if err = command.Run(); err != nil {
		return errors.New("Importazione non riuscita. Verifica che Ollama sia aperto e che supporti questo modello.")
	}
	return nil
}
