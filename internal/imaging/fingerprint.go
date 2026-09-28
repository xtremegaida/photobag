package imaging

import (
	"image"
	"math"
	"math/bits"
	"sort"
)

// FingerprintVersion identifies the thumbprint algorithm. Bump it whenever
// the pipeline or maths changes so stored fingerprints can be recomputed.
const FingerprintVersion = 1

// FlatEnergy is the ACEnergy below which an image counts as "flat" (sky,
// black frames, blank scans): its pHash bits are mostly noise.
const FlatEnergy = 3.0

// Fingerprint is the perceptual thumbprint of an image.
type Fingerprint struct {
	Version int
	// PHash is a 64-bit DCT perceptual hash of the 32x32 luma image.
	PHash uint64
	// Color is a 4x4 grid of mean CIELAB values, quantised to bytes
	// (L*2.55, a+128, b+128).
	Color [48]byte
	// Aspect is width/height after orientation.
	Aspect float64
	// ACEnergy is the RMS of the low-frequency AC signal in luma units.
	ACEnergy float64
}

var dctCos [8][32]float64

func init() {
	for u := 0; u < 8; u++ {
		c := math.Sqrt(2.0 / 32)
		if u == 0 {
			c = math.Sqrt(1.0 / 32)
		}
		for x := 0; x < 32; x++ {
			dctCos[u][x] = c * math.Cos(float64(2*x+1)*float64(u)*math.Pi/64)
		}
	}
}

// ComputeFingerprint derives the thumbprint from an oriented, opaque image
// (the ≤1024px working image of the pipeline).
func ComputeFingerprint(img *image.RGBA) Fingerprint {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	fp := Fingerprint{Version: FingerprintVersion, Aspect: float64(w) / float64(h)}

	// 32x32 luma by area averaging.
	var luma [32][32]float64
	cells(img, 32, func(cx, cy int, r, g, b float64) {
		luma[cy][cx] = 0.299*r + 0.587*g + 0.114*b
	})

	// Separable orthonormal 2-D DCT-II, low 8x8 frequencies only.
	var tmp [32][8]float64 // [y][u]
	for y := 0; y < 32; y++ {
		for u := 0; u < 8; u++ {
			var s float64
			for x := 0; x < 32; x++ {
				s += luma[y][x] * dctCos[u][x]
			}
			tmp[y][u] = s
		}
	}
	var coef [64]float64 // index v*8+u (v vertical frequency)
	for v := 0; v < 8; v++ {
		for u := 0; u < 8; u++ {
			var s float64
			for y := 0; y < 32; y++ {
				s += tmp[y][u] * dctCos[v][y]
			}
			coef[v*8+u] = s
		}
	}
	ac := make([]float64, 0, 63)
	var energy float64
	for i := 1; i < 64; i++ {
		ac = append(ac, coef[i])
		energy += coef[i] * coef[i]
	}
	fp.ACEnergy = math.Sqrt(energy / 1024)
	sort.Float64s(ac)
	median := ac[31] // 63 values
	for i := 0; i < 64; i++ {
		if coef[i] > median {
			fp.PHash |= 1 << uint(i)
		}
	}

	// 4x4 colour layout in CIELAB.
	cells(img, 4, func(cx, cy int, r, g, b float64) {
		l, a, bb := rgbToLab(r, g, b)
		i := 3 * (cy*4 + cx)
		fp.Color[i] = clampByte(l * 2.55)
		fp.Color[i+1] = clampByte(a + 128)
		fp.Color[i+2] = clampByte(bb + 128)
	})
	return fp
}

// cells averages img over an n×n grid. Every cell covers at least one pixel,
// so tiny images still work.
func cells(img *image.RGBA, n int, fn func(cx, cy int, r, g, b float64)) {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	for cy := 0; cy < n; cy++ {
		y0 := cy * h / n
		y1 := max(y0+1, (cy+1)*h/n)
		for cx := 0; cx < n; cx++ {
			x0 := cx * w / n
			x1 := max(x0+1, (cx+1)*w/n)
			var r, g, b float64
			for y := y0; y < y1; y++ {
				p := img.Pix[y*img.Stride:]
				for x := x0; x < x1; x++ {
					r += float64(p[4*x])
					g += float64(p[4*x+1])
					b += float64(p[4*x+2])
				}
			}
			cnt := float64((y1 - y0) * (x1 - x0))
			fn(cx, cy, r/cnt, g/cnt, b/cnt)
		}
	}
}

func clampByte(v float64) byte {
	return byte(math.Max(0, math.Min(255, math.Round(v))))
}

func srgbToLinear(c float64) float64 {
	c /= 255
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

// rgbToLab converts sRGB (0..255) to CIELAB (D65).
func rgbToLab(r, g, b float64) (float64, float64, float64) {
	rl, gl, bl := srgbToLinear(r), srgbToLinear(g), srgbToLinear(b)
	x := (0.4124*rl + 0.3576*gl + 0.1805*bl) / 0.95047
	y := 0.2126*rl + 0.7152*gl + 0.0722*bl
	z := (0.0193*rl + 0.1192*gl + 0.9505*bl) / 1.08883
	f := func(t float64) float64 {
		if t > 216.0/24389 {
			return math.Cbrt(t)
		}
		return (24389.0/27*t + 16) / 116
	}
	fx, fy, fz := f(x), f(y), f(z)
	return 116*fy - 16, 500 * (fx - fy), 200 * (fy - fz)
}

// Hamming returns the number of differing pHash bits.
func Hamming(a, b uint64) int { return bits.OnesCount64(a ^ b) }

// ColorDistance is the mean CIE76 ΔE between the two 4x4 colour grids.
func ColorDistance(a, b *[48]byte) float64 {
	var sum float64
	for i := 0; i < 48; i += 3 {
		dl := (float64(a[i]) - float64(b[i])) / 2.55
		da := float64(a[i+1]) - float64(b[i+1])
		db := float64(a[i+2]) - float64(b[i+2])
		sum += math.Sqrt(dl*dl + da*da + db*db)
	}
	return sum / 16
}

// Similarity returns 0..1 (1 = visually identical) between two thumbprints.
func Similarity(a, b *Fingerprint) float64 {
	ham := float64(Hamming(a.PHash, b.PHash))
	dE := ColorDistance(&a.Color, &b.Color)
	aspect := 0.0
	if a.Aspect > 0 && b.Aspect > 0 {
		aspect = math.Min(1, math.Abs(math.Log(a.Aspect/b.Aspect))/math.Log(1.5))
	}
	d := 0.75*math.Min(1, ham/32) + 0.2*math.Min(1, dE/50) + 0.05*aspect
	aFlat, bFlat := a.ACEnergy < FlatEnergy, b.ACEnergy < FlatEnergy
	switch {
	case aFlat && bFlat:
		// The pHash bits of flat images are noise, so judge them purely on
		// a close match of their colour layout.
		d = 0.85*math.Min(1, dE/20) + 0.15*aspect
	case aFlat || bFlat:
		// A flat and a textured image are rarely duplicates: demand a
		// close colour match and a stricter structural match.
		d = math.Max(d, math.Max(math.Min(1, dE/15), math.Min(1, ham/16)))
	}
	return 1 - math.Min(1, d)
}
