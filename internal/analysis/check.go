package analysis

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"math/big"
	"strings"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"photobag/internal/llm"
)

// CheckResult reports a connection check.
type CheckResult struct {
	// OK means the model read the number in the test image: the endpoint
	// works and accepts images.
	OK      bool     `json:"ok"`
	Message string   `json:"message"`
	Models  []string `json:"models"`
	// ModelsError explains why the model list is unavailable (some
	// servers do not offer one).
	ModelsError string `json:"modelsError,omitempty"`
	Reply       string `json:"reply,omitempty"`
	Model       string `json:"model,omitempty"`
	Millis      int64  `json:"millis"`
}

// CheckImage renders a JPEG showing code in large digits.
func CheckImage(code string) ([]byte, error) {
	const w, h = 640, 320
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{250, 247, 240, 255}), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(0, 250, w, h), image.NewUniform(color.RGBA{0, 128, 128, 255}), image.Point{}, draw.Src)
	f, err := opentype.Parse(gobold.TTF)
	if err != nil {
		return nil, err
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: 150, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil, err
	}
	defer face.Close()
	d := &font.Drawer{Dst: img, Src: image.NewUniform(color.RGBA{20, 20, 30, 255}), Face: face}
	adv := d.MeasureString(code)
	d.Dot = fixed.Point26_6{X: (fixed.I(w) - adv) / 2, Y: fixed.I(195)}
	d.DrawString(code)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Check lists the endpoint's models and asks the model to read a number
// from a generated image.
func Check(ctx context.Context, s Settings, client *llm.Client) CheckResult {
	var res CheckResult
	models, err := client.Models(ctx)
	if err != nil {
		res.ModelsError = err.Error()
		if llm.IsFatal(err) && !strings.Contains(err.Error(), "HTTP 404") {
			res.Message = err.Error()
			return res
		}
	}
	res.Models = models
	if s.Model == "" && len(models) > 1 {
		res.Message = "Choose a model."
		return res
	}
	n, _ := rand.Int(rand.Reader, big.NewInt(9000))
	code := fmt.Sprint(1000 + n.Int64())
	img, err := CheckImage(code)
	if err != nil {
		res.Message = err.Error()
		return res
	}
	start := time.Now()
	r, err := client.Chat(ctx, []llm.Message{
		{Role: "system", Text: s.system()},
		{Role: "user", Text: "What number is written in this image? Reply with only the digits.", Images: [][]byte{img}},
	})
	res.Millis = time.Since(start).Milliseconds()
	if err != nil {
		res.Message = err.Error()
		return res
	}
	res.Reply, res.Model = r.Text, r.Model
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, r.Text)
	if digits == code {
		res.OK = true
		res.Message = fmt.Sprintf("The model read the test image correctly in %.1fs.", float64(res.Millis)/1000)
	} else {
		res.Message = fmt.Sprintf("The model answered %q but the image showed %s: it may not accept images.", truncate(r.Text, 80), code)
	}
	return res
}
