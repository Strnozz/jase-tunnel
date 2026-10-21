package main

import (
	"runtime"
	"syscall"
	"unsafe"
)

var (
	uxtheme = syscall.NewLazyDLL("uxtheme.dll")
	dwmapi  = syscall.NewLazyDLL("dwmapi.dll")
)

type windowsStyle struct {
	background, surface, text, muted, border, accent, tint, green uintptr
	backgroundBrush, surfaceBrush, tintBrush                      uintptr
	semibold, small, brandFont                                    uintptr
	logo, emptyLogo                                               uintptr
}

type winPaint struct {
	DC              uintptr
	Erase           int32
	Rect            winRect
	Restore, Update int32
	Reserved        [32]byte
}

type winDrawItem struct {
	Type, ID, Item, Action, State uint32
	Window, DC                    uintptr
	Rect                          winRect
	Data                          uintptr
}

type winMeasureItem struct {
	Type, ID, Item, Width, Height uint32
	Data                          uintptr
}

// Callback arguments point at Windows-owned memory. Copy rather than retaining
// a converted uintptr on the Go stack (also safe with checkptr / race builds).
func winReadNative(address uintptr, target unsafe.Pointer, size uintptr) {
	if address != 0 {
		kernel32.NewProc("RtlMoveMemory").Call(uintptr(target), address, size)
	}
}

func winRGB(rgb uint32) uintptr {
	return uintptr((rgb&0xff)<<16 | (rgb & 0xff00) | (rgb>>16)&0xff)
}

func (p *windowsPanel) pixels(value int) int { return value * p.scale / 96 }

func (p *windowsPanel) initStyle() {
	s := &p.style
	s.background, s.surface = winRGB(0xf5f6f8), winRGB(0xffffff)
	s.text, s.muted, s.border = winRGB(0x20242c), winRGB(0x656c78), winRGB(0xe4e7ec)
	s.accent, s.tint, s.green = winRGB(0xe44400), winRGB(0xfff3ec), winRGB(0x18845b)
	var contrast struct {
		Size, Flags uint32
		Scheme      uintptr
	}
	contrast.Size = uint32(unsafe.Sizeof(contrast))
	winCall("SystemParametersInfoW", 0x42, uintptr(contrast.Size), uintptr(unsafe.Pointer(&contrast)), 0)
	if contrast.Flags&1 != 0 {
		s.surface = winCall("GetSysColor", 5)
		s.background = s.surface
		s.text = winCall("GetSysColor", 8)
		s.muted, s.border, s.green = s.text, s.text, s.text
		s.accent, s.tint = winCall("GetSysColor", 13), s.surface
	}
	s.backgroundBrush, _, _ = gdi32.NewProc("CreateSolidBrush").Call(s.background)
	s.surfaceBrush, _, _ = gdi32.NewProc("CreateSolidBrush").Call(s.surface)
	s.tintBrush, _, _ = gdi32.NewProc("CreateSolidBrush").Call(s.tint)
}

func (p *windowsPanel) disposeStyle() {
	for _, icon := range []uintptr{p.style.logo, p.style.emptyLogo} {
		winCall("DestroyIcon", icon)
	}
	for _, object := range []uintptr{p.font, p.titleFont, p.style.semibold, p.style.small, p.style.brandFont,
		p.style.backgroundBrush, p.style.surfaceBrush, p.style.tintBrush} {
		if object != 0 {
			gdi32.NewProc("DeleteObject").Call(object)
		}
	}
}

func (p *windowsPanel) newFont(size, weight int) uintptr {
	face := wtext("Segoe UI")
	font, _, _ := gdi32.NewProc("CreateFontW").Call(uintptr(int64(-p.pixels(size))), 0, 0, 0, uintptr(weight), 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(face)))
	runtime.KeepAlive(face)
	return font
}

func (p *windowsPanel) logoIcon(size int) uintptr {
	return winCall("CreateIconFromResourceEx", uintptr(unsafe.Pointer(&windowsIconPNG[0])), uintptr(len(windowsIconPNG)), 1, 0x30000, uintptr(p.pixels(size)), uintptr(p.pixels(size)), 0)
}

func (p *windowsPanel) resetFonts() {
	for _, icon := range []uintptr{p.style.logo, p.style.emptyLogo} {
		if icon != 0 {
			winCall("DestroyIcon", icon)
		}
	}
	p.style.logo, p.style.emptyLogo = p.logoIcon(48), p.logoIcon(40)
	old := []uintptr{p.font, p.titleFont, p.style.semibold, p.style.small, p.style.brandFont}
	p.font, p.titleFont = p.newFont(14, 400), p.newFont(19, 600)
	p.style.semibold, p.style.small, p.style.brandFont = p.newFont(15, 600), p.newFont(13, 400), p.newFont(25, 600)
	for _, h := range p.controls {
		winCall("SendMessageW", h, 0x30, p.font, 1)
	}
	for _, h := range []uintptr{p.connection, p.stats, p.notice, p.detail, p.hint, p.privacy} {
		winCall("SendMessageW", h, 0x30, p.font, 1)
	}
	winCall("SendMessageW", p.status, 0x30, p.titleFont, 1)
	winCall("SendMessageW", p.section, 0x30, p.style.semibold, 1)
	for _, id := range []int{idModelList, idSiteList, idEventList} {
		winCall("SendMessageW", p.controls[id], 0x1a0, 0, uintptr(p.pixels(p.rowHeight(id))))
	}
	for _, object := range old {
		if object != 0 {
			gdi32.NewProc("DeleteObject").Call(object)
		}
	}
}

func (p *windowsPanel) rect(x, y, width, height int) winRect {
	return winRect{int32(p.pixels(x)), int32(p.pixels(y)), int32(p.pixels(x + width)), int32(p.pixels(y + height))}
}

func winFill(dc uintptr, rect winRect, brush uintptr) {
	winCall("FillRect", dc, uintptr(unsafe.Pointer(&rect)), brush)
}

func (p *windowsPanel) rounded(dc uintptr, rect winRect, fill, border uintptr, radius int) {
	brush, _, _ := gdi32.NewProc("CreateSolidBrush").Call(fill)
	pen, _, _ := gdi32.NewProc("CreatePen").Call(0, uintptr(p.pixels(1)), border)
	oldBrush, _, _ := gdi32.NewProc("SelectObject").Call(dc, brush)
	oldPen, _, _ := gdi32.NewProc("SelectObject").Call(dc, pen)
	gdi32.NewProc("RoundRect").Call(dc, uintptr(rect.Left), uintptr(rect.Top), uintptr(rect.Right), uintptr(rect.Bottom), uintptr(p.pixels(radius)), uintptr(p.pixels(radius)))
	gdi32.NewProc("SelectObject").Call(dc, oldPen)
	gdi32.NewProc("SelectObject").Call(dc, oldBrush)
	gdi32.NewProc("DeleteObject").Call(pen)
	gdi32.NewProc("DeleteObject").Call(brush)
}

func winDrawText(dc uintptr, text string, font, color uintptr, rect winRect, flags uintptr) {
	value := wtext(text)
	old, _, _ := gdi32.NewProc("SelectObject").Call(dc, font)
	gdi32.NewProc("SetTextColor").Call(dc, color)
	gdi32.NewProc("SetBkMode").Call(dc, 1)
	winCall("DrawTextW", dc, uintptr(unsafe.Pointer(value)), ^uintptr(0), uintptr(unsafe.Pointer(&rect)), flags|0x800) // DT_NOPREFIX
	gdi32.NewProc("SelectObject").Call(dc, old)
	runtime.KeepAlive(value)
}

func (p *windowsPanel) paint() {
	var paint winPaint
	dc := winCall("BeginPaint", p.hwnd, uintptr(unsafe.Pointer(&paint)))
	defer winCall("EndPaint", p.hwnd, uintptr(unsafe.Pointer(&paint)))
	var client winRect
	winCall("GetClientRect", p.hwnd, uintptr(unsafe.Pointer(&client)))
	w, h := int(client.Right)*96/p.scale, int(client.Bottom)*96/p.scale
	s := p.style
	winFill(dc, client, s.backgroundBrush)
	winFill(dc, p.rect(0, 0, w, 96), s.surfaceBrush)
	winCall("DrawIconEx", dc, uintptr(p.pixels(24)), uintptr(p.pixels(24)), s.logo, uintptr(p.pixels(48)), uintptr(p.pixels(48)), 0, 0, 3)
	winDrawText(dc, "Jase Tunnel", s.brandFont, s.text, p.rect(88, 22, 240, 34), 0x24)
	winDrawText(dc, "Your models. Your computer.", s.small, s.muted, p.rect(90, 59, 250, 22), 0x24)
	p.rounded(dc, p.rect(24, 112, w-48, 100), s.surface, s.border, 18)
	p.rounded(dc, p.rect(44, 136, 8, 8), s.green, s.green, 8)
	p.rounded(dc, p.rect(24, 280, w-48, h-456), s.surface, s.border, 18)
	p.rounded(dc, p.rect(24, h-164, w-48, 108), s.surface, s.border, 18)
	winDrawText(dc, "Preferences", s.semibold, s.text, p.rect(w-222, h-147, 170, 24), 0x22)
	winDrawText(dc, "Stored on this computer", s.small, s.muted, p.rect(w-262, h-111, 210, 22), 0x22)
	if !p.activity && len(p.models) == 0 {
		// Leave the list's native keyboard and accessibility behavior intact;
		// its empty area is painted with an actionable first-run message.
		middle := 364 + (h-604)/2
		titleY := middle - 28
		if h-604 >= 160 {
			winCall("DrawIconEx", dc, uintptr(p.pixels(w/2-20)), uintptr(p.pixels(middle-44)), s.emptyLogo, uintptr(p.pixels(40)), uintptr(p.pixels(40)), 0, 0, 3)
			titleY = middle + 8
		}
		winDrawText(dc, "Your models, ready when you are", s.semibold, s.text, p.rect(44, titleY, w-88, 26), 0x25)
		winDrawText(dc, "Start Ollama or LM Studio, or add your model weights.", p.font, s.muted, p.rect(44, titleY+32, w-88, 24), 0x25)
	}
}

func (p *windowsPanel) controlColor(dc, control uintptr) uintptr {
	s := p.style
	color := s.text
	if control == p.hint || control == p.privacy || control == p.notice || control == p.connection || control == p.stats {
		color = s.muted
	}
	gdi32.NewProc("SetTextColor").Call(dc, color)
	gdi32.NewProc("SetBkMode").Call(dc, 1)
	background, brush := s.surface, s.surfaceBrush
	if control == p.privacy {
		background, brush = s.background, s.backgroundBrush
	}
	gdi32.NewProc("SetBkColor").Call(dc, background)
	return brush
}

func (p *windowsPanel) rowHeight(id int) int {
	if id == idEventList {
		return 40
	}
	return 64
}

func (p *windowsPanel) drawItem(address uintptr) uintptr {
	var item winDrawItem
	winReadNative(address, unsafe.Pointer(&item), unsafe.Sizeof(item))
	s := p.style
	if item.Type == 4 { // ODT_BUTTON
		id := int(item.ID)
		brush := s.surfaceBrush
		if id == idModels || id == idActivity {
			brush = s.backgroundBrush
		}
		winFill(item.DC, item.Rect, brush)
		primary := id == idAdd
		selected := id == idModels && !p.activity || id == idActivity && p.activity
		fill, border, text := s.surface, s.border, s.text
		if selected {
			fill, border, text = s.tint, s.tint, s.accent
		}
		if primary {
			fill, border, text = s.accent, s.accent, winRGB(0xffffff)
		}
		if item.State&1 != 0 {
			fill = s.tint
			text = s.accent
		}
		if item.State&4 != 0 {
			fill, border, text = s.background, s.border, s.muted
		}
		r := item.Rect
		r.Right--
		r.Bottom--
		p.rounded(item.DC, r, fill, border, 12)
		length := winCall("GetWindowTextLengthW", item.Window)
		label := make([]uint16, length+1)
		winCall("GetWindowTextW", item.Window, uintptr(unsafe.Pointer(&label[0])), length+1)
		winDrawText(item.DC, syscall.UTF16ToString(label), p.font, text, r, 0x25)
		if item.State&0x10 != 0 && item.State&0x200 == 0 {
			r.Left += int32(p.pixels(4))
			r.Top += int32(p.pixels(4))
			r.Right -= int32(p.pixels(4))
			r.Bottom -= int32(p.pixels(4))
			winCall("DrawFocusRect", item.DC, uintptr(unsafe.Pointer(&r)))
		}
		return 1
	}
	if item.Type != 2 { // ODT_LISTBOX
		return 0
	}
	winFill(item.DC, item.Rect, s.surfaceBrush)
	if item.Item == ^uint32(0) {
		return 1
	}
	r := item.Rect
	selected := item.State&1 != 0
	if selected {
		r.Left += int32(p.pixels(2))
		r.Right -= int32(p.pixels(2))
		r.Top += int32(p.pixels(2))
		r.Bottom -= int32(p.pixels(2))
		p.rounded(item.DC, r, s.tint, s.tint, 12)
	}
	index, title, subtitle := int(item.Item), "", ""
	switch int(item.ID) {
	case idModelList:
		if index < len(p.models) {
			m := p.models[index]
			title, subtitle = m.Name, m.Engine+"  ·  "+m.Status
		}
	case idSiteList:
		if index < len(p.sites) {
			site := p.sites[index]
			title, subtitle = site.Label, site.Status
		}
	case idEventList:
		if index < len(p.events) {
			title = eventSummary(p.events[index])
		}
	}
	textRect := item.Rect
	textRect.Left += int32(p.pixels(14))
	textRect.Right -= int32(p.pixels(14))
	if subtitle == "" {
		winDrawText(item.DC, title, p.font, s.text, textRect, 0x8024)
	} else {
		textRect.Top += int32(p.pixels(10))
		textRect.Bottom = textRect.Top + int32(p.pixels(22))
		winDrawText(item.DC, title, s.semibold, s.text, textRect, 0x8024)
		textRect.Top += int32(p.pixels(25))
		textRect.Bottom += int32(p.pixels(25))
		winDrawText(item.DC, subtitle, s.small, s.muted, textRect, 0x8024)
	}
	if item.State&0x10 != 0 {
		winCall("DrawFocusRect", item.DC, uintptr(unsafe.Pointer(&r)))
	}
	return 1
}
