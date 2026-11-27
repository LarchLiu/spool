package screencapture

import (
	"os"
	"strconv"
	"testing"
)

func TestNativeScreenshotWindowMetadata(t *testing.T) {
	if os.Getenv("SPOOL_SCREENSHOT_NATIVE_PROBE") != "1" {
		t.Skip("explicit local window-server probe")
	}
	windows := checkNativeScreenshotWindowMetadata(t)
	t.Logf("Read %d front-to-back window bounds through CoreGraphics", len(windows))
}

func TestNativeScreenshotWindowMetadataConcurrent(t *testing.T) {
	if os.Getenv("SPOOL_SCREENSHOT_NATIVE_PROBE") != "1" {
		t.Skip("explicit local window-server probe")
	}
	for worker := range 8 {
		t.Run(strconv.Itoa(worker), func(t *testing.T) {
			t.Parallel()
			for range 25 {
				checkNativeScreenshotWindowMetadata(t)
			}
		})
	}
}

func checkNativeScreenshotWindowMetadata(t *testing.T) []Window {
	t.Helper()
	windows, err := Windows()
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) == 0 {
		t.Fatal("no visible application windows")
	}
	for _, window := range windows {
		if window.ID == 0 || window.Bounds.Width < 20 || window.Bounds.Height < 20 {
			t.Fatal("invalid native window geometry")
		}
	}
	return windows
}
