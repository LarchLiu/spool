package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image/png"
	"net/url"
	"time"

	"github.com/egoist/mygo"
	"github.com/google/uuid"

	"spool/internal/screencapture"
)

const desktopScreenshotPath = "/__desktop/screenshot"
const screenshotPageURL = "mygo://app/screenshot.html"

type screenshotRequest struct {
	Token      string `json:"token"`
	Visibility string `json:"visibility"`
}
type screenshotResult struct {
	Token string               `json:"token"`
	Files []desktopDroppedFile `json:"files,omitempty"`
	Error string               `json:"error,omitempty"`
}
type screenshotChoice struct {
	Data   []byte
	Cancel bool
	Error  error
}

var desktopScreenshotResult = mygo.NewEvent[screenshotResult]("spool:screenshot")

func screenshotNavigation(address, target string) (screenshotRequest, bool, bool) {
	if !chatNavigationAllowed(address, target) {
		return screenshotRequest{}, false, false
	}
	u, _ := url.Parse(target)
	if u.Path != desktopScreenshotPath {
		return screenshotRequest{}, false, false
	}
	params, err := url.ParseQuery(u.RawQuery)
	request := screenshotRequest{params.Get("token"), params.Get("visibility")}
	_, tokenErr := uuid.Parse(request.Token)
	valid := err == nil && u.RawPath == "" && u.Fragment == "" && len(params) == 2 &&
		len(params["token"]) == 1 && len(params["visibility"]) == 1 && len(request.Token) == 36 && tokenErr == nil &&
		(request.Visibility == "hide" || request.Visibility == "show")
	return request, true, valid
}

// Image data goes through the caller-checked binding, never through a URL.
func screenshotChoiceNavigation(target, token string) (string, screenshotChoice, bool) {
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "mygo" || u.Host != "app" || u.User != nil || u.Path != "/__screenshot" || u.RawPath != "" || u.Fragment != "" {
		return "", screenshotChoice{}, false
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || q.Get("token") != token {
		return "", screenshotChoice{}, false
	}
	for _, values := range q {
		if len(values) != 1 {
			return "", screenshotChoice{}, false
		}
	}
	action := q.Get("action")
	switch action {
	case "ready", "cancel", "complete":
		return action, screenshotChoice{Cancel: action == "cancel"}, len(q) == 2
	}
	return "", screenshotChoice{}, false
}

// Runs on the UI thread, only for a clicked link in the foreground chat page.
func (p *panel) takeScreenshot(view *mygo.WebView, request screenshotRequest) {
	p.takeScreenshotWithCapture(view, request, screenshotCapture{
		permission:   screencapture.RequestPermission,
		displays:     screencapture.CaptureDisplays,
		systemPicker: screencapture.SystemPicker(),
	})
}

type screenshotCapture struct {
	permission   func() bool
	displays     func(context.Context, []screencapture.Display) (*screencapture.Snapshot, error)
	systemPicker bool
}

func (p *panel) takeScreenshotWithCapture(view *mygo.WebView, request screenshotRequest, capture screenshotCapture) {
	if p.quitting || !screencapture.Supported() || p.chatView != view || p.chatPresentedView != view || p.tab != "Chat" || p.serverDialogOpen || !p.window.IsVisible() {
		if view != nil && !view.IsDestroyed() {
			_ = desktopScreenshotResult.EmitPage(view.Page(), screenshotResult{Token: request.Token})
		}
		return
	}
	if p.screenshotCancel != nil {
		_ = desktopScreenshotResult.EmitPage(view.Page(), screenshotResult{Token: request.Token, Error: "A screenshot is already in progress"})
		return
	}
	if !capture.permission() {
		_ = desktopScreenshotResult.EmitPage(view.Page(), screenshotResult{Token: request.Token, Error: screencapture.ErrFailed.Error()})
		return
	}
	var displays []screencapture.Display
	for i, display := range mygo.Screen.Displays() {
		b := display.Bounds
		displays = append(displays, screencapture.Display{Index: i + 1, ID: display.ID, Scale: display.ScaleFactor, Bounds: screencapture.Rect{X: b.X, Y: b.Y, Width: b.Width, Height: b.Height}})
	}
	displays = screencapture.PrepareDisplays(displays)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	done := make(chan struct{})
	p.screenshotCancel, p.screenshotDone = cancel, done
	address := p.chatURL
	if request.Visibility == "hide" {
		p.window.Hide()
	}
	type captureResult struct {
		snapshot *screencapture.Snapshot
		content  []screenshotContent
		err      error
	}
	captured := make(chan captureResult, 1)
	go func() {
		// Window hiding can finish before the compositor drops its pixels. Allow
		// it to settle; 50 ms can leave Spool visible in the frozen desktop.
		// Keeping Spool visible requires no settling delay.
		if request.Visibility == "hide" {
			timer := time.NewTimer(250 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				captured <- captureResult{err: ctx.Err()}
				return
			case <-timer.C:
			}
		}
		snapshot, err := capture.displays(ctx, displays)
		if err == nil && (snapshot == nil || len(snapshot.Screens) == 0 || (!capture.systemPicker && len(snapshot.Screens) != len(displays)) || (capture.systemPicker && (!snapshot.Picked || len(snapshot.Screens) != 1))) {
			err = screencapture.ErrFailed
		}
		result := captureResult{snapshot: snapshot, err: err}
		if err == nil {
			result.content = screenshotContents(snapshot)
		}
		captured <- result
	}()
	// Load the hidden WebViews while the compositor settles and the OS captures
	// the displays. Only small metadata goes in the preload, never the PNG.
	choices := make(chan screenshotChoice, 1)
	var editor *screenshotEditorSession
	if !capture.systemPicker {
		editor = p.prepareScreenshotSelection(ctx, displays, choices, false)
	}
	go func() {
		var data []byte
		var err error
		var choice screenshotChoice
		captureReceived := false
		select {
		case <-ctx.Done():
			err = ctx.Err()
		case choice = <-choices:
		case result := <-captured:
			captureReceived = true
			err = result.err
			if err == nil {
				p.window.Update(func() {
					if !p.quitting && ctx.Err() == nil {
						if result.snapshot.Picked {
							b := result.snapshot.Screens[0].Bounds
							editor = p.prepareScreenshotSelection(ctx, []screencapture.Display{{Bounds: b, Selection: result.snapshot.Screens[0].Selection}}, choices, true)
						}
						p.loadScreenshotSelection(editor, result.snapshot, result.content)
					}
				})
				select {
				case <-ctx.Done():
					err = ctx.Err()
				case choice = <-choices:
				}
			}
		}
		if choice.Error != nil {
			err = choice.Error
		} else if !choice.Cancel {
			data = choice.Data
		}
		cancel()
		if !captureReceived {
			<-captured // Wait for cancelled OS captures and their temporary files.
		}
		close(done) // No subprocess or temporary capture files remain.
		p.window.Update(func() {
			p.closeScreenshotWindows()
			p.screenshotCancel, p.screenshotDone = nil, nil
			if p.quitting {
				return
			}
			p.showMainWindow()
			result := screenshotResult{Token: request.Token}
			if p.chatView == view && p.chatPresentedView == view && p.chatURL == address && p.tab == "Chat" && !p.serverDialogOpen {
				view.Focus()
				if err != nil && !errors.Is(err, context.Canceled) {
					result.Error = screencapture.ErrFailed.Error()
					if err == screencapture.ErrTooLarge {
						result.Error = err.Error()
					} else if errors.Is(err, context.DeadlineExceeded) {
						result.Error = "The screenshot timed out. Try again."
					}
				} else if len(data) > 0 {
					now := time.Now()
					result.Files = []desktopDroppedFile{{Name: "screenshot-" + now.Format("20060102-150405") + ".png", Type: "image/png", Data: data, Modified: now.UnixMilli()}}
				}
			}
			if !view.IsDestroyed() {
				_ = desktopScreenshotResult.EmitPage(view.Page(), result)
			}
		})
	}()
}

func (p *panel) closeScreenshotWindows() {
	p.screenshotEditor = nil
	windows := p.screenshotWindows
	p.screenshotWindows = nil
	for _, window := range windows {
		if !window.IsDestroyed() {
			window.Close()
		}
	}
}

func (p *panel) showScreenshotSelection(ctx context.Context, snapshot *screencapture.Snapshot, choices chan<- screenshotChoice) {
	displays := make([]screencapture.Display, len(snapshot.Screens))
	for i, screen := range snapshot.Screens {
		displays[i] = screencapture.Display{Index: i + 1, Bounds: screen.Bounds, Selection: screen.Selection}
	}
	editor := p.prepareScreenshotSelection(ctx, displays, choices, snapshot.Picked)
	p.loadScreenshotSelection(editor, snapshot, screenshotContents(snapshot))
}

func (p *panel) prepareScreenshotSelection(ctx context.Context, displays []screencapture.Display, choices chan<- screenshotChoice, picked bool) *screenshotEditorSession {
	token := uuid.NewString()
	editor := &screenshotEditorSession{ctx: ctx, token: token, pages: make(map[*mygo.Page]*screenshotExport)}
	p.screenshotEditor = editor
	cursor := mygo.Screen.CursorScreenPoint()
	ready := map[*mygo.Window]bool{}
	finished := false
	finish := func(choice screenshotChoice) {
		if finished {
			return
		}
		finished = true
		p.closeScreenshotWindows()
		choices <- choice
	}
	editor.finish = finish
	for _, display := range displays {
		bounds := display.Bounds
		cursorX, cursorY := screencapture.LocalCursor(display, cursor.X, cursor.Y)
		theme := "light"
		if mygo.Theme.IsDark() {
			theme = "dark"
		}
		payload, err := json.Marshal(map[string]any{
			"token": token, "bounds": bounds, "windows": []screencapture.Window{},
			"image":     "",
			"cursor":    map[string]int{"x": cursorX, "y": cursorY},
			"selected":  picked,
			"selection": display.Selection,
			"locale":    mygo.App.Locale(),
			"theme":     theme,
		})
		if err != nil {
			finish(screenshotChoice{Error: screencapture.ErrFailed})
			return editor
		}
		window := mygo.NewWindow(mygo.WindowOptions{
			Title: "Spool Screenshot", X: bounds.X, Y: bounds.Y, Width: bounds.Width, Height: bounds.Height,
			Hidden: true, Frameless: true, Panel: !picked, AlwaysOnTop: true, DisableResize: true, DisableMove: true,
			DisableShadow: true, DisableMinimize: true, DisableMaximize: true, SkipTaskbar: true,
			Page: mygo.PageOptions{PreloadScript: "Object.defineProperty(window, 'spoolScreenshot', {value:" + string(payload) + "});", DevTools: mygo.DevToolsDisabled},
		})
		if !picked {
			screencapture.ConfigureOverlay(window.NativeHandle())
		}
		window.SetMovable(true) // MyGo's Windows WM_WINDOWPOSCHANGING otherwise blocks programmatic moves too.
		window.SetBounds(mygo.Rectangle{X: bounds.X, Y: bounds.Y, Width: bounds.Width, Height: bounds.Height})
		if !picked {
			screencapture.PositionOverlay(window.NativeHandle(), display)
		}
		window.SetMovable(false)
		p.screenshotWindows = append(p.screenshotWindows, window)
		editor.pages[window.Page()] = &screenshotExport{bounds: bounds, ready: make(chan struct{})}
		window.OnClosed(func() { finish(screenshotChoice{Cancel: true}) })
		window.Page().SetWindowOpenHandler(func(mygo.WindowOpenRequest) *mygo.WindowOptions { return nil })
		window.Page().OnWillNavigate(func(event *mygo.NavigateEvent) {
			if event.URL == screenshotPageURL {
				return
			}
			event.PreventDefault()
			action, choice, ok := screenshotChoiceNavigation(event.URL, token)
			if !ok || finished || ctx.Err() != nil {
				return
			}
			if action != "ready" {
				if action == "complete" {
					export := editor.pages[window.Page()]
					if len(export.data) == 0 {
						return
					}
					choice.Data = export.data
				}
				finish(choice)
				return
			}
			if editor.pages[window.Page()].width == 0 {
				return
			}
			ready[window] = true
			if len(ready) == len(displays) {
				for _, overlay := range p.screenshotWindows {
					overlay.Show()
				}
				for i, display := range displays {
					x, y := screencapture.LocalCursor(display, cursor.X, cursor.Y)
					if picked || imagePointInRect(x, y, screencapture.Rect{Width: display.Bounds.Width, Height: display.Bounds.Height}) {
						p.screenshotWindows[i].Focus()
						break
					}
				}
			}
		})
		window.Page().OnDidFailLoad(func(*mygo.LoadError) { finish(screenshotChoice{Error: screencapture.ErrFailed}) })
		if err := window.Page().LoadURL(screenshotPageURL); err != nil {
			finish(screenshotChoice{Error: screencapture.ErrFailed})
			return editor
		}
	}
	return editor
}

type screenshotContent struct {
	Image   string                 `json:"image"`
	Windows []screencapture.Window `json:"windows"`
}

func screenshotContents(snapshot *screencapture.Snapshot) []screenshotContent {
	content := make([]screenshotContent, len(snapshot.Screens))
	for i, screen := range snapshot.Screens {
		windows := screen.Windows
		if windows == nil {
			windows = snapshot.Windows
		}
		if windows == nil {
			windows = []screencapture.Window{}
		}
		content[i] = screenshotContent{Image: "data:image/png;base64," + base64.StdEncoding.EncodeToString(screen.PNG), Windows: windows}
	}
	return content
}

func (p *panel) loadScreenshotSelection(editor *screenshotEditorSession, snapshot *screencapture.Snapshot, content []screenshotContent) {
	if p.screenshotEditor != editor || editor.ctx.Err() != nil {
		return
	}
	if len(snapshot.Screens) != len(p.screenshotWindows) || len(content) != len(snapshot.Screens) {
		editor.finish(screenshotChoice{Error: screencapture.ErrFailed})
		return
	}
	for i, window := range p.screenshotWindows {
		export, screen := editor.pages[window.Page()], snapshot.Screens[i]
		size := screen.PixelSize()
		if export.bounds != screen.Bounds || size.X <= 0 || size.Y <= 0 || export.width != 0 {
			editor.finish(screenshotChoice{Error: screencapture.ErrFailed})
			return
		}
		export.width, export.height, export.content = size.X, size.Y, content[i]
		close(export.ready)
	}
}

type desktopScreenshot struct{ panel *panel }
type screenshotEditorSession struct {
	ctx    context.Context
	token  string
	pages  map[*mygo.Page]*screenshotExport
	finish func(screenshotChoice)
}
type screenshotExport struct {
	width, height int
	data          []byte
	bounds        screencapture.Rect
	content       screenshotContent
	ready         chan struct{}
}

// Load waits off the UI thread for captured pixels while the hidden editor
// loads its JS and CSS. Only this session's owned page may read its display.
func (s *desktopScreenshot) Load(ctx context.Context, token string) (screenshotContent, error) {
	type pending struct {
		export  *screenshotExport
		session context.Context
	}
	load, err := screenshotEditorUI(ctx, s.panel, token, func(export *screenshotExport) pending {
		return pending{export, s.panel.screenshotEditor.ctx}
	})
	if err != nil {
		return screenshotContent{}, err
	}
	select {
	case <-ctx.Done():
		return screenshotContent{}, ctx.Err()
	case <-load.session.Done():
		return screenshotContent{}, load.session.Err()
	case <-load.export.ready:
		return load.export.content, nil
	}
}

// All session state stays on the UI thread. Chat pages (including local dev
// pages) cannot submit pixels, and closing an overlay invalidates its token.
func screenshotEditorUI[T any](ctx context.Context, p *panel, token string, fn func(*screenshotExport) T) (T, error) {
	type result struct {
		value T
		err   error
	}
	var zero T
	if p == nil || p.window == nil {
		return zero, screencapture.ErrFailed
	}
	reply := make(chan result, 1)
	p.window.Update(func() {
		if err := ctx.Err(); err != nil {
			reply <- result{err: err}
			return
		}
		session, caller := p.screenshotEditor, mygo.CallerPage(ctx)
		if p.quitting || session == nil || session.ctx.Err() != nil || session.token != token || caller == nil || caller.URL() != screenshotPageURL || session.pages[caller] == nil {
			reply <- result{err: screencapture.ErrFailed}
			return
		}
		reply <- result{value: fn(session.pages[caller])}
	})
	select {
	case r := <-reply:
		return r.value, r.err
	case <-ctx.Done():
		return zero, ctx.Err()
	}
}

func (s *desktopScreenshot) Stage(ctx context.Context, token, encoded string) error {
	limits, err := screenshotEditorUI(ctx, s.panel, token, func(export *screenshotExport) [2]int {
		return [2]int{export.width, export.height}
	})
	if err != nil {
		return err
	}
	data, err := decodeScreenshotExport(encoded, limits[0], limits[1])
	if err != nil {
		return err
	}
	_, err = screenshotEditorUI(ctx, s.panel, token, func(export *screenshotExport) bool {
		export.data = data
		return true
	})
	return err
}

func decodeScreenshotExport(encoded string, width, height int) ([]byte, error) {
	const maxBytes = 4 * 1024 * 1024
	if len(encoded) > base64.StdEncoding.EncodedLen(maxBytes) {
		return nil, screencapture.ErrTooLarge
	}
	data, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(data) == 0 {
		return nil, screencapture.ErrFailed
	}
	if len(data) > maxBytes {
		return nil, screencapture.ErrTooLarge
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > width || config.Height > height || int64(config.Width)*int64(config.Height) > 100_000_000 {
		return nil, screencapture.ErrFailed
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		return nil, screencapture.ErrFailed
	}
	return data, nil
}

func imagePointInRect(x, y int, r screencapture.Rect) bool {
	return x >= r.X && x < r.X+r.Width && y >= r.Y && y < r.Y+r.Height
}
