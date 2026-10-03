package imaging

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"sort"

	"github.com/gen2brain/jpegn"
)

// Metadata is what re-encoding carries from one file to the next, as raw
// payloads.
type Metadata struct {
	// ICC is the colour profile. Pixel values only mean the right colours
	// with it, so it is always kept.
	ICC []byte
	// EXIF is the TIFF structure of the EXIF block (no "Exif\0\0" prefix).
	EXIF []byte
	// XMP is the XMP packet.
	XMP []byte
}

var (
	iccHeader = []byte("ICC_PROFILE\x00")
	xmpHeader = []byte("http://ns.adobe.com/xap/1.0/\x00")
)

const pngXMPKeyword = "XML:com.adobe.xmp"

// maxMetadata bounds a decompressed profile or packet.
const maxMetadata = 16 << 20

// ReadMetadata extracts the colour profile, EXIF and XMP of a file.
// Anything missing or malformed is left out.
func ReadMetadata(f Format, b []byte) Metadata {
	var m Metadata
	switch f {
	case JPEG:
		readJPEGMetadata(b, &m)
	case PNG:
		readPNGMetadata(b, &m)
	case WebP:
		readWebPMetadata(b, &m)
	case TIFF:
		readTIFFMetadata(b, &m)
	}
	return m
}

func readJPEGMetadata(b []byte, m *Metadata) {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return
	}
	type part struct {
		seq  int
		data []byte
	}
	var icc []part
	iccCount := 0
	for i := 2; i+4 <= len(b); {
		if b[i] != 0xFF {
			break
		}
		marker := b[i+1]
		if marker == 0xFF {
			i++
			continue
		}
		if marker == 0xD9 || marker == 0xDA {
			break
		}
		if marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7) {
			i += 2
			continue
		}
		n := int(binary.BigEndian.Uint16(b[i+2:]))
		if n < 2 || i+2+n > len(b) {
			break
		}
		seg := b[i+4 : i+2+n]
		switch {
		case marker == 0xE1 && bytes.HasPrefix(seg, exifHeader) && m.EXIF == nil:
			m.EXIF = bytes.Clone(seg[len(exifHeader):])
		case marker == 0xE1 && bytes.HasPrefix(seg, xmpHeader) && m.XMP == nil:
			m.XMP = bytes.Clone(seg[len(xmpHeader):])
		case marker == 0xE2 && bytes.HasPrefix(seg, iccHeader) && len(seg) > len(iccHeader)+2:
			seq, count := int(seg[len(iccHeader)]), int(seg[len(iccHeader)+1])
			iccCount = count
			icc = append(icc, part{seq, seg[len(iccHeader)+2:]})
		}
		i += 2 + n
	}
	if len(icc) > 0 && len(icc) == iccCount {
		sort.Slice(icc, func(i, j int) bool { return icc[i].seq < icc[j].seq })
		var out []byte
		for k, p := range icc {
			if p.seq != k+1 {
				return
			}
			out = append(out, p.data...)
		}
		m.ICC = out
	}
}

func inflate(data []byte) []byte {
	zr, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	defer zr.Close()
	out, err := io.ReadAll(io.LimitReader(zr, maxMetadata+1))
	if err != nil || len(out) > maxMetadata {
		return nil
	}
	return out
}

func readPNGMetadata(b []byte, m *Metadata) {
	for i := 8; i+12 <= len(b); {
		n := int(binary.BigEndian.Uint32(b[i:]))
		typ := string(b[i+4 : i+8])
		if n < 0 || i+12+n > len(b) {
			return
		}
		d := b[i+8 : i+8+n]
		switch typ {
		case "iCCP":
			if k := bytes.IndexByte(d, 0); k >= 0 && k+2 <= len(d) && d[k+1] == 0 {
				m.ICC = inflate(d[k+2:])
			}
		case "eXIf":
			m.EXIF = bytes.Clone(bytes.TrimPrefix(d, exifHeader))
		case "iTXt":
			if text, ok := pngXMP(d); ok {
				m.XMP = text
			}
		case "IEND":
			return
		}
		i += 12 + n
	}
}

// pngXMP reads an iTXt chunk holding XMP.
func pngXMP(d []byte) ([]byte, bool) {
	k := bytes.IndexByte(d, 0)
	if k < 0 || string(d[:k]) != pngXMPKeyword || k+3 > len(d) {
		return nil, false
	}
	compressed := d[k+1] == 1
	rest := d[k+3:]
	for range 2 { // language tag, translated keyword
		z := bytes.IndexByte(rest, 0)
		if z < 0 {
			return nil, false
		}
		rest = rest[z+1:]
	}
	if compressed {
		rest = inflate(rest)
		return rest, rest != nil
	}
	return bytes.Clone(rest), true
}

func readWebPMetadata(b []byte, m *Metadata) {
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WEBP" {
		return
	}
	for i := 12; i+8 <= len(b); {
		typ := string(b[i : i+4])
		n := int(binary.LittleEndian.Uint32(b[i+4:]))
		if n < 0 || i+8+n > len(b) {
			return
		}
		d := b[i+8 : i+8+n]
		switch typ {
		case "ICCP":
			m.ICC = bytes.Clone(d)
		case "EXIF":
			m.EXIF = bytes.Clone(bytes.TrimPrefix(d, exifHeader))
		case "XMP ":
			m.XMP = bytes.Clone(d)
		}
		i += 8 + n + n&1
	}
}

const (
	tagXMP = 700
	tagICC = 34675
)

func readTIFFMetadata(b []byte, m *Metadata) {
	if len(b) < 8 {
		return
	}
	r := ifdReader{b: b}
	switch string(b[:2]) {
	case "II":
		r.bo = binary.LittleEndian
	case "MM":
		r.bo = binary.BigEndian
	default:
		return
	}
	for _, e := range r.entries(r.bo.Uint32(b[4:])) {
		if (e.tag != tagICC && e.tag != tagXMP) || e.count <= 4 || e.count > maxMetadata {
			continue
		}
		off := r.bo.Uint32(e.raw)
		if int64(off)+int64(e.count) > int64(len(b)) {
			continue
		}
		d := bytes.Clone(b[off : off+e.count])
		if e.tag == tagICC {
			m.ICC = d
		} else {
			m.XMP = d
		}
	}
}

// iccSpace returns the colour space of an ICC profile ("RGB ", "GRAY",
// "CMYK"...), or "" if it is not a profile.
func iccSpace(icc []byte) string {
	if len(icc) < 128 || string(icc[36:40]) != "acsp" {
		return ""
	}
	return string(icc[16:20])
}

// uprightExif returns EXIF for pixels already turned upright: the
// orientation tag reads 1, and the embedded thumbnail (still in the old
// orientation) is unlinked. It returns nil for malformed EXIF.
func uprightExif(tiff []byte, rotated bool) []byte {
	if len(tiff) < 8 {
		return nil
	}
	out := bytes.Clone(tiff)
	r := ifdReader{b: out}
	switch string(out[:2]) {
	case "II":
		r.bo = binary.LittleEndian
	case "MM":
		r.bo = binary.BigEndian
	default:
		return nil
	}
	if r.bo.Uint16(out[2:]) != 42 {
		return nil
	}
	ifd0 := r.bo.Uint32(out[4:])
	entries := r.entries(ifd0)
	if entries == nil {
		return nil
	}
	for _, e := range entries {
		if e.tag == tagOrientation && e.typ == 3 {
			r.bo.PutUint16(e.raw, 1)
		}
	}
	if rotated {
		next := int(ifd0) + 2 + len(entries)*12
		if next+4 <= len(out) {
			r.bo.PutUint32(out[next:], 0)
		}
	}
	return out
}

// errNoRoom reports metadata too big for its container.
var errNoRoom = errors.New("too large for the format")

// jpegMetadata turns metadata into jpegn options: EXIF as APP1, XMP as
// APP1, the profile as APP2 chunks.
func jpegMetadata(m Metadata) (exif []byte, segs []jpegn.Segment, err error) {
	const maxSeg = 65533
	if m.EXIF != nil {
		exif = append(bytes.Clone(exifHeader), m.EXIF...)
		if len(exif) > maxSeg {
			return nil, nil, errNoRoom
		}
	}
	if m.ICC != nil {
		const chunk = maxSeg - 14
		count := (len(m.ICC) + chunk - 1) / chunk
		if count > 255 {
			return nil, nil, errNoRoom
		}
		for k := 0; k < count; k++ {
			part := m.ICC[k*chunk : min((k+1)*chunk, len(m.ICC))]
			d := append(append(bytes.Clone(iccHeader), byte(k+1), byte(count)), part...)
			segs = append(segs, jpegn.Segment{Marker: 0xE2, Data: d})
		}
	}
	if m.XMP != nil {
		d := append(bytes.Clone(xmpHeader), m.XMP...)
		if len(d) > maxSeg {
			return nil, nil, errNoRoom
		}
		segs = append(segs, jpegn.Segment{Marker: 0xE1, Data: d})
	}
	return exif, segs, nil
}

func pngChunk(typ string, data []byte) []byte {
	c := make([]byte, 8, 12+len(data))
	binary.BigEndian.PutUint32(c, uint32(len(data)))
	copy(c[4:], typ)
	c = append(c, data...)
	return binary.BigEndian.AppendUint32(c, crc32.ChecksumIEEE(c[4:]))
}

// pngWithMetadata adds metadata chunks to an encoded PNG, right after its
// header (before the palette and image data, as the chunks require).
func pngWithMetadata(b []byte, m Metadata) []byte {
	if m.ICC == nil && m.EXIF == nil && m.XMP == nil {
		return b
	}
	ihdrEnd := 8 + 12 + int(binary.BigEndian.Uint32(b[8:]))
	var extra []byte
	if m.ICC != nil {
		var z bytes.Buffer
		zw, _ := zlib.NewWriterLevel(&z, zlib.BestCompression)
		zw.Write(m.ICC)
		zw.Close()
		extra = append(extra, pngChunk("iCCP", append([]byte("ICC Profile\x00\x00"), z.Bytes()...))...)
	}
	if m.EXIF != nil {
		extra = append(extra, pngChunk("eXIf", m.EXIF)...)
	}
	if m.XMP != nil {
		d := append([]byte(pngXMPKeyword), 0, 0, 0, 0, 0)
		extra = append(extra, pngChunk("iTXt", append(d, m.XMP...))...)
	}
	out := make([]byte, 0, len(b)+len(extra))
	out = append(out, b[:ihdrEnd]...)
	out = append(out, extra...)
	return append(out, b[ihdrEnd:]...)
}

type riffChunk struct {
	id   string
	data []byte
}

// webpWithMetadata adds metadata to an encoded WebP, which takes the
// extended (VP8X) layout: VP8X, ICCP, [ALPH], VP8/VP8L, EXIF, XMP.
func webpWithMetadata(b []byte, m Metadata, width, height int) ([]byte, error) {
	if m.ICC == nil && m.EXIF == nil && m.XMP == nil {
		return b, nil
	}
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WEBP" {
		return nil, errors.New("not a WebP file")
	}
	var chunks []riffChunk
	var flags byte
	for i := 12; i+8 <= len(b); {
		id := string(b[i : i+4])
		n := int(binary.LittleEndian.Uint32(b[i+4:]))
		if n < 0 || i+8+n > len(b) {
			return nil, errors.New("malformed WebP")
		}
		d := b[i+8 : i+8+n]
		switch id {
		case "VP8X":
			if len(d) > 0 {
				flags = d[0]
			}
		case "ICCP", "EXIF", "XMP ":
		case "VP8L":
			// The alpha_is_used bit follows the 14-bit width and height.
			if len(d) >= 5 && d[0] == 0x2f && binary.LittleEndian.Uint32(d[1:])&(1<<28) != 0 {
				flags |= 0x10
			}
			chunks = append(chunks, riffChunk{id, d})
		case "ALPH":
			flags |= 0x10
			chunks = append(chunks, riffChunk{id, d})
		default:
			chunks = append(chunks, riffChunk{id, d})
		}
		i += 8 + n + n&1
	}
	vp8x := make([]byte, 10)
	var list []riffChunk
	list = append(list, riffChunk{"VP8X", vp8x})
	if m.ICC != nil {
		flags |= 0x20
		list = append(list, riffChunk{"ICCP", m.ICC})
	}
	list = append(list, chunks...)
	if m.EXIF != nil {
		flags |= 0x08
		list = append(list, riffChunk{"EXIF", m.EXIF})
	}
	if m.XMP != nil {
		flags |= 0x04
		list = append(list, riffChunk{"XMP ", m.XMP})
	}
	vp8x[0] = flags
	put24(vp8x[4:], width)
	put24(vp8x[7:], height)
	var body bytes.Buffer
	body.WriteString("WEBP")
	for _, c := range list {
		body.WriteString(c.id)
		binary.Write(&body, binary.LittleEndian, uint32(len(c.data)))
		body.Write(c.data)
		if len(c.data)%2 == 1 {
			body.WriteByte(0)
		}
	}
	out := make([]byte, 8, 8+body.Len())
	copy(out, "RIFF")
	binary.LittleEndian.PutUint32(out[4:], uint32(body.Len()))
	return append(out, body.Bytes()...), nil
}
