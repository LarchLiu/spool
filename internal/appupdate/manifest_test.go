package appupdate

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

func manifestFixture(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey, signedManifest) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, key, signedManifest{
		Schema: "spool-update-v1", Repository: "owner/spool", Target: "darwin-arm64", Version: "0.1.2",
		Notes: "Fixes", URL: "https://github.com/owner/spool/releases/download/v0.1.2/spool-0.1.2-darwin-arm64.tar.gz",
		Size: 100, Signature: base64.StdEncoding.EncodeToString(make([]byte, 64)),
	}
}

func TestManifestSignatureBindsAllMetadata(t *testing.T) {
	pub, key, manifest := manifestFixture(t)
	data, _ := json.Marshal(manifest)
	signature := ed25519.Sign(key, data)
	if err := verifyManifest(data, signature, pub, manifest.Repository, manifest.Target); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"version", "url", "size", "target", "repository", "notes", "signature"} {
		t.Run(field, func(t *testing.T) {
			var changed map[string]any
			_ = json.Unmarshal(data, &changed)
			changed[field] = "attacker-controlled"
			if field == "size" {
				changed[field] = 101
			}
			modified, _ := json.Marshal(changed)
			if err := verifyManifest(modified, signature, pub, manifest.Repository, manifest.Target); err == nil {
				t.Fatal("tampered metadata was accepted")
			}
		})
	}
	for _, signature := range [][]byte{nil, make([]byte, 64), append(signature, 0)} {
		if verifyManifest(data, signature, pub, manifest.Repository, manifest.Target) == nil {
			t.Fatal("missing or invalid signature was accepted")
		}
	}
	if verifyManifest(data, signature, pub, "other/spool", manifest.Target) == nil || verifyManifest(data, signature, pub, manifest.Repository, "linux-arm64") == nil {
		t.Fatal("signature from another repository or platform was accepted")
	}
}

func TestSignedManifestRejectsMismatchedIdentityAndLegacyMetadata(t *testing.T) {
	pub, key, original := manifestFixture(t)
	for _, mutate := range []func(*signedManifest){
		func(m *signedManifest) { m.Schema = "" },
		func(m *signedManifest) { m.Version = "99.0.0" },
		func(m *signedManifest) { m.Version = "0.1.2-beta.01" },
		func(m *signedManifest) { m.Target = "linux-arm64" },
		func(m *signedManifest) { m.Repository = "other/spool" },
		func(m *signedManifest) { m.URL = "https://example.test/app.tar.gz" },
		func(m *signedManifest) { m.Size = 1<<30 + 1 },
	} {
		manifest := original
		mutate(&manifest)
		data, _ := json.Marshal(manifest)
		if verifyManifest(data, ed25519.Sign(key, data), pub, original.Repository, original.Target) == nil {
			t.Fatalf("mismatched manifest accepted: %+v", manifest)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestSignedSourceAuthenticatesRedirectedBytesWithoutRefetching(t *testing.T) {
	pub, key, manifest := manifestFixture(t)
	data, _ := json.Marshal(manifest)
	signature := ed25519.Sign(key, data)
	feed := "https://github.com/owner/spool/releases/latest/download/update-darwin-arm64.json"
	var requests []string
	base := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		url := req.URL.String()
		requests = append(requests, url)
		response := &http.Response{StatusCode: 200, Header: make(http.Header), Request: req}
		switch url {
		case feed:
			response.StatusCode = 302
			response.Header.Set("Location", "https://cdn.example.test/manifest")
			response.Body = io.NopCloser(strings.NewReader(""))
		case "https://cdn.example.test/manifest":
			response.Body = io.NopCloser(bytes.NewReader(data))
		case feed + ".sig":
			response.Body = io.NopCloser(bytes.NewReader(signature))
		default:
			response.Body = io.NopCloser(strings.NewReader("ordinary HTTP traffic"))
		}
		return response, nil
	})}
	source, err := newSignedSource(base, manifest.Repository, base64.StdEncoding.EncodeToString(pub), manifest.Target)
	if err != nil {
		t.Fatal(err)
	}
	body, err := source.Manifest(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	actual, _ := readLimited(body, maxManifestSize)
	if !bytes.Equal(actual, data) || len(requests) != 3 {
		t.Fatalf("verified bytes changed or were fetched twice: %s, %v", actual, requests)
	}
	response, err := base.Get("https://example.test/other")
	if err != nil {
		t.Fatal(err)
	}
	actual, _ = readLimited(response.Body, maxManifestSize)
	if string(actual) != "ordinary HTTP traffic" {
		t.Fatal("unrelated HTTP requests changed")
	}
	// Replaying an old archive as a high version requires changing signed
	// metadata. It must fail before MyGo ever sees the forged version.
	data = bytes.ReplaceAll(data, []byte("0.1.2"), []byte("99.0.0"))
	if _, err := source.Manifest(t.Context(), false); err == nil {
		t.Fatal("relabelled old update metadata was accepted")
	}
	signature = nil
	if _, err := source.Manifest(t.Context(), false); err == nil {
		t.Fatal("unsigned fallback was allowed")
	}
}

func TestManifestConfigurationAndSizeLimits(t *testing.T) {
	if _, err := newSignedSource(http.DefaultClient, "owner/spool", "bad-key", "linux-amd64"); err == nil {
		t.Fatal("invalid key accepted")
	}
	if _, err := readLimited(io.NopCloser(strings.NewReader("too long")), 3); err == nil {
		t.Fatal("oversized metadata accepted")
	}
}

func TestOnlyAuthenticatedArchiveTrafficGetsNetworkClassification(t *testing.T) {
	pub, key, manifest := manifestFixture(t)
	data, _ := json.Marshal(manifest)
	base := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := []byte("short")
		if strings.HasSuffix(req.URL.Path, ".json") {
			body = data
		}
		if strings.HasSuffix(req.URL.Path, ".sig") {
			body = ed25519.Sign(key, data)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), Request: req}, nil
	})}
	source, err := newSignedSource(base, manifest.Repository, base64.StdEncoding.EncodeToString(pub), manifest.Target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Open(t.Context(), manifest.URL); err == nil {
		t.Fatal("archive accepted before its manifest was verified")
	}
	body, err := source.Manifest(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	body.Close()
	body, err = source.Open(t.Context(), manifest.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = readLimited(body, 100)
	var network *NetworkError
	if !errors.As(err, &network) {
		t.Fatalf("truncated authenticated download not classified: %v", err)
	}
	if _, err = source.Open(t.Context(), "https://other.test/ordinary"); err == nil {
		t.Fatal("archive without an authenticated manifest was accepted")
	}
}

func TestSignedSourceConfigurationDoesNotChangeDefaultHTTPClient(t *testing.T) {
	previousRepo, previousKey := releaseRepository, releasePublicKey
	t.Cleanup(func() { releaseRepository, releasePublicKey = previousRepo, previousKey })
	originalClient := http.DefaultClient
	pub, _, _ := manifestFixture(t)
	publicKey := base64.StdEncoding.EncodeToString(pub)
	for _, configuration := range []struct {
		repo, key string
		wantError bool
	}{
		{"", "", false},
		{"owner/spool", "", true},
		{"", publicKey, true},
		{"owner/spool", "invalid", true},
		{"owner/spool", publicKey, false},
	} {
		releaseRepository, releasePublicKey = configuration.repo, configuration.key
		source, err := NewSignedSource(originalClient)
		if (err != nil) != configuration.wantError {
			t.Fatalf("configuration %q: %v", configuration.repo, err)
		}
		if (source != nil) != (configuration.repo != "" && !configuration.wantError) {
			t.Fatal("signed update source has incorrect enabled state")
		}
		if http.DefaultClient != originalClient {
			t.Fatal("signed updates replaced the application's HTTP client")
		}
	}
	if _, err := newSignedSource(originalClient, "owner/spool", publicKey, "android-arm64"); err == nil {
		t.Fatal("unsupported update target accepted")
	}
}

func TestSignedSourceHonorsCancellationAndNeverFallsBackToUnsignedMetadata(t *testing.T) {
	pub, _, manifest := manifestFixture(t)
	data, _ := json.Marshal(manifest)
	for _, status := range []int{http.StatusNotFound, http.StatusForbidden, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			base := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				response := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data)), Request: req}
				if strings.HasSuffix(req.URL.Path, ".sig") {
					response.StatusCode = status
				}
				return response, nil
			})}
			source, err := newSignedSource(base, manifest.Repository, base64.StdEncoding.EncodeToString(pub), manifest.Target)
			if err != nil {
				t.Fatal(err)
			}
			_, err = source.Manifest(t.Context(), false)
			if err == nil {
				t.Fatal("missing signature allowed an update")
			}
			var network *NetworkError
			if errors.As(err, &network) != (status == http.StatusServiceUnavailable) {
				t.Fatalf("incorrect retry classification: %v", err)
			}
			if _, err := source.Open(t.Context(), manifest.URL); err == nil {
				t.Fatal("failed metadata check authorized its archive")
			}
		})
	}
	base := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("User-Agent") != "spool-updater" {
			t.Error("missing updater user agent")
		}
		<-req.Context().Done()
		return nil, req.Context().Err()
	})}
	source, err := newSignedSource(base, manifest.Repository, base64.StdEncoding.EncodeToString(pub), manifest.Target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Manifest(t.Context(), true); err == nil {
		t.Fatal("unconfigured prerelease channel accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := source.Manifest(ctx, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestSignedSourceKeepsEarlierArchivesAcrossConcurrentChecks(t *testing.T) {
	pub, key, manifest := manifestFixture(t)
	data, _ := json.Marshal(manifest)
	base := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := data
		if strings.HasSuffix(req.URL.Path, ".sig") {
			body = ed25519.Sign(key, data)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), Request: req}, nil
	})}
	source, err := newSignedSource(base, manifest.Repository, base64.StdEncoding.EncodeToString(pub), manifest.Target)
	if err != nil {
		t.Fatal(err)
	}
	body, err := source.Manifest(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	body.Close()
	manifest.Version = "0.1.3"
	manifest.URL = strings.ReplaceAll(manifest.URL, "0.1.2", "0.1.3")
	data, _ = json.Marshal(manifest)
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			body, err := source.Manifest(t.Context(), false)
			if err != nil {
				t.Error(err)
				return
			}
			body.Close()
			for _, address := range []string{manifest.URL, strings.ReplaceAll(manifest.URL, "0.1.3", "0.1.2")} {
				body, err := source.Open(t.Context(), address)
				if err != nil {
					t.Error(err)
					continue
				}
				body.Close()
			}
		})
	}
	workers.Wait()
}
