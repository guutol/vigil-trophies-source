//go:build windows

package tray

import (
	"github.com/vigil-tracker/agent/internal/app"
	"github.com/vigil-tracker/agent/internal/config"

	"golang.org/x/sys/windows"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

func TestMenuSkinNativeStringsAndDPI(t *testing.T) {
	if unsafe.Sizeof(menuItemInfo{}) != 80 || unsafe.Sizeof(measureItem{}) != 32 || unsafe.Sizeof(drawItem{}) != 64 {
		t.Fatal("Win64 menu ABI layout")
	}
	for _, scale := range []float64{1, 1.25, 1.5, 2} {
		s := newMenuSkin(point{})
		if s == nil {
			t.Fatal("skin allocation failed")
		}
		s.scale = scale
		s.width = s.px(320)
		items := []app.Item{{ID: 7, Label: "Folder & status", Detail: `C:\Synthetic\A long folder\user`, Note: "Watching", Checked: true}, app.Separator, {Label: "Manage folders", Sub: []app.Item{{ID: 8, Label: "Stop watching", Danger: true}}}}
		h := s.build(items)
		if h == 0 {
			s.close()
			t.Fatal("native owner-draw menu failed")
		}
		mi := menuItemInfo{Mask: 0x163, Length: 511}
		buf := make([]uint16, 512)
		mi.Text = &buf[0]
		mi.Size = uint32(unsafe.Sizeof(mi))
		ok, _, _ := user32.NewProc("GetMenuItemInfoW").Call(h, 0, 1, uintptr(unsafe.Pointer(&mi)))
		if ok == 0 || mi.Type&0x100 == 0 || windows.UTF16ToString(buf) != menuText(items[0].Text()) || mi.State&8 == 0 {
			t.Fatalf("native label/state missing: ok=%d info=%+v text=%q", ok, mi, windows.UTF16ToString(buf))
		}
		var header msaaMenuInfo
		menuCopy.Call(uintptr(unsafe.Pointer(&header)), mi.Data, unsafe.Sizeof(header))
		accessible := make([]uint16, header.Length+1)
		menuCopy.Call(uintptr(unsafe.Pointer(&accessible[0])), header.Text, uintptr(len(accessible)*2))
		if header.Signature != 0xaa0df00d || windows.UTF16ToString(accessible) != items[0].Text() {
			t.Fatal("MSAA name missing or shortened")
		}
		m := measureItem{Type: 1, Data: mi.Data}
		var pin runtime.Pinner
		pin.Pin(&m)
		if s.measure(uintptr(unsafe.Pointer(&m))) != 1 || int(m.Height) != s.px(74) {
			t.Fatalf("DPI measurement: %+v", m)
		}
		pin.Unpin()
		for _, item := range items {
			c, err := s.render(item, s.width, s.height(item), true, true)
			if err != nil {
				t.Fatal(err)
			}
			if c.NRGBA().NRGBAAt(0, 0).A == 0 {
				t.Fatal("transparent menu background")
			}
			c.Close()
		}
		if got := s.menuChar('m', h); got>>16 != 3 || got&0xffff != 2 {
			t.Fatalf("keyboard selection %x", got)
		}
		pDestroyMenu.Call(h)
		s.close()
	}
}

// Optional real Win32 tracking test: creates only its own hidden owner and posts keys only
// to that window (never SendInput or another application's window).
func TestNativeMenuTracking(t *testing.T) {
	if os.Getenv("VIGIL_MENU_NATIVE_TEST") == "" {
		t.Skip("optional native popup tracking")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	s := newMenuSkin(point{})
	if s == nil {
		t.Fatal("skin")
	}
	defer s.close()
	measured, painted := 0, 0

	proc := syscall.NewCallback(func(hwnd uintptr, msg uint32, w, l uintptr) uintptr {
		switch msg {
		case 0x2c:
			measured++
			return s.measure(l)
		case 0x2b:
			painted++
			return s.draw(l)
		}
		r, _, _ := pDefWindowProcW.Call(hwnd, uintptr(msg), w, l)
		return r
	})
	inst, _, _ := pGetModuleHandleW.Call(0)
	name := utf16("VigilMenuSyntheticTrackingTest")
	wc := wndClassEx{WndProc: proc, Instance: inst, ClassName: name}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if r, _, e := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		t.Fatal(e)
	}
	hwnd, _, e := pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(name)), 0, 0x80000000, 0, 0, 1, 1, 0, 0, inst, 0)
	if hwnd == 0 {
		t.Fatal(e)
	}
	defer func() {
		pDestroyWindow.Call(hwnd)
		user32.NewProc("UnregisterClassW").Call(uintptr(unsafe.Pointer(name)), inst)
	}()
	for _, key := range []uintptr{0x0d, 0x1b} {
		h := s.build([]app.Item{{Label: "Synthetic folder", Sub: []app.Item{{ID: 7, Label: "Stop watching"}}}, {ID: 8, Label: "Other action"}})
		if h == 0 {
			t.Fatal("menu")
		}
		done := make(chan struct{})
		go func() {
			select {
			case <-done:
				return
			case <-time.After(150 * time.Millisecond):
			}
			pPostMessageW.Call(hwnd, 0x100, 0x28, 0) // Down selects the first enabled action
			pPostMessageW.Call(hwnd, 0x100, 0x27, 0) // Right opens the folder submenu
			pPostMessageW.Call(hwnd, 0x100, 0x28, 0)
			pPostMessageW.Call(hwnd, 0x100, key, 0)
			if key == 0x1b {
				pPostMessageW.Call(hwnd, 0x100, key, 0)
			} // Escape child, then parent
			select {
			case <-done:
				return
			case <-time.After(1500 * time.Millisecond):
			}
			pPostMessageW.Call(hwnd, 0x001f, 0, 0) // WM_CANCELMODE, bounded fallback
		}()
		cmd, _, _ := pTrackPopupMenu.Call(h, tpmReturnCmd|tpmNoNotify, 40, 40, 0, hwnd, 0)
		close(done)
		pDestroyMenu.Call(h)
		if key == 0x0d && cmd != 7 {
			t.Fatalf("keyboard action returned %d", cmd)
		}
		if key == 0x1b && cmd != 0 {
			t.Fatalf("Escape returned %d", cmd)
		}
	}
	if measured == 0 || painted == 0 {
		t.Fatalf("native menu did not invoke renderer: measure=%d draw=%d", measured, painted)
	}
}

// Optional output uses the actual owner-draw renderer, with synthetic state and no tray instance.
func TestMenuRendererPreviews(t *testing.T) {
	dir := os.Getenv("VIGIL_MENU_PREVIEWS")
	if dir == "" {
		t.Skip("optional renderer previews")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"en", "pt-BR"} {
		s := newMenuSkin(point{})
		if s == nil {
			t.Fatal("skin")
		}
		s.scale = 2
		s.width = s.px(320)
		items := app.BuildMenu(app.Texts(lang), app.MenuState{Status: app.Status{State: app.Running}, Username: "hunter_demo", ProfileURL: "https://example.test/u/hunter_demo", Autostart: true})
		folder := app.BuildMenu(app.Texts(lang), app.MenuState{Status: app.Status{State: app.Running}, Username: "hunter_demo", Summaries: []app.Summary{{Config: config.SourceConfig{Type: "bbport-log", Path: `D:\Games\Bloodborne\user`}, Games: 1, Unlocks: 2}}})
		for _, it := range folder {
			if it.Icon == "folder" {
				folder = it.Sub
				break
			}
		}
		for name, list := range map[string][]app.Item{"main": items, "folders": folder} {
			height := 0
			for _, it := range list {
				height += s.height(it)
			}
			img := image.NewNRGBA(image.Rect(0, 0, s.width, height))
			y := 0
			for _, it := range list {
				h := s.height(it)
				c, err := s.render(it, s.width, h, it.Icon == "folder", false)
				if err != nil {
					t.Fatal(err)
				}
				draw.Draw(img, image.Rect(0, y, s.width, y+h), c.NRGBA(), image.Point{}, draw.Src)
				c.Close()
				y += h
			}
			f, err := os.Create(filepath.Join(dir, "menu-"+name+"-"+lang+".png"))
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(f, img)
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
		s.close()
	}
}
