//go:build ignore

// printgen draws the reader's linocut prints into static/: the turned spindle
// from the app icon (internal/desktop/appicon_gen.go), laid on its side. Each
// print is two files, so the art can follow the theme:
//
//   - print-<name>-colour.png holds the yellow and red passes in their real
//     inks (both themes print them the same).
//   - print-<name>-key.png is the navy key block as an alpha mask; the page
//     fills it with --print-key, navy on paper by day and cream at night.
//
// Every pass has rough edges, mottled density, starved specks and a small
// misregistration, as in the icon. The animated print is a vertical strip of
// frames of the spindle turning, its carve marks swept round. Regenerate with
// `go generate ./internal/serve`; the output is deterministic.
package main

import (
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"math/rand"
	"os"
	"sync"
)

const ss = 3

type rgb struct{ r, g, b float64 }

func hx(v uint32) rgb {
	return rgb{float64(v>>16&0xFF) / 255, float64(v>>8&0xFF) / 255, float64(v&0xFF) / 255}
}

var (
	yellowC = hx(0xF5C33B)
	redC    = hx(0xD63B22)
)

func hash(x, y int, seed int) float64 {
	h := uint32(x*374761393 + y*668265263 + seed*1442695041)
	h = (h ^ (h >> 13)) * 1274126177
	h ^= h >> 16
	return float64(h&0xFFFFFF) / float64(0xFFFFFF)
}

func smooth(t float64) float64 { return t * t * (3 - 2*t) }

func noise(x, y float64, seed int) float64 {
	xi, yi := math.Floor(x), math.Floor(y)
	fx, fy := smooth(x-xi), smooth(y-yi)
	ix, iy := int(xi), int(yi)
	a := hash(ix, iy, seed)
	b := hash(ix+1, iy, seed)
	c := hash(ix, iy+1, seed)
	d := hash(ix+1, iy+1, seed)
	return a + (b-a)*fx + (c-a)*fy + (a-b-c+d)*fx*fy
}

func fbm(x, y float64, seed, oct int) float64 {
	v, amp, sum := 0.0, 1.0, 0.0
	for i := 0; i < oct; i++ {
		v += amp * noise(x, y, seed+i*17)
		sum += amp
		x, y = x*2.03+11.7, y*2.03+5.3
		amp *= 0.5
	}
	return v / sum
}

func inRRect(x, y, x0, y0, x1, y1, r float64) bool {
	if x < x0 || x > x1 || y < y0 || y > y1 {
		return false
	}
	cx := math.Max(x0+r, math.Min(x, x1-r))
	cy := math.Max(y0+r, math.Min(y, y1-r))
	return math.Hypot(x-cx, y-cy) <= r
}

func rough(x, y float64, seed int, amp float64) (float64, float64) {
	return x + amp*(fbm(x/9, y/9, seed, 3)-0.5)*2, y + amp*(fbm(x/9, y/9, seed+99, 3)-0.5)*2
}

// print is one composition: a carved block with the spindle lying across it.
// Lengths are in output pixels.
type print struct {
	name       string
	w, h       float64
	margin     float64 // block inset from the canvas edge
	corner     float64
	ax, bx, cy float64 // spindle axis runs from (ax,cy) to (bx,cy)
	scale      float64 // profile radius scale against the icon
	squash     float64 // spin-mark ellipses: y radius / x radius
	arcs       []arc
	frames     int
	outW       int  // output width in pixels; the design is drawn at w x h
	block      bool // false prints the spindle alone, with no block behind it
}

type arc struct{ r, a0, a1, w float64 }

func (p *print) length() float64 { return p.bx - p.ax }

func (p *print) radius(u float64) float64 {
	t := u / p.length()
	if t < 0.14 || t > 0.86 {
		return 0
	}
	g := func(c, w, e float64) float64 { d := math.Abs(t-c) / w; return math.Exp(-math.Pow(d, e)) }
	r := 34 + 36*g(0.25, 0.03, 2) - 8*g(0.31, 0.03, 2) + 70*g(0.55, 0.15, 2.6) + 24*g(0.775, 0.022, 2)
	if t < 0.19 || t > 0.82 {
		r = 32
	}
	return r * p.scale
}

func (p *print) inCentre(u, v float64) bool {
	lenC, half := 70.0*p.scale, 30.0*p.scale
	L := p.length()
	t0, t1 := 0.14*L, 0.86*L
	for _, c := range []struct{ tip, dir float64 }{{t0, -1}, {t1, 1}} {
		d := (u - c.tip) * c.dir
		if d < 0 || d > lenC+30*p.scale {
			continue
		}
		w := half
		if d < lenC {
			w = half * d / lenC
		}
		if math.Abs(v) <= w {
			return true
		}
	}
	return false
}

// gouges is how many carve marks ring the turning spindle; spokes is how
// many times the spin marks repeat round it. A turning print loops after the
// spin marks turn one spoke, which moves the gouges on by exactly one.
const gouges, spokes = 9, 6

func (p *print) makeArcs(seed int64) {
	rng := rand.New(rand.NewSource(seed))
	turn := 2 * math.Pi
	if p.frames > 1 {
		turn /= spokes
	}
	rMax := (p.w/2 - p.margin) * 1.25
	step := 22 * p.scale / 0.67
	for r := p.h * 0.32; r < rMax; r += step + rng.Float64()*step*0.4 {
		start := rng.Float64() * turn
		end := start + turn - 0.3
		if p.frames <= 1 {
			end = 3 * math.Pi
		}
		a := start
		for a < end {
			span := 0.25 + rng.Float64()*0.55*(p.h*0.5/r)
			if rng.Float64() < 0.8 {
				w := (7 + rng.Float64()*6) * p.scale / 0.67
				for k := 0.0; k < 2*math.Pi-1e-9; k += turn {
					p.arcs = append(p.arcs, arc{r + rng.Float64()*6 - 3, a + k, a + k + span, w})
				}
			}
			a += span + 0.12 + rng.Float64()*0.35
		}
	}
}

func angleIn(t, a0, a1 float64) (float64, bool) {
	for t < a0 {
		t += 2 * math.Pi
	}
	if t > a1 {
		return 0, false
	}
	return (t - a0) / (a1 - a0), true
}

// inArcs tests the spin marks, ellipses round the spindle, turned by phase.
func (p *print) inArcs(x, y, phase float64) bool {
	dx, dy := x-p.w/2, (y-p.cy)/p.squash
	r := math.Hypot(dx, dy)
	th := math.Atan2(dy, dx) - phase
	for _, a := range p.arcs {
		if math.Abs(r-a.r) > a.w {
			continue
		}
		if f, ok := angleIn(th, a.a0, a.a1); ok {
			w := a.w * math.Pow(math.Sin(math.Pi*f), 0.55)
			if math.Abs(r-a.r) <= w/2 {
				return true
			}
		}
	}
	return false
}

func (p *print) inBlock(x, y float64) bool {
	m := p.margin
	return inRRect(x, y, m, m, p.w-m, p.h-m, p.corner)
}

func (p *print) toSpindle(x, y float64) (u, v float64) { return x - p.ax, y - p.cy }

// shade is the key ink cut into the spindle's shadow side. A still print
// carries the icon's tapered rings; a turning one carries short gouges that
// ride round the work with the phase and thin as they turn away.
func (p *print) shade(x, y, u, v, r, phase float64) bool {
	s := v / r
	if p.frames <= 1 {
		pitch := 15.0 * p.scale
		i := math.Floor(u / pitch)
		d := math.Abs(u-(i+0.5)*pitch) + (fbm(x/6, y/6, 5, 2)-0.5)*1.6
		start := 0.3 + 0.35*hash(int(i), 3, 77)
		taper := math.Max(0, math.Min(1, (s-start)/0.4))
		return taper > 0 && d < 2.6*taper
	}
	pitch := 11.0 * p.scale / 0.67
	i := math.Floor(u / pitch)
	off := hash(int(i), 0, 31)
	lu := pitch * (0.3 + 0.35*hash(int(i), 0, 41))
	for k := 0; k < gouges; k++ {
		th := 2*math.Pi*(float64(k)+off)/gouges + phase*float64(spokes)/gouges
		c := math.Cos(th)
		if c <= 0.05 {
			continue
		}
		sv := math.Sin(th)
		if sv < -0.25 {
			continue
		}
		half := (1.3 + 2.6*c) * (0.4 + 0.6*math.Max(0, sv+0.25))
		if math.Abs(s-sv)*r > half {
			continue
		}
		if math.Abs(u-(i+0.5)*pitch) < lu {
			return true
		}
	}
	return false
}

func (p *print) key(x, y, phase float64) bool {
	x, y = rough(x, y, 1, 2.6)
	u, v := p.toSpindle(x, y)
	r := p.radius(u)
	if r > 0 && math.Abs(v) <= r {
		if r-math.Abs(v) < 5 {
			return true
		}
		return p.shade(x, y, u, v, r, phase)
	}
	if r > 0 && math.Abs(v) <= r+6 {
		return p.block
	}
	if !p.block {
		return false
	}
	if !p.inBlock(x, y) {
		return false
	}
	if p.inCentre(u, v) {
		return false
	}
	return !p.inArcs(x, y, phase)
}

func (p *print) yellow(x, y float64) bool {
	x, y = rough(x+4, y-3, 2, 2.2)
	u, v := p.toSpindle(x, y)
	r := p.radius(u)
	return r > 0 && math.Abs(v) <= r-2
}

func (p *print) red(x, y float64) bool {
	x, y = rough(x-3, y+4, 3, 2.2)
	u, v := p.toSpindle(x, y)
	r := p.radius(u)
	if r <= 0 || math.Abs(v) > r-3 {
		return false
	}
	t := u / p.length()
	return (t > 0.235 && t < 0.265) || (t > 0.53 && t < 0.565)
}

func dens(x, y float64, seed int) float64 {
	d := 0.94 + 0.06*fbm(x/70, y/70, seed, 3)
	if fbm(x/3.5, y/3.5, seed+300, 2) > 0.79 {
		d *= 0.35
	}
	return d
}

// levels is how many alpha steps a pass keeps; few steps keep the PNGs small
// and the specks still read.
const levels = 6

func quant(a float64) uint8 {
	q := math.Round(a * (levels - 1))
	return uint8(math.Max(0, math.Min(levels-1, q)))
}

func render(p *print) {
	W, fh := p.outW, int(math.Round(float64(p.outW)*p.h/p.w))
	H := fh * p.frames
	k := p.w / float64(W)
	keyPal := color.Palette{}
	for i := 0; i < levels; i++ {
		keyPal = append(keyPal, color.NRGBA{0, 0, 0, uint8(255 * i / (levels - 1))})
	}
	colPal := color.Palette{color.NRGBA{}}
	for _, c := range []rgb{yellowC, redC} {
		for i := 1; i < levels; i++ {
			colPal = append(colPal, color.NRGBA{uint8(c.r*255 + .5), uint8(c.g*255 + .5), uint8(c.b*255 + .5), uint8(255 * i / (levels - 1))})
		}
	}
	keyImg := image.NewPaletted(image.Rect(0, 0, W, H), keyPal)
	colImg := image.NewPaletted(image.Rect(0, 0, W, fh), colPal)
	var wg sync.WaitGroup
	for py := 0; py < H; py++ {
		wg.Add(1)
		go func(py int) {
			defer wg.Done()
			f := py / fh
			phase := 2 * math.Pi / spokes * float64(f) / float64(p.frames)
			ly := py % fh
			for px := 0; px < W; px++ {
				var ki, yl, rd float64
				for sy := 0; sy < ss; sy++ {
					for sx := 0; sx < ss; sx++ {
						x := (float64(px) + (float64(sx)+0.5)/ss) * k
						y := (float64(ly) + (float64(sy)+0.5)/ss) * k
						if p.key(x, y, phase) {
							ki++
						}
						if f == 0 {
							if p.red(x, y) {
								rd++
							} else if p.yellow(x, y) {
								yl++
							}
						}
					}
				}
				n := float64(ss * ss)
				x, y := float64(px)*k, float64(ly)*k
				keyImg.SetColorIndex(px, py, quant(ki/n*dens(x, y, 30)))
				if f == 0 {
					switch {
					case rd >= yl && rd > 0:
						if q := quant(rd / n * dens(x, y, 20)); q > 0 {
							colImg.SetColorIndex(px, py, levels-1+q)
						}
					case yl > 0:
						if q := quant(yl / n * dens(x, y, 10)); q > 0 {
							colImg.SetColorIndex(px, py, q)
						}
					}
				}
			}
		}(py)
	}
	wg.Wait()
	write("static/print-"+p.name+"-key.png", keyImg)
	write("static/print-"+p.name+"-colour.png", colImg)
}

func write(path string, img image.Image) {
	f, err := os.Create(path)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(f, img); err != nil {
		log.Fatal(err)
	}
}

func spindle(name string, outW, frames int) *print {
	p := &print{name: name, w: 720, h: 300, margin: 8, corner: 44, ax: 40, bx: 680, cy: 150, scale: 0.66, squash: 0.42, frames: frames, outW: outW, block: true}
	p.makeArcs(7)
	return p
}

// grain draws print-grain.png, a tiling alpha mask of starved ink: specks
// and thin patches knocked out of a solid pass. Pages lay it over a stamp or
// a wordmark so flat type prints like the blocks do.
func grain(size int) {
	pal := color.Palette{}
	for i := 0; i < levels; i++ {
		pal = append(pal, color.NRGBA{0, 0, 0, uint8(255 * i / (levels - 1))})
	}
	img := image.NewPaletted(image.Rect(0, 0, size, size), pal)
	period := float64(size)
	// wrap keeps the value noise periodic, so the tile has no seam.
	wrap := func(x, y, cell float64, seed int) float64 {
		n := int(period / cell)
		xi, yi := math.Floor(x/cell), math.Floor(y/cell)
		fx, fy := smooth(x/cell-xi), smooth(y/cell-yi)
		m := func(v int) int { return ((v % n) + n) % n }
		ix, iy := int(xi), int(yi)
		a := hash(m(ix), m(iy), seed)
		b := hash(m(ix+1), m(iy), seed)
		c := hash(m(ix), m(iy+1), seed)
		d := hash(m(ix+1), m(iy+1), seed)
		return a + (b-a)*fx + (c-a)*fy + (a-b-c+d)*fx*fy
	}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			fx, fy := float64(x)+0.5, float64(y)+0.5
			a := 1.0
			speck := 0.6*wrap(fx, fy, 4, 51) + 0.4*wrap(fx, fy, 2, 52)
			thin := wrap(fx, fy, 32, 53)
			switch {
			case speck > 0.84-0.06*thin:
				a = 0
			case speck > 0.78-0.06*thin:
				a = 0.6
			}
			img.SetColorIndex(x, y, quant(a))
		}
	}
	write("static/print-grain.png", img)
}

func main() {
	render(spindle("spindle", 720, 1))
	render(spindle("turning", 480, 8))
	grain(256)
}
