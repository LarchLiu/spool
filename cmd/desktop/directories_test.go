package main

import (
	"errors"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"spool/internal/workspace"
)

func TestDirectoryPickerDoesNotBlockUIOrUpdateItFromWorker(t *testing.T) {
	var picker directoryPicker
	entered := make(chan mygo.OpenDialogOptions, 1)
	release := make(chan struct{})
	defer close(release)
	updates := make(chan func(), 1)
	completed := false
	opts := mygo.OpenDialogOptions{Title: "Project directory", DefaultPath: "/existing", Directory: true}
	returned := make(chan struct{})
	go func() {
		picker.open(opts, func(got mygo.OpenDialogOptions) ([]string, error) {
			entered <- got
			<-release
			return []string{"/chosen"}, nil
		}, func(fn func()) { updates <- fn }, func(paths []string, err error) {
			if err != nil || len(paths) != 1 || paths[0] != "/chosen" {
				t.Errorf("unexpected selection: %v, %v", paths, err)
			}
			completed = true
		})
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("directory picker blocked the UI while awaiting a choice")
	}
	select {
	case got := <-entered:
		if got.Title != opts.Title || got.DefaultPath != opts.DefaultPath || !got.Directory {
			t.Fatalf("directory options changed: %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("native dialog did not start")
	}
	if !picker.pending || completed {
		t.Fatal("picker must remain pending while the dialog is open")
	}
	picker.open(opts, func(mygo.OpenDialogOptions) ([]string, error) {
		t.Error("duplicate dialog opened")
		return nil, nil
	}, func(func()) { t.Error("duplicate UI update") }, func([]string, error) {})
	release <- struct{}{}
	select {
	case apply := <-updates:
		if !picker.pending || completed {
			t.Fatal("worker changed UI state before the UI callback ran")
		}
		apply()
	case <-time.After(2 * time.Second):
		t.Fatal("dialog result was not dispatched to the UI")
	}
	if picker.pending || !completed {
		t.Fatal("UI callback did not apply the result and re-enable the picker")
	}
}

func TestDirectoryPickerCanReopenAfterCancelOrError(t *testing.T) {
	var picker directoryPicker
	for _, outcome := range []error{nil, errors.New("dialog failed")} {
		updates := make(chan func(), 1)
		completed := false
		picker.open(mygo.OpenDialogOptions{}, func(mygo.OpenDialogOptions) ([]string, error) {
			return nil, outcome
		}, func(fn func()) { updates <- fn }, func(paths []string, err error) {
			if len(paths) != 0 || err != outcome {
				t.Errorf("unexpected result: %v, %v", paths, err)
			}
			completed = true
		})
		select {
		case apply := <-updates:
			apply()
		case <-time.After(2 * time.Second):
			t.Fatal("dialog did not complete")
		}
		if picker.pending || !completed {
			t.Fatal("cancel/error left the picker disabled")
		}
	}
}

func TestProjectDirectorySelection(t *testing.T) {
	for _, tc := range []struct {
		name    string
		paths   []string
		err     error
		path    string
		message string
	}{
		{name: "selected", paths: []string{"/chosen"}, path: "/chosen", message: "existing message"},
		{name: "cancelled", path: "/existing", message: "existing message"},
		{name: "error", err: errors.New("dialog failed"), path: "/existing", message: "dialog failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := panel{project: workspace.Project{Path: "/existing"}, message: "existing message"}
			p.applyProjectDirectory(tc.paths, tc.err)
			if p.project.Path != tc.path || p.message != tc.message {
				t.Fatalf("path=%q, message=%q", p.project.Path, p.message)
			}
		})
	}
}
