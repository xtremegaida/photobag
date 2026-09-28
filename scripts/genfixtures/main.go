// Command genfixtures writes a synthetic photo library for manual testing:
//
//	go run ./scripts/genfixtures <dir> [scale] [extra]
//
// extra adds that many additional random scenes under extra/.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"photobag/internal/testimg"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: genfixtures <dir> [scale] [extra]")
		os.Exit(2)
	}
	scale := 1.0
	if len(os.Args) > 2 {
		v, err := strconv.ParseFloat(os.Args[2], 64)
		if err != nil || v <= 0 {
			fmt.Fprintln(os.Stderr, "scale must be a positive number")
			os.Exit(2)
		}
		scale = v
	}
	tree, err := testimg.WriteTree(os.Args[1], scale)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	n := len(tree.Files)
	if len(os.Args) > 3 {
		extra, err := strconv.Atoi(os.Args[3])
		if err != nil || extra < 0 {
			fmt.Fprintln(os.Stderr, "extra must be a non-negative integer")
			os.Exit(2)
		}
		dir := filepath.Join(os.Args[1], "extra")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		for i := 0; i < extra; i++ {
			w, h := max(16, int(1200*scale)), max(16, int(900*scale))
			if i%3 == 0 {
				w, h = h, w
			}
			p := filepath.Join(dir, fmt.Sprintf("scene_%04d.jpg", i))
			if err := os.WriteFile(p, testimg.JPEG(testimg.Scene(uint64(1000+i), w, h), 85, testimg.EXIF{}), 0o644); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}
		n += extra
	}
	fmt.Printf("wrote %d files to %s\n", n, os.Args[1])
}
