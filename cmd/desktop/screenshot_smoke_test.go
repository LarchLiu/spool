package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"spool/internal/screencapture"
)

func TestScreenshotSmokeSnapshotMatchesConnectedDisplays(t *testing.T) {
	displays := []screencapture.Display{
		{Index: 1, Bounds: screencapture.Rect{Width: 800, Height: 600}},
		{Index: 2, Bounds: screencapture.Rect{X: -640, Y: 80, Width: 640, Height: 480}},
		{Index: 3, Bounds: screencapture.Rect{X: 160, Y: -800, Width: 600, Height: 800}},
	}
	snapshot, err := screenshotSmokeSnapshot(displays)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Screens) != len(displays) || len(snapshot.Windows) != len(displays) {
		t.Fatalf("fixture must capture all displays and window targets: %d screens, %d windows", len(snapshot.Screens), len(snapshot.Windows))
	}
	for i, display := range displays {
		screen, window := snapshot.Screens[i], snapshot.Windows[i]
		if screen.Bounds != display.Bounds || screen.PixelSize() != image.Pt(display.Bounds.Width, display.Bounds.Height) {
			t.Fatalf("display %d lost its position, size or capture order: %+v", i+1, screen.Bounds)
		}
		config, err := png.DecodeConfig(bytes.NewReader(screen.PNG))
		if err != nil || config.Width != display.Bounds.Width || config.Height != display.Bounds.Height {
			t.Fatalf("display %d has incorrect PNG dimensions: %+v, %v", i+1, config, err)
		}
		if window.ID == 0 || window.Bounds.Image().Intersect(display.Bounds.Image()) != window.Bounds.Image() {
			t.Fatalf("display %d window target must stay on its own display: %+v", i+1, window)
		}
		data, err := snapshot.Crop(window.Bounds)
		if err != nil {
			t.Fatalf("display %d window capture: %v", i+1, err)
		}
		crop, err := png.Decode(bytes.NewReader(data))
		if err != nil || crop.Bounds().Size() != image.Pt(500, 350) {
			t.Fatalf("display %d window PNG: %v", i+1, err)
		}
		if got := color.NRGBAModel.Convert(crop.At(5, 5)); got != (color.NRGBA{R: 40, G: 70, B: 100 + uint8(i), A: 255}) {
			t.Fatalf("display %d window used another display's pixels: %v", i+1, got)
		}
	}
}
