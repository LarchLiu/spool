package appupdate

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"runtime"
	"strings"
	"sync"
)

// Set by release builds, independently of MyGo's legacy archive-signing flags.
var releaseRepository, releasePublicKey string

const maxManifestSize = 1 << 20

var versionPattern = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)
var repositoryPattern = regexp.MustCompile(`^[\w.-]+/[\w.-]+$`)

type signedManifest struct {
	Schema     string `json:"schema"`
	Repository string `json:"repository"`
	Target     string `json:"target"`
	Version    string `json:"version"`
	Notes      string `json:"notes"`
	Date       string `json:"date"`
	URL        string `json:"url"`
	Size       int64  `json:"size"`
	Signature  string `json:"signature"`
}

// RequireSignedManifests runs once during process initialization, before any
// HTTP users start. MyGo has no per-updater HTTP client hook: intercept only its
// exact release feed, authenticate the bytes, and leave archive installation to
// MyGo. In particular, there is no separate check followed by a mutable re-fetch.
func RequireSignedManifests() (bool, error) {
	if releaseRepository == "" && releasePublicKey == "" {
		return false, nil
	}
	client, err := manifestClient(http.DefaultClient, releaseRepository, releasePublicKey, runtime.GOOS+"-"+runtime.GOARCH)
	if err != nil {
		return false, err
	}
	http.DefaultClient = client
	return true, nil
}

type manifestTransport struct {
	base               *http.Client
	feed, repo, target string
	key                ed25519.PublicKey
	mu                 sync.Mutex
	archiveURL         string
	archiveSize        int64
}

func manifestClient(base *http.Client, repo, publicKey, target string) (*http.Client, error) {
	key, err := base64.StdEncoding.DecodeString(publicKey)
	if err != nil || len(key) != ed25519.PublicKeySize || !repositoryPattern.MatchString(repo) {
		return nil, errors.New("invalid signed update configuration")
	}
	if target != "darwin-arm64" && target != "darwin-amd64" && target != "windows-amd64" && target != "windows-arm64" && target != "linux-amd64" && target != "linux-arm64" {
		return nil, errors.New("unsupported signed update target")
	}
	plain := *base
	if plain.Transport == nil {
		plain.Transport = http.DefaultTransport
	}
	client := *base
	client.Transport = &manifestTransport{
		base: &plain, repo: repo, target: target, key: key,
		feed: "https://github.com/" + repo + "/releases/latest/download/update-" + target + ".json",
	}
	return &client, nil
}

func (t *manifestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet || req.URL.String() != t.feed {
		t.mu.Lock()
		archive, size := req.Method == http.MethodGet && req.URL.String() == t.archiveURL, t.archiveSize
		t.mu.Unlock()
		if archive {
			response, err := updateResponse(t.base.Do(req.Clone(req.Context())))
			if err == nil && response.StatusCode == http.StatusOK {
				response.Body = &networkBody{ReadCloser: response.Body, remaining: size}
			}
			return response, err
		}
		return t.base.Transport.RoundTrip(req)
	}
	// Follow GitHub's asset redirects inside the unwrapped client, so the final
	// CDN body is verified even though its URL no longer resembles the feed.
	response, err := updateResponse(t.base.Do(req.Clone(req.Context())))
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return response, nil
	}
	data, err := readLimited(response.Body, maxManifestSize)
	if err != nil {
		return nil, networkFailure(err)
	}
	signatureRequest := req.Clone(req.Context())
	url := *req.URL
	url.Path += ".sig"
	signatureRequest.URL = &url
	signatureResponse, err := updateResponse(t.base.Do(signatureRequest))
	if err != nil {
		return nil, fmt.Errorf("fetch signed update metadata: %w", err)
	}
	if signatureResponse.StatusCode != http.StatusOK {
		signatureResponse.Body.Close()
		return nil, errors.New("update manifest signature is unavailable; refusing unsigned metadata")
	}
	signature, err := readLimited(signatureResponse.Body, ed25519.SignatureSize)
	if err != nil {
		return nil, networkFailure(err)
	}
	if err := verifyManifest(data, signature, t.key, t.repo, t.target); err != nil {
		return nil, err
	}
	var manifest signedManifest
	_ = json.Unmarshal(data, &manifest) // Already strictly decoded and verified.
	t.mu.Lock()
	t.archiveURL, t.archiveSize = manifest.URL, manifest.Size
	t.mu.Unlock()
	response.Body = io.NopCloser(bytes.NewReader(data))
	response.ContentLength = int64(len(data))
	return response, nil
}

func readLimited(body io.ReadCloser, limit int64) ([]byte, error) {
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("signed update metadata exceeds the size limit")
	}
	return data, nil
}

func verifyManifest(data, signature []byte, key ed25519.PublicKey, repo, target string) error {
	if !ed25519.Verify(key, data, signature) {
		return errors.New("invalid update manifest signature")
	}
	var manifest signedManifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return fmt.Errorf("invalid signed update manifest: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("trailing data in signed update manifest")
	}
	validVersion := versionPattern.MatchString(manifest.Version)
	if _, pre, ok := strings.Cut(manifest.Version, "-"); ok {
		for _, part := range strings.Split(pre, ".") {
			if len(part) > 1 && part[0] == '0' && strings.Trim(part, "0123456789") == "" {
				validVersion = false
			}
		}
	}
	expectedURL := "https://github.com/" + repo + "/releases/download/v" + manifest.Version + "/spool-" + manifest.Version + "-" + target + ".tar.gz"
	archiveSignature, err := base64.StdEncoding.DecodeString(manifest.Signature)
	if manifest.Schema != "spool-update-v1" || manifest.Repository != repo || manifest.Target != target || !validVersion ||
		manifest.URL != expectedURL || manifest.Size <= 0 || manifest.Size > 1<<30 || err != nil || len(archiveSignature) != ed25519.SignatureSize {
		return errors.New("signed update manifest does not match this application, version or target")
	}
	return nil
}
