package main

import (
	"context"
	"errors"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/permissionflow"
)

func TestScreenshotPermissionGuidesOnlyWhenNeeded(t *testing.T) {
	for _, scenario := range []struct {
		name                              string
		check, request, guide             permissionflow.Status
		wantRequest, wantGuide, wantError bool
	}{
		{"granted", permissionflow.Granted, permissionflow.Denied, permissionflow.Denied, false, false, false},
		{"first prompt", permissionflow.NotDetermined, permissionflow.Granted, permissionflow.Denied, true, false, false},
		{"settings guide", permissionflow.Denied, permissionflow.Denied, permissionflow.Granted, true, true, false},
		{"closed guide", permissionflow.Denied, permissionflow.Denied, permissionflow.Denied, true, true, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			requested, guided := false, false
			err := ensureScreenshotPermission(t.Context(), func() (permissionflow.Status, error) { return scenario.check, nil },
				func(context.Context) (permissionflow.Status, error) { requested = true; return scenario.request, nil },
				func(context.Context) (permissionflow.Status, error) { guided = true; return scenario.guide, nil })
			if requested != scenario.wantRequest || guided != scenario.wantGuide || (err != nil) != scenario.wantError {
				t.Fatalf("permission flow: request=%v guide=%v err=%v", requested, guided, err)
			}
		})
	}
}

func TestScreenshotQuitCancelsCompletedWorkAndCannotDeferSystemTermination(t *testing.T) {
	for _, sessionEnding := range []bool{false, true} {
		canceled := false
		done := make(chan struct{})
		if !sessionEnding {
			close(done)
		}
		p := &panel{screenshotCancel: func() { canceled = true }, screenshotDone: done}
		event := &mygo.QuitEvent{SessionEnding: sessionEnding}
		p.deferQuitForScreenshot(event)
		if !canceled || event.DefaultPrevented() || p.screenshotQuitPending {
			t.Fatalf("quit cancellation: system=%v canceled=%v deferred=%v", sessionEnding, canceled, event.DefaultPrevented())
		}
	}
}

func TestScreenshotPermissionStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	err := ensureScreenshotPermission(ctx, func() (permissionflow.Status, error) { return permissionflow.Denied, nil },
		func(context.Context) (permissionflow.Status, error) { cancel(); return permissionflow.Denied, nil },
		func(context.Context) (permissionflow.Status, error) {
			t.Fatal("opened settings after cancellation")
			return permissionflow.Unknown, nil
		})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}
