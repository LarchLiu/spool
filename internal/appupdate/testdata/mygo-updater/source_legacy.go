//go:build spool_legacy_updater

package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

var legacyPublicKey string

// MyGo 0.6.4 reads http.DefaultClient. Authenticate metadata there, as Spool
// did before SetSource existed, then exercise that actual updater's parser,
// archive verification, replacement and relaunch with today's release format.
type legacyTransport struct{ root string }

func (l legacyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := (fixtureTransport{l.root}).RoundTrip(request)
	if err != nil || !strings.HasSuffix(request.URL.Path, ".json") {
		return response, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	signature, err := os.ReadFile(filepath.Join(l.root, "manifest.sig"))
	if err != nil {
		return nil, err
	}
	key, err := base64.StdEncoding.DecodeString(legacyPublicKey)
	if err != nil || len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, data, signature) {
		return nil, errors.New("invalid legacy update manifest signature")
	}
	response.Body = io.NopCloser(bytes.NewReader(data))
	return response, nil
}

func configureFixtureSource(root string) error {
	http.DefaultClient = &http.Client{Transport: legacyTransport{root}}
	return nil
}
