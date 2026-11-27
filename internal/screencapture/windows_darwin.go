package screencapture

import (
	"encoding/json"
	"math"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

var native struct {
	once      sync.Once
	err       error
	list      func(uint32, uint32) uintptr
	release   func(uintptr)
	preflight func() bool
	request   func() bool
}

func loadNative() error {
	native.once.Do(func() {
		cg, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			native.err = err
			return
		}
		cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			native.err = err
			return
		}
		if _, err := purego.Dlopen("/System/Library/Frameworks/Foundation.framework/Foundation", purego.RTLD_NOW|purego.RTLD_GLOBAL); err != nil {
			native.err = err
			return
		}
		purego.RegisterLibFunc(&native.list, cg, "CGWindowListCopyWindowInfo")
		purego.RegisterLibFunc(&native.preflight, cg, "CGPreflightScreenCaptureAccess")
		purego.RegisterLibFunc(&native.request, cg, "CGRequestScreenCaptureAccess")
		purego.RegisterLibFunc(&native.release, cf, "CFRelease")
	})
	return native.err
}

// RequestPermission runs on the app's UI thread before capturing. No
// Accessibility or Apple Events permission is required to enumerate windows.
func RequestPermission() bool {
	return loadNative() == nil && (native.preflight() || native.request())
}

// ConfigureOverlay makes the owned NSPanel genuinely borderless. MyGo's
// Frameless option keeps NSWindowStyleMaskTitled, which AppKit constrains below
// the menu bar even when its buttons and title are hidden. Apply this before
// setting the display bounds, so the frozen desktop aligns with the live one.
// Its caller runs on the UI thread before showing the panel.
func ConfigureOverlay(handle uintptr) {
	window := objc.ID(handle)
	window.Send(objc.RegisterName("setStyleMask:"), uintptr(1<<7))      // borderless + nonactivating panel
	window.Send(objc.RegisterName("setAnimationBehavior:"), uintptr(2)) // NSWindowAnimationBehaviorNone
	window.Send(objc.RegisterName("setLevel:"), 1000)                   // above the menu bar and Dock
}

func Windows() ([]Window, error) {
	if err := loadNative(); err != nil {
		return nil, ErrFailed
	}
	// Autorelease pools belong to their OS thread. Capture runs in a
	// goroutine, which must not migrate before the pool is drained.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(objc.RegisterName("new"))
	defer pool.Send(objc.RegisterName("drain"))
	list := native.list(1|16, 0)
	if list == 0 {
		return nil, ErrFailed
	}
	defer native.release(list)
	data := objc.ID(objc.GetClass("NSJSONSerialization")).Send(objc.RegisterName("dataWithJSONObject:options:error:"), list, uintptr(0), uintptr(0))
	if data == 0 {
		return nil, ErrFailed
	}
	length := uintptr(data.Send(objc.RegisterName("length")))
	ptr := objc.Send[unsafe.Pointer](data, objc.RegisterName("bytes"))
	if length == 0 || length > 16*1024*1024 || ptr == nil {
		return nil, ErrFailed
	}
	var entries []struct {
		ID     uint32                                `json:"kCGWindowNumber"`
		Layer  int                                   `json:"kCGWindowLayer"`
		Alpha  float64                               `json:"kCGWindowAlpha"`
		Owner  string                                `json:"kCGWindowOwnerName"`
		Bounds struct{ X, Y, Width, Height float64 } `json:"kCGWindowBounds"`
	}
	if err := json.Unmarshal(unsafe.Slice((*byte)(ptr), int(length)), &entries); err != nil {
		return nil, ErrFailed
	}
	var windows []Window
	for _, e := range entries {
		if e.ID == 0 || e.Layer != 0 || e.Alpha <= 0 || e.Bounds.Width < 20 || e.Bounds.Height < 20 {
			continue
		}
		windows = append(windows, Window{e.ID, e.Owner, Rect{int(math.Round(e.Bounds.X)), int(math.Round(e.Bounds.Y)), int(math.Round(e.Bounds.Width)), int(math.Round(e.Bounds.Height))}})
	}
	return windows, nil
}
