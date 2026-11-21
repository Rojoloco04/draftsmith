//go:build ignore

// mkicon draws internal/web/static/icon.svg (a 32x32 rounded square with a
// stroked "A") at several sizes and writes a PNG-compressed .ico plus a
// 256px PNG. Regenerate after changing the logo, from cmd/draftsmith/winres:
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

// The SVG, in its 32-unit viewBox.
const (
	rx     = 7.0
	halfSW = 1.5 // stroke-width 3
)

var bg = color.NRGBA{0x53, 0x83, 0xe8, 0xff}

// Path segments: M9 23 L16 7 L23 23 M12 17 H20 (round caps and joins, so
// the stroke is exactly the union of capsules around each segment).
var segs = [][4]float64{{9, 23, 16, 7}, {16, 7, 23, 23}, {12, 17, 20, 17}}

func inRoundRect(x, y float64) bool {
	if x < 0 || y < 0 || x > 32 || y > 32 {
		return false
	}
	cx := math.Max(rx, math.Min(32-rx, x))
	cy := math.Max(rx, math.Min(32-rx, y))
	return (x-cx)*(x-cx)+(y-cy)*(y-cy) <= rx*rx
}

func inStroke(x, y float64) bool {
	for _, s := range segs {
		dx, dy := s[2]-s[0], s[3]-s[1]
		t := ((x-s[0])*dx + (y-s[1])*dy) / (dx*dx + dy*dy)
		t = math.Max(0, math.Min(1, t))
		px, py := s[0]+t*dx-x, s[1]+t*dy-y
		if px*px+py*py <= halfSW*halfSW {
			return true
		}
	}
	return false
}

func render(size int) *image.NRGBA {
	const ss = 16 // 16x16 subsamples per pixel
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	scale := 32.0 / float64(size)
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var cover, white float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					x := (float64(px) + (float64(sx)+0.5)/ss) * scale
					y := (float64(py) + (float64(sy)+0.5)/ss) * scale
					if inRoundRect(x, y) {
						cover++
						if inStroke(x, y) {
							white++
						}
					}
				}
			}
			if cover == 0 {
				continue
			}
			// Colour of the covered part: blue mixed with white, then
			// coverage as alpha.
			w := white / cover
			mix := func(c uint8) uint8 { return uint8(math.Round(float64(c)*(1-w) + 255*w)) }
			img.SetNRGBA(px, py, color.NRGBA{mix(bg.R), mix(bg.G), mix(bg.B), uint8(math.Round(255 * cover / (ss * ss)))})
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
