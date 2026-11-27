//go:build linux && (amd64 || arm64)

package screencapture

import (
	"context"
	"math"
	"os"
	"strings"
	"sync"

	"github.com/ebitengine/purego"
)

const failureMessage = "Could not take a screenshot. Check screen capture permissions and the desktop screenshot service."

var linuxGUI struct {
	once              sync.Once
	err               error
	wayland           bool
	decorated         func(uintptr, bool)
	keepAbove         func(uintptr, bool)
	skipPager         func(uintptr, bool)
	fullscreenMonitor func(uintptr, uintptr, int32)
	windowScreen      func(uintptr) uintptr
}

func loadLinuxGUI() error {
	linuxGUI.once.Do(func() {
		gtk, err := purego.Dlopen("libgtk-3.so.0", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			linuxGUI.err = err
			return
		}
		gdk, err := purego.Dlopen("libgdk-3.so.0", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			linuxGUI.err = err
			return
		}
		gobject, err := purego.Dlopen("libgobject-2.0.so.0", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			linuxGUI.err = err
			return
		}
		purego.RegisterLibFunc(&linuxGUI.decorated, gtk, "gtk_window_set_decorated")
		purego.RegisterLibFunc(&linuxGUI.keepAbove, gtk, "gtk_window_set_keep_above")
		purego.RegisterLibFunc(&linuxGUI.skipPager, gtk, "gtk_window_set_skip_pager_hint")
		purego.RegisterLibFunc(&linuxGUI.fullscreenMonitor, gtk, "gtk_window_fullscreen_on_monitor")
		purego.RegisterLibFunc(&linuxGUI.windowScreen, gtk, "gtk_window_get_screen")
		var display func() uintptr
		var isType func(uintptr, uintptr) bool
		purego.RegisterLibFunc(&display, gdk, "gdk_display_get_default")
		purego.RegisterLibFunc(&isType, gobject, "g_type_check_instance_is_a")
		if symbol, err := purego.Dlsym(gdk, "gdk_wayland_display_get_type"); err == nil {
			var waylandType func() uintptr
			purego.RegisterFunc(&waylandType, symbol)
			if d := display(); d != 0 {
				linuxGUI.wayland = isType(d, waylandType())
			}
		}
		// An XWayland root cannot capture native Wayland applications.
		linuxGUI.wayland = linuxGUI.wayland || waylandSession()
	})
	return linuxGUI.err
}

func Supported() bool { return true }

func waylandSession() bool {
	return strings.EqualFold(os.Getenv("XDG_SESSION_TYPE"), "wayland") || os.Getenv("WAYLAND_DISPLAY") != ""
}

// Called on the UI thread, before starting capture workers.
func SystemPicker() bool      { return loadLinuxGUI() == nil && linuxGUI.wayland }
func RequestPermission() bool { return loadLinuxGUI() == nil }

func PrepareDisplays(displays []Display) []Display {
	for i := range displays {
		d := &displays[i]
		scale := d.Scale
		if scale <= 0 {
			scale = 1
		}
		d.Pixels = Rect{int(math.Round(float64(d.Bounds.X) * scale)), int(math.Round(float64(d.Bounds.Y) * scale)), int(math.Round(float64(d.Bounds.Width) * scale)), int(math.Round(float64(d.Bounds.Height) * scale))}
	}
	return displays
}

func LocalCursor(display Display, x, y int) (int, int) {
	return x - display.Bounds.X, y - display.Bounds.Y
}

func ConfigureOverlay(handle uintptr) {
	if loadLinuxGUI() != nil {
		return
	}
	linuxGUI.decorated(handle, false)
	linuxGUI.keepAbove(handle, true)
	linuxGUI.skipPager(handle, true)
}

func PositionOverlay(handle uintptr, display Display) {
	if loadLinuxGUI() != nil {
		return
	}
	// EWMH fullscreen covers desktop panels as well. The stable GDK monitor ID
	// survives MyGo's primary-first reordering of its Displays slice.
	linuxGUI.fullscreenMonitor(handle, linuxGUI.windowScreen(handle), int32(display.ID))
}

func capturePlatformDisplays(ctx context.Context, displays []Display) (*Snapshot, error) {
	// SystemPicker initialized this on the UI thread before capture starts.
	if linuxGUI.wayland {
		data, err := portalScreenshot(ctx)
		if err != nil {
			return nil, err
		}
		return pickedSnapshot(ctx, data, displays)
	}
	windows, err := windowsX11(ctx)
	if err != nil {
		return nil, err
	}
	return captureDisplays(ctx, displays, nil, func(ctx context.Context, display Display) (Screen, error) {
		screen, err := captureX11Rect(ctx, display.Pixels, display.Bounds)
		if err == nil {
			screen.Windows = windowsForDisplay(windows, display)
		}
		return screen, err
	})
}

func runWindow(ctx context.Context, id uint32, path string) error {
	windows, err := Windows()
	if err != nil {
		return err
	}
	for _, window := range windows {
		if window.ID == id {
			screen, err := captureX11Rect(ctx, window.Bounds, window.Bounds)
			if err != nil {
				return err
			}
			return os.WriteFile(path, screen.PNG, 0o600)
		}
	}
	return ErrFailed
}
