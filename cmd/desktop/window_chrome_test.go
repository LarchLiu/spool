package main

import (
	"testing"

	"github.com/egoist/mygo"
)

func TestMacWindowHidesOnlyTitleBarAndKeepsNativeControls(t *testing.T) {
	for _, platform := range []string{"darwin", "windows", "linux"} {
		t.Run(platform, func(t *testing.T) {
			opts := mygo.WindowOptions{Title: "Spool", StateKey: "main"}
			configureWindowChrome(&opts, platform)
			if opts.Frameless || opts.HideWindowButtons || opts.DisableMove || opts.DisableResize {
				t.Fatal("native controls, dragging, and window resizing must remain available")
			}
			if platform == "darwin" {
				if opts.TitleBarStyle != mygo.TitleBarHidden || opts.TrafficLightPosition == nil ||
					opts.TrafficLightPosition.X != 16 || opts.TrafficLightPosition.Y != 20 {
					t.Fatalf("macOS chrome is not aligned with the 54px app header: %+v", opts)
				}
			} else if opts.TitleBarStyle != "" || opts.TrafficLightPosition != nil {
				t.Fatal("other desktop platforms must keep their existing native title bar")
			}
			if opts.Title != "Spool" || opts.StateKey != "main" {
				t.Fatal("hiding the visible title must preserve window identity and restoration")
			}
		})
	}
}
