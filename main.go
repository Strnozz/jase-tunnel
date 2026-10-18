// Jase Tunnel: connects a model running on this computer to the websites that adopt the
// jase-tunnel-v1 module, after the person's consent. No cloud SDK or outside relay.
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

var version = "dev"

// Loopback site origins (http://localhost:3000 …) are for development builds only
// (`npm run build:dev` sets this through -ldflags). A release accepts only public HTTPS
// sites: otherwise any local web page could pair the app to a server of its own.
// No runtime flag can turn a release into a development build.
var devBuild = "false"

// A site's grant, pinned to one model and short-lived (see answerWebConsent).
type link struct {
	Protocol     string `json:"protocol,omitempty"`
	Broker       string `json:"broker,omitempty"`
	Model        model  `json:"model,omitempty"`
	ExpiresAt    int64  `json:"expiresAt,omitempty"`
	Origin       string `json:"origin"`
	Token        string `json:"token"`
	ConnectionID string `json:"connectionId"`
}
type settings struct {
	Links []link `json:"links"`
}
type application struct {
	mu        sync.RWMutex
	settings  settings
	models    []model
	files     []modelFile
	stateDir  string
	port      int
	ctx       context.Context
	cancel    context.CancelFunc
	running   map[string]bool
	inference chan struct{}
	importMu  sync.Mutex
	// --debug: full log (prompts, thinking, replies, every call to a site); --think: let
	// models that can reason do it.
	debug    atomic.Bool
	think    atomic.Bool
	activity activityLog
	nativeUI bool
	web      *webManager
	runs     map[string]context.CancelFunc
	webHTTP  *http.Client
	// Shows a native notification (notifyDesktop with the panel; none in headless mode).
	notify func(id, title, body string)
	// LM Studio instance loaded by Jase Tunnel for each model name (see studioInstance).
	studioLoaded map[string]string
	// Last successful answer of each local engine (see refreshModels).
	engineSeen map[string]time.Time
}

func main() {
	foreground := flag.Bool("foreground", false, "Run attached to the current terminal (diagnostics)")
	background := flag.Bool("background", false, "Internal background process")
	state := flag.String("state-dir", "", "Private application state directory")
	port := flag.Int("port", 11439, "Loopback service port")
	debug := flag.Bool("debug", false, "Log prompts, model thinking, replies and every call to a site (local diagnostics)")
	think := flag.Bool("think", false, "Let models that declare the thinking capability reason before answering")
	headless := flag.Bool("headless", false, "Run without the desktop interface (explicit terminal diagnostics only)")
	flag.Parse()
	desktop := desktopAvailable && !*headless
	failure := func(message string) {
		if desktop {
			desktopStartupError(message)
		} else {
			log.Print(message)
		}
	}
	if runtime.GOOS == "darwin" && !*headless && !desktopAvailable {
		log.Print("The macOS app requires AppKit: build on macOS with CGO_ENABLED=1, or use --headless for diagnostics")
		return
	}
	if desktop {
		if err := prepareDesktopUI(); err != nil {
			desktopStartupError(err.Error())
			return
		}
	}
	if *state == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			failure("The user configuration directory is not available.")
			return
		}
		*state = filepath.Join(base, "Jase Tunnel")
	}
	if err := os.MkdirAll(*state, 0700); err != nil {
		failure("Could not create the private Jase Tunnel directory: " + err.Error())
		return
	}
	if !*foreground && !*background && !desktop {
		executable, err := os.Executable()
		if err != nil {
			log.Print("Unable to locate the Jase Tunnel executable")
			return
		}
		file, err := os.OpenFile(filepath.Join(*state, "jase-tunnel.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			log.Print("Unable to open the Jase Tunnel log")
			return
		}
		defer file.Close()
		args := []string{"--background", "--state-dir", *state, "--port", fmt.Sprint(*port)}
		if *headless {
			args = append(args, "--headless")
		}
		for name, on := range map[string]bool{"--debug": *debug, "--think": *think} {
			if on {
				args = append(args, name)
			}
		}
		command := exec.Command(executable, args...)
		command.Stdout = file
		command.Stderr = file
		detach(command)
		if err := command.Start(); err != nil {
			log.Print("Unable to start Jase Tunnel")
		}
		return
	}
	if !*foreground {
		file, err := os.OpenFile(filepath.Join(*state, "jase-tunnel.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			failure("Could not open the Jase Tunnel log: " + err.Error())
			return
		}
		defer file.Close()
		log.SetOutput(file)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	app := &application{stateDir: *state, port: *port, ctx: ctx, cancel: cancel, running: map[string]bool{}, inference: make(chan struct{}, 1), nativeUI: desktop}
	if desktop {
		app.notify = notifyDesktop
	}
	app.debug.Store(*debug)
	app.think.Store(*think)
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	if *debug {
		log.Print("Full logging enabled (--debug): prompts, reasoning and responses are written here.")
	}

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
			failure("The private Jase Tunnel settings could not be read.")
			return
		}
	}
	app.dropInvalidLinks()
	go func() {
		app.refreshModels()
		if ctx.Err() != nil {
			return
		}
		app.mu.RLock()
		connections := append([]link{}, app.settings.Links...)
		app.mu.RUnlock()
		for _, connection := range connections {
			app.startLink(connection)
		}
		app.watchModels()
	}()
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
	log.Printf("Jase Tunnel %s ready on loopback port %d", version, *port)
	app.record("app", "info", "Jase Tunnel started", "Compatible pages announce themselves as you browse. Quit Jase Tunnel also stops the local service.")
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

// Only unexpired grants of the current protocol survive a restart. Connections saved by
// releases before 0.3 belonged to a single site and are discarded with their tokens.
func (a *application) dropInvalidLinks() {
	a.mu.Lock()
	defer a.mu.Unlock()
	kept := []link{}
	for _, c := range a.settings.Links {
		if validWebLink(c) {
			kept = append(kept, c)
		}
	}
	if len(kept) != len(a.settings.Links) {
		a.settings.Links = kept
		_ = a.saveLocked()
	}
}

func randomSecret() string {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		panic(err)
	}
	return hex.EncodeToString(bytes)
}
func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func fail(w http.ResponseWriter, status int, message string) {
	respond(w, status, map[string]any{"error": map[string]string{"message": message}})
}

// decode reads one JSON value and refuses unknown fields: for the app's own local API.
func decode(w http.ResponseWriter, r *http.Request, value any, limit int64) error {
	return decodeJSON(w, r, value, limit, true)
}

// decodeWeb reads what websites send. The site module and the app are updated
// independently, so a field added by a newer module is ignored rather than refused.
func decodeWeb(w http.ResponseWriter, r *http.Request, value any, limit int64) error {
	return decodeJSON(w, r, value, limit, false)
}
func decodeJSON(w http.ResponseWriter, r *http.Request, value any, limit int64, strict bool) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	if strict {
		decoder.DisallowUnknownFields()
	}
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
		fail(w, 403, "Invalid local address.")
		return
	}
	if strings.HasPrefix(r.URL.Path, "/tunnel/v1/") {
		a.serveWeb(w, r)
		return
	}
	// Everything else manages the app itself: only its status page (under either Host
	// name accepted above) or a local process without a browser Origin may use it.
	// Websites reach the tunnel exclusively through /tunnel/v1/.
	origin := r.Header.Get("Origin")
	if origin != "" && origin != fmt.Sprintf("http://127.0.0.1:%d", a.port) && origin != fmt.Sprintf("http://localhost:%d", a.port) {
		fail(w, 403, "Local request not allowed.")
		return
	}
	if r.URL.Path == "/" && r.Method == "GET" {
		a.statusPage(w)
		return
	}
	if r.URL.Path == "/v1/status" && r.Method == "GET" {
		a.mu.RLock()
		count := len(a.models)
		a.mu.RUnlock()
		ui := ""
		if a.nativeUI {
			ui = desktopUIName
		}
		respond(w, 200, map[string]any{"protocol": webProtocol, "version": version, "modelCount": count, "desktop": a.nativeUI, "desktopUI": ui, "development": devBuild == "true"})
		return
	}
	// State changes need the page's Origin and JSON: a cross-site form can send neither.
	if r.Method != "POST" || origin == "" || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		fail(w, 403, "Local request not allowed.")
		return
	}
	switch r.URL.Path {
	case "/v1/show-window":
		if !a.nativeUI {
			fail(w, 404, "This version does not have a native panel.")
			return
		}
		showDesktopUI()
		respond(w, 200, map[string]bool{"ok": true})
	case "/v1/add-model":
		var input struct {
			Folder bool `json:"folder"`
		}
		if decode(w, r, &input, 1024) != nil {
			fail(w, 400, "Invalid request.")
			return
		}
		if !a.importMu.TryLock() {
			fail(w, 409, "A file chooser is already open on this computer.")
			return
		}
		defer a.importMu.Unlock()
		if err := a.importModel(r.Context(), input.Folder); err != nil {
			fail(w, 400, err.Error())
			return
		}
		a.refreshModels()
		respond(w, 200, map[string]bool{"ok": true})
	case "/v1/quit":
		respond(w, 200, map[string]bool{"ok": true})
		go func() { time.Sleep(100 * time.Millisecond); a.cancel() }()
	default:
		fail(w, 404, "Resource not found.")
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
