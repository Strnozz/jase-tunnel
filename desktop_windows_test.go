package main

import (
	"runtime"
	"testing"
	"unsafe"
)

func TestWindowsNativeABIAndTrayActions(t *testing.T) {
	if unsafe.Sizeof(winClass{}) != 80 || unsafe.Sizeof(winMessage{}) != 48 || unsafe.Sizeof(winNotifyIcon{}) != 976 {
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
		t.Fatal("favicon cannot be loaded as a Windows icon")
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
