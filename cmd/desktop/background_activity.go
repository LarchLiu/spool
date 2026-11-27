package main

import "github.com/egoist/mygo"

// Owned by the UI thread. KeepRunning prevents App Nap, while allowing normal
// computer sleep. Acquire only while serving or connected to a workspace.
type desktopBackgroundActivity struct{ release func() }

func (a *desktopBackgroundActivity) update(active bool, acquire func() func()) {
	if active && a.release == nil {
		a.release = acquire()
	} else if !active {
		a.close()
	}
}

func (a *desktopBackgroundActivity) close() {
	if a.release != nil {
		a.release()
		a.release = nil
	}
}

func (p *panel) refreshBackgroundActivity() {
	if p.window == nil {
		return
	}
	active := p.status.Running
	for _, chat := range p.serverChats {
		active = active || chat.status == "connected" || chat.status == "connecting"
	}
	if p.updates != nil {
		active = active || p.updates.State().Installing
	}
	p.backgroundActivity.update(active && !p.quitting, func() func() {
		return mygo.Power.KeepRunning("Serving Spool workspaces")
	})
}
