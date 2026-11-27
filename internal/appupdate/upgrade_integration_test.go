package appupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// These are real release-mode MyGo executables, not mocks of Install. The
// legacy modfile changes only this fixture's dependency graph in owned scratch.
func buildUpgradeFixture(t *testing.T, root, name, version, key string, legacy bool) []byte {
	t.Helper()
	output := filepath.Join(root, name)
	if runtime.GOOS == "windows" {
		output += ".exe"
	}
	flags := "-X github.com/egoist/mygo.production=1" +
		" -X github.com/egoist/mygo.packageUpdateFeed=https://github.com/owner/spool/releases/latest/download/update-" + runtime.GOOS + "-" + runtime.GOARCH + ".json" +
		" -X github.com/egoist/mygo.packageUpdateKey=" + key +
		" -X spool/internal/appupdate.releaseRepository=owner/spool" +
		" -X spool/internal/appupdate.releasePublicKey=" + key +
		" -X main.legacyPublicKey=" + key +
		" -X main.fixtureVersion=" + version
	args := []string{"build", "-ldflags", flags, "-o", output}
	if legacy {
		modfile := filepath.Join(root, "legacy.mod")
		module := "module spool\n\ngo 1.27.3\n\nrequire github.com/egoist/mygo v0.6.4\n"
		if err := os.WriteFile(modfile, []byte(module), 0o600); err != nil {
			t.Fatal(err)
		}
		args = append(args, "-modfile", modfile, "-mod=mod", "-tags", "spool_legacy_updater")
	}
	args = append(args, "./testdata/mygo-updater")
	command := exec.CommandContext(t.Context(), "go", args...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", name, err, output)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func updaterPayload(binary []byte, version string) map[string][]byte {
	if runtime.GOOS == "darwin" {
		return map[string][]byte{
			"Spool.app/Contents/MacOS/Spool":     binary,
			"Spool.app/Contents/Info.plist":      []byte(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleExecutable</key><string>Spool</string><key>CFBundleIdentifier</key><string>com.cloudgeek.spool.updater-fixture</string><key>CFBundleName</key><string>Spool Updater Test</string><key>CFBundlePackageType</key><string>APPL</string><key>CFBundleShortVersionString</key><string>` + version + `</string></dict></plist>`),
			"Spool.app/Contents/payload-version": []byte(version),
		}
	}
	name := "spool"
	if runtime.GOOS == "windows" {
		name = "Spool.exe"
	}
	return map[string][]byte{name: binary, "payload-version": []byte(version)}
}

func upgradeArchive(t *testing.T, payload map[string][]byte) []byte {
	t.Helper()
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tarball := tar.NewWriter(gz)
	for name, data := range payload {
		if err := tarball.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarball.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarball.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return archive.Bytes()
}

func writeUpgradeFixture(t *testing.T, root string, payload map[string][]byte) string {
	t.Helper()
	for name, data := range payload {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(root, "Spool.app", "Contents", "MacOS", "Spool")
	case "windows":
		return filepath.Join(root, "Spool.exe")
	default:
		return filepath.Join(root, "spool")
	}
}

func waitUpgradeFile(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if data, err := os.ReadFile(path); err == nil && string(data) == want {
			return
		}
		select {
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		case <-deadline.C:
			t.Fatalf("upgrade did not produce %s = %q", filepath.Base(path), want)
		case <-ticker.C:
		}
	}
}

func TestMyGoUpgradeReplacesApplicationAndPreservesUserData(t *testing.T) {
	pub, key, manifest := manifestFixture(t)
	publicKey := base64.StdEncoding.EncodeToString(pub)
	buildRoot := t.TempDir()
	current := buildUpgradeFixture(t, buildRoot, "current", "0.1.0", publicKey, false)
	latest := buildUpgradeFixture(t, buildRoot, "latest", "0.1.2", publicKey, false)
	legacy := buildUpgradeFixture(t, buildRoot, "legacy", "0.1.0", publicKey, true)
	archive := upgradeArchive(t, updaterPayload(latest, "0.1.2"))
	manifest.Target = runtime.GOOS + "-" + runtime.GOARCH
	manifest.URL = "https://github.com/owner/spool/releases/download/v0.1.2/spool-0.1.2-" + manifest.Target + ".tar.gz"
	manifest.Size = int64(len(archive))
	sum := sha256.Sum256(archive)
	manifest.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(key, sum[:]))
	metadata, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name   string
		binary []byte
		fault  string
	}{
		{"legacy-to-latest", legacy, ""}, {"latest-to-latest", current, ""},
		{"interrupted-download", current, "truncated"}, {"canceled-download", current, "cancel"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			root := t.TempDir()
			install := filepath.Join(root, "installation space's")
			binary := writeUpgradeFixture(t, install, updaterPayload(scenario.binary, "0.1.0"))
			for name, data := range map[string][]byte{"manifest.json": metadata, "manifest.sig": ed25519.Sign(key, metadata), "archive": archive, "fault": []byte(scenario.fault)} {
				if err := os.WriteFile(filepath.Join(root, name), data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			userData := filepath.Join(install, "user-notes.txt")
			if err := os.WriteFile(userData, []byte("keep my data"), 0o600); err != nil {
				t.Fatal(err)
			}
			mode := "install"
			if os.Getenv("SPOOL_UPDATER_E2E") == "1" && scenario.fault == "" {
				mode = "relaunch"
			}
			command := exec.CommandContext(t.Context(), binary, root, mode)
			command.Env = append(os.Environ(), "APPIMAGE=")
			output, installErr := command.CombinedOutput()
			if scenario.fault != "" {
				if installErr == nil {
					t.Fatalf("failed download installed: %s", output)
				}
				data, err := os.ReadFile(binary)
				if err != nil || !bytes.Equal(data, scenario.binary) {
					t.Fatal("failed download changed the installed executable")
				}
			} else {
				if installErr != nil {
					t.Fatalf("install: %v\n%s", installErr, output)
				}
				versionFile := filepath.Join(install, "payload-version")
				if runtime.GOOS == "darwin" {
					versionFile = filepath.Join(install, "Spool.app", "Contents", "payload-version")
				}
				waitUpgradeFile(t, versionFile, "0.1.2")
				if mode == "relaunch" {
					waitUpgradeFile(t, filepath.Join(root, "restarted-version"), "0.1.2")
					waitUpgradeFile(t, filepath.Join(root, "quit-0.1.0"), "closed")
					waitUpgradeFile(t, filepath.Join(root, "quit-0.1.2"), "closed")
				}
				check := exec.CommandContext(t.Context(), binary, "--version")
				version, err := check.CombinedOutput()
				if err != nil || strings.TrimSpace(string(version)) != "0.1.2" {
					t.Fatalf("installed program did not run: %v %s", err, version)
				}
			}
			if data, err := os.ReadFile(userData); err != nil || string(data) != "keep my data" {
				t.Fatal("upgrade changed user data")
			}
			if data, err := os.ReadFile(filepath.Join(root, "error")); err == nil {
				t.Fatal(fmt.Sprintf("native updater failed: %s", data))
			}
		})
	}
}
