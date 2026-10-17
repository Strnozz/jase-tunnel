package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"unicode"
	"unsafe"

	"github.com/ebitengine/purego"
)

const desktopAvailable = true
const desktopUIName = "gtk3-v1"

//go:embed assets/desktop-icon.png
var linuxIconPNG []byte

// GTK is loaded from the desktop system. No compiler, browser runtime, network
// service or CGO toolchain is required on the user's computer.
type gtkAPI struct {
	GtkInitCheck                func(uintptr, uintptr) int32
	GtkMain                     func()
	GtkMainQuit                 func()
	GtkMainLevel                func() uint32
	GtkWindowNew                func(int32) uintptr
	GtkWindowSetTitle           func(uintptr, string)
	GtkWindowSetDefaultSize     func(uintptr, int32, int32)
	GtkWindowSetIconFromFile    func(uintptr, string, uintptr) int32
	GtkWindowPresent            func(uintptr)
	GtkWindowIconify            func(uintptr)
	GtkWidgetShowAll            func(uintptr)
	GtkWidgetHide               func(uintptr)
	GtkWidgetDestroy            func(uintptr)
	GtkWidgetSetSensitive       func(uintptr, int32)
	GtkWidgetSetSizeRequest     func(uintptr, int32, int32)
	GtkWidgetSetMarginTop       func(uintptr, int32)
	GtkWidgetSetMarginBottom    func(uintptr, int32)
	GtkWidgetSetMarginStart     func(uintptr, int32)
	GtkWidgetSetMarginEnd       func(uintptr, int32)
	GtkContainerAdd             func(uintptr, uintptr)
	GtkContainerSetBorderWidth  func(uintptr, uint32)
	GtkContainerForeach         func(uintptr, uintptr, uintptr)
	GtkBoxNew                   func(int32, int32) uintptr
	GtkBoxPackStart             func(uintptr, uintptr, int32, int32, uint32)
	GtkLabelNew                 func(string) uintptr
	GtkLabelSetText             func(uintptr, string)
	GtkLabelSetXalign           func(uintptr, float32)
	GtkLabelSetLineWrap         func(uintptr, int32)
	GtkLabelSetSelectable       func(uintptr, int32)
	GtkButtonNewWithLabel       func(string) uintptr
	GtkCheckButtonNewWithLabel  func(string) uintptr
	GtkToggleButtonSetActive    func(uintptr, int32)
	GtkToggleButtonGetActive    func(uintptr) int32
	GtkNotebookNew              func() uintptr
	GtkNotebookAppendPage       func(uintptr, uintptr, uintptr) int32
	GtkScrolledWindowNew        func(uintptr, uintptr) uintptr
	GtkScrolledWindowSetPolicy  func(uintptr, int32, int32)
	GtkListBoxNew               func() uintptr
	GtkListBoxGetSelectedRow    func(uintptr) uintptr
	GtkListBoxGetRowAtIndex     func(uintptr, int32) uintptr
	GtkListBoxRowGetIndex       func(uintptr) int32
	GtkListBoxSelectRow         func(uintptr, uintptr)
	GtkComboBoxTextNew          func() uintptr
	GtkComboBoxTextAppendText   func(uintptr, string)
	GtkComboBoxSetActive        func(uintptr, int32)
	GtkComboBoxGetActive        func(uintptr) int32
	GtkTextViewNew              func() uintptr
	GtkTextViewSetEditable      func(uintptr, int32)
	GtkTextViewSetWrapMode      func(uintptr, int32)
	GtkTextViewGetBuffer        func(uintptr) uintptr
	GtkTextBufferSetText        func(uintptr, string, int32)
	GtkMenuNew                  func() uintptr
	GtkMenuItemNewWithLabel     func(string) uintptr
	GtkMenuShellAppend          func(uintptr, uintptr)
	GtkMenuPopup                func(uintptr, uintptr, uintptr, uintptr, uintptr, uint32, uint32)
	GtkGetCurrentEventTime      func() uint32
	GtkStatusIconNewFromFile    func(string) uintptr
	GtkStatusIconSetVisible     func(uintptr, int32)
	GtkStatusIconSetTooltipText func(uintptr, string)
	GtkStatusIconIsEmbedded     func(uintptr) int32
	GtkDialogNew                func() uintptr
	GtkDialogAddButton          func(uintptr, string, int32) uintptr
	GtkDialogGetContentArea     func(uintptr) uintptr
	GtkDialogRun                func(uintptr) int32
	GtkDialogResponse           func(uintptr, int32)
	GtkWindowSetTransientFor    func(uintptr, uintptr)
	GtkWindowSetModal           func(uintptr, int32)
	GtkFileChooserDialogNew     func(string, uintptr, int32, uintptr) uintptr
	GtkFileChooserGetFilename   func(uintptr) unsafe.Pointer
	GtkFileChooserAddFilter     func(uintptr, uintptr)
	GtkFileFilterNew            func() uintptr
	GtkFileFilterSetName        func(uintptr, string)
	GtkFileFilterAddPattern     func(uintptr, string)
	GSignalConnectData          func(uintptr, string, uintptr, uintptr, uintptr, uint32) uint64
	GTimeoutAdd                 func(uint32, uintptr, uintptr) uint32
	GSourceRemove               func(uint32) int32
	GObjectUnref                func(uintptr)
	GObjectGet                  func(uintptr, string, *int32, uintptr)
	GFree                       func(unsafe.Pointer)
}

var gtk gtkAPI
var gtkLoaded bool
var linuxShow, linuxStop atomic.Bool

func init() { runtime.LockOSThread() }
func nativeSymbol(name string) string {
	var s strings.Builder
	for i, c := range name {
		if i > 0 && unicode.IsUpper(c) {
			s.WriteByte('_')
		}
		s.WriteRune(unicode.ToLower(c))
	}
	return s.String()
}
func prepareDesktopUI() (err error) {
	if gtkLoaded {
		return nil
	}
	defer func() {
		if failure := recover(); failure != nil {
			err = fmt.Errorf("Interfaccia GTK 3 non disponibile: %v", failure)
		}
	}()
	lib, err := purego.Dlopen("libgtk-3.so.0", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return errors.New("Per aprire Jase Tunnel serve GTK 3 (su Ubuntu/Debian: libgtk-3-0 o libgtk-3-0t64). Il tunnel non è stato avviato in background.")
	}
	fields := reflect.ValueOf(&gtk).Elem()
	for i := 0; i < fields.NumField(); i++ {
		purego.RegisterLibFunc(fields.Field(i).Addr().Interface(), lib, nativeSymbol(fields.Type().Field(i).Name))
	}
	if gtk.GtkInitCheck(0, 0) == 0 {
		return errors.New("Non è disponibile una sessione grafica. Avvia Jase Tunnel dal desktop oppure usa --headless --foreground per la diagnostica.")
	}
	gtkLoaded = true
	return nil
}
func showDesktopUI() { linuxShow.Store(true) }
func stopDesktopUI() { linuxStop.Store(true) }
func gtkDialog(parent uintptr, message string, confirm bool) bool {
	d := gtk.GtkDialogNew()
	gtk.GtkWindowSetTitle(d, "Jase Tunnel")
	gtk.GtkWindowSetDefaultSize(d, 480, 160)
	gtk.GtkWindowSetModal(d, 1)
	if parent != 0 {
		gtk.GtkWindowSetTransientFor(d, parent)
	}
	label := gtk.GtkLabelNew(message)
	gtk.GtkLabelSetLineWrap(label, 1)
	gtk.GtkLabelSetSelectable(label, 1)
	area := gtk.GtkDialogGetContentArea(d)
	gtk.GtkContainerSetBorderWidth(area, 20)
	gtk.GtkContainerAdd(area, label)
	if confirm {
		gtk.GtkDialogAddButton(d, "Annulla", -6)
		gtk.GtkDialogAddButton(d, "Riavvia Jase Tunnel", -3)
	} else {
		gtk.GtkDialogAddButton(d, "Chiudi", -7)
	}
	gtk.GtkWidgetShowAll(d)
	response := gtk.GtkDialogRun(d)
	gtk.GtkWidgetDestroy(d)
	return response == -3
}
func confirmDesktopReplacement() bool {
	return gtkDialog(0, "Una versione precedente di Jase Tunnel è già aperta. Riavviala per usare il nuovo pannello. Le richieste in corso verranno interrotte.", true)
}
func desktopStartupError(message string) {
	if gtkLoaded {
		gtkDialog(0, message, false)
		return
	}
	log.Print(message)
	fmt.Fprintln(os.Stderr, message)
	if path, err := exec.LookPath("zenity"); err == nil {
		_ = exec.Command(path, "--error", "--title=Jase Tunnel", "--text="+message).Run()
	}
}

type linuxPanel struct {
	a                                                                                                                           *application
	window, status, connection, statistics, privacy, notice, models, events, detail, filter, debug, thinking, add, importButton uintptr
	menu, weightsMenu, indicator, statusIcon, dialog                                                                            uintptr
	indicatorConnected                                                                                                          func() bool
	modelRows                                                                                                                   []panelModel
	activityRows                                                                                                                []activityEvent
	generation                                                                                                                  uint64
	imports                                                                                                                     <-chan string
	updating                                                                                                                    bool
	destroyChildren                                                                                                             uintptr
}

func gtkConnect(object uintptr, signal string, callback any) {
	gtk.GSignalConnectData(object, signal, purego.NewCallback(callback), 0, 0, 0)
}
func gtkLabel(text string) uintptr {
	h := gtk.GtkLabelNew(text)
	gtk.GtkLabelSetXalign(h, 0)
	gtk.GtkLabelSetLineWrap(h, 1)
	return h
}
func gtkPack(box, child uintptr, expand bool) {
	var e int32
	if expand {
		e = 1
	}
	gtk.GtkBoxPackStart(box, child, e, e, 0)
}
func gtkButton(box uintptr, label string, action func()) uintptr {
	h := gtk.GtkButtonNewWithLabel(label)
	gtkPack(box, h, false)
	gtkConnect(h, "clicked", func(uintptr, uintptr) { action() })
	return h
}
func gtkScroll(child uintptr, height int32) uintptr {
	h := gtk.GtkScrolledWindowNew(0, 0)
	gtk.GtkScrolledWindowSetPolicy(h, 1, 1)
	gtk.GtkWidgetSetSizeRequest(h, -1, height)
	gtk.GtkContainerAdd(h, child)
	return h
}
func (p *linuxPanel) show() { gtk.GtkWidgetShowAll(p.window); gtk.GtkWindowPresent(p.window) }
func (p *linuxPanel) hasTray() bool {
	if p.indicatorConnected != nil {
		return p.indicatorConnected()
	}
	return p.statusIcon != 0 && gtk.GtkStatusIconIsEmbedded(p.statusIcon) != 0
}
func (p *linuxPanel) setupTray(iconPath string) {
	for _, name := range []string{"libayatana-appindicator3.so.1", "libappindicator3.so.1"} {
		lib, err := purego.Dlopen(name, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			continue
		}
		var create func(string, string, int32) uintptr
		var setMenu func(uintptr, uintptr)
		var setStatus func(uintptr, int32)
		// An optional integration may be absent on some desktops; GTK's tray is
		// the fallback. A missing host never makes the last window disappear.
		func() {
			defer func() { _ = recover() }()
			purego.RegisterLibFunc(&create, lib, "app_indicator_new")
			purego.RegisterLibFunc(&setMenu, lib, "app_indicator_set_menu")
			purego.RegisterLibFunc(&setStatus, lib, "app_indicator_set_status")
		}()
		if create == nil || setMenu == nil || setStatus == nil {
			continue
		}
		p.indicator = create("jase-tunnel", iconPath, 0)
		if p.indicator == 0 {
			continue
		}
		setMenu(p.indicator, p.menu)
		setStatus(p.indicator, 1)
		p.indicatorConnected = func() bool {
			var connected int32
			gtk.GObjectGet(p.indicator, "connected", &connected, 0)
			return connected != 0
		}
		return
	}
	p.statusIcon = gtk.GtkStatusIconNewFromFile(iconPath)
	gtk.GtkStatusIconSetVisible(p.statusIcon, 1)
	gtk.GtkStatusIconSetTooltipText(p.statusIcon, "Jase Tunnel · in esecuzione")
	gtkConnect(p.statusIcon, "activate", func(uintptr, uintptr) { p.show() })
	gtkConnect(p.statusIcon, "popup-menu", func(_ uintptr, button, time uint32, _ uintptr) { gtk.GtkMenuPopup(p.menu, 0, 0, 0, 0, button, time) })
}
func (p *linuxPanel) chooseWeights() {
	if p.imports != nil || p.dialog != 0 {
		p.show()
		return
	}
	p.show()
	gtk.GtkMenuPopup(p.weightsMenu, 0, 0, 0, 0, 0, gtk.GtkGetCurrentEventTime())
}
func (p *linuxPanel) selectWeights(folder bool) {
	if p.imports != nil || p.dialog != 0 {
		return
	}
	if !p.a.importMu.TryLock() {
		gtk.GtkLabelSetText(p.notice, "Un’importazione è già in corso. Attendi che termini.")
		return
	}
	locked := true
	defer func() {
		if locked {
			p.a.importMu.Unlock()
		}
	}()
	// The native modal selector stays on GTK's thread. Only the import and
	// engine refresh leave the GUI thread; their completion is polled below.
	action := int32(0)
	if folder {
		action = 2
	}
	d := gtk.GtkFileChooserDialogNew("Aggiungi pesi a Jase Tunnel", p.window, action, 0)
	p.dialog = d
	gtk.GtkDialogAddButton(d, "Annulla", -6)
	gtk.GtkDialogAddButton(d, "Aggiungi", -3)
	if !folder {
		filter := gtk.GtkFileFilterNew()
		gtk.GtkFileFilterSetName(filter, "File GGUF")
		gtk.GtkFileFilterAddPattern(filter, "*.gguf")
		gtk.GtkFileFilterAddPattern(filter, "*.GGUF")
		gtk.GtkFileChooserAddFilter(d, filter)
	}
	response := gtk.GtkDialogRun(d)
	var path string
	if response == -3 {
		raw := gtk.GtkFileChooserGetFilename(d)
		if raw != nil {
			for length := 0; length < 4096; length++ {
				if *(*byte)(unsafe.Add(raw, length)) == 0 {
					path = string(unsafe.Slice((*byte)(raw), length))
					break
				}
			}
			gtk.GFree(raw)
		}
	}
	gtk.GtkWidgetDestroy(d)
	p.dialog = 0
	if path == "" {
		return
	}
	p.a.importMu.Unlock()
	locked = false
	p.imports = p.a.importFromDesktop(func(context.Context) (string, bool, error) { return path, folder, nil })
	gtk.GtkLabelSetText(p.notice, "Aggiungo i pesi al motore locale…")
	p.refresh()
}
func (p *linuxPanel) importSelected() {
	row := gtk.GtkListBoxGetSelectedRow(p.models)
	if row == 0 || p.imports != nil || p.dialog != 0 {
		return
	}
	index := int(gtk.GtkListBoxRowGetIndex(row))
	if index >= 0 && index < len(p.modelRows) && p.modelRows[index].ID != "" {
		p.imports = p.a.importDetectedFromDesktop(p.modelRows[index].ID)
		gtk.GtkLabelSetText(p.notice, "Aggiungo i pesi al motore locale…")
		p.refresh()
	}
}
func (p *linuxPanel) selected(list uintptr) int {
	row := gtk.GtkListBoxGetSelectedRow(list)
	if row == 0 {
		return -1
	}
	return int(gtk.GtkListBoxRowGetIndex(row))
}
func (p *linuxPanel) updateImportButton() {
	i := p.selected(p.models)
	var enabled int32
	if p.imports == nil && p.dialog == 0 && i >= 0 && i < len(p.modelRows) && p.modelRows[i].ID != "" {
		enabled = 1
	}
	gtk.GtkWidgetSetSensitive(p.importButton, enabled)
}
func (p *linuxPanel) detailText(index int) {
	gtk.GtkTextBufferSetText(gtk.GtkTextViewGetBuffer(p.detail), eventDetail(p.activityRows, index), -1)
}
func (p *linuxPanel) fill(list uintptr, rows []string, selected int) {
	gtk.GtkContainerForeach(list, p.destroyChildren, 0)
	for _, row := range rows {
		label := gtkLabel(row)
		gtk.GtkWidgetSetMarginTop(label, 9)
		gtk.GtkWidgetSetMarginBottom(label, 9)
		gtk.GtkWidgetSetMarginStart(label, 9)
		gtk.GtkWidgetSetMarginEnd(label, 9)
		gtk.GtkContainerAdd(list, label)
	}
	gtk.GtkWidgetShowAll(list)
	if selected >= 0 {
		gtk.GtkListBoxSelectRow(list, gtk.GtkListBoxGetRowAtIndex(list, int32(selected)))
	}
}
func (p *linuxPanel) refresh() {
	if p.updating {
		return
	}
	p.updating = true
	defer func() { p.updating = false }()
	if p.imports != nil {
		select {
		case message := <-p.imports:
			p.imports = nil
			gtk.GtkLabelSetText(p.notice, message)
		default:
		}
	}
	s := p.a.panelSnapshot(int(gtk.GtkComboBoxGetActive(p.filter)))
	gtk.GtkLabelSetText(p.status, s.Title)
	gtk.GtkLabelSetText(p.connection, s.Connection)
	gtk.GtkLabelSetText(p.statistics, s.Statistics)
	gtk.GtkLabelSetText(p.privacy, s.Privacy)
	for control, on := range map[uintptr]bool{p.debug: s.Debug, p.thinking: s.Thinking} {
		var active int32
		if on {
			active = 1
		}
		gtk.GtkToggleButtonSetActive(control, active)
	}
	if !reflect.DeepEqual(p.modelRows, s.Models) {
		selected := panelModelSelection(p.modelRows, s.Models, p.selected(p.models))
		p.modelRows = s.Models
		rows := []string{}
		for _, m := range s.Models {
			rows = append(rows, fmt.Sprintf("%s    ·    %s    ·    %s", m.Name, m.Engine, m.Status))
		}
		p.fill(p.models, rows, selected)
	}
	if p.generation != s.Generation || !reflect.DeepEqual(p.activityRows, s.Events) {
		selected := panelEventSelection(p.activityRows, s.Events, p.selected(p.events), p.generation == s.Generation)
		rows := []string{}
		for _, e := range s.Events {
			rows = append(rows, eventSummary(e))
		}
		p.activityRows, p.generation = s.Events, s.Generation
		p.fill(p.events, rows, selected)
		p.detailText(selected)
	}
	var enabled int32
	if p.imports == nil && p.dialog == 0 {
		enabled = 1
	}
	gtk.GtkWidgetSetSensitive(p.add, enabled)
	p.updateImportButton()
}
func (a *application) runDesktopUI() {
	if a.ctx.Err() != nil {
		return
	}
	p := &linuxPanel{a: a}
	p.window = gtk.GtkWindowNew(0)
	gtk.GtkWindowSetTitle(p.window, "Jase Tunnel")
	gtk.GtkWindowSetDefaultSize(p.window, 900, 740)
	defer gtk.GtkWidgetDestroy(p.window)
	icon := filepath.Join(a.stateDir, "desktop-icon.png")
	if err := os.WriteFile(icon, linuxIconPNG, 0600); err != nil {
		desktopStartupError("Impossibile preparare l’icona nella cartella privata di Jase Tunnel.")
		return
	}
	gtk.GtkWindowSetIconFromFile(p.window, icon, 0)
	root := gtk.GtkBoxNew(1, 12)
	gtk.GtkContainerSetBorderWidth(root, 20)
	gtk.GtkContainerAdd(p.window, root)
	p.status = gtkLabel("Jase Tunnel è in esecuzione")
	p.connection = gtkLabel("")
	gtkPack(root, p.status, false)
	gtkPack(root, p.connection, false)
	buttons := gtk.GtkBoxNew(0, 10)
	gtkPack(root, buttons, false)
	gtkButton(buttons, "Apri Jase", func() { go a.openJase() })
	p.add = gtkButton(buttons, "Aggiungi pesi…", p.chooseWeights)
	gtkButton(buttons, trayLabels[2], a.cancel)
	tabs := gtk.GtkNotebookNew()
	gtkPack(root, tabs, true)
	models := gtk.GtkBoxNew(1, 10)
	gtk.GtkContainerSetBorderWidth(models, 14)
	p.models = gtk.GtkListBoxNew()
	gtkPack(models, gtkLabel("Modelli e pesi trovati sul computer"), false)
	gtkPack(models, gtkScroll(p.models, 180), true)
	modelButtons := gtk.GtkBoxNew(0, 10)
	gtkPack(models, modelButtons, false)
	p.importButton = gtkButton(modelButtons, "Aggiungi selezionati", p.importSelected)
	p.notice = gtkLabel("Apri Ollama o il server locale di LM Studio. Puoi aggiungere file GGUF o cartelle di pesi compatibili.")
	gtkPack(models, p.notice, false)
	gtk.GtkNotebookAppendPage(tabs, models, gtk.GtkLabelNew("Modelli"))
	activity := gtk.GtkBoxNew(1, 10)
	gtk.GtkContainerSetBorderWidth(activity, 14)
	p.statistics = gtkLabel("")
	gtkPack(activity, p.statistics, false)
	tools := gtk.GtkBoxNew(0, 10)
	gtkPack(activity, tools, false)
	p.filter = gtk.GtkComboBoxTextNew()
	for _, filter := range panelFilters {
		gtk.GtkComboBoxTextAppendText(p.filter, filter.Label)
	}
	gtk.GtkComboBoxSetActive(p.filter, 0)
	gtkPack(tools, p.filter, false)
	gtkButton(tools, "Svuota registro", func() { a.clearActivity(); p.refresh() })
	p.events = gtk.GtkListBoxNew()
	gtkPack(activity, gtkScroll(p.events, 160), true)
	p.detail = gtk.GtkTextViewNew()
	gtk.GtkTextViewSetEditable(p.detail, 0)
	gtk.GtkTextViewSetWrapMode(p.detail, 2)
	gtkPack(activity, gtkScroll(p.detail, 110), false)
	p.detailText(-1)
	gtk.GtkNotebookAppendPage(tabs, activity, gtk.GtkLabelNew("Attività"))
	p.debug = gtk.GtkCheckButtonNewWithLabel("Log dettagliato, inclusi i testi")
	p.thinking = gtk.GtkCheckButtonNewWithLabel("Ragionamento del modello, se supportato")
	gtkPack(root, p.debug, false)
	gtkPack(root, p.thinking, false)
	p.privacy = gtkLabel("")
	gtkPack(root, p.privacy, false)
	gtkPack(root, gtkLabel("Jase Tunnel · "+version), false)
	gtkConnect(p.debug, "toggled", func(uintptr, uintptr) {
		if !p.updating {
			a.setDetailedLogging(gtk.GtkToggleButtonGetActive(p.debug) != 0)
			p.refresh()
		}
	})
	gtkConnect(p.thinking, "toggled", func(uintptr, uintptr) {
		if !p.updating {
			a.setModelThinking(gtk.GtkToggleButtonGetActive(p.thinking) != 0)
			p.refresh()
		}
	})
	gtkConnect(p.filter, "changed", func(uintptr, uintptr) { p.refresh() })
	gtkConnect(p.events, "row-selected", func(_ uintptr, row uintptr, _ uintptr) {
		if !p.updating {
			index := -1
			if row != 0 {
				index = int(gtk.GtkListBoxRowGetIndex(row))
			}
			p.detailText(index)
		}
	})
	gtkConnect(p.models, "row-selected", func(uintptr, uintptr, uintptr) {
		if !p.updating {
			p.updateImportButton()
		}
	})
	gtkConnect(p.models, "row-activated", func(uintptr, uintptr, uintptr) { p.importSelected() })
	p.destroyChildren = purego.NewCallback(func(child, _ uintptr) { gtk.GtkWidgetDestroy(child) })
	p.menu = gtk.GtkMenuNew()
	for i, label := range trayLabels {
		item := gtk.GtkMenuItemNewWithLabel(label)
		gtk.GtkMenuShellAppend(p.menu, item)
		action := i
		gtkConnect(item, "activate", func(uintptr, uintptr) {
			switch action {
			case 0:
				p.show()
			case 1:
				p.chooseWeights()
			case 2:
				a.cancel()
			}
		})
	}
	gtk.GtkWidgetShowAll(p.menu)
	p.setupTray(icon)
	defer func() {
		if p.statusIcon != 0 {
			gtk.GtkStatusIconSetVisible(p.statusIcon, 0)
			gtk.GObjectUnref(p.statusIcon)
		}
		if p.indicator != 0 {
			gtk.GObjectUnref(p.indicator)
		}
	}()
	p.weightsMenu = gtk.GtkMenuNew()
	for i, label := range []string{"File GGUF…", "Cartella modello…"} {
		item := gtk.GtkMenuItemNewWithLabel(label)
		gtk.GtkMenuShellAppend(p.weightsMenu, item)
		folder := i == 1
		gtkConnect(item, "activate", func(uintptr, uintptr) { p.selectWeights(folder) })
	}
	gtk.GtkWidgetShowAll(p.weightsMenu)
	defer gtk.GtkWidgetDestroy(p.weightsMenu)
	gtkConnect(p.window, "delete-event", func(uintptr, uintptr, uintptr) int32 {
		if p.hasTray() {
			gtk.GtkWidgetHide(p.window)
		} else {
			gtk.GtkWindowIconify(p.window)
		}
		return 1
	})
	timer := gtk.GTimeoutAdd(300, purego.NewCallback(func(uintptr) int32 {
		if linuxStop.Load() || a.ctx.Err() != nil {
			if p.dialog != 0 {
				gtk.GtkDialogResponse(p.dialog, -6)
			}
			gtk.GtkMainQuit()
			return 1
		}
		if linuxShow.Swap(false) {
			p.show()
		}
		p.refresh()
		return 1
	}), 0)
	defer gtk.GSourceRemove(timer)
	p.refresh()
	p.show()
	gtk.GtkMain()
}
