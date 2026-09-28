package imaging

import (
	"bytes"
	"encoding/binary"
)

// Meta is the EXIF subset PhotoBag uses.
type Meta struct {
	// Orientation is the EXIF orientation (1..8); 1 when absent.
	Orientation int
	// TakenAt is the local capture time as "2006-01-02T15:04:05", or "".
	TakenAt string
	// TakenOffset is the UTC offset of TakenAt such as "+02:00", or "".
	TakenOffset string
}

// ReadMeta extracts EXIF metadata from a file of the given format. Missing
// or malformed metadata yields defaults, never an error.
func ReadMeta(f Format, b []byte) Meta {
	m := Meta{Orientation: 1}
	var tiff []byte
	switch f {
	case JPEG:
		tiff = jpegExif(b)
	case PNG:
		tiff = pngExif(b)
	case WebP:
		tiff = webpExif(b)
	case TIFF:
		tiff = b
	}
	if tiff != nil {
		parseTIFFExif(tiff, &m)
	}
	if m.Orientation < 1 || m.Orientation > 8 {
		m.Orientation = 1
	}
	return m
}

var exifHeader = []byte("Exif\x00\x00")

func jpegExif(b []byte) []byte {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return nil
	}
	i := 2
	for i+4 <= len(b) {
		if b[i] != 0xFF {
			return nil
		}
		marker := b[i+1]
		if marker == 0xFF { // fill byte
			i++
			continue
		}
		if marker == 0xD9 || marker == 0xDA { // EOI / start of scan
			return nil
		}
		if marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7) { // no length
			i += 2
			continue
		}
		n := int(binary.BigEndian.Uint16(b[i+2:]))
		if n < 2 || i+2+n > len(b) {
			return nil
		}
		seg := b[i+4 : i+2+n]
		if marker == 0xE1 && bytes.HasPrefix(seg, exifHeader) {
			return seg[len(exifHeader):]
		}
		i += 2 + n
	}
	return nil
}

func pngExif(b []byte) []byte {
	i := 8
	for i+12 <= len(b) {
		n := int(binary.BigEndian.Uint32(b[i:]))
		typ := string(b[i+4 : i+8])
		if n < 0 || i+12+n > len(b) {
			return nil
		}
		if typ == "eXIf" {
			d := b[i+8 : i+8+n]
			return bytes.TrimPrefix(d, exifHeader)
		}
		if typ == "IEND" {
			return nil
		}
		i += 12 + n
	}
	return nil
}

func webpExif(b []byte) []byte {
	i := 12
	for i+8 <= len(b) {
		typ := string(b[i : i+4])
		n := int(binary.LittleEndian.Uint32(b[i+4:]))
		if n < 0 || i+8+n > len(b) {
			return nil
		}
		if typ == "EXIF" {
			return bytes.TrimPrefix(b[i+8:i+8+n], exifHeader)
		}
		i += 8 + n + n&1
	}
	return nil
}

const (
	tagOrientation     = 0x0112
	tagDateTime        = 0x0132
	tagExifIFD         = 0x8769
	tagDateTimeOrig    = 0x9003
	tagDateTimeDigit   = 0x9004
	tagOffsetTime      = 0x9010
	tagOffsetTimeOrig  = 0x9011
	tagOffsetTimeDigit = 0x9012
)

type ifdReader struct {
	b  []byte
	bo binary.ByteOrder
}

type ifdEntry struct {
	tag, typ uint16
	count    uint32
	raw      []byte // the 4-byte value/offset field
}

func (r ifdReader) entries(off uint32) []ifdEntry {
	if int64(off)+2 > int64(len(r.b)) {
		return nil
	}
	n := int(r.bo.Uint16(r.b[off:]))
	base := int(off) + 2
	if base+n*12 > len(r.b) {
		n = (len(r.b) - base) / 12
	}
	out := make([]ifdEntry, 0, n)
	for k := 0; k < n; k++ {
		e := r.b[base+k*12 : base+k*12+12]
		out = append(out, ifdEntry{
			tag:   r.bo.Uint16(e[0:]),
			typ:   r.bo.Uint16(e[2:]),
			count: r.bo.Uint32(e[4:]),
			raw:   e[8:12],
		})
	}
	return out
}

func (r ifdReader) short(e ifdEntry) (int, bool) {
	switch e.typ {
	case 3: // SHORT
		return int(r.bo.Uint16(e.raw)), true
	case 4: // LONG
		return int(r.bo.Uint32(e.raw)), true
	}
	return 0, false
}

func (r ifdReader) ascii(e ifdEntry) string {
	if e.typ != 2 || e.count == 0 || e.count > 64 {
		return ""
	}
	var d []byte
	if e.count <= 4 {
		d = e.raw[:e.count]
	} else {
		off := r.bo.Uint32(e.raw)
		if int64(off)+int64(e.count) > int64(len(r.b)) {
			return ""
		}
		d = r.b[off : off+e.count]
	}
	if i := bytes.IndexByte(d, 0); i >= 0 {
		d = d[:i]
	}
	return string(bytes.TrimSpace(d))
}

func parseTIFFExif(b []byte, m *Meta) {
	if len(b) < 8 {
		return
	}
	var r ifdReader
	switch string(b[:2]) {
	case "II":
		r.bo = binary.LittleEndian
	case "MM":
		r.bo = binary.BigEndian
	default:
		return
	}
	r.b = b
	if r.bo.Uint16(b[2:]) != 42 {
		return
	}
	var dateTime, exifOff string
	var exifIFD uint32
	for _, e := range r.entries(r.bo.Uint32(b[4:])) {
		switch e.tag {
		case tagOrientation:
			if v, ok := r.short(e); ok {
				m.Orientation = v
			}
		case tagDateTime:
			dateTime = r.ascii(e)
		case tagExifIFD:
			if v, ok := r.short(e); ok {
				exifIFD = uint32(v)
			}
		}
	}
	var orig, digit, offOrig, offDigit string
	if exifIFD != 0 {
		for _, e := range r.entries(exifIFD) {
			switch e.tag {
			case tagDateTimeOrig:
				orig = r.ascii(e)
			case tagDateTimeDigit:
				digit = r.ascii(e)
			case tagOffsetTimeOrig:
				offOrig = r.ascii(e)
			case tagOffsetTimeDigit:
				offDigit = r.ascii(e)
			case tagOffsetTime:
				exifOff = r.ascii(e)
			}
		}
	}
	switch {
	case exifDate(orig) != "":
		m.TakenAt, m.TakenOffset = exifDate(orig), exifOffset(offOrig)
	case exifDate(digit) != "":
		m.TakenAt, m.TakenOffset = exifDate(digit), exifOffset(offDigit)
	case exifDate(dateTime) != "":
		m.TakenAt, m.TakenOffset = exifDate(dateTime), exifOffset(exifOff)
	}
}

// exifDate converts "2006:01:02 15:04:05" to "2006-01-02T15:04:05".
func exifDate(s string) string {
	if len(s) < 19 {
		return ""
	}
	s = s[:19]
	for i, c := range []byte(s) {
		switch i {
		case 4, 7:
			if c != ':' && c != '-' {
				return ""
			}
		case 10:
			if c != ' ' && c != 'T' {
				return ""
			}
		case 13, 16:
			if c != ':' {
				return ""
			}
		default:
			if c < '0' || c > '9' {
				return ""
			}
		}
	}
	if s[:4] == "0000" || s[5:7] == "00" || s[8:10] == "00" {
		return ""
	}
	return s[0:4] + "-" + s[5:7] + "-" + s[8:10] + "T" + s[11:19]
}

// exifOffset validates "+02:00" style offsets.
func exifOffset(s string) string {
	if len(s) != 6 || (s[0] != '+' && s[0] != '-') || s[3] != ':' {
		return ""
	}
	for _, i := range []int{1, 2, 4, 5} {
		if s[i] < '0' || s[i] > '9' {
			return ""
		}
	}
	return s
}
