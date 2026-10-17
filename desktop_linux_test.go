package main

import (
	"os"
	"runtime"
	"testing"
	"time"
)

func TestLinuxDesktopSmoke(t *testing.T) {
	if os.Getenv("JASE_DESKTOP_SMOKE") != "1" {
		t.Skip("requires a graphical Linux session or Xvfb")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := prepareDesktopUI(); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t)
	a.models = []model{{Name: "Native smoke fixture", Engine: "ollama", Tools: true}}
	a.files = []modelFile{{ID: "fixture-weights", Name: "Fixture.gguf"}}
	a.record("server", "info", "Fixture request", "Native inspector fixture")
	a.record("receive", "info", "Fixture reply", "")
	go func() { time.Sleep(900 * time.Millisecond); a.cancel() }()
	a.runDesktopUI()
	if a.ctx.Err() == nil {
		t.Fatal("closing the desktop did not cancel its engine")
	}
}
