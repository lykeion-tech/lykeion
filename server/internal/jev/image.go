package jev

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg" // registers the JPEG decoder
	"image/png"
	"math"
	"slices"
)

// What Jev takes in one image and one request.
const (
	MinImageSide   = 280
	MaxImageSide   = 1568
	MaxImageBytes  = 16 << 20
	maxBody        = 32 << 20
	maxImageSource = 64 << 20 // ponytail: pixels decoded at most, to bound memory; raise if real figures need more
)

// Image is one picture Jev looks at, encoded as format ("png" or "jpg").
type Image struct {
	Format string
	Data   []byte
}

// FitImage decodes a PNG or JPEG whose decoded format is one of want, and
// re-encodes it as a PNG Jev accepts: scaled, aspect kept, so each side is
// within MinImageSide..MaxImageSide, and padded with white along its short
// side when it is too long and narrow to fit by scaling alone. Re-encoding
// drops metadata; no EXIF rotation is applied.
func FitImage(raw []byte, want ...string) (Image, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return Image{}, err
	}
	if !slices.Contains(want, format) {
		return Image{}, fmt.Errorf("jev: image is %s, not %v", format, want)
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width*cfg.Height > maxImageSource {
		return Image{}, fmt.Errorf("jev: image of %dx%d pixels is out of range", cfg.Width, cfg.Height)
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return Image{}, err
	}
	w, h := cfg.Width, cfg.Height
	s := 1.0
	lo, hi := float64(MinImageSide)/float64(min(w, h)), float64(MaxImageSide)/float64(max(w, h))
	if lo <= hi {
		s = max(lo, min(s, hi))
	} else {
		s = hi
	}
	sw, sh := scaled(w, s), scaled(h, s)
	cw, ch := max(sw, MinImageSide), max(sh, MinImageSide)
	dst := image.NewNRGBA(image.Rect(0, 0, cw, ch))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	resize(dst, image.Rect((cw-sw)/2, (ch-sh)/2, (cw-sw)/2+sw, (ch-sh)/2+sh), src)
	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return Image{}, err
	}
	if buf.Len() > MaxImageBytes {
		return Image{}, errors.New("jev: the fitted image is over the size limit")
	}
	return Image{Format: "png", Data: buf.Bytes()}, nil
}

// scaled is n scaled by s, kept within Jev's side limits.
func scaled(n int, s float64) int {
	return min(MaxImageSide, max(1, int(math.Round(float64(n)*s))))
}

// resize draws src into r of dst, each pixel the average of the source
// pixels it covers (or the nearest one, when enlarging).
func resize(dst *image.NRGBA, r image.Rectangle, src image.Image) {
	b := src.Bounds()
	fx, fy := float64(b.Dx())/float64(r.Dx()), float64(b.Dy())/float64(r.Dy())
	for y := 0; y < r.Dy(); y++ {
		y0 := b.Min.Y + int(float64(y)*fy)
		y1 := min(b.Max.Y, max(y0+1, b.Min.Y+int(float64(y+1)*fy)))
		for x := 0; x < r.Dx(); x++ {
			x0 := b.Min.X + int(float64(x)*fx)
			x1 := min(b.Max.X, max(x0+1, b.Min.X+int(float64(x+1)*fx)))
			var sr, sg, sb, sa, n uint64
			for yy := y0; yy < y1; yy++ {
				for xx := x0; xx < x1; xx++ {
					c := color.NRGBA64Model.Convert(src.At(xx, yy)).(color.NRGBA64)
					sr, sg, sb, sa, n = sr+uint64(c.R), sg+uint64(c.G), sb+uint64(c.B), sa+uint64(c.A), n+1
				}
			}
			dst.Set(r.Min.X+x, r.Min.Y+y, color.NRGBA64{R: uint16(sr / n), G: uint16(sg / n), B: uint16(sb / n), A: uint16(sa / n)})
		}
	}
}
