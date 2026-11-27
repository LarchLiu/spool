package screencapture

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNativeDesktopPixelFormats(t *testing.T) {
	for _, tc := range []struct {
		name             string
		data             []byte
		stride, bpp      int
		little, bottomUp bool
		masks            [3]uint32
	}{
		{"GDI top down BGRA", []byte{0, 0, 255, 0, 0, 255, 0, 0}, 4, 32, true, false, [3]uint32{0xff0000, 0xff00, 0xff}},
		{"GDI bottom up BGRA", []byte{0, 255, 0, 0, 0, 0, 255, 0}, 4, 32, true, true, [3]uint32{0xff0000, 0xff00, 0xff}},
		{"X11 padded BGR", []byte{0, 0, 255, 99, 0, 255, 0, 99}, 4, 24, true, false, [3]uint32{0xff0000, 0xff00, 0xff}},
		{"X11 big endian RGB", []byte{255, 0, 0, 99, 0, 255, 0, 99}, 4, 24, false, false, [3]uint32{0xff0000, 0xff00, 0xff}},
		{"X11 RGB565", []byte{0, 248, 224, 7}, 2, 16, true, false, [3]uint32{0xf800, 0x7e0, 0x1f}},
		{"X11 10bit RGB", []byte{0, 0, 240, 63, 0, 252, 15, 0}, 4, 32, true, false, [3]uint32{0x3ff00000, 0xffc00, 0x3ff}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			img, err := nativePixels(tc.data, 1, 2, tc.stride, tc.bpp, tc.little, tc.bottomUp, tc.masks)
			if err != nil {
				t.Fatal(err)
			}
			if img.NRGBAAt(0, 0) != (color.NRGBA{R: 255, A: 255}) || img.NRGBAAt(0, 1) != (color.NRGBA{G: 255, A: 255}) {
				t.Fatalf("native colors/opacity changed: %v %v", img.NRGBAAt(0, 0), img.NRGBAAt(0, 1))
			}
		})
	}
	for _, tc := range []struct {
		width, height, stride, bpp int
		data                       []byte
	}{
		{0, 1, 4, 32, make([]byte, 4)}, {1, 1, 3, 32, make([]byte, 4)}, {1, 2, 4, 32, make([]byte, 4)}, {1, 1, 4, 8, make([]byte, 4)}, {100001, 100000, 400004, 32, nil},
	} {
		if _, err := nativePixels(tc.data, tc.width, tc.height, tc.stride, tc.bpp, true, false, [3]uint32{0xff0000, 0xff00, 0xff}); err == nil {
			t.Fatal("accepted invalid native pixel buffer")
		}
	}
}

func TestNativeWindowsMappedPerMonitorDPIAndZOrder(t *testing.T) {
	windows := []Window{{1, "front", Rect{-1600, 200, 1800, 600}}, {2, "back", Rect{100, 100, 500, 400}}, {3, "other", Rect{3000, 0, 100, 100}}}
	displays := []Display{
		{Bounds: Rect{-1280, 0, 1280, 720}, Pixels: Rect{-1920, 0, 1920, 1080}},
		{Bounds: Rect{0, 0, 1280, 720}, Pixels: Rect{0, 0, 2560, 1440}},
	}
	left, right := windowsForDisplay(windows, displays[0]), windowsForDisplay(windows, displays[1])
	if len(left) != 1 || left[0].ID != 1 || left[0].Bounds != (Rect{-1067, 133, 1067, 400}) {
		t.Fatalf("negative-origin monitor mapping: %+v", left)
	}
	if len(right) != 2 || right[0].ID != 1 || right[1].ID != 2 || right[0].Bounds != (Rect{0, 100, 100, 300}) || right[1].Bounds != (Rect{50, 50, 250, 200}) {
		t.Fatalf("mixed-DPI clipping/order: %+v", right)
	}
}

func TestSystemPickedEditorKeepsExactPixelsAndRoomForTools(t *testing.T) {
	for _, size := range []image.Point{{1, 1}, {53, 99}, {3840, 2160}, {100, 3000}} {
		img := image.NewNRGBA(image.Rect(0, 0, size.X, size.Y))
		img.SetNRGBA(0, 0, color.NRGBA{R: 17, G: 91, B: 203, A: 255})
		var data bytes.Buffer
		if err := png.Encode(&data, img); err != nil {
			t.Fatal(err)
		}
		snapshot, err := pickedSnapshot(context.Background(), data.Bytes(), []Display{{Bounds: Rect{-1920, 100, 1920, 1080}}})
		if err != nil {
			t.Fatal(err)
		}
		s := snapshot.Screens[0]
		if !snapshot.Picked || s.Bounds.Width < 1 || s.Bounds.Height < 1 || s.Bounds.Width > 1840 || s.Bounds.Height > 1000 || s.Selection == nil {
			t.Fatalf("invalid system-picked editor: %+v", s)
		}
		sx, sy := float64(s.Size.X)/float64(s.Bounds.Width), float64(s.Size.Y)/float64(s.Bounds.Height)
		r := s.Selection
		left, top := int(math.Round(r.X*sx)), int(math.Round(r.Y*sy))
		w, h := int(math.Round((r.X+r.Width)*sx))-left, int(math.Round((r.Y+r.Height)*sy))-top
		decoded, err := png.Decode(bytes.NewReader(s.PNG))
		if err != nil || w != size.X || h != size.Y || color.NRGBAModel.Convert(decoded.At(left, top)) != img.NRGBAAt(0, 0) {
			t.Fatalf("system-picked image changed: %v, %dx%d want %v", err, w, h, size)
		}
	}
}

func TestPortalImageOnlyReadsBoundedLocalPNG(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux portal file URIs use Unix paths")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "selected image.png")
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewNRGBA(image.Rect(0, 0, 7, 5))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	uri := (&url.URL{Scheme: "file", Path: path}).String()
	if got, err := readPortalImage(uri); err != nil || !bytes.Equal(got, data.Bytes()) {
		t.Fatalf("portal PNG: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("portal-owned file must not be removed")
	}
	for _, uri := range []string{"https://example.test/image.png", "file://other-host" + path, uri + "?query=x", uri + "#fragment", "file:relative.png", (&url.URL{Scheme: "file", Path: dir}).String()} {
		if _, err := readPortalImage(uri); err == nil {
			t.Fatalf("accepted invalid portal image: %s", uri)
		}
	}
	if err := os.WriteFile(path, []byte("not PNG"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPortalImage(uri); err == nil {
		t.Fatal("accepted non-PNG")
	}
}

func TestScreenEncodingHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := screenFromImage(ctx, Rect{Width: 1, Height: 1}, image.NewNRGBA(image.Rect(0, 0, 1, 1))); err != context.Canceled {
		t.Fatalf("cancelled encoding: %v", err)
	}
}
