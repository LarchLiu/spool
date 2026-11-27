// Package screencapture captures local displays and windows for the native
// screenshot selection surface. It never reads or replaces the clipboard.
package screencapture

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/image/draw"
)

var ErrFailed = errors.New(failureMessage)
var ErrTooLarge = errors.New("Each attachment must be nonempty and at most 4 MB")

type Rect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

func (r Rect) Image() image.Rectangle { return image.Rect(r.X, r.Y, r.X+r.Width, r.Y+r.Height) }

type Window struct {
	ID     uint32 `json:"id"`
	Name   string `json:"name"`
	Bounds Rect   `json:"bounds"`
}
type Display struct {
	Index     int
	ID        int64
	Bounds    Rect // Desktop toolkit coordinates, used by the editor.
	Pixels    Rect // Native desktop pixels, when different from Bounds.
	Scale     float64
	Selection *Selection
}

// Fractional editor points retain exact source pixels when a system-picked
// image is fitted into a smaller editor window.
type Selection struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}
type Screen struct {
	Bounds    Rect
	PNG       []byte
	Image     image.Image
	Size      image.Point
	Windows   []Window // Per-display coordinates; nil uses Snapshot.Windows.
	Selection *Selection
}

func (s Screen) PixelSize() image.Point {
	if s.Image != nil {
		return s.Image.Bounds().Size()
	}
	return s.Size
}

type Snapshot struct {
	Screens []Screen
	Windows []Window
	Picked  bool // The system selected the image; open an editor, not a desktop overlay.
}

func CaptureDisplays(ctx context.Context, displays []Display) (*Snapshot, error) {
	if len(displays) == 0 {
		return nil, ErrFailed
	}
	return capturePlatformDisplays(ctx, displays)
}

// The editor decodes the frozen PNG in its WebView. Only its dimensions are needed
// here; decoding every Retina pixel a second time delays entry and doubles RAM.
func screenFromPNG(bounds Rect, data []byte) (Screen, error) {
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || int64(config.Width)*int64(config.Height) > 100_000_000 {
		return Screen{}, ErrFailed
	}
	return Screen{Bounds: bounds, PNG: data, Size: image.Pt(config.Width, config.Height)}, nil
}

func captureDisplays(ctx context.Context, displays []Display, windows []Window, captureDisplay func(context.Context, Display) (Screen, error)) (*Snapshot, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	snapshot := &Snapshot{Windows: windows, Screens: make([]Screen, len(displays))}
	failures := make(chan error, 1)
	var workers sync.WaitGroup
	for i, display := range displays {
		workers.Add(1)
		go func() {
			defer workers.Done()
			screen, err := captureDisplay(ctx, display)
			if err != nil {
				select {
				case failures <- err:
					cancel()
				default:
				}
				return
			}
			snapshot.Screens[i] = screen
		}()
	}
	workers.Wait() // Join all subprocesses before the caller can restore windows.
	select {
	case err := <-failures:
		return nil, err
	default:
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func CaptureWindow(ctx context.Context, id uint32) ([]byte, error) {
	return capture(ctx, func(ctx context.Context, path string) error { return runWindow(ctx, id, path) }, 4*1024*1024)
}

func capture(ctx context.Context, command func(context.Context, string) error, limit int64) ([]byte, error) {
	dir, err := scratchDir()
	if err != nil {
		return nil, ErrFailed
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "capture.png")
	err = command(ctx, path)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	file, openErr := os.Open(path)
	if err != nil || openErr != nil {
		if file != nil {
			file.Close()
		}
		return nil, ErrFailed
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, ErrFailed
	}
	if int64(len(data)) > limit {
		return nil, ErrTooLarge
	}
	if config, err := png.DecodeConfig(bytes.NewReader(data)); err != nil || config.Width < 1 || config.Height < 1 || int64(config.Width)*int64(config.Height) > 100_000_000 {
		return nil, ErrFailed
	}
	return data, nil
}

// Crop uses each display's pixel ratio, including negative desktop coordinates
// and mixed-DPI monitors. Areas between displays remain transparent.
func (s *Snapshot) Crop(rect Rect) ([]byte, error) {
	if rect.Width < 1 || rect.Height < 1 {
		return nil, ErrFailed
	}
	scale := 0.0
	covered := image.Rectangle{}
	for _, screen := range s.Screens {
		part := screen.Bounds.Image().Intersect(rect.Image())
		if part.Empty() {
			continue
		}
		covered = covered.Union(part)
		scale = math.Max(scale, float64(screen.PixelSize().X)/float64(screen.Bounds.Width))
	}
	if covered != rect.Image() || scale <= 0 {
		return nil, ErrFailed
	}
	width, height := int(math.Round(float64(rect.Width)*scale)), int(math.Round(float64(rect.Height)*scale))
	if int64(width)*int64(height) > 100_000_000 {
		return nil, ErrTooLarge
	}
	out := image.NewNRGBA(image.Rect(0, 0, width, height))
	for _, screen := range s.Screens {
		part := screen.Bounds.Image().Intersect(rect.Image())
		if part.Empty() {
			continue
		}
		img := screen.Image
		if img == nil {
			var err error
			img, err = png.Decode(bytes.NewReader(screen.PNG))
			if err != nil {
				return nil, ErrFailed
			}
		}
		sx := float64(img.Bounds().Dx()) / float64(screen.Bounds.Width)
		sy := float64(img.Bounds().Dy()) / float64(screen.Bounds.Height)
		src := image.Rect(int(math.Round(float64(part.Min.X-screen.Bounds.X)*sx)), int(math.Round(float64(part.Min.Y-screen.Bounds.Y)*sy)), int(math.Round(float64(part.Max.X-screen.Bounds.X)*sx)), int(math.Round(float64(part.Max.Y-screen.Bounds.Y)*sy)))
		dst := image.Rect(int(math.Round(float64(part.Min.X-rect.X)*scale)), int(math.Round(float64(part.Min.Y-rect.Y)*scale)), int(math.Round(float64(part.Max.X-rect.X)*scale)), int(math.Round(float64(part.Max.Y-rect.Y)*scale)))
		draw.CatmullRom.Scale(out, dst, img, src, draw.Src, nil)
	}
	var result bytes.Buffer
	if err := png.Encode(&result, out); err != nil {
		return nil, ErrFailed
	}
	if result.Len() > 4*1024*1024 {
		return nil, ErrTooLarge
	}
	return result.Bytes(), nil
}
