package appupdate

import (
	"bytes"
	"context"
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

// NewSignedSource returns the release build's authenticated update source, or
// nil when signed updates are not configured. It implements MyGo's UpdateSource
// without changing the HTTP client used by other parts of the app. MyGo reads
// the exact verified bytes and still verifies the archive before installing it.
func NewSignedSource(base *http.Client) (*SignedSource, error) {
	if releaseRepository == "" && releasePublicKey == "" {
		return nil, nil
	}
	return newSignedSource(base, releaseRepository, releasePublicKey, runtime.GOOS+"-"+runtime.GOARCH)
}

// SignedSource serves only authenticated release metadata and the archives it
// names. A source may be used by overlapping checks and installations.
type SignedSource struct {
	base               *http.Client
	feed, repo, target string
	key                ed25519.PublicKey
	mu                 sync.Mutex
	archives           map[string]int64
}

func newSignedSource(base *http.Client, repo, publicKey, target string) (*SignedSource, error) {
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
	return &SignedSource{
		base: &plain, repo: repo, target: target, key: key,
		feed:     "https://github.com/" + repo + "/releases/latest/download/update-" + target + ".json",
		archives: make(map[string]int64),
	}, nil
}

func (s *SignedSource) get(ctx context.Context, address string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "spool-updater")
	response, err := updateResponse(s.base.Do(req))
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("update download returned HTTP %d", response.StatusCode)
	}
	return response.Body, nil
}

// Manifest stays on Spool's stable release channel. A prerelease channel needs
// its own authenticated feed rather than falling back to unsigned metadata.
func (s *SignedSource) Manifest(ctx context.Context, prereleases bool) (io.ReadCloser, error) {
	if prereleases {
		return nil, errors.New("prerelease updates are not configured")
	}
	// Follow asset redirects before authenticating, then pass these same bytes
	// to MyGo. Never verify one response and ask MyGo to fetch it again.
	body, err := s.get(ctx, s.feed)
	if err != nil {
		return nil, err
	}
	data, err := readLimited(body, maxManifestSize)
	if err != nil {
		return nil, networkFailure(err)
	}
	body, err = s.get(ctx, s.feed+".sig")
	if err != nil {
		return nil, fmt.Errorf("fetch signed update metadata: %w", err)
	}
	signature, err := readLimited(body, ed25519.SignatureSize)
	if err != nil {
		return nil, networkFailure(err)
	}
	if err := verifyManifest(data, signature, s.key, s.repo, s.target); err != nil {
		return nil, err
	}
	var manifest signedManifest
	_ = json.Unmarshal(data, &manifest) // Already strictly decoded and verified.
	s.mu.Lock()
	s.archives[manifest.URL] = manifest.Size
	s.mu.Unlock()
	return io.NopCloser(bytes.NewReader(data)), nil
}

// Open accepts only archives named by a manifest verified by this source.
// Keep earlier checked releases available while a later check runs.
func (s *SignedSource) Open(ctx context.Context, address string) (io.ReadCloser, error) {
	s.mu.Lock()
	size, authenticated := s.archives[address]
	s.mu.Unlock()
	if !authenticated {
		return nil, errors.New("update archive has no authenticated manifest")
	}
	body, err := s.get(ctx, address)
	if err != nil {
		return nil, err
	}
	return &networkBody{ReadCloser: body, remaining: size}, nil
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
