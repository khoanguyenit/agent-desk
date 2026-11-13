// pageimg makes the pictures of the app's page on khoabug.com out of one
// recording: the still pictures as they are, a few parts cut out of them for
// the smaller cards, and the app's icon at a size for the page.
//
//	pageimg <shots dir> <app icon png> <out dir>
//
// The parts are cut at fixed places of a 1266x793 window. If the board is laid
// out differently, look at the new pictures and move the rectangles below.
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
)

// A picture of the page: taken whole from a shot, or a rectangle of one.
type piece struct {
	out, shot string
	cut       image.Rectangle // empty: the whole shot
}

var pieces = []piece{
	{"board.png", "top.png", image.Rectangle{}},
	{"trail.png", "trail.png", image.Rectangle{}},
	{"workflow.png", "workflow.png", image.Rectangle{}},
	{"summary.png", "summary.png", image.Rectangle{}},
	{"attention.png", "attention.png", image.Rectangle{}},
	{"history.png", "history.png", image.Rectangle{}},
	{"permit.png", "permit.png", image.Rectangle{}},
	// The line about the limit with the desks under it.
	{"limit.png", "top.png", image.Rect(0, 92, 860, 312)},
	// One desk, for the first card. Column 0 is the window's border.
	{"card-desks.png", "top.png", image.Rect(1, 170, 293, 310)},
	// The session waiting for an answer and the one stopped by a limit.
	{"card-attention.png", "attention.png", image.Rect(18, 328, 560, 676)},
	// The summary card of the session.
	{"card-summary.png", "summary.png", image.Rect(697, 178, 1266, 520)},
	// The request to edit a file, as a diff.
	{"card-permit.png", "permit.png", image.Rect(700, 421, 1256, 712)},
}

// The icon is drawn at 1024 pixels; the page shows it at up to 64.
const iconFactor = 8

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: pageimg <shots dir> <app icon png> <out dir>")
		os.Exit(2)
	}
	shots, icon, out := os.Args[1], os.Args[2], os.Args[3]
	if err := run(shots, icon, out); err != nil {
		fmt.Fprintln(os.Stderr, "pageimg:", err)
		os.Exit(1)
	}
}

func run(shots, icon, out string) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	for _, p := range pieces {
		if p.cut.Empty() {
			raw, err := os.ReadFile(filepath.Join(shots, p.shot))
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(out, p.out), raw, 0o644); err != nil {
				return err
			}
			fmt.Printf("%s: %s as it is\n", p.out, p.shot)
			continue
		}
		src, err := load(filepath.Join(shots, p.shot))
		if err != nil {
			return err
		}
		r := p.cut
		if !r.In(src.Bounds()) {
			return fmt.Errorf("%s: %v is outside %s (%v)", p.out, r, p.shot, src.Bounds())
		}
		dst := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
		draw.Draw(dst, dst.Bounds(), src, r.Min, draw.Src)
		if err := save(filepath.Join(out, p.out), dst); err != nil {
			return err
		}
		fmt.Printf("%s: %dx%d\n", p.out, r.Dx(), r.Dy())
	}
	src, err := load(icon)
	if err != nil {
		return err
	}
	small := shrink(src, iconFactor)
	if err := save(filepath.Join(out, "icon.png"), small); err != nil {
		return err
	}
	fmt.Printf("icon.png: %dx%d\n", small.Bounds().Dx(), small.Bounds().Dy())
	return nil
}

// shrink scales a picture down by a whole factor, averaging each block of
// pixels and weighting colour by alpha, so that transparent edges do not darken.
func shrink(src image.Image, k int) *image.NRGBA {
	b := src.Bounds()
	w, h := b.Dx()/k, b.Dy()/k
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var r, g, bl, a, n uint64
			for dy := 0; dy < k; dy++ {
				for dx := 0; dx < k; dx++ {
					c := color.NRGBAModel.Convert(src.At(b.Min.X+x*k+dx, b.Min.Y+y*k+dy)).(color.NRGBA)
					r += uint64(c.R) * uint64(c.A)
					g += uint64(c.G) * uint64(c.A)
					bl += uint64(c.B) * uint64(c.A)
					a += uint64(c.A)
					n++
				}
			}
			px := color.NRGBA{A: uint8(a / n)}
			if a > 0 {
				px.R, px.G, px.B = uint8(r/a), uint8(g/a), uint8(bl/a)
			}
			dst.SetNRGBA(x, y, px)
		}
	}
	return dst
}

func load(file string) (image.Image, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	return img, nil
}

func save(file string, img image.Image) error {
	f, err := os.Create(file)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
