//go:build ignore

// Run from the Go project root: go run ./tools/branding.go.
package main

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"os"
	"path/filepath"

	xdraw "golang.org/x/image/draw"
)

func main() {
	source, err := os.Open("assets/logo.png")
	if err != nil {
		log.Fatal(err)
	}
	logo, err := png.Decode(source)
	source.Close()
	if err != nil {
		log.Fatal(err)
	}
	for _, asset := range []struct {
		path     string
		size     int
		maskable bool
	}{
		{"web/public/icon-192.png", 192, false},
		{"web/public/icon-512.png", 512, false},
		{"web/public/icon-maskable-512.png", 512, true},
		{"web/public/apple-touch-icon.png", 180, true},
		{"web/public/favicon.png", 32, false},
		{"web/public/logo.png", 128, false},
		{"assets/icon.png", 1024, false},
		{"tray.png", 44, false},
	} {
		img := image.NewNRGBA(image.Rect(0, 0, asset.size, asset.size))
		target := img.Bounds()
		if asset.maskable {
			draw.Draw(img, target, image.NewUniform(color.NRGBA{R: 247, G: 247, B: 242, A: 255}), image.Point{}, draw.Src)
			// Keep the entire logo inside the maskable icon's circular safe area.
			target = target.Inset((asset.size*22 + 50) / 100)
		}
		xdraw.CatmullRom.Scale(img, target, logo, logo.Bounds(), draw.Over, nil)
		if err := os.MkdirAll(filepath.Dir(asset.path), 0755); err != nil {
			log.Fatal(err)
		}
		file, err := os.Create(asset.path)
		if err != nil {
			log.Fatal(err)
		}
		err = png.Encode(file, img)
		closeErr := file.Close()
		if err != nil {
			log.Fatal(err)
		}
		if closeErr != nil {
			log.Fatal(closeErr)
		}
	}
}
