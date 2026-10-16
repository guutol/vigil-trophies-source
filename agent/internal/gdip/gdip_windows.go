//go:build windows

// Package gdip is a small wrapper over the GDI+ flat API (gdiplus.dll, part of Windows): an
// antialiased 32-bit canvas backed by a DIB section (for layered windows), rounded rectangles,
// gradients, text and PNG images. PNGs are decoded in Go (image/png) and handed to GDI+ as
// premultiplied pixels, so no COM streams are involved.
package gdip

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"math"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	gdiplus = windows.NewLazySystemDLL("gdiplus.dll")
	gdi32   = windows.NewLazySystemDLL("gdi32.dll")
	user32  = windows.NewLazySystemDLL("user32.dll")

	pStartup                  = gdiplus.NewProc("GdiplusStartup")
	pCreateBitmapFromScan0    = gdiplus.NewProc("GdipCreateBitmapFromScan0")
	pGetImageGraphicsContext  = gdiplus.NewProc("GdipGetImageGraphicsContext")
	pDeleteGraphics           = gdiplus.NewProc("GdipDeleteGraphics")
	pDisposeImage             = gdiplus.NewProc("GdipDisposeImage")
	pSetSmoothingMode         = gdiplus.NewProc("GdipSetSmoothingMode")
	pSetPixelOffsetMode       = gdiplus.NewProc("GdipSetPixelOffsetMode")
	pSetInterpolationMode     = gdiplus.NewProc("GdipSetInterpolationMode")
	pSetTextRenderingHint     = gdiplus.NewProc("GdipSetTextRenderingHint")
	pGraphicsClear            = gdiplus.NewProc("GdipGraphicsClear")
	pCreatePath               = gdiplus.NewProc("GdipCreatePath")
	pDeletePath               = gdiplus.NewProc("GdipDeletePath")
	pAddPathArc               = gdiplus.NewProc("GdipAddPathArc")
	pClosePathFigure          = gdiplus.NewProc("GdipClosePathFigure")
	pAddPathPolygon           = gdiplus.NewProc("GdipAddPathPolygon")
	pCreateSolidFill          = gdiplus.NewProc("GdipCreateSolidFill")
	pCreateLineBrush          = gdiplus.NewProc("GdipCreateLineBrush")
	pDeleteBrush              = gdiplus.NewProc("GdipDeleteBrush")
	pCreatePen1               = gdiplus.NewProc("GdipCreatePen1")
	pDeletePen                = gdiplus.NewProc("GdipDeletePen")
	pFillPath                 = gdiplus.NewProc("GdipFillPath")
	pDrawPath                 = gdiplus.NewProc("GdipDrawPath")
	pSetClipPath              = gdiplus.NewProc("GdipSetClipPath")
	pResetClip                = gdiplus.NewProc("GdipResetClip")
	pDrawImageRectI           = gdiplus.NewProc("GdipDrawImageRectI")
	pCreateFontFamilyFromName = gdiplus.NewProc("GdipCreateFontFamilyFromName")
	pDeleteFontFamily         = gdiplus.NewProc("GdipDeleteFontFamily")
	pCreateFont               = gdiplus.NewProc("GdipCreateFont")
	pDeleteFont               = gdiplus.NewProc("GdipDeleteFont")
	pCreateStringFormat       = gdiplus.NewProc("GdipCreateStringFormat")
	pDeleteStringFormat       = gdiplus.NewProc("GdipDeleteStringFormat")
	pSetStringFormatTrimming  = gdiplus.NewProc("GdipSetStringFormatTrimming")
	pSetStringFormatFlags     = gdiplus.NewProc("GdipSetStringFormatFlags")
	pDrawString               = gdiplus.NewProc("GdipDrawString")
	pMeasureString            = gdiplus.NewProc("GdipMeasureString")
	pCreateHICONFromBitmap    = gdiplus.NewProc("GdipCreateHICONFromBitmap")

	pCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	pCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	pSelectObject       = gdi32.NewProc("SelectObject")
	pDeleteObject       = gdi32.NewProc("DeleteObject")
	pDeleteDC           = gdi32.NewProc("DeleteDC")
	pDestroyIcon        = user32.NewProc("DestroyIcon")
)

const (
	pixelFormat32bppPARGB = 0xE200B
	unitPixel             = 2
	smoothingAntiAlias    = 4
	pixelOffsetHigh       = 4
	interpolationHQBicub  = 7
	textHintAntiAlias     = 4
	trimmingEllipsis      = 3
	trimmingEllipsisWord  = 4
	trimmingEllipsisPath  = 5
	formatNoWrap          = 0x1000
	formatLineLimit       = 0x2000
)

var (
	startOnce sync.Once
	startErr  error
)

// Startup initializes GDI+ once per process.
func Startup() error {
	startOnce.Do(func() {
		input := struct {
			Version                  uint32
			DebugEventCallback       uintptr
			SuppressBackgroundThread int32
			SuppressExternalCodecs   int32
		}{Version: 1}
		var token uintptr
		if st, _, _ := pStartup.Call(uintptr(unsafe.Pointer(&token)), uintptr(unsafe.Pointer(&input)), 0); st != 0 {
			startErr = fmt.Errorf("GdiplusStartup: status %d", st)
		}
	})
	return startErr
}

// call runs a GDI+ function and returns its status (0 = Ok).
func call(p *windows.LazyProc, args ...uintptr) uintptr {
	st, _, _ := p.Call(args...)
	return st
}

func f(v float64) uintptr { return uintptr(math.Float32bits(float32(v))) }

func ok(st uintptr, what string) error {
	if st != 0 {
		return fmt.Errorf("%s: GDI+ status %d", what, st)
	}
	return nil
}

// Canvas is a premultiplied BGRA image (a DIB section, selected into DC) that GDI+ draws on.
type Canvas struct {
	W, H int
	DC   uintptr
	// Pix is the DIB's memory: W*H*4 bytes, top row first, premultiplied BGRA.
	Pix []byte

	dib, old, bitmap, graphics uintptr
}

// NewCanvas creates a transparent w×h canvas.
func NewCanvas(w, h int) (*Canvas, error) {
	if err := Startup(); err != nil {
		return nil, err
	}
	c := &Canvas{W: w, H: h}
	dc, _, err := pCreateCompatibleDC.Call(0)
	if dc == 0 {
		return nil, err
	}
	c.DC = dc
	bi := struct {
		Size          uint32
		Width         int32
		Height        int32
		Planes        uint16
		BitCount      uint16
		Compression   uint32
		SizeImage     uint32
		XPelsPerMeter int32
		YPelsPerMeter int32
		ClrUsed       uint32
		ClrImportant  uint32
		Colors        uint32
	}{Width: int32(w), Height: -int32(h), Planes: 1, BitCount: 32}
	bi.Size = 40
	var bits *byte
	dib, _, err := pCreateDIBSection.Call(dc, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if dib == 0 || bits == nil {
		c.Close()
		return nil, err
	}
	c.dib = dib
	c.old, _, _ = pSelectObject.Call(dc, dib)
	c.Pix = unsafe.Slice(bits, w*h*4)
	if err := ok(call(pCreateBitmapFromScan0, uintptr(w), uintptr(h), uintptr(w*4), pixelFormat32bppPARGB,
		uintptr(unsafe.Pointer(bits)), uintptr(unsafe.Pointer(&c.bitmap))), "bitmap"); err != nil {
		c.Close()
		return nil, err
	}
	if err := ok(call(pGetImageGraphicsContext, c.bitmap, uintptr(unsafe.Pointer(&c.graphics))), "graphics"); err != nil {
		c.Close()
		return nil, err
	}
	pSetSmoothingMode.Call(c.graphics, smoothingAntiAlias)
	pSetPixelOffsetMode.Call(c.graphics, pixelOffsetHigh)
	pSetInterpolationMode.Call(c.graphics, interpolationHQBicub)
	pSetTextRenderingHint.Call(c.graphics, textHintAntiAlias)
	pGraphicsClear.Call(c.graphics, 0)
	return c, nil
}

// Close frees everything.
func (c *Canvas) Close() {
	if c.graphics != 0 {
		pDeleteGraphics.Call(c.graphics)
	}
	if c.bitmap != 0 {
		pDisposeImage.Call(c.bitmap)
	}
	if c.old != 0 {
		pSelectObject.Call(c.DC, c.old)
	}
	if c.dib != 0 {
		pDeleteObject.Call(c.dib)
	}
	if c.DC != 0 {
		pDeleteDC.Call(c.DC)
	}
	*c = Canvas{}
}

// roundRect builds a closed rounded-rectangle path.
func roundRect(x, y, w, h, r float64) (uintptr, error) {
	var path uintptr
	if err := ok(call(pCreatePath, 0, uintptr(unsafe.Pointer(&path))), "path"); err != nil {
		return 0, err
	}
	d := r * 2
	if d <= 0 {
		d = 0.01
	}
	pAddPathArc.Call(path, f(x), f(y), f(d), f(d), f(180), f(90))
	pAddPathArc.Call(path, f(x+w-d), f(y), f(d), f(d), f(270), f(90))
	pAddPathArc.Call(path, f(x+w-d), f(y+h-d), f(d), f(d), f(0), f(90))
	pAddPathArc.Call(path, f(x), f(y+h-d), f(d), f(d), f(90), f(90))
	pClosePathFigure.Call(path)
	return path, nil
}

// FillRoundRect fills with one colour (0xAARRGGBB).
func (c *Canvas) FillRoundRect(x, y, w, h, r float64, argb uint32) {
	path, err := roundRect(x, y, w, h, r)
	if err != nil {
		return
	}
	defer pDeletePath.Call(path)
	var brush uintptr
	if call(pCreateSolidFill, uintptr(argb), uintptr(unsafe.Pointer(&brush))) != 0 {
		return
	}
	defer pDeleteBrush.Call(brush)
	pFillPath.Call(c.graphics, brush, path)
}

// FillRoundRectGradient fills with a left-to-right gradient.
func (c *Canvas) FillRoundRectGradient(x, y, w, h, r float64, from, to uint32) {
	path, err := roundRect(x, y, w, h, r)
	if err != nil {
		return
	}
	defer pDeletePath.Call(path)
	p1 := [2]float32{float32(x - 1), float32(y)}
	p2 := [2]float32{float32(x + w + 1), float32(y)}
	var brush uintptr
	if call(pCreateLineBrush, uintptr(unsafe.Pointer(&p1)), uintptr(unsafe.Pointer(&p2)), uintptr(from), uintptr(to), 0,
		uintptr(unsafe.Pointer(&brush))) != 0 {
		return
	}
	defer pDeleteBrush.Call(brush)
	pFillPath.Call(c.graphics, brush, path)
}

// StrokeRoundRect draws the outline.
func (c *Canvas) StrokeRoundRect(x, y, w, h, r float64, argb uint32, width float64) {
	path, err := roundRect(x, y, w, h, r)
	if err != nil {
		return
	}
	defer pDeletePath.Call(path)
	var pen uintptr
	if call(pCreatePen1, uintptr(argb), f(width), unitPixel, uintptr(unsafe.Pointer(&pen))) != 0 {
		return
	}
	defer pDeletePen.Call(pen)
	pDrawPath.Call(c.graphics, pen, path)
}

// ClipRoundRect limits drawing to a rounded rectangle until ResetClip.
func (c *Canvas) ClipRoundRect(x, y, w, h, r float64) {
	path, err := roundRect(x, y, w, h, r)
	if err != nil {
		return
	}
	defer pDeletePath.Call(path)
	pSetClipPath.Call(c.graphics, path, 0)
}

// ResetClip removes the clip.
func (c *Canvas) ResetClip() { pResetClip.Call(c.graphics) }

// FillPolygon fills a closed polygon.
func (c *Canvas) FillPolygon(points [][2]float64, argb uint32) {
	if len(points) < 3 {
		return
	}
	pts := make([]float32, 0, len(points)*2)
	for _, p := range points {
		pts = append(pts, float32(p[0]), float32(p[1]))
	}
	var path uintptr
	if call(pCreatePath, 0, uintptr(unsafe.Pointer(&path))) != 0 {
		return
	}
	defer pDeletePath.Call(path)
	pAddPathPolygon.Call(path, uintptr(unsafe.Pointer(&pts[0])), uintptr(len(points)))
	var brush uintptr
	if call(pCreateSolidFill, uintptr(argb), uintptr(unsafe.Pointer(&brush))) != 0 {
		return
	}
	defer pDeleteBrush.Call(brush)
	pFillPath.Call(c.graphics, brush, path)
	runtime.KeepAlive(pts)
}

// Font styles.
const (
	Regular = 0
	Bold    = 1
)

// text holds a font, a string format and a brush for one piece of text.
type text struct {
	fam, font, format, brush uintptr
}

func newText(family string, size float64, style int, argb uint32, wrap bool) (*text, error) {
	return newTextTrim(family, size, style, argb, wrap, false)
}

func newTextTrim(family string, size float64, style int, argb uint32, wrap, path bool) (*text, error) {
	t := &text{}
	fam, err := fontFamily(family)
	if err != nil {
		return nil, err
	}
	t.fam = fam
	if call(pCreateFont, fam, f(size), uintptr(style), unitPixel, uintptr(unsafe.Pointer(&t.font))) != 0 {
		t.close()
		return nil, errors.New("font")
	}
	if call(pCreateStringFormat, 0, 0, uintptr(unsafe.Pointer(&t.format))) != 0 {
		t.close()
		return nil, errors.New("format")
	}
	if wrap {
		pSetStringFormatTrimming.Call(t.format, trimmingEllipsisWord)
		pSetStringFormatFlags.Call(t.format, formatLineLimit)
	} else {
		trim := uintptr(trimmingEllipsis)
		if path {
			trim = trimmingEllipsisPath
		}
		pSetStringFormatTrimming.Call(t.format, trim)
		pSetStringFormatFlags.Call(t.format, formatNoWrap)
	}
	if call(pCreateSolidFill, uintptr(argb), uintptr(unsafe.Pointer(&t.brush))) != 0 {
		t.close()
		return nil, errors.New("brush")
	}
	return t, nil
}

func (t *text) close() {
	if t.brush != 0 {
		pDeleteBrush.Call(t.brush)
	}
	if t.format != 0 {
		pDeleteStringFormat.Call(t.format)
	}
	if t.font != 0 {
		pDeleteFont.Call(t.font)
	}
	if t.fam != 0 {
		pDeleteFontFamily.Call(t.fam)
	}
}

// DrawText draws one line, cut with "…" when it does not fit in w.
func (c *Canvas) DrawText(s, family string, size float64, style int, argb uint32, x, y, w, h float64) {
	t, err := newText(family, size, style, argb, false)
	if err != nil {
		return
	}
	defer t.close()
	c.drawString(t, s, x, y, w, h)
}

func (c *Canvas) drawString(t *text, s string, x, y, w, h float64) {
	u, err := windows.UTF16FromString(s)
	if err != nil {
		return
	}
	rect := [4]float32{float32(x), float32(y), float32(w), float32(h)}
	pDrawString.Call(c.graphics, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), t.font,
		uintptr(unsafe.Pointer(&rect)), t.format, t.brush)
}

// DrawPath draws a file path on one line, eliding its middle when it does not fit.
func (c *Canvas) DrawPath(s, family string, size float64, style int, argb uint32, x, y, w, h float64) {
	t, err := newTextTrim(family, size, style, argb, false, true)
	if err != nil {
		return
	}
	defer t.close()
	c.drawString(t, s, x, y, w, h)
}

// Lines is how many lines s needs at width w (word wrap), at most maxLines.
func (c *Canvas) Lines(s, family string, size float64, style int, w float64, maxLines int) int {
	t, err := newText(family, size, style, 0xFF000000, true)
	if err != nil {
		return 1
	}
	defer t.close()
	u, err := windows.UTF16FromString(s)
	if err != nil || len(u) <= 1 {
		return 1
	}
	layout := [4]float32{0, 0, float32(w), 1e6}
	var box [4]float32
	var fitted, lines int32
	pMeasureString.Call(c.graphics, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), t.font,
		uintptr(unsafe.Pointer(&layout)), t.format, uintptr(unsafe.Pointer(&box)),
		uintptr(unsafe.Pointer(&fitted)), uintptr(unsafe.Pointer(&lines)))
	return max(1, min(int(lines), maxLines))
}

// DrawTextLines draws s wrapped in up to lines lines of height lineH, the last one ending in "…"
// when the text does not fit.
func (c *Canvas) DrawTextLines(s, family string, size float64, style int, argb uint32, x, y, w, lineH float64, lines int) {
	t, err := newText(family, size, style, argb, true)
	if err != nil {
		return
	}
	defer t.close()
	c.drawString(t, s, x, y, w, lineH*float64(lines)+1)
}

// fontFamily finds the first installed family of name (names may be separated by "|"), then
// Segoe UI, then Arial.
func fontFamily(name string) (uintptr, error) {
	for _, n := range append(strings.Split(name, "|"), "Segoe UI", "Arial") {
		p, err := windows.UTF16PtrFromString(n)
		if err != nil {
			continue
		}
		var fam uintptr
		if st, _, _ := pCreateFontFamilyFromName.Call(uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&fam))); st == 0 {
			return fam, nil
		}
	}
	return 0, errors.New("no font")
}

// premultiplied decodes a PNG into premultiplied BGRA pixels for GDI+.
func premultiplied(data []byte) ([]byte, int, int, error) {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, err
	}
	b := img.Bounds()
	rgba := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy())) // image.RGBA is premultiplied
	draw.Draw(rgba, rgba.Bounds(), img, b.Min, draw.Src)
	pix := rgba.Pix
	for i := 0; i < len(pix); i += 4 {
		pix[i], pix[i+2] = pix[i+2], pix[i] // RGBA -> BGRA
	}
	return pix, b.Dx(), b.Dy(), nil
}

// withBitmap gives fn a GDI+ bitmap of the PNG, valid only during the call.
func withBitmap(data []byte, fn func(bitmap uintptr)) error {
	if err := Startup(); err != nil {
		return err
	}
	pix, w, h, err := premultiplied(data)
	if err != nil {
		return err
	}
	var bmp uintptr
	if err := ok(call(pCreateBitmapFromScan0, uintptr(w), uintptr(h), uintptr(w*4), pixelFormat32bppPARGB,
		uintptr(unsafe.Pointer(&pix[0])), uintptr(unsafe.Pointer(&bmp))), "image"); err != nil {
		return err
	}
	fn(bmp)
	pDisposeImage.Call(bmp)
	runtime.KeepAlive(pix) // GDI+ reads pix until the bitmap is disposed
	return nil
}

// DrawPNG draws a PNG scaled into the rectangle, clipped to rounded corners of radius r.
func (c *Canvas) DrawPNG(data []byte, x, y, w, h, r int) error {
	return withBitmap(data, func(bmp uintptr) {
		if r > 0 {
			c.ClipRoundRect(float64(x), float64(y), float64(w), float64(h), float64(r))
			defer c.ResetClip()
		}
		pDrawImageRectI.Call(c.graphics, bmp, uintptr(x), uintptr(y), uintptr(w), uintptr(h))
	})
}

// IconFromPNG makes an HICON (for a notification balloon); free it with DestroyIcon.
func IconFromPNG(data []byte) (uintptr, error) {
	var icon uintptr
	err := withBitmap(data, func(bmp uintptr) {
		pCreateHICONFromBitmap.Call(bmp, uintptr(unsafe.Pointer(&icon)))
	})
	if err == nil && icon == 0 {
		err = errors.New("cannot make an icon")
	}
	return icon, err
}

// DestroyIcon frees an icon from IconFromPNG.
func DestroyIcon(icon uintptr) {
	if icon != 0 {
		pDestroyIcon.Call(icon)
	}
}

// NRGBA returns the canvas as a straight-alpha image (for screenshots and tests).
func (c *Canvas) NRGBA() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, c.W, c.H))
	for i := 0; i < len(c.Pix); i += 4 {
		b, g, r, a := c.Pix[i], c.Pix[i+1], c.Pix[i+2], c.Pix[i+3]
		if a > 0 && a < 255 {
			r = byte(min(255, int(r)*255/int(a)))
			g = byte(min(255, int(g)*255/int(a)))
			b = byte(min(255, int(b)*255/int(a)))
		}
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = r, g, b, a
	}
	return img
}
