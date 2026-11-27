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

	"spool/internal/appupdate"
)

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
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body, Request: req}, nil
}

type denyHTTP struct{}

func (denyHTTP) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("updater bypassed its authenticated source")
}

func run() error {
	// Neither the global client nor MyGo's default client may serve the update.
	http.DefaultClient = &http.Client{Transport: denyHTTP{}}
	http.DefaultTransport = denyHTTP{}
	source, err := appupdate.NewSignedSource(&http.Client{Transport: fixtureTransport{os.Args[1]}})
	if err != nil {
		return err
	}
	mygo.Updater.SetSource(source)
	mygo.App.SetVersion("0.1.0")
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
		return update.Install(ctx, nil)
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		io.WriteString(os.Stderr, err.Error()+"\n")
		os.Exit(1)
	}
}
