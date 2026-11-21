//go:build ignore

// mkicon draws internal/web/static/icon.svg (the Forge logo: a gold-rimmed
// navy hexagon holding a gold anvil under a teal spark) at several sizes
// and writes a PNG-compressed .ico plus a 256px PNG. The shapes below are
// the SVG's polygons and gradients, in its 32-unit viewBox; keep them in
// step. Regenerate after changing the logo, from cmd/draftsmith/winres:
//
//	go run mkicon.go icon.ico icon.png
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

type rgb struct{ r, g, b float64 }

func hex(c uint32) rgb {
	return rgb{float64(c >> 16 & 0xff), float64(c >> 8 & 0xff), float64(c & 0xff)}
}

// shape is a filled polygon, painted in order (later on top). With top and
// bottom set, its fill is a vertical gradient over its bounding box, like
// the SVG's objectBoundingBox gradients.
type shape struct {
	pts         [][2]float64
	top, bottom rgb
	gradient    bool
}

func poly(flat ...float64) [][2]float64 {
	var p [][2]float64
	for i := 0; i+1 < len(flat); i += 2 {
		p = append(p, [2]float64{flat[i], flat[i+1]})
	}
	return p
}

var shapes = []shape{
	{pts: poly(16, 0.5, 29.42, 8.25, 29.42, 23.75, 16, 31.5, 2.58, 23.75, 2.58, 8.25), top: hex(0xd6b46a)},
	{pts: poly(16, 2.4, 27.78, 9.2, 27.78, 22.8, 16, 29.6, 4.22, 22.8, 4.22, 9.2), top: hex(0x16243d), bottom: hex(0x060b14), gradient: true},
	{pts: poly(8, 13.4, 12, 12.6, 24, 12.6, 24, 15.6, 21.2, 16.2, 20, 19.2, 22.6, 21.2, 22.6, 23.2, 9.4, 23.2, 9.4, 21.2, 12, 19.2, 12, 16.2, 10.6, 15.4),
		top: hex(0xf3e2b0), bottom: hex(0xb38a3a), gradient: true},
	{pts: poly(16, 4.8, 16.9, 7.3, 19.4, 8.2, 16.9, 9.1, 16, 11.6, 15.1, 9.1, 12.6, 8.2, 15.1, 7.3), top: hex(0x4fe0ec)},
}

// inside is the even-odd point-in-polygon test.
func inside(pts [][2]float64, x, y float64) bool {
	in := false
	for i, j := 0, len(pts)-1; i < len(pts); j, i = i, i+1 {
		a, b := pts[i], pts[j]
		if (a[1] > y) != (b[1] > y) && x < (b[0]-a[0])*(y-a[1])/(b[1]-a[1])+a[0] {
			in = !in
		}
	}
	return in
}

func (s shape) color(y float64) rgb {
	if !s.gradient {
		return s.top
	}
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, p := range s.pts {
		lo, hi = math.Min(lo, p[1]), math.Max(hi, p[1])
	}
	t := (y - lo) / (hi - lo)
	return rgb{s.top.r + (s.bottom.r-s.top.r)*t, s.top.g + (s.bottom.g-s.top.g)*t, s.top.b + (s.bottom.b-s.top.b)*t}
}

func render(size int) *image.NRGBA {
	const ss = 16 // 16x16 subsamples per pixel
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	scale := 32.0 / float64(size)
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var sum rgb
			var cover float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					x := (float64(px) + (float64(sx)+0.5)/ss) * scale
					y := (float64(py) + (float64(sy)+0.5)/ss) * scale
					hit := -1
					for i, s := range shapes {
						if inside(s.pts, x, y) {
							hit = i
						}
					}
					if hit < 0 {
						continue
					}
					c := shapes[hit].color(y)
					sum.r, sum.g, sum.b = sum.r+c.r, sum.g+c.g, sum.b+c.b
					cover++
				}
			}
			if cover == 0 {
				continue
			}
			ch := func(v float64) uint8 { return uint8(math.Round(v / cover)) }
			img.SetNRGBA(px, py, color.NRGBA{ch(sum.r), ch(sum.g), ch(sum.b), uint8(math.Round(255 * cover / (ss * ss)))})
		}
	}
	return img
}

func main() {
	sizes := []int{16, 20, 24, 32, 40, 48, 64, 128, 256}
	var pngs [][]byte
	for _, s := range sizes {
		var b bytes.Buffer
		if err := png.Encode(&b, render(s)); err != nil {
			panic(err)
		}
		pngs = append(pngs, b.Bytes())
	}

	var ico bytes.Buffer
	binary.Write(&ico, binary.LittleEndian, [3]uint16{0, 1, uint16(len(sizes))})
	off := 6 + 16*len(sizes)
	for i, s := range sizes {
		dim := uint8(s)
		if s == 256 {
			dim = 0 // 0 means 256
		}
		ico.Write([]byte{dim, dim, 0, 0})
		binary.Write(&ico, binary.LittleEndian, [2]uint16{1, 32}) // planes, bpp
		binary.Write(&ico, binary.LittleEndian, [2]uint32{uint32(len(pngs[i])), uint32(off)})
		off += len(pngs[i])
	}
	for _, p := range pngs {
		ico.Write(p)
	}
	if err := os.WriteFile(os.Args[1], ico.Bytes(), 0o644); err != nil {
		panic(err)
	}
	if err := os.WriteFile(os.Args[2], pngs[len(pngs)-1], 0o644); err != nil {
		panic(err)
	}
}
