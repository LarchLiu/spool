package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ebitengine/purego/objc"
)

type smokeMousePoint struct{ X, Y float64 }

// Send mouse events only to the owned fixture window. Unlike DOM .click(),
// this covers WebKit's hit testing, focus changes, and native mouse down/up.
// It does not move the system cursor or require Accessibility permission.
func (p *panel) clickSmokeElement(ctx context.Context, selector string) error {
	if err := p.rendererSmokeUI(ctx, func() error {
		p.window.Focus()
		p.chatView.Focus()
		return nil
	}); err != nil {
		return err
	}
	if err := waitChatScript(ctx, p.chatView.Page(), `return document.hasFocus();`); err != nil {
		return err
	}
	value, err := p.chatView.Page().EvalContext(ctx, fmt.Sprintf(`
await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));
const element=document.querySelector(%q);
const rect=element?.getBoundingClientRect();
if(!rect || !rect.width || !rect.height) throw new Error('Missing clickable fixture element');
return JSON.stringify({x:rect.x+rect.width/2,y:rect.y+rect.height/2,height:innerHeight});
`, selector))
	if err != nil {
		return err
	}
	var target struct{ X, Y, Height float64 }
	encoded, _ := value.(string)
	if err := json.Unmarshal([]byte(encoded), &target); err != nil {
		return err
	}
	var point smokeMousePoint
	if err := p.rendererSmokeUI(ctx, func() error {
		window := objc.ID(p.window.NativeHandle())
		var findWebView func(objc.ID) objc.ID
		findWebView = func(view objc.ID) objc.ID {
			if objc.Send[bool](view, objc.RegisterName("isHiddenOrHasHiddenAncestor")) {
				return 0
			}
			if objc.Send[bool](view, objc.RegisterName("isKindOfClass:"), objc.GetClass("WKWebView")) {
				return view
			}
			children := view.Send(objc.RegisterName("subviews"))
			for i, count := uintptr(0), uintptr(children.Send(objc.RegisterName("count"))); i < count; i++ {
				if found := findWebView(children.Send(objc.RegisterName("objectAtIndex:"), i)); found != 0 {
					return found
				}
			}
			return 0
		}
		web := findWebView(window.Send(objc.RegisterName("contentView")))
		if web == 0 {
			return fmt.Errorf("missing visible native fixture WebView")
		}
		local := smokeMousePoint{target.X, target.Y}
		if !objc.Send[bool](web, objc.RegisterName("isFlipped")) {
			local.Y = target.Height - local.Y
		}
		point = objc.Send[smokeMousePoint](web, objc.RegisterName("convertPoint:toView:"), local, objc.ID(0))
		return nil
	}); err != nil {
		return err
	}
	for _, kind := range []uint{5, 1, 2} { // moved, left mouse down, left mouse up
		if err := p.rendererSmokeUI(ctx, func() error {
			window := objc.ID(p.window.NativeHandle())
			uptime := objc.Send[float64](objc.ID(objc.GetClass("NSProcessInfo")).Send(objc.RegisterName("processInfo")), objc.RegisterName("systemUptime"))
			event := objc.ID(objc.GetClass("NSEvent")).Send(objc.RegisterName("mouseEventWithType:location:modifierFlags:timestamp:windowNumber:context:eventNumber:clickCount:pressure:"),
				kind, point, uint(0), uptime, int(window.Send(objc.RegisterName("windowNumber"))), objc.ID(0), int(1), int(1), float32(1))
			if event == 0 {
				return fmt.Errorf("could not create fixture mouse event")
			}
			window.Send(objc.RegisterName("sendEvent:"), event)
			return nil
		}); err != nil {
			return err
		}
		if kind == 1 {
			timer := time.NewTimer(120 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return nil
}
