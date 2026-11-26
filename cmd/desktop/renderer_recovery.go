package main

import (
	"fmt"
	"log"
	"net/url"
	"regexp"
	"time"

	"github.com/egoist/mygo"
)

const desktopRenderErrorPath = "/__desktop/render-error"

var desktopRenderErrorName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,80}$`)

type desktopRenderFailure struct {
	name, components string
	fatal            bool
}

// This is a bounded diagnostic hint, not a native API. The origin must be the
// page's own (including the privileged settings document's custom scheme).
func desktopRenderErrorNavigation(address, target string) (desktopRenderFailure, bool) {
	base, err := url.Parse(address)
	if err != nil || base.Host == "" || base.User != nil {
		return desktopRenderFailure{}, false
	}
	u, err := url.Parse(target)
	if err != nil || len(target) > 16384 || u.Scheme != base.Scheme || u.Host != base.Host || u.User != nil || u.Opaque != "" || u.Path != desktopRenderErrorPath || u.RawPath != "" || u.Fragment != "" {
		return desktopRenderFailure{}, false
	}
	params, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(params) != 3 || len(params["name"]) != 1 || len(params["components"]) != 1 || len(params["fatal"]) != 1 ||
		!desktopRenderErrorName.MatchString(params.Get("name")) || len(params.Get("components")) > 8192 || (params.Get("fatal") != "true" && params.Get("fatal") != "false") {
		return desktopRenderFailure{}, false
	}
	return desktopRenderFailure{params.Get("name"), params.Get("components"), params.Get("fatal") == "true"}, true
}

func logDesktopRenderFailure(surface string, failure desktopRenderFailure) {
	log.Printf("event=desktop_render_error surface=%q name=%q fatal=%t components=%q", surface, failure.name, failure.fatal, failure.components)
}

// A successful load does not reset this budget: a page that crashes just after
// loading must not enter an endless reload loop. Each WebView owns its budget.
type desktopRendererRecovery struct {
	started  time.Time
	attempts int
}

func (r *desktopRendererRecovery) blocked(now time.Time) bool {
	return r.attempts >= 2 && now.Sub(r.started) < time.Minute
}

func (r *desktopRendererRecovery) allow(now time.Time) bool {
	if r.blocked(now) {
		return false
	}
	if r.started.IsZero() || now.Sub(r.started) >= time.Minute {
		r.started, r.attempts = now, 0
	}
	r.attempts++
	return true
}

func (p *panel) recoverDesktopRenderer(page *mygo.Page, recovery *desktopRendererRecovery, surface, reason string) bool {
	if p.quitting {
		return false
	}
	reload := recovery.allow(time.Now())
	log.Printf("event=desktop_renderer_gone surface=%q reason=%q reload=%t attempts=%d", surface, reason, reload, recovery.attempts)
	if reload {
		page.Reload()
	}
	return reload
}

func (p *panel) desktopChatRendererGone(id string, state *desktopServerChat, reason string) {
	if p.quitting || p.serverChats[id] != state {
		return
	}
	view := state.view
	state.loaded = false
	if p.chatView == view {
		p.chatLoaded = false
		p.setDesktopNotificationFocused(p.desktopNotificationFocused())
	}
	if p.recoverDesktopRenderer(view.Page(), &state.rendererRecovery, "chat:"+id, reason) {
		state.failed = false
		if p.chatView == view {
			p.chatFailed = false
		}
		p.recordDesktopServerState(id, "connecting")
		return
	}
	state.failed = true
	p.recordDesktopServerState(id, "error")
	if p.chatView == view {
		p.chatFailed = true
		p.chatError(fmt.Errorf("%s: %s", p.tr("Chat renderer stopped. Reopen Chat to reload."), reason))
	} else if p.chatPresentedView == view {
		// Do not leave a failed outgoing page covering a healthy incoming tab.
		p.chatPresentedView = nil
		p.window.Invalidate()
	}
}
