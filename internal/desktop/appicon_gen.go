//go:build ignore

// appicon_gen draws appicon.png, the 1024x1024 app mark: a turned spindle
// between a lathe's two centres, in the reader's design-system colours
// (internal/serve/styles.css). Regenerate with `go generate ./internal/desktop`.
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
	"os"
)

const (
	size  = 1024
	plate = 824 // rounded-square body
	inset = (size - plate) / 2
	round = 185.0 // corner radius
	ss    = 4     // supersamples per axis
)

var (
	navy   = color.NRGBA{0x1C, 0x21, 0x40, 0xFF} // --rule / --text (light)
	cream  = color.NRGBA{0xFF, 0xF4, 0xD6, 0xFF} // --cmd-text
	shade  = color.NRGBA{0xE9, 0xDF, 0xC6, 0xFF} // --border (light), the spindle's lower half
	red    = color.NRGBA{0xD6, 0x3B, 0x22, 0xFF} // --red
	yellow = color.NRGBA{0xF5, 0xC3, 0x3B, 0xFF} // --yellow
	teal   = color.NRGBA{0x6F, 0xD0, 0xC9, 0xFF} // --teal (dark)
)

// Spindle geometry in canvas pixels: it lies along the horizontal axis at
// cy, from x0 to x1.
const (
	cy = 512.0
	x0 = 262.0
	x1 = 762.0
)

// radius is the spindle's turned profile: a tenon at each end, a bead
// toward the headstock, a cove, and a vase-shaped belly.
func radius(x float64) float64 {
	t := (x - x0) / (x1 - x0) // 0..1
	if t < 0 || t > 1 {
		return 0
	}
	g := func(c, w float64) float64 { d := (t - c) / w; return math.Exp(-d * d) }
	r := 40.0 + 58*g(0.17, 0.07) + 124*g(0.64, 0.21)
	// Square shoulders at the tenons.
	if t < 0.04 || t > 0.97 {
		r = 32
	}
	return r
}

// band colours the turned rings: a yellow bead and a red ring on the belly.
func band(x float64) (color.NRGBA, bool) {
	t := (x - x0) / (x1 - x0)
	switch {
	case t > 0.135 && t < 0.205:
		return yellow, true
	case t > 0.605 && t < 0.675:
		return red, true
	}
	return color.NRGBA{}, false
}

// sample is the colour at canvas point (x, y), or transparent.
func sample(x, y float64) color.NRGBA {
	if !inPlate(x, y) {
		return color.NRGBA{}
	}
	// The centres: a teal cone at each end pointing into the work, and the
	// rest of the axis as a thin line.
	if c, ok := centres(x, y); ok {
		return c
	}
	if r := radius(x); r > 0 && math.Abs(y-cy) <= r {
		if c, ok := band(x); ok {
			if y > cy {
				return darken(c, 0.86)
			}
			return c
		}
		if y > cy {
			return shade
		}
		return cream
	}
	return navy
}

func centres(x, y float64) (color.NRGBA, bool) {
	const length, half = 62.0, 28.0
	dy := math.Abs(y - cy)
	// Headstock centre: base at x0-length, tip at x0.
	if x >= x0-length-40 && x <= x0 {
		if x < x0-length {
			if dy <= half {
				return teal, true
			}
			return color.NRGBA{}, false
		}
		if dy <= half*(x0-x)/length {
			return teal, true
		}
	}
	// Tailstock centre, mirrored.
	if x >= x1 && x <= x1+length+40 {
		if x > x1+length {
			if dy <= half {
				return teal, true
			}
			return color.NRGBA{}, false
		}
		if dy <= half*(x-x1)/length {
			return teal, true
		}
	}
	return color.NRGBA{}, false
}

func darken(c color.NRGBA, f float64) color.NRGBA {
	return color.NRGBA{uint8(float64(c.R) * f), uint8(float64(c.G) * f), uint8(float64(c.B) * f), c.A}
}

func inPlate(x, y float64) bool {
	lo, hi := float64(inset), float64(inset+plate)
	if x < lo || x > hi || y < lo || y > hi {
		return false
	}
	cx := math.Max(lo+round, math.Min(x, hi-round))
	cyy := math.Max(lo+round, math.Min(y, hi-round))
	return math.Hypot(x-cx, y-cyy) <= round
}

func main() {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var r, g, b, a float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					c := sample(float64(px)+(float64(sx)+0.5)/ss, float64(py)+(float64(sy)+0.5)/ss)
					fa := float64(c.A) / 255
					r += float64(c.R) * fa
					g += float64(c.G) * fa
					b += float64(c.B) * fa
					a += fa
				}
			}
			n := float64(ss * ss)
			if a == 0 {
				continue
			}
			img.SetNRGBA(px, py, color.NRGBA{
				R: uint8(r/a + 0.5), G: uint8(g/a + 0.5), B: uint8(b/a + 0.5),
				A: uint8(a/n*255 + 0.5),
			})
		}
	}
	f, err := os.Create("appicon.png")
	if err != nil {
		log.Fatal(err)
	}
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(f, img); err != nil {
		log.Fatal(err)
	}
	if err := f.Close(); err != nil {
		log.Fatal(err)
	}
}
