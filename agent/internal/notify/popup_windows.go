//go:build windows

package notify

import (
	"errors"
	"math"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/vigil-tracker/agent/internal/config"
	"github.com/vigil-tracker/agent/internal/gdip"
)

// Card layout and timing (at 96 DPI; scaled for the monitor).
const (
	cardW     = 380
	minCardH  = 96
	padY      = 15
	iconSize  = 64
	margin    = 16
	enterTime = 280 * time.Millisecond
	holdTime  = 5 * time.Second
	exitTime  = 260 * time.Millisecond
	slide     = 28
	frame     = 15 * time.Millisecond

	// Text: semibold everywhere (never light), sizes in pixels at 96 DPI.
	labelSize    = 13
	labelLineH   = 18
	titleSize    = 18
	titleLineH   = 24
	titleLines   = 2
	subtitleSize = 13.5
	subtitleLnH  = 19
	textGap      = 3
)

// Fonts: Segoe UI Variable (Windows 11) when installed, Segoe UI otherwise.
const (
	fontSemibold = "Segoe UI Variable Text Semibold|Segoe UI Semibold"
	fontTitle    = "Segoe UI Variable Display Semibold|Segoe UI Variable Text Semibold|Segoe UI Semibold"
)

// Colours (web/src/styles/tokens.css), 0xAARRGGBB. Every text colour passes WCAG AA (4.5:1) on the
// card (checked in popup_windows_test.go).
var gradeColor = map[string]uint32{
	Bronze:   0xFFC48E66,
	Silver:   0xFFB4B8BD,
	Gold:     0xFFD0AE69,
	Platinum: 0xFFBCCDD4,
}

const (
	colorBg      = 0xFD0B1522
	colorAccent  = 0xFF8FC7E8
	colorText    = 0xFFE9EFF4
	colorMuted   = 0xFFA8BFD3
	colorBorder  = 0x80304B65
	colorPlatBg1 = 0xFD14243A
	colorPlatBg2 = 0xFD0B1522
)

// Render draws a card at the given DPI scale (1 = 96 DPI). The same pixels go to the screen and
// to the screenshots in docs/screenshots/agent.
func Render(it Item, lang string, scale float64) (*gdip.Canvas, error) {
	s := func(v float64) float64 { return v * scale }
	w := int(math.Round(s(cardW)))
	ix := int(math.Round(s(18)))
	isz := int(math.Round(s(iconSize)))
	tx := float64(ix+isz) + s(16)
	tw := float64(w) - tx - s(20)
	title := Title(lang, it)

	// Long names take two lines: measure first, then size the card.
	lines := 1
	if probe, err := gdip.NewCanvas(1, 1); err == nil {
		lines = probe.Lines(title, fontTitle, s(titleSize), gdip.Regular, tw, titleLines)
		probe.Close()
	}
	block := s(labelLineH) + s(textGap) + s(titleLineH)*float64(lines) + s(textGap) + s(subtitleLnH)
	h := int(math.Round(math.Max(s(minCardH), block+2*s(padY))))

	c, err := gdip.NewCanvas(w, h)
	if err != nil {
		return nil, err
	}
	W, H := float64(w), float64(h)
	plat := it.IsPlatinum()
	accent := uint32(colorAccent)
	if g, ok := gradeColor[NormalizeGrade(it.Grade)]; ok && !it.Summary && !it.Update {
		accent = g
	}

	// card
	if plat {
		c.FillRoundRectGradient(0.5, 0.5, W-1, H-1, s(10), colorPlatBg1, colorPlatBg2)
	} else {
		c.FillRoundRect(0.5, 0.5, W-1, H-1, s(10), colorBg)
	}
	// Quiet blue identity stripe; metallic grade colours remain on the trophy and label.
	c.ClipRoundRect(0.5, 0.5, W-1, H-1, s(10))
	if plat {
		c.FillRoundRectGradient(0, 0, s(4), H, 0, gradeColor[Platinum], colorAccent)
	} else {
		c.FillRoundRect(0, 0, s(3), H, 0, colorAccent)
	}
	c.ResetClip()
	border, bw := uint32(colorBorder), s(1)
	if plat {
		border, bw = 0xE6BCCDD4, s(1.6)
	}
	c.StrokeRoundRect(0.5, 0.5, W-1, H-1, s(10), border, bw)

	// icon, centred vertically
	iy := (h - isz) / 2
	fallback := GenericIcon(it.Grade)
	if it.Update {
		fallback = VigilIcon
	}
	icon := it.Icon
	if icon == nil {
		icon = fallback
	}
	if err := c.DrawPNG(icon, ix, iy, isz, isz, int(math.Round(s(9)))); err != nil {
		_ = c.DrawPNG(fallback, ix, iy, isz, isz, int(math.Round(s(9))))
	}
	if plat {
		c.StrokeRoundRect(float64(ix)-s(1.5), float64(iy)-s(1.5), float64(isz)+s(3), float64(isz)+s(3), s(10.5), 0xCCBCCDD4, s(1.4))
	}

	// text block, centred vertically
	y := (H - block) / 2
	c.DrawText(strings.ToUpper(Label(lang, it)), fontSemibold, s(labelSize), gdip.Regular, accent, tx, y, tw, s(labelLineH))
	y += s(labelLineH) + s(textGap)
	c.DrawTextLines(title, fontTitle, s(titleSize), gdip.Regular, colorText, tx, y, tw, s(titleLineH), lines)
	y += s(titleLineH)*float64(lines) + s(textGap)
	c.DrawText(Subtitle(lang, it), fontSemibold, s(subtitleSize), gdip.Regular, colorMuted, tx, y, tw, s(subtitleLnH))

	if plat {
		sparkle(c, W-s(20), s(16), s(6))
		sparkle(c, W-s(33), s(27), s(3))
	}
	return c, nil
}

// sparkle is a four-point star.
func sparkle(c *gdip.Canvas, cx, cy, r float64) {
	k := r * 0.22
	c.FillPolygon([][2]float64{{cx, cy - r}, {cx + k, cy - k}, {cx + r, cy}, {cx + k, cy + k},
		{cx, cy + r}, {cx - k, cy + k}, {cx - r, cy}, {cx - k, cy - k}}, 0xF2FFFFFF)
}

// ---- the window ----

var (
	user32                    = windows.NewLazySystemDLL("user32.dll")
	winmm                     = windows.NewLazySystemDLL("winmm.dll")
	pRegisterClassExW         = user32.NewProc("RegisterClassExW")
	pCreateWindowExW          = user32.NewProc("CreateWindowExW")
	pDefWindowProcW           = user32.NewProc("DefWindowProcW")
	pDestroyWindow            = user32.NewProc("DestroyWindow")
	pGetMessageW              = user32.NewProc("GetMessageW")
	pTranslateMessage         = user32.NewProc("TranslateMessage")
	pDispatchMessageW         = user32.NewProc("DispatchMessageW")
	pPostMessageW             = user32.NewProc("PostMessageW")
	pPostQuitMessage          = user32.NewProc("PostQuitMessage")
	pShowWindow               = user32.NewProc("ShowWindow")
	pUpdateLayeredWindow      = user32.NewProc("UpdateLayeredWindow")
	pSetTimer                 = user32.NewProc("SetTimer")
	pKillTimer                = user32.NewProc("KillTimer")
	pSystemParametersInfoW    = user32.NewProc("SystemParametersInfoW")
	pGetDpiForSystem          = user32.NewProc("GetDpiForSystem")
	pSetCursor                = user32.NewProc("SetCursor")
	pLoadCursorW              = user32.NewProc("LoadCursorW")
	pSetWindowPos             = user32.NewProc("SetWindowPos")
	pGetModuleHandleW         = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetModuleHandleW")
	pPlaySoundW               = winmm.NewProc("PlaySoundW")
	pSetThreadDpiAwarenessCtx = user32.NewProc("SetThreadDpiAwarenessContext")
)

const (
	popupClass = "VigilTrophiesPopup"

	wsPopup         = 0x80000000
	wsExLayered     = 0x00080000
	wsExToolWindow  = 0x00000080
	wsExTopmost     = 0x00000008
	wsExNoActivate  = 0x08000000
	wmDestroy       = 0x0002
	wmSetCursor     = 0x0020
	wmMouseActivate = 0x0021
	wmTimer         = 0x0113
	wmLButtonUp     = 0x0202
	wmApp           = 0x8000
	wmNext          = wmApp + 1
	wmQuit          = wmApp + 2
	maNoActivate    = 3
	ulwAlpha        = 2
	spiGetWorkArea  = 0x0030
	idcHand         = 32649
	timerID         = 1
	hwndTopmost     = ^uintptr(0) // (HWND)-1
	swpNoSizeMove   = 0x0001 | 0x0002
	swpNoActivate   = 0x0010
	swpNoZOrder     = 0x0004
	swpShowWindow   = 0x0040
	swpHideWindow   = 0x0080

	sndAsync     = 0x0001
	sndNoDefault = 0x0002
	sndMemory    = 0x0004
)

type rect struct{ Left, Top, Right, Bottom int32 }
type point struct{ X, Y int32 }
type size struct{ CX, CY int32 }

// Presenter shows cards one after the other on its own UI thread. It never takes the keyboard
// focus, stays out of the taskbar and Alt+Tab, and opens nothing but the item's URL (via OnClick).
type Presenter struct {
	mu   sync.Mutex
	lang string
	// Prefs gives the current corner and sound setting (read for each card).
	Prefs func() config.NotifyPrefs
	// OnClick receives the card's URL (the caller validates and opens it).
	OnClick func(url string)
	// Log, when set, receives problems (a card that could not be drawn).
	Log func(format string, args ...any)

	queue Queue
	ready chan error
	hwnd  uintptr
	idle  chan struct{}

	// UI thread only:
	cur     *Item
	canvas  *gdip.Canvas
	start   time.Time
	leaving bool
	pos     point
	from    int32 // slide offset sign: +1 slides in from the right, -1 from the left
}

var (
	presenterMu sync.Mutex
	active      *Presenter
)

// NewPresenter starts the popup thread.
func NewPresenter(lang string, prefs func() config.NotifyPrefs, onClick func(string)) (*Presenter, error) {
	p := &Presenter{lang: lang, Prefs: prefs, OnClick: onClick, ready: make(chan error, 1), idle: make(chan struct{}, 1)}
	presenterMu.Lock()
	if active != nil {
		presenterMu.Unlock()
		return nil, errors.New("a presenter is already running")
	}
	active = p // the window procedure finds it here
	presenterMu.Unlock()
	go p.loop()
	if err := <-p.ready; err != nil {
		presenterMu.Lock()
		active = nil
		presenterMu.Unlock()
		return nil, err
	}
	return p, nil
}

// SetLang changes the language of the next cards.
func (p *Presenter) SetLang(lang string) {
	p.mu.Lock()
	p.lang = lang
	p.mu.Unlock()
}

func (p *Presenter) language() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lang
}

// Show queues cards (bursts of more than three become a summary; platinum always shows alone).
func (p *Presenter) Show(items ...Item) {
	p.queue.Add(items...)
	pPostMessageW.Call(p.hwnd, wmNext, 0, 0)
}

// ShowInOrder queues cards exactly as given (the test sequence).
func (p *Presenter) ShowInOrder(items ...Item) {
	p.queue.AddInOrder(items...)
	pPostMessageW.Call(p.hwnd, wmNext, 0, 0)
}

// WaitIdle waits until every queued card was shown (or the timeout passes).
func (p *Presenter) WaitIdle(timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		select {
		case <-p.idle:
			if p.queue.Len() == 0 {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

// Close stops the thread.
func (p *Presenter) Close() {
	pPostMessageW.Call(p.hwnd, wmQuit, 0, 0)
}

func (p *Presenter) loop() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	// Per-monitor DPI aware even when the executable has no manifest (the command line).
	pSetThreadDpiAwarenessCtx.Call(^uintptr(3)) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4
	if err := gdip.Startup(); err != nil {
		p.ready <- err
		return
	}
	inst, _, _ := pGetModuleHandleW.Call(0)
	class, _ := windows.UTF16PtrFromString(popupClass)
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
	}{WndProc: syscall.NewCallback(popupProc), Instance: inst, ClassName: class}
	wc.Size = uint32(unsafe.Sizeof(wc))
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	title, _ := windows.UTF16PtrFromString("Vigil Trophies")
	hwnd, _, err := pCreateWindowExW.Call(wsExLayered|wsExToolWindow|wsExTopmost|wsExNoActivate,
		uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)), wsPopup, 0, 0, 0, 0, 0, 0, inst, 0)
	if hwnd == 0 {
		p.ready <- err
		return
	}
	p.hwnd = hwnd
	p.ready <- nil

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
	if p.canvas != nil {
		p.canvas.Close()
	}
	presenterMu.Lock()
	active = nil
	presenterMu.Unlock()
}

func popupProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	presenterMu.Lock()
	p := active
	presenterMu.Unlock()
	if p != nil && p.hwnd == hwnd {
		switch msg {
		case wmNext:
			p.next()
			return 0
		case wmTimer:
			p.tick()
			return 0
		case wmMouseActivate:
			return maNoActivate // a click never steals the focus from the game
		case wmSetCursor:
			if p.cur != nil && p.cur.URL != "" {
				cur, _, _ := pLoadCursorW.Call(0, idcHand)
				pSetCursor.Call(cur)
				return 1
			}
		case wmLButtonUp:
			if p.cur != nil {
				if p.cur.URL != "" && p.OnClick != nil {
					go p.OnClick(p.cur.URL)
				}
				p.leave()
			}
			return 0
		case wmQuit:
			pDestroyWindow.Call(hwnd)
			return 0
		case wmDestroy:
			pPostQuitMessage.Call(0)
			return 0
		}
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return r
}

// next shows the next card, if none is on screen.
func (p *Presenter) next() {
	if p.cur != nil {
		return
	}
	it, ok := p.queue.Next()
	if !ok {
		select {
		case p.idle <- struct{}{}:
		default:
		}
		return
	}
	dpi, _, _ := pGetDpiForSystem.Call()
	scale := float64(dpi) / 96
	if scale <= 0 {
		scale = 1
	}
	canvas, err := Render(it, p.language(), scale)
	if err != nil {
		if p.Log != nil {
			p.Log("notification: cannot draw the card: %v", err)
		}
		pPostMessageW.Call(p.hwnd, wmNext, 0, 0)
		return
	}
	if p.canvas != nil {
		p.canvas.Close()
	}
	p.canvas, p.cur, p.leaving, p.start = canvas, &it, false, time.Now()

	prefs := config.NotifyPrefs{}.Normalized()
	if p.Prefs != nil {
		prefs = p.Prefs().Normalized()
	}
	var work rect
	pSystemParametersInfoW.Call(spiGetWorkArea, 0, uintptr(unsafe.Pointer(&work)), 0)
	m := int32(math.Round(margin * scale))
	w, h := int32(canvas.W), int32(canvas.H)
	p.pos = point{work.Right - w - m, work.Top + m}
	p.from = 1
	if strings.HasSuffix(prefs.Corner, "left") {
		p.pos.X, p.from = work.Left+m, -1
	}
	if strings.HasPrefix(prefs.Corner, "bottom") {
		p.pos.Y = work.Bottom - h - m
	}
	if !it.Silent() {
		PlaySound(it.IsPlatinum(), prefs.Volume)
	}
	p.paint(0, 0)
	// SetWindowPos instead of ShowWindow: the first ShowWindow of a process follows how it was
	// started (a hidden launch would keep the card hidden).
	pSetWindowPos.Call(p.hwnd, hwndTopmost, 0, 0, 0, 0, swpNoSizeMove|swpNoActivate|swpShowWindow)
	pSetTimer.Call(p.hwnd, timerID, uintptr(frame/time.Millisecond), 0)
}

// leave starts the exit animation now (after a click).
func (p *Presenter) leave() {
	if !p.leaving {
		p.start = time.Now().Add(-(enterTime + holdTime))
		p.leaving = true
	}
}

func easeOut(t float64) float64 { return 1 - math.Pow(1-t, 3) }

// tick moves the animation: slide + fade in, hold, slide + fade out.
func (p *Presenter) tick() {
	if p.cur == nil {
		pKillTimer.Call(p.hwnd, timerID)
		return
	}
	t := time.Since(p.start)
	var progress float64 // 0 = hidden at the edge, 1 = in place
	switch {
	case t < enterTime:
		progress = easeOut(float64(t) / float64(enterTime))
	case t < enterTime+holdTime:
		progress = 1
	case t < enterTime+holdTime+exitTime:
		progress = 1 - easeOut(float64(t-enterTime-holdTime)/float64(exitTime))
	default:
		pKillTimer.Call(p.hwnd, timerID)
		pSetWindowPos.Call(p.hwnd, 0, 0, 0, 0, 0, swpNoSizeMove|swpNoActivate|swpNoZOrder|swpHideWindow)
		p.cur = nil
		pPostMessageW.Call(p.hwnd, wmNext, 0, 0)
		return
	}
	dpi, _, _ := pGetDpiForSystem.Call()
	offset := int32(math.Round((1 - progress) * slide * float64(dpi) / 96))
	p.paint(byte(math.Round(progress*255)), offset*p.from)
}

// paint pushes the card to the layered window with the given opacity and horizontal offset.
func (p *Presenter) paint(alpha byte, dx int32) {
	c := p.canvas
	dst := point{p.pos.X + dx, p.pos.Y}
	sz := size{int32(c.W), int32(c.H)}
	src := point{}
	blend := [4]byte{0, 0, alpha, 1} // AC_SRC_OVER, 0, SourceConstantAlpha, AC_SRC_ALPHA
	pUpdateLayeredWindow.Call(p.hwnd, 0, uintptr(unsafe.Pointer(&dst)), uintptr(unsafe.Pointer(&sz)), c.DC,
		uintptr(unsafe.Pointer(&src)), 0, uintptr(unsafe.Pointer(&blend)), ulwAlpha)
}

// PlaySound plays the chime (or the platinum one) at the chosen volume, unless the volume is off
// or Windows is in Do not disturb / Focus assist.
func PlaySound(platinum bool, volume string) {
	sound := Sound(platinum, volume)
	if len(sound) == 0 || Quiet() {
		return
	}
	pPlaySoundW.Call(uintptr(unsafe.Pointer(&sound[0])), 0, sndAsync|sndMemory|sndNoDefault)
}
