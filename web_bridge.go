package main

import (
	_ "embed"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

//go:embed web_bridge.html
var browserWindowHTML string

// Safari blocks HTTPS pages fetching HTTP loopback. A user-opened top-level
// window can instead contact this service on its own origin. No model inventory
// or app management operation is exposed to the opener.
func (a *application) serveBrowserWindow(w http.ResponseWriter, r *http.Request) {
	nonce := webSecret(24)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+nonce+"'; style-src 'nonce-"+nonce+"'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	_, _ = io.WriteString(w, strings.ReplaceAll(browserWindowHTML, "{{nonce}}", nonce))
}

func (a *application) serveBrowserBridge(w http.ResponseWriter, r *http.Request) {
	// ServeHTTP also enforces the exact loopback Host, a local Origin, POST and
	// JSON. Check a single Origin explicitly: external pages cannot use this relay
	// directly, by CORS, HTML forms, navigation or an embedded connection window.
	if len(r.Header.Values("Origin")) != 1 {
		webFail(w, 403, "origin_mismatch", "Invalid local window origin.")
		return
	}
	var input struct {
		Origin    string          `json:"origin"`
		Operation string          `json:"operation"`
		Receipt   string          `json:"receipt"`
		Body      json.RawMessage `json:"body"`
	}
	if decode(w, r, &input, 8192) != nil {
		webFail(w, 400, "invalid_request", "Invalid connection request.")
		return
	}
	if !webOrigin(input.Origin) {
		webFail(w, 403, "origin_mismatch", "Invalid website origin.")
		return
	}
	if input.Operation != "discover" && input.Operation != "discovery" && input.Operation != "pair" {
		webFail(w, 400, "invalid_request", "Unsupported connection operation.")
		return
	}
	// Reuse the normal limits, origin-bound receipts, public-domain verification,
	// one-use broker tickets and native consent. Never forward a URL or headers
	// supplied by the opener, and never return the desktop's broker credential.
	forward := r.Clone(r.Context())
	forward.URL.Path = "/tunnel/v1/" + input.Operation
	forward.Header = make(http.Header)
	forward.Header.Set("Origin", input.Origin)
	forward.Method = "POST"
	forward.Header.Set("Content-Type", "application/json")
	forward.Body = io.NopCloser(strings.NewReader(string(input.Body)))
	forward.ContentLength = int64(len(input.Body))
	if input.Operation == "discovery" {
		forward.Method = "GET"
		forward.Body = http.NoBody
		forward.ContentLength = 0
	}
	if input.Receipt != "" {
		forward.Header.Set("Authorization", "Bearer "+input.Receipt)
	}
	a.serveWebRequest(w, forward, false)
}
