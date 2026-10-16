package sidecartui

import (
	"image/color"
	"os"
	"testing"
)

// light reports whether c reads as a light color, by luminance. It is the
// property every background-dependent gray must get right: dark on a light
// terminal, light on a dark one.
func light(c color.Color) bool {
	r, g, b, _ := c.RGBA() // 16-bit, 0..65535
	y := (299*int(r) + 587*int(g) + 114*int(b)) / 1000
	return y > 0x8000
}

// TestThemeFollowsBackground pins the fix for DES-234: the body-text gray must
// track the terminal's background, not a single startup guess. A terminal that
// does not set COLORFGBG (Warp among them) used to strand the palette on the
// dark variant and paint light text onto a light background.
func TestThemeFollowsBackground(t *testing.T) {
	// setTheme mutates the package palette; put it back for later tests.
	defer setTheme(darkBackground(os.Getenv("COLORFGBG")))

	setTheme(false) // a light terminal background
	if light(colStrong) {
		t.Errorf("on a light background, body text must be dark; colStrong is light")
	}

	setTheme(true) // a dark terminal background
	if !light(colStrong) {
		t.Errorf("on a dark background, body text must be light; colStrong is dark")
	}
}
