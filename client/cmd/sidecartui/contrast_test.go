package sidecartui

import (
	"image/color"
	"math"
	"testing"
)

// Every text color reads at WCAG AA (4.5:1) on the backgrounds it is drawn
// on: the terminal's, from the darkest to the lightest of its kind, and the
// selected row. A brand color that looks right on one theme can fail the
// other; this catches it before a person squints at it.
func TestTextColorsReadOnBothBackgrounds(t *testing.T) {
	defer setTheme(darkBackground(""))
	for _, dark := range []bool{false, true} {
		setTheme(dark)
		bgs := map[string]color.Color{"#F0F0F0": rgbHex(0xF0F0F0), "#FFFFFF": rgbHex(0xFFFFFF), "selected row": colSelBg}
		if dark {
			bgs = map[string]color.Color{"#000000": rgbHex(0x000000), "#1E1E1E": rgbHex(0x1E1E1E), "selected row": colSelBg}
		}
		texts := map[string]color.Color{"faint": colFaint, "strong": colStrong, "primary": colPrimaryText, "danger": colDangerText}
		for tn, fg := range texts {
			for bn, bg := range bgs {
				if c := contrast(fg, bg); c < 4.5 {
					t.Errorf("dark=%v: %s text on %s is %.2f:1, want 4.5:1 or more", dark, tn, bn, c)
				}
			}
		}
	}
	// White ink on the brand fills: badges, the header, buttons.
	for n, bg := range map[string]color.Color{"primary": colPrimary, "danger": colDanger} {
		if c := contrast(colInk, bg); c < 4.5 {
			t.Errorf("white on %s is %.2f:1", n, c)
		}
	}
}

func rgbHex(v uint32) color.Color {
	return color.RGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xFF}
}

func contrast(a, b color.Color) float64 {
	la, lb := luminance(a), luminance(b)
	return (math.Max(la, lb) + 0.05) / (math.Min(la, lb) + 0.05)
}

func luminance(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	lin := func(v uint32) float64 {
		x := float64(v) / 0xFFFF
		if x <= 0.03928 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}
