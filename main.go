// Jase's desktop companion. No cloud SDK or outside relay.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const protocol = "jase-companion-v1"

var version = "dev"

// Loopback site origins (http://localhost:3000 …) are for development builds only
// (`npm run companion:build -- --dev` sets this). A release accepts only https://jaseengine.it:
// otherwise any local web page could pair the app to a server of its own and use the models.
var devBuild = "false"

type link struct {
	Origin       string `json:"origin"`
	Token        string `json:"token"`
	ConnectionID string `json:"connectionId"`
}
type settings struct {
	Links   []link `json:"links"`
	Browser string `json:"browser,omitempty"`
}
type application struct {
	mu        sync.RWMutex
	settings  settings
	models    []model
	files     []modelFile
	stateDir  string
	port      int
	client    *http.Client
	ctx       context.Context
	cancel    context.CancelFunc
	running   map[string]bool
	inference chan struct{}
	paired    chan struct{}
	pairOnce  sync.Once
	importMu  sync.Mutex
	// --debug: full log (prompts, thinking, replies, every call to Jase); --think: let
	// models that can reason do it; site: the page the app opens.
	debug    atomic.Bool
	think    atomic.Bool
	site     string
	activity activityLog
	nativeUI bool
}

func main() {
	foreground := flag.Bool("foreground", false, "Run attached to the current terminal (diagnostics)")
	background := flag.Bool("background", false, "Internal background process")
	state := flag.String("state-dir", "", "Private application state directory")
	port := flag.Int("port", 11439, "Loopback service port")
	noBrowser := flag.Bool("no-browser", false, "Do not show a browser picker")
	debug := flag.Bool("debug", false, "Log prompts, model thinking, replies and every call to Jase (local diagnostics)")
	think := flag.Bool("think", false, "Let models that declare the thinking capability reason before answering")
	dev := flag.Bool("dev", false, "Also accept http://localhost sites (local development of Jase)")
	site := flag.String("site", "https://jaseengine.it", "Jase site opened by the app")
	headless := flag.Bool("headless", false, "Run without the desktop interface (explicit terminal diagnostics only)")
	flag.Parse()
	if runtime.GOOS == "darwin" && !*headless && !desktopAvailable {
		log.Print("The macOS app requires AppKit: build on macOS with CGO_ENABLED=1, or use --headless for diagnostics")
		return
	}
	if desktopAvailable && !*headless {
		if err := prepareDesktopUI(); err != nil {
			desktopStartupError(err.Error())
			return
		}
	}
	if *dev {
		devBuild = "true"
	}
	if devBuild == "true" {
		explicitSite := false
		flag.Visit(func(f *flag.Flag) { explicitSite = explicitSite || f.Name == "site" })
		if !explicitSite {
			*site = "http://localhost:3000"
		}
	}
	if !allowedOrigin(*site) {
		log.Print("Invalid --site (use https://jaseengine.it, or a localhost address with --dev)")
		return
	}
	if *state == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return
		}
		*state = filepath.Join(base, "Jase")
	}
	if err := os.MkdirAll(*state, 0700); err != nil {
		return
	}
	if !*foreground && !*background && !(desktopAvailable && !*headless) {
		executable, err := os.Executable()
		if err != nil {
			return
		}
		file, err := os.OpenFile(filepath.Join(*state, "companion.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return
		}
		defer file.Close()
		args := []string{"--background", "--state-dir", *state, "--port", fmt.Sprint(*port)}
		if *noBrowser {
			args = append(args, "--no-browser")
		}
		if *headless {
			args = append(args, "--headless")
		}
		for name, on := range map[string]bool{"--debug": *debug, "--think": *think, "--dev": *dev} {
			if on {
				args = append(args, name)
			}
		}
		args = append(args, "--site", *site)
		command := exec.Command(executable, args...)
		command.Stdout = file
		command.Stderr = file
		detach(command)
		if err := command.Start(); err != nil {
			log.Print("Unable to start Jase")
		}
		return
	}
	if !*foreground {
		file, err := os.OpenFile(filepath.Join(*state, "companion.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return
		}
		defer file.Close()
		log.SetOutput(file)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	app := &application{stateDir: *state, port: *port, ctx: ctx, cancel: cancel, running: map[string]bool{}, inference: make(chan struct{}, 1), paired: make(chan struct{}), site: *site, nativeUI: desktopAvailable && !*headless}
	app.debug.Store(*debug)
	app.think.Store(*think)
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	if *debug {
		log.Print("Log completo attivo (--debug): prompt, thinking e risposte vengono scritti qui.")
	}
	app.client = &http.Client{Timeout: 25 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		if app.nativeUI {
			listener = app.resolveExistingDesktop()
		}
		if listener == nil {
			return
		}
	} // An existing app keeps its connections.
	if bytes, err := os.ReadFile(filepath.Join(*state, "connections.json")); err == nil {
		if err := json.Unmarshal(bytes, &app.settings); err != nil {
			log.Print("Private settings could not be read")
			return
		}
	}
	go func() {
		app.refreshModels()
		if ctx.Err() != nil {
			return
		}
		app.mu.RLock()
		connections := append([]link{}, app.settings.Links...)
		app.mu.RUnlock()
		for _, connection := range connections {
			if allowedOrigin(connection.Origin) {
				app.startLink(connection)
			}
		}
		app.watchModels()
	}()
	if !*noBrowser && !app.nativeUI {
		go func() {
			select {
			case <-time.After(12 * time.Second):
				app.mu.RLock()
				paired := len(app.settings.Links) > 0
				app.mu.RUnlock()
				if !paired {
					app.openJase()
				}
			case <-app.paired:
			case <-ctx.Done():
			}
		}()
	}
	server := &http.Server{Handler: app, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, MaxHeaderBytes: 8192}
	shutdownDone := make(chan struct{})
	go func() {
		<-ctx.Done()
		shutdown, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		if server.Shutdown(shutdown) != nil {
			_ = server.Close()
		}
		if app.nativeUI {
			stopDesktopUI()
		}
		close(shutdownDone)
	}()
	log.Printf("Jase %s ready on loopback port %d", version, *port)
	app.record("app", "info", "Jase Tunnel avviato", "Chiudi Jase Tunnel termina anche il servizio locale.")
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Print("Local service stopped")
		}
		cancel()
	}()
	if app.nativeUI {
		app.runDesktopUI()
		cancel()
	} else {
		<-ctx.Done()
	}
	<-shutdownDone
}

func randomSecret() string {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		panic(err)
	}
	return hex.EncodeToString(bytes)
}
func allowedOrigin(value string) bool {
	u, err := url.Parse(value)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return false
	}
	if value == "https://jaseengine.it" {
		return true
	}
	return devBuild == "true" && u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1") && u.Port() != ""
}
func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func fail(w http.ResponseWriter, status int, message string) {
	respond(w, status, map[string]any{"error": map[string]string{"message": message}})
}
func decode(w http.ResponseWriter, r *http.Request, value any, limit int64) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return errors.New("unexpected body")
	}
	return nil
}

func (a *application) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Host != fmt.Sprintf("127.0.0.1:%d", a.port) && r.Host != fmt.Sprintf("localhost:%d", a.port) {
		fail(w, 403, "Indirizzo locale non valido.")
		return
	}
	origin := r.Header.Get("Origin")
	localOrigin := fmt.Sprintf("http://127.0.0.1:%d", a.port)
	if origin != "" && !allowedOrigin(origin) && origin != localOrigin {
		fail(w, 403, "Apri questo collegamento dal sito Jase.")
		return
	}
	if origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Private-Network", "true")
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(204)
		return
	}
	if r.URL.Path == "/" && r.Method == "GET" {
		a.statusPage(w)
		return
	}
	if r.URL.Path == "/v1/status" && r.Method == "GET" {
		a.mu.RLock()
		count := len(a.models)
		files := append([]modelFile{}, a.files...)
		connectionIDs := []string{}
		for _, c := range a.settings.Links {
			connectionIDs = append(connectionIDs, c.ConnectionID)
		}
		a.mu.RUnlock()
		ui := ""
		if a.nativeUI {
			ui = desktopUIName
		}
		respond(w, 200, map[string]any{"protocol": protocol, "version": version, "modelCount": count, "files": files, "connectionIds": connectionIDs, "desktop": a.nativeUI, "desktopUI": ui, "development": devBuild == "true"})
		return
	}
	if r.Method != "POST" || origin == "" || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		fail(w, 403, "Richiesta locale non consentita.")
		return
	}
	switch r.URL.Path {
	case "/v1/show-window":
		if !a.nativeUI {
			fail(w, 404, "Questa versione non ha un pannello nativo.")
			return
		}
		showDesktopUI()
		respond(w, 200, map[string]bool{"ok": true})
	case "/v1/pair":
		var input struct {
			Ticket string `json:"ticket"`
			Origin string `json:"origin"`
		}
		if decode(w, r, &input, 4096) != nil || len(input.Ticket) != 64 || input.Origin != origin || !allowedOrigin(input.Origin) || origin == localOrigin {
			fail(w, 400, "Collegamento non valido.")
			return
		}
		a.mu.RLock()
		models := append([]model{}, a.models...)
		a.mu.RUnlock()
		name := "Il mio computer"
		var result struct {
			Protocol     string `json:"protocol"`
			Token        string `json:"token"`
			ConnectionID string `json:"connectionId"`
		}
		err := a.remote(r.Context(), link{Origin: input.Origin}, "pair", map[string]any{"ticket": input.Ticket, "label": name, "models": models}, &result)
		if err != nil || result.Protocol != protocol || len(result.Token) != 64 {
			fail(w, 502, "Collegamento a Jase non riuscito. Riprova dalla pagina.")
			return
		}
		connection := link{Origin: input.Origin, Token: result.Token, ConnectionID: result.ConnectionID}
		a.mu.Lock()
		if len(a.settings.Links) >= 20 {
			a.mu.Unlock()
			fail(w, 409, "Troppi browser collegati. Scollega un browser dalle impostazioni.")
			return
		}
		a.settings.Links = append(a.settings.Links, connection)
		err = a.saveLocked()
		a.mu.Unlock()
		if err != nil {
			fail(w, 500, "Impossibile conservare il collegamento sul computer.")
			return
		}
		a.pairOnce.Do(func() { close(a.paired) })
		a.startLink(connection)
		a.record("app", "info", "Browser collegato a Jase", connection.Origin)
		respond(w, 200, map[string]bool{"ok": true})
	case "/v1/add-model":
		var input struct {
			Folder bool `json:"folder"`
		}
		if decode(w, r, &input, 1024) != nil {
			fail(w, 400, "Richiesta non valida.")
			return
		}
		if !a.importMu.TryLock() {
			fail(w, 409, "Una selezione è già aperta sul computer.")
			return
		}
		defer a.importMu.Unlock()
		if err := a.importModel(r.Context(), input.Folder); err != nil {
			fail(w, 400, err.Error())
			return
		}
		a.refreshModels()
		a.publishModels()
		respond(w, 200, map[string]bool{"ok": true})
	case "/v1/open-jase":
		a.openJase()
		respond(w, 200, map[string]bool{"ok": true})
	case "/v1/import-model":
		var input struct {
			ID string `json:"id"`
		}
		if decode(w, r, &input, 1024) != nil {
			fail(w, 400, "Modello non valido.")
			return
		}
		if !a.importMu.TryLock() {
			fail(w, 409, "Un’importazione è già aperta.")
			return
		}
		defer a.importMu.Unlock()
		a.mu.RLock()
		var chosen *modelFile
		for _, candidate := range a.files {
			if candidate.ID == input.ID {
				copy := candidate
				chosen = &copy
				break
			}
		}
		a.mu.RUnlock()
		if chosen == nil {
			fail(w, 404, "Modello non trovato nei percorsi locali.")
			return
		}
		if err := a.importModelAt(r.Context(), chosen.Path, chosen.Folder); err != nil {
			fail(w, 400, err.Error())
			return
		}
		a.refreshModels()
		a.publishModels()
		respond(w, 200, map[string]bool{"ok": true})
	case "/v1/quit":
		respond(w, 200, map[string]bool{"ok": true})
		go func() { time.Sleep(100 * time.Millisecond); a.cancel() }()
	default:
		fail(w, 404, "Risorsa non trovata.")
	}
}
func (a *application) saveLocked() error {
	bytes, err := json.Marshal(a.settings)
	if err != nil {
		return err
	}
	temporary := filepath.Join(a.stateDir, "connections.json.tmp")
	if err = os.WriteFile(temporary, bytes, 0600); err != nil {
		return err
	}
	return replacePrivateFile(temporary, filepath.Join(a.stateDir, "connections.json"))
}
func replacePrivateFile(source, target string) error { return os.Rename(source, target) }
