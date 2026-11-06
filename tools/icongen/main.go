// icongen draws the 1024x1024 app icon: a white shield with a keyhole on a
// teal-to-navy macOS-style rounded square. Usage: go run ./tools/icongen out.png
package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

const (
	size     = 1024
	samples  = 3
	margin   = 100.0
	radius   = 185.0
	shieldCX = 512.0
	shieldT  = 225.0 // top of the shield
	shieldB  = 815.0 // tip of the shield
	shieldW  = 235.0 // half width
)

type rgba struct{ r, g, b, a float64 }

func inRoundedSquare(x, y float64) bool {
	lo, hi := margin, size-margin
	dx := math.Max(math.Max(lo+radius-x, x-(hi-radius)), 0)
	dy := math.Max(math.Max(lo+radius-y, y-(hi-radius)), 0)
	return x >= lo && x <= hi && y >= lo && y <= hi && dx*dx+dy*dy <= radius*radius
}

func inShield(x, y float64) bool {
	h := shieldB - shieldT
	u := (x - shieldCX) / shieldW
	v := (y - shieldT) / h
	if math.Abs(u) > 1 || v > 1 {
		return false
	}
	// Top edge dips from the centre peak towards the shoulders.
	if v < 0.09*(1-(1-math.Abs(u))*(1-math.Abs(u))) {
		return false
	}
	if v < 0.38 {
		return true
	}
	t := (v - 0.38) / 0.62
	return math.Abs(u) <= 1-t*t
}

func inKeyhole(x, y float64) bool {
	cy := 455.0
	if math.Hypot(x-shieldCX, y-cy) <= 62 {
		return true
	}
	if y < cy || y > 640 {
		return false
	}
	return math.Abs(x-shieldCX) <= 26+(y-cy)*0.14
}

func background(y float64) rgba {
	t := (y - margin) / (size - 2*margin)
	return rgba{0.06 + t*(0.05-0.06), 0.58 + t*(0.20-0.58), 0.62 + t*(0.42-0.62), 1}
}

func sample(x, y float64) rgba {
	if !inRoundedSquare(x, y) {
		return rgba{}
	}
	if inShield(x, y) && !inKeyhole(x, y) {
		return rgba{1, 1, 1, 1}
	}
	return background(y)
}

func main() {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var sum rgba
			for sy := 0; sy < samples; sy++ {
				for sx := 0; sx < samples; sx++ {
					c := sample(float64(px)+(float64(sx)+.5)/samples, float64(py)+(float64(sy)+.5)/samples)
					sum.r += c.r * c.a
					sum.g += c.g * c.a
					sum.b += c.b * c.a
					sum.a += c.a
				}
			}
			if sum.a == 0 {
				continue
			}
			img.SetNRGBA(px, py, color.NRGBA{
				uint8(sum.r / sum.a * 255), uint8(sum.g / sum.a * 255),
				uint8(sum.b / sum.a * 255), uint8(sum.a / samples / samples * 255),
			})
		}
	}
	f, err := os.Create(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}
