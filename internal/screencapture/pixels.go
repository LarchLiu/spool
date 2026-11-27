package screencapture

import (
	"bytes"
	"context"
	"image"
	"image/draw"
	"image/png"
	"math"
	"math/bits"
)

// Native GDI/XImage buffers can have padding, reversed rows and different RGB
// masks. The desktop alpha channel is undefined; exported pixels are opaque.
func nativePixels(data []byte, width, height, stride, bpp int, little, bottomUp bool, masks [3]uint32) (*image.NRGBA, error) {
	if width < 1 || height < 1 || int64(width)*int64(height) > 100_000_000 || (bpp != 16 && bpp != 24 && bpp != 32) || stride < width*(bpp/8) || int64(stride)*int64(height) > int64(len(data)) {
		return nil, ErrFailed
	}
	for _, mask := range masks {
		if mask == 0 {
			return nil, ErrFailed
		}
	}
	out := image.NewNRGBA(image.Rect(0, 0, width, height))
	var shifts [3]int
	var maxima [3]uint32
	for i, mask := range masks {
		shifts[i] = bits.TrailingZeros32(mask)
		maxima[i] = mask >> shifts[i]
	}
	for y := range height {
		sourceY := y
		if bottomUp {
			sourceY = height - 1 - y
		}
		row := data[sourceY*stride:]
		if bpp == 32 && little && masks == [3]uint32{0xff0000, 0xff00, 0xff} {
			for x := range width {
				src, dst := row[x*4:][:4], out.Pix[y*out.Stride+x*4:][:4]
				dst[0], dst[1], dst[2], dst[3] = src[2], src[1], src[0], 255
			}
			continue
		}
		for x := range width {
			pixel := row[x*(bpp/8):][:bpp/8]
			var value uint32
			for i, part := range pixel {
				shift := i * 8
				if !little {
					shift = (len(pixel) - 1 - i) * 8
				}
				value |= uint32(part) << shift
			}
			dst := out.Pix[y*out.Stride+x*4:][:4]
			for i, mask := range masks {
				dst[i] = byte(uint64((value&mask)>>shifts[i]) * 255 / uint64(maxima[i]))
			}
			dst[3] = 255
		}
	}
	return out, nil
}

func screenFromImage(ctx context.Context, bounds Rect, img image.Image) (Screen, error) {
	if err := ctx.Err(); err != nil {
		return Screen{}, err
	}
	var data bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoder.Encode(&data, img); err != nil {
		return Screen{}, ErrFailed
	}
	if err := ctx.Err(); err != nil {
		return Screen{}, err
	}
	if data.Len() > 64*1024*1024 {
		return Screen{}, ErrTooLarge
	}
	return screenFromPNG(bounds, data.Bytes())
}

// Map each native window into this editor's coordinates. A single global DIP
// transform is incorrect when adjacent Windows monitors have different DPI.
func windowsForDisplay(windows []Window, display Display) []Window {
	out := make([]Window, 0, len(windows))
	p := display.Pixels
	if p.Width <= 0 || p.Height <= 0 {
		p = display.Bounds
	}
	sx, sy := float64(display.Bounds.Width)/float64(p.Width), float64(display.Bounds.Height)/float64(p.Height)
	for _, window := range windows {
		r := window.Bounds.Image().Intersect(p.Image())
		if r.Empty() {
			continue
		}
		left, top := int(math.Round(float64(r.Min.X-p.X)*sx)), int(math.Round(float64(r.Min.Y-p.Y)*sy))
		right, bottom := int(math.Round(float64(r.Max.X-p.X)*sx)), int(math.Round(float64(r.Max.Y-p.Y)*sy))
		window.Bounds = Rect{display.Bounds.X + left, display.Bounds.Y + top, right - left, bottom - top}
		out = append(out, window)
	}
	return out
}

func pickedSnapshot(ctx context.Context, data []byte, displays []Display) (*Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(displays) == 0 {
		return nil, ErrFailed
	}
	screen, err := screenFromPNG(Rect{}, data)
	if err != nil {
		return nil, err
	}
	b := displays[0].Bounds
	if b.Width < 100 || b.Height < 100 {
		return nil, ErrFailed
	}
	// Small or very narrow system selections need space for the same toolbar.
	// Pad with transparent pixels and initially select only the original image.
	sourceWidth, sourceHeight := screen.Size.X, screen.Size.Y
	left, top := 0, 0
	fit := math.Min(1, math.Min(float64(b.Width-80)/float64(sourceWidth), float64(b.Height-80)/float64(sourceHeight)))
	wantedWidth := max(sourceWidth, int(math.Ceil(float64(min(480, b.Width-80))/fit)))
	wantedHeight := max(sourceHeight, int(math.Ceil(float64(min(320, b.Height-80))/fit)))
	if wantedWidth != sourceWidth || wantedHeight != sourceHeight {
		if int64(wantedWidth)*int64(wantedHeight) > 100_000_000 {
			return nil, ErrTooLarge
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, ErrFailed
		}
		padded := image.NewNRGBA(image.Rect(0, 0, wantedWidth, wantedHeight))
		left, top = (wantedWidth-sourceWidth)/2, (wantedHeight-sourceHeight)/2
		draw.Draw(padded, image.Rect(left, top, left+sourceWidth, top+sourceHeight), img, img.Bounds().Min, draw.Src)
		screen, err = screenFromImage(ctx, Rect{}, padded)
		if err != nil {
			return nil, err
		}
	}
	scale := math.Min(1, math.Min(float64(b.Width-80)/float64(screen.Size.X), float64(b.Height-80)/float64(screen.Size.Y)))
	w, h := max(1, int(float64(screen.Size.X)*scale)), max(1, int(float64(screen.Size.Y)*scale))
	screen.Bounds = Rect{b.X + (b.Width-w)/2, b.Y + (b.Height-h)/2, w, h}
	sx, sy := float64(w)/float64(screen.Size.X), float64(h)/float64(screen.Size.Y)
	screen.Selection = &Selection{float64(left) * sx, float64(top) * sy, float64(sourceWidth) * sx, float64(sourceHeight) * sy}
	return &Snapshot{Screens: []Screen{screen}, Picked: true}, nil
}
