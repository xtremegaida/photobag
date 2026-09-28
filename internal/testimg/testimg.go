// Package testimg generates synthetic, photo-like test images and encodes
// them with optional EXIF metadata. It is used by tests and genfixtures.
package testimg

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
	"math/rand/v2"
)

// Scene draws a deterministic photo-like image: a two-colour gradient sky,
// a wavy horizon, several soft shapes and fine noise.
func Scene(seed uint64, w, h int) *image.RGBA {
	r := rand.New(rand.NewPCG(seed, seed^0x9E3779B97F4A7C15))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	top := randColor(r)
	bottom := randColor(r)
	horizon := 0.3 + 0.4*r.Float64()
	ground := randColor(r)
	phase := r.Float64() * 6
	type blob struct {
		cx, cy, rx, ry float64
		c              color.RGBA
	}
	blobs := make([]blob, 3+r.IntN(4))
	for i := range blobs {
		blobs[i] = blob{r.Float64(), r.Float64(), 0.05 + 0.2*r.Float64(), 0.05 + 0.2*r.Float64(), randColor(r)}
	}
	for y := 0; y < h; y++ {
		fy := float64(y) / float64(h)
		for x := 0; x < w; x++ {
			fx := float64(x) / float64(w)
			var c [3]float64
			if fy < horizon+0.05*math.Sin(fx*7+phase) {
				c = mix(top, bottom, fy/horizon)
			} else {
				c = mix(ground, bottom, 0.3*math.Sin(fx*20+phase)*math.Cos(fy*15))
			}
			for _, b := range blobs {
				dx, dy := (fx-b.cx)/b.rx, (fy-b.cy)/b.ry
				if d := dx*dx + dy*dy; d < 1 {
					c = mixf(c, b.c, 1-d*d)
				}
			}
			n := (r.Float64() - 0.5) * 8
			o := y*img.Stride + 4*x
			img.Pix[o] = clamp(c[0] + n)
			img.Pix[o+1] = clamp(c[1] + n)
			img.Pix[o+2] = clamp(c[2] + n)
			img.Pix[o+3] = 255
		}
	}
	return img
}

// Flat draws a nearly uniform image (blank sky, black frame) with a faint
// gradient and noise.
func Flat(seed uint64, w, h int, base color.RGBA) *image.RGBA {
	r := rand.New(rand.NewPCG(seed, 1))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		g := float64(y) / float64(h) * 3
		for x := 0; x < w; x++ {
			n := (r.Float64() - 0.5) * 3
			o := y*img.Stride + 4*x
			img.Pix[o] = clamp(float64(base.R) + g + n)
			img.Pix[o+1] = clamp(float64(base.G) + g + n)
			img.Pix[o+2] = clamp(float64(base.B) + g + n)
			img.Pix[o+3] = 255
		}
	}
	return img
}

func randColor(r *rand.Rand) color.RGBA {
	return color.RGBA{uint8(r.IntN(256)), uint8(r.IntN(256)), uint8(r.IntN(256)), 255}
}

func mix(a, b color.RGBA, t float64) [3]float64 {
	t = math.Max(0, math.Min(1, t))
	return [3]float64{
		float64(a.R)*(1-t) + float64(b.R)*t,
		float64(a.G)*(1-t) + float64(b.G)*t,
		float64(a.B)*(1-t) + float64(b.B)*t,
	}
}

func mixf(c [3]float64, b color.RGBA, t float64) [3]float64 {
	return [3]float64{c[0]*(1-t) + float64(b.R)*t, c[1]*(1-t) + float64(b.G)*t, c[2]*(1-t) + float64(b.B)*t}
}

func clamp(v float64) uint8 { return uint8(math.Max(0, math.Min(255, math.Round(v)))) }

// Brighten adds delta to every colour channel.
func Brighten(src *image.RGBA, delta int) *image.RGBA {
	dst := image.NewRGBA(src.Rect)
	for i := range src.Pix {
		if i%4 == 3 {
			dst.Pix[i] = src.Pix[i]
			continue
		}
		dst.Pix[i] = clamp(float64(int(src.Pix[i]) + delta))
	}
	return dst
}

// Resize scales with nearest-neighbour sampling (deliberately different
// from the pipeline's filter).
func Resize(src *image.RGBA, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			sx, sy := x*sw/w, y*sh/h
			copy(dst.Pix[y*dst.Stride+4*x:y*dst.Stride+4*x+4], src.Pix[sy*src.Stride+4*sx:])
		}
	}
	return dst
}

// Transform applies an EXIF orientation transform to src (same semantics
// as imaging.Orient), so tests can build a "stored" image whose displayed
// form is known: stored = Transform(display, Inverse(o)).
func Transform(src *image.RGBA, o int) *image.RGBA {
	if o <= 1 {
		return src
	}
	w, h := src.Rect.Dx(), src.Rect.Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var dx, dy int
			switch o {
			case 2:
				dx, dy = w-1-x, y
			case 3:
				dx, dy = w-1-x, h-1-y
			case 4:
				dx, dy = x, h-1-y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = h-1-y, x
			case 7:
				dx, dy = h-1-y, w-1-x
			case 8:
				dx, dy = y, w-1-x
			}
			copy(dst.Pix[dy*dst.Stride+4*dx:dy*dst.Stride+4*dx+4], src.Pix[y*src.Stride+4*x:])
		}
	}
	return dst
}

// Inverse returns the orientation whose transform undoes o.
func Inverse(o int) int {
	switch o {
	case 6:
		return 8
	case 8:
		return 6
	}
	return o
}

// EXIF describes metadata to embed.
type EXIF struct {
	Orientation int
	DateTime    string // "2006:01:02 15:04:05"
	Offset      string // "+02:00"
}

func (e EXIF) empty() bool { return e.Orientation <= 1 && e.DateTime == "" && e.Offset == "" }

type tiffEntry struct {
	tag, typ uint16
	count    uint32
	value    []byte
}

// TIFFBlock builds a little-endian TIFF/EXIF structure.
func (e EXIF) TIFFBlock() []byte {
	le := binary.LittleEndian
	ascii := func(tag uint16, s string) tiffEntry {
		b := append([]byte(s), 0)
		return tiffEntry{tag, 2, uint32(len(b)), b}
	}
	var ifd0, exif []tiffEntry
	if e.Orientation > 1 {
		ifd0 = append(ifd0, tiffEntry{0x0112, 3, 1, le.AppendUint16(nil, uint16(e.Orientation))})
	}
	if e.DateTime != "" {
		exif = append(exif, ascii(0x9003, e.DateTime))
	}
	if e.Offset != "" {
		exif = append(exif, ascii(0x9011, e.Offset))
	}
	n0 := len(ifd0)
	if len(exif) > 0 {
		n0++
	}
	ifd0Size := 2 + 12*n0 + 4
	exifSize := 0
	if len(exif) > 0 {
		exifSize = 2 + 12*len(exif) + 4
		ifd0 = append(ifd0, tiffEntry{0x8769, 4, 1, le.AppendUint32(nil, uint32(8+ifd0Size))})
	}
	dataOff := 8 + ifd0Size + exifSize
	var data []byte
	write := func(buf []byte, es []tiffEntry) []byte {
		buf = le.AppendUint16(buf, uint16(len(es)))
		for _, en := range es {
			buf = le.AppendUint16(buf, en.tag)
			buf = le.AppendUint16(buf, en.typ)
			buf = le.AppendUint32(buf, en.count)
			if len(en.value) <= 4 {
				v := make([]byte, 4)
				copy(v, en.value)
				buf = append(buf, v...)
			} else {
				buf = le.AppendUint32(buf, uint32(dataOff+len(data)))
				data = append(data, en.value...)
			}
		}
		return le.AppendUint32(buf, 0)
	}
	out := []byte("II*\x00")
	out = le.AppendUint32(out, 8)
	out = write(out, ifd0)
	if len(exif) > 0 {
		out = write(out, exif)
	}
	return append(out, data...)
}

// JPEG encodes img with an optional EXIF APP1 segment.
func JPEG(img image.Image, quality int, e EXIF) []byte {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		panic(err)
	}
	b := buf.Bytes()
	if e.empty() {
		return b
	}
	payload := append([]byte("Exif\x00\x00"), e.TIFFBlock()...)
	seg := binary.BigEndian.AppendUint16([]byte{0xFF, 0xE1}, uint16(len(payload)+2))
	seg = append(seg, payload...)
	out := append([]byte{}, b[:2]...)
	out = append(out, seg...)
	return append(out, b[2:]...)
}

// PNG encodes img with an optional eXIf chunk.
func PNG(img image.Image, e EXIF) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	b := buf.Bytes()
	if e.empty() {
		return b
	}
	// Insert after the signature (8 bytes) and IHDR chunk (25 bytes).
	data := e.TIFFBlock()
	chunk := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
	chunk = append(chunk, "eXIf"...)
	chunk = append(chunk, data...)
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(chunk[4:]))
	out := append([]byte{}, b[:33]...)
	out = append(out, chunk...)
	return append(out, b[33:]...)
}
