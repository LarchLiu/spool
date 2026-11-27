//go:build !darwin && !windows && !linux

package screencapture

import "context"

func Supported() bool { return false }

const failureMessage = "Could not take a screenshot on this platform."

func SystemPicker() bool                           { return false }
func PrepareDisplays(displays []Display) []Display { return displays }
func PositionOverlay(uintptr, Display)             {}
func LocalCursor(display Display, x, y int) (int, int) {
	return x - display.Bounds.X, y - display.Bounds.Y
}
func capturePlatformDisplays(context.Context, []Display) (*Snapshot, error) { return nil, ErrFailed }

func runDisplay(context.Context, int, string) error   { return ErrFailed }
func runWindow(context.Context, uint32, string) error { return ErrFailed }
func scratchDir() (string, error)                     { return "", ErrFailed }
func Windows() ([]Window, error)                      { return nil, ErrFailed }
func RequestPermission() bool                         { return false }
func ConfigureOverlay(uintptr)                        {}
