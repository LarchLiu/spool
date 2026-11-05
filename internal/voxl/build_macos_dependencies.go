package voxl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (b *buildEnvironment) useHomebrewPrefix(prefix string) {
	b.addPaths("PATH", filepath.Join(prefix, "bin"), filepath.Join(prefix, "opt", "rust", "bin"), filepath.Join(prefix, "opt", "llvm", "bin"))
	b.addPaths("PKG_CONFIG_PATH", filepath.Join(prefix, "lib", "pkgconfig"), filepath.Join(prefix, "share", "pkgconfig"), filepath.Join(prefix, "opt", "x264", "lib", "pkgconfig"), filepath.Join(prefix, "opt", "opus", "lib", "pkgconfig"))
}

func (b *buildEnvironment) prepareMacOS(ctx context.Context) (string, error) {
	// Rust's linker and Apple's frameworks need a working SDK even when clang
	// comes from Homebrew. Do not open an interactive CLT installer in a job.
	xcrun := b.usable(ctx, "xcrun")
	if xcrun == "" {
		return "", errors.New("Install Xcode Command Line Tools on this Mac, then retry.")
	}
	sdk, err := b.output(ctx, xcrun, "--show-sdk-path")
	if info, statErr := os.Stat(sdk); err != nil || statErr != nil || !info.IsDir() {
		return "", errors.New("Install Xcode Command Line Tools on this Mac, then retry.")
	}
	b.set("SDKROOT", sdk)
	if clang, err := b.output(ctx, xcrun, "--find", "clang"); err == nil && filepath.IsAbs(clang) {
		b.appleClang = clang
		b.addPaths("PATH", filepath.Dir(clang))
	}
	brew, prefix := b.usable(ctx, "brew"), ""
	if brew != "" {
		prefix, err = b.output(ctx, brew, "--prefix")
		if err != nil || !filepath.IsAbs(prefix) {
			return "", errors.New("Could not read the Homebrew prefix. Check Homebrew locally, then retry.")
		}
		b.useHomebrewPrefix(prefix)
	}
	missing, cargo := b.missingMacOS(ctx, prefix)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if len(missing) == 0 {
		return cargo, nil
	}
	if brew == "" {
		return "", dependencyError("Homebrew is required to install missing voxl dependencies. Install Homebrew locally, then retry.", missing)
	}
	b.step("Installing missing dependencies with Homebrew…")
	fmt.Fprintln(b.log, "Installing missing voxl dependencies:", strings.Join(missing, ", "))
	cmd := b.command(ctx, brew, append([]string{"install"}, missing...)...)
	// Avoid unattended prompts, unrelated updates and cleanup of host packages.
	cmd.Env = append(withoutEnv(b.env, "NONINTERACTIVE", "HOMEBREW_NO_AUTO_UPDATE", "HOMEBREW_NO_INSTALL_CLEANUP"), "NONINTERACTIVE=1", "HOMEBREW_NO_AUTO_UPDATE=1", "HOMEBREW_NO_INSTALL_CLEANUP=1")
	cmd.Stdout, cmd.Stderr = b.log, b.log
	if err := b.run(cmd); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", errors.New("Could not install voxl dependencies with Homebrew. Check Homebrew and network access, then retry.")
	}
	b.step("Checking voxl build dependencies…")
	missing, cargo = b.missingMacOS(ctx, prefix)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if len(missing) != 0 {
		return "", dependencyError("voxl build dependencies are still unavailable after Homebrew installation.", missing)
	}
	return cargo, nil
}

func (b *buildEnvironment) missingMacOS(ctx context.Context, prefix string) ([]string, string) {
	var missing []string
	cargo, rustc := b.usable(ctx, "cargo"), b.usable(ctx, "rustc")
	if cargo == "" || rustc == "" {
		missing = append(missing, "rust")
	} else {
		// Use the working compiler, including when a broken rustup shim appears
		// earlier on PATH than Homebrew's compiler.
		b.set("RUSTC", rustc)
	}
	pkg := b.usable(ctx, "pkg-config")
	if pkg == "" {
		missing = append(missing, "pkgconf")
	} else {
		b.set("PKG_CONFIG", pkg)
	}
	for _, lib := range []string{"x264", "opus"} {
		if pkg == "" {
			missing = append(missing, lib)
		} else if _, err := b.output(ctx, pkg, "--exists", lib); err != nil {
			missing = append(missing, lib)
		}
	}
	if esbuild := b.esbuild(ctx); esbuild == "" {
		missing = append(missing, "esbuild")
	} else {
		b.set("ESBUILD", esbuild)
	}
	clang := b.usable(ctx, "clang")
	libclang := b.libclang(clang, prefix)
	if clang == "" || libclang == "" {
		missing = append(missing, "llvm")
	} else {
		b.set("LIBCLANG_PATH", libclang)
	}
	return missing, cargo
}
