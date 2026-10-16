//go:build windows

// Package vui draws the tray app's own dialogs in the Vigil look (instead of the plain Windows
// message box): legible text, a blue primary button, full keyboard use (Tab, arrows, Enter,
// Space, Esc) with a visible focus ring. Drawn with GDI+ into a layered window.
package vui

import (
	"math"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/vigil-tracker/agent/internal/gdip"
)

// Line is one entry of a dialog's list: a title and a detail (a folder path).
type Line struct {
	Title  string
	Detail string
}

// Dialog is what to show. No == "" makes a single-button message.
type Dialog struct {
	Title    string
	Intro    string
	Lines    []Line
	More     string // e.g. "…and 2 more folders"
	Question string
	Yes, No  string
}

// Layout (96 DPI).
const (
	dlgW       = 540
	pad        = 28
	titleSize  = 20
	titleH     = 28
	bodySize   = 15
	bodyH      = 22
	lineTitleH = 22
	lineSize   = 15
	pathSize   = 13
	pathH      = 19
	lineGap    = 10
	buttonH    = 40
	buttonGap  = 10
	// MaxLines is how many list entries a dialog shows (the rest go in Dialog.More).
	MaxLines = 6
)

const (
	fontSemibold = "Segoe UI Variable Text Semibold|Segoe UI Semibold"
	fontDisplay  = "Segoe UI Variable Display Semibold|Segoe UI Variable Text Semibold|Segoe UI Semibold"

	colorBg     = 0xFF0B1522
	colorBorder = 0x808FC7E8
	colorAccent = 0xFF8FC7E8
	colorText   = 0xFFE9EFF4
	colorMuted  = 0xFFA8BFD3
	colorInk    = 0xFF0B1522 // dark text on the light blue button
	colorHover  = 0xFFA9D7F0
	colorGhost  = 0x1FFFFFFF
	colorFocus  = 0xFFB7DDF3
)

// rect is a button's area in pixels.
type rect struct{ x, y, w, h float64 }

func (r rect) has(x, y float64) bool { return x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h }

// Render draws the dialog: focus is the focused button (0 = Yes, 1 = No), hover the button under
// the mouse (-1 = none). Returns the canvas and the buttons' areas (Yes first).
func Render(d Dialog, focus, hover int, scale float64) (*gdip.Canvas, []rect, error) {
	s := func(v float64) float64 { return v * scale }
	lines := d.Lines
	if len(lines) > MaxLines {
		lines = lines[:MaxLines]
	}
	inner := s(dlgW) - 2*s(pad)

	// measure
	probe, err := gdip.NewCanvas(1, 1)
	if err != nil {
		return nil, nil, err
	}
	introLines := probe.Lines(d.Intro, fontSemibold, s(bodySize), gdip.Regular, inner, 3)
	questionLines := probe.Lines(d.Question, fontSemibold, s(bodySize), gdip.Regular, inner, 3)
	probe.Close()
	h := s(pad) + s(titleH) + s(12)
	if d.Intro != "" {
		h += s(bodyH)*float64(introLines) + s(14)
	}
	h += float64(len(lines)) * (s(lineTitleH) + s(pathH) + s(lineGap))
	if d.More != "" {
		h += s(pathH) + s(lineGap)
	}
	if d.Question != "" {
		h += s(4) + s(bodyH)*float64(questionLines) + s(8)
	}
	h += s(18) + s(buttonH) + s(pad)

	w := int(math.Round(s(dlgW)))
	c, err := gdip.NewCanvas(w, int(math.Round(h)))
	if err != nil {
		return nil, nil, err
	}
	W, H := float64(c.W), float64(c.H)
	c.FillRoundRect(0.5, 0.5, W-1, H-1, s(12), colorBg)
	c.ClipRoundRect(0.5, 0.5, W-1, H-1, s(12))
	c.FillRoundRect(0, 0, W, s(3), 0, colorAccent) // thin blue band on top
	c.ResetClip()
	c.StrokeRoundRect(0.5, 0.5, W-1, H-1, s(12), colorBorder, s(1))

	x, y := s(pad), s(pad)
	c.DrawText(d.Title, fontDisplay, s(titleSize), gdip.Regular, colorText, x, y, inner, s(titleH))
	y += s(titleH) + s(12)
	if d.Intro != "" {
		c.DrawTextLines(d.Intro, fontSemibold, s(bodySize), gdip.Regular, colorMuted, x, y, inner, s(bodyH), introLines)
		y += s(bodyH)*float64(introLines) + s(14)
	}
	for _, l := range lines {
		c.FillRoundRect(x, y+s(8), s(6), s(6), s(1.5), colorAccent)
		c.DrawText(l.Title, fontSemibold, s(lineSize), gdip.Regular, colorText, x+s(16), y, inner-s(16), s(lineTitleH))
		y += s(lineTitleH)
		c.DrawPath(l.Detail, fontSemibold, s(pathSize), gdip.Regular, colorMuted, x+s(16), y, inner-s(16), s(pathH))
		y += s(pathH) + s(lineGap)
	}
	if d.More != "" {
		c.DrawText(d.More, fontSemibold, s(pathSize), gdip.Regular, colorMuted, x+s(16), y, inner-s(16), s(pathH))
		y += s(pathH) + s(lineGap)
	}
	if d.Question != "" {
		y += s(4)
		c.DrawTextLines(d.Question, fontSemibold, s(bodySize), gdip.Regular, colorText, x, y, inner, s(bodyH), questionLines)
	}

	// buttons, right-aligned: [No] [Yes]
	labels := []string{d.Yes}
	if d.No != "" {
		labels = append(labels, d.No)
	}
	by := H - s(pad) - s(buttonH)
	right := W - s(pad)
	rects := make([]rect, len(labels))
	for i := 0; i < len(labels); i++ {
		bw := math.Max(s(110), float64(len([]rune(labels[i])))*s(9)+s(36))
		rects[i] = rect{right - bw, by, bw, s(buttonH)}
		right -= bw + s(buttonGap)
	}
	for i, r := range rects {
		if i == 0 { // primary
			fill := uint32(colorAccent)
			if hover == 0 {
				fill = colorHover
			}
			c.FillRoundRect(r.x, r.y, r.w, r.h, s(8), fill)
			c.DrawText(labels[i], fontSemibold, s(bodySize), gdip.Regular, colorInk, r.x+s(14), r.y+s(9), r.w-s(28), s(bodyH))
		} else {
			if hover == i {
				c.FillRoundRect(r.x, r.y, r.w, r.h, s(8), colorGhost)
			}
			c.StrokeRoundRect(r.x+0.5, r.y+0.5, r.w-1, r.h-1, s(8), colorMuted, s(1))
			c.DrawText(labels[i], fontSemibold, s(bodySize), gdip.Regular, colorText, r.x+s(14), r.y+s(9), r.w-s(28), s(bodyH))
		}
		if focus == i { // keyboard focus ring, outside the button
			c.StrokeRoundRect(r.x-s(3), r.y-s(3), r.w+s(6), r.h+s(6), s(10), colorFocus, s(2))
		}
	}
	return c, rects, nil
}

// ---- the window ----

var (
	user32                    = windows.NewLazySystemDLL("user32.dll")
	pRegisterClassExW         = user32.NewProc("RegisterClassExW")
	pCreateWindowExW          = user32.NewProc("CreateWindowExW")
	pDefWindowProcW           = user32.NewProc("DefWindowProcW")
	pDestroyWindow            = user32.NewProc("DestroyWindow")
	pGetMessageW              = user32.NewProc("GetMessageW")
	pTranslateMessage         = user32.NewProc("TranslateMessage")
	pDispatchMessageW         = user32.NewProc("DispatchMessageW")
	pPostQuitMessage          = user32.NewProc("PostQuitMessage")
	pUpdateLayeredWindow      = user32.NewProc("UpdateLayeredWindow")
	pSystemParametersInfoW    = user32.NewProc("SystemParametersInfoW")
	pGetDpiForSystem          = user32.NewProc("GetDpiForSystem")
	pSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	pSetWindowPos             = user32.NewProc("SetWindowPos")
	pSetFocus                 = user32.NewProc("SetFocus")
	pGetKeyState              = user32.NewProc("GetKeyState")
	pScreenToClient           = user32.NewProc("ScreenToClient")
	pSetCursor                = user32.NewProc("SetCursor")
	pLoadCursorW              = user32.NewProc("LoadCursorW")
	pSetThreadDpiAwarenessCtx = user32.NewProc("SetThreadDpiAwarenessContext")
	pGetModuleHandleW         = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetModuleHandleW")
)

const (
	className = "VigilTrophiesDialog"

	wsPopup        = 0x80000000
	wsExLayered    = 0x00080000
	wsExTopmost    = 0x00000008
	wsExAppWindow  = 0x00040000
	wmDestroy      = 0x0002
	wmClose        = 0x0010
	wmSetCursor    = 0x0020
	wmNCHitTest    = 0x0084
	wmKeyDown      = 0x0100
	wmMouseMove    = 0x0200
	wmLButtonUp    = 0x0202
	htClient       = 1
	htCaption      = 2
	vkTab          = 0x09
	vkReturn       = 0x0D
	vkShift        = 0x10
	vkEscape       = 0x1B
	vkSpace        = 0x20
	vkLeft         = 0x25
	vkRight        = 0x27
	ulwAlpha       = 2
	spiGetWorkArea = 0x0030
	swpShowWindow  = 0x0040
	swpNoSize      = 0x0001
	idcHand        = 32649
	idcArrow       = 32512
)

type point struct{ X, Y int32 }

type dialog struct {
	spec    Dialog
	hwnd    uintptr
	scale   float64
	focus   int
	hover   int
	canvas  *gdip.Canvas
	buttons []rect
	pos     point
	result  bool
}

var (
	registerOnce sync.Once
	openMu       sync.Mutex
	open         = map[uintptr]*dialog{}
)

// Show displays the dialog and waits for an answer: true for Yes (or OK), false for No, Esc or
// closing it. Safe to call from any goroutine (it runs on its own UI thread).
func Show(d Dialog) bool {
	done := make(chan bool, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		done <- run(d)
	}()
	return <-done
}

func run(spec Dialog) bool {
	pSetThreadDpiAwarenessCtx.Call(^uintptr(3)) // per-monitor v2
	if err := gdip.Startup(); err != nil {
		return false
	}
	inst, _, _ := pGetModuleHandleW.Call(0)
	class, _ := windows.UTF16PtrFromString(className)
	registerOnce.Do(func() {
		wc := struct {
			Size       uint32
			Style      uint32
			WndProc    uintptr
			ClsExtra   int32
			WndExtra   int32
			Instance   uintptr
			Icon       uintptr
			Cursor     uintptr
			Background uintptr
			MenuName   *uint16
			ClassName  *uint16
			IconSm     uintptr
		}{WndProc: syscall.NewCallback(proc), Instance: inst, ClassName: class}
		wc.Size = uint32(unsafe.Sizeof(wc))
		pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	})
	dpi, _, _ := pGetDpiForSystem.Call()
	d := &dialog{spec: spec, scale: math.Max(1, float64(dpi)/96), hover: -1}
	title, _ := windows.UTF16PtrFromString(spec.Title)
	hwnd, _, _ := pCreateWindowExW.Call(wsExLayered|wsExTopmost|wsExAppWindow, uintptr(unsafe.Pointer(class)),
		uintptr(unsafe.Pointer(title)), wsPopup, 0, 0, 0, 0, 0, 0, inst, 0)
	if hwnd == 0 {
		return false
	}
	d.hwnd = hwnd
	openMu.Lock()
	open[hwnd] = d
	openMu.Unlock()
	defer func() {
		openMu.Lock()
		delete(open, hwnd)
		openMu.Unlock()
		if d.canvas != nil {
			d.canvas.Close()
		}
	}()

	if !d.paint() {
		pDestroyWindow.Call(hwnd)
		return false
	}
	// centred on the main screen's work area
	var work struct{ L, T, R, B int32 }
	pSystemParametersInfoW.Call(spiGetWorkArea, 0, uintptr(unsafe.Pointer(&work)), 0)
	d.pos = point{work.L + (work.R-work.L-int32(d.canvas.W))/2, work.T + (work.B-work.T-int32(d.canvas.H))/2}
	d.paint()
	pSetWindowPos.Call(hwnd, 0, uintptr(d.pos.X), uintptr(d.pos.Y), 0, 0, swpShowWindow|swpNoSize)
	pSetForegroundWindow.Call(hwnd)
	pSetFocus.Call(hwnd)

	var m struct {
		Hwnd    uintptr
		Message uint32
		WParam  uintptr
		LParam  uintptr
		Time    uint32
		Pt      point
		Private uint32
	}
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	return d.result
}

// paint renders the current state and pushes it to the window.
func (d *dialog) paint() bool {
	c, rects, err := Render(d.spec, d.focus, d.hover, d.scale)
	if err != nil {
		return false
	}
	if d.canvas != nil {
		d.canvas.Close()
	}
	d.canvas, d.buttons = c, rects
	size := struct{ CX, CY int32 }{int32(c.W), int32(c.H)}
	src := point{}
	blend := [4]byte{0, 0, 255, 1}
	pUpdateLayeredWindow.Call(d.hwnd, 0, uintptr(unsafe.Pointer(&d.pos)), uintptr(unsafe.Pointer(&size)), c.DC,
		uintptr(unsafe.Pointer(&src)), 0, uintptr(unsafe.Pointer(&blend)), ulwAlpha)
	return true
}

func (d *dialog) finish(yes bool) {
	d.result = yes
	pDestroyWindow.Call(d.hwnd)
}

func (d *dialog) buttonAt(x, y float64) int {
	for i, r := range d.buttons {
		if r.has(x, y) {
			return i
		}
	}
	return -1
}

func proc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	openMu.Lock()
	d := open[hwnd]
	openMu.Unlock()
	if d != nil {
		switch msg {
		case wmNCHitTest:
			// Drag the dialog by anything that is not a button.
			pt := point{int32(int16(lParam & 0xffff)), int32(int16((lParam >> 16) & 0xffff))}
			pScreenToClient.Call(hwnd, uintptr(unsafe.Pointer(&pt)))
			if d.buttonAt(float64(pt.X), float64(pt.Y)) >= 0 {
				return htClient
			}
			return htCaption
		case wmSetCursor:
			cursor := uintptr(idcArrow)
			if d.hover >= 0 {
				cursor = idcHand
			}
			h, _, _ := pLoadCursorW.Call(0, cursor)
			pSetCursor.Call(h)
			return 1
		case wmMouseMove:
			x, y := float64(int16(lParam&0xffff)), float64(int16((lParam>>16)&0xffff))
			if h := d.buttonAt(x, y); h != d.hover {
				d.hover = h
				d.paint()
			}
			return 0
		case wmLButtonUp:
			x, y := float64(int16(lParam&0xffff)), float64(int16((lParam>>16)&0xffff))
			if b := d.buttonAt(x, y); b >= 0 {
				d.finish(b == 0)
			}
			return 0
		case wmKeyDown:
			n := len(d.buttons)
			switch wParam {
			case vkTab, vkLeft, vkRight:
				shift, _, _ := pGetKeyState.Call(vkShift)
				step := 1
				if wParam == vkRight || (wParam == vkTab && int16(shift) < 0) {
					step = -1 // buttons are laid out right to left
				}
				d.focus = ((d.focus+step)%n + n) % n
				d.paint()
			case vkReturn, vkSpace:
				d.finish(d.focus == 0)
			case vkEscape:
				d.finish(n == 1) // a single-button message: Esc is OK
			}
			return 0
		case wmClose:
			d.finish(false)
			return 0
		case wmDestroy:
			pPostQuitMessage.Call(0)
			return 0
		}
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return r
}
