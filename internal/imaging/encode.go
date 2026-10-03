package imaging

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	stddraw "image/draw"
	"image/png"
	"math"

	"github.com/gen2brain/jpegn"
	"github.com/gen2brain/vpx/webp"
	"golang.org/x/image/draw"
)

// EncodeOptions describe a re-encode.
type EncodeOptions struct {
	// Format is JPEG, PNG or WebP.
	Format Format
	// Lossless selects lossless WebP (PNG always is lossless, JPEG never).
	Lossless bool
	// Quality (1..100) applies to JPEG and lossy WebP.
	Quality int
	// Effort trades time for size: the WebP method (0..6), or for PNG 0
	// (fast), 1 (default) or 2 (smallest).
	Effort int
	// Progressive writes a progressive JPEG.
	Progressive bool
	// Chroma444 keeps JPEG colour at full resolution (default: 4:2:0).
	Chroma444 bool
	// MaxWidth and MaxHeight scale larger images down to fit, keeping their
	// proportions (0: no limit).
	MaxWidth, MaxHeight int
	// KeepMetadata carries EXIF and XMP over. The colour profile is always
	// carried over, since the pixel values need it to mean the same colours.
	KeepMetadata bool
}

// Reencoded is the result of a re-encode.
type Reencoded struct {
	Data   []byte
	Format Format
	// Width and Height are the new dimensions (upright).
	Width, Height int
	Scaled        bool
	// Exact is set when the new file decodes to exactly the pixels that
	// were encoded (lossless formats always do, or the re-encode fails).
	Exact bool
	// PSNR compares the decoded result with the pixels encoded, in dB
	// (+Inf when exact).
	PSNR float64
	// Notes explain anything changed besides the encoding.
	Notes []string
	// Result holds the thumbnail and fingerprint of the new file.
	Result *Result
}

// maxSide is the largest width or height each format can hold.
var maxSide = map[Format]int{JPEG: 65535, PNG: 1 << 30, WebP: 16383}

// Reasons an image is not re-encoded.
var (
	ErrAnimated = errors.New("animated: re-encoding would keep only the first frame")
	ErrCMYK     = errors.New("CMYK colour: re-encoding would change its colours")
)

// Animated reports whether a GIF, PNG or WebP holds more than one frame.
func Animated(f Format, b []byte) bool {
	switch f {
	case GIF:
		return gifFrames(b) > 1
	case PNG:
		for i := 8; i+12 <= len(b); {
			n := int(binary.BigEndian.Uint32(b[i:]))
			typ := string(b[i+4 : i+8])
			if typ == "acTL" {
				return true
			}
			if typ == "IDAT" || n < 0 {
				return false
			}
			i += 12 + n
		}
	case WebP:
		for i := 12; i+8 <= len(b); {
			typ := string(b[i : i+4])
			n := int(binary.LittleEndian.Uint32(b[i+4:]))
			if typ == "ANIM" || typ == "ANMF" || (typ == "VP8X" && n > 0 && i+8 < len(b) && b[i+8]&0x02 != 0) {
				return true
			}
			if n < 0 {
				return false
			}
			i += 8 + n + n&1
		}
	}
	return false
}

// gifFrames counts the image descriptors of a GIF without decoding it.
func gifFrames(b []byte) int {
	if len(b) < 13 {
		return 0
	}
	i := 13
	if b[10]&0x80 != 0 {
		i += 3 << (int(b[10]&7) + 1)
	}
	skipBlocks := func() bool {
		for i < len(b) {
			n := int(b[i])
			i++
			if n == 0 {
				return true
			}
			i += n
		}
		return false
	}
	frames := 0
	for i < len(b) {
		switch b[i] {
		case 0x2C: // image descriptor
			if i+10 > len(b) {
				return frames
			}
			frames++
			if frames > 1 {
				return frames
			}
			flags := b[i+9]
			i += 10
			if flags&0x80 != 0 {
				i += 3 << (int(flags&7) + 1)
			}
			i++ // LZW minimum code size
			if !skipBlocks() {
				return frames
			}
		case 0x21: // extension
			i += 2
			if !skipBlocks() {
				return frames
			}
		default: // trailer or garbage
			return frames
		}
	}
	return frames
}

// fitWithin scales w×h down to fit maxW×maxH (0: unbounded), keeping the
// proportions; it never scales up.
func fitWithin(w, h, maxW, maxH int) (int, int, bool) {
	s := 1.0
	if maxW > 0 && w > maxW {
		s = float64(maxW) / float64(w)
	}
	if maxH > 0 && h > maxH {
		s = min(s, float64(maxH)/float64(h))
	}
	if s >= 1 {
		return w, h, false
	}
	return max(1, int(math.Round(float64(w)*s))), max(1, int(math.Round(float64(h)*s))), true
}

func isOpaque(img image.Image) bool {
	if o, ok := img.(interface{ Opaque() bool }); ok {
		return o.Opaque()
	}
	return false
}

// toRGBA converts to premultiplied 8-bit RGBA (fast for common types).
func toRGBA(img image.Image) *image.RGBA {
	if m, ok := img.(*image.RGBA); ok && m.Rect.Min == (image.Point{}) {
		return m
	}
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	stddraw.Draw(dst, dst.Rect, img, b.Min, stddraw.Src)
	return dst
}

// toNRGBA converts to non-premultiplied 8-bit RGBA, keeping exact values
// for the types lossless sources decode to.
func toNRGBA(img image.Image) *image.NRGBA {
	b := img.Bounds()
	if m, ok := img.(*image.NRGBA); ok && b.Min == (image.Point{}) {
		return m
	}
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	switch m := img.(type) {
	case *image.Paletted:
		pal := make([]color.NRGBA, len(m.Palette))
		for i, c := range m.Palette {
			pal[i] = color.NRGBAModel.Convert(c).(color.NRGBA)
		}
		for y := 0; y < b.Dy(); y++ {
			src := m.Pix[m.PixOffset(b.Min.X, b.Min.Y+y):]
			d := dst.Pix[y*dst.Stride:]
			for x := 0; x < b.Dx(); x++ {
				var c color.NRGBA
				if int(src[x]) < len(pal) {
					c = pal[src[x]]
				}
				d[4*x], d[4*x+1], d[4*x+2], d[4*x+3] = c.R, c.G, c.B, c.A
			}
		}
	default:
		for y := 0; y < b.Dy(); y++ {
			d := dst.Pix[y*dst.Stride:]
			for x := 0; x < b.Dx(); x++ {
				c := color.NRGBAModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
				d[4*x], d[4*x+1], d[4*x+2], d[4*x+3] = c.R, c.G, c.B, c.A
			}
		}
	}
	return dst
}

func toGray(img image.Image) *image.Gray {
	b := img.Bounds()
	if m, ok := img.(*image.Gray); ok && b.Min == (image.Point{}) {
		return m
	}
	dst := image.NewGray(image.Rect(0, 0, b.Dx(), b.Dy()))
	stddraw.Draw(dst, dst.Rect, img, b.Min, stddraw.Src)
	return dst
}

// flatten composites onto white (JPEG has no transparency).
func flatten(img image.Image) *image.RGBA {
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	stddraw.Draw(dst, dst.Rect, image.White, image.Point{}, stddraw.Src)
	stddraw.Draw(dst, dst.Rect, img, b.Min, stddraw.Over)
	return dst
}

// pixels exposes an image's pixel buffer for orienting it.
func pixels(img image.Image) (pix []uint8, stride, bpp int, make func(r image.Rectangle) image.Image, ok bool) {
	switch m := img.(type) {
	case *image.RGBA:
		return m.Pix, m.Stride, 4, func(r image.Rectangle) image.Image { return image.NewRGBA(r) }, true
	case *image.NRGBA:
		return m.Pix, m.Stride, 4, func(r image.Rectangle) image.Image { return image.NewNRGBA(r) }, true
	case *image.Gray:
		return m.Pix, m.Stride, 1, func(r image.Rectangle) image.Image { return image.NewGray(r) }, true
	case *image.Gray16:
		return m.Pix, m.Stride, 2, func(r image.Rectangle) image.Image { return image.NewGray16(r) }, true
	case *image.NRGBA64:
		return m.Pix, m.Stride, 8, func(r image.Rectangle) image.Image { return image.NewNRGBA64(r) }, true
	case *image.RGBA64:
		return m.Pix, m.Stride, 8, func(r image.Rectangle) image.Image { return image.NewRGBA64(r) }, true
	}
	return nil, 0, 0, nil, false
}

// orientAny applies an EXIF orientation to an image of one of the types
// pixels knows (others are converted to RGBA first).
func orientAny(img image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return img
	}
	pix, stride, bpp, mk, ok := pixels(img)
	if !ok || img.Bounds().Min != (image.Point{}) {
		img = toRGBA(img)
		pix, stride, bpp, mk, _ = pixels(img)
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	dw, dh := OrientedSize(w, h, o)
	dst := mk(image.Rect(0, 0, dw, dh))
	dpix, dstride, _, _, _ := pixels(dst)
	for y := 0; y < h; y++ {
		row := pix[y*stride:]
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
			copy(dpix[dy*dstride+bpp*dx:dy*dstride+bpp*dx+bpp], row[bpp*x:bpp*x+bpp])
		}
	}
	return dst
}

// scaleDown resamples to w×h with Catmull-Rom in premultiplied RGBA.
func scaleDown(img image.Image, w, h int) *image.RGBA {
	src := toRGBA(img)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Rect, src, src.Rect, draw.Src, nil)
	return dst
}

func deep(img image.Image) bool {
	switch img.(type) {
	case *image.Gray16, *image.NRGBA64, *image.RGBA64:
		return true
	}
	return false
}

func gray(img image.Image) bool {
	switch img.(type) {
	case *image.Gray, *image.Gray16:
		return true
	}
	return false
}

// Reencode decodes an image and encodes it again as o describes: turned
// upright, scaled down if asked, and checked by decoding the result.
func Reencode(f Format, src []byte, o EncodeOptions) (*Reencoded, error) {
	switch o.Format {
	case JPEG, PNG, WebP:
	default:
		return nil, fmt.Errorf("cannot encode %q", o.Format)
	}
	if Animated(f, src) {
		return nil, ErrAnimated
	}
	img, err := Decode(f, src)
	if err != nil {
		return nil, err
	}
	if _, ok := img.(*image.CMYK); ok {
		return nil, ErrCMYK
	}
	meta := ReadMeta(f, src)
	orient := meta.Orientation
	out := &Reencoded{Format: o.Format}
	note := func(s string) { out.Notes = append(out.Notes, s) }

	// Scale (in the stored orientation, before turning upright: less to
	// turn), then turn upright.
	b := img.Bounds()
	uw, uh := OrientedSize(b.Dx(), b.Dy(), orient)
	tw, th, scale := fitWithin(uw, uh, o.MaxWidth, o.MaxHeight)
	alpha := !isOpaque(img)
	isGray, isDeep := gray(img), deep(img)
	var pix image.Image
	switch {
	case scale:
		sw, sh := OrientedSize(tw, th, orient) // back to stored orientation
		scaled := scaleDown(img, sw, sh)
		out.Scaled = true
		if isDeep {
			note("16 bits per channel became 8 when scaling")
			isDeep = false
		}
		pix = scaled
		if isGray && o.Format != WebP {
			pix = toGray(scaled)
		}
	case o.Format == PNG && isDeep:
		if !isGray {
			img = toNRGBA64(img)
		}
		pix = img
	case o.Format == PNG && orient <= 1:
		if _, ok := img.(*image.Paletted); ok {
			pix = img // stays a palette image
			break
		}
		fallthrough
	default:
		switch {
		case isGray && o.Format != WebP:
			pix = toGray(img)
		case alpha && o.Format != JPEG:
			pix = toNRGBA(img)
		default:
			pix = toRGBA(img)
		}
		if isDeep {
			note("16 bits per channel became 8")
		}
	}
	if o.Format == JPEG && alpha {
		pix = flatten(pix)
		note("transparent areas were filled with white")
	}
	pix = orientAny(pix, orient)
	out.Width, out.Height = pix.Bounds().Dx(), pix.Bounds().Dy()
	if limit := maxSide[o.Format]; out.Width > limit || out.Height > limit {
		return nil, fmt.Errorf("%s holds at most %d pixels on a side; scale the image down", o.Format, limit)
	}

	// Metadata: the profile only if it suits the colours written.
	md := ReadMetadata(f, src)
	if md.ICC != nil {
		want := "RGB "
		if gray(pix) {
			want = "GRAY"
		}
		if iccSpace(md.ICC) != want {
			md.ICC = nil
			note("the colour profile did not fit the new colours and was left out")
		}
	}
	if !o.KeepMetadata || f == TIFF {
		md.EXIF, md.XMP = nil, nil
	} else if md.EXIF != nil {
		md.EXIF = uprightExif(md.EXIF, orient > 1)
	}

	var buf bytes.Buffer
	switch o.Format {
	case PNG:
		levels := []png.CompressionLevel{png.BestSpeed, png.DefaultCompression, png.BestCompression}
		enc := png.Encoder{CompressionLevel: levels[min(max(o.Effort, 0), 2)]}
		if err := enc.Encode(&buf, pix); err != nil {
			return nil, err
		}
		out.Data = pngWithMetadata(buf.Bytes(), md)
	case JPEG:
		exif, segs, err := jpegMetadata(md)
		if errors.Is(err, errNoRoom) {
			note("metadata too large for JPEG was left out")
			icc := md.ICC
			exif, segs, _ = jpegMetadata(Metadata{ICC: icc})
		}
		sub := jpegn.Subsample420
		if o.Chroma444 {
			sub = jpegn.Subsample444
		}
		if gray(pix) {
			sub = jpegn.SubsampleGray
		}
		if err := jpegn.Encode(&buf, pix, &jpegn.EncodeOptions{
			Quality: min(max(o.Quality, 1), 100), Subsampling: sub, OptimizeCoding: true,
			Progressive: o.Progressive, AdaptiveQuantization: true, Exif: exif, Segments: segs,
		}); err != nil {
			return nil, err
		}
		out.Data = buf.Bytes()
	case WebP:
		if err := webp.Encode(&buf, pix, webp.EncodeOptions{
			Quality: min(max(o.Quality, 1), 100), Lossless: o.Lossless, Method: min(max(o.Effort, 0), 6),
		}); err != nil {
			return nil, err
		}
		if out.Data, err = webpWithMetadata(buf.Bytes(), md, out.Width, out.Height); err != nil {
			return nil, err
		}
	}

	// Check the result by decoding it again.
	got, err := Decode(o.Format, out.Data)
	if err != nil {
		return nil, fmt.Errorf("the new file does not decode: %w", err)
	}
	if got.Bounds().Dx() != out.Width || got.Bounds().Dy() != out.Height {
		return nil, fmt.Errorf("the new file decodes at %v, not %dx%d", got.Bounds().Size(), out.Width, out.Height)
	}
	lossless := o.Format == PNG || (o.Format == WebP && o.Lossless)
	maxDiff, psnr := compare(pix, got)
	out.PSNR = psnr
	out.Exact = maxDiff == 0
	// Premultiplying rounds semi-transparent pixels, so they may differ by 1.
	if lossless && (maxDiff > 1 || maxDiff == 1 && !alpha) {
		return nil, fmt.Errorf("lossless encoding changed pixel values (by up to %d)", maxDiff)
	}
	newMeta := ReadMeta(o.Format, out.Data)
	if out.Result, err = derive(o.Format, got, out.Width, out.Height, newMeta); err != nil {
		return nil, err
	}
	return out, nil
}

func toNRGBA64(img image.Image) *image.NRGBA64 {
	b := img.Bounds()
	if m, ok := img.(*image.NRGBA64); ok && b.Min == (image.Point{}) {
		return m
	}
	dst := image.NewNRGBA64(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			dst.Set(x, y, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

// premulRow writes row y of img as premultiplied 8-bit RGBA into dst.
func premulRow(img image.Image) func(y int, dst []uint8) {
	b := img.Bounds()
	w := b.Dx()
	switch m := img.(type) {
	case *image.RGBA:
		return func(y int, dst []uint8) { copy(dst, m.Pix[m.PixOffset(b.Min.X, b.Min.Y+y):][:4*w]) }
	case *image.NRGBA:
		return func(y int, dst []uint8) {
			p := m.Pix[m.PixOffset(b.Min.X, b.Min.Y+y):]
			for x := 0; x < w; x++ {
				a := uint32(p[4*x+3])
				dst[4*x] = uint8((uint32(p[4*x])*a + 127) / 255)
				dst[4*x+1] = uint8((uint32(p[4*x+1])*a + 127) / 255)
				dst[4*x+2] = uint8((uint32(p[4*x+2])*a + 127) / 255)
				dst[4*x+3] = uint8(a)
			}
		}
	case *image.YCbCr:
		return func(y int, dst []uint8) {
			yi := m.YOffset(b.Min.X, b.Min.Y+y)
			for x := 0; x < w; x++ {
				ci := m.COffset(b.Min.X+x, b.Min.Y+y)
				r, g, bl := color.YCbCrToRGB(m.Y[yi+x], m.Cb[ci], m.Cr[ci])
				dst[4*x], dst[4*x+1], dst[4*x+2], dst[4*x+3] = r, g, bl, 255
			}
		}
	case *image.Gray:
		return func(y int, dst []uint8) {
			p := m.Pix[m.PixOffset(b.Min.X, b.Min.Y+y):]
			for x := 0; x < w; x++ {
				dst[4*x], dst[4*x+1], dst[4*x+2], dst[4*x+3] = p[x], p[x], p[x], 255
			}
		}
	}
	return func(y int, dst []uint8) {
		for x := 0; x < w; x++ {
			r, g, bl, a := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			dst[4*x], dst[4*x+1], dst[4*x+2], dst[4*x+3] = uint8(r>>8), uint8(g>>8), uint8(bl>>8), uint8(a>>8)
		}
	}
}

// compare returns the largest difference of any premultiplied 8-bit
// channel and the PSNR over the colour channels (+Inf when equal).
func compare(a, b image.Image) (maxDiff int, psnr float64) {
	w, h := a.Bounds().Dx(), a.Bounds().Dy()
	ra, rb := premulRow(a), premulRow(b)
	rowA, rowB := make([]uint8, 4*w), make([]uint8, 4*w)
	var sum float64
	for y := 0; y < h; y++ {
		ra(y, rowA)
		rb(y, rowB)
		for i := 0; i < 4*w; i++ {
			d := int(rowA[i]) - int(rowB[i])
			if d < 0 {
				d = -d
			}
			maxDiff = max(maxDiff, d)
			if i%4 != 3 {
				sum += float64(d * d)
			}
		}
	}
	if sum == 0 {
		return maxDiff, math.Inf(1)
	}
	mse := sum / float64(3*w*h)
	return maxDiff, 10 * math.Log10(255*255/mse)
}
