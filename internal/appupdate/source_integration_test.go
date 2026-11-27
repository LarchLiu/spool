package appupdate

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMyGoUpdaterUsesSignedSourceForChecksAndDownloads(t *testing.T) {
	pub, key, manifest := manifestFixture(t)
	manifest.Target = runtime.GOOS + "-" + runtime.GOARCH
	manifest.URL = "https://github.com/owner/spool/releases/download/v0.1.2/spool-0.1.2-" + manifest.Target + ".tar.gz"
	archive := []byte("untrusted archive")
	manifest.Size = int64(len(archive))
	root := t.TempDir()
	binary := filepath.Join(root, "updater")
	if runtime.GOOS == "darwin" {
		binary = filepath.Join(root, "Updater.app", "Contents", "MacOS", "updater")
	} else if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if err := os.MkdirAll(filepath.Dir(binary), 0o700); err != nil {
		t.Fatal(err)
	}
	publicKey := base64.StdEncoding.EncodeToString(pub)
	flags := "-X github.com/egoist/mygo.production=1" +
		" -X github.com/egoist/mygo.packageUpdateFeed=https://example.invalid/unused" +
		" -X github.com/egoist/mygo.packageUpdateKey=" + publicKey +
		" -X spool/internal/appupdate.releaseRepository=owner/spool" +
		" -X spool/internal/appupdate.releasePublicKey=" + publicKey
	build := exec.CommandContext(t.Context(), "go", "build", "-ldflags", flags, "-o", binary, "./testdata/mygo-updater")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build updater fixture: %v\n%s", err, output)
	}
	data, _ := json.Marshal(manifest)
	write := func(name string, body []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("manifest.json", data)
	write("manifest.sig", ed25519.Sign(key, data))
	write("archive", archive)
	run := func(install bool) (string, error) {
		t.Helper()
		args := []string{root}
		if install {
			args = append(args, "install")
		}
		command := exec.CommandContext(t.Context(), binary, args...)
		command.Env = append(os.Environ(), "APPIMAGE=")
		output, err := command.CombinedOutput()
		return string(output), err
	}
	if output, err := run(false); err != nil || !strings.Contains(output, "checked 0.1.2 Fixes") {
		t.Fatalf("real updater did not accept verified metadata: %v\n%s", err, output)
	}
	// The metadata is signed, but its archive signature is deliberately wrong.
	// MyGo must obtain the bytes through Open and reject them before extraction.
	if output, err := run(true); err == nil || !strings.Contains(output, "not signed with the app's key") || !strings.Contains(output, "checked 0.1.2 Fixes") {
		t.Fatalf("real updater did not verify the downloaded archive: %v\n%s", err, output)
	}
	write("manifest.json", []byte(strings.ReplaceAll(string(data), "0.1.2", "99.0.0")))
	if output, err := run(false); err == nil || !strings.Contains(output, "invalid update manifest signature") || strings.Contains(output, "checked") {
		t.Fatalf("real updater accepted tampered metadata: %v\n%s", err, output)
	}
}
