//go:build windows

package notify

import (
	"math"
	"testing"

	"github.com/vigil-tracker/agent/internal/gdip"
)

var scales = []float64{1, 1.25, 1.5, 2}

func TestRenderEveryCard(t *testing.T) {
	for _, lang := range []string{"pt-BR", "en"} {
		items := append(Samples(lang), SampleSummary(), UpdateItem("v0.1.0-beta.10", ""))
		for _, scale := range scales {
			for _, it := range items {
				c, err := Render(it, lang, scale)
				if err != nil {
					t.Fatalf("%s: %v", it.Grade, err)
				}
				img := c.NRGBA()
				if want := int(cardW*scale + 0.5); c.W != want {
					t.Fatalf("width %d, want %d", c.W, want)
				}
				if minH := int(minCardH*scale + 0.5); c.H < minH {
					t.Fatalf("height %d below %d", c.H, minH)
				}
				if a := img.NRGBAAt(0, 0).A; a != 0 {
					t.Errorf("rounded corner must be transparent, alpha %d", a)
				}
				if a := img.NRGBAAt(c.W/2, c.H/2).A; a < 240 {
					t.Errorf("card body must be opaque, alpha %d", a)
				}
				stripe := img.NRGBAAt(int(1*scale), c.H/2)
				if stripe.B <= stripe.R || stripe.G <= stripe.R || stripe.A < 240 {
					t.Errorf("%s: stripe colour %v", it.Grade, stripe)
				}
				icon := img.NRGBAAt(int(50*scale), c.H/2)
				// the Vigil mark on update cards is a line drawing: its centre may show the card
				if !it.Update && icon.R == 0x0b && icon.G == 0x15 && icon.B == 0x22 {
					t.Errorf("%s: icon missing", it.Grade)
				}
				c.Close()
			}
		}
	}
}

func TestLongNamesUseTwoLines(t *testing.T) {
	short := Item{Grade: Gold, Trophy: "Caçador", Game: "Bloodborne"}
	long := Item{Grade: Gold, Trophy: "O Caçador de Pesadelos que Acordou no Sonho Antes de Todos os Outros e Ficou",
		Game: "Bloodborne"}
	for _, scale := range scales {
		a, _ := Render(short, "pt-BR", scale)
		b, _ := Render(long, "pt-BR", scale)
		if b.H <= a.H {
			t.Errorf("scale %.2f: a long name should make the card taller (%d vs %d)", scale, b.H, a.H)
		}
		probe, _ := gdip.NewCanvas(1, 1)
		if n := probe.Lines(long.Trophy, fontTitle, titleSize*scale, gdip.Regular, 1e9, 9); n != 1 {
			t.Errorf("an unbounded width fits one line, got %d", n)
		}
		probe.Close()
		// never more than two lines: an even longer name gives the same height
		c, _ := Render(Item{Grade: Gold, Trophy: long.Trophy + " " + long.Trophy, Game: "x"}, "pt-BR", scale)
		if c.H != b.H {
			t.Errorf("scale %.2f: names are capped at 2 lines (%d vs %d)", scale, c.H, b.H)
		}
		a.Close()
		b.Close()
		c.Close()
	}
}

func luminance(argb uint32) float64 {
	ch := func(v uint32) float64 {
		c := float64(v&0xff) / 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(argb>>16) + 0.7152*ch(argb>>8) + 0.0722*ch(argb)
}

func contrast(a, b uint32) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// WCAG AA for normal text is 4.5:1. Measured against both card backgrounds.
func TestTextContrastIsAA(t *testing.T) {
	texts := map[string]uint32{"title": colorText, "game": colorMuted, "summary label": colorAccent}
	for g, c := range gradeColor {
		texts["label "+g] = c
	}
	for name, fg := range texts {
		for _, bg := range []uint32{colorBg, colorPlatBg1, colorPlatBg2} {
			if r := contrast(fg, bg); r < 4.5 {
				t.Errorf("%s on %06X: %.2f:1 (needs 4.5:1)", name, bg&0xffffff, r)
			}
		}
	}
}

func TestRenderSurvivesABrokenIcon(t *testing.T) {
	c, err := Render(Item{Grade: Gold, Trophy: "x", Icon: []byte("not a png")}, "en", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	icon := c.NRGBA().NRGBAAt(50, c.H/2)
	if icon.R == 0x0b && icon.G == 0x15 && icon.B == 0x22 {
		t.Fatal("the generic icon replaces a broken one")
	}
}

func TestIconFromPNG(t *testing.T) {
	h, err := gdip.IconFromPNG(GenericIcon(Platinum))
	if err != nil || h == 0 {
		t.Fatalf("icon: %v", err)
	}
	gdip.DestroyIcon(h)
	if _, err := gdip.IconFromPNG([]byte("junk")); err == nil {
		t.Fatal("junk is not an icon")
	}
}
