package appupdate

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
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

func TestManifestClientAuthenticatesRedirectedBytesWithoutRefetching(t *testing.T) {
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
	client, err := manifestClient(base, manifest.Repository, base64.StdEncoding.EncodeToString(pub), manifest.Target)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(feed)
	if err != nil {
		t.Fatal(err)
	}
	actual, _ := readLimited(response.Body, maxManifestSize)
	if !bytes.Equal(actual, data) || len(requests) != 3 {
		t.Fatalf("verified bytes changed or were fetched twice: %s, %v", actual, requests)
	}
	response, err = client.Get("https://example.test/other")
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
	if _, err := client.Get(feed); err == nil {
		t.Fatal("relabelled old update metadata was accepted")
	}
	signature = nil
	if _, err := client.Get(feed); err == nil {
		t.Fatal("unsigned fallback was allowed")
	}
}

func TestManifestConfigurationAndSizeLimits(t *testing.T) {
	if _, err := manifestClient(http.DefaultClient, "owner/spool", "bad-key", "linux-amd64"); err == nil {
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
	client, err := manifestClient(base, manifest.Repository, base64.StdEncoding.EncodeToString(pub), manifest.Target)
	if err != nil {
		t.Fatal(err)
	}
	feed := "https://github.com/owner/spool/releases/latest/download/update-darwin-arm64.json"
	res, err := client.Get(feed)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	res, err = client.Get(manifest.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = readLimited(res.Body, 100)
	var network *NetworkError
	if !errors.As(err, &network) {
		t.Fatalf("truncated authenticated download not classified: %v", err)
	}
	res, err = client.Get("https://other.test/ordinary")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = readLimited(res.Body, 100); err != nil {
		t.Fatal("unrelated request changed", err)
	}
}
