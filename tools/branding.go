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

func readLogo(path string) image.Image {
	source, err := os.Open(path)
	if err != nil {
		log.Fatal(err)
	}
	logo, err := png.Decode(source)
	source.Close()
	if err != nil {
		log.Fatal(err)
	}
	return logo
}

func main() {
	logo := readLogo("assets/logo.png")
	relayLogo := readLogo("assets/relay-logo.png")
	for _, asset := range []struct {
		path     string
		size     int
		maskable bool
		source   image.Image
	}{
		{"web/public/icon-192.png", 192, false, logo},
		{"web/public/icon-512.png", 512, false, logo},
		{"web/public/icon-maskable-512.png", 512, true, logo},
		{"web/public/apple-touch-icon.png", 180, true, logo},
		{"web/public/favicon.png", 32, false, logo},
		{"web/public/logo.png", 128, false, logo},
		{"relay/adminui/logo.png", 128, false, relayLogo},
		{"assets/icon.png", 1024, false, logo},
		{"android/app/src/main/res/drawable-nodpi/spool_launcher_logo.png", 512, false, logo},
		{"tray.png", 44, false, logo},
	} {
		img := image.NewNRGBA(image.Rect(0, 0, asset.size, asset.size))
		target := img.Bounds()
		if asset.maskable {
			draw.Draw(img, target, image.NewUniform(color.NRGBA{R: 247, G: 247, B: 242, A: 255}), image.Point{}, draw.Src)
			// Keep the entire logo inside the maskable icon's circular safe area.
			target = target.Inset((asset.size*22 + 50) / 100)
		}
		xdraw.CatmullRom.Scale(img, target, asset.source, asset.source.Bounds(), draw.Over, nil)
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
