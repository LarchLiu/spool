package voxl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// Executables are inert files. Only this runner can execute a dependency
// command, including in tests for platforms other than the test host.
type dependencyFixture struct {
	t                      *testing.T
	b                      *buildEnvironment
	root, bin, prefix, sdk string
	libs                   map[string]bool
	calls                  [][]string
	steps                  []string
	onInstall              func(*exec.Cmd) error
	broken                 map[string]bool
	versions               map[string]string
	yuvLink                bool
	host                   string
}

func newDependencyFixture(t *testing.T, goos string) *dependencyFixture {
	t.Helper()
	root := t.TempDir()
	f := &dependencyFixture{t: t, root: root, bin: filepath.Join(root, "tools", "bin"), prefix: filepath.Join(root, "brew"), sdk: filepath.Join(root, "sdk"), libs: map[string]bool{}, broken: map[string]bool{}, versions: map[string]string{}, host: "x86_64-pc-windows-msvc"}
	for _, dir := range []string{f.bin, f.sdk, filepath.Join(root, "tmp")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	f.b = &buildEnvironment{goos: goos, env: []string{"PATH=" + f.bin, "TMPDIR=" + filepath.Join(root, "tmp")}, log: &bytes.Buffer{}, run: f.run, step: func(s string) { f.steps = append(f.steps, s) }}
	f.b.download = func(context.Context, string, int64) ([]byte, error) {
		return nil, errors.New("unexpected fixture download")
	}
	return f
}

func (f *dependencyFixture) file(path string) string {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("inert fixture"), 0700); err != nil {
		f.t.Fatal(err)
	}
	return path
}

func (f *dependencyFixture) tool(dir, name string) string {
	f.t.Helper()
	if f.b.goos == "windows" {
		name += ".exe"
	}
	return f.file(filepath.Join(dir, name))
}

func (f *dependencyFixture) ready() {
	f.t.Helper()
	for _, tool := range []string{"cargo", "rustc", "clang", "esbuild"} {
		f.tool(f.bin, tool)
	}
	switch f.b.goos {
	case "darwin":
		f.tool(f.bin, "xcrun")
		f.tool(f.bin, "pkg-config")
		f.file(filepath.Join(f.bin, "..", "lib", "libclang.dylib"))
	case "linux":
		f.tool(f.bin, "pkg-config")
		f.tool(f.bin, "cc")
		f.file(filepath.Join(f.bin, "..", "lib", "libclang.so.18"))
	case "windows":
		f.tool(f.bin, "vcpkg")
		f.file(filepath.Join(f.bin, "libclang.dll"))
	}
	for _, lib := range []string{"x264", "opus", "libyuv", "libpulse-simple", "xcb"} {
		f.libs[lib] = true
	}
}

func (f *dependencyFixture) run(cmd *exec.Cmd) error {
	f.t.Helper()
	if cmd.Dir != filepath.Join(f.root, "tmp") {
		f.t.Fatalf("command escaped staging: %q", cmd.Dir)
	}
	if !filepath.IsAbs(cmd.Path) {
		f.t.Fatalf("unresolved command: %q", cmd.Path)
	}
	f.calls = append(f.calls, slices.Clone(cmd.Args))
	if f.broken[cmd.Path] {
		return errors.New("broken tool")
	}
	if _, err := os.Stat(cmd.Path); err != nil {
		return err
	}
	name := strings.TrimSuffix(filepath.Base(cmd.Path), ".exe")
	args := cmd.Args[1:]
	switch {
	case name == "brew" && slices.Equal(args, []string{"--prefix"}):
		fmt.Fprint(cmd.Stdout, f.prefix)
	case name == "xcrun" && slices.Equal(args, []string{"--show-sdk-path"}):
		fmt.Fprint(cmd.Stdout, f.sdk)
	case name == "xcrun" && slices.Equal(args, []string{"--find", "clang"}):
		fmt.Fprint(cmd.Stdout, filepath.Join(f.bin, "clang"))
	case name == "rustc" && slices.Equal(args, []string{"-vV"}):
		fmt.Fprintln(cmd.Stdout, "host: "+f.host)
	case (name == "rustc" || name == "cargo") && slices.Equal(args, []string{"--version"}):
		version := f.versions[cmd.Path]
		if version == "" {
			version = "1.85.0"
		}
		fmt.Fprintln(cmd.Stdout, name+" "+version+" (fixture)")
	case name == "pkg-config" && len(args) == 2 && args[0] == "--exists":
		if !f.libs[args[1]] {
			return errors.New("missing " + args[1])
		}
	case name == "clang" && len(args) > 0 && args[0] == "-x":
		if !f.yuvLink {
			return errors.New("cannot link libyuv")
		}
		if input, _ := io.ReadAll(cmd.Stdin); !bytes.Contains(input, []byte("main")) {
			f.t.Fatal("link probe omitted input")
		}
	case (name == "brew" || name == "vcpkg") && len(args) > 0 && args[0] == "install":
		if f.onInstall != nil {
			return f.onInstall(cmd)
		}
	case name == "rustup-init":
		if f.onInstall != nil {
			return f.onInstall(cmd)
		}
		for _, name := range []string{"cargo", "rustc"} {
			f.tool(filepath.Join(f.b.get("CARGO_HOME"), "bin"), name)
		}
	case slices.Equal(args, []string{"--version"}), name == "vcpkg" && slices.Equal(args, []string{"version"}):
		fmt.Fprintln(cmd.Stdout, name+" fixture version")
	default:
		f.t.Fatalf("unexpected command: %v", cmd.Args)
	}
	return nil
}

func (f *dependencyFixture) installs() [][]string {
	var result [][]string
	for _, call := range f.calls {
		if len(call) > 1 && call[1] == "install" {
			result = append(result, call[1:])
		}
	}
	return result
}

func TestBuildMacOSReadyWithoutHomebrew(t *testing.T) {
	f := newDependencyFixture(t, "darwin")
	f.ready()
	// macOS does not depend on Linux's libyuv and PulseAudio.
	f.libs["libyuv"], f.libs["libpulse-simple"] = false, false
	cargo, err := f.b.prepare(t.Context())
	if err != nil || cargo != filepath.Join(f.bin, "cargo") {
		t.Fatalf("cargo=%q err=%v", cargo, err)
	}
	if len(f.installs()) != 0 || f.b.get("SDKROOT") != f.sdk || f.b.get("LIBCLANG_PATH") == "" {
		t.Fatal("ready toolchain not preserved")
	}
}

func TestBuildMacOSInstallsOnlyMissingDependenciesAndUsesPrefix(t *testing.T) {
	f := newDependencyFixture(t, "darwin")
	f.ready()
	f.tool(f.bin, "brew")
	f.libs["opus"] = false
	if err := os.Remove(filepath.Join(f.bin, "esbuild")); err != nil {
		t.Fatal(err)
	}
	f.onInstall = func(cmd *exec.Cmd) error {
		for _, expected := range []string{"NONINTERACTIVE=1", "HOMEBREW_NO_AUTO_UPDATE=1", "HOMEBREW_NO_INSTALL_CLEANUP=1"} {
			if !slices.Contains(cmd.Env, expected) {
				t.Fatalf("missing environment %s", expected)
			}
		}
		f.tool(filepath.Join(f.prefix, "bin"), "esbuild")
		f.libs["opus"] = true
		return nil
	}
	_, err := f.b.prepare(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.installs(), [][]string{{"install", "opus", "esbuild"}}) {
		t.Fatalf("installs: %v", f.installs())
	}
	if f.b.get("ESBUILD") != filepath.Join(f.prefix, "bin", "esbuild") || !strings.Contains(f.b.get("PKG_CONFIG_PATH"), filepath.Join(f.prefix, "lib", "pkgconfig")) {
		t.Fatal("custom Homebrew prefix not exported")
	}
}

func TestBuildMacOSFindsWorkingRustAfterBrokenRustup(t *testing.T) {
	f := newDependencyFixture(t, "darwin")
	f.ready()
	f.tool(f.bin, "brew")
	f.broken[filepath.Join(f.bin, "cargo")] = true
	f.broken[filepath.Join(f.bin, "rustc")] = true
	for _, tool := range []string{"cargo", "rustc"} {
		f.tool(filepath.Join(f.prefix, "opt", "rust", "bin"), tool)
	}
	cargo, err := f.b.prepare(t.Context())
	if err != nil || cargo != filepath.Join(f.prefix, "opt", "rust", "bin", "cargo") {
		t.Fatalf("cargo=%s err=%v", cargo, err)
	}
	if f.b.get("RUSTC") != filepath.Join(f.prefix, "opt", "rust", "bin", "rustc") || len(f.installs()) != 0 {
		t.Fatal("working Rust ignored")
	}
}

func TestBuildMacOSBootstrapsAllMissingDependencies(t *testing.T) {
	f := newDependencyFixture(t, "darwin")
	f.tool(f.bin, "xcrun")
	f.tool(f.bin, "brew")
	f.onInstall = func(cmd *exec.Cmd) error {
		for _, tool := range []string{"cargo", "rustc", "pkg-config", "esbuild", "clang"} {
			f.tool(filepath.Join(f.prefix, "bin"), tool)
		}
		f.file(filepath.Join(f.prefix, "opt", "llvm", "lib", "libclang.dylib"))
		f.libs["x264"], f.libs["opus"] = true, true
		return nil
	}
	_, err := f.b.prepare(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"install", "rust", "pkgconf", "x264", "opus", "esbuild", "llvm"}}
	if !reflect.DeepEqual(f.installs(), want) {
		t.Fatalf("installs=%v", f.installs())
	}
	if f.b.get("LIBCLANG_PATH") != filepath.Join(f.prefix, "opt", "llvm", "lib", "libclang.dylib") {
		t.Fatal("Homebrew LLVM was not selected for bindgen")
	}
}

func TestBuildMacOSDependencyFailures(t *testing.T) {
	for _, scenario := range []string{"no-sdk", "no-brew", "brew-failed", "recheck-failed", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			f := newDependencyFixture(t, "darwin")
			f.ready()
			f.libs["x264"] = false
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			expected := "Homebrew is required"
			switch scenario {
			case "no-sdk":
				f.sdk = filepath.Join(f.root, "missing-sdk")
				expected = "Xcode Command Line Tools"
			case "brew-failed", "recheck-failed", "canceled":
				f.tool(f.bin, "brew")
				expected = "still unavailable"
				if scenario == "brew-failed" {
					expected = "Could not install"
					f.onInstall = func(*exec.Cmd) error { return errors.New("package failure") }
				}
				if scenario == "canceled" {
					f.onInstall = func(*exec.Cmd) error { cancel(); return ctx.Err() }
				}
			}
			_, err := f.b.prepare(ctx)
			if scenario == "canceled" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("err=%v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), expected) {
				t.Fatalf("err=%v", err)
			}
			if (scenario == "no-brew" || scenario == "no-sdk") && len(f.installs()) != 0 {
				t.Fatal("unexpected package install")
			}
		})
	}
}

func TestBuildLinuxChecksPulseAudioAndAcceptsLibyuvWithoutPC(t *testing.T) {
	f := newDependencyFixture(t, "linux")
	f.ready()
	f.libs["libyuv"] = false
	f.yuvLink = true
	if _, err := f.b.prepare(t.Context()); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(f.b.get("TMPDIR"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("link probe not cleaned up: %v %v", entries, err)
	}
	if len(f.installs()) != 0 {
		t.Fatal("Linux must not invoke a system package manager")
	}
	f.libs["libpulse-simple"], f.yuvLink = false, false
	_, err = f.b.prepare(t.Context())
	if err == nil || !strings.Contains(err.Error(), "libpulse-simple") || !strings.Contains(err.Error(), "libyuv") {
		t.Fatalf("missing dependency details: %v", err)
	}
}

func TestBuildLinuxRejectsMissingXCBDevelopmentPackage(t *testing.T) {
	f := newDependencyFixture(t, "linux")
	f.ready()
	f.libs["xcb"] = false
	_, err := f.b.prepare(t.Context())
	if err == nil || !strings.Contains(err.Error(), "xcb") {
		t.Fatalf("missing XCB development package was not reported: %v", err)
	}
	if len(f.installs()) != 0 {
		t.Fatal("Linux must not invoke a system package manager")
	}
}

func TestBuildLinuxSkipsOldDistributionRustForUserToolchain(t *testing.T) {
	f := newDependencyFixture(t, "linux")
	f.ready()
	cargoHome := filepath.Join(f.root, "cargo")
	userBin := filepath.Join(cargoHome, "bin")
	for _, name := range []string{"cargo", "rustc"} {
		f.versions[filepath.Join(f.bin, name)] = "1.75.0"
		f.tool(userBin, name)
	}
	b := newBuildEnvironment([]string{"PATH=" + f.bin, "CARGO_HOME=" + cargoHome, "TMPDIR=" + f.b.get("TMPDIR")}, f.b.log, f.b.step)
	b.goos, b.run = "linux", f.run
	f.b = b
	cargo, err := b.prepare(t.Context())
	if err != nil || cargo != filepath.Join(userBin, "cargo") || b.get("RUSTC") != filepath.Join(userBin, "rustc") {
		t.Fatalf("compatible user Rust was not selected: cargo=%q rustc=%q err=%v", cargo, b.get("RUSTC"), err)
	}
}

func TestBuildLinuxDoesNotUseRustOlderThanVoxlRequires(t *testing.T) {
	f := newDependencyFixture(t, "linux")
	f.ready()
	for _, name := range []string{"cargo", "rustc"} {
		f.versions[filepath.Join(f.bin, name)] = "1.75.0"
	}
	_, err := f.b.prepare(t.Context())
	if err == nil || !strings.Contains(err.Error(), "unexpected fixture download") {
		t.Fatalf("incompatible Rust was used instead of preparing temporary tooling: %v", err)
	}
}

func TestBuildWindowsPreparesNativeTripletAndGlobalEsbuild(t *testing.T) {
	for host, triplet := range map[string]string{"x86_64-pc-windows-msvc": "x64-windows-static-md", "aarch64-pc-windows-msvc": "arm64-windows-static-md"} {
		t.Run(host, func(t *testing.T) {
			f := newDependencyFixture(t, "windows")
			f.ready()
			f.host = host
			if err := os.Remove(filepath.Join(f.bin, "esbuild.exe")); err != nil {
				t.Fatal(err)
			}
			esbuild := f.file(filepath.Join(f.bin, "node_modules", "@esbuild", "win32-x64", "esbuild.exe"))
			_, err := f.b.prepare(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if f.b.get("ESBUILD") != esbuild || f.b.get("VCPKGRS_TRIPLET") != triplet || f.b.get("VCPKG_ROOT") != f.bin {
				t.Fatalf("incorrect build environment: %v", f.b.env)
			}
			want := [][]string{{"install", "x264", "libyuv", "opus", "--triplet", triplet, "--classic", "--vcpkg-root=" + f.bin}}
			if !reflect.DeepEqual(f.installs(), want) {
				t.Fatalf("installs: %v", f.installs())
			}
		})
	}
}

func TestBuildWindowsUsesBunGlobalNativeEsbuildBeforeShim(t *testing.T) {
	f := newDependencyFixture(t, "windows")
	f.ready()
	esbuild := f.file(filepath.Join(f.bin, "..", "install", "global", "node_modules", "@esbuild", "win32-arm64", "esbuild.exe"))
	if _, err := f.b.prepare(t.Context()); err != nil {
		t.Fatal(err)
	}
	if f.b.get("ESBUILD") != esbuild {
		t.Fatalf("expected native Bun global esbuild %s, got %s", esbuild, f.b.get("ESBUILD"))
	}
}

func TestBuildWindowsRejectsMissingOrIncompatibleToolsBeforeVcpkg(t *testing.T) {
	for _, scenario := range []string{"gnu", "llvm", "esbuild", "vcpkg", "triplet"} {
		t.Run(scenario, func(t *testing.T) {
			f := newDependencyFixture(t, "windows")
			f.ready()
			var remove string
			expected := ""
			switch scenario {
			case "gnu":
				f.host = "x86_64-pc-windows-gnu"
				expected = "MSVC"
			case "llvm":
				remove = "libclang.dll"
				expected = "LLVM"
			case "esbuild":
				remove = "esbuild.exe"
				expected = "bun add --global esbuild"
			case "vcpkg":
				remove = "vcpkg.exe"
				expected = "VCPKG_ROOT"
			case "triplet":
				f.b.set("VCPKGRS_TRIPLET", "x64-windows")
				expected = "windows-static-md"
			}
			if remove != "" {
				if err := os.Remove(filepath.Join(f.bin, remove)); err != nil {
					t.Fatal(err)
				}
			}
			_, err := f.b.prepare(t.Context())
			if err == nil || !strings.Contains(err.Error(), expected) {
				t.Fatalf("err=%v", err)
			}
			if len(f.installs()) != 0 {
				t.Fatal("vcpkg ran before prerequisites were ready")
			}
		})
	}
}

func TestBuildWindowsVcpkgRootAndFailure(t *testing.T) {
	f := newDependencyFixture(t, "windows")
	f.ready()
	if err := os.Remove(filepath.Join(f.bin, "vcpkg.exe")); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(f.root, "vcpkg with spaces")
	f.tool(root, "vcpkg")
	f.b.set("VCPKG_ROOT", root)
	f.onInstall = func(cmd *exec.Cmd) error {
		if cmd.Path != filepath.Join(root, "vcpkg.exe") || !slices.Contains(cmd.Env, "VCPKG_ROOT="+root) {
			t.Fatal("explicit vcpkg root ignored")
		}
		return errors.New("vcpkg failure")
	}
	_, err := f.b.prepare(t.Context())
	if err == nil || !strings.Contains(err.Error(), "Could not prepare voxl dependencies with vcpkg") {
		t.Fatalf("err=%v", err)
	}
}

func TestBuildUnsupportedAndCanceledPlatformsDoNotRunCommands(t *testing.T) {
	f := newDependencyFixture(t, "freebsd")
	if _, err := f.b.prepare(t.Context()); err == nil {
		t.Fatal("unsupported host accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := f.b.prepare(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if len(f.calls) != 0 {
		t.Fatal("unexpected tool invocation")
	}
}
