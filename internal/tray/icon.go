package tray

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

// icon renders a small solid-color square with a simplified "E" mark as the
// tray icon. Generated in code rather than shipped as a binary asset so the
// module has no external image dependency.
func icon() []byte {
	const size = 32
	img := image.NewRGBA(image.Rect(0, 0, size, size))

	bg := color.RGBA{R: 0x1f, G: 0x3a, B: 0x5f, A: 0xff}
	fg := color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.Set(x, y, bg)
		}
	}

	// A blocky "E" so the icon reads as something even at 16px.
	for y := 8; y < 24; y++ {
		img.Set(8, y, fg)
	}
	for x := 8; x < 24; x++ {
		img.Set(x, 8, fg)
		img.Set(x, 23, fg)
	}
	for x := 8; x < 20; x++ {
		img.Set(x, 15, fg)
		img.Set(x, 16, fg)
	}

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
