package imaging

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"testing"

	"photobag/internal/testimg"
)

func mustProcess(t *testing.T, b []byte) *Result {
	t.Helper()
	f, reason := Sniff(b[:min(len(b), SniffLen)], "x")
	if f == "" {
		t.Fatalf("sniff failed: %s", reason)
	}
	r, err := Process(f, b)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func fpOf(t *testing.T, b []byte) Fingerprint { return mustProcess(t, b).Fingerprint }

func TestSniff(t *testing.T) {
	scene := testimg.Scene(1, 40, 30)
	cases := []struct {
		name string
		data []byte
		want Format
	}{
		{"a.jpg", testimg.JPEG(scene, 90, testimg.EXIF{}), JPEG},
		{"a.png", testimg.PNG(scene, testimg.EXIF{}), PNG},
		{"a.heic", []byte("\x00\x00\x00\x18ftypheic\x00\x00\x00\x00mif1heic"), ""},
		{"a.nef", []byte("MM\x00*\x00\x00\x00\x08\x00\x00\x00\x00\x00\x00\x00\x00"), ""},
		{"a.cr2", []byte("II*\x00\x10\x00\x00\x00CR\x02\x00\x00\x00\x00\x00"), ""},
		{"notes.txt", []byte("hello world, this is text"), ""},
		{"fake.jpg", []byte("hello world, this is text"), ""},
	}
	for _, c := range cases {
		got, reason := Sniff(c.data[:min(len(c.data), SniffLen)], c.name)
		if got != c.want {
			t.Errorf("%s: got %q (%s), want %q", c.name, got, reason, c.want)
		}
		if got == "" && reason == "" {
			t.Errorf("%s: missing reason", c.name)
		}
	}
}

func TestEXIFMetadata(t *testing.T) {
	scene := testimg.Scene(2, 40, 30)
	e := testimg.EXIF{Orientation: 6, DateTime: "2019:07:04 15:22:10", Offset: "+02:00"}
	for _, tc := range []struct {
		f Format
		b []byte
	}{
		{JPEG, testimg.JPEG(scene, 90, e)},
		{PNG, testimg.PNG(scene, e)},
	} {
		m := ReadMeta(tc.f, tc.b)
		if m.Orientation != 6 || m.TakenAt != "2019-07-04T15:22:10" || m.TakenOffset != "+02:00" {
			t.Errorf("%s: got %+v", tc.f, m)
		}
	}
	if m := ReadMeta(JPEG, testimg.JPEG(scene, 90, testimg.EXIF{})); m.Orientation != 1 || m.TakenAt != "" {
		t.Errorf("no exif: got %+v", m)
	}
}

func near(a, b uint8) bool { return int(a)-int(b) < 12 && int(b)-int(a) < 12 }

func TestAllOrientations(t *testing.T) {
	// A display image with a distinct colour in each quadrant.
	display := image.NewRGBA(image.Rect(0, 0, 60, 40))
	quad := []color.RGBA{{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255}, {255, 255, 0, 255}}
	for y := 0; y < 40; y++ {
		for x := 0; x < 60; x++ {
			q := 0
			if x >= 30 {
				q++
			}
			if y >= 20 {
				q += 2
			}
			display.SetRGBA(x, y, quad[q])
		}
	}
	for o := 1; o <= 8; o++ {
		stored := testimg.Transform(display, testimg.Inverse(o))
		for _, enc := range []struct {
			f Format
			b []byte
		}{
			{PNG, testimg.PNG(stored, testimg.EXIF{Orientation: o})},
			{JPEG, testimg.JPEG(stored, 95, testimg.EXIF{Orientation: o})},
		} {
			r, err := Process(enc.f, enc.b)
			if err != nil {
				t.Fatal(err)
			}
			if r.Width != 60 || r.Height != 40 {
				t.Errorf("o=%d %s: display size %dx%d, want 60x40", o, enc.f, r.Width, r.Height)
			}
			work, err := Working(enc.f, enc.b)
			if err != nil {
				t.Fatal(err)
			}
			for _, pt := range []struct {
				x, y, q int
			}{{5, 5, 0}, {55, 5, 1}, {5, 35, 2}, {55, 35, 3}} {
				c := work.RGBAAt(pt.x, pt.y)
				w := quad[pt.q]
				if !near(c.R, w.R) || !near(c.G, w.G) || !near(c.B, w.B) {
					t.Errorf("o=%d %s: pixel (%d,%d) = %v, want %v", o, enc.f, pt.x, pt.y, c, w)
				}
			}
		}
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	return testimg.Fixture(name)
}

func TestFixtureFormats(t *testing.T) {
	for _, name := range []string{
		"still_lossy.webp", "still_lossless.webp", "alpha_lossy.webp", "alpha_lossless.webp",
		"animated.webp", "animated_alpha.webp", "orient6.webp", "orient6.tif", "plain.tif", "plain.bmp",
	} {
		t.Run(name, func(t *testing.T) {
			r := mustProcess(t, readFixture(t, name))
			if r.Width != 64 || r.Height != 48 {
				t.Fatalf("display size %dx%d, want 64x48", r.Width, r.Height)
			}
			if len(r.Thumb) == 0 {
				t.Fatal("no thumbnail")
			}
			b := readFixture(t, name)
			f, _ := Sniff(b[:SniffLen], name)
			work, err := Working(f, b)
			if err != nil {
				t.Fatal(err)
			}
			// The black marker must end up in the top-left corner.
			if c := work.RGBAAt(2, 2); c.R > 40 || c.G > 40 || c.B > 40 {
				if name[:5] != "alpha" { // alpha fixtures fade the marker to the matte
					t.Errorf("top-left pixel %v, want black", c)
				}
			}
		})
	}
	if m := ReadMeta(WebP, readFixture(t, "orient6.webp")); m.Orientation != 6 {
		t.Errorf("webp EXIF orientation = %d", m.Orientation)
	}
	if webpFirstFrame(readFixture(t, "animated.webp")) == nil {
		t.Error("animated.webp should be recognised as animated")
	}
}

func TestGIFFirstFrame(t *testing.T) {
	pal := color.Palette{color.RGBA{0, 0, 0, 255}, color.RGBA{255, 255, 255, 255}}
	a := image.NewPaletted(image.Rect(0, 0, 20, 10), pal)
	b := image.NewPaletted(image.Rect(0, 0, 20, 10), pal)
	for i := range b.Pix {
		b.Pix[i] = 1
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, &gif.GIF{Image: []*image.Paletted{a, b}, Delay: []int{10, 10}}); err != nil {
		t.Fatal(err)
	}
	work, err := Working(GIF, buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if c := work.RGBAAt(5, 5); c.R != 0 {
		t.Errorf("expected first (black) frame, got %v", c)
	}
}

func TestSimilarityRobustness(t *testing.T) {
	orig := testimg.Scene(42, 1600, 1200)
	base := fpOf(t, testimg.JPEG(orig, 92, testimg.EXIF{}))

	variants := map[string][]byte{
		"reencoded q50": testimg.JPEG(orig, 50, testimg.EXIF{}),
		"resized 50%":   testimg.JPEG(testimg.Resize(orig, 800, 600), 85, testimg.EXIF{}),
		"resized 20%":   testimg.JPEG(testimg.Resize(orig, 320, 240), 85, testimg.EXIF{}),
		"brighter":      testimg.JPEG(testimg.Brighten(orig, 12), 90, testimg.EXIF{}),
		"png":           testimg.PNG(orig, testimg.EXIF{}),
	}
	for name, b := range variants {
		fp := fpOf(t, b)
		s := Similarity(&base, &fp)
		t.Logf("%-14s ham=%2d dE=%5.2f sim=%.3f", name, Hamming(base.PHash, fp.PHash), ColorDistance(&base.Color, &fp.Color), s)
		if s < 0.85 {
			t.Errorf("%s: similarity %.3f too low for a near-duplicate", name, s)
		}
	}

	var maxOther float64
	for seed := uint64(100); seed < 140; seed++ {
		fp := fpOf(t, testimg.JPEG(testimg.Scene(seed, 400, 300), 85, testimg.EXIF{}))
		maxOther = max(maxOther, Similarity(&base, &fp))
	}
	t.Logf("max similarity to unrelated scenes: %.3f", maxOther)
	if maxOther > 0.7 {
		t.Errorf("unrelated scenes too similar: %.3f", maxOther)
	}
}

func TestFlatImagesNeedColourMatch(t *testing.T) {
	sky := fpOf(t, testimg.JPEG(testimg.Flat(1, 400, 300, color.RGBA{120, 170, 230, 255}), 90, testimg.EXIF{}))
	sky2 := fpOf(t, testimg.JPEG(testimg.Flat(2, 400, 300, color.RGBA{120, 170, 230, 255}), 90, testimg.EXIF{}))
	black := fpOf(t, testimg.JPEG(testimg.Flat(3, 400, 300, color.RGBA{5, 5, 5, 255}), 90, testimg.EXIF{}))
	if sky.ACEnergy >= FlatEnergy {
		t.Fatalf("flat sky energy %.2f not below FlatEnergy", sky.ACEnergy)
	}
	scene := fpOf(t, testimg.JPEG(testimg.Scene(9, 400, 300), 90, testimg.EXIF{}))
	if scene.ACEnergy < FlatEnergy {
		t.Fatalf("scene energy %.2f unexpectedly flat", scene.ACEnergy)
	}
	if s := Similarity(&sky, &black); s > 0.5 {
		t.Errorf("sky vs black frame similarity %.3f", s)
	}
	if s := Similarity(&sky, &scene); s > 0.5 {
		t.Errorf("sky vs textured scene similarity %.3f", s)
	}
	// Re-encoded or resized copies of a flat image must still match, even
	// though their pHash bits are mostly noise.
	img := testimg.Flat(1, 1200, 900, color.RGBA{120, 170, 230, 255})
	q95 := fpOf(t, testimg.JPEG(img, 95, testimg.EXIF{}))
	for name, b := range map[string][]byte{
		"q60":     testimg.JPEG(img, 60, testimg.EXIF{}),
		"resized": testimg.JPEG(testimg.Resize(img, 600, 450), 80, testimg.EXIF{}),
	} {
		fp := fpOf(t, b)
		if s := Similarity(&q95, &fp); s < 0.9 {
			t.Errorf("flat %s copy similarity %.3f (ham=%d)", name, s, Hamming(q95.PHash, fp.PHash))
		}
	}
	t.Logf("sky vs sky2 sim=%.3f", Similarity(&sky, &sky2))
}

func TestDownscaleNeverUpscales(t *testing.T) {
	small := testimg.Scene(5, 100, 50)
	out := Downscale(small, 1024)
	if out.Rect.Dx() != 100 || out.Rect.Dy() != 50 {
		t.Fatalf("got %v", out.Rect)
	}
	big := testimg.Scene(5, 5000, 1000)
	out = Downscale(big, 1024)
	if out.Rect.Dx() != 1024 || out.Rect.Dy() != 205 {
		t.Fatalf("got %v", out.Rect)
	}
}

func BenchmarkProcess12MP(b *testing.B) {
	data := testimg.JPEG(testimg.Scene(7, 4000, 3000), 90, testimg.EXIF{})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Process(JPEG, data); err != nil {
			b.Fatal(err)
		}
	}
}
