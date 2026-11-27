//go:build !spool_legacy_updater

package main

import (
	"net/http"

	"github.com/egoist/mygo"
	"spool/internal/appupdate"
)

func configureFixtureSource(root string) error {
	// Neither the global client nor MyGo's default client may serve the update.
	http.DefaultClient = &http.Client{Transport: denyHTTP{}}
	http.DefaultTransport = denyHTTP{}
	source, err := appupdate.NewSignedSource(&http.Client{Transport: fixtureTransport{root}})
	if err == nil {
		mygo.Updater.SetSource(source)
	}
	return err
}
