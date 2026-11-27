package screencapture

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestCaptureReturnsOnlySelectedPNGAndCleansOutput(t *testing.T) {
	if !Supported() {
		t.Skip("native screenshot capture is unavailable on this platform")
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 4, 3))); err != nil {
		t.Fatal(err)
	}
	var output string
	data, err := capture(context.Background(), func(_ context.Context, path string) error {
		output = path
		info, err := os.Stat(filepath.Dir(path))
		if err != nil || !info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o700) {
			t.Fatalf("screenshot directory must be private: %v, %v", info, err)
		}
		return os.WriteFile(path, encoded.Bytes(), 0o600)
	}, 4*1024*1024)
	if err != nil || !bytes.Equal(data, encoded.Bytes()) {
		t.Fatalf("selected PNG was not preserved: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(output)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("screenshot output was not cleaned: %v", err)
	}
}

func TestCaptureCancellationFailuresAndLimitsCleanOutput(t *testing.T) {
	if !Supported() {
		t.Skip("native screenshot capture is unavailable on this platform")
	}
	for _, tc := range []struct {
		name string
		data []byte
		err  error
		want error
	}{
		{name: "missing image", want: ErrFailed},
		{name: "launch failure", err: errors.New("could not start"), want: ErrFailed},
		{name: "invalid image", data: []byte("not a PNG"), want: ErrFailed},
		{name: "empty image", data: []byte{}, want: ErrFailed},
		{name: "too large", data: make([]byte, 4*1024*1024+1), want: ErrTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output string
			data, err := capture(context.Background(), func(_ context.Context, path string) error {
				output = path
				if tc.data != nil {
					if err := os.WriteFile(path, tc.data, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				return tc.err
			}, 4*1024*1024)
			if !errors.Is(err, tc.want) || data != nil {
				t.Fatalf("capture returned %d bytes, %v; want %v", len(data), err, tc.want)
			}
			if _, err := os.Stat(filepath.Dir(output)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("screenshot output was not cleaned: %v", err)
			}
		})
	}
}

func TestCaptureContextCancellationWaitsForPickerBeforeCleanup(t *testing.T) {
	if !Supported() {
		t.Skip("native screenshot capture is unavailable on this platform")
	}
	ctx, cancel := context.WithCancel(context.Background())
	var output string
	data, err := capture(ctx, func(ctx context.Context, path string) error {
		output = path
		cancel()
		<-ctx.Done()
		if _, err := os.Stat(filepath.Dir(path)); err != nil {
			t.Fatalf("files removed before the picker stopped: %v", err)
		}
		return ctx.Err()
	}, 4*1024*1024)
	if data != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled capture = %v, %v", data, err)
	}
	if _, err := os.Stat(filepath.Dir(output)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("screenshot output was not cleaned: %v", err)
	}
}

func TestCropPreservesPixelsAtRetinaScaleAndNegativeOrigin(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 800, 600))
	for y := 0; y < 600; y++ {
		for x := 0; x < 800; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 77, A: 255})
		}
	}
	snapshot := &Snapshot{Screens: []Screen{{Bounds: Rect{X: -400, Y: 100, Width: 400, Height: 300}, Image: img}}}
	data, err := snapshot.Crop(Rect{X: -350, Y: 140, Width: 100, Height: 60})
	if err != nil {
		t.Fatal(err)
	}
	out, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if out.Bounds().Dx() != 200 || out.Bounds().Dy() != 120 {
		t.Fatalf("incorrect Retina crop dimensions: %v", out.Bounds())
	}
	for _, point := range []image.Point{{0, 0}, {150, 50}, {199, 119}} {
		if color.NRGBAModel.Convert(out.At(point.X, point.Y)) != color.NRGBAModel.Convert(img.At(point.X+100, point.Y+80)) {
			t.Fatalf("crop pixel offset at %v", point)
		}
	}
	for _, rect := range []Rect{{X: -401, Y: 100, Width: 5, Height: 5}, {X: -200, Y: 140, Width: 0, Height: 1}, {X: 0, Y: 0, Width: 1, Height: 1}} {
		if _, err := snapshot.Crop(rect); err == nil {
			t.Fatalf("accepted out-of-display crop: %+v", rect)
		}
	}
}

func TestCropComposesMixedScaleDisplays(t *testing.T) {
	left := image.NewNRGBA(image.Rect(0, 0, 100, 100))
	right := image.NewNRGBA(image.Rect(0, 0, 200, 200))
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			left.Set(x, y, color.NRGBA{R: 255, A: 255})
		}
	}
	for y := 0; y < 200; y++ {
		for x := 0; x < 200; x++ {
			right.Set(x, y, color.NRGBA{B: 255, A: 255})
		}
	}
	snapshot := &Snapshot{Screens: []Screen{
		{Bounds: Rect{X: -100, Y: 0, Width: 100, Height: 100}, Image: left},
		{Bounds: Rect{X: 0, Y: 0, Width: 100, Height: 100}, Image: right},
	}}
	data, err := snapshot.Crop(Rect{X: -50, Y: 25, Width: 100, Height: 50})
	if err != nil {
		t.Fatal(err)
	}
	out, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if out.Bounds().Dx() != 200 || out.Bounds().Dy() != 100 {
		t.Fatalf("wrong composite dimensions: %v", out.Bounds())
	}
	if color.NRGBAModel.Convert(out.At(25, 50)) != (color.NRGBA{R: 255, A: 255}) || color.NRGBAModel.Convert(out.At(175, 50)) != (color.NRGBA{B: 255, A: 255}) {
		t.Fatal("display halves were not composed in order")
	}
}

func TestCapturedPNGMetadataAndDeferredCropPreservePixels(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 80, 60))
	img.SetNRGBA(20, 16, color.NRGBA{R: 70, G: 90, B: 110, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	screen, err := screenFromPNG(Rect{X: -40, Y: 20, Width: 40, Height: 30}, encoded.Bytes())
	if err != nil || screen.PixelSize() != image.Pt(80, 60) || screen.Image != nil {
		t.Fatalf("captured screen must retain source PNG and pixel dimensions without decoding: %+v, %v", screen.PixelSize(), err)
	}
	data, err := (&Snapshot{Screens: []Screen{screen}}).Crop(Rect{X: -30, Y: 28, Width: 10, Height: 10})
	if err != nil {
		t.Fatal(err)
	}
	out, err := png.Decode(bytes.NewReader(data))
	if err != nil || out.Bounds().Size() != image.Pt(20, 20) || color.NRGBAModel.Convert(out.At(0, 0)) != img.NRGBAAt(20, 16) {
		t.Fatalf("deferred PNG decode changed crop dimensions or pixels: %v", err)
	}
}

func TestDisplayCapturesOverlapAndKeepDisplayOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	displays := []Display{{Index: 1, Bounds: Rect{X: -100, Width: 100, Height: 100}}, {Index: 2, Bounds: Rect{Width: 200, Height: 100}}}
	started := make(chan int, len(displays))
	release := make(chan struct{}, len(displays))
	t.Cleanup(func() { close(release) })
	finished := make(chan *Snapshot, 1)
	errors := make(chan error, 1)
	go func() {
		snapshot, err := captureDisplays(ctx, displays, []Window{{ID: 7}}, func(ctx context.Context, display Display) (Screen, error) {
			started <- display.Index
			select {
			case <-release:
			case <-ctx.Done():
				return Screen{}, ctx.Err()
			}
			return Screen{Bounds: display.Bounds, Size: image.Pt(display.Index*100, 100)}, nil
		})
		finished <- snapshot
		errors <- err
	}()
	for range displays {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("display captures did not start together")
		}
	}
	// Release both workers only after observing that both started.
	for range displays {
		release <- struct{}{}
	}
	snapshot := <-finished
	if err := <-errors; err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Windows) != 1 || snapshot.Screens[0].Bounds != displays[0].Bounds || snapshot.Screens[1].Bounds != displays[1].Bounds {
		t.Fatalf("concurrent capture reordered displays: %+v", snapshot)
	}
}

func TestDisplayCaptureFailureCancelsAndJoinsOtherCaptures(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started := make(chan struct{})
	stopped := make(chan struct{})
	want := errors.New("display unavailable")
	snapshot, err := captureDisplays(ctx, []Display{{Index: 1}, {Index: 2}}, nil, func(ctx context.Context, display Display) (Screen, error) {
		if display.Index == 1 {
			<-started
			return Screen{}, want
		}
		close(started)
		<-ctx.Done()
		close(stopped)
		return Screen{}, ctx.Err()
	})
	if snapshot != nil || !errors.Is(err, want) {
		t.Fatalf("failed display capture: %v, %v", snapshot, err)
	}
	select {
	case <-stopped:
	default:
		t.Fatal("returned before other captures stopped")
	}
}

func BenchmarkScreenPreparation(b *testing.B) {
	img := image.NewNRGBA(image.Rect(0, 0, 3840, 2160))
	for y := 0; y < 2160; y++ {
		for x := 0; x < 3840; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x / 4), G: uint8(y / 4), B: uint8((x + y) / 8), A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		b.Fatal(err)
	}
	for _, decode := range []bool{true, false} {
		name := "metadata"
		if decode {
			name = "full_decode"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if decode {
					if _, err := png.Decode(bytes.NewReader(encoded.Bytes())); err != nil {
						b.Fatal(err)
					}
				} else if _, err := screenFromPNG(Rect{Width: 1920, Height: 1080}, encoded.Bytes()); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
