//go:build ignore

// appicon_gen draws appicon.png, the 1024x1024 app mark: a two-colour
// linocut of a turned spindle held between a lathe's centres, on a navy block
// carved with spin marks. The inks are the reader's colours
// (internal/serve/styles.css). Each pass is printed with rough edges, mottled
// density and a small misregistration, multiplied onto grained paper, so the
// mark reads as a hand print rather than vector art. Regenerate with
// `go generate -tags desktop ./internal/desktop`; the output is deterministic.
//
// The plate follows the macOS icon grid (an 824px rounded square centred on
// the 1024px canvas, transparent outside), so the same file serves the .icns
// and the Linux/Windows window icon.
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

const (
	size  = 1024
	plate = 824
	inset = (size - plate) / 2
	round = 185.0
	ss    = 3
)

type rgb struct{ r, g, b float64 }

func hx(v uint32) rgb {
	return rgb{float64(v>>16&0xFF) / 255, float64(v>>8&0xFF) / 255, float64(v&0xFF) / 255}
}

var (
	paperC  = hx(0xF4ECD6)
	navyC   = hx(0x1C2140)
	yellowC = hx(0xF5C33B)
	redC    = hx(0xD63B22)
)

// hash, noise and fbm are seeded value noise for paper grain, ink mottle and
// edge wobble.
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

func inPlate(x, y float64) bool {
	return inRRect(x, y, inset, inset, inset+plate, inset+plate, round)
}

// The spindle lies on the diagonal from (ax, ay) to (bx, by); u runs along
// it and v across it.
var (
	ax, ay = 196.0, 828.0
	bx, by = 828.0, 196.0
	length = math.Hypot(bx-ax, by-ay)
	ux, uy = (bx - ax) / length, (by - ay) / length
	vx, vy = -uy, ux // v > 0 is the lower-right, shadow side
)

func toSpindle(x, y float64) (u, v float64) {
	dx, dy := x-ax, y-ay
	return dx*ux + dy*uy, dx*vx + dy*vy
}

// radius of the turned profile at axial distance u (px).
func radius(u float64) float64 {
	t := u / length
	if t < 0.14 || t > 0.86 {
		return 0
	}
	g := func(c, w, p float64) float64 { d := math.Abs(t-c) / w; return math.Exp(-math.Pow(d, p)) }
	r := 34 + 36*g(0.25, 0.03, 2) - 8*g(0.31, 0.03, 2) + 70*g(0.55, 0.15, 2.6) + 24*g(0.775, 0.022, 2)
	if t < 0.19 || t > 0.82 {
		r = 32
	}
	return r
}

// inCentre is true on the two lathe centres: cones whose tips press into the
// ends of the work.
func inCentre(u, v float64) bool {
	const lenC, half = 70.0, 30.0
	t0, t1 := 0.14*length, 0.86*length
	for _, c := range []struct{ tip, dir float64 }{{t0, -1}, {t1, 1}} {
		d := (u - c.tip) * c.dir
		if d < 0 || d > lenC+30 {
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

type arc struct{ r, a0, a1, w float64 }

var arcs []arc

const cxS, cyS = 512.0, 512.0

func init() {
	rng := rand.New(rand.NewSource(7))
	for r := 230.0; r < 560; r += 26 + rng.Float64()*10 {
		a := rng.Float64() * 2 * math.Pi
		for a < 2*math.Pi*1.5 {
			span := 0.25 + rng.Float64()*0.55*(260/r)
			if rng.Float64() < 0.8 {
				arcs = append(arcs, arc{r + rng.Float64()*6 - 3, a, a + span, 9 + rng.Float64()*8})
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

func inArcs(x, y float64) bool {
	dx, dy := x-cxS, y-cyS
	r := math.Hypot(dx, dy)
	th := math.Atan2(dy, dx)
	for _, a := range arcs {
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

func block(x, y float64) bool {
	m := 50.0
	return inRRect(x, y, inset+m, inset+m, inset+plate-m, inset+plate-m, round-m)
}

// rough returns displaced coordinates for an ink pass, so its edges look
// pressed rather than cut by a machine.
func rough(x, y float64, seed int, amp float64) (float64, float64) {
	return x + amp*(fbm(x/9, y/9, seed, 3)-0.5)*2, y + amp*(fbm(x/9, y/9, seed+99, 3)-0.5)*2
}

func navyInk(x, y float64) bool {
	x, y = rough(x, y, 1, 3.2)
	if !block(x, y) {
		return false
	}
	u, v := toSpindle(x, y)
	r := radius(u)
	if r > 0 && math.Abs(v) <= r {
		if r-math.Abs(v) < 8 {
			return true
		}
		s := v / r
		const pitch = 14.0
		i := math.Floor(u / pitch)
		d := math.Abs(u-(i+0.5)*pitch) + (fbm(x/6, y/6, 5, 2)-0.5)*2.2
		start := 0.18 + 0.3*hash(int(i), 3, 77)
		taper := math.Max(0, math.Min(1, (s-start)/0.45))
		return d < 4.6*taper
	}
	if r > 0 && math.Abs(v) <= r+10 {
		return true
	}
	if inCentre(u, v) {
		return false
	}
	return !inArcs(x, y)
}

func yellowInk(x, y float64) bool {
	x, y = rough(x+6, y-5, 2, 2.6)
	if !block(x, y) {
		return false
	}
	u, v := toSpindle(x, y)
	r := radius(u)
	if r > 0 && math.Abs(v) <= r-3 {
		return true
	}
	return false
}

func redInk(x, y float64) bool {
	x, y = rough(x-5, y+6, 3, 2.6)
	u, v := toSpindle(x, y)
	r := radius(u)
	if r <= 0 || math.Abs(v) > r-4 {
		return false
	}
	t := u / length
	return (t > 0.235 && t < 0.265) || (t > 0.53 && t < 0.565)
}

func mul(a rgb, ink rgb, c float64) rgb {
	return rgb{a.r * (1 - c + c*ink.r), a.g * (1 - c + c*ink.g), a.b * (1 - c + c*ink.b)}
}

func sample(x, y float64) (rgb, bool) {
	if !inPlate(x, y) {
		return rgb{}, false
	}
	grain := (fbm(x/1.6, y/5, 40, 2) - 0.5) * 0.05
	p := rgb{paperC.r + grain, paperC.g + grain, paperC.b + grain*1.2}
	// ink density: mottled, with sparse starved specks
	dens := func(seed int) float64 {
		d := 0.9 + 0.1*fbm(x/70, y/70, seed, 3)
		if fbm(x/3.5, y/3.5, seed+300, 2) > 0.79 {
			d *= 0.35
		}
		return d
	}
	if yellowInk(x, y) {
		p = mul(p, yellowC, dens(10))
	}
	if redInk(x, y) {
		p = mul(p, redC, dens(20)*0.95)
	}
	if navyInk(x, y) {
		p = mul(p, navyC, dens(30))
	}
	return p, true
}

func main() {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	var wg sync.WaitGroup
	for py := 0; py < size; py++ {
		wg.Add(1)
		go func(py int) {
			defer wg.Done()
			for px := 0; px < size; px++ {
				var r, g, b, a float64
				for sy := 0; sy < ss; sy++ {
					for sx := 0; sx < ss; sx++ {
						c, ok := sample(float64(px)+(float64(sx)+0.5)/ss, float64(py)+(float64(sy)+0.5)/ss)
						if !ok {
							continue
						}
						r += c.r
						g += c.g
						b += c.b
						a++
					}
				}
				if a == 0 {
					continue
				}
				cl := func(v float64) uint8 { return uint8(math.Max(0, math.Min(255, v/a*255+0.5))) }
				img.SetNRGBA(px, py, color.NRGBA{cl(r), cl(g), cl(b), uint8(a / (ss * ss) * 255)})
			}
		}(py)
	}
	wg.Wait()
	f, err := os.Create("appicon.png")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(f, img); err != nil {
		log.Fatal(err)
	}
}
