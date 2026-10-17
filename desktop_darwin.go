//go:build darwin && cgo

package main

/*
#cgo CFLAGS: -mmacosx-version-min=13.0
#cgo LDFLAGS: -framework AppKit -framework CoreGraphics
#include <stdlib.h>
void JaseRunDesktop(void);
void JaseShowDesktop(void);
void JaseStopDesktop(void);
void JaseImportFinished(const char *);
int JaseConfirmReplacement(void);
void JaseStartupError(const char *);
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
	case "open-jase":
		go a.openJase()
	case "revoke-sites":
		go a.revokeWebLinks()
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
			err = errors.New("Un’importazione è già in corso. Attendi che termini.")
		} else {
			err = a.importModelAt(a.ctx, path, folder)
			a.importMu.Unlock()
			if err == nil {
				a.refreshModels()
				a.publishModels()
			}
		}
		message := "Pesi aggiunti. Il modello viene rilevato dal motore locale."
		level := "info"
		if err != nil {
			message, level = err.Error(), "error"
		}
		a.record("app", level, "Importazione dei pesi", message)
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
		message := C.CString("Pesi non più disponibili. Selezionali con Aggiungi pesi.")
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
