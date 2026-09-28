package imaging

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"

	"golang.org/x/image/bmp"
	"golang.org/x/image/tiff"
	"golang.org/x/image/webp"
)

// MaxPixels bounds the decoded size of a single image (about 1.6 GB RGBA).
const MaxPixels = 400_000_000

// ErrTooLarge is returned for images above MaxPixels.
var ErrTooLarge = errors.New("image dimensions are too large")

// DecodeConfig reads the image dimensions (before orientation) from r.
func DecodeConfig(f Format, r io.Reader) (w, h int, err error) {
	var c image.Config
	switch f {
	case JPEG:
		c, err = jpeg.DecodeConfig(r)
	case PNG:
		c, err = png.DecodeConfig(r)
	case GIF:
		c, err = gif.DecodeConfig(r)
	case WebP:
		c, err = webp.DecodeConfig(r)
	case BMP:
		c, err = bmp.DecodeConfig(r)
	case TIFF:
		c, err = tiff.DecodeConfig(r)
	default:
		return 0, 0, fmt.Errorf("unsupported format %q", f)
	}
	if err != nil {
		return 0, 0, fmt.Errorf("reading %s header: %w", f, err)
	}
	if c.Width <= 0 || c.Height <= 0 {
		return 0, 0, fmt.Errorf("%s has invalid dimensions %dx%d", f, c.Width, c.Height)
	}
	if int64(c.Width)*int64(c.Height) > MaxPixels {
		return 0, 0, fmt.Errorf("%w (%dx%d)", ErrTooLarge, c.Width, c.Height)
	}
	return c.Width, c.Height, nil
}

// Decode decodes the image (first frame for animations), without applying
// orientation.
func Decode(f Format, b []byte) (image.Image, error) {
	if _, _, err := DecodeConfig(f, bytes.NewReader(b)); err != nil {
		return nil, err
	}
	r := bytes.NewReader(b)
	var (
		img image.Image
		err error
	)
	switch f {
	case JPEG:
		img, err = jpeg.Decode(r)
	case PNG:
		img, err = png.Decode(r)
	case GIF:
		img, err = gif.Decode(r)
	case WebP:
		img, err = webp.Decode(r)
		if err != nil {
			if frame := webpFirstFrame(b); frame != nil {
				img, err = webp.Decode(bytes.NewReader(frame))
			}
		}
	case BMP:
		img, err = bmp.Decode(r)
	case TIFF:
		img, err = tiff.Decode(r)
	default:
		return nil, fmt.Errorf("unsupported format %q", f)
	}
	if err != nil {
		return nil, fmt.Errorf("decoding %s: %w", f, err)
	}
	return img, nil
}

// webpFirstFrame rebuilds the first frame of an animated WebP as a still
// WebP file, since x/image/webp does not decode animations. It returns nil
// if b is not an animated WebP.
func webpFirstFrame(b []byte) []byte {
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WEBP" {
		return nil
	}
	for i := 12; i+8 <= len(b); {
		typ := string(b[i : i+4])
		n := int(binary.LittleEndian.Uint32(b[i+4:]))
		if n < 0 || i+8+n > len(b) {
			return nil
		}
		if typ == "ANMF" && n >= 16 {
			frame := b[i+8 : i+8+n]
			fw := int(frame[6]) | int(frame[7])<<8 | int(frame[8])<<16
			fh := int(frame[9]) | int(frame[10])<<8 | int(frame[11])<<16
			var alph, bits []byte
			var bitsType string
			for j := 16; j+8 <= len(frame); {
				st := string(frame[j : j+4])
				sn := int(binary.LittleEndian.Uint32(frame[j+4:]))
				if sn < 0 || j+8+sn > len(frame) {
					return nil
				}
				chunk := frame[j : j+8+sn+sn&1]
				if j+8+sn+sn&1 > len(frame) {
					chunk = frame[j : j+8+sn]
				}
				switch st {
				case "ALPH":
					alph = chunk
				case "VP8 ", "VP8L":
					bits, bitsType = chunk, st
				}
				j += 8 + sn + sn&1
			}
			if bits == nil {
				return nil
			}
			var body bytes.Buffer
			body.WriteString("WEBP")
			if bitsType == "VP8 " && alph != nil {
				vp8x := make([]byte, 18)
				copy(vp8x, "VP8X")
				binary.LittleEndian.PutUint32(vp8x[4:], 10)
				vp8x[8] = 1 << 4 // alpha
				put24(vp8x[12:], fw)
				put24(vp8x[15:], fh)
				body.Write(vp8x)
				body.Write(alph)
			}
			body.Write(bits)
			if body.Len()%2 == 1 {
				body.WriteByte(0)
			}
			out := make([]byte, 8, 8+body.Len())
			copy(out, "RIFF")
			binary.LittleEndian.PutUint32(out[4:], uint32(body.Len()))
			return append(out, body.Bytes()...)
		}
		i += 8 + n + n&1
	}
	return nil
}

func put24(b []byte, v int) {
	v--
	b[0], b[1], b[2] = byte(v), byte(v>>8), byte(v>>16)
}
