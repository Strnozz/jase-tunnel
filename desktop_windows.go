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
const desktopUIName = "win32-v2"

//go:generate node scripts/windows-resources.mjs

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
	// Latest announcement (discovery id, title, text) waiting for the window thread.
	winAnnouncement atomic.Pointer[[3]string]
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
	idNotice    = 113 // Click on the tray balloon of a compatible page.
	idRevoke    = 114
	idSiteList  = 115 // Activity tab: the sites, each with its own console.
	idRevokeOne = 116 // «Disconnect website» for the selected site.
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
	announcement                                                string
	sites                                                       []panelSite
	site                                                        string // Key of the console shown
	style                                                       windowsStyle
	section, hint                                               uintptr
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
	// Avoid repainting unchanged labels on every service poll.
	length := winCall("GetWindowTextLengthW", hwnd)
	if length == uintptr(len(syscall.StringToUTF16(text))-1) {
		current := make([]uint16, length+1)
		winCall("GetWindowTextW", hwnd, uintptr(unsafe.Pointer(&current[0])), length+1)
		if syscall.UTF16ToString(current) == text {
			return
		}
	}
	winCall("SetWindowTextW", hwnd, uintptr(unsafe.Pointer(p)))
	runtime.KeepAlive(p)
}
func (p *windowsPanel) child(class, text string, style uint32, id int) uintptr {
	c, t := wtext(class), wtext(text)
	h := winCall("CreateWindowExW", 0, uintptr(unsafe.Pointer(c)), uintptr(unsafe.Pointer(t)), uintptr(style|0x50000000), 0, 0, 1, 1, p.hwnd, uintptr(id), p.instance, 0)
	runtime.KeepAlive(c)
	runtime.KeepAlive(t)
	if h == 0 {
		panic("Could not create the native control " + class)
	}
	theme := wtext("Explorer")
	uxtheme.NewProc("SetWindowTheme").Call(h, uintptr(unsafe.Pointer(theme)), 0)
	runtime.KeepAlive(theme)
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
	return winMessageBox(0, "An earlier version of Jase Tunnel is already running. Restart it to use the new panel? Current requests will be interrupted.", 0x24|0x100) == 6
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
	case 0x405:
		return idNotice // NIN_BALLOONUSERCLICK
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

// The tray balloon (a toast on Windows 10 and later) announces a compatible page.
// Windows respects quiet hours and focus assist; the panel's journal keeps the event.
func notifyDesktop(id, title, body string) {
	winAnnouncement.Store(&[3]string{id, title, body})
}
func (p *windowsPanel) balloon(n *[3]string) {
	if !p.tray {
		return
	}
	p.announcement = n[0]
	icon := winNotifyIcon{Window: p.hwnd, ID: 1, Flags: 0x10, InfoFlags: 1 | 0x80}
	icon.Size = uint32(unsafe.Sizeof(icon))
	title, _ := syscall.UTF16FromString(n[1])
	text, _ := syscall.UTF16FromString(n[2])
	copy(icon.InfoTitle[:len(icon.InfoTitle)-1], title)
	copy(icon.Info[:len(icon.Info)-1], text)
	shell32.NewProc("Shell_NotifyIconW").Call(1, uintptr(unsafe.Pointer(&icon)))
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
	winAppend(menu, 1, "GGUF file…")
	winAppend(menu, 2, "Model directory…")
	choice := p.popup(menu)
	if choice == 0 {
		return
	}
	p.imports = p.a.importFromDesktop(func(ctx context.Context) (string, bool, error) {
		folder := choice == 2
		path, err := pickModel(ctx, folder)
		return path, folder, err
	})
	winText(p.notice, "Choose the weights in the file chooser. The import will continue in the background…")
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
	case idSiteList:
		if index := winListSelection(p.controls[idSiteList]); notification == 1 && index >= 0 && index < len(p.sites) {
			p.site = p.sites[index].Key
			p.refresh()
		}
	case idRevokeOne:
		if p.site != allSites && p.site != "" {
			go p.a.revokeSite(p.site)
		}
	case idNotice:
		if p.announcement != "" {
			go p.a.requestWebConnection(p.announcement)
			p.announcement = ""
		}
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
			winText(p.notice, "Adding weights to the local engine…")
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
		case 0xf: // WM_PAINT
			p.paint()
			return 0
		case 0x14: // WM_ERASEBKGND: paint the full surface once in WM_PAINT.
			return 1
		case 0x2b: // WM_DRAWITEM
			return p.drawItem(lparam)
		case 0x2c: // WM_MEASUREITEM (owner-drawn list rows)
			var item winMeasureItem
			winReadNative(lparam, unsafe.Pointer(&item), unsafe.Sizeof(item))
			item.Height = uint32(p.pixels(p.rowHeight(int(item.ID))))
			kernel32.NewProc("RtlMoveMemory").Call(lparam, uintptr(unsafe.Pointer(&item)), unsafe.Sizeof(item))
			return 1
		case 0x138, 0x133, 0x134: // WM_CTLCOLORSTATIC / EDIT / LISTBOX
			return p.controlColor(wparam, lparam)
		case 0x1a, 0x15, 0x31a: // settings, system colors or theme changed
			for _, brush := range []uintptr{p.style.backgroundBrush, p.style.surfaceBrush, p.style.tintBrush} {
				gdi32.NewProc("DeleteObject").Call(brush)
			}
			p.initStyle()
			winCall("RedrawWindow", hwnd, 0, 0, 0x185)
			return 0
		case 0x2e0: // WM_DPICHANGED
			p.scale = int(wparam & 0xffff)
			p.resetFonts()
			var rect winRect
			winReadNative(lparam, unsafe.Pointer(&rect), unsafe.Sizeof(rect))
			winCall("SetWindowPos", hwnd, 0, uintptr(rect.Left), uintptr(rect.Top), uintptr(rect.Right-rect.Left), uintptr(rect.Bottom-rect.Top), 0x14)
			p.layout()
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
				minimum := winPoint{X: int32(p.pixels(920)), Y: int32(p.pixels(720))}
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
			if n := winAnnouncement.Swap(nil); n != nil {
				p.balloon(n)
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
	if w < 880 {
		w = 880
	}
	if h < 640 {
		h = 640
	}
	p.position(p.status, 60, 126, w-300, 28)
	p.position(p.connection, 44, 164, w-88, 40)
	p.position(p.controls[idAdd], w-336, 28, 150, 40)
	p.position(p.controls[idQuit], w-174, 28, 150, 40)
	p.position(p.controls[idModels], 24, 228, 128, 40)
	p.position(p.controls[idActivity], 160, 228, 128, 40)
	p.position(p.controls[idRevoke], w-212, 120, 168, 36)
	sectionWidth := w - 300
	if p.activity {
		sectionWidth = 220
	}
	p.position(p.section, 44, 296, sectionWidth, 26)
	p.position(p.hint, 44, 326, w-88, 24)
	p.position(p.controls[idModelList], 36, 364, w-72, h-604)
	p.position(p.controls[idImport], 44, h-224, 156, 36)
	p.position(p.notice, 216, h-222, w-260, 44)
	p.position(p.stats, 308, 296, w-352, 24)
	p.position(p.controls[idSiteList], 36, 328, 244, h-530)
	p.position(p.controls[idFilter], 308, 332, 200, 220)
	p.position(p.controls[idRevokeOne], w-348, 330, 176, 34)
	p.position(p.controls[idClear], w-160, 330, 116, 34)
	detailHeight := max(60, min(100, (h-572)/3))
	p.position(p.controls[idEventList], 300, 380, w-336, h-584-detailHeight)
	p.position(p.detail, 308, h-192-detailHeight, w-352, detailHeight)
	p.position(p.controls[idDebug], 44, h-148, 340, 26)
	p.position(p.controls[idThinking], 44, h-110, 340, 26)
	p.position(p.privacy, 24, h-44, w-48, 34)
	for _, id := range []int{idModelList, idImport} {
		winCall("ShowWindow", p.controls[id], boolShow(!p.activity && (id != idModelList || len(p.models) > 0)))
	}
	winCall("ShowWindow", p.notice, boolShow(!p.activity))
	winCall("ShowWindow", p.hint, boolShow(!p.activity))
	winText(p.section, map[bool]string{false: "Your local models", true: "Websites"}[p.activity])
	for _, id := range []int{idFilter, idClear, idEventList, idSiteList, idRevokeOne} {
		winCall("ShowWindow", p.controls[id], boolShow(p.activity))
	}
	for _, h := range []uintptr{p.stats, p.detail} {
		winCall("ShowWindow", h, boolShow(p.activity))
	}
	for _, id := range []int{idModels, idActivity} {
		winCall("InvalidateRect", p.controls[id], 0, 1)
	}
	winCall("InvalidateRect", p.hwnd, 0, 0)
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
			// Show the requesting site's console behind the consent question.
			p.site, p.activity, p.sites = approval.Origin, true, nil
			p.layout()
			p.refresh()
			if len(approval.Models) > 0 && winMessageBox(p.hwnd, webConsentText(approval)+"\n\nClick Yes, then choose the model to allow.", 0x4|0x20|0x100) == 6 {
				menu := winCall("CreatePopupMenu")
				for i, m := range approval.Models {
					winAppend(menu, 1000+i, "Allow con "+m.Name)
				}
				winAppend(menu, 999, "Decline")
				selected := p.popup(menu) - 1000
				winCall("DestroyMenu", menu)
				if selected >= 0 && selected < len(approval.Models) {
					key = approval.Models[selected].Key
				}
			} else if len(approval.Models) == 0 {
				winMessageBox(p.hwnd, webConsentText(approval)+"\n\nStart a compatible model in Ollama or LM Studio, then try again.", 0x40)
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
	s := p.a.panelSnapshot(filter, p.site)
	if !reflect.DeepEqual(p.sites, s.Sites) {
		selected := 0
		for i, site := range s.Sites {
			if site.Key == p.site {
				selected = i
			}
		}
		rows := []string{}
		for _, site := range s.Sites {
			mark := "    "
			if site.Open {
				mark = "●  "
			}
			rows = append(rows, mark+site.Label+"  —  "+site.Status)
		}
		p.sites, p.site = s.Sites, s.Sites[selected].Key
		winListFill(p.controls[idSiteList], rows, selected)
	}
	open := false
	for _, site := range p.sites {
		open = open || site.Key == p.site && site.Open
	}
	winCall("EnableWindow", p.controls[idRevokeOne], boolFlag(open))
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
		label := "models"
		if len(p.models) == 1 {
			label = "model"
		}
		winText(p.hint, fmt.Sprintf("%d %s detected on this computer", len(p.models), label))
		winCall("ShowWindow", p.controls[idModelList], boolShow(!p.activity && len(p.models) > 0))
		winCall("InvalidateRect", p.hwnd, 0, 0)
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
	p := &windowsPanel{a: a, controls: map[int]uintptr{}, scale: 96, site: allSites}
	winPanel = p
	defer func() { winHandle.Store(0); winPanel = nil }()
	// The embedded manifest selects per-monitor v2. This also covers go run.
	if dpi := user32.NewProc("SetProcessDpiAwarenessContext"); dpi.Find() == nil {
		dpi.Call(^uintptr(3)) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 (-4)
	}
	if dpi := user32.NewProc("GetDpiForSystem"); dpi.Find() == nil {
		value, _, _ := dpi.Call()
		if value > 0 {
			p.scale = int(value)
		}
	}
	p.instance, _, _ = kernel32.NewProc("GetModuleHandleW").Call(0)
	p.icon = p.logoIcon(32)
	if p.icon == 0 {
		desktopStartupError("Could not load the Jase Tunnel icon.")
		return
	}
	defer winCall("DestroyIcon", p.icon)
	p.initStyle()
	defer p.disposeStyle()
	name := wtext("JaseTunnelNativePanel")
	class := winClass{Style: 8, Procedure: syscall.NewCallback(winProcedure), Instance: p.instance, Icon: p.icon, SmallIcon: p.icon, Name: name}
	class.Size = uint32(unsafe.Sizeof(class))
	class.Cursor = winCall("LoadCursorW", 0, 32512)
	if winCall("RegisterClassExW", uintptr(unsafe.Pointer(&class))) == 0 {
		desktopStartupError("Could not register the Jase Tunnel window.")
		return
	}
	defer winCall("UnregisterClassW", uintptr(unsafe.Pointer(name)), p.instance)
	title := wtext("Jase Tunnel")
	p.hwnd = winCall("CreateWindowExW", 0, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(title)), 0x00cf0000|0x02000000, 0x80000000, 0x80000000, uintptr(p.pixels(1080)), uintptr(p.pixels(860)), 0, 0, p.instance, 0)
	runtime.KeepAlive(title)
	if p.hwnd == 0 {
		desktopStartupError("Could not open Jase Tunnel.")
		return
	}
	winHandle.Store(p.hwnd)
	corner := uint32(2) // DWMWCP_ROUND on Windows 11; ignored on Windows 10.
	dwmapi.NewProc("DwmSetWindowAttribute").Call(p.hwnd, 33, uintptr(unsafe.Pointer(&corner)), 4)
	p.resetFonts()
	p.status = p.child("STATIC", "Jase Tunnel is running", 0, 0)
	winCall("SendMessageW", p.status, 0x30, p.titleFont, 1)
	p.connection = p.child("STATIC", "", 0, 0)
	p.stats = p.child("STATIC", "", 0, 0)
	p.privacy = p.child("STATIC", "", 0, 0)
	p.notice = p.child("STATIC", "Open Ollama or the LM Studio local server. You can add GGUF files or compatible weights directories.", 0, 0)
	p.section = p.child("STATIC", "Your local models", 0, 0)
	p.hint = p.child("STATIC", "No models detected yet", 0, 0)
	winCall("SendMessageW", p.section, 0x30, p.style.semibold, 1)
	for _, button := range []struct {
		id   int
		text string
	}{{idRevoke, "Disconnect websites"}, {idRevokeOne, "Disconnect website"}, {idAdd, "Add weights…"}, {idQuit, trayLabels[2]}, {idModels, "Models"}, {idActivity, "Activity"}, {idImport, "Add selected"}, {idClear, "Clear log"}} {
		p.child("BUTTON", button.text, 0x10000|0xb, button.id) // BS_OWNERDRAW
	}
	p.child("BUTTON", "Detailed log, including text", 0x10003, idDebug)
	p.child("BUTTON", "Model reasoning, if supported", 0x10003, idThinking)
	for _, id := range []int{idModelList, idEventList, idSiteList} {
		p.child("LISTBOX", "", 0x10000|0x200000|1|0x100|0x10|0x40, id) // strings + fixed owner-drawn rows
		winCall("SendMessageW", p.controls[id], 0x1a0, 0, uintptr(p.pixels(p.rowHeight(id))))
	}
	p.detail = p.child("EDIT", "Select an event to view its details.", 0x10000|0x200000|4|0x40|0x800, 0)
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
		desktopStartupError("Windows could not show the Jase Tunnel tray icon. Open the app again when the taskbar is available.")
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
