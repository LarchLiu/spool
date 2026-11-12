package voxl

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func temporaryRustFixture(t *testing.T) *dependencyFixture {
	t.Helper()
	f := newDependencyFixture(t, "linux")
	f.ready()
	t.Cleanup(f.b.cleanupTemporaryRust)
	f.b.download = func(ctx context.Context, address string, limit int64) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !strings.HasPrefix(address, "https://static.rust-lang.org/rustup/dist/") {
			t.Fatalf("unexpected download: %s", address)
		}
		binary := []byte("inert rustup installer")
		if strings.HasSuffix(address, ".sha256") {
			return fmt.Appendf(nil, "%x  rustup-init\n", sha256.Sum256(binary)), nil
		}
		return binary, nil
	}
	return f
}

func TestBuildLinuxTemporaryRustIsIsolatedAndCleaned(t *testing.T) {
	for _, scenario := range []string{"missing", "old", "broken"} {
		t.Run(scenario, func(t *testing.T) {
			f := temporaryRustFixture(t)
			for _, name := range []string{"cargo", "rustc"} {
				file := filepath.Join(f.bin, name)
				switch scenario {
				case "missing":
					if err := os.Remove(file); err != nil {
						t.Fatal(err)
					}
				case "old":
					f.versions[file] = "1.75.0"
				case "broken":
					f.broken[file] = true
				}
			}
			userCargo, userRustup := filepath.Join(f.root, "user-cargo"), filepath.Join(f.root, "user-rustup")
			settings := f.file(filepath.Join(userRustup, "settings.toml"))
			f.b.set("CARGO_HOME", userCargo)
			f.b.set("RUSTUP_HOME", userRustup)
			f.b.set("RUSTUP_TOOLCHAIN", "1.75.0")
			f.b.set("RUSTC_WRAPPER", "/user/wrapper")
			original := slices.Clone(f.b.env)
			f.onInstall = func(cmd *exec.Cmd) error {
				if !slices.Contains(cmd.Args, "--no-modify-path") || !slices.Contains(cmd.Args, "minimal") {
					t.Fatalf("unsafe installer arguments: %v", cmd.Args)
				}
				if f.b.get("CARGO_HOME") == userCargo || f.b.get("RUSTUP_HOME") == userRustup || f.b.get("RUSTUP_TOOLCHAIN") != "stable" || f.b.get("RUSTC_WRAPPER") != "" {
					t.Fatalf("user toolchain environment leaked into bootstrap: %v", cmd.Env)
				}
				for _, name := range []string{"cargo", "rustc"} {
					f.tool(filepath.Join(f.b.get("CARGO_HOME"), "bin"), name)
				}
				return nil
			}
			cargo, err := f.b.prepare(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			root := f.b.rustRoot
			if root == "" || !strings.HasPrefix(cargo, root+string(os.PathSeparator)) || !strings.HasPrefix(f.b.get("RUSTC"), root+string(os.PathSeparator)) {
				t.Fatalf("temporary compiler was not selected: cargo=%q rustc=%q", cargo, f.b.get("RUSTC"))
			}
			if !slices.Contains(original, "CARGO_HOME="+userCargo) || !slices.Contains(original, "RUSTUP_TOOLCHAIN=1.75.0") {
				t.Fatal("original environment was modified")
			}
			if data, err := os.ReadFile(settings); err != nil || string(data) != "inert fixture" {
				t.Fatalf("user settings changed: %q %v", data, err)
			}
			f.b.cleanupTemporaryRust()
			if _, err := os.Stat(root); !os.IsNotExist(err) {
				t.Fatalf("temporary Rust was retained: %v", err)
			}
			if _, err := os.Stat(settings); err != nil {
				t.Fatal("user toolchain was removed")
			}
		})
	}
}

func TestBuildLinuxTemporaryRustFailureAndCancellationCleanUp(t *testing.T) {
	for _, scenario := range []string{"download", "checksum", "install", "recheck", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			f := temporaryRustFixture(t)
			if err := os.Remove(filepath.Join(f.bin, "cargo")); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			download := f.b.download
			f.b.download = func(ctx context.Context, address string, limit int64) ([]byte, error) {
				if scenario == "download" {
					return nil, errors.New("download failed")
				}
				if scenario == "checksum" && strings.HasSuffix(address, ".sha256") {
					return []byte(strings.Repeat("0", 64)), nil
				}
				return download(ctx, address, limit)
			}
			f.onInstall = func(*exec.Cmd) error {
				if scenario == "canceled" {
					cancel()
					return ctx.Err()
				}
				if scenario == "recheck" {
					return nil
				}
				return errors.New("installer failed")
			}
			_, err := f.b.prepare(ctx)
			if err == nil {
				t.Fatal("failed temporary toolchain accepted")
			}
			if scenario == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
			if _, err := os.Stat(f.b.rustRoot); !os.IsNotExist(err) {
				t.Fatalf("failed bootstrap retained its files: %v", err)
			}
		})
	}
}

func TestBuildLinuxDoesNotDownloadRustBeforeNativeDependenciesAreReady(t *testing.T) {
	f := temporaryRustFixture(t)
	if err := os.Remove(filepath.Join(f.bin, "cargo")); err != nil {
		t.Fatal(err)
	}
	f.libs["xcb"] = false
	f.b.download = func(context.Context, string, int64) ([]byte, error) {
		t.Fatal("downloaded before checking native libraries")
		return nil, nil
	}
	if _, err := f.b.prepare(t.Context()); err == nil || !strings.Contains(err.Error(), "xcb") {
		t.Fatalf("missing native dependency ignored: %v", err)
	}
}

func TestToolDownloadBoundsStatusAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		fmt.Fprint(w, "tool binary")
	}))
	defer server.Close()
	if _, err := downloadBuildTool(t.Context(), server.URL, 4); err == nil {
		t.Fatal("oversized download accepted")
	}
	if _, err := downloadBuildTool(t.Context(), server.URL+"/missing", 1024); err == nil {
		t.Fatal("HTTP error accepted")
	}
	data, err := downloadBuildTool(t.Context(), server.URL, 1024)
	if err != nil || string(data) != "tool binary" {
		t.Fatalf("download=%q err=%v", data, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := downloadBuildTool(ctx, server.URL, 1024); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}
