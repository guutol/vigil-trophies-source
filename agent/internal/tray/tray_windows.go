//go:build windows

// Package tray is a small Windows notification-area (system tray) icon: a hidden window, the
// icon, a popup menu built on demand, balloon notifications and a couple of native dialogs.
// It talks to user32/shell32 directly (no cgo, no GUI framework) and opens no network port.
package tray

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/vigil-tracker/agent/internal/app"
	"github.com/vigil-tracker/agent/internal/gdip"
)

// ClassName identifies the tray window; a second copy of the app uses it to find the first.
const ClassName = "VigilTrophiesTray"

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	ole32    = windows.NewLazySystemDLL("ole32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	pRegisterClassExW         = user32.NewProc("RegisterClassExW")
	pCreateWindowExW          = user32.NewProc("CreateWindowExW")
	pDefWindowProcW           = user32.NewProc("DefWindowProcW")
	pDestroyWindow            = user32.NewProc("DestroyWindow")
	pGetMessageW              = user32.NewProc("GetMessageW")
	pTranslateMessage         = user32.NewProc("TranslateMessage")
	pDispatchMessageW         = user32.NewProc("DispatchMessageW")
	pPostMessageW             = user32.NewProc("PostMessageW")
	pPostQuitMessage          = user32.NewProc("PostQuitMessage")
	pRegisterWindowMessageW   = user32.NewProc("RegisterWindowMessageW")
	pCreatePopupMenu          = user32.NewProc("CreatePopupMenu")
	pAppendMenuW              = user32.NewProc("AppendMenuW")
	pSetMenuDefaultItem       = user32.NewProc("SetMenuDefaultItem")
	pTrackPopupMenu           = user32.NewProc("TrackPopupMenu")
	pDestroyMenu              = user32.NewProc("DestroyMenu")
	pGetCursorPos             = user32.NewProc("GetCursorPos")
	pSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	pFindWindowW              = user32.NewProc("FindWindowW")
	pGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	pAllowSetForegroundWindow = user32.NewProc("AllowSetForegroundWindow")
	pMessageBoxW              = user32.NewProc("MessageBoxW")
	pLoadImageW               = user32.NewProc("LoadImageW")
	pLoadIconW                = user32.NewProc("LoadIconW")
	pGetSystemMetrics         = user32.NewProc("GetSystemMetrics")
	pShellNotifyIconW         = shell32.NewProc("Shell_NotifyIconW")
	pShellExecuteW            = shell32.NewProc("ShellExecuteW")
	pSHBrowseForFolderW       = shell32.NewProc("SHBrowseForFolderW")
	pSHGetPathFromIDListW     = shell32.NewProc("SHGetPathFromIDListW")
	pCoInitializeEx           = ole32.NewProc("CoInitializeEx")
	pCoUninitialize           = ole32.NewProc("CoUninitialize")
	pCoTaskMemFree            = ole32.NewProc("CoTaskMemFree")
	pGetModuleHandleW         = kernel32.NewProc("GetModuleHandleW")
)

const (
	wmNull        = 0x0000
	wmDestroy     = 0x0002
	wmClose       = 0x0010
	wmContextMenu = 0x007B
	wmLButtonUp   = 0x0202
	wmUser        = 0x0400
	wmApp         = 0x8000

	wmTrayIcon = wmApp + 1 // the icon's callback message
	wmWake     = wmApp + 2 // another goroutine queued work for the UI thread
	wmShowMenu = wmApp + 3 // a second copy of the app asks to show the menu

	ninSelect           = wmUser + 0
	ninKeySelect        = wmUser + 1
	ninBalloonUserClick = wmUser + 5

	nimAdd        = 0
	nimModify     = 1
	nimDelete     = 2
	nimSetVersion = 4

	nifMessage  = 0x01
	nifIcon     = 0x02
	nifTip      = 0x04
	nifInfo     = 0x10
	nifShowTip  = 0x80
	niifUser    = 0x04
	niifNoSound = 0x10
	niifLarge   = 0x20
	notifyIconV = 4

	mfString    = 0x0000
	mfGrayed    = 0x0001
	mfChecked   = 0x0008
	mfPopup     = 0x0010
	mfSeparator = 0x0800

	tpmRightButton = 0x0002
	tpmReturnCmd   = 0x0100
	tpmNoNotify    = 0x0080

	imageIcon      = 1
	lrDefaultColor = 0
	smCxSmIcon     = 49
	smCySmIcon     = 50
	smCxIcon       = 11
	smCyIcon       = 12
	idiApplication = 32512

	mbYesNo         = 0x0004
	mbIconQuestion  = 0x0020
	mbIconInfo      = 0x0040
	mbIconWarning   = 0x0030
	mbSetForeground = 0x10000
	mbTopmost       = 0x40000
	idYes           = 6

	swShowNormal = 1
)

type point struct{ X, Y int32 }

type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	Private uint32
}

type wndClassEx struct {
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
}

type notifyIconData struct {
	Size            uint32
	Wnd             uintptr
	ID              uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            uintptr
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	Version         uint32
	InfoTitle       [64]uint16
	InfoFlags       uint32
	GUIDItem        windows.GUID
	BalloonIcon     uintptr
}

// Tray is the icon and its menu. Run must be called once; the other methods are safe from any
// goroutine.
type Tray struct {
	// Tooltip shown when hovering the icon.
	Tooltip string
	// Menu builds the menu each time it opens (on the UI thread: keep it quick).
	Menu func() []app.Item
	// NativeMenu opts out of custom drawing. High contrast and screen-reader mode also do so.
	NativeMenu func() bool
	// OnCommand runs a menu choice on the UI thread; do slow work in a goroutine.
	OnCommand func(id int)
	// OnBalloonClick runs when the user clicks a notification (may be nil).
	OnBalloonClick func()
	// OnSecondCopy runs when the user starts the app again while it is running.
	OnSecondCopy func()

	hwnd           uintptr
	icon, bigIcon  uintptr
	taskbarCreated uint32

	mu      sync.Mutex
	pending []func() // work queued for the UI thread

	balloonIcon uintptr // trophy icon of the last notification (UI thread only)
	menuOpen    bool
	skin        *menuSkin
}

// self is the running tray (the window procedure is a plain callback).
var self *Tray

// Run creates the icon and processes window messages until Quit is called.
func (t *Tray) Run() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if self != nil {
		return errors.New("tray already running")
	}
	self = t

	inst, _, _ := pGetModuleHandleW.Call(0)
	class := utf16(ClassName)
	wc := wndClassEx{WndProc: syscall.NewCallback(wndProc), Instance: inst, ClassName: class}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if r, _, err := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return err
	}
	hwnd, _, err := pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(utf16("Vigil Trophies"))),
		0, 0, 0, 0, 0, 0, 0, inst, 0)
	if hwnd == 0 {
		return err
	}
	t.mu.Lock()
	t.hwnd = hwnd
	t.mu.Unlock()
	tc, _, _ := pRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(utf16("TaskbarCreated"))))
	t.taskbarCreated = uint32(tc)
	t.icon = loadIcon(inst, smCxSmIcon, smCySmIcon)
	t.bigIcon = loadIcon(inst, smCxIcon, smCyIcon)
	if err := t.addIcon(); err != nil {
		return err
	}
	defer t.removeIcon()
	pPostMessageW.Call(hwnd, wmWake, 0, 0) // run whatever was queued before the window existed

	var m msg
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return nil
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// loadIcon takes the "APP" icon from the executable's resources (see cmd/vigil-agent-tray/winres),
// or the generic application icon when the binary has none (tests).
func loadIcon(inst uintptr, cxMetric, cyMetric uintptr) uintptr {
	cx, _, _ := pGetSystemMetrics.Call(cxMetric)
	cy, _, _ := pGetSystemMetrics.Call(cyMetric)
	h, _, _ := pLoadImageW.Call(inst, uintptr(unsafe.Pointer(utf16("APP"))), imageIcon, cx, cy, lrDefaultColor)
	if h == 0 {
		h, _, _ = pLoadIconW.Call(0, idiApplication)
	}
	return h
}

func (t *Tray) data() notifyIconData {
	d := notifyIconData{Wnd: t.hwnd, ID: 1}
	d.Size = uint32(unsafe.Sizeof(d))
	return d
}

func (t *Tray) addIcon() error {
	d := t.data()
	d.Flags = nifMessage | nifIcon | nifTip | nifShowTip
	d.CallbackMessage = wmTrayIcon
	d.Icon = t.icon
	copyUTF16(d.Tip[:], t.Tooltip)
	if r, _, err := pShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&d))); r == 0 {
		return err
	}
	d.Version = notifyIconV
	pShellNotifyIconW.Call(nimSetVersion, uintptr(unsafe.Pointer(&d)))
	return nil
}

func (t *Tray) removeIcon() {
	d := t.data()
	pShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&d)))
	gdip.DestroyIcon(t.balloonIcon)
	t.balloonIcon = 0
}

// do queues fn for the UI thread.
func (t *Tray) do(fn func()) {
	t.mu.Lock()
	t.pending = append(t.pending, fn)
	hwnd := t.hwnd
	t.mu.Unlock()
	if hwnd != 0 {
		pPostMessageW.Call(hwnd, wmWake, 0, 0)
	}
}

// Notify shows a native notification (a toast on Windows 10/11) from the tray icon.
func (t *Tray) Notify(title, body string) {
	t.do(func() {
		d := t.data()
		d.Flags = nifInfo
		copyUTF16(d.InfoTitle[:], title)
		copyUTF16(d.Info[:], body)
		d.InfoFlags = niifUser | niifLarge
		d.BalloonIcon = t.bigIcon
		pShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&d)))
	})
}

// NotifyIcon shows a Windows notification with the trophy's PNG as its picture (the app icon when
// the PNG cannot be used). silent turns off Windows' own sound (the app plays its own chime).
func (t *Tray) NotifyIcon(title, body string, icon []byte, silent bool) {
	t.do(func() {
		d := t.data()
		d.Flags = nifInfo
		copyUTF16(d.InfoTitle[:], title)
		copyUTF16(d.Info[:], body)
		d.InfoFlags = niifUser | niifLarge
		if silent {
			d.InfoFlags |= niifNoSound
		}
		d.BalloonIcon = t.bigIcon
		var custom uintptr
		if len(icon) > 0 {
			if h, err := gdip.IconFromPNG(icon); err == nil {
				custom, d.BalloonIcon = h, h
			}
		}
		pShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&d)))
		// Windows copies the picture: the previous one can go.
		gdip.DestroyIcon(t.balloonIcon)
		t.balloonIcon = custom
	})
}

// SetTooltip changes the hover text.
func (t *Tray) SetTooltip(text string) {
	t.do(func() {
		t.Tooltip = text
		d := t.data()
		d.Flags = nifTip | nifShowTip
		copyUTF16(d.Tip[:], text)
		pShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&d)))
	})
}

// ShowMenu opens the menu at the mouse pointer.
func (t *Tray) ShowMenu() { t.do(t.showMenu) }

// Quit removes the icon and makes Run return.
func (t *Tray) Quit() {
	t.do(func() { pDestroyWindow.Call(t.hwnd) })
}

func wndProc(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	t := self
	switch {
	case message == 0x0120 && t != nil && t.skin != nil: // WM_MENUCHAR
		return t.skin.menuChar(rune(wParam&0xffff), lParam)
	case message == 0x002c && t != nil && t.skin != nil: // WM_MEASUREITEM
		return t.skin.measure(lParam)
	case message == 0x002b && t != nil && t.skin != nil: // WM_DRAWITEM
		return t.skin.draw(lParam)
	case message == wmTrayIcon:
		switch uint32(lParam & 0xFFFF) {
		case wmContextMenu, ninSelect, ninKeySelect, wmLButtonUp:
			t.showMenu()
		case ninBalloonUserClick:
			if t.OnBalloonClick != nil {
				t.OnBalloonClick()
			}
		}
		return 0
	case message == wmWake:
		t.mu.Lock()
		work := t.pending
		t.pending = nil
		t.mu.Unlock()
		for _, fn := range work {
			fn()
		}
		return 0
	case message == wmShowMenu:
		if t.OnSecondCopy != nil {
			t.OnSecondCopy()
		}
		t.showMenu()
		return 0
	case t != nil && t.taskbarCreated != 0 && message == t.taskbarCreated:
		t.addIcon() // Explorer restarted: the icon has to be added again
		return 0
	case message == wmClose:
		pDestroyWindow.Call(hwnd)
		return 0
	case message == wmDestroy:
		pPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, uintptr(message), wParam, lParam)
	return r
}

func (t *Tray) showMenu() {
	if t.Menu == nil || t.menuOpen {
		return
	}
	t.menuOpen = true
	defer func() { t.menuOpen = false }()
	items := t.Menu()
	var pt point
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	// The icon rectangle also anchors keyboard invocation (the pointer may be on another monitor).
	anchor := struct {
		Size   uint32
		Window uintptr
		ID     uint32
		GUID   windows.GUID
	}{Window: t.hwnd, ID: 1}
	anchor.Size = uint32(unsafe.Sizeof(anchor))
	var iconRect menuRect
	if r, _, _ := shell32.NewProc("Shell_NotifyIconGetRect").Call(uintptr(unsafe.Pointer(&anchor)), uintptr(unsafe.Pointer(&iconRect))); r == 0 {
		pt = point{X: (iconRect.Left + iconRect.Right) / 2, Y: (iconRect.Top + iconRect.Bottom) / 2}
	}
	var h uintptr
	if !nativeMenuRequired() && (t.NativeMenu == nil || !t.NativeMenu()) {
		t.skin = newMenuSkin(pt)
		if t.skin != nil {
			h = t.skin.build(items)
		}
	}
	if h == 0 {
		if t.skin != nil {
			t.skin.close()
			t.skin = nil
		}
		h = buildMenu(items)
	}
	defer func() {
		if t.skin != nil {
			t.skin.close()
			t.skin = nil
		}
	}()
	if h == 0 {
		return
	}
	defer pDestroyMenu.Call(h)
	// Needed so the menu closes when the user clicks elsewhere.
	pSetForegroundWindow.Call(t.hwnd)
	cmd, _, _ := pTrackPopupMenu.Call(h, tpmRightButton|tpmReturnCmd|tpmNoNotify,
		uintptr(pt.X), uintptr(pt.Y), 0, t.hwnd, 0)
	pPostMessageW.Call(t.hwnd, wmNull, 0, 0)
	if cmd != 0 && t.OnCommand != nil {
		t.OnCommand(int(cmd))
	}
}

func buildMenu(items []app.Item) uintptr {
	h, _, _ := pCreatePopupMenu.Call()
	if h == 0 {
		return 0
	}
	for _, it := range items {
		switch {
		case it.ID == 0 && it.Label == "" && len(it.Sub) == 0:
			pAppendMenuW.Call(h, mfSeparator, 0, 0)
		case len(it.Sub) > 0:
			sub := buildMenu(it.Sub)
			pAppendMenuW.Call(h, mfPopup, sub, uintptr(unsafe.Pointer(utf16(menuText(it.Text())))))
		default:
			flags := uintptr(mfString)
			if it.Disabled {
				flags |= mfGrayed
			}
			if it.Checked {
				flags |= mfChecked
			}
			pAppendMenuW.Call(h, flags, uintptr(it.ID), uintptr(unsafe.Pointer(utf16(menuText(it.Text())))))
			if it.Default {
				pSetMenuDefaultItem.Call(h, uintptr(it.ID), 0)
			}
		}
	}
	return h
}

// menuText escapes "&" (menus treat it as a keyboard shortcut marker).
func menuText(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '&' {
			out = append(out, '&')
		}
		out = append(out, r)
	}
	return string(out)
}

// ---- helpers usable from any goroutine ----

// AskSecondCopyToShowMenu finds a running copy and asks it to open its menu. ok is false when no
// running copy was found.
func AskSecondCopyToShowMenu() bool {
	h, _, _ := pFindWindowW.Call(uintptr(unsafe.Pointer(utf16(ClassName))), 0)
	if h == 0 {
		return false
	}
	var pid uint32
	pGetWindowThreadProcessId.Call(h, uintptr(unsafe.Pointer(&pid)))
	pAllowSetForegroundWindow.Call(uintptr(pid))
	pPostMessageW.Call(h, wmShowMenu, 0, 0)
	return true
}

// Ask shows a Yes/No question; true means Yes.
func Ask(title, text string) bool {
	r, _, _ := pMessageBoxW.Call(0, uintptr(unsafe.Pointer(utf16(text))), uintptr(unsafe.Pointer(utf16(title))),
		mbYesNo|mbIconQuestion|mbSetForeground|mbTopmost)
	return r == idYes
}

// Inform shows a message with an OK button.
func Inform(title, text string, warning bool) {
	icon := uintptr(mbIconInfo)
	if warning {
		icon = mbIconWarning
	}
	pMessageBoxW.Call(0, uintptr(unsafe.Pointer(utf16(text))), uintptr(unsafe.Pointer(utf16(title))),
		icon|mbSetForeground|mbTopmost)
}

// PickFolder shows the native folder picker. ok is false when the user cancelled.
func PickFolder(title string) (string, bool) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	const coinitApartmentThreaded = 0x2
	if r, _, _ := pCoInitializeEx.Call(0, coinitApartmentThreaded); int32(r) >= 0 {
		defer pCoUninitialize.Call()
	}
	const (
		bifReturnOnlyFSDirs = 0x0001
		bifNewDialogStyle   = 0x0040
		bifNoNewFolder      = 0x0200
	)
	display := make([]uint16, windows.MAX_PATH)
	bi := struct {
		Owner       uintptr
		Root        uintptr
		DisplayName *uint16
		Title       *uint16
		Flags       uint32
		Callback    uintptr
		LParam      uintptr
		Image       int32
	}{DisplayName: &display[0], Title: utf16(title), Flags: bifReturnOnlyFSDirs | bifNewDialogStyle | bifNoNewFolder}
	pidl, _, _ := pSHBrowseForFolderW.Call(uintptr(unsafe.Pointer(&bi)))
	if pidl == 0 {
		return "", false
	}
	defer pCoTaskMemFree.Call(pidl)
	path := make([]uint16, windows.MAX_PATH)
	if r, _, _ := pSHGetPathFromIDListW.Call(pidl, uintptr(unsafe.Pointer(&path[0]))); r == 0 {
		return "", false
	}
	return windows.UTF16ToString(path), true
}

// OpenURL opens an http(s) page in the default browser. Anything else is refused.
func OpenURL(raw string) error {
	u, err := app.SafeWebURL(raw)
	if err != nil {
		return err
	}
	return shellOpen(u.String())
}

// OpenPath opens a local folder (Explorer) or a .log file (Notepad). Nothing that could run.
func OpenPath(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() && !strings.EqualFold(filepath.Ext(path), ".log") {
		return errors.New("only folders and .log files are opened")
	}
	return shellOpen(path)
}

func shellOpen(target string) error {
	r, _, err := pShellExecuteW.Call(0, uintptr(unsafe.Pointer(utf16("open"))), uintptr(unsafe.Pointer(utf16(target))),
		0, 0, swShowNormal)
	if r <= 32 {
		return err
	}
	return nil
}

func utf16(s string) *uint16 {
	p, err := windows.UTF16PtrFromString(s)
	if err != nil {
		p, _ = windows.UTF16PtrFromString("?")
	}
	return p
}

// copyUTF16 copies s into a fixed buffer, truncated and NUL-terminated.
func copyUTF16(dst []uint16, s string) {
	src, err := windows.UTF16FromString(s)
	if err != nil {
		return
	}
	if len(src) > len(dst) {
		src = src[:len(dst)]
		src[len(src)-1] = 0
	}
	copy(dst, src)
}
