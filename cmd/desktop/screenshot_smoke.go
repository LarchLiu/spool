package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"time"

	"github.com/egoist/mygo"
	"spool/internal/screencapture"
)

// Model PermissionFlow's cancellation cleanup without opening System Settings
// or requesting access. Quitting must leave the UI thread available for cleanup.
func (p *panel) prepareScreenshotPermissionQuitProbe(ctx context.Context, finish func(error)) error {
	started := make(chan struct{})
	cleaned := false // Written and checked only on the native UI thread.
	if err := p.rendererSmokeUI(ctx, func() error {
		p.authorizeScreenshot(p.chatView, screenshotRequest{Token: "permission-quit-probe"}, screenshotCapture{
			authorize: func(ctx context.Context) error {
				close(started)
				<-ctx.Done()
				mygo.RunOnMain(func() { cleaned = true })
				return ctx.Err()
			},
		})
		mygo.App.OnQuit(func() {
			if !cleaned {
				finish(fmt.Errorf("permission worker did not finish UI cleanup before quit"))
				return
			}
			finish(nil)
		})
		return nil
	}); err != nil {
		return err
	}
	select {
	case <-started:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Exercise the owned native panel and event bridge with a synthetic screen.
// This probe never captures or retains pixels from the developer's desktop.
func (p *panel) probeScreenshotSelection(ctx context.Context, report string) error {
	var bounds screencapture.Rect
	if err := p.rendererSmokeUI(ctx, func() error {
		b := mygo.Screen.PrimaryDisplay().Bounds
		bounds = screencapture.Rect{X: b.X, Y: b.Y, Width: b.Width, Height: b.Height}
		return nil
	}); err != nil {
		return err
	}
	snapshot, err := screenshotSmokeSnapshot([]screencapture.Display{{Bounds: bounds}})
	if err != nil {
		return err
	}
	choices := make(chan screenshotChoice, 1)
	if err := p.rendererSmokeUI(ctx, func() error { p.showScreenshotSelection(ctx, snapshot, choices); return nil }); err != nil {
		return err
	}
	defer p.window.Update(p.closeScreenshotWindows)
	var overlay *mygo.Window
	if err := p.rendererSmokeUI(ctx, func() error {
		if len(p.screenshotWindows) != 1 {
			return fmt.Errorf("missing screenshot overlay")
		}
		overlay = p.screenshotWindows[0]
		return nil
	}); err != nil {
		return err
	}
	if err := waitChatScript(ctx, overlay.Page(), `return document.querySelector('.capture-background')?.naturalWidth>0;`); err != nil {
		return fmt.Errorf("native screenshot surface: %w", err)
	}
	if err := p.waitDesktopServerUI(ctx, func() bool { return overlay.IsVisible() }); err != nil {
		return err
	}
	focusContext, stopFocus := context.WithTimeout(ctx, 3*time.Second)
	defer stopFocus()
	if err := waitChatScript(focusContext, overlay.Page(), `return document.activeElement===document.querySelector('.capture-surface') && !document.querySelector('.capture-toolbar button:focus-visible');`); err != nil {
		state, _ := overlay.Page().EvalContext(ctx, `JSON.stringify({active:document.activeElement?.outerHTML?.slice(0,200),ring:document.querySelector('.capture-toolbar button:focus-visible')?.getAttribute('aria-label')})`)
		return fmt.Errorf("native screenshot initial focus: %w; state=%v", err, state)
	}
	if err := p.rendererSmokeUI(ctx, func() error {
		want := mygo.Rectangle{X: bounds.X, Y: bounds.Y, Width: bounds.Width, Height: bounds.Height}
		if frame, content := overlay.Bounds(), overlay.ContentBounds(); frame != want || content != want {
			return fmt.Errorf("screenshot overlay must cover the display exactly: display=%+v frame=%+v content=%+v", want, frame, content)
		}
		return nil
	}); err != nil {
		return err
	}
	if err := waitChatScript(ctx, overlay.Page(), fmt.Sprintf(`return innerWidth===%d && innerHeight===%d && document.querySelector('.capture-surface').getBoundingClientRect().top===0;`, bounds.Width, bounds.Height)); err != nil {
		return err
	}
	if _, err := overlay.Page().EvalContext(ctx, `
const root=document.querySelector('.capture-surface');
root.addEventListener('pointermove',event=>{if(event.isTrusted)event.stopImmediatePropagation();},true);
root.dispatchEvent(new PointerEvent('pointermove',{bubbles:true,clientX:200,clientY:200,pointerId:1}));
`); err != nil {
		return err
	}
	hoverContext, stopHover := context.WithTimeout(ctx, 3*time.Second)
	defer stopHover()
	if err := waitChatScript(hoverContext, overlay.Page(), `return document.querySelector('.capture-size')?.textContent==='Fixture app · 500 × 350';`); err != nil {
		state, _ := overlay.Page().EvalContext(ctx, `JSON.stringify({label:document.querySelector('.capture-size')?.textContent,bounds:window.spoolScreenshot.bounds,windows:window.spoolScreenshot.windows,rect:document.querySelector('.capture-surface').getBoundingClientRect().toJSON()})`)
		return fmt.Errorf("native window hover: %w; state=%v", err, state)
	}
	picture, err := overlay.CapturePage()
	if err != nil {
		return err
	}
	if err = os.WriteFile(report+"-screenshot.png", picture, 0o600); err != nil {
		return err
	}
	// Even a page with the session token may not submit pixels unless it is one
	// of this session's owned overlay pages.
	selectionToken, err := overlay.Page().EvalContext(ctx, `window.spoolScreenshot.token`)
	if err != nil {
		return err
	}
	if err := waitChatScript(ctx, p.chatView.Page(), fmt.Sprintf(`return await window.mygo.call('DesktopScreenshot.Stage',%q,%q).then(()=>false,()=>true);`, selectionToken, base64.StdEncoding.EncodeToString(snapshot.Screens[0].PNG))); err != nil {
		return fmt.Errorf("chat page gained screenshot export access: %w", err)
	}
	if err := waitChatScript(ctx, overlay.Page(), `return await window.mygo.call('DesktopScreenshot.Stage','expired-token','eA==').then(()=>false,()=>true);`); err != nil {
		return fmt.Errorf("expired screenshot export token accepted: %w", err)
	}
	if err := waitChatScript(ctx, p.chatView.Page(), fmt.Sprintf(`return await window.mygo.call('DesktopScreenshot.Load',%q).then(()=>false,()=>true);`, selectionToken)); err != nil {
		return fmt.Errorf("chat page gained screenshot source access: %w", err)
	}
	if err := waitChatScript(ctx, overlay.Page(), `return await window.mygo.call('DesktopScreenshot.Load','expired-token').then(()=>false,()=>true);`); err != nil {
		return fmt.Errorf("expired screenshot source token accepted: %w", err)
	}
	if err := editSmokeScreenshot(ctx, overlay.Page(), "region", 200, 140); err != nil {
		return fmt.Errorf("initial screenshot editor: %w", err)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case choice := <-choices:
		config, err := png.DecodeConfig(bytes.NewReader(choice.Data))
		if err != nil || config.Width != 200 || config.Height != 140 || choice.Cancel || choice.Error != nil {
			return fmt.Errorf("unexpected screenshot choice: %+v", choice)
		}
		edited, err := png.Decode(bytes.NewReader(choice.Data))
		if err != nil {
			return err
		}
		background := color.NRGBAModel.Convert(edited.At(5, 5)).(color.NRGBA)
		stroke := color.NRGBAModel.Convert(edited.At(20, 30)).(color.NRGBA)
		if background != (color.NRGBA{R: 40, G: 70, B: 100, A: 255}) || stroke == background {
			return fmt.Errorf("native screenshot export lost its source pixels or rectangle annotation")
		}
	}
	page := p.chatView.Page()
	if _, err := page.EvalContext(ctx, `
document.querySelector('.composer-screenshot button').click();
document.addEventListener('click', event=>{
 const link=event.target.closest('.screenshot-options a');
 if(link){event.preventDefault();window.smokeScreenshotToken=new URL(link.href).searchParams.get('token');}
},{capture:true,once:true});
`); err != nil {
		return fmt.Errorf("open screenshot options: %w", err)
	}
	if err := waitChatScript(ctx, page, `return !!document.querySelector('.screenshot-options:not([hidden]) a');`); err != nil {
		return err
	}
	if _, err := page.EvalContext(ctx, `document.querySelector('.screenshot-options a').click();`); err != nil {
		return fmt.Errorf("select screenshot option: %w", err)
	}
	if err := waitChatScript(ctx, page, `return !!window.smokeScreenshotToken && document.querySelector('.composer-screenshot button')?.disabled;`); err != nil {
		return err
	}
	tokenValue, err := page.EvalContext(ctx, `window.smokeScreenshotToken`)
	if err != nil {
		return err
	}
	token, ok := tokenValue.(string)
	if !ok || token == "" {
		return fmt.Errorf("missing screenshot request token")
	}
	pngBytes, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err := p.rendererSmokeUI(ctx, func() error {
		return desktopScreenshotResult.EmitPage(page, screenshotResult{Token: token, Files: []desktopDroppedFile{{Name: "screenshot.png", Type: "image/png", Data: pngBytes, Modified: 123}}})
	}); err != nil {
		return err
	}
	if err := waitChatScript(ctx, page, `return document.querySelector('.composer img')?.naturalWidth>0 && !document.querySelector('.composer-screenshot button').disabled;`); err != nil {
		return err
	}
	if err := p.probeScreenshotNavigation(ctx); err != nil {
		return fmt.Errorf("screenshot navigation: %w", err)
	}
	if err := p.probeScreenshotVisibility(ctx, report); err != nil {
		return fmt.Errorf("screenshot visibility: %w", err)
	}
	return nil
}

// Match the production capture contract: one frozen image and window target
// per requested display, in the same order and desktop coordinates.
func screenshotSmokeSnapshot(displays []screencapture.Display) (*screencapture.Snapshot, error) {
	if len(displays) == 0 {
		return nil, fmt.Errorf("no displays supplied")
	}
	snapshot := &screencapture.Snapshot{}
	for i, display := range displays {
		bounds := display.Bounds
		if bounds.Width <= 0 || bounds.Height <= 0 {
			return nil, fmt.Errorf("invalid fixture display bounds: %+v", bounds)
		}
		img := image.NewNRGBA(image.Rect(0, 0, bounds.Width, bounds.Height))
		draw.Draw(img, img.Bounds(), image.NewUniform(color.NRGBA{R: 40, G: 70, B: 100 + uint8(i), A: 255}), image.Point{}, draw.Src)
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, img); err != nil {
			return nil, err
		}
		snapshot.Screens = append(snapshot.Screens, screencapture.Screen{Bounds: bounds, PNG: encoded.Bytes(), Image: img})
		snapshot.Windows = append(snapshot.Windows, screencapture.Window{
			ID: uint32(7 + i), Name: "Fixture app",
			Bounds: screencapture.Rect{X: bounds.X + 100, Y: bounds.Y + 100, Width: min(500, bounds.Width-100), Height: min(350, bounds.Height-100)},
		})
	}
	return snapshot, nil
}

// Exercise the production lifecycle with synthetic pixels and window metadata.
// Both visibility actions must offer the same full-display/window selection.
func (p *panel) probeScreenshotVisibility(ctx context.Context, report string) error {
	page := p.chatView.Page()
	type startupTiming struct {
		Visibility       string `json:"visibility"`
		Action           string `json:"action"`
		CaptureStartMS   int64  `json:"capture_start_ms"`
		SelectionReadyMS int64  `json:"selection_ready_ms"`
		Displays         int    `json:"displays"`
		Display          int    `json:"display"`
	}
	var timings []startupTiming
	for stepIndex, step := range []struct{ visibility, action string }{{"hide", "region"}, {"show", "window"}, {"hide", "cancel"}, {"show", "region"}, {"hide", "startup-cancel"}} {
		selector := ".composer-screenshot > a"
		if step.visibility == "show" {
			if _, err := page.EvalContext(ctx, `document.querySelector('.composer-screenshot button').click();`); err != nil {
				return err
			}
			if err := waitChatScript(ctx, page, `return !!document.querySelector('.screenshot-options:not([hidden]) a');`); err != nil {
				return err
			}
			selector = ".screenshot-options a"
		}
		if _, err := page.EvalContext(ctx, `
window.smokeVisibilityURL='';window.smokeScreenshotReplies=[];
document.addEventListener('click',event=>{
 const link=event.target.closest('.composer-screenshot a');
 if(link){event.preventDefault();window.smokeVisibilityURL=link.href;}
},{capture:true,once:true});
`); err != nil {
			return err
		}
		if _, err := page.EvalContext(ctx, `document.querySelector(`+fmt.Sprintf("%q", selector)+`).click();`); err != nil {
			return err
		}
		value, err := page.EvalContext(ctx, `window.smokeVisibilityURL`)
		if err != nil {
			return err
		}
		target, _ := value.(string)
		request, reserved, valid := screenshotNavigation(p.chatURL, target)
		if !reserved || !valid || request.Visibility != step.visibility {
			return fmt.Errorf("invalid visibility request: %v", value)
		}
		permissionChecked := false
		type captureStart struct {
			at       time.Time
			displays []screencapture.Display
		}
		captureStarted := make(chan captureStart, 1)
		captureStopped := make(chan struct{})
		capture := screenshotCapture{
			permission: func() bool { permissionChecked = true; return true },
			displays: func(captureContext context.Context, displays []screencapture.Display) (*screencapture.Snapshot, error) {
				captureStarted <- captureStart{time.Now(), displays}
				defer close(captureStopped)
				if step.action == "startup-cancel" {
					<-captureContext.Done()
					return nil, captureContext.Err()
				}
				if len(displays) == 0 {
					return nil, fmt.Errorf("no displays supplied")
				}
				err := p.rendererSmokeUI(captureContext, func() error {
					if p.window.IsVisible() != (step.visibility == "show") {
						return fmt.Errorf("screenshot %s has incorrect main window visibility", step.visibility)
					}
					return nil
				})
				if err != nil {
					return nil, err
				}
				return screenshotSmokeSnapshot(displays)
			},
		}
		started := time.Now()
		if err := p.rendererSmokeUI(ctx, func() error {
			p.takeScreenshotWithCapture(p.chatView, request, capture)
			if !permissionChecked {
				return fmt.Errorf("screenshot %s skipped display capture permission", step.visibility)
			}
			return nil
		}); err != nil {
			return err
		}
		var captureStartState captureStart
		select {
		case captureStartState = <-captureStarted:
		case <-ctx.Done():
			return ctx.Err()
		}
		displays := captureStartState.displays
		if len(displays) == 0 {
			return fmt.Errorf("no displays supplied")
		}
		if step.action == "startup-cancel" {
			var overlays []*mygo.Window
			if err := p.rendererSmokeUI(ctx, func() error {
				if len(p.screenshotWindows) != len(displays) {
					return fmt.Errorf("pending screenshot editor must preload on all %d displays", len(displays))
				}
				for _, overlay := range p.screenshotWindows {
					if overlay.IsVisible() {
						return fmt.Errorf("pending screenshot editor must preload while hidden")
					}
				}
				overlays = append(overlays, p.screenshotWindows...)
				return nil
			}); err != nil {
				return err
			}
			for _, overlay := range overlays {
				if err := waitChatScript(ctx, overlay.Page(), `return document.readyState==='complete' && window.spoolScreenshot?.image==='' && !document.querySelector('.capture-surface');`); err != nil {
					return fmt.Errorf("hidden screenshot preload did not finish during capture: %w", err)
				}
			}
			overlay := overlays[len(overlays)-1]
			if _, err := overlay.Page().EvalContext(ctx, `setTimeout(() => location.assign('mygo://app/__screenshot?'+new URLSearchParams({token:window.spoolScreenshot.token,action:'cancel'})), 0);`); err != nil {
				return err
			}
			if err := waitChatScript(ctx, page, `return !document.querySelector('.composer-screenshot button').disabled && window.smokeScreenshotReplies.length===1;`); err != nil {
				return err
			}
			if err := waitChatScript(ctx, page, `return document.hasFocus() && document.activeElement===document.querySelector('#chat-message');`); err != nil {
				return fmt.Errorf("startup cancellation did not return focus to the composer: %w", err)
			}
			select {
			case <-captureStopped:
			default:
				return fmt.Errorf("startup cancellation restored main window before capture stopped")
			}
			if err := p.rendererSmokeUI(ctx, func() error {
				if !p.window.IsVisible() || p.screenshotCancel != nil || len(p.screenshotWindows) != 0 {
					return fmt.Errorf("startup cancellation did not restore its main window and settle")
				}
				return nil
			}); err != nil {
				return err
			}
			continue
		}
		var overlay *mygo.Window
		displayIndex := stepIndex % len(displays)
		if err := p.waitDesktopServerUI(ctx, func() bool {
			if len(p.screenshotWindows) != len(displays) {
				return false
			}
			for _, window := range p.screenshotWindows {
				if !window.IsVisible() {
					return false
				}
			}
			overlay = p.screenshotWindows[displayIndex]
			return true
		}); err != nil {
			return fmt.Errorf("screenshot %s surface: %w", step.visibility, err)
		}
		if err := p.rendererSmokeUI(ctx, func() error {
			for i, window := range p.screenshotWindows {
				bounds := displays[i].Bounds
				want := mygo.Rectangle{X: bounds.X, Y: bounds.Y, Width: bounds.Width, Height: bounds.Height}
				if window.Bounds() != want || window.ContentBounds() != want {
					return fmt.Errorf("screenshot display %d must cover its display exactly", i+1)
				}
			}
			return nil
		}); err != nil {
			return err
		}
		timings = append(timings, startupTiming{step.visibility, step.action, captureStartState.at.Sub(started).Milliseconds(), time.Since(started).Milliseconds(), len(displays), displayIndex + 1})
		if step.action == "cancel" {
			if _, err := overlay.Page().EvalContext(ctx, `setTimeout(() => location.assign('mygo://app/__screenshot?'+new URLSearchParams({token:window.spoolScreenshot.token,action:'cancel'})), 0);`); err != nil {
				return err
			}
		} else if err := editSmokeScreenshot(ctx, overlay.Page(), step.action, 100, 100); err != nil {
			return err
		}
		if err := waitChatScript(ctx, page, `return !document.querySelector('.composer-screenshot button').disabled && window.smokeScreenshotReplies.length===1;`); err != nil {
			return err
		}
		if err := waitChatScript(ctx, page, `return document.hasFocus() && document.activeElement===document.querySelector('#chat-message');`); err != nil {
			return fmt.Errorf("screenshot %s %s did not return focus to the composer: %w", step.visibility, step.action, err)
		}
		if err := p.rendererSmokeUI(ctx, func() error {
			if !p.window.IsVisible() || p.screenshotCancel != nil || len(p.screenshotWindows) != 0 {
				return fmt.Errorf("screenshot %s did not restore its main window and settle", step.visibility)
			}
			return nil
		}); err != nil {
			return err
		}
		if step.action == "cancel" {
			if err := waitChatScript(ctx, page, `return !window.smokeScreenshotReplies[0].files && !window.smokeScreenshotReplies[0].error;`); err != nil {
				return err
			}
			continue
		}
		width, height := 100, 100
		if step.action == "window" {
			bounds := displays[displayIndex].Bounds
			width, height = min(500, bounds.Width-100), min(350, bounds.Height-100)
		}
		script := fmt.Sprintf(`return await new Promise(resolve=>{const image=new Image();image.onload=()=>resolve(image.naturalWidth===%d && image.naturalHeight===%d);image.onerror=()=>resolve(false);image.src='data:image/png;base64,'+window.smokeScreenshotReplies[0].files?.[0]?.data;});`, width, height)
		if err := waitChatScript(ctx, page, script); err != nil {
			return fmt.Errorf("screenshot %s %s image: %w", step.visibility, step.action, err)
		}
	}
	data, err := json.MarshalIndent(timings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(report+"-timings.json", append(data, '\n'), 0o600)
}

func editSmokeScreenshot(ctx context.Context, page *mygo.Page, action string, width, height int) error {
	script := fmt.Sprintf(`
const root=document.querySelector('.capture-surface');
root.setPointerCapture=()=>{};
root.addEventListener('pointermove',event=>{if(event.isTrusted)event.stopImmediatePropagation();},true);
const pointer=(type,x,y)=>root.dispatchEvent(new PointerEvent(type,{bubbles:true,clientX:x,clientY:y,pointerId:41,button:0}));
pointer('pointerdown',150,120);
if(%q!=='window')pointer('pointermove',150+%d,120+%d);
pointer('pointerup',150+%d,120+%d);
`, action, width, height, width, height)
	if _, err := page.EvalContext(ctx, script); err != nil {
		return err
	}
	selectionContext, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := waitChatScript(selectionContext, page, `return !!document.querySelector('.capture-preview')?.width && !!document.querySelector('button[data-tool="rect"]');`); err != nil {
		state, _ := page.EvalContext(ctx, `JSON.stringify({selected:!!document.querySelector('.editable'),size:document.querySelector('.capture-size')?.textContent,tools:[...document.querySelectorAll('.capture-toolbar button')].map(button=>button.getAttribute('aria-label'))})`)
		return fmt.Errorf("screenshot editor selection: %w; state=%v", err, state)
	}
	// Exercise the same renderer and IPC export used by a user after editing.
	if _, err := page.EvalContext(ctx, `document.querySelector('button[data-tool="rect"]').click();`); err != nil {
		return err
	}
	if err := waitChatScript(ctx, page, `return document.querySelector('button[data-tool="rect"]').getAttribute('aria-pressed')==='true';`); err != nil {
		return err
	}
	if _, err := page.EvalContext(ctx, `
const root=document.querySelector('.capture-surface');
const pointer=(type,x,y)=>root.dispatchEvent(new PointerEvent(type,{bubbles:true,clientX:x,clientY:y,pointerId:41,button:0}));
pointer('pointerdown',170,140);pointer('pointermove',210,170);pointer('pointerup',210,170);
`); err != nil {
		return err
	}
	if err := waitChatScript(ctx, page, `return !document.querySelector('button[data-action="undo"]').disabled;`); err != nil {
		return err
	}
	// Completing closes this page. Let Eval settle before the click so the
	// probe observes the resulting choice instead of racing page destruction.
	_, err := page.EvalContext(ctx, `setTimeout(() => document.querySelector('.capture-confirm').click(), 0);`)
	return err
}

// Let the real clicked links reach WKWebView's navigation delegate and the
// production screenshot handler. Simulate an existing capture so the handler
// replies without requesting permission or capturing the developer's desktop.
func (p *panel) probeScreenshotNavigation(ctx context.Context) error {
	page := p.chatView.Page()
	type navigation struct {
		request screenshotRequest
		gesture bool
	}
	requests := make(chan navigation, 3)
	if err := p.rendererSmokeUI(ctx, func() error {
		p.window.Show()
		done := make(chan struct{})
		close(done)
		p.screenshotCancel, p.screenshotDone = func() {}, done
		return nil
	}); err != nil {
		return err
	}
	defer p.window.Update(func() { p.screenshotCancel, p.screenshotDone = nil, nil })
	off := page.OnWillNavigate(func(event *mygo.NavigateEvent) {
		if request, reserved, ok := screenshotNavigation(p.chatURL, event.URL); reserved && ok {
			requests <- navigation{request, event.UserInitiated}
		}
	})
	defer off()
	if _, err := page.EvalContext(ctx, `
window.smokeScreenshotReplies=[];
window.addEventListener('spool:screenshot',event=>window.smokeScreenshotReplies.push(event.detail));
window.smokeScreenshotPointer=[];
for(const type of ['pointerdown','mousedown','pointerup','mouseup','click','focusin','focusout']) {
 document.addEventListener(type,event=>{
  window.smokeScreenshotPointer.push({type,trusted:event.isTrusted,target:event.target.tagName,composer:!!event.target.closest?.('.composer-screenshot'),related:event.relatedTarget?.tagName,active:document.activeElement?.tagName,hidden:document.querySelector('.screenshot-options')?.hidden});
 },true);
}
`); err != nil {
		return err
	}
	for _, visibility := range []string{"hide", "show", "hide"} {
		selector := `.composer-screenshot > a`
		if visibility == "show" {
			selector = `.screenshot-options a`
			if err := p.clickSmokeElement(ctx, ".composer-screenshot button"); err != nil {
				return err
			}
			if err := waitChatScript(ctx, page, `return !!document.querySelector('.screenshot-options:not([hidden]) a');`); err != nil {
				return err
			}
		}
		if err := p.clickSmokeElement(ctx, selector); err != nil {
			return err
		}
		timer := time.NewTimer(3 * time.Second)
		var request screenshotRequest
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
			state, _ := page.EvalContext(ctx, `return JSON.stringify({events:window.smokeScreenshotPointer,busy:document.querySelector('.composer-screenshot > a')?.getAttribute('aria-busy'),hidden:document.querySelector('.screenshot-options')?.hidden});`)
			return fmt.Errorf("screenshot %s link did not reach native navigation: %v", visibility, state)
		case got := <-requests:
			timer.Stop()
			if got.request.Visibility != visibility || !got.gesture {
				return fmt.Errorf("screenshot %s native navigation: %+v", visibility, got)
			}
			request = got.request
		}
		if err := waitChatScript(ctx, page, fmt.Sprintf(`return !document.querySelector('.composer-screenshot button').disabled && window.smokeScreenshotReplies.some(reply=>reply.token===%q && reply.error==='A screenshot is already in progress');`, request.Token)); err != nil {
			return fmt.Errorf("screenshot %s native response: %w", visibility, err)
		}
	}
	return nil
}
