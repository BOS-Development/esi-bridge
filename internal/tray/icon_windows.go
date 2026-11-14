package tray

import (
	"bytes"
	"encoding/binary"
	"image/png"
)

// icon returns the tray icon as .ico bytes. fyne.io/systray on Windows loads
// the icon via the Win32 LoadImage API with IMAGE_ICON, which requires a
// real .ico file — handing it a bare PNG fails silently (LoadImage returns a
// null handle) and the tray icon never appears. The .ico container wraps a
// single PNG-encoded frame, which Windows Vista+ decodes natively.
func icon() []byte {
	img := iconImage()

	var pngBuf bytes.Buffer
	_ = png.Encode(&pngBuf, img)
	pngBytes := pngBuf.Bytes()

	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	// ICONDIRENTRY encodes 256 as 0 in the single-byte width/height fields.
	widthByte, heightByte := byte(width), byte(height)
	if width >= 256 {
		widthByte = 0
	}
	if height >= 256 {
		heightByte = 0
	}

	var buf bytes.Buffer
	// ICONDIR
	binary.Write(&buf, binary.LittleEndian, uint16(0)) // reserved
	binary.Write(&buf, binary.LittleEndian, uint16(1)) // type: icon
	binary.Write(&buf, binary.LittleEndian, uint16(1)) // image count

	// ICONDIRENTRY
	buf.WriteByte(widthByte)
	buf.WriteByte(heightByte)
	buf.WriteByte(0)                                               // color palette size
	buf.WriteByte(0)                                               // reserved
	binary.Write(&buf, binary.LittleEndian, uint16(1))             // color planes
	binary.Write(&buf, binary.LittleEndian, uint16(32))            // bits per pixel
	binary.Write(&buf, binary.LittleEndian, uint32(len(pngBytes))) // image data size
	binary.Write(&buf, binary.LittleEndian, uint32(22))            // offset: 6 (ICONDIR) + 16 (ICONDIRENTRY)

	buf.Write(pngBytes)

	return buf.Bytes()
}
