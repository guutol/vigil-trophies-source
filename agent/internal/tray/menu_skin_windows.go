//go:build windows

package tray

// Documented Win32 owner-drawn menus: Windows still owns tracking, keyboard navigation,
// dismissal, scrolling and submenu placement. No menu-window hooks or undocumented themes.
import (
	"math"
	"strings"
	"unicode/utf8"
	"unsafe"

	"github.com/vigil-tracker/agent/internal/app"
	"github.com/vigil-tracker/agent/internal/gdip"
	"golang.org/x/sys/windows"
)

var (
	menuGDI         = windows.NewLazySystemDLL("gdi32.dll")
	menuInsert      = user32.NewProc("InsertMenuItemW")
	menuSetInfo     = user32.NewProc("SetMenuInfo")
	menuMonitor     = user32.NewProc("MonitorFromPoint")
	menuMonitorInfo = user32.NewProc("GetMonitorInfoW")
	menuDPI         = windows.NewLazySystemDLL("shcore.dll").NewProc("GetDpiForMonitor")
	menuSPI         = user32.NewProc("SystemParametersInfoW")
	menuBrush       = menuGDI.NewProc("CreateSolidBrush")
	menuDelete      = menuGDI.NewProc("DeleteObject")
	menuBlit        = menuGDI.NewProc("BitBlt")
	menuGetState    = user32.NewProc("GetMenuState")
	menuCopy        = kernel32.NewProc("RtlMoveMemory")
	menuAllocate    = kernel32.NewProc("LocalAlloc")
	menuFree        = kernel32.NewProc("LocalFree")
)

type menuRect struct{ Left, Top, Right, Bottom int32 }
type menuItemInfo struct {
	Size, Mask, Type, State, ID   uint32
	Sub, Checked, Unchecked, Data uintptr
	Text                          *uint16
	Length                        uint32
	Bitmap                        uintptr
}
type menuInfo struct {
	Size, Mask, Style, MaxHeight uint32
	Background                   uintptr
	Help                         uint32
	Data                         uintptr
}
type measureItem struct {
	Type, Control, ID, Width, Height uint32
	Data                             uintptr
}
type drawItem struct {
	Type, Control, ID, Action, State uint32
	Window, DC                       uintptr
	Rect                             menuRect
	Data                             uintptr
}

type menuSkin struct {
	scale            float64
	width, maxHeight int
	brush            uintptr
	items            map[uintptr]app.Item
	lists            map[uintptr][]app.Item
	memory           []uintptr // OS-owned MSAAMENUINFO + UTF-16 text; freed after DestroyMenu
}

type msaaMenuInfo struct {
	Signature, Length uint32
	Text              uintptr
}

// MSAA's documented owner-drawn menu protocol exposes complete labels, including paths,
// without retaining Go heap pointers in Windows or replacing its accessibility provider.
func (s *menuSkin) itemData(it app.Item) uintptr {
	text, err := windows.UTF16FromString(it.Text())
	if err != nil {
		return 0
	}
	var header msaaMenuInfo
	size := unsafe.Sizeof(header)
	ptr, _, _ := menuAllocate.Call(0x40, size+uintptr(len(text)*2))
	if ptr == 0 {
		return 0
	}
	header = msaaMenuInfo{Signature: 0xaa0df00d, Length: uint32(len(text) - 1), Text: ptr + size}
	menuCopy.Call(ptr, uintptr(unsafe.Pointer(&header)), size)
	menuCopy.Call(header.Text, uintptr(unsafe.Pointer(&text[0])), uintptr(len(text)*2))
	s.memory = append(s.memory, ptr)
	s.items[ptr] = it
	return ptr
}

// Screen readers and high contrast get unmodified system menus, with complete text labels.
func nativeMenuRequired() bool {
	hc := struct {
		Size, Flags uint32
		Scheme      *uint16
	}{}
	hc.Size = uint32(unsafe.Sizeof(hc))
	ok, _, _ := menuSPI.Call(0x0042, uintptr(hc.Size), uintptr(unsafe.Pointer(&hc)), 0)
	if ok == 0 || hc.Flags&1 != 0 {
		return true
	}
	var reader int32
	ok, _, _ = menuSPI.Call(0x0046, 0, uintptr(unsafe.Pointer(&reader)), 0)
	return ok == 0 || reader != 0
}

func newMenuSkin(pt point) *menuSkin {
	if gdip.Startup() != nil {
		return nil
	}
	s := &menuSkin{scale: 1, width: 320, maxHeight: 700, items: map[uintptr]app.Item{}, lists: map[uintptr][]app.Item{}}
	monitor, _, _ := menuMonitor.Call(uintptr(uint64(uint32(pt.X))|uint64(uint32(pt.Y))<<32), 2)
	info := struct {
		Size          uint32
		Monitor, Work menuRect
		Flags         uint32
	}{}
	info.Size = uint32(unsafe.Sizeof(info))
	var dpiX, dpiY uint32
	if menuDPI.Find() == nil {
		result, _, _ := menuDPI.Call(monitor, 0, uintptr(unsafe.Pointer(&dpiX)), uintptr(unsafe.Pointer(&dpiY)))
		if result == 0 && dpiX >= 96 && dpiX <= 480 {
			s.scale = float64(dpiX) / 96
		}
	}
	s.width = s.px(320)
	if ok, _, _ := menuMonitorInfo.Call(monitor, uintptr(unsafe.Pointer(&info))); ok != 0 {
		s.maxHeight = int(info.Work.Bottom-info.Work.Top) - s.px(16)
		s.width = min(s.width, int(info.Work.Right-info.Work.Left)-s.px(24))
	}
	s.brush, _, _ = menuBrush.Call(0x22150b) // COLORREF: #0b1522
	if s.brush == 0 || s.width < 100 {
		s.close()
		return nil
	}
	return s
}

func (s *menuSkin) px(v int) int { return int(math.Round(float64(v) * s.scale)) }
func (s *menuSkin) close() {
	for _, ptr := range s.memory {
		menuFree.Call(ptr)
	}
	s.memory = nil
	if s.brush != 0 {
		menuDelete.Call(s.brush)
		s.brush = 0
	}
}
func menuSeparator(it app.Item) bool { return it.ID == 0 && it.Label == "" && len(it.Sub) == 0 }
func (s *menuSkin) height(it app.Item) int {
	if menuSeparator(it) {
		return s.px(10)
	}
	h := 36
	if it.Detail != "" {
		h += 20
	}
	if it.Note != "" {
		h += 18
	}
	return s.px(h)
}

func (s *menuSkin) build(items []app.Item) uintptr {
	h, _, _ := pCreatePopupMenu.Call()
	if h == 0 {
		return 0
	}
	info := menuInfo{Mask: 0x13, Style: 0x80000000, MaxHeight: uint32(s.maxHeight), Background: s.brush}
	info.Size = uint32(unsafe.Sizeof(info))
	if ok, _, _ := menuSetInfo.Call(h, uintptr(unsafe.Pointer(&info))); ok == 0 {
		pDestroyMenu.Call(h)
		return 0
	}
	s.lists[h] = items
	for pos, it := range items {
		var sub uintptr
		if len(it.Sub) > 0 {
			sub = s.build(it.Sub)
			if sub == 0 {
				pDestroyMenu.Call(h)
				return 0
			}
		}
		data := s.itemData(it)
		if data == 0 {
			if sub != 0 {
				pDestroyMenu.Call(sub)
			}
			pDestroyMenu.Call(h)
			return 0
		}
		text := utf16(menuText(it.Text()))
		mi := menuItemInfo{Mask: 0x167, Type: 0x100, ID: uint32(it.ID), Sub: sub, Data: data, Text: text}
		// MIIM_STATE | ID | SUBMENU | DATA | STRING | FTYPE; retain the real string for accessibility.
		mi.Size = uint32(unsafe.Sizeof(mi))
		if it.Disabled {
			mi.State |= 3
		}
		if it.Checked {
			mi.State |= 8
		}
		if it.Default {
			mi.State |= 0x1000
		}
		if menuSeparator(it) {
			mi.Type |= 0x800
		}
		if ok, _, _ := menuInsert.Call(h, uintptr(pos), 1, uintptr(unsafe.Pointer(&mi))); ok == 0 {
			if sub != 0 {
				pDestroyMenu.Call(sub)
			}
			pDestroyMenu.Call(h)
			return 0
		}
	}
	return h
}

func (s *menuSkin) measure(ptr uintptr) uintptr {
	if ptr == 0 {
		return 0
	}
	// Copy the OS-owned callback payload through Win32, without constructing Go pointers
	// from integer addresses or retaining native pointers beyond the synchronous callback.
	var m measureItem
	menuCopy.Call(uintptr(unsafe.Pointer(&m)), ptr, unsafe.Sizeof(m))
	it, ok := s.items[m.Data]
	if m.Type != 1 || !ok {
		return 0
	}
	m.Width, m.Height = uint32(s.width), uint32(s.height(it))
	menuCopy.Call(ptr, uintptr(unsafe.Pointer(&m)), unsafe.Sizeof(m))
	return 1
}

func (s *menuSkin) draw(ptr uintptr) uintptr {
	if ptr == 0 {
		return 0
	}
	var d drawItem
	menuCopy.Call(uintptr(unsafe.Pointer(&d)), ptr, unsafe.Sizeof(d))
	it, ok := s.items[d.Data]
	if d.Type != 1 || !ok {
		return 0
	}
	width, height := int(d.Rect.Right-d.Rect.Left), int(d.Rect.Bottom-d.Rect.Top)
	c, err := s.render(it, width, height, d.State&1 != 0, d.State&0x10 != 0)
	if err != nil {
		return 0
	}
	defer c.Close()
	menuBlit.Call(d.DC, uintptr(d.Rect.Left), uintptr(d.Rect.Top), uintptr(width), uintptr(height), c.DC, 0, 0, 0x00cc0020)
	return 1
}

func (s *menuSkin) render(it app.Item, width, height int, selected, focused bool) (*gdip.Canvas, error) {
	c, err := gdip.NewCanvas(width, height)
	if err != nil {
		return nil, err
	}
	z := s.scale
	w, h := float64(width), float64(height)
	c.FillRoundRect(0, 0, w, h, 0, 0xff0b1522)
	if menuSeparator(it) {
		c.FillRoundRect(14*z, 4*z, w-28*z, z, 0, 0xff283f53)
		return c, nil
	}
	active := selected && !it.Disabled
	if active {
		c.FillRoundRect(5*z, 2*z, w-10*z, h-4*z, 5*z, 0xff20394f)
	}
	if active && focused {
		c.StrokeRoundRect(5*z, 2*z, w-10*z, h-4*z, 5*z, 0xff8fc7e8, z)
	}
	text, muted, accent := uint32(0xffe9eff4), uint32(0xffa8bfd3), uint32(0xff8fc7e8)
	if it.Disabled && !it.Status {
		text, muted, accent = 0xff728ba1, 0xff728ba1, 0xff728ba1
	}
	if it.Status {
		text = 0xffa8bfd3
	}
	if it.Danger {
		accent = 0xffffb6b6
	}
	if it.Checked {
		c.DrawText("✓", "Segoe UI", 14*z, 0, accent, 13*z, 7*z, 22*z, 24*z)
	} else {
		drawMenuIcon(c, it.Icon, 15*z, 11*z, z, accent)
	}
	style := 0
	if it.Default {
		style = 1
	}
	c.DrawText(it.Label, "Inter", 13*z, style, text, 42*z, 8*z, w-72*z, 22*z)
	y := 30 * z
	if it.Note != "" {
		c.DrawText(it.Note, "Inter", 11*z, 0, muted, 42*z, y, w-60*z, 18*z)
		y += 18 * z
	}
	if it.Detail != "" {
		c.DrawPath(it.Detail, "Inter", 11*z, 0, muted, 42*z, y, w-60*z, 20*z)
	}
	if len(it.Sub) > 0 {
		c.DrawText("›", "Segoe UI", 20*z, 0, accent, w-24*z, 4*z, 20*z, 28*z)
	}
	return c, nil
}

// Small geometric line icons, drawn in device pixels without icon fonts or external assets.
func drawMenuIcon(c *gdip.Canvas, name string, x, y, z float64, color uint32) {
	if name == "" {
		return
	}
	line := func(ax, ay, bx, by float64) {
		dx, dy := bx-ax, by-ay
		l := math.Hypot(dx, dy)
		if l == 0 {
			return
		}
		nx, ny := -dy/l*.65, dx/l*.65
		c.FillPolygon([][2]float64{{x + (ax+nx)*z, y + (ay+ny)*z}, {x + (bx+nx)*z, y + (by+ny)*z}, {x + (bx-nx)*z, y + (by-ny)*z}, {x + (ax-nx)*z, y + (ay-ny)*z}}, color)
	}
	box := func(a, b, w, h, r float64) { c.StrokeRoundRect(x+a*z, y+b*z, w*z, h*z, r*z, color, 1.3*z) }
	switch name {
	case "folder", "open", "history":
		line(0, 4, 0, 13)
		line(0, 13, 16, 13)
		line(16, 13, 16, 4)
		line(16, 4, 7, 4)
		line(7, 4, 5, 1)
		line(5, 1, 0, 1)
		line(0, 1, 0, 4)
	case "profile":
		box(5, 0, 6, 6, 3)
		box(1, 9, 14, 6, 3)
	case "pause":
		line(5, 1, 5, 14)
		line(11, 1, 11, 14)
	case "play":
		line(3, 0, 14, 7)
		line(14, 7, 3, 14)
		line(3, 14, 3, 0)
	case "search":
		box(0, 0, 11, 11, 5.5)
		line(9, 9, 15, 15)
	case "add":
		line(8, 1, 8, 15)
		line(1, 8, 15, 8)
	case "quit":
		line(1, 1, 7, 1)
		line(1, 1, 1, 14)
		line(1, 14, 7, 14)
		line(6, 7, 16, 7)
		line(12, 3, 16, 7)
		line(16, 7, 12, 11)
	case "stop":
		box(2, 1, 12, 12, 2)
	case "refresh", "restore", "update":
		box(1, 1, 13, 13, 6)
		line(10, 0, 15, 1)
		line(15, 1, 14, 6)
	case "log":
		box(2, 0, 12, 15, 1)
		line(5, 4, 11, 4)
		line(5, 8, 11, 8)
		line(5, 11, 9, 11)
	case "language", "globe":
		box(1, 0, 14, 14, 7)
		line(1, 7, 15, 7)
		box(5, 0, 6, 14, 3)
	case "link":
		box(0, 5, 10, 7, 3)
		box(6, 1, 10, 7, 3)
	case "status-on", "status-off", "status-wait":
		box(3, 2, 10, 10, 5)
	case "notifications", "bell":
		box(3, 1, 10, 11, 4)
		line(1, 12, 15, 12)
		line(6, 15, 10, 15)
	default:
		box(1, 1, 14, 12, 2)
		line(1, 5, 15, 5)
	}
}

func (s *menuSkin) menuChar(ch rune, menu uintptr) uintptr {
	list := s.lists[menu]
	current := -1
	for i := range list {
		state, _, _ := menuGetState.Call(menu, uintptr(i), 0x400)
		if state&0x80 != 0 {
			current = i
		}
	}
	for n := 1; n <= len(list); n++ {
		i := (current + n) % len(list)
		it := list[i]
		first, _ := utf8.DecodeRuneInString(it.Label)
		if !it.Disabled && !menuSeparator(it) && strings.EqualFold(string(first), string(ch)) {
			return uintptr(i) | 3<<16
		}
	}
	return 0
}
