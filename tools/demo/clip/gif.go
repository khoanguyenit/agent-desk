package main

// gif turns the frames written by rec into an animated GIF.
//
//	clip gif -dir frames -track track.json -out demo.gif [-fps 10] [-scale 1]
//
// A pointer is drawn over the frames from the track the demo script wrote:
// the window was driven without a real mouse, and a recording in which
// things happen with no hand in sight is hard to follow. Stretches in which
// nothing moves are cut short, the colours are reduced to one palette chosen
// from the recording itself, and each frame stores only what changed.

import (
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type trackEvent struct {
	T    int64   `json:"t"`
	Kind string  `json:"kind"` // "move" or "click"
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	Dur  int64   `json:"dur"`
	// Space is "page" for positions in CSS pixels of the page, "frame" for
	// positions in pixels of the captured frame.
	Space string `json:"space"`
}

type track struct {
	DPR    float64      `json:"dpr"`
	Events []trackEvent `json:"events"`
	// Cut lists stretches of time left out of the clip, such as a system
	// dialog that showed the user's own folders.
	Cut [][2]int64 `json:"cut"`
}

type frameFile struct {
	t    int64
	path string
}

// The pointer, drawn at one and a half times this size.
var arrow = []string{
	"X..........",
	"XX.........",
	"X#X........",
	"X##X.......",
	"X###X......",
	"X####X.....",
	"X#####X....",
	"X######X...",
	"X#######X..",
	"X########X.",
	"X#####XXXXX",
	"X##X##X....",
	"X#X.X##X...",
	"XX..X##X...",
	"X....X##X..",
	".....X##X..",
	"......XX...",
}

const arrowScale = 1.5

func drawArrow(img *image.RGBA, x, y int) {
	h := int(float64(len(arrow)) * arrowScale)
	w := int(float64(len(arrow[0])) * arrowScale)
	for dy := 0; dy < h; dy++ {
		for dx := 0; dx < w; dx++ {
			switch arrow[int(float64(dy)/arrowScale)][int(float64(dx)/arrowScale)] {
			case 'X':
				set(img, x+dx, y+dy, color.RGBA{20, 22, 28, 255})
			case '#':
				set(img, x+dx, y+dy, color.RGBA{255, 255, 255, 255})
			}
		}
	}
}

func set(img *image.RGBA, x, y int, c color.RGBA) {
	if image.Pt(x, y).In(img.Rect) {
		img.SetRGBA(x, y, c)
	}
}

// drawRing marks a click: a ring that widens around the pointer.
func drawRing(img *image.RGBA, cx, cy int, radius float64) {
	ring := color.RGBA{92, 159, 240, 255}
	r := int(radius) + 2
	for dy := -r; dy <= r; dy++ {
		for dx := -r; dx <= r; dx++ {
			d := math.Hypot(float64(dx), float64(dy))
			if d >= radius-1.3 && d <= radius+1.3 {
				set(img, cx+dx, cy+dy, ring)
			}
		}
	}
}

type pointer struct {
	x, y    float64
	visible bool
	ring    float64 // radius of the click ring, or 0
}

// pointerAt works out where the pointer is at time t.
func pointerAt(tr track, meta recMeta, t int64) pointer {
	toFrame := func(e trackEvent) (float64, float64) {
		if e.Space == "frame" {
			return e.X, e.Y
		}
		return float64(meta.ClientX) + e.X*tr.DPR, float64(meta.ClientY) + e.Y*tr.DPR
	}
	// It starts off to the lower right, out of the way.
	p := pointer{x: float64(meta.Width) * 0.56, y: float64(meta.Height) * 0.62}
	for _, e := range tr.Events {
		if e.T > t {
			break
		}
		switch e.Kind {
		case "move":
			p.visible = true
			tx, ty := toFrame(e)
			k := 1.0
			if e.Dur > 0 && t < e.T+e.Dur {
				k = float64(t-e.T) / float64(e.Dur)
				k = k * k * (3 - 2*k) // ease in and out
			}
			p.x, p.y = p.x+(tx-p.x)*k, p.y+(ty-p.y)*k
			if k < 1 {
				return p
			}
		case "click":
			if age := t - e.T; age >= 0 && age < 380 {
				p.ring = 5 + float64(age)/380*17
			} else {
				p.ring = 0
			}
		}
	}
	return p
}

// shrink scales an image down by averaging the pixels each new pixel covers.
func shrink(src *image.RGBA, scale float64) *image.RGBA {
	if scale >= 0.999 {
		return src
	}
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
	dw, dh := int(float64(sw)*scale+0.5), int(float64(sh)*scale+0.5)
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for dy := 0; dy < dh; dy++ {
		y0, y1 := float64(dy)/scale, float64(dy+1)/scale
		for dx := 0; dx < dw; dx++ {
			x0, x1 := float64(dx)/scale, float64(dx+1)/scale
			var r, g, b, wsum float64
			for sy := int(y0); sy < sh && float64(sy) < y1; sy++ {
				wy := math.Min(float64(sy+1), y1) - math.Max(float64(sy), y0)
				for sx := int(x0); sx < sw && float64(sx) < x1; sx++ {
					wx := math.Min(float64(sx+1), x1) - math.Max(float64(sx), x0)
					w := wx * wy
					i := src.PixOffset(sx, sy)
					r += float64(src.Pix[i]) * w
					g += float64(src.Pix[i+1]) * w
					b += float64(src.Pix[i+2]) * w
					wsum += w
				}
			}
			i := dst.PixOffset(dx, dy)
			dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2], dst.Pix[i+3] = uint8(r/wsum+0.5), uint8(g/wsum+0.5), uint8(b/wsum+0.5), 255
		}
	}
	return dst
}

// ---- one palette for the whole clip -------------------------------------------

type bin struct {
	r, g, b uint8 // five bits each
	n       int
}

func key15(r, g, b uint8) int { return int(r>>3)<<10 | int(g>>3)<<5 | int(b>>3) }

// medianCut picks n colours for the histogram: the box of colours that is
// largest, by spread times weight, is split at its median until there are n.
func medianCut(hist []int, n int) color.Palette {
	var all []bin
	for k, c := range hist {
		if c > 0 {
			all = append(all, bin{uint8(k >> 10), uint8(k >> 5 & 31), uint8(k & 31), c})
		}
	}
	boxes := [][]bin{all}
	spread := func(box []bin) (axis int, size int) {
		lo, hi := [3]uint8{31, 31, 31}, [3]uint8{}
		total := 0
		for _, b := range box {
			v := [3]uint8{b.r, b.g, b.b}
			for i := range v {
				lo[i], hi[i] = min(lo[i], v[i]), max(hi[i], v[i])
			}
			total += b.n
		}
		best := 0
		for i := 1; i < 3; i++ {
			if hi[i]-lo[i] > hi[best]-lo[best] {
				best = i
			}
		}
		// Weight grows slowly with the number of pixels, so small areas of a
		// distinct colour, such as the green of a diff, keep a box.
		return best, int(hi[best]-lo[best]) * int(math.Sqrt(float64(total))+1)
	}
	for len(boxes) < n {
		pick, pickAxis, pickSize := -1, 0, 0
		for i, box := range boxes {
			if len(box) < 2 {
				continue
			}
			if axis, size := spread(box); size > pickSize {
				pick, pickAxis, pickSize = i, axis, size
			}
		}
		if pick < 0 {
			break
		}
		box := boxes[pick]
		ch := func(b bin) uint8 { return [3]uint8{b.r, b.g, b.b}[pickAxis] }
		sort.Slice(box, func(i, j int) bool { return ch(box[i]) < ch(box[j]) })
		total := 0
		for _, b := range box {
			total += b.n
		}
		half, cut := 0, 1
		for i, b := range box {
			half += b.n
			if half >= total/2 {
				cut = max(1, min(i+1, len(box)-1))
				break
			}
		}
		boxes[pick] = box[:cut]
		boxes = append(boxes, box[cut:])
	}
	pal := make(color.Palette, 0, n+1)
	for _, box := range boxes {
		var r, g, b, total float64
		for _, c := range box {
			w := float64(c.n)
			r, g, b, total = r+float64(c.r)*w, g+float64(c.g)*w, b+float64(c.b)*w, total+w
		}
		up := func(v float64) uint8 { return uint8(math.Min(255, v/total*255/31+0.5)) }
		pal = append(pal, color.RGBA{up(r), up(g), up(b), 255})
	}
	return pal
}

type mapper struct {
	pal   color.Palette
	cache []int16
}

func (m *mapper) index(r, g, b uint8) uint8 {
	k := key15(r, g, b)
	if m.cache[k] >= 0 {
		return uint8(m.cache[k])
	}
	best, bestD := 0, math.MaxInt
	for i, c := range m.pal {
		p := c.(color.RGBA)
		if p.A == 0 {
			continue
		}
		dr, dg, db := int(r)-int(p.R), int(g)-int(p.G), int(b)-int(p.B)
		if d := 2*dr*dr + 4*dg*dg + 3*db*db; d < bestD {
			best, bestD = i, d
		}
	}
	m.cache[k] = int16(best)
	return uint8(best)
}

func loadPNG(path string) (*image.RGBA, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil, err
	}
	if rgba, ok := img.(*image.RGBA); ok {
		return rgba, nil
	}
	b := img.Bounds()
	out := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			out.Set(x, y, img.At(x, y))
		}
	}
	return out, nil
}

func makeGIF(args []string) error {
	fs := flag.NewFlagSet("gif", flag.ExitOnError)
	dir := fs.String("dir", "frames", "frames written by rec")
	trackPath := fs.String("track", "", "pointer track written by the demo script")
	out := fs.String("out", "demo.gif", "the GIF to write")
	fps := fs.Int("fps", 10, "frames per second of the clip")
	scale := fs.Float64("scale", 1, "size of the clip relative to the window")
	hold := fs.Int64("hold", 1400, "longest stretch, in ms, in which nothing changes")
	tail := fs.Int64("tail", 2600, "how long the last frame stays, in ms")
	dump := fs.String("dump", "", "also write some frames of the clip as PNG here, to look at")
	_ = fs.Parse(args)

	var meta recMeta
	raw, err := os.ReadFile(filepath.Join(*dir, "meta.json"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return err
	}
	tr := track{DPR: 1}
	if *trackPath != "" {
		raw, err := os.ReadFile(*trackPath)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &tr); err != nil {
			return err
		}
	}

	names, _ := filepath.Glob(filepath.Join(*dir, "f-*.png"))
	var frames []frameFile
	for _, n := range names {
		base := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(n), "f-"), ".png")
		if t, err := strconv.ParseInt(base, 10, 64); err == nil {
			frames = append(frames, frameFile{t, n})
		}
	}
	sort.Slice(frames, func(i, j int) bool { return frames[i].t < frames[j].t })
	if len(frames) == 0 {
		return fmt.Errorf("no frames in %s", *dir)
	}

	// The clip as a list of pictures and how long each stays.
	type shot struct {
		img   *image.RGBA
		delay int64
	}
	var shots []shot
	step := int64(1000 / *fps)
	cut := func(t int64) bool {
		for _, c := range tr.Cut {
			if t >= c[0] && t < c[1] {
				return true
			}
		}
		return false
	}
	var cur *image.RGBA
	curIdx, next := -1, 0
	var lastKey string
	still := int64(0)
	for t := frames[0].t; t <= meta.End; t += step {
		for next < len(frames) && frames[next].t <= t {
			next++
		}
		if cut(t) {
			continue
		}
		if next-1 != curIdx {
			curIdx = next - 1
			if cur, err = loadPNG(frames[curIdx].path); err != nil {
				return err
			}
		}
		p := pointerAt(tr, meta, t)
		key := fmt.Sprintf("%d|%v|%.0f|%.0f|%.0f", curIdx, p.visible, p.x, p.y, p.ring)
		if key == lastKey && len(shots) > 0 {
			// Nothing moved. The picture stays, up to a point.
			if still += step; still <= *hold {
				shots[len(shots)-1].delay += step
			}
			continue
		}
		lastKey, still = key, 0
		pic := image.NewRGBA(cur.Rect)
		copy(pic.Pix, cur.Pix)
		if p.visible {
			if p.ring > 0 {
				drawRing(pic, int(p.x), int(p.y), p.ring)
			}
			drawArrow(pic, int(p.x), int(p.y))
		}
		shots = append(shots, shot{shrink(pic, *scale), step})
	}
	shots[len(shots)-1].delay = *tail

	// One palette from the whole clip.
	hist := make([]int, 1<<15)
	for i, s := range shots {
		if i%3 != 0 && i != len(shots)-1 {
			continue
		}
		for j := 0; j < len(s.img.Pix); j += 4 {
			hist[key15(s.img.Pix[j], s.img.Pix[j+1], s.img.Pix[j+2])]++
		}
	}
	pal := medianCut(hist, 255)
	for len(pal) < 255 {
		pal = append(pal, color.RGBA{0, 0, 0, 255})
	}
	const clear = 255
	pal = append(pal, color.RGBA{0, 0, 0, 0})
	m := &mapper{pal: pal, cache: make([]int16, 1<<15)}
	for i := range m.cache {
		m.cache[i] = -1
	}

	b := shots[0].img.Rect
	w, h := b.Dx(), b.Dy()
	g := &gif.GIF{Config: image.Config{ColorModel: pal, Width: w, Height: h}}
	prev := make([]uint8, w*h)
	idx := make([]uint8, w*h)
	var total int64
	for n, s := range shots {
		for i := 0; i < w*h; i++ {
			idx[i] = m.index(s.img.Pix[i*4], s.img.Pix[i*4+1], s.img.Pix[i*4+2])
		}
		box := image.Rect(0, 0, w, h)
		if n > 0 {
			// Only the part that changed is stored; the rest shows through.
			minX, minY, maxX, maxY := w, h, -1, -1
			for y := 0; y < h; y++ {
				for x := 0; x < w; x++ {
					if idx[y*w+x] != prev[y*w+x] {
						minX, minY, maxX, maxY = min(minX, x), min(minY, y), max(maxX, x), max(maxY, y)
					}
				}
			}
			if maxX < 0 {
				g.Delay[len(g.Delay)-1] += int(s.delay / 10)
				total += s.delay
				continue
			}
			box = image.Rect(minX, minY, maxX+1, maxY+1)
		}
		pm := image.NewPaletted(box, pal)
		for y := box.Min.Y; y < box.Max.Y; y++ {
			for x := box.Min.X; x < box.Max.X; x++ {
				v := idx[y*w+x]
				if n > 0 && v == prev[y*w+x] {
					v = clear
				}
				pm.Pix[pm.PixOffset(x, y)] = v
			}
		}
		copy(prev, idx)
		g.Image = append(g.Image, pm)
		g.Delay = append(g.Delay, int(s.delay/10))
		g.Disposal = append(g.Disposal, gif.DisposalNone)
		total += s.delay

		if *dump != "" && (n%12 == 0 || n == len(shots)-1) {
			_ = os.MkdirAll(*dump, 0o755)
			if f, err := os.Create(filepath.Join(*dump, fmt.Sprintf("shot-%03d-%05dms.png", n, total))); err == nil {
				_ = png.Encode(f, s.img)
				f.Close()
			}
		}
	}

	f, err := os.Create(*out)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := gif.EncodeAll(f, g); err != nil {
		return err
	}
	st, _ := f.Stat()
	fmt.Printf("%s: %dx%d, %d frames from %d shots, %.1f s, %.2f MB\n", *out, w, h, len(g.Image), len(shots), float64(total)/1000, float64(st.Size())/1e6)
	return nil
}
