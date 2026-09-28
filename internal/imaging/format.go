// Package imaging decodes supported image formats, applies EXIF
// orientation, and produces thumbnails, display previews and perceptual
// fingerprints through one fixed pipeline.
package imaging

import (
	"bytes"
	"path/filepath"
	"strings"
)

// Format is a supported image container.
type Format string

const (
	JPEG Format = "jpeg"
	PNG  Format = "png"
	GIF  Format = "gif"
	WebP Format = "webp"
	BMP  Format = "bmp"
	TIFF Format = "tiff"
)

// Extension returns the canonical file extension (with dot).
func (f Format) Extension() string {
	switch f {
	case JPEG:
		return ".jpg"
	case TIFF:
		return ".tif"
	case "":
		return ""
	}
	return "." + string(f)
}

// MIME returns the content type.
func (f Format) MIME() string {
	switch f {
	case JPEG:
		return "image/jpeg"
	case PNG:
		return "image/png"
	case GIF:
		return "image/gif"
	case WebP:
		return "image/webp"
	case BMP:
		return "image/bmp"
	case TIFF:
		return "image/tiff"
	}
	return "application/octet-stream"
}

// Extensions lists every extension associated with a format (lower-case, with dot).
func (f Format) Extensions() []string {
	switch f {
	case JPEG:
		return []string{".jpg", ".jpeg", ".jpe", ".jfif"}
	case TIFF:
		return []string{".tif", ".tiff"}
	case "":
		return nil
	}
	return []string{"." + string(f)}
}

// SniffLen is how many leading bytes Sniff needs.
const SniffLen = 32

// rawExtensions are TIFF-based camera RAW formats. They share TIFF magic
// but decoding their first IFD yields a tiny preview, so they are rejected.
var rawExtensions = map[string]bool{
	".3fr": true, ".ari": true, ".arw": true, ".bay": true, ".cr2": true, ".cr3": true,
	".crw": true, ".dcr": true, ".dng": true, ".erf": true, ".fff": true, ".iiq": true,
	".k25": true, ".kdc": true, ".mef": true, ".mos": true, ".mrw": true, ".nef": true,
	".nrw": true, ".orf": true, ".pef": true, ".raf": true, ".raw": true, ".rw2": true,
	".rwl": true, ".sr2": true, ".srf": true, ".srw": true, ".x3f": true,
}

// Sniff identifies a supported format from the leading bytes of a file.
// When the format is not supported, reason explains why (for import reports).
func Sniff(head []byte, filename string) (f Format, reason string) {
	ext := strings.ToLower(filepath.Ext(filename))
	switch {
	case bytes.HasPrefix(head, []byte{0xFF, 0xD8, 0xFF}):
		return JPEG, ""
	case bytes.HasPrefix(head, []byte("\x89PNG\r\n\x1a\n")):
		return PNG, ""
	case bytes.HasPrefix(head, []byte("GIF87a")), bytes.HasPrefix(head, []byte("GIF89a")):
		return GIF, ""
	case len(head) >= 12 && string(head[0:4]) == "RIFF" && string(head[8:12]) == "WEBP":
		return WebP, ""
	case bytes.HasPrefix(head, []byte("BM")) && len(head) >= 14 && (ext == ".bmp" || ext == ".dib" || bmpPlausible(head)):
		return BMP, ""
	case bytes.HasPrefix(head, []byte("II*\x00")), bytes.HasPrefix(head, []byte("MM\x00*")):
		if rawExtensions[ext] || (len(head) >= 10 && string(head[8:10]) == "CR") {
			return "", "camera RAW is not supported"
		}
		return TIFF, ""
	case bytes.HasPrefix(head, []byte("II+\x00")), bytes.HasPrefix(head, []byte("MM\x00+")):
		return "", "BigTIFF is not supported"
	case len(head) >= 12 && string(head[4:8]) == "ftyp":
		brand := string(head[8:12])
		switch brand {
		case "heic", "heix", "hevc", "hevx", "heim", "heis", "mif1", "msf1":
			return "", "HEIC/HEIF is not supported"
		case "avif", "avis":
			return "", "AVIF is not supported"
		case "crx ":
			return "", "camera RAW is not supported"
		}
		return "", "not an image (ISO media file)"
	case bytes.HasPrefix(head, []byte{0xFF, 0x0A}), bytes.HasPrefix(head, []byte("\x00\x00\x00\x0cJXL \r\n\x87\n")):
		return "", "JPEG XL is not supported"
	case bytes.HasPrefix(head, []byte("8BPS")):
		return "", "Photoshop PSD is not supported"
	}
	if rawExtensions[ext] {
		return "", "camera RAW is not supported"
	}
	switch ext {
	case ".svg", ".svgz":
		return "", "SVG is not supported"
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".tif", ".tiff":
		return "", "file content does not match its " + ext + " extension"
	}
	return "", "not a supported image"
}

// bmpPlausible checks the BMP header when the extension is not .bmp, to
// avoid claiming arbitrary files that happen to start with "BM".
func bmpPlausible(h []byte) bool {
	// Reserved fields (bytes 6..9) must be zero; DIB header size is one of
	// the known values.
	if h[6] != 0 || h[7] != 0 || h[8] != 0 || h[9] != 0 || len(h) < 18 {
		return false
	}
	switch uint32(h[14]) | uint32(h[15])<<8 | uint32(h[16])<<16 | uint32(h[17])<<24 {
	case 12, 40, 52, 56, 64, 108, 124:
		return true
	}
	return false
}
