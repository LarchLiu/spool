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
	"runtime"
	"slices"
	"strings"
	"time"
)

// All dependency commands belong to the local install job. Tests supply a
// runner and isolated PATH, so they never invoke the host's package manager.
type buildEnvironment struct {
	goos       string
	env        []string
	log        io.Writer
	step       func(string)
	run        func(*exec.Cmd) error
	appleClang string
}

func newBuildEnvironment(env []string, log io.Writer, step func(string)) *buildEnvironment {
	home, _ := os.UserHomeDir()
	b := &buildEnvironment{goos: runtime.GOOS, env: env, log: log, step: step, run: func(cmd *exec.Cmd) error { return cmd.Run() }}
	bunRoot := b.get("BUN_INSTALL")
	if bunRoot == "" {
		bunRoot = filepath.Join(home, ".bun")
	}
	b.addPaths("PATH", filepath.Join(bunRoot, "bin"))
	cargoRoot := b.get("CARGO_HOME")
	if cargoRoot == "" {
		cargoRoot = filepath.Join(home, ".cargo")
	}
	b.addPaths("PATH", filepath.Join(cargoRoot, "bin"))
	switch b.goos {
	case "darwin":
		b.addPaths("PATH", "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin")
	case "linux":
		b.addPaths("PATH", "/usr/local/bin", "/usr/bin", "/bin")
	case "windows":
		b.addPaths("PATH", filepath.Join(b.get("APPDATA"), "npm"), filepath.Join(b.get("ProgramFiles"), "LLVM", "bin"))
	}
	return b
}

func (b *buildEnvironment) get(key string) string {
	for _, item := range b.env {
		if name, value, ok := strings.Cut(item, "="); ok && strings.EqualFold(name, key) {
			return value
		}
	}
	return ""
}

func (b *buildEnvironment) set(key, value string) {
	b.env = append(withoutEnv(b.env, key), key+"="+value)
}

func (b *buildEnvironment) addPaths(key string, paths ...string) {
	parts := filepath.SplitList(b.get(key))
	for _, path := range paths {
		if path != "" && !slices.Contains(parts, path) {
			parts = append(parts, path)
		}
	}
	b.set(key, strings.Join(parts, string(os.PathListSeparator)))
}

func (b *buildEnvironment) find(name string) []string {
	if b.goos == "windows" && filepath.Ext(name) == "" {
		name += ".exe"
	}
	var found []string
	for _, dir := range filepath.SplitList(b.get("PATH")) {
		if !filepath.IsAbs(dir) {
			continue
		}
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && (b.goos == "windows" || info.Mode()&0111 != 0) {
			found = append(found, path)
		}
	}
	return found
}

func (b *buildEnvironment) command(ctx context.Context, executable string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Env = b.env
	// Run package managers outside this repository so vcpkg cannot pick up an
	// unrelated manifest. Build probes and generated files stay in staging.
	cmd.Dir = b.get("TMPDIR")
	configureCommand(cmd)
	return cmd
}

func (b *buildEnvironment) output(ctx context.Context, executable string, args ...string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := b.command(probeCtx, executable, args...)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, b.log
	err := b.run(cmd)
	return strings.TrimSpace(output.String()), err
}

func (b *buildEnvironment) usable(ctx context.Context, name string) string {
	for _, path := range b.find(name) {
		if _, err := b.output(ctx, path, "--version"); err == nil {
			return path
		}
	}
	return ""
}

func (b *buildEnvironment) esbuild(ctx context.Context) string {
	if path := b.get("ESBUILD"); filepath.IsAbs(path) {
		if _, err := b.output(ctx, path, "--version"); err == nil {
			return path
		}
	}
	if b.goos == "windows" {
		// Global package managers expose script shims. Prefer the native binary
		// so probing and Cargo do not need a JavaScript runtime or a shell.
		for _, dir := range filepath.SplitList(b.get("PATH")) {
			if !filepath.IsAbs(dir) {
				continue
			}
			for _, pattern := range []string{
				"../install/global/node_modules/@esbuild/win32-*/esbuild.exe",
				"../install/global/node_modules/esbuild/node_modules/@esbuild/win32-*/esbuild.exe",
				"node_modules/@esbuild/win32-*/esbuild.exe",
				"node_modules/esbuild/node_modules/@esbuild/win32-*/esbuild.exe",
			} {
				paths, _ := filepath.Glob(filepath.Join(dir, filepath.FromSlash(pattern)))
				for _, path := range paths {
					if _, err := b.output(ctx, path, "--version"); err == nil {
						return path
					}
				}
			}
		}
	}
	return b.usable(ctx, "esbuild")
}

func (b *buildEnvironment) prepare(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	b.step("Checking voxl build dependencies…")
	switch b.goos {
	case "darwin":
		return b.prepareMacOS(ctx)
	case "linux":
		return b.prepareLinux(ctx)
	case "windows":
		return b.prepareWindows(ctx)
	default:
		return "", errors.New("voxl supports macOS, Windows and Linux with X11.")
	}
}

func dependencyError(message string, missing []string) error {
	return fmt.Errorf("%s\n%s", message, strings.Join(missing, ", "))
}

// clang-sys accepts either the library file or its directory. Resolve the actual
// compiler, since /usr/bin/clang can be a symlink or an Apple toolchain shim.
func (b *buildEnvironment) libclang(clang, prefix string) string {
	paths := []string{b.get("LIBCLANG_PATH")}
	for _, compiler := range []string{clang, b.appleClang} {
		if compiler == "" {
			continue
		}
		paths = append(paths, filepath.Join(filepath.Dir(compiler), "..", "lib"), filepath.Dir(compiler))
		if resolved, err := filepath.EvalSymlinks(compiler); err == nil {
			paths = append(paths, filepath.Join(filepath.Dir(resolved), "..", "lib"))
		}
	}
	if prefix != "" {
		paths = append(paths, filepath.Join(prefix, "opt", "llvm", "lib"))
	}
	if b.goos == "linux" {
		paths = append(paths, "/usr/lib/llvm-*/lib", "/usr/local/lib", "/usr/lib", "/usr/lib64", "/usr/lib/*-linux-gnu")
	}
	names := []string{"libclang.dylib"}
	if b.goos == "linux" {
		names = []string{"libclang.so", "libclang.so.*", "libclang-*.so*"}
	}
	if b.goos == "windows" {
		names = []string{"libclang.dll"}
	}
	for _, pattern := range paths {
		if pattern == "" {
			continue
		}
		dirs, _ := filepath.Glob(pattern)
		for _, path := range dirs {
			if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
				return path
			}
			for _, name := range names {
				files, _ := filepath.Glob(filepath.Join(path, name))
				for _, file := range files {
					if info, err := os.Stat(file); err == nil && info.Mode().IsRegular() {
						return file
					}
				}
			}
		}
	}
	return ""
}
