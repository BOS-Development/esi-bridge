package tray

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
)

// icon renders a small solid-color square with a simplified "E" mark as the
// tray icon. Generated in code rather than shipped as a binary asset so the
// module has no external image dependency.
//
// Returned as a .ico container wrapping the PNG, not a bare PNG: systray's
// Windows backend loads icon bytes via Win32 LoadImage(IMAGE_ICON, ...),
// which requires a real ICONDIR header and fails to parse raw PNG bytes.
// That failure surfaces as a misleading "systray error: unable to set icon:
// The operation completed successfully" (a stale GetLastError(), not an
// actual success) and the tray icon never appears. The Vista+ ICO format
// embedding a PNG image works on Windows and is accepted as-is on macOS and
// Linux, so one encoding covers every platform.
func icon() []byte {
	return pngToICO(iconPNG())
}

func iconPNG() []byte {
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

// pngToICO wraps a single 32x32 PNG image in a minimal single-entry .ico
// container (ICONDIR + ICONDIRENTRY, PNG-compressed per the Vista+ icon
// format). bytes.Buffer.Write never errors, so binary.Write's errors are
// ignored here.
func pngToICO(pngData []byte) []byte {
	const size = 32 // must match iconPNG's dimensions; 0 would mean 256px

	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint16(0)) // reserved
	binary.Write(&buf, binary.LittleEndian, uint16(1)) // type: icon
	binary.Write(&buf, binary.LittleEndian, uint16(1)) // image count

	buf.WriteByte(size)                                           // width
	buf.WriteByte(size)                                           // height
	buf.WriteByte(0)                                              // color count (>=256 colors)
	buf.WriteByte(0)                                              // reserved
	binary.Write(&buf, binary.LittleEndian, uint16(1))            // color planes
	binary.Write(&buf, binary.LittleEndian, uint16(32))           // bits per pixel
	binary.Write(&buf, binary.LittleEndian, uint32(len(pngData))) // image data size
	binary.Write(&buf, binary.LittleEndian, uint32(22))           // image data offset (6-byte ICONDIR + 16-byte ICONDIRENTRY)

	buf.Write(pngData)
	return buf.Bytes()
}
