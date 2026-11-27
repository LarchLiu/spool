package screencapture

import (
	"context"
	"os/exec"
	"strconv"
)

const failureMessage = "Could not take a screenshot. Check Spool's screen recording permission in System Settings."

func Supported() bool                              { return true }
func SystemPicker() bool                           { return false }
func PrepareDisplays(displays []Display) []Display { return displays }
func PositionOverlay(uintptr, Display)             {}
func LocalCursor(display Display, x, y int) (int, int) {
	return x - display.Bounds.X, y - display.Bounds.Y
}

func capturePlatformDisplays(ctx context.Context, displays []Display) (*Snapshot, error) {
	windows, err := Windows()
	if err != nil {
		return nil, err
	}
	return captureDisplays(ctx, displays, windows, func(ctx context.Context, display Display) (Screen, error) {
		data, err := capture(ctx, func(ctx context.Context, path string) error {
			return runDisplay(ctx, display.Index, path)
		}, 64*1024*1024)
		if err != nil {
			return Screen{}, err
		}
		return screenFromPNG(display.Bounds, data)
	})
}

func runDisplay(ctx context.Context, index int, path string) error {
	return exec.CommandContext(ctx, "/usr/sbin/screencapture", "-x", "-D", strconv.Itoa(index), "-t", "png", path).Run()
}

func runWindow(ctx context.Context, id uint32, path string) error {
	return exec.CommandContext(ctx, "/usr/sbin/screencapture", "-x", "-o", "-l", strconv.FormatUint(uint64(id), 10), "-t", "png", path).Run()
}
