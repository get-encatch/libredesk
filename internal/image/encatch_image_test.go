package image

// encatch: tests for our guard in Downscale (only JPEG, PNG and GIF are decoded).

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func sample() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for x := 0; x < 40; x++ {
		img.Set(x, x%20, color.RGBA{R: 200, A: 255})
	}
	return img
}

// tinyTIFF returns a valid 1x1 uncompressed grayscale TIFF, built by hand so the test
// needs no extra module (imaging registers the TIFF decoder Downscale must not reach).
func tinyTIFF() []byte {
	type entry struct {
		tag, typ uint16
		val      uint32
	}
	const short, long = 3, 4
	entries := []entry{
		{256, short, 1}, {257, short, 1}, {258, short, 8}, {259, short, 1}, // width, height, bits, no compression
		{262, short, 1}, {273, long, 8 + 2 + 8*12 + 4}, {278, short, 1}, {279, long, 1}, // BlackIsZero, strip offset, rows, bytes
	}
	b := []byte("II*\x00")
	b = binary.LittleEndian.AppendUint32(b, 8)
	b = binary.LittleEndian.AppendUint16(b, uint16(len(entries)))
	for _, e := range entries {
		b = binary.LittleEndian.AppendUint16(b, e.tag)
		b = binary.LittleEndian.AppendUint16(b, e.typ)
		b = binary.LittleEndian.AppendUint32(b, 1)
		b = binary.LittleEndian.AppendUint32(b, e.val)
	}
	b = binary.LittleEndian.AppendUint32(b, 0) // no next IFD
	return append(b, 0x80)                     // the one pixel
}

func TestDownscaleFormats(t *testing.T) {
	encode := map[string]func(*bytes.Buffer) error{
		"jpeg": func(b *bytes.Buffer) error { return jpeg.Encode(b, sample(), nil) },
		"png":  func(b *bytes.Buffer) error { return png.Encode(b, sample()) },
		"gif":  func(b *bytes.Buffer) error { return gif.Encode(b, sample(), nil) },
		"tiff": func(b *bytes.Buffer) error { _, err := b.Write(tinyTIFF()); return err },
	}
	for format, enc := range encode {
		var b bytes.Buffer
		if err := enc(&b); err != nil {
			t.Fatalf("%s: encoding sample: %v", format, err)
		}
		out, err := Downscale(10, bytes.NewReader(b.Bytes()))
		switch format {
		case "jpeg", "png", "gif":
			if err != nil {
				t.Errorf("%s: %v", format, err)
				continue
			}
			cfg, got, err := image.DecodeConfig(out)
			if err != nil || got != format || cfg.Width != 10 || cfg.Height != 5 {
				t.Errorf("%s: re-encoded as %q %dx%d (%v)", format, got, cfg.Width, cfg.Height, err)
			}
		default:
			// A real TIFF that the registered decoders would read...
			if _, got, err := image.DecodeConfig(bytes.NewReader(b.Bytes())); err != nil || got != format {
				t.Fatalf("%s: sample isn't a valid %s (%q, %v)", format, format, got, err)
			}
			// ...is refused before decoding, whatever the file was called.
			if err == nil || !strings.Contains(err.Error(), "unsupported image format") {
				t.Errorf("%s: want unsupported format, got %v", format, err)
			}
		}
	}
}
