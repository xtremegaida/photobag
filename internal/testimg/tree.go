package testimg

import (
	"bytes"
	"embed"
	"image"
	"image/color"
	"image/gif"
	"os"
	"path/filepath"
)

//go:embed fixtures/*.webp fixtures/*.tif fixtures/*.bmp
var fixtures embed.FS

// Fixture returns an embedded Pillow-generated fixture (see fixtures/gen.py).
func Fixture(name string) []byte {
	b, err := fixtures.ReadFile("fixtures/" + name)
	if err != nil {
		panic(err)
	}
	return b
}

// TreeFile describes one file written by WriteTree.
type TreeFile struct {
	Rel string
	// Kind: "image", "exact-dup", "near-dup", "unsupported", "corrupt",
	// "hidden", "flat".
	Kind string
	// Of names the original for duplicates.
	Of string
}

// Tree is the manifest of a fixture tree.
type Tree struct {
	Files []TreeFile
}

// Count returns how many files of the given kinds exist.
func (t Tree) Count(kinds ...string) int {
	n := 0
	for _, f := range t.Files {
		for _, k := range kinds {
			if f.Kind == k {
				n++
			}
		}
	}
	return n
}

// WriteTree writes a synthetic photo library under dir:
//
//	2019/Holiday/beach.jpg          scene, EXIF date
//	2019/Holiday/beach_small.jpg    near-duplicate (resized, recompressed)
//	2019/Holiday/sunset.png         scene
//	2020/beach.jpg                  different scene, same name as 2019's
//	2020/copy of sunset.png         exact duplicate
//	2020/portrait.jpg               stored rotated, EXIF orientation 6
//	2020/Party/party_01..06.jpg     six scenes (for scoring)
//	2020/Party/party_03_edit.jpg    near-duplicate (brightened)
//	misc/…                          webp/tif/bmp/gif, flat sky, junk files
//	.hidden/secret.jpg              hidden folder (skipped)
//
// scale multiplies image dimensions (1 = ~1600px photos).
func WriteTree(dir string, scale float64) (Tree, error) {
	var t Tree
	px := func(n int) int { return max(16, int(float64(n)*scale)) }
	write := func(rel, kind, of string, data []byte) error {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		t.Files = append(t.Files, TreeFile{Rel: rel, Kind: kind, Of: of})
		return os.WriteFile(p, data, 0o644)
	}
	beach := Scene(1, px(1600), px(1200))
	sunset := PNG(Scene(2, px(1200), px(800)), EXIF{})
	steps := []struct {
		rel, kind, of string
		data          []byte
	}{
		{"2019/Holiday/beach.jpg", "image", "", JPEG(beach, 90, EXIF{DateTime: "2019:07:04 15:22:10", Offset: "+02:00"})},
		{"2019/Holiday/beach_small.jpg", "near-dup", "2019/Holiday/beach.jpg", JPEG(Resize(beach, px(800), px(600)), 70, EXIF{})},
		{"2019/Holiday/sunset.png", "image", "", sunset},
		{"2020/beach.jpg", "image", "", JPEG(Scene(3, px(1600), px(1066)), 88, EXIF{DateTime: "2020:01:15 09:00:00"})},
		{"2020/copy of sunset.png", "exact-dup", "2019/Holiday/sunset.png", sunset},
		{"2020/portrait.jpg", "image", "", JPEG(Transform(Scene(4, px(1200), px(1600)), Inverse(6)), 90, EXIF{Orientation: 6})},
	}
	for i := 1; i <= 6; i++ {
		name := "2020/Party/party_0" + string(rune('0'+i)) + ".jpg"
		img := Scene(uint64(100+i), px(1200), px(900))
		steps = append(steps, struct {
			rel, kind, of string
			data          []byte
		}{name, "image", "", JPEG(img, 85, EXIF{})})
		if i == 3 {
			steps = append(steps, struct {
				rel, kind, of string
				data          []byte
			}{"2020/Party/party_03_edit.jpg", "near-dup", name, JPEG(Brighten(img, 10), 80, EXIF{})})
		}
	}
	for _, s := range steps {
		if err := write(s.rel, s.kind, s.of, s.data); err != nil {
			return t, err
		}
	}
	misc := []struct {
		rel, kind string
		data      []byte
	}{
		{"misc/still.webp", "image", Fixture("still_lossy.webp")},
		{"misc/animated.webp", "image", Fixture("animated.webp")},
		{"misc/rotated.tif", "image", Fixture("orient6.tif")},
		{"misc/plain.bmp", "image", Fixture("plain.bmp")},
		{"misc/anim.gif", "image", animatedGIF()},
		{"misc/sky.jpg", "flat", JPEG(Flat(7, px(1200), px(800), color.RGBA{120, 170, 230, 255}), 90, EXIF{})},
		{"misc/notes.txt", "unsupported", []byte("not an image, just some notes about the trip\n")},
		{"misc/phone.heic", "unsupported", []byte("\x00\x00\x00\x18ftypheic\x00\x00\x00\x00mif1heicmore bytes here")},
		{"misc/broken.jpg", "corrupt", append([]byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 16}, bytes.Repeat([]byte{0x42}, 200)...)},
		{".hidden/secret.jpg", "hidden", JPEG(Scene(9, 64, 64), 80, EXIF{})},
	}
	for _, m := range misc {
		if err := write(m.rel, m.kind, "", m.data); err != nil {
			return t, err
		}
	}
	return t, nil
}

func animatedGIF() []byte {
	pal := color.Palette{color.RGBA{20, 20, 60, 255}, color.RGBA{240, 200, 40, 255}}
	var frames []*image.Paletted
	for f := 0; f < 3; f++ {
		img := image.NewPaletted(image.Rect(0, 0, 48, 32), pal)
		for y := 0; y < 32; y++ {
			for x := 0; x < 48; x++ {
				if (x/8+y/8+f)%2 == 0 {
					img.Pix[y*img.Stride+x] = 1
				}
			}
		}
		frames = append(frames, img)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, &gif.GIF{Image: frames, Delay: []int{20, 20, 20}}); err != nil {
		panic(err)
	}
	return buf.Bytes()
}
