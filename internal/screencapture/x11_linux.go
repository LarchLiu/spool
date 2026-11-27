//go:build linux && (amd64 || arm64)

package screencapture

import (
	"context"
	"encoding/binary"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"golang.org/x/sys/unix"
)

// Dedicated XCB connections avoid Xlib's process-global error handler: an app
// closing between enumeration and a metadata read must not terminate Spool.
var x11 struct {
	once            sync.Once
	err             error
	screens         sync.Map // connection -> the DISPLAY-selected X screen index
	connect         func(*byte, *int32) uintptr
	disconnect      func(uintptr)
	connectionError func(uintptr) int32
	fileDescriptor  func(uintptr) int32
	setup           func(uintptr) unsafe.Pointer
	getImage        func(uintptr, uint8, uint32, int16, int16, uint16, uint16, uint32) uint32
	imageReply      func(uintptr, uint32, *unsafe.Pointer) unsafe.Pointer
	imageData       func(unsafe.Pointer) unsafe.Pointer
	imageLength     func(unsafe.Pointer) int32
	geometry        func(uintptr, uint32) uint32
	geometryReply   func(uintptr, uint32, *unsafe.Pointer) unsafe.Pointer
	attributes      func(uintptr, uint32) uint32
	attributesReply func(uintptr, uint32, *unsafe.Pointer) unsafe.Pointer
	translate       func(uintptr, uint32, uint32, int16, int16) uint32
	translateReply  func(uintptr, uint32, *unsafe.Pointer) unsafe.Pointer
	atom            func(uintptr, uint8, uint16, string) uint32
	atomReply       func(uintptr, uint32, *unsafe.Pointer) unsafe.Pointer
	property        func(uintptr, uint8, uint32, uint32, uint32, uint32, uint32) uint32
	propertyReply   func(uintptr, uint32, *unsafe.Pointer) unsafe.Pointer
	propertyValue   func(unsafe.Pointer) unsafe.Pointer
	propertyLength  func(unsafe.Pointer) int32
	queryTree       func(uintptr, uint32) uint32
	queryTreeReply  func(uintptr, uint32, *unsafe.Pointer) unsafe.Pointer
	children        func(unsafe.Pointer) unsafe.Pointer
	childrenLength  func(unsafe.Pointer) int32
	free            func(unsafe.Pointer)
}

func openX11() (uintptr, error) {
	x11.once.Do(func() {
		lib, err := purego.Dlopen("libxcb.so.1", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			x11.err = err
			return
		}
		libc, err := purego.Dlopen("libc.so.6", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			x11.err = err
			return
		}
		purego.RegisterLibFunc(&x11.free, libc, "free")
		purego.RegisterLibFunc(&x11.connect, lib, "xcb_connect")
		purego.RegisterLibFunc(&x11.disconnect, lib, "xcb_disconnect")
		purego.RegisterLibFunc(&x11.connectionError, lib, "xcb_connection_has_error")
		purego.RegisterLibFunc(&x11.fileDescriptor, lib, "xcb_get_file_descriptor")
		purego.RegisterLibFunc(&x11.setup, lib, "xcb_get_setup")
		purego.RegisterLibFunc(&x11.getImage, lib, "xcb_get_image")
		purego.RegisterLibFunc(&x11.imageReply, lib, "xcb_get_image_reply")
		purego.RegisterLibFunc(&x11.imageData, lib, "xcb_get_image_data")
		purego.RegisterLibFunc(&x11.imageLength, lib, "xcb_get_image_data_length")
		purego.RegisterLibFunc(&x11.geometry, lib, "xcb_get_geometry")
		purego.RegisterLibFunc(&x11.geometryReply, lib, "xcb_get_geometry_reply")
		purego.RegisterLibFunc(&x11.attributes, lib, "xcb_get_window_attributes")
		purego.RegisterLibFunc(&x11.attributesReply, lib, "xcb_get_window_attributes_reply")
		purego.RegisterLibFunc(&x11.translate, lib, "xcb_translate_coordinates")
		purego.RegisterLibFunc(&x11.translateReply, lib, "xcb_translate_coordinates_reply")
		purego.RegisterLibFunc(&x11.atom, lib, "xcb_intern_atom")
		purego.RegisterLibFunc(&x11.atomReply, lib, "xcb_intern_atom_reply")
		purego.RegisterLibFunc(&x11.property, lib, "xcb_get_property")
		purego.RegisterLibFunc(&x11.propertyReply, lib, "xcb_get_property_reply")
		purego.RegisterLibFunc(&x11.propertyValue, lib, "xcb_get_property_value")
		purego.RegisterLibFunc(&x11.propertyLength, lib, "xcb_get_property_value_length")
		purego.RegisterLibFunc(&x11.queryTree, lib, "xcb_query_tree")
		purego.RegisterLibFunc(&x11.queryTreeReply, lib, "xcb_query_tree_reply")
		purego.RegisterLibFunc(&x11.children, lib, "xcb_query_tree_children")
		purego.RegisterLibFunc(&x11.childrenLength, lib, "xcb_query_tree_children_length")
	})
	if x11.err != nil {
		return 0, ErrFailed
	}
	var screen int32
	display := x11.connect(nil, &screen)
	if display == 0 {
		return 0, ErrFailed
	}
	if x11.connectionError(display) != 0 {
		x11.disconnect(display)
		return 0, ErrFailed
	}
	x11.screens.Store(display, int(screen))
	return display, nil
}

func closeX11(display uintptr) {
	x11.screens.Delete(display)
	x11.disconnect(display)
}

func openX11Context(ctx context.Context) (uintptr, func(), error) {
	if err := ctx.Err(); err != nil {
		return 0, nil, err
	}
	display, err := openX11()
	if err != nil {
		return 0, nil, err
	}
	stop, stopped := make(chan struct{}), make(chan struct{})
	fd := int(x11.fileDescriptor(display))
	go func() {
		defer close(stopped)
		select {
		case <-stop:
		case <-ctx.Done():
			// Interrupt pending XCB reads without racing xcb_disconnect or closing
			// a file descriptor that another operation could have reused.
			if fd >= 0 {
				_ = unix.Shutdown(fd, unix.SHUT_RDWR)
			}
		}
	}()
	return display, func() { close(stop); <-stopped; closeX11(display) }, nil
}

func nativeBytes(ptr unsafe.Pointer, size int) []byte {
	return unsafe.Slice((*byte)(ptr), size)
}
func xU32(ptr unsafe.Pointer, offset int) uint32 {
	return binary.NativeEndian.Uint32(nativeBytes(unsafe.Add(ptr, offset), 4))
}
func xU16(ptr unsafe.Pointer, offset int) uint16 {
	return binary.NativeEndian.Uint16(nativeBytes(unsafe.Add(ptr, offset), 2))
}

type xScreen struct {
	root                    uint32
	width, height, bpp, pad int
	little                  bool
	masks                   [3]uint32
}

func xScreenInfo(display uintptr) (xScreen, error) {
	setup := x11.setup(display)
	if setup == nil {
		return xScreen{}, ErrFailed
	}
	header := nativeBytes(setup, 40)
	if header[28] == 0 {
		return xScreen{}, ErrFailed
	}
	formats := unsafe.Add(setup, 40+((int(xU16(setup, 24))+3)&^3))
	screen := unsafe.Add(formats, int(header[29])*8)
	index, _ := x11.screens.Load(display)
	selected, _ := index.(int)
	if selected < 0 || selected >= int(header[28]) {
		return xScreen{}, ErrFailed
	}
	for i := 0; i < selected; i++ {
		depths := int(nativeBytes(screen, 40)[39])
		screen = unsafe.Add(screen, 40)
		for j := 0; j < depths; j++ {
			n := int(xU16(screen, 2))
			screen = unsafe.Add(screen, 8+n*24)
		}
	}
	info := xScreen{root: xU32(screen, 0), width: int(xU16(screen, 20)), height: int(xU16(screen, 22)), little: header[30] == 0}
	depth, visual := nativeBytes(screen, 40)[38], xU32(screen, 32)
	for i := 0; i < int(header[29]); i++ {
		format := nativeBytes(unsafe.Add(formats, i*8), 8)
		if format[0] == depth {
			info.bpp, info.pad = int(format[1]), int(format[2])
		}
	}
	d := unsafe.Add(screen, 40)
	for i := 0; i < int(nativeBytes(screen, 40)[39]); i++ {
		n := int(xU16(d, 2))
		d = unsafe.Add(d, 8)
		for j := 0; j < n; j++ {
			if xU32(d, 0) == visual {
				info.masks = [3]uint32{xU32(d, 8), xU32(d, 12), xU32(d, 16)}
			}
			d = unsafe.Add(d, 24)
		}
	}
	if info.bpp == 0 || info.pad == 0 || info.masks[0] == 0 {
		return xScreen{}, ErrFailed
	}
	return info, nil
}

func captureX11Rect(ctx context.Context, pixels, bounds Rect) (Screen, error) {
	if err := ctx.Err(); err != nil {
		return Screen{}, err
	}
	display, cleanup, err := openX11Context(ctx)
	if err != nil {
		return Screen{}, err
	}
	defer cleanup()
	info, err := xScreenInfo(display)
	if err != nil {
		return Screen{}, err
	}
	if pixels.Width < 1 || pixels.Height < 1 || int64(pixels.Width)*int64(pixels.Height) > 100_000_000 || !pixels.Image().In(Rect{Width: info.width, Height: info.height}.Image()) || pixels.Width > 65535 || pixels.Height > 65535 || pixels.X > 32767 || pixels.Y > 32767 {
		return Screen{}, ErrFailed
	}
	reply := x11.imageReply(display, x11.getImage(display, 2, info.root, int16(pixels.X), int16(pixels.Y), uint16(pixels.Width), uint16(pixels.Height), 0xffffffff), nil)
	if reply == nil {
		if err := ctx.Err(); err != nil {
			return Screen{}, err
		}
		return Screen{}, ErrFailed
	}
	defer x11.free(reply)
	length := int(x11.imageLength(reply))
	ptr := x11.imageData(reply)
	if ptr == nil || length < 1 || length > 512*1024*1024 {
		return Screen{}, ErrFailed
	}
	stride := ((pixels.Width*info.bpp + info.pad - 1) / info.pad) * (info.pad / 8)
	rgba, err := nativePixels(unsafe.Slice((*byte)(ptr), length), pixels.Width, pixels.Height, stride, info.bpp, info.little, false, info.masks)
	if err != nil {
		return Screen{}, err
	}
	return screenFromImage(ctx, bounds, rgba)
}

func xAtom(display uintptr, name string) uint32 {
	reply := x11.atomReply(display, x11.atom(display, 0, uint16(len(name)), name), nil)
	if reply == nil {
		return 0
	}
	defer x11.free(reply)
	return xU32(reply, 8)
}

type xProperty struct {
	values []uint32
	text   string
}

func readXProperty(display uintptr, window uint32, name string) xProperty {
	atom := xAtom(display, name)
	if atom == 0 {
		return xProperty{}
	}
	reply := x11.propertyReply(display, x11.property(display, 0, window, atom, 0, 0, 4096), nil)
	if reply == nil {
		return xProperty{}
	}
	defer x11.free(reply)
	n := int(x11.propertyLength(reply))
	ptr := x11.propertyValue(reply)
	if n < 0 || n > 16384 || ptr == nil {
		return xProperty{}
	}
	switch nativeBytes(reply, 2)[1] {
	case 32:
		return xProperty{values: append([]uint32(nil), unsafe.Slice((*uint32)(ptr), n/4)...)}
	case 8:
		return xProperty{text: string(unsafe.Slice((*byte)(ptr), n))}
	}
	return xProperty{}
}

func Windows() ([]Window, error) {
	return windowsX11(context.Background())
}

func windowsX11(ctx context.Context) ([]Window, error) {
	display, cleanup, err := openX11Context(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	info, err := xScreenInfo(display)
	if err != nil {
		return nil, err
	}
	clients := readXProperty(display, info.root, "_NET_CLIENT_LIST_STACKING").values
	if len(clients) == 0 {
		reply := x11.queryTreeReply(display, x11.queryTree(display, info.root), nil)
		if reply != nil {
			n := int(x11.childrenLength(reply))
			ptr := x11.children(reply)
			if n > 0 && n <= 16384 && ptr != nil {
				clients = append([]uint32(nil), unsafe.Slice((*uint32)(ptr), n)...)
			}
			x11.free(reply)
		}
	}
	desktop, dock := xAtom(display, "_NET_WM_WINDOW_TYPE_DESKTOP"), xAtom(display, "_NET_WM_WINDOW_TYPE_DOCK")
	windows := make([]Window, 0, len(clients))
	for i := len(clients) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id := clients[i]
		attr := x11.attributesReply(display, x11.attributes(display, id), nil)
		if attr == nil {
			continue
		}
		a := nativeBytes(attr, 44)
		visible := a[26] == 2 && a[27] == 0 && xU16(attr, 12) == 1
		x11.free(attr)
		if !visible {
			continue
		}
		skip := false
		for _, kind := range readXProperty(display, id, "_NET_WM_WINDOW_TYPE").values {
			if kind == desktop || kind == dock {
				skip = true
			}
		}
		if skip {
			continue
		}
		geo := x11.geometryReply(display, x11.geometry(display, id), nil)
		if geo == nil {
			continue
		}
		width, height := int(xU16(geo, 16)), int(xU16(geo, 18))
		x11.free(geo)
		if width < 20 || height < 20 {
			continue
		}
		pos := x11.translateReply(display, x11.translate(display, id, info.root, 0, 0), nil)
		if pos == nil {
			continue
		}
		// The reply's child window occupies bytes 8–11; destination coordinates
		// follow it. Reading the child ID as coordinates shifts hover targets.
		b := Rect{int(int16(xU16(pos, 12))), int(int16(xU16(pos, 14))), width, height}
		x11.free(pos)
		if ext := readXProperty(display, id, "_NET_FRAME_EXTENTS").values; len(ext) == 4 && ext[0] < 32768 && ext[1] < 32768 && ext[2] < 32768 && ext[3] < 32768 {
			b = Rect{b.X - int(ext[0]), b.Y - int(ext[2]), b.Width + int(ext[0]+ext[1]), b.Height + int(ext[2]+ext[3])}
		}
		name := readXProperty(display, id, "_NET_WM_NAME").text
		if name == "" {
			name = readXProperty(display, id, "WM_NAME").text
		}
		if name == "" {
			continue
		}
		windows = append(windows, Window{id, name, b})
	}
	return windows, nil
}
