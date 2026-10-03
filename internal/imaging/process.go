package imaging

import (
	"bytes"
	"errors"
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
	thumb, tw, th, err := encodeThumb(work)
	if err != nil {
		return nil, err
	}
	return &Result{
		Format:      f,
		Width:       dw,
		Height:      dh,
		Meta:        meta,
		Thumb:       thumb,
		ThumbW:      tw,
		ThumbH:      th,
		Fingerprint: ComputeFingerprint(work),
	}, nil
}

// encodeThumb makes the thumbnail JPEG of an oriented working image.
func encodeThumb(work *image.RGBA) (data []byte, w, h int, err error) {
	thumb := Resize(work, ThumbSize)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, thumb, &jpeg.Options{Quality: ThumbQuality}); err != nil {
		return nil, 0, 0, err
	}
	return buf.Bytes(), thumb.Rect.Dx(), thumb.Rect.Dy(), nil
}

// Thumbnail makes only the thumbnail of an image, the way Process does,
// for bags that make thumbnails when they are shown.
func Thumbnail(f Format, b []byte) (data []byte, w, h int, err error) {
	work, err := Working(f, b)
	if err != nil {
		return nil, 0, 0, err
	}
	return encodeThumb(work)
}

// Identify names the format of image bytes already in a bag. They were
// checked on import, where a .bmp name may have vouched for a BMP with an
// unusual header, so here any file starting with "BM" counts as one.
func Identify(b []byte) (Format, error) {
	f, reason := Sniff(b[:min(len(b), SniffLen)], ".bmp")
	if f == "" {
		return "", errors.New(reason)
	}
	return f, nil
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
