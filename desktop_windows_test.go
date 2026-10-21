package main

import (
	"bytes"
	"os"
	"runtime"
	"testing"
	"time"
	"unsafe"
)

func TestWindowsNativeABIAndTrayActions(t *testing.T) {
	if unsafe.Sizeof(winClass{}) != 80 || unsafe.Sizeof(winMessage{}) != 48 || unsafe.Sizeof(winNotifyIcon{}) != 976 || unsafe.Sizeof(winDrawItem{}) != 64 || unsafe.Sizeof(winMeasureItem{}) != 32 || unsafe.Sizeof(winPaint{}) != 72 {
		t.Fatal("Win32 ABI layout is incompatible")
	}
	if windowsTrayAction(1<<16|0x203) != idOpen || windowsTrayAction(1<<16|0x7b) != -1 || windowsTrayAction(1<<16|0x401) != idOpen || windowsTrayAction(1<<16|0x201) != 0 {
		t.Fatal("double click, context menu or keyboard activation is incorrect")
	}
}

func TestWindowsNativeIconAndControls(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	icon := winCall("CreateIconFromResourceEx", uintptr(unsafe.Pointer(&windowsIconPNG[0])), uintptr(len(windowsIconPNG)), 1, 0x30000, 32, 32, 0)
	if icon == 0 {
		t.Fatal("Jase logo cannot be loaded as a Windows icon")
	}
	defer winCall("DestroyIcon", icon)
	class, title := wtext("STATIC"), wtext("Jase Tunnel native test")
	hwnd := winCall("CreateWindowExW", 0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)), 0, 0, 0, 800, 650, 0, 0, 0, 0)
	runtime.KeepAlive(class)
	runtime.KeepAlive(title)
	if hwnd == 0 {
		t.Fatal("native window could not be created")
	}
	defer winCall("DestroyWindow", hwnd)
	p := &windowsPanel{hwnd: hwnd, controls: map[int]uintptr{}, scale: 96}
	for _, control := range []string{"BUTTON", "LISTBOX", "COMBOBOX", "EDIT"} {
		if p.child(control, "Fixture", 0, 0) == 0 {
			t.Fatalf("cannot create %s", control)
		}
	}
	list := p.child("LISTBOX", "", 1, idModelList)
	winListFill(list, []string{"First", "Second"}, 1)
	if winListSelection(list) != 1 {
		t.Fatal("model selection did not reach native list")
	}
}

func TestWindowsExecutableResources(t *testing.T) {
	instance, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
	for _, size := range []uintptr{16, 20, 24, 32, 40, 48, 64, 128, 256} {
		icon := winCall("LoadImageW", instance, 1, 1, size, size, 0)
		if icon == 0 {
			t.Fatalf("executable has no usable %d px icon", size)
		}
		winCall("DestroyIcon", icon)
	}
	resource, _, _ := kernel32.NewProc("FindResourceW").Call(instance, 1, 24)
	if resource == 0 {
		t.Fatal("executable has no application manifest")
	}
	size, _, _ := kernel32.NewProc("SizeofResource").Call(instance, resource)
	loaded, _, _ := kernel32.NewProc("LoadResource").Call(instance, resource)
	address, _, _ := kernel32.NewProc("LockResource").Call(loaded)
	if address == 0 || size == 0 {
		t.Fatal("cannot read executable manifest")
	}
	manifest := make([]byte, size)
	winReadNative(address, unsafe.Pointer(&manifest[0]), size)
	for _, required := range [][]byte{[]byte("Microsoft.Windows.Common-Controls"), []byte("6.0.0.0"), []byte("PerMonitorV2"), []byte("asInvoker")} {
		if !bytes.Contains(manifest, required) {
			t.Fatalf("manifest missing %q", required)
		}
	}
}

func TestWindowsPanelLayoutAtDifferentSizesAndDPI(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	for _, scale := range []int{96, 144, 192} {
		// Client sizes of the minimum and default windows, excluding the frame.
		for _, size := range [][2]int{{904, 681}, {1064, 821}} {
			class := wtext("STATIC")
			hwnd := winCall("CreateWindowExW", 0, uintptr(unsafe.Pointer(class)), 0, 0x80000000, 0, 0, uintptr(size[0]*scale/96), uintptr(size[1]*scale/96), 0, 0, 0, 0)
			runtime.KeepAlive(class)
			if hwnd == 0 {
				t.Fatal("cannot create layout fixture")
			}
			p := &windowsPanel{hwnd: hwnd, scale: scale, controls: map[int]uintptr{}, models: []panelModel{{Name: "Fixture"}}}
			labels := []*uintptr{&p.status, &p.connection, &p.stats, &p.privacy, &p.notice, &p.section, &p.hint, &p.detail}
			for _, label := range labels {
				*label = p.child("STATIC", "Fixture", 0, 0)
			}
			for id := idOpen; id <= idRevokeOne; id++ {
				p.child("STATIC", "Fixture", 0, id)
			}
			bounds := func(control uintptr) winRect {
				var rect winRect
				winCall("GetWindowRect", control, uintptr(unsafe.Pointer(&rect)))
				return rect
			}
			for _, activity := range []bool{false, true} {
				p.activity = activity
				p.layout()
				ids := []int{idModels, idActivity, idAdd, idQuit, idDebug, idThinking}
				if activity {
					ids = append(ids, idSiteList, idEventList, idFilter, idRevokeOne, idClear)
				} else {
					ids = append(ids, idModelList, idImport)
				}
				for _, id := range ids {
					r := bounds(p.controls[id])
					if r.Right <= r.Left || r.Bottom <= r.Top {
						t.Fatalf("empty control %d at %d DPI, size %v", id, scale, size)
					}
				}
				if activity {
					filter, revoke, clear := bounds(p.controls[idFilter]), bounds(p.controls[idRevokeOne]), bounds(p.controls[idClear])
					if filter.Right > revoke.Left || revoke.Right > clear.Left {
						t.Fatalf("activity toolbar overlaps at %d DPI, size %v", scale, size)
					}
					if bounds(p.controls[idEventList]).Bottom > bounds(p.detail).Top || bounds(p.detail).Bottom > bounds(p.controls[idDebug]).Top {
						t.Fatalf("activity details overlap at %d DPI, size %v", scale, size)
					}
				} else if bounds(p.controls[idModelList]).Bottom > bounds(p.controls[idImport]).Top {
					t.Fatalf("models overlap import action at %d DPI, size %v", scale, size)
				}
			}
			winCall("DestroyWindow", hwnd)
		}
	}
}

func TestWindowsDesktopSmoke(t *testing.T) {
	if os.Getenv("JASE_DESKTOP_SMOKE") != "1" {
		t.Skip("requires a graphical Windows session")
	}
	a := newTestApp(t)
	a.models = []model{{Name: "Native smoke fixture", Engine: "ollama", Tools: true}}
	a.files = []modelFile{{ID: "fixture-weights", Name: "Fixture.gguf"}}
	a.record("server", "info", "Fixture request", "Native inspector fixture")
	go func() {
		for winHandle.Load() == 0 {
			if a.ctx.Err() != nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		hwnd := winHandle.Load()
		// Switch both views; painting and control mutations stay on the panel's
		// locked OS thread while the application is polled in the background.
		winCall("PostMessageW", hwnd, wmCommand, idActivity, 0)
		winCall("PostMessageW", hwnd, wmCommand, idModels, 0)
		time.Sleep(500 * time.Millisecond)
		a.cancel()
	}()
	a.runDesktopUI()
	if a.ctx.Err() == nil {
		t.Fatal("desktop did not stop its engine")
	}
}
