// Exercise the real MyGo updater with release flags and an isolated HTTP source.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
)

var fixtureVersion = "0.1.0"

type fixtureTransport struct{ directory string }

func (f fixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	name := "archive"
	if strings.HasSuffix(req.URL.Path, ".json") {
		name = "manifest.json"
	} else if strings.HasSuffix(req.URL.Path, ".json.sig") {
		name = "manifest.sig"
	}
	body, err := os.Open(filepath.Join(f.directory, name))
	if err != nil {
		return nil, err
	}
	var stream io.ReadCloser = body
	if name == "archive" {
		fault, _ := os.ReadFile(filepath.Join(f.directory, "fault"))
		switch string(fault) {
		case "truncated":
			stat, _ := body.Stat()
			stream = &limitedBody{Reader: io.LimitReader(body, stat.Size()/2), Closer: body}
		case "cancel":
			body.Close()
			stream = canceledBody{req.Context()}
		}
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: stream, Request: req}, nil
}

type limitedBody struct {
	io.Reader
	io.Closer
}
type canceledBody struct{ context.Context }

func (c canceledBody) Read([]byte) (int, error) { <-c.Done(); return 0, c.Err() }
func (c canceledBody) Close() error             { return nil }

type denyHTTP struct{}

func (denyHTTP) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("updater bypassed its authenticated source")
}

func run() error {
	if err := configureFixtureSource(os.Args[1]); err != nil {
		return err
	}
	mygo.App.SetVersion(fixtureVersion)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	update, err := mygo.Updater.Check(ctx)
	if err != nil {
		return err
	}
	if update == nil {
		return errors.New("signed release was not offered")
	}
	fmt.Println("checked", update.Version, update.Notes)
	if len(os.Args) > 2 {
		fault, _ := os.ReadFile(filepath.Join(os.Args[1], "fault"))
		if string(fault) == "cancel" {
			timer := time.AfterFunc(50*time.Millisecond, cancel)
			defer timer.Stop()
		}
		return update.Install(ctx, nil)
	}
	return nil
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println(fixtureVersion)
		return
	}
	if len(os.Args) > 2 && os.Args[2] == "relaunch" {
		if err := runWithRelaunch(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		io.WriteString(os.Stderr, err.Error()+"\n")
		os.Exit(1)
	}
}

// Opt-in native test: the same release APIs that Spool uses, including the
// single-instance lock and OnQuit cleanup before MyGo relaunches the new app.
func runWithRelaunch() error {
	root := os.Args[1]
	mygo.App.SetName("Spool Updater Test")
	mygo.App.SetPath(mygo.PathUserData, filepath.Join(root, "native"))
	if !mygo.App.RequestSingleInstanceLock() {
		return errors.New("updated process could not acquire the single-instance lock")
	}
	mygo.App.OnQuit(func() { _ = os.WriteFile(filepath.Join(root, "quit-"+fixtureVersion), []byte("closed"), 0o600) })
	mygo.App.WhenReady(func() {
		go func() {
			if _, err := os.Stat(filepath.Join(root, "relaunch-pending")); err == nil {
				_ = os.WriteFile(filepath.Join(root, "restarted-version"), []byte(fixtureVersion), 0o600)
				mygo.App.Quit()
				return
			}
			if err := run(); err != nil {
				_ = os.WriteFile(filepath.Join(root, "error"), []byte(err.Error()), 0o600)
				mygo.App.Quit()
				return
			}
			_ = os.WriteFile(filepath.Join(root, "relaunch-pending"), []byte("1"), 0o600)
			mygo.App.Relaunch()
		}()
	})
	return mygo.App.Run()
}
