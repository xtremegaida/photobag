package imaging

import (
	"image"
	"image/color"

	"golang.org/x/image/draw"
)

// matte is the grey that transparent pixels are composited onto.
const matte = 128

// Downscale returns an opaque RGBA copy of img whose long side is at most
// maxSide (never upscaled). Large reductions use an exact box (area) filter
// first, then Catmull-Rom for the fractional remainder, so the result does
// not depend on decoder-specific scaling.
func Downscale(img image.Image, maxSide int) *image.RGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	long := max(w, h)
	k := 1
	if long > maxSide {
		k = long / maxSide
	}
	box := boxDownsample(img, k)
	bw, bh := box.Rect.Dx(), box.Rect.Dy()
	if max(bw, bh) <= maxSide {
		return box
	}
	tw, th := fit(bw, bh, maxSide)
	dst := image.NewRGBA(image.Rect(0, 0, tw, th))
	draw.CatmullRom.Scale(dst, dst.Rect, box, box.Rect, draw.Src, nil)
	return dst
}

// Resize scales an opaque RGBA image to fit maxSide (never upscales).
func Resize(src *image.RGBA, maxSide int) *image.RGBA {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	if max(w, h) <= maxSide {
		return src
	}
	tw, th := fit(w, h, maxSide)
	dst := image.NewRGBA(image.Rect(0, 0, tw, th))
	draw.CatmullRom.Scale(dst, dst.Rect, src, src.Rect, draw.Src, nil)
	return dst
}

func fit(w, h, maxSide int) (int, int) {
	if w >= h {
		return maxSide, max(1, (h*maxSide+w/2)/w)
	}
	return max(1, (w*maxSide+h/2)/h), maxSide
}

// rowFunc writes opaque RGB triples for source row y into dst (len 3*width).
type rowFunc func(y int, dst []uint8)

func rowReader(img image.Image) rowFunc {
	b := img.Bounds()
	w := b.Dx()
	switch m := img.(type) {
	case *image.YCbCr:
		return func(y int, dst []uint8) {
			sy := b.Min.Y + y
			yi := m.YOffset(b.Min.X, sy)
			for x := 0; x < w; x++ {
				ci := m.COffset(b.Min.X+x, sy)
				r, g, bl := color.YCbCrToRGB(m.Y[yi+x], m.Cb[ci], m.Cr[ci])
				dst[3*x], dst[3*x+1], dst[3*x+2] = r, g, bl
			}
		}
	case *image.RGBA:
		return func(y int, dst []uint8) {
			p := m.Pix[m.PixOffset(b.Min.X, b.Min.Y+y):]
			for x := 0; x < w; x++ {
				a := uint32(p[4*x+3])
				bg := matte * (255 - a) / 255
				dst[3*x] = uint8(uint32(p[4*x]) + bg)
				dst[3*x+1] = uint8(uint32(p[4*x+1]) + bg)
				dst[3*x+2] = uint8(uint32(p[4*x+2]) + bg)
			}
		}
	case *image.NRGBA:
		return func(y int, dst []uint8) {
			p := m.Pix[m.PixOffset(b.Min.X, b.Min.Y+y):]
			for x := 0; x < w; x++ {
				a := uint32(p[4*x+3])
				dst[3*x] = uint8((uint32(p[4*x])*a + matte*(255-a)) / 255)
				dst[3*x+1] = uint8((uint32(p[4*x+1])*a + matte*(255-a)) / 255)
				dst[3*x+2] = uint8((uint32(p[4*x+2])*a + matte*(255-a)) / 255)
			}
		}
	case *image.Gray:
		return func(y int, dst []uint8) {
			p := m.Pix[m.PixOffset(b.Min.X, b.Min.Y+y):]
			for x := 0; x < w; x++ {
				dst[3*x], dst[3*x+1], dst[3*x+2] = p[x], p[x], p[x]
			}
		}
	case *image.Paletted:
		pal := make([][3]uint8, len(m.Palette))
		for i, c := range m.Palette {
			pal[i] = composite(c)
		}
		return func(y int, dst []uint8) {
			p := m.Pix[m.PixOffset(b.Min.X, b.Min.Y+y):]
			for x := 0; x < w; x++ {
				var c [3]uint8
				if int(p[x]) < len(pal) {
					c = pal[p[x]]
				}
				dst[3*x], dst[3*x+1], dst[3*x+2] = c[0], c[1], c[2]
			}
		}
	}
	return func(y int, dst []uint8) {
		for x := 0; x < w; x++ {
			c := composite(img.At(b.Min.X+x, b.Min.Y+y))
			dst[3*x], dst[3*x+1], dst[3*x+2] = c[0], c[1], c[2]
		}
	}
}

func composite(c color.Color) [3]uint8 {
	r, g, bl, a := c.RGBA() // premultiplied, 16-bit
	bg := uint32(matte) * 257 * (0xFFFF - a) / 0xFFFF
	return [3]uint8{uint8((r + bg) >> 8), uint8((g + bg) >> 8), uint8((bl + bg) >> 8)}
}

// boxDownsample averages k×k blocks (partial blocks at the edges are
// averaged over the pixels they contain) and composites alpha.
func boxDownsample(img image.Image, k int) *image.RGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	ow, oh := (w+k-1)/k, (h+k-1)/k
	dst := image.NewRGBA(image.Rect(0, 0, ow, oh))
	read := rowReader(img)
	row := make([]uint8, 3*w)
	if k == 1 {
		for y := 0; y < h; y++ {
			read(y, row)
			d := dst.Pix[y*dst.Stride:]
			for x := 0; x < w; x++ {
				d[4*x], d[4*x+1], d[4*x+2], d[4*x+3] = row[3*x], row[3*x+1], row[3*x+2], 255
			}
		}
		return dst
	}
	sums := make([]uint32, 3*ow)
	for oy := 0; oy < oh; oy++ {
		clear(sums)
		y0, y1 := oy*k, min((oy+1)*k, h)
		for y := y0; y < y1; y++ {
			read(y, row)
			for ox := 0; ox < ow; ox++ {
				var r, g, bl uint32
				for x, x1 := ox*k, min((ox+1)*k, w); x < x1; x++ {
					r += uint32(row[3*x])
					g += uint32(row[3*x+1])
					bl += uint32(row[3*x+2])
				}
				sums[3*ox] += r
				sums[3*ox+1] += g
				sums[3*ox+2] += bl
			}
		}
		rows := uint32(y1 - y0)
		d := dst.Pix[oy*dst.Stride:]
		for ox := 0; ox < ow; ox++ {
			cols := uint32(min((ox+1)*k, w) - ox*k)
			n := rows * cols
			d[4*ox] = uint8((sums[3*ox] + n/2) / n)
			d[4*ox+1] = uint8((sums[3*ox+1] + n/2) / n)
			d[4*ox+2] = uint8((sums[3*ox+2] + n/2) / n)
			d[4*ox+3] = 255
		}
	}
	return dst
}

// Orient applies an EXIF orientation (1..8) to src.
func Orient(src *image.RGBA, o int) *image.RGBA {
	if o <= 1 || o > 8 {
		return src
	}
	w, h := src.Rect.Dx(), src.Rect.Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		s := src.Pix[y*src.Stride:]
		for x := 0; x < w; x++ {
			var dx, dy int
			switch o {
			case 2: // mirror horizontal
				dx, dy = w-1-x, y
			case 3: // rotate 180
				dx, dy = w-1-x, h-1-y
			case 4: // mirror vertical
				dx, dy = x, h-1-y
			case 5: // transpose
				dx, dy = y, x
			case 6: // rotate 90 CW
				dx, dy = h-1-y, x
			case 7: // transverse
				dx, dy = h-1-y, w-1-x
			case 8: // rotate 90 CCW
				dx, dy = y, w-1-x
			}
			copy(dst.Pix[dy*dst.Stride+4*dx:dy*dst.Stride+4*dx+4], s[4*x:4*x+4])
		}
	}
	return dst
}

// OrientedSize returns the display size for raw dimensions and orientation.
func OrientedSize(w, h, o int) (int, int) {
	if o >= 5 && o <= 8 {
		return h, w
	}
	return w, h
}
