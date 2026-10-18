//go:build darwin && cgo

package main

/*
#cgo CFLAGS: -mmacosx-version-min=13.0
#cgo LDFLAGS: -framework AppKit -framework CoreGraphics -framework UserNotifications
#include <stdlib.h>
void JaseRunDesktop(void);
void JaseShowDesktop(void);
void JaseStopDesktop(void);
void JaseImportFinished(const char *);
int JaseConfirmReplacement(void);
void JaseStartupError(const char *);
void JaseNotify(const char *, const char *, const char *);
*/
import "C"

import (
	"encoding/json"
	"errors"
	"runtime"
	"unsafe"
)

const desktopAvailable = true
const desktopUIName = "appkit-v1"

func prepareDesktopUI() error { return nil }

// AppKit's event loop stays on the initial macOS thread. Discovery, imports and
// inference remain on Go's worker threads, in the same application process.
func init() { runtime.LockOSThread() }

var desktopApplication *application

//export jaseDesktopQuit
func jaseDesktopQuit() {
	if desktopApplication != nil {
		desktopApplication.cancel()
	}
}

//export jaseDesktopState
func jaseDesktopState(after C.ulonglong) *C.char {
	if desktopApplication == nil {
		return C.CString("{}")
	}
	data, _ := json.Marshal(desktopApplication.desktopState(uint64(after)))
	return C.CString(string(data)) // AppKit frees each snapshot immediately.
}

//export jaseDesktopAction
func jaseDesktopAction(action *C.char, enabled C.int) {
	a := desktopApplication
	if a == nil {
		return
	}
	switch C.GoString(action) {
	case "debug":
		a.setDetailedLogging(enabled != 0)
	case "thinking":
		a.setModelThinking(enabled != 0)
	case "clear":
		a.clearActivity()
	case "revoke-sites":
		go a.revokeLinks()
	}
}

//export jaseDesktopNotificationClicked
func jaseDesktopNotificationClicked(id *C.char) {
	if a := desktopApplication; a != nil {
		identifier := C.GoString(id)
		go a.requestWebConnection(identifier)
	}
}

//export jaseDesktopRevokeSite
func jaseDesktopRevokeSite(origin *C.char) {
	if a := desktopApplication; a != nil {
		site := C.GoString(origin)
		go a.revokeSite(site)
	}
}

//export jaseDesktopApproval
func jaseDesktopApproval() *C.char {
	if desktopApplication == nil {
		return C.CString("null")
	}
	data, _ := json.Marshal(desktopApplication.claimWebApproval())
	return C.CString(string(data))
}

//export jaseDesktopConsent
func jaseDesktopConsent(id, model *C.char) {
	if desktopApplication != nil {
		a := desktopApplication
		identifier, key := C.GoString(id), C.GoString(model)
		go a.answerWebConsent(identifier, key)
	}
}

func nativeImport(a *application, path string, folder bool) {
	go func() {
		var err error
		if !a.importMu.TryLock() {
			err = errors.New("An import is already in progress. Wait for it to finish.")
		} else {
			err = a.importModelAt(a.ctx, path, folder)
			a.importMu.Unlock()
			if err == nil {
				a.refreshModels()
			}
		}
		message := "Weights added. The local engine will detect the model."
		level := "info"
		if err != nil {
			message, level = err.Error(), "error"
		}
		a.record("app", level, "Weight import", message)
		result := C.CString(message)
		defer C.free(unsafe.Pointer(result))
		C.JaseImportFinished(result)
	}()
}

//export jaseDesktopImport
func jaseDesktopImport(path *C.char, folder C.int) {
	if a := desktopApplication; a != nil {
		nativeImport(a, C.GoString(path), folder != 0)
	}
}

//export jaseDesktopImportFound
func jaseDesktopImportFound(id *C.char) {
	a := desktopApplication
	if a == nil {
		return
	}
	key := C.GoString(id)
	a.mu.RLock()
	var selected modelFile
	for _, f := range a.files {
		if f.ID == key {
			selected = f
			break
		}
	}
	a.mu.RUnlock()
	if selected.Path != "" {
		nativeImport(a, selected.Path, selected.Folder)
	} else {
		message := C.CString("These weights are no longer available. Select them with Add weights.")
		C.JaseImportFinished(message)
		C.free(unsafe.Pointer(message))
	}
}

func (a *application) runDesktopUI() {
	desktopApplication = a
	C.JaseRunDesktop()
}
func showDesktopUI()                  { C.JaseShowDesktop() }
func stopDesktopUI()                  { C.JaseStopDesktop() }
func confirmDesktopReplacement() bool { return C.JaseConfirmReplacement() != 0 }
func desktopStartupError(message string) {
	text := C.CString(message)
	defer C.free(unsafe.Pointer(text))
	C.JaseStartupError(text)
}

// Notification Center needs the signed app bundle; a bare development binary keeps
// the announcement in the panel's journal only.
func notifyDesktop(id, title, body string) {
	identifier, heading, text := C.CString(id), C.CString(title), C.CString(body)
	defer C.free(unsafe.Pointer(identifier))
	defer C.free(unsafe.Pointer(heading))
	defer C.free(unsafe.Pointer(text))
	C.JaseNotify(identifier, heading, text)
}
