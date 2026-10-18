package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"
)

type desktopIdentity struct {
	Protocol    string `json:"protocol"`
	Desktop     bool   `json:"desktop"`
	DesktopUI   string `json:"desktopUI"`
	Version     string `json:"version"`
	Development bool   `json:"development"`
}

// Jase Tunnel 0.2 and earlier identified their local service with this older protocol.
const previousLocalProtocol = "jase-companion-v1"

func currentDesktop(state desktopIdentity) bool {
	return state.Protocol == webProtocol && state.Desktop && state.DesktopUI == desktopUIName && state.Version == version && state.Development == (devBuild == "true")
}

// Opening Jase Tunnel twice focuses its existing window. An invisible older version can
// be replaced only after a native, explicit choice; unrelated listeners are left alone.
func (a *application) resolveExistingDesktop() net.Listener {
	base := fmt.Sprintf("http://127.0.0.1:%d", a.port)
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(base + "/v1/status")
	var state desktopIdentity
	if err == nil {
		err = json.NewDecoder(http.MaxBytesReader(nil, response.Body, 64000)).Decode(&state)
		response.Body.Close()
	}
	if err != nil || state.Protocol != webProtocol && state.Protocol != previousLocalProtocol {
		desktopStartupError("The local service is already in use. Close the other application and open Jase Tunnel again.")
		return nil
	}
	post := func(action string) bool {
		req, _ := http.NewRequest("POST", base+"/v1/"+action, bytes.NewBufferString("{}"))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", base)
		res, err := client.Do(req)
		if err != nil {
			return false
		}
		defer res.Body.Close()
		return res.StatusCode == 200
	}
	if currentDesktop(state) {
		post("show-window")
		return nil
	}
	if !confirmDesktopReplacement() {
		return nil
	}
	if !post("quit") {
		desktopStartupError("The previous version did not close. Quit it and open Jase Tunnel again.")
		return nil
	}
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", a.port))
		if err == nil {
			return listener
		}
	}
	desktopStartupError("The previous version is still shutting down. Open Jase Tunnel again in a moment.")
	return nil
}
