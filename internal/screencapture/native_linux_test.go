//go:build linux && (amd64 || arm64)

package screencapture

import (
	"bytes"
	"context"
	"image/color"
	"image/png"
	"os"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
)

func TestWaylandSessionRoutesXWaylandThroughPortal(t *testing.T) {
	t.Setenv("XDG_SESSION_TYPE", "x11")
	t.Setenv("WAYLAND_DISPLAY", "")
	if waylandSession() {
		t.Fatal("X11 session selected portal")
	}
	t.Setenv("XDG_SESSION_TYPE", "wayland")
	if !waylandSession() {
		t.Fatal("XWayland app missed Wayland session")
	}
	t.Setenv("XDG_SESSION_TYPE", "")
	t.Setenv("WAYLAND_DISPLAY", "wayland-1")
	if !waylandSession() {
		t.Fatal("Wayland display missed without session type")
	}
}

// Opt in only on an owned virtual X11 display, never on the developer's desktop.
func TestX11OwnedDisplayCaptureAndWindowMetadata(t *testing.T) {
	if os.Getenv("SPOOL_SCREENSHOT_XVFB") != "1" {
		t.Skip("requires an isolated virtual X11 display")
	}
	display, err := openX11()
	if err != nil {
		t.Fatal(err)
	}
	defer closeX11(display)
	info, err := xScreenInfo(display)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := purego.Dlopen("libxcb.so.1", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		t.Fatal(err)
	}
	var generate func(uintptr) uint32
	var create func(uintptr, uint8, uint32, uint32, int16, int16, uint16, uint16, uint16, uint16, uint32, uint32, unsafe.Pointer) uint32
	var mapWindow func(uintptr, uint32) uint32
	var property func(uintptr, uint8, uint32, uint32, uint32, uint8, uint32, unsafe.Pointer) uint32
	var destroy func(uintptr, uint32) uint32
	purego.RegisterLibFunc(&generate, lib, "xcb_generate_id")
	purego.RegisterLibFunc(&create, lib, "xcb_create_window")
	purego.RegisterLibFunc(&mapWindow, lib, "xcb_map_window")
	purego.RegisterLibFunc(&property, lib, "xcb_change_property")
	purego.RegisterLibFunc(&destroy, lib, "xcb_destroy_window")
	id := generate(display)
	red := uint32(0xff0000)
	create(display, 0, id, info.root, 24, 32, 160, 120, 0, 1, 0, 2, unsafe.Pointer(&red))
	name := []byte("Spool owned screenshot fixture")
	property(display, 0, id, 39, 31, 8, uint32(len(name)), unsafe.Pointer(&name[0]))
	mapWindow(display, id)
	// Add a stale window to exercise checked replies (no process-global X error).
	clients := []uint32{id, 0xdeadbeef}
	property(display, 0, info.root, xAtom(display, "_NET_CLIENT_LIST_STACKING"), 33, 32, 2, unsafe.Pointer(&clients[0]))
	if reply := x11.geometryReply(display, x11.geometry(display, id), nil); reply != nil {
		x11.free(reply)
	} else {
		t.Fatal("fixture window did not map")
	}
	windows, err := Windows()
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) != 1 || windows[0].ID != id || windows[0].Name != string(name) || windows[0].Bounds != (Rect{24, 32, 160, 120}) {
		t.Fatalf("window metadata: %+v", windows)
	}
	ctx := context.Background()
	displays := []Display{{Bounds: Rect{Width: 320, Height: 240}, Pixels: Rect{Width: 320, Height: 240}, Scale: 1}}
	snapshot, err := CaptureDisplays(ctx, displays)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(snapshot.Screens[0].PNG))
	if err != nil {
		t.Fatal(err)
	}
	if color.NRGBAModel.Convert(img.At(30, 40)) != (color.NRGBA{R: 255, A: 255}) || img.Bounds().Dx() != 320 || img.Bounds().Dy() != 240 {
		t.Fatal("XCB capture changed pixels or geometry")
	}
	if len(snapshot.Screens[0].Windows) != 1 {
		t.Fatal("missing per-display window targets")
	}
	// Capture two monitor regions concurrently. The second uses a negative
	// toolkit origin, as a monitor to the left of the primary display would.
	blueID := generate(display)
	blue := uint32(0x0000ff)
	create(display, 0, blueID, info.root, 220, 50, 80, 100, 0, 1, 0, 2, unsafe.Pointer(&blue))
	blueName := []byte("Spool 截图 fixture")
	property(display, 0, blueID, xAtom(display, "_NET_WM_NAME"), xAtom(display, "UTF8_STRING"), 8, uint32(len(blueName)), unsafe.Pointer(&blueName[0]))
	extents := []uint32{2, 3, 20, 4}
	property(display, 0, blueID, xAtom(display, "_NET_FRAME_EXTENTS"), 6, 32, 4, unsafe.Pointer(&extents[0]))
	mapWindow(display, blueID)
	clients = []uint32{id, 0xdeadbeef, blueID}
	property(display, 0, info.root, xAtom(display, "_NET_CLIENT_LIST_STACKING"), 33, 32, uint32(len(clients)), unsafe.Pointer(&clients[0]))
	if reply := x11.geometryReply(display, x11.geometry(display, blueID), nil); reply != nil {
		x11.free(reply)
	} else {
		t.Fatal("second fixture window did not map")
	}
	windows, err = Windows()
	if err != nil || len(windows) != 2 || windows[0].ID != blueID || windows[0].Name != string(blueName) || windows[0].Bounds != (Rect{218, 30, 85, 124}) {
		t.Fatalf("stacking, UTF-8 title or frame extents: %+v, %v", windows, err)
	}
	snapshot, err = CaptureDisplays(ctx, []Display{
		{Bounds: Rect{Width: 200, Height: 240}, Pixels: Rect{Width: 200, Height: 240}, Scale: 1},
		{Bounds: Rect{X: -120, Y: 20, Width: 120, Height: 240}, Pixels: Rect{X: 200, Width: 120, Height: 240}, Scale: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Screens) != 2 || len(snapshot.Screens[0].Windows) != 1 || len(snapshot.Screens[1].Windows) != 1 || snapshot.Screens[1].Windows[0].Bounds != (Rect{-102, 50, 85, 124}) {
		t.Fatalf("monitor targets: %+v", snapshot.Screens)
	}
	for i, sample := range []struct {
		x, y, width int
		color       color.NRGBA
	}{{30, 40, 200, color.NRGBA{R: 255, A: 255}}, {30, 60, 120, color.NRGBA{B: 255, A: 255}}} {
		img, err := png.Decode(bytes.NewReader(snapshot.Screens[i].PNG))
		if err != nil {
			t.Fatal(err)
		}
		if img.Bounds().Dx() != sample.width || img.Bounds().Dy() != 240 || color.NRGBAModel.Convert(img.At(sample.x, sample.y)) != sample.color {
			t.Fatalf("monitor %d lost source pixels or capture order", i+1)
		}
	}
	// A removed monitor fails cleanly rather than emitting an X error/aborting.
	if _, err := captureX11Rect(ctx, Rect{X: info.width - 20, Y: info.height - 20, Width: 400, Height: 400}, Rect{Width: 400, Height: 400}); err == nil {
		t.Fatal("captured outside the desktop")
	}
	destroy(display, id)
	destroy(display, blueID)
	if reply := x11.geometryReply(display, x11.geometry(display, info.root), nil); reply != nil {
		x11.free(reply)
	}
	windows, err = Windows()
	if err != nil || len(windows) != 0 {
		t.Fatalf("closed window enumeration: %+v %v", windows, err)
	}
}
