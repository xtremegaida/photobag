package comfy

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"unicode/utf16"
)

var pngMagic = []byte("\x89PNG\r\n\x1a\n")

// EmbedPrompt adds the workflow to a PNG as a "prompt" text chunk, as
// ComfyUI's Save Image does, so dropping the file on ComfyUI opens the
// workflow. Other data is returned unchanged.
func EmbedPrompt(png []byte, w *Workflow) []byte {
	if !bytes.HasPrefix(png, pngMagic) || len(png) < 33 {
		return png
	}
	// The first chunk is IHDR (8 + 4 + 4 + 13 + 4 bytes in); insert after it.
	if string(png[12:16]) != "IHDR" {
		return png
	}
	ihdrEnd := 8 + 8 + int(binary.BigEndian.Uint32(png[8:12])) + 4
	if ihdrEnd > len(png) || hasText(png, "prompt") {
		return png
	}
	chunk := textChunk("prompt", asciiJSON(w.Compact()))
	out := make([]byte, 0, len(png)+len(chunk))
	out = append(out, png[:ihdrEnd]...)
	out = append(out, chunk...)
	return append(out, png[ihdrEnd:]...)
}

// PromptFromPNG returns the "prompt" text chunk of a PNG, if any.
func PromptFromPNG(png []byte) []byte {
	var found []byte
	eachChunk(png, func(typ string, data []byte) bool {
		if typ == "tEXt" {
			if k, v, ok := bytes.Cut(data, []byte{0}); ok && string(k) == "prompt" {
				found = v
				return false
			}
		}
		return typ != "IDAT"
	})
	return found
}

func hasText(png []byte, key string) bool {
	found := false
	eachChunk(png, func(typ string, data []byte) bool {
		if typ == "tEXt" || typ == "iTXt" {
			if k, _, ok := bytes.Cut(data, []byte{0}); ok && string(k) == key {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func eachChunk(png []byte, fn func(typ string, data []byte) bool) {
	if !bytes.HasPrefix(png, pngMagic) {
		return
	}
	for p := 8; p+12 <= len(png); {
		n := int(binary.BigEndian.Uint32(png[p : p+4]))
		if n < 0 || p+12+n > len(png) {
			return
		}
		typ := string(png[p+4 : p+8])
		if !fn(typ, png[p+8:p+8+n]) || typ == "IEND" {
			return
		}
		p += 12 + n
	}
}

func textChunk(key string, text []byte) []byte {
	data := append(append([]byte(key), 0), text...)
	var b bytes.Buffer
	binary.Write(&b, binary.BigEndian, uint32(len(data)))
	b.WriteString("tEXt")
	b.Write(data)
	crc := crc32.NewIEEE()
	crc.Write([]byte("tEXt"))
	crc.Write(data)
	binary.Write(&b, binary.BigEndian, crc.Sum32())
	return b.Bytes()
}

// asciiJSON escapes non-ASCII characters (tEXt chunks are Latin-1, and
// ComfyUI writes ASCII JSON too).
func asciiJSON(js []byte) []byte {
	var b bytes.Buffer
	for _, r := range string(js) {
		switch {
		case r < 0x80:
			b.WriteRune(r)
		case r > 0xFFFF:
			r1, r2 := utf16.EncodeRune(r)
			fmt.Fprintf(&b, `\u%04x\u%04x`, r1, r2)
		default:
			fmt.Fprintf(&b, `\u%04x`, r)
		}
	}
	return b.Bytes()
}
