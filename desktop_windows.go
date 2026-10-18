package main

import (
	"context"
	_ "embed"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"unsafe"
)

const desktopAvailable = true
const desktopUIName = "win32-v1"

//go:embed assets/desktop-icon.png
var windowsIconPNG []byte

var (
	user32    = syscall.NewLazyDLL("user32.dll")
	shell32   = syscall.NewLazyDLL("shell32.dll")
	gdi32     = syscall.NewLazyDLL("gdi32.dll")
	kernel32  = syscall.NewLazyDLL("kernel32.dll")
	winHandle atomic.Uintptr
	winShow   atomic.Bool
	winStop   atomic.Bool
	winPanel  *windowsPanel // Accessed only by the window's locked OS thread.
)

const (
	wmClose     = 0x10
	wmDestroy   = 2
	wmSize      = 5
	wmCommand   = 0x111
	wmTimer     = 0x113
	wmApp       = 0x8000
	wmShowPanel = wmApp + 1
	wmStopPanel = wmApp + 2
	wmTray      = wmApp + 3
	idOpen      = 101
	idAdd       = 102
	idQuit      = 103
	idModels    = 104
	idActivity  = 105
	idImport    = 106
	idDebug     = 107
	idThinking  = 108
	idClear     = 109
	idFilter    = 110
	idModelList = 111
	idEventList = 112
	idOpenSite  = 113
	idRevoke    = 114
)

type winPoint struct{ X, Y int32 }
type winRect struct{ Left, Top, Right, Bottom int32 }
type winMessage struct {
	Window         uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	Point          winPoint
	Private        uint32
}
type winClass struct {
	Size, Style                        uint32
	Procedure                          uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	Menu, Name                         *uint16
	SmallIcon                          uintptr
}
type winNotifyIcon struct {
	Size                uint32
	Window              uintptr
	ID, Flags, Callback uint32
	Icon                uintptr
	Tip                 [128]uint16
	State, StateMask    uint32
	Info                [256]uint16
	Version             uint32
	InfoTitle           [64]uint16
	InfoFlags           uint32
	GUID                [16]byte
	BalloonIcon         uintptr
}

type windowsPanel struct {
	a                                                           *application
	hwnd, instance, font, titleFont, icon, menu                 uintptr
	taskbarCreated                                              uint32
	controls                                                    map[int]uintptr
	status, connection, stats, privacy, notice, detail, version uintptr
	models                                                      []panelModel
	events                                                      []activityEvent
	generation                                                  uint64
	imports                                                     <-chan string
	activity, tray                                              bool
	consenting                                                  bool
	scale                                                       int
}

func init()                   { runtime.LockOSThread() }
func prepareDesktopUI() error { return nil }
func wtext(text string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(strings.ReplaceAll(text, "\x00", ""))
	return p
}
func winCall(name string, args ...uintptr) uintptr {
	r, _, _ := user32.NewProc(name).Call(args...)
	return r
}
func winText(hwnd uintptr, text string) {
	p := wtext(text)
	winCall("SetWindowTextW", hwnd, uintptr(unsafe.Pointer(p)))
	runtime.KeepAlive(p)
}
func (p *windowsPanel) child(class, text string, style uint32, id int) uintptr {
	c, t := wtext(class), wtext(text)
	h := winCall("CreateWindowExW", 0, uintptr(unsafe.Pointer(c)), uintptr(unsafe.Pointer(t)), uintptr(style|0x50000000), 0, 0, 1, 1, p.hwnd, uintptr(id), p.instance, 0)
	runtime.KeepAlive(c)
	runtime.KeepAlive(t)
	if h == 0 {
		panic("Impossibile creare il controllo nativo " + class)
	}
	winCall("SendMessageW", h, 0x30, p.font, 1)
	if id != 0 {
		p.controls[id] = h
	}
	return h
}
func winMessageBox(owner uintptr, text string, flags uintptr) uintptr {
	t, title := wtext(text), wtext("Jase Tunnel")
	r := winCall("MessageBoxW", owner, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(title)), flags)
	runtime.KeepAlive(t)
	runtime.KeepAlive(title)
	return r
}
func confirmDesktopReplacement() bool {
	return winMessageBox(0, "Una versione precedente di Jase Tunnel è già aperta. Riavviarla per usare il nuovo pannello? Le richieste in corso verranno interrotte.", 0x24|0x100) == 6
}
func desktopStartupError(message string) { winMessageBox(0, message, 0x10) }
func showDesktopUI() {
	winShow.Store(true)
	if h := winHandle.Load(); h != 0 {
		winCall("PostMessageW", h, wmShowPanel, 0, 0)
	}
}
func stopDesktopUI() {
	winStop.Store(true)
	if h := winHandle.Load(); h != 0 {
		winCall("PostMessageW", h, wmStopPanel, 0, 0)
	}
}

// NOTIFYICON_VERSION_4 packs the notification code into LOWORD(lParam).
func windowsTrayAction(event uintptr) int {
	switch event & 0xffff {
	case 0x203, 0x401:
		return idOpen // Double click or keyboard activation.
	case 0x7b:
		return -1 // Native context menu (mouse or keyboard).
	}
	return 0
}

func (p *windowsPanel) addTray() bool {
	n := winNotifyIcon{Window: p.hwnd, ID: 1, Flags: 1 | 2 | 4 | 0x80, Callback: wmTray, Icon: p.icon}
	n.Size = uint32(unsafe.Sizeof(n))
	copy(n.Tip[:], syscall.StringToUTF16("Jase Tunnel · in esecuzione"))
	ok, _, _ := shell32.NewProc("Shell_NotifyIconW").Call(0, uintptr(unsafe.Pointer(&n)))
	if ok != 0 {
		n.Version = 4
		shell32.NewProc("Shell_NotifyIconW").Call(4, uintptr(unsafe.Pointer(&n)))
	}
	p.tray = ok != 0
	return p.tray
}
func (p *windowsPanel) removeTray() {
	n := winNotifyIcon{Window: p.hwnd, ID: 1}
	n.Size = uint32(unsafe.Sizeof(n))
	shell32.NewProc("Shell_NotifyIconW").Call(2, uintptr(unsafe.Pointer(&n)))
	p.tray = false
}
func winAppend(menu uintptr, id int, label string) {
	t := wtext(label)
	winCall("AppendMenuW", menu, 0, uintptr(id), uintptr(unsafe.Pointer(t)))
	runtime.KeepAlive(t)
}
func (p *windowsPanel) popup(menu uintptr) int {
	var point winPoint
	winCall("GetCursorPos", uintptr(unsafe.Pointer(&point)))
	winCall("SetForegroundWindow", p.hwnd)
	id := winCall("TrackPopupMenuEx", menu, 0x100|0x2, uintptr(point.X), uintptr(point.Y), p.hwnd, 0)
	winCall("PostMessageW", p.hwnd, 0, 0, 0)
	return int(id)
}
func (p *windowsPanel) show() {
	winCall("ShowWindow", p.hwnd, 9)
	winCall("SetForegroundWindow", p.hwnd)
}
func (p *windowsPanel) selectWeights() {
	if p.imports != nil {
		p.show()
		return
	}
	p.show()
	menu := winCall("CreatePopupMenu")
	defer winCall("DestroyMenu", menu)
	winAppend(menu, 1, "File GGUF…")
	winAppend(menu, 2, "Cartella modello…")
	choice := p.popup(menu)
	if choice == 0 {
		return
	}
	p.imports = p.a.importFromDesktop(func(ctx context.Context) (string, bool, error) {
		folder := choice == 2
		path, err := pickModel(ctx, folder)
		return path, folder, err
	})
	winText(p.notice, "Scegli i pesi nella finestra del computer. L’importazione proseguirà in background…")
	p.refresh()
}
func winListSelection(hwnd uintptr) int {
	return int(int32(winCall("SendMessageW", hwnd, 0x188, 0, 0)))
}
func (p *windowsPanel) command(id, notification int) {
	switch id {
	case idOpen:
		p.show()
	case idAdd:
		p.selectWeights()
	case idQuit:
		p.a.cancel()
	case idRevoke:
		go p.a.revokeLinks()
	case idOpenSite:
		go p.a.openJase()
	case idModels:
		p.activity = false
		p.layout()
		p.refresh()
	case idActivity:
		p.activity = true
		p.layout()
		p.refresh()
	case idImport:
		index := winListSelection(p.controls[idModelList])
		if p.imports == nil && index >= 0 && index < len(p.models) && p.models[index].ID != "" {
			p.imports = p.a.importDetectedFromDesktop(p.models[index].ID)
			winText(p.notice, "Aggiungo i pesi al motore locale…")
			p.refresh()
		}
	case idDebug:
		p.a.setDetailedLogging(winCall("SendMessageW", p.controls[idDebug], 0xf0, 0, 0) == 1)
		p.refresh()
	case idThinking:
		p.a.setModelThinking(winCall("SendMessageW", p.controls[idThinking], 0xf0, 0, 0) == 1)
		p.refresh()
	case idClear:
		p.a.clearActivity()
		p.refresh()
	case idFilter:
		if notification == 1 {
			p.refresh()
		}
	case idModelList:
		if notification == 2 {
			p.command(idImport, 0)
		} else {
			p.updateImportButton()
		}
	case idEventList:
		if notification == 1 {
			winText(p.detail, strings.ReplaceAll(eventDetail(p.events, winListSelection(p.controls[idEventList])), "\n", "\r\n"))
		}
	}
}
func winProcedure(hwnd uintptr, message uint32, wparam, lparam uintptr) uintptr {
	p := winPanel
	if p != nil {
		if message == p.taskbarCreated && p.taskbarCreated != 0 {
			if !p.addTray() {
				p.show()
			}
			return 0
		}
		switch message {
		case wmCommand:
			p.command(int(wparam&0xffff), int(wparam>>16))
			return 0
		case wmTray:
			action := windowsTrayAction(lparam)
			if action == -1 {
				action = p.popup(p.menu)
			}
			if action != 0 {
				p.command(action, 0)
			}
			return 0
		case wmShowPanel:
			winShow.Store(false)
			p.show()
			return 0
		case wmStopPanel:
			winCall("DestroyWindow", hwnd)
			return 0
		case wmClose:
			if p.tray {
				winCall("ShowWindow", hwnd, 0)
			} else {
				winCall("ShowWindow", hwnd, 6)
			}
			return 0
		case wmDestroy:
			p.removeTray()
			winHandle.Store(0)
			winCall("PostQuitMessage", 0)
			return 0
		case wmSize:
			if len(p.controls) > 0 {
				p.layout()
			}
			return 0
		case 0x24: // WM_GETMINMAXINFO: keep all controls and Quit reachable.
			if lparam != 0 {
				minimum := winPoint{X: int32(760 * p.scale / 96), Y: int32(660 * p.scale / 96)}
				// MINMAXINFO.ptMinTrackSize is its fourth POINT. Write through
				// the native API without converting an integer callback argument
				// into a Go pointer retained on the callback stack.
				kernel32.NewProc("RtlMoveMemory").Call(lparam+3*unsafe.Sizeof(winPoint{}), uintptr(unsafe.Pointer(&minimum)), unsafe.Sizeof(minimum))
				runtime.KeepAlive(minimum)
			}
			return 0
		case wmTimer:
			if winStop.Load() || p.a.ctx.Err() != nil {
				winCall("DestroyWindow", hwnd)
				return 0
			}
			if winShow.Swap(false) {
				p.show()
			}
			p.refresh()
			return 0
		}
	}
	return winCall("DefWindowProcW", hwnd, uintptr(message), wparam, lparam)
}
func (p *windowsPanel) position(hwnd uintptr, x, y, w, h int) {
	s := p.scale
	winCall("MoveWindow", hwnd, uintptr(x*s/96), uintptr(y*s/96), uintptr(w*s/96), uintptr(h*s/96), 1)
}
func (p *windowsPanel) layout() {
	if p.status == 0 {
		return
	}
	var r winRect
	winCall("GetClientRect", p.hwnd, uintptr(unsafe.Pointer(&r)))
	w, h := int(r.Right)*96/p.scale, int(r.Bottom)*96/p.scale
	if w < 650 {
		w = 650
	}
	if h < 520 {
		h = 520
	}
	p.position(p.status, 24, 18, w-48, 30)
	p.position(p.connection, 24, 52, w-48, 24)
	p.position(p.controls[idOpenSite], 24, 84, 130, 30)
	p.position(p.controls[idAdd], 166, 84, 140, 30)
	p.position(p.controls[idQuit], w-190, 84, 166, 30)
	p.position(p.controls[idModels], 24, 130, 130, 30)
	p.position(p.controls[idActivity], 166, 130, 130, 30)
	p.position(p.controls[idRevoke], w-190, 130, 166, 30)
	p.position(p.controls[idModelList], 24, 174, w-48, h-365)
	p.position(p.controls[idImport], 24, h-181, 190, 30)
	p.position(p.notice, 24, h-143, w-48, 40)
	p.position(p.stats, 24, 170, w-48, 24)
	p.position(p.controls[idFilter], 24, 200, 270, 200)
	p.position(p.controls[idClear], w-194, 200, 170, 28)
	p.position(p.controls[idEventList], 24, 240, w-48, h-475)
	p.position(p.detail, 24, h-225, w-48, 117)
	p.position(p.controls[idDebug], 24, h-96, w-48, 23)
	p.position(p.controls[idThinking], 24, h-69, w-48, 23)
	p.position(p.privacy, 24, h-42, w-48, 36)
	for _, id := range []int{idModelList, idImport} {
		winCall("ShowWindow", p.controls[id], boolShow(!p.activity))
	}
	winCall("ShowWindow", p.notice, boolShow(!p.activity))
	for _, id := range []int{idFilter, idClear, idEventList} {
		winCall("ShowWindow", p.controls[id], boolShow(p.activity))
	}
	for _, h := range []uintptr{p.stats, p.detail} {
		winCall("ShowWindow", h, boolShow(p.activity))
	}
}
func boolShow(visible bool) uintptr {
	if visible {
		return 5
	}
	return 0
}
func winListFill(hwnd uintptr, rows []string, selected int) {
	winCall("SendMessageW", hwnd, 0xb, 0, 0)
	winCall("SendMessageW", hwnd, 0x184, 0, 0)
	for _, row := range rows {
		text := wtext(row)
		winCall("SendMessageW", hwnd, 0x180, 0, uintptr(unsafe.Pointer(text)))
		runtime.KeepAlive(text)
	}
	if selected >= 0 && selected < len(rows) {
		winCall("SendMessageW", hwnd, 0x186, uintptr(selected), 0)
	}
	winCall("SendMessageW", hwnd, 0xb, 1, 0)
	winCall("InvalidateRect", hwnd, 0, 1)
}
func (p *windowsPanel) updateImportButton() {
	index := winListSelection(p.controls[idModelList])
	enabled := p.imports == nil && index >= 0 && index < len(p.models) && p.models[index].ID != ""
	winCall("EnableWindow", p.controls[idImport], boolFlag(enabled))
}
func boolFlag(b bool) uintptr {
	if b {
		return 1
	}
	return 0
}
func (p *windowsPanel) refresh() {
	if !p.consenting {
		if approval := p.a.claimWebApproval(); approval != nil {
			p.consenting = true
			defer func() { p.consenting = false }()
			key := ""
			p.show()
			if len(approval.Models) > 0 && winMessageBox(p.hwnd, webConsentText(approval)+"\n\nPremi Sì, poi scegli il modello da autorizzare.", 0x4|0x20|0x100) == 6 {
				menu := winCall("CreatePopupMenu")
				for i, m := range approval.Models {
					winAppend(menu, 1000+i, "Consenti con "+m.Name)
				}
				winAppend(menu, 999, "Rifiuta")
				selected := p.popup(menu) - 1000
				winCall("DestroyMenu", menu)
				if selected >= 0 && selected < len(approval.Models) {
					key = approval.Models[selected].Key
				}
			} else if len(approval.Models) == 0 {
				winMessageBox(p.hwnd, webConsentText(approval)+"\n\nAvvia un modello compatibile in Ollama o LM Studio, poi riprova.", 0x40)
			}
			go p.a.answerWebConsent(approval.ID, key)
		}
	}
	if p.imports != nil {
		select {
		case message := <-p.imports:
			p.imports = nil
			winText(p.notice, message)
		default:
		}
	}
	filter := int(winCall("SendMessageW", p.controls[idFilter], 0x147, 0, 0))
	s := p.a.panelSnapshot(filter)
	winText(p.status, s.Title)
	winText(p.connection, s.Connection)
	winText(p.stats, s.Statistics)
	winText(p.privacy, s.Privacy)
	for id, enabled := range map[int]bool{idDebug: s.Debug, idThinking: s.Thinking} {
		winCall("SendMessageW", p.controls[id], 0xf1, boolFlag(enabled), 0)
	}
	if !reflect.DeepEqual(p.models, s.Models) {
		selected := panelModelSelection(p.models, s.Models, winListSelection(p.controls[idModelList]))
		rows := []string{}
		for _, m := range s.Models {
			rows = append(rows, fmt.Sprintf("%s    ·    %s    ·    %s", m.Name, m.Engine, m.Status))
		}
		p.models = s.Models
		winListFill(p.controls[idModelList], rows, selected)
	}
	if p.generation != s.Generation || !reflect.DeepEqual(p.events, s.Events) {
		selected := panelEventSelection(p.events, s.Events, winListSelection(p.controls[idEventList]), p.generation == s.Generation)
		rows := []string{}
		for _, e := range s.Events {
			rows = append(rows, eventSummary(e))
		}
		p.events, p.generation = s.Events, s.Generation
		winListFill(p.controls[idEventList], rows, selected)
		winText(p.detail, strings.ReplaceAll(eventDetail(p.events, selected), "\n", "\r\n"))
	}
	winCall("EnableWindow", p.controls[idAdd], boolFlag(p.imports == nil))
	p.updateImportButton()
}
func (a *application) runDesktopUI() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if a.ctx.Err() != nil {
		return
	}
	p := &windowsPanel{a: a, controls: map[int]uintptr{}, scale: 96}
	winPanel = p
	defer func() { winHandle.Store(0); winPanel = nil }()
	user32.NewProc("SetProcessDPIAware").Call()
	if dpi := user32.NewProc("GetDpiForSystem"); dpi.Find() == nil {
		value, _, _ := dpi.Call()
		if value > 0 {
			p.scale = int(value)
		}
	}
	p.instance, _, _ = kernel32.NewProc("GetModuleHandleW").Call(0)
	p.icon = winCall("CreateIconFromResourceEx", uintptr(unsafe.Pointer(&windowsIconPNG[0])), uintptr(len(windowsIconPNG)), 1, 0x30000, 32, 32, 0)
	if p.icon == 0 {
		desktopStartupError("Impossibile caricare l’icona di Jase Tunnel.")
		return
	}
	defer winCall("DestroyIcon", p.icon)
	name := wtext("JaseTunnelNativePanel")
	class := winClass{Style: 8, Procedure: syscall.NewCallback(winProcedure), Instance: p.instance, Icon: p.icon, SmallIcon: p.icon, Name: name, Background: 16}
	class.Size = uint32(unsafe.Sizeof(class))
	class.Cursor = winCall("LoadCursorW", 0, 32512)
	if winCall("RegisterClassExW", uintptr(unsafe.Pointer(&class))) == 0 {
		desktopStartupError("Impossibile registrare la finestra di Jase Tunnel.")
		return
	}
	defer winCall("UnregisterClassW", uintptr(unsafe.Pointer(name)), p.instance)
	title := wtext("Jase Tunnel")
	p.hwnd = winCall("CreateWindowExW", 0, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(title)), 0x00cf0000, 0x80000000, 0x80000000, uintptr(960*p.scale/96), uintptr(760*p.scale/96), 0, 0, p.instance, 0)
	runtime.KeepAlive(title)
	if p.hwnd == 0 {
		desktopStartupError("Impossibile aprire Jase Tunnel.")
		return
	}
	winHandle.Store(p.hwnd)
	face := wtext("Segoe UI")
	p.font, _, _ = gdi32.NewProc("CreateFontW").Call(uintptr(int64(-14*p.scale/96)), 0, 0, 0, 400, 0, 0, 0, 1, 0, 0, 0, 0, uintptr(unsafe.Pointer(face)))
	p.titleFont, _, _ = gdi32.NewProc("CreateFontW").Call(uintptr(int64(-22*p.scale/96)), 0, 0, 0, 600, 0, 0, 0, 1, 0, 0, 0, 0, uintptr(unsafe.Pointer(face)))
	runtime.KeepAlive(face)
	defer gdi32.NewProc("DeleteObject").Call(p.font)
	defer gdi32.NewProc("DeleteObject").Call(p.titleFont)
	p.status = p.child("STATIC", "Jase Tunnel è in esecuzione", 0, 0)
	winCall("SendMessageW", p.status, 0x30, p.titleFont, 1)
	p.connection = p.child("STATIC", "", 0, 0)
	p.stats = p.child("STATIC", "", 0, 0)
	p.privacy = p.child("STATIC", "", 0, 0)
	p.notice = p.child("STATIC", "Apri Ollama o il server locale di LM Studio. Puoi aggiungere file GGUF o cartelle di pesi compatibili.", 0, 0)
	for _, button := range []struct {
		id   int
		text string
	}{{idRevoke, "Scollega siti"}, {idOpenSite, "Apri Jase"}, {idAdd, "Aggiungi pesi…"}, {idQuit, trayLabels[2]}, {idModels, "Modelli"}, {idActivity, "Attività"}, {idImport, "Aggiungi selezionati"}, {idClear, "Svuota registro"}} {
		p.child("BUTTON", button.text, 0x10000, button.id)
	}
	p.child("BUTTON", "Log dettagliato, inclusi i testi", 0x10003, idDebug)
	p.child("BUTTON", "Ragionamento del modello, se supportato", 0x10003, idThinking)
	for _, id := range []int{idModelList, idEventList} {
		p.child("LISTBOX", "", 0x10000|0x200000|0x100000|0x800000|1|0x100, id)
		winCall("SendMessageW", p.controls[id], 0x194, 1600, 0)
	}
	p.detail = p.child("EDIT", "Seleziona un evento per leggerne i dettagli.", 0x10000|0x800000|0x200000|4|0x40|0x800, 0)
	p.child("COMBOBOX", "", 0x10000|0x200000|3, idFilter)
	for _, f := range panelFilters {
		t := wtext(f.Label)
		winCall("SendMessageW", p.controls[idFilter], 0x143, 0, uintptr(unsafe.Pointer(t)))
		runtime.KeepAlive(t)
	}
	winCall("SendMessageW", p.controls[idFilter], 0x14e, 0, 0)
	p.menu = winCall("CreatePopupMenu")
	defer winCall("DestroyMenu", p.menu)
	for i, label := range trayLabels {
		winAppend(p.menu, idOpen+i, label)
	}
	taskbar := wtext("TaskbarCreated")
	p.taskbarCreated = uint32(winCall("RegisterWindowMessageW", uintptr(unsafe.Pointer(taskbar))))
	runtime.KeepAlive(taskbar)
	if !p.addTray() {
		winCall("DestroyWindow", p.hwnd)
		desktopStartupError("Windows non riesce a mostrare l’icona di Jase Tunnel nell’area di notifica. Riapri l’app quando la barra delle applicazioni è disponibile.")
		return
	}
	p.layout()
	p.refresh()
	p.show()
	winCall("SetTimer", p.hwnd, 1, 500, 0)
	defer winCall("KillTimer", p.hwnd, 1)
	if winStop.Load() || a.ctx.Err() != nil {
		winCall("DestroyWindow", p.hwnd)
	}
	var message winMessage
	for {
		r := int32(winCall("GetMessageW", uintptr(unsafe.Pointer(&message)), 0, 0, 0))
		if r <= 0 {
			break
		}
		if winCall("IsDialogMessageW", p.hwnd, uintptr(unsafe.Pointer(&message))) == 0 {
			winCall("TranslateMessage", uintptr(unsafe.Pointer(&message)))
			winCall("DispatchMessageW", uintptr(unsafe.Pointer(&message)))
		}
	}
	runtime.KeepAlive(name)
}
