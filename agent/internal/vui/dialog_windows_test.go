//go:build windows

package vui

import (
	"fmt"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func sample(n int) Dialog {
	d := Dialog{Title: "Fontes encontradas", Intro: "O Vigil encontrou estas pastas com troféus:",
		Question: "Acompanhar essas pastas? O agent só lê os arquivos; nunca altera nada.",
		Yes:      "Acompanhar", No: "Agora não"}
	for i := 0; i < n; i++ {
		d.Lines = append(d.Lines, Line{Title: fmt.Sprintf("RPCS3 (beta) — %d jogos, %d troféus", i+2, 10*i+2),
			Detail: fmt.Sprintf(`C:\Users\jogador\Documents\Emuladores\Uma pasta com nome bem comprido %d\rpcs3`, i)})
	}
	return d
}

func TestRenderDialogs(t *testing.T) {
	out := os.Getenv("VIGIL_DIALOG_PNG") // set it to a folder to look at the result
	for _, scale := range []float64{1, 1.25, 1.5, 2} {
		heights := []int{}
		for _, n := range []int{1, 3, 8} {
			d := sample(n)
			if n == 8 {
				d.Lines = d.Lines[:8]
				d.More = "…e mais 2 pastas"
			}
			c, rects, err := Render(d, 0, -1, scale)
			if err != nil {
				t.Fatal(err)
			}
			if len(rects) != 2 {
				t.Fatalf("two buttons, got %d", len(rects))
			}
			for _, r := range rects {
				if r.x < 0 || r.y < 0 || r.x+r.w > float64(c.W) || r.y+r.h > float64(c.H) {
					t.Fatalf("button outside the dialog: %+v in %dx%d", r, c.W, c.H)
				}
			}
			if rects[0].x < rects[1].x {
				t.Fatal("the primary button is the rightmost one")
			}
			heights = append(heights, c.H)
			if out != "" {
				f, _ := os.Create(filepath.Join(out, fmt.Sprintf("dialog-%d-%.2f.png", n, scale)))
				png.Encode(f, c.NRGBA())
				f.Close()
			}
			c.Close()
		}
		if !(heights[0] < heights[1] && heights[1] < heights[2]) {
			t.Fatalf("height grows with the lines: %v", heights)
		}
		// at most MaxLines rows are drawn: 8 lines + "more" is not much taller than 6 lines
		six, _, _ := Render(sample(6), 0, -1, scale)
		if heights[2]-six.H > int(40*scale) {
			t.Fatalf("lines are capped at %d", MaxLines)
		}
		six.Close()
	}
}

func TestFocusRingAndSingleButton(t *testing.T) {
	a, rects, _ := Render(sample(1), 0, -1, 1)
	b, _, _ := Render(sample(1), 1, -1, 1)
	defer a.Close()
	defer b.Close()
	// the ring is drawn around the focused button
	r := rects[1]
	x, y := int(r.x-3), int(r.y+r.h/2)
	if a.NRGBA().NRGBAAt(x, y) == b.NRGBA().NRGBAAt(x, y) {
		t.Fatal("focus on the second button must show a ring around it")
	}
	ok, rs, err := Render(Dialog{Title: "Vigil Trophies", Intro: "Essa pasta não parece ser do RPCS3.", Yes: "OK"}, 0, -1, 1)
	if err != nil || len(rs) != 1 {
		t.Fatalf("message: %v %d", err, len(rs))
	}
	ok.Close()
}

func TestEnglishSourcesDialog(t *testing.T) {
	d := Dialog{Title: "Sources found", Intro: "Vigil found these folders with trophies:",
		Lines:    []Line{{Title: "bbport — Bloodborne", Detail: `C:\Users\player\bloodborne_pc\user`}},
		Question: "Watch them? The agent only reads these folders; it never changes anything.",
		Yes:      "Watch them", No: "Not now"}
	for _, scale := range []float64{1, 1.25, 1.5, 2} {
		c, rs, err := Render(d, 0, -1, scale)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rs {
			if r.x < 0 || r.y < 0 || r.x+r.w > float64(c.W) || r.y+r.h > float64(c.H) {
				t.Fatalf("button outside the dialog at %.2f scaling", scale)
			}
		}
		if out := os.Getenv("VIGIL_DIALOG_PNG"); out != "" {
			f, err := os.Create(filepath.Join(out, fmt.Sprintf("dialog-en-%.2f.png", scale)))
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(f, c.NRGBA())
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
		c.Close()
	}
}

func TestDialogContrast(t *testing.T) {
	luminance := func(argb uint32) float64 {
		channel := func(v uint32) float64 {
			c := float64(v&0xff) / 255
			if c <= 0.04045 {
				return c / 12.92
			}
			return math.Pow((c+0.055)/1.055, 2.4)
		}
		return 0.2126*channel(argb>>16) + 0.7152*channel(argb>>8) + 0.0722*channel(argb)
	}
	for _, pair := range [][2]uint32{{colorText, colorBg}, {colorMuted, colorBg}, {colorInk, colorAccent}, {colorInk, colorHover}} {
		a, b := luminance(pair[0]), luminance(pair[1])
		if a < b {
			a, b = b, a
		}
		if ratio := (a + 0.05) / (b + 0.05); ratio < 4.5 {
			t.Errorf("text contrast %.2f:1 needs at least 4.5:1", ratio)
		}
	}
}
