// Package qr renders QR codes with Unicode half blocks (two modules per
// cell). Light modules are drawn as blocks, so codes scan on dark terminal
// backgrounds.
package qr

import (
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

// Quiet is the quiet-zone width in modules.
const Quiet = 2

func bitmap(text string) ([][]bool, error) {
	q, err := qrcode.New(text, qrcode.Low)
	if err != nil {
		return nil, err
	}
	q.DisableBorder = true
	bm := q.Bitmap()
	n := len(bm) + 2*Quiet
	out := make([][]bool, n)
	for y := range out {
		out[y] = make([]bool, n)
		for x := range out[y] {
			by, bx := y-Quiet, x-Quiet
			dark := by >= 0 && by < len(bm) && bx >= 0 && bx < len(bm) && bm[by][bx]
			out[y][x] = !dark // true = light = drawn
		}
	}
	return out, nil
}

// Render returns the QR code for text as lines of half-block glyphs.
func Render(text string) (string, error) {
	bm, err := bitmap(text)
	if err != nil {
		return "", err
	}
	n := len(bm)
	var lines []string
	for y := 0; y < n; y += 2 {
		var b strings.Builder
		for x := 0; x < n; x++ {
			top := bm[y][x]
			bot := y+1 < n && bm[y+1][x]
			switch {
			case top && bot:
				b.WriteString("█")
			case top:
				b.WriteString("▀")
			case bot:
				b.WriteString("▄")
			default:
				b.WriteByte(' ')
			}
		}
		lines = append(lines, b.String())
	}
	return strings.Join(lines, "\n"), nil
}

// Size returns the rendered width and height in cells.
func Size(text string) (w, h int, err error) {
	bm, err := bitmap(text)
	if err != nil {
		return 0, 0, err
	}
	return len(bm), (len(bm) + 1) / 2, nil
}

// Fits reports whether the rendered code fits in w x h cells.
func Fits(text string, w, h int) bool {
	cw, ch, err := Size(text)
	return err == nil && cw <= w && ch <= h
}
