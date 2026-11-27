package screencapture

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

const failureMessage = "Could not take a screenshot. Check that the desktop is available and try again."

var (
	user32                = windows.NewLazySystemDLL("user32.dll")
	gdi32                 = windows.NewLazySystemDLL("gdi32.dll")
	dwmapi                = windows.NewLazySystemDLL("dwmapi.dll")
	getMonitorInfo        = user32.NewProc("GetMonitorInfoW")
	getCursorPos          = user32.NewProc("GetCursorPos")
	getDC                 = user32.NewProc("GetDC")
	releaseDC             = user32.NewProc("ReleaseDC")
	createCompatibleDC    = gdi32.NewProc("CreateCompatibleDC")
	deleteDC              = gdi32.NewProc("DeleteDC")
	createDIBSection      = gdi32.NewProc("CreateDIBSection")
	selectObject          = gdi32.NewProc("SelectObject")
	deleteObject          = gdi32.NewProc("DeleteObject")
	bitBlt                = gdi32.NewProc("BitBlt")
	gdiFlush              = gdi32.NewProc("GdiFlush")
	enumWindows           = user32.NewProc("EnumWindows")
	isWindowVisible       = user32.NewProc("IsWindowVisible")
	isIconic              = user32.NewProc("IsIconic")
	getWindowRect         = user32.NewProc("GetWindowRect")
	getWindowText         = user32.NewProc("GetWindowTextW")
	getClassName          = user32.NewProc("GetClassNameW")
	getWindowLong         = user32.NewProc("GetWindowLongPtrW")
	setWindowLong         = user32.NewProc("SetWindowLongPtrW")
	setWindowPos          = user32.NewProc("SetWindowPos")
	dwmGetWindowAttribute = dwmapi.NewProc("DwmGetWindowAttribute")
	dwmSetWindowAttribute = dwmapi.NewProc("DwmSetWindowAttribute")
	windowCallback        = windows.NewCallback(enumerateCaptureWindow)
	windowRequests        sync.Map
	windowRequestID       atomic.Uintptr
)

type winRect struct{ Left, Top, Right, Bottom int32 }

func (r winRect) rect() Rect {
	return Rect{int(r.Left), int(r.Top), int(r.Right - r.Left), int(r.Bottom - r.Top)}
}

type monitorInfo struct {
	Size          uint32
	Monitor, Work winRect
	Flags         uint32
}
type bitmapInfo struct {
	Size                         uint32
	Width, Height                int32
	Planes, BitCount             uint16
	Compression, SizeImage       uint32
	XPelsPerMeter, YPelsPerMeter int32
	ColorsUsed, ColorsImportant  uint32
}

func Supported() bool         { return true }
func SystemPicker() bool      { return false }
func RequestPermission() bool { return true }

func PrepareDisplays(displays []Display) []Display {
	for i := range displays {
		info := monitorInfo{Size: uint32(unsafe.Sizeof(monitorInfo{}))}
		if ok, _, _ := getMonitorInfo.Call(uintptr(displays[i].ID), uintptr(unsafe.Pointer(&info))); ok != 0 {
			displays[i].Pixels = info.Monitor.rect()
		}
	}
	return displays
}

func LocalCursor(display Display, x, y int) (int, int) {
	var point struct{ X, Y int32 }
	if ok, _, _ := getCursorPos.Call(uintptr(unsafe.Pointer(&point))); ok != 0 && display.Pixels.Width > 0 && display.Pixels.Height > 0 {
		p := display.Pixels
		return int((int64(point.X) - int64(p.X)) * int64(display.Bounds.Width) / int64(p.Width)), int((int64(point.Y) - int64(p.Y)) * int64(display.Bounds.Height) / int64(p.Height))
	}
	return x - display.Bounds.X, y - display.Bounds.Y
}

func ConfigureOverlay(handle uintptr) {
	// Remove the actual nonclient frame, including the invisible resize border.
	style, _, _ := getWindowLong.Call(handle, ^uintptr(15)) // GWL_STYLE = -16
	setWindowLong.Call(handle, ^uintptr(15), style&^uintptr(0x00cf0000))
	if dwmSetWindowAttribute.Find() == nil {
		corner := uint32(1) // DWMWCP_DONOTROUND (Windows 11; ignored on Windows 10).
		dwmSetWindowAttribute.Call(handle, 33, uintptr(unsafe.Pointer(&corner)), 4)
	}
}

func PositionOverlay(handle uintptr, display Display) {
	p := display.Pixels
	if p.Width <= 0 || p.Height <= 0 {
		return
	}
	// MyGo SetBounds uses the window's previous DPI. Position in native pixels
	// after it, so moving to a monitor with another DPI cannot shift the image.
	setWindowPos.Call(handle, ^uintptr(0), uintptr(p.X), uintptr(p.Y), uintptr(p.Width), uintptr(p.Height), 0x10|0x20) // TOPMOST, NOACTIVATE, FRAMECHANGED
	// The first move can synchronously change DPI and apply Windows' suggested
	// rectangle. With the new DPI settled, reapply the exact physical rectangle.
	setWindowPos.Call(handle, ^uintptr(0), uintptr(p.X), uintptr(p.Y), uintptr(p.Width), uintptr(p.Height), 0x10)
}

func capturePlatformDisplays(ctx context.Context, displays []Display) (*Snapshot, error) {
	windows, err := Windows()
	if err != nil {
		return nil, err
	}
	return captureDisplays(ctx, displays, nil, func(ctx context.Context, display Display) (Screen, error) {
		if display.Pixels.Width < 1 || display.Pixels.Height < 1 {
			return Screen{}, ErrFailed
		}
		screen, err := captureWindowsRect(ctx, display.Pixels, display.Bounds)
		if err == nil {
			screen.Windows = windowsForDisplay(windows, display)
		}
		return screen, err
	})
}

func captureWindowsRect(ctx context.Context, pixels, bounds Rect) (Screen, error) {
	if err := ctx.Err(); err != nil {
		return Screen{}, err
	}
	if pixels.Width < 1 || pixels.Height < 1 || int64(pixels.Width)*int64(pixels.Height) > 100_000_000 {
		return Screen{}, ErrFailed
	}
	// GetDC/ReleaseDC and the GDI objects must stay on their creating thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	source, _, _ := getDC.Call(0)
	if source == 0 {
		return Screen{}, ErrFailed
	}
	defer releaseDC.Call(0, source)
	dest, _, _ := createCompatibleDC.Call(source)
	if dest == 0 {
		return Screen{}, ErrFailed
	}
	defer deleteDC.Call(dest)
	info := bitmapInfo{Size: uint32(unsafe.Sizeof(bitmapInfo{})), Width: int32(pixels.Width), Height: -int32(pixels.Height), Planes: 1, BitCount: 32}
	var data unsafe.Pointer
	bitmap, _, _ := createDIBSection.Call(source, uintptr(unsafe.Pointer(&info)), 0, uintptr(unsafe.Pointer(&data)), 0, 0)
	if bitmap == 0 || data == nil {
		return Screen{}, ErrFailed
	}
	defer deleteObject.Call(bitmap)
	old, _, _ := selectObject.Call(dest, bitmap)
	if old == 0 || old == ^uintptr(0) {
		return Screen{}, ErrFailed
	}
	defer selectObject.Call(dest, old)
	if ok, _, _ := bitBlt.Call(dest, 0, 0, uintptr(pixels.Width), uintptr(pixels.Height), source, uintptr(pixels.X), uintptr(pixels.Y), 0x00cc0020|0x40000000); ok == 0 {
		return Screen{}, ErrFailed
	}
	gdiFlush.Call()
	img, err := nativePixels(unsafe.Slice((*byte)(data), pixels.Width*pixels.Height*4), pixels.Width, pixels.Height, pixels.Width*4, 32, true, false, [3]uint32{0xff0000, 0xff00, 0xff})
	if err != nil {
		return Screen{}, err
	}
	return screenFromImage(ctx, bounds, img)
}

func enumerateCaptureWindow(handle, param uintptr) uintptr {
	if visible, _, _ := isWindowVisible.Call(handle); visible == 0 {
		return 1
	}
	if minimized, _, _ := isIconic.Call(handle); minimized != 0 {
		return 1
	}
	var cloaked uint32
	if dwmGetWindowAttribute.Find() == nil {
		dwmGetWindowAttribute.Call(handle, 14, uintptr(unsafe.Pointer(&cloaked)), 4)
		if cloaked != 0 {
			return 1
		}
	}
	var class [256]uint16
	getClassName.Call(handle, uintptr(unsafe.Pointer(&class[0])), uintptr(len(class)))
	switch windows.UTF16ToString(class[:]) {
	case "Progman", "WorkerW", "Shell_TrayWnd", "Shell_SecondaryTrayWnd":
		return 1
	}
	var rect winRect
	if ok, _, _ := getWindowRect.Call(handle, uintptr(unsafe.Pointer(&rect))); ok == 0 {
		return 1
	}
	if dwmGetWindowAttribute.Find() == nil {
		dwmGetWindowAttribute.Call(handle, 9, uintptr(unsafe.Pointer(&rect)), unsafe.Sizeof(rect))
	}
	if rect.Right-rect.Left < 20 || rect.Bottom-rect.Top < 20 {
		return 1
	}
	var text [512]uint16
	getWindowText.Call(handle, uintptr(unsafe.Pointer(&text[0])), uintptr(len(text)))
	name := windows.UTF16ToString(text[:])
	if name == "" {
		return 1
	}
	value, ok := windowRequests.Load(param)
	if !ok {
		return 0
	}
	list := value.(*[]Window)
	*list = append(*list, Window{uint32(handle), name, rect.rect()})
	return 1
}

func Windows() ([]Window, error) {
	list := make([]Window, 0)
	token := windowRequestID.Add(1)
	windowRequests.Store(token, &list)
	defer windowRequests.Delete(token)
	if ok, _, _ := enumWindows.Call(windowCallback, token); ok == 0 {
		return nil, ErrFailed
	}
	return list, nil // EnumWindows enumerates in front-to-back order.
}

func runWindow(ctx context.Context, id uint32, path string) error {
	var rect winRect
	if ok, _, _ := getWindowRect.Call(uintptr(id), uintptr(unsafe.Pointer(&rect))); ok == 0 {
		return ErrFailed
	}
	screen, err := captureWindowsRect(ctx, rect.rect(), rect.rect())
	if err != nil {
		return err
	}
	return os.WriteFile(path, screen.PNG, 0o600)
}

func scratchDir() (string, error) {
	account, err := user.Current()
	if err != nil {
		return "", err
	}
	// Windows' per-user TEMP supplies the inherited user ACL.
	base := filepath.Join(os.TempDir(), "spool-"+account.Uid, "spool")
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", err
	}
	info, err := os.Lstat(base)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", os.ErrPermission
	}
	return os.MkdirTemp(base, "screenshot.")
}
