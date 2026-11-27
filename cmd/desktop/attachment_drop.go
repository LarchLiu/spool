package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
)

type desktopDroppedFile struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Data     []byte `json:"data"`
	Modified int64  `json:"modified"`
}

type desktopAttachmentDelivery struct {
	Token string               `json:"token"`
	URL   string               `json:"url"`
	Files []desktopDroppedFile `json:"files"`
	Error string               `json:"error,omitempty"`
}

var desktopAttachmentDropped = mygo.NewEvent[desktopAttachmentDelivery]("spool:attachment-drop")

type desktopAttachmentDrag struct {
	Active bool `json:"active"`
	X      int  `json:"x"`
	Y      int  `json:"y"`
}

var desktopAttachmentDragged = mygo.NewEvent[desktopAttachmentDrag]("spool:attachment-drag")

// Embedded WebViews sit under MyGo's native content surface. Register this
// destination so native hover/leave events also reach the composer's UI; an
// OnFileDrop listener alone only supplies the completed drop.
func (p *panel) attachmentDropTarget(element ui.Element) {
	options := transfer.DropOptions{Formats: []transfer.Format{transfer.FileList}, Operations: transfer.Copy}
	_, over := ui.DataDragOver(element, options)
	x, y, _ := element.PointerPosition()
	point := desktopAttachmentDrag{Active: over, X: int(math.Round(float64(x))), Y: int(math.Round(float64(y)))}
	p.updateAttachmentDrag(point)
	if drop, ok := ui.DropData(element, options); ok {
		if paths, err := drop.Data.Files(); err == nil && len(paths) > 0 {
			p.receiveAttachmentDrop(&mygo.FileDropEvent{Paths: paths, X: point.X, Y: point.Y})
		}
	}
}

func (p *panel) updateAttachmentDrag(state desktopAttachmentDrag) {
	if !state.Active || p.quitting || p.tab != "Chat" || p.serverDialogOpen ||
		p.chatView == nil || p.chatPresentedView != p.chatView {
		p.clearAttachmentDrag()
		return
	}
	if p.attachmentDragView == p.chatView && p.attachmentDrag == state {
		return
	}
	if p.attachmentDragView != p.chatView {
		p.clearAttachmentDrag()
	}
	p.attachmentDragView, p.attachmentDrag = p.chatView, state
	_ = desktopAttachmentDragged.EmitPage(p.chatView.Page(), state)
}

func (p *panel) clearAttachmentDrag() {
	if p.attachmentDragView == nil {
		return
	}
	_ = desktopAttachmentDragged.EmitPage(p.attachmentDragView.Page(), desktopAttachmentDrag{})
	p.attachmentDragView, p.attachmentDrag = nil, desktopAttachmentDrag{}
}

// Runs on the UI thread. Only an actual native drop supplies these paths; chat
// pages never receive a method for reading arbitrary local files.
func (p *panel) receiveAttachmentDrop(event *mygo.FileDropEvent) {
	if p.quitting || p.attachmentDropPending || p.tab != "Chat" || p.serverDialogOpen ||
		p.chatView == nil || p.chatPresentedView != p.chatView || len(event.Paths) == 0 {
		return
	}
	p.attachmentDropPending = true
	view, address := p.chatView, p.chatURL
	paths := append([]string(nil), event.Paths...)
	x, y := event.X, event.Y
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		// The chat WebView fills the native content area, so its CSS coordinates
		// are the window's drop coordinates. Reject overlays and non-composer UI.
		value, err := view.Page().EvalContext(ctx, fmt.Sprintf(`
const token = window.spoolAttachmentDrop?.claim(%d, %d);
return token ? JSON.stringify({token, url: location.href}) : "";`, x, y))
		var delivery desktopAttachmentDelivery
		if text, ok := value.(string); err == nil && ok && text != "" {
			err = json.Unmarshal([]byte(text), &delivery)
			if err == nil && delivery.Token != "" && chatNavigationAllowed(address, delivery.URL) {
				delivery.Files, err = readDroppedAttachments(paths)
				if err != nil {
					delivery.Error = err.Error()
				}
			} else {
				delivery.Token = ""
			}
		}
		p.window.Update(func() {
			p.attachmentDropPending = false
			if p.quitting || p.tab != "Chat" || p.serverDialogOpen || p.chatView != view ||
				p.chatPresentedView != view || p.chatURL != address || delivery.Token == "" {
				return
			}
			_ = desktopAttachmentDropped.EmitPage(view.Page(), delivery)
		})
	}()
}

func readDroppedAttachments(paths []string) ([]desktopDroppedFile, error) {
	const maxFile = 4 * 1024 * 1024
	const maxTotal = 16 * 1024 * 1024
	if len(paths) > 6 {
		return nil, errors.New("Attach at most 6 files, up to 16 MB total")
	}
	files := make([]desktopDroppedFile, 0, len(paths))
	total := 0
	for _, path := range paths {
		file, err := readDroppedAttachment(path, maxFile)
		if err != nil {
			return nil, err
		}
		total += len(file.Data)
		if total > maxTotal {
			return nil, errors.New("Attach at most 6 files, up to 16 MB total")
		}
		files = append(files, file)
	}
	return files, nil
}

func readDroppedAttachment(path string, limit int64) (desktopDroppedFile, error) {
	unreadable := errors.New("Could not read the dropped files")
	invalidSize := errors.New("Each attachment must be nonempty and at most 4 MB")
	if !filepath.IsAbs(path) {
		return desktopDroppedFile{}, unreadable
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return desktopDroppedFile{}, unreadable
	}
	if info.Size() == 0 || info.Size() > limit {
		return desktopDroppedFile{}, invalidSize
	}
	file, err := os.Open(path)
	if err != nil {
		return desktopDroppedFile{}, unreadable
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return desktopDroppedFile{}, unreadable
	}
	if len(data) == 0 || int64(len(data)) > limit {
		return desktopDroppedFile{}, invalidSize
	}
	mediaType, _, _ := mime.ParseMediaType(mime.TypeByExtension(filepath.Ext(path)))
	if mediaType == "" {
		mediaType = http.DetectContentType(data)
	}
	return desktopDroppedFile{filepath.Base(path), mediaType, data, info.ModTime().UnixMilli()}, nil
}
