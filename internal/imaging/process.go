package imaging

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
)

// Pipeline sizes.
const (
	WorkingSize    = 1024
	ThumbSize      = 400
	ThumbQuality   = 80
	PreviewQuality = 85
)

// Result is everything the importer stores about an image.
type Result struct {
	Format Format
	// Width and Height are the display dimensions (orientation applied).
	Width, Height int
	Meta          Meta
	Thumb         []byte
	ThumbW        int
	ThumbH        int
	Fingerprint   Fingerprint
}

// Process runs the fixed pipeline: decode, box/area downsample to the
// working size, orient, then derive the thumbnail and thumbprint.
func Process(f Format, b []byte) (*Result, error) {
	w, h, err := DecodeConfig(f, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	meta := ReadMeta(f, b)
	img, err := Decode(f, b)
	if err != nil {
		return nil, err
	}
	dw, dh := OrientedSize(w, h, meta.Orientation)
	return derive(f, img, dw, dh, meta)
}

// derive makes the thumbnail and thumbprint of a decoded image whose
// display size is dw×dh.
func derive(f Format, img image.Image, dw, dh int, meta Meta) (*Result, error) {
	work := Orient(Downscale(img, WorkingSize), meta.Orientation)
	thumb := Resize(work, ThumbSize)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, thumb, &jpeg.Options{Quality: ThumbQuality}); err != nil {
		return nil, err
	}
	return &Result{
		Format:      f,
		Width:       dw,
		Height:      dh,
		Meta:        meta,
		Thumb:       buf.Bytes(),
		ThumbW:      thumb.Rect.Dx(),
		ThumbH:      thumb.Rect.Dy(),
		Fingerprint: ComputeFingerprint(work),
	}, nil
}

// Working returns the oriented working image used for fingerprints.
func Working(f Format, b []byte) (*image.RGBA, error) {
	img, err := Decode(f, b)
	if err != nil {
		return nil, err
	}
	return Orient(Downscale(img, WorkingSize), ReadMeta(f, b).Orientation), nil
}

// UprightPNG renders an image losslessly as PNG, orientation applied.
func UprightPNG(f Format, b []byte) ([]byte, error) {
	img, err := Decode(f, b)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, orientAny(img, ReadMeta(f, b).Orientation)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Preview renders an oriented JPEG whose long side is at most maxSide.
func Preview(f Format, b []byte, maxSide int) ([]byte, error) {
	img, err := Decode(f, b)
	if err != nil {
		return nil, err
	}
	o := ReadMeta(f, b).Orientation
	out := Orient(Downscale(img, maxSide), o)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, out, &jpeg.Options{Quality: PreviewQuality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
