package imaging

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"image/png"
	"math"
	"slices"
	"testing"

	"photobag/internal/testimg"
)

// fakeICC is enough of a profile for the colour-space check.
func fakeICC(space string) []byte {
	p := make([]byte, 300)
	binary.BigEndian.PutUint32(p, 300)
	copy(p[16:], space)
	copy(p[36:], "acsp")
	for i := 128; i < len(p); i++ {
		p[i] = byte(i)
	}
	return p
}

// withJPEGICC inserts a profile into a JPEG as one APP2 segment.
func withJPEGICC(b, icc []byte) []byte {
	payload := append(append(bytes.Clone(iccHeader), 1, 1), icc...)
	seg := binary.BigEndian.AppendUint16([]byte{0xFF, 0xE2}, uint16(len(payload)+2))
	out := append(bytes.Clone(b[:2]), append(seg, payload...)...)
	return append(out, b[2:]...)
}

func reencode(t *testing.T, f Format, src []byte, o EncodeOptions) *Reencoded {
	t.Helper()
	r, err := Reencode(f, src, o)
	if err != nil {
		t.Fatalf("%s → %s: %v", f, o.Format, err)
	}
	if got, _ := Sniff(r.Data[:min(len(r.Data), SniffLen)], "x"); got != o.Format {
		t.Fatalf("wrote %q, want %s", got, o.Format)
	}
	return r
}

func sameNRGBA(t *testing.T, want image.Image, got image.Image) {
	t.Helper()
	a, b := toNRGBA(want), toNRGBA(got)
	if a.Rect.Size() != b.Rect.Size() {
		t.Fatalf("size %v, want %v", b.Rect.Size(), a.Rect.Size())
	}
	if !bytes.Equal(a.Pix, b.Pix) {
		t.Fatal("pixels differ")
	}
}

func TestReencodeLossless(t *testing.T) {
	// A 64×48 picture stored turned (EXIF orientation 6) comes out upright.
	scene := testimg.Scene(3, 64, 48)
	e := testimg.EXIF{Orientation: 6, DateTime: "2019:07:04 15:22:10"}
	src := testimg.PNG(testimg.Transform(scene, testimg.Inverse(6)), e)
	if m := ReadMeta(PNG, src); m.Orientation != 6 {
		t.Fatalf("fixture orientation %d", m.Orientation)
	}
	for _, o := range []EncodeOptions{
		{Format: WebP, Lossless: true, Effort: 2, KeepMetadata: true},
		{Format: PNG, Effort: 1, KeepMetadata: true},
	} {
		r := reencode(t, PNG, src, o)
		if !r.Exact || !math.IsInf(r.PSNR, 1) || r.Width != 64 || r.Height != 48 || r.Scaled {
			t.Fatalf("%s: %+v", o.Format, r)
		}
		img, err := Decode(o.Format, r.Data)
		if err != nil {
			t.Fatal(err)
		}
		sameNRGBA(t, scene, img)
		m := ReadMeta(o.Format, r.Data)
		if m.Orientation != 1 || m.TakenAt != "2019-07-04T15:22:10" {
			t.Errorf("%s metadata %+v", o.Format, m)
		}
		if r.Result.Width != 64 || r.Result.Height != 48 || len(r.Result.Thumb) == 0 {
			t.Errorf("%s derived %+v", o.Format, r.Result)
		}
	}
	// Without metadata, EXIF goes (the date stays in the library).
	r := reencode(t, PNG, src, EncodeOptions{Format: PNG})
	if m := ReadMetadata(PNG, r.Data); m.EXIF != nil {
		t.Error("EXIF kept")
	}

	// Transparency survives lossless WebP and PNG exactly.
	alpha := testimg.Fixture("alpha_lossless.webp")
	orig, _ := Decode(WebP, alpha)
	for _, f := range []Format{PNG, WebP} {
		r := reencode(t, WebP, alpha, EncodeOptions{Format: f, Lossless: true})
		img, _ := Decode(f, r.Data)
		if !r.Exact || isOpaque(img) {
			t.Errorf("%s: exact %v, opaque %v", f, r.Exact, isOpaque(img))
		}
		sameNRGBA(t, orig, img)
	}

	// 16-bit PNGs stay 16-bit as PNG; WebP holds 8.
	deepImg := image.NewNRGBA64(image.Rect(0, 0, 20, 10))
	for i := range deepImg.Pix {
		deepImg.Pix[i] = byte(i * 7)
	}
	for i := 6; i < len(deepImg.Pix); i += 8 {
		deepImg.Pix[i], deepImg.Pix[i+1] = 0xff, 0xff // opaque
	}
	var buf bytes.Buffer
	png.Encode(&buf, deepImg)
	r = reencode(t, PNG, buf.Bytes(), EncodeOptions{Format: PNG, Effort: 2})
	if img, _ := Decode(PNG, r.Data); !deep(img) || len(r.Notes) != 0 {
		t.Errorf("16-bit PNG became %T, notes %v", img, r.Notes)
	}
	r = reencode(t, PNG, buf.Bytes(), EncodeOptions{Format: WebP, Lossless: true})
	if len(r.Notes) != 1 {
		t.Errorf("16 → 8 bits not noted: %v", r.Notes)
	}

	// Palette images stay palette images.
	pal := image.NewPaletted(image.Rect(0, 0, 30, 20), palette.Plan9)
	for i := range pal.Pix {
		pal.Pix[i] = uint8(i % 200)
	}
	buf.Reset()
	png.Encode(&buf, pal)
	r = reencode(t, PNG, buf.Bytes(), EncodeOptions{Format: PNG})
	if img, _ := Decode(PNG, r.Data); !r.Exact {
		t.Errorf("palette PNG: %T exact %v", img, r.Exact)
	} else if _, ok := img.(*image.Paletted); !ok {
		t.Errorf("palette PNG became %T", img)
	}
}

func TestReencodeLossy(t *testing.T) {
	scene := testimg.Scene(4, 300, 200)
	icc := fakeICC("RGB ")
	src := withJPEGICC(testimg.JPEG(scene, 95, testimg.EXIF{DateTime: "2020:01:02 03:04:05"}), icc)
	if !bytes.Equal(ReadMetadata(JPEG, src).ICC, icc) {
		t.Fatal("fixture profile not read")
	}
	for _, o := range []EncodeOptions{
		{Format: JPEG, Quality: 80, Progressive: true, MaxWidth: 150, KeepMetadata: true},
		{Format: WebP, Quality: 80, Effort: 4, MaxHeight: 50, KeepMetadata: true},
		{Format: JPEG, Quality: 90, Chroma444: true, KeepMetadata: true},
	} {
		r := reencode(t, JPEG, src, o)
		w, h := 300, 200
		if o.MaxWidth > 0 || o.MaxHeight > 0 {
			w, h, _ = fitWithin(300, 200, o.MaxWidth, o.MaxHeight)
		}
		if r.Width != w || r.Height != h || r.Scaled != (w != 300) {
			t.Errorf("%+v: %dx%d scaled %v", o, r.Width, r.Height, r.Scaled)
		}
		if r.Exact || !(r.PSNR > 25 && r.PSNR < 80) {
			t.Errorf("%+v: PSNR %.1f", o, r.PSNR)
		}
		md := ReadMetadata(o.Format, r.Data)
		if !bytes.Equal(md.ICC, icc) || md.EXIF == nil {
			t.Errorf("%s lost metadata: icc %d bytes, exif %d bytes", o.Format, len(md.ICC), len(md.EXIF))
		}
		if m := ReadMeta(o.Format, r.Data); m.TakenAt != "2020-01-02T03:04:05" {
			t.Errorf("%s date %q", o.Format, m.TakenAt)
		}
	}
	// A profile that does not suit the output's colours is left out.
	graySrc := withJPEGICC(testimg.JPEG(scene, 90, testimg.EXIF{}), fakeICC("GRAY"))
	r := reencode(t, JPEG, graySrc, EncodeOptions{Format: WebP, Quality: 70})
	if ReadMetadata(WebP, r.Data).ICC != nil || len(r.Notes) != 1 {
		t.Errorf("gray profile on colour WebP: notes %v", r.Notes)
	}

	// Transparency becomes white in a JPEG.
	r = reencode(t, WebP, testimg.Fixture("alpha_lossless.webp"), EncodeOptions{Format: JPEG, Quality: 90})
	if !slices.ContainsFunc(r.Notes, func(n string) bool { return n == "transparent areas were filled with white" }) {
		t.Errorf("notes %v", r.Notes)
	}
	img, _ := Decode(JPEG, r.Data)
	if c := color.RGBAModel.Convert(img.At(0, 0)).(color.RGBA); c.R < 240 || c.G < 240 || c.B < 240 {
		t.Errorf("corner %v, want white", c)
	}
}

func TestReencodeRefuses(t *testing.T) {
	for _, name := range []string{"animated.webp", "animated_alpha.webp"} {
		if _, err := Reencode(WebP, testimg.Fixture(name), EncodeOptions{Format: PNG}); !errors.Is(err, ErrAnimated) {
			t.Errorf("%s: %v", name, err)
		}
	}
	frame := func(c uint8) *image.Paletted {
		p := image.NewPaletted(image.Rect(0, 0, 8, 8), palette.WebSafe)
		for i := range p.Pix {
			p.Pix[i] = c
		}
		return p
	}
	var buf bytes.Buffer
	gif.EncodeAll(&buf, &gif.GIF{Image: []*image.Paletted{frame(1), frame(2), frame(3)}, Delay: []int{10, 10, 10}})
	if gifFrames(buf.Bytes()) != 2 { // counting stops at two
		t.Errorf("gif frames %d", gifFrames(buf.Bytes()))
	}
	if _, err := Reencode(GIF, buf.Bytes(), EncodeOptions{Format: PNG}); !errors.Is(err, ErrAnimated) {
		t.Errorf("animated gif: %v", err)
	}
	buf.Reset()
	gif.Encode(&buf, frame(5), nil)
	if r, err := Reencode(GIF, buf.Bytes(), EncodeOptions{Format: PNG}); err != nil || !r.Exact {
		t.Errorf("still gif: %v", err)
	}
	if _, err := Reencode(PNG, testimg.PNG(testimg.Scene(1, 20000, 1), testimg.EXIF{}), EncodeOptions{Format: WebP}); err == nil {
		t.Error("WebP wider than 16383 accepted")
	}
}

func TestMetadataHelpers(t *testing.T) {
	cases := [][6]int{{4000, 3000, 2000, 0, 2000, 1500}, {3000, 4000, 2000, 2000, 1500, 2000}, {100, 50, 2000, 2000, 100, 50}, {5000, 1, 100, 0, 100, 1}}
	for _, c := range cases {
		if w, h, _ := fitWithin(c[0], c[1], c[2], c[3]); w != c[4] || h != c[5] {
			t.Errorf("fit %v: %dx%d", c, w, h)
		}
	}
	tiff := testimg.EXIF{Orientation: 6, DateTime: "2021:05:06 07:08:09"}.TIFFBlock()
	up := uprightExif(tiff, true)
	var m Meta
	parseTIFFExif(up, &m)
	if m.Orientation != 1 || m.TakenAt != "2021-05-06T07:08:09" {
		t.Errorf("upright exif %+v", m)
	}
	parseTIFFExif(tiff, &m)
	if m.Orientation != 6 {
		t.Error("the original EXIF was changed")
	}
	// Metadata survives a WebP round trip through the extended layout.
	md := Metadata{ICC: fakeICC("RGB "), EXIF: tiff, XMP: []byte("<x:xmpmeta/>")}
	lossy := testimg.Fixture("alpha_lossy.webp")
	out, err := webpWithMetadata(lossy, md, 64, 48)
	if err != nil {
		t.Fatal(err)
	}
	got := ReadMetadata(WebP, out)
	if !bytes.Equal(got.ICC, md.ICC) || !bytes.Equal(got.EXIF, md.EXIF) || !bytes.Equal(got.XMP, md.XMP) {
		t.Errorf("webp metadata %+v", got)
	}
	if img, err := Decode(WebP, out); err != nil || isOpaque(img) {
		t.Errorf("webp with metadata: %v", err)
	}
	p := pngWithMetadata(testimg.PNG(testimg.Scene(1, 10, 10), testimg.EXIF{}), md)
	if got := ReadMetadata(PNG, p); !bytes.Equal(got.ICC, md.ICC) || !bytes.Equal(got.EXIF, md.EXIF) || !bytes.Equal(got.XMP, md.XMP) {
		t.Errorf("png metadata %+v", got)
	}
	if _, err := png.Decode(bytes.NewReader(p)); err != nil {
		t.Errorf("png with metadata: %v", err)
	}
}

func TestWebPStudioRange(t *testing.T) {
	// Lossy WebP is studio-range YUV: white and black must come back as
	// such, as browsers show them.
	img := image.NewRGBA(image.Rect(0, 0, 32, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 32; x++ {
			v := uint8(255)
			if x >= 16 {
				v = 0
			}
			img.SetRGBA(x, y, color.RGBA{v, v, v, 255})
		}
	}
	r := reencode(t, PNG, testimg.PNG(img, testimg.EXIF{}), EncodeOptions{Format: WebP, Quality: 100})
	got, _ := Decode(WebP, r.Data)
	white := color.RGBAModel.Convert(got.At(4, 4)).(color.RGBA)
	black := color.RGBAModel.Convert(got.At(28, 4)).(color.RGBA)
	if white.R < 253 || black.R > 2 || r.PSNR < 35 {
		t.Errorf("white %v, black %v, PSNR %.1f", white, black, r.PSNR)
	}
}
