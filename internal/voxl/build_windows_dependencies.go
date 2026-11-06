package voxl

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
)

func (b *buildEnvironment) prepareWindows(ctx context.Context) (string, error) {
	cargo, rustc := b.usable(ctx, "cargo"), b.usable(ctx, "rustc")
	if cargo == "" || rustc == "" {
		return "", errors.New("Install Rust with an MSVC toolchain and Visual Studio C++ Build Tools on this Windows host, then retry.")
	}
	b.set("RUSTC", rustc)
	version, err := b.output(ctx, rustc, "-vV")
	if err != nil {
		return "", err
	}
	var arch string
	for _, line := range strings.Split(version, "\n") {
		if host, ok := strings.CutPrefix(strings.TrimSpace(line), "host: "); ok {
			switch host {
			case "x86_64-pc-windows-msvc":
				arch = "x64"
			case "aarch64-pc-windows-msvc":
				arch = "arm64"
			case "i686-pc-windows-msvc":
				arch = "x86"
			}
		}
	}
	if arch == "" {
		return "", errors.New("Install Rust with an MSVC toolchain and Visual Studio C++ Build Tools on this Windows host, then retry.")
	}
	triplet := arch + "-windows-static-md"
	if configured := b.get("VCPKGRS_TRIPLET"); configured != "" && configured != triplet {
		return "", errors.New("Set VCPKGRS_TRIPLET to the native MSVC architecture with windows-static-md, then retry.")
	}
	b.set("VCPKGRS_TRIPLET", triplet)
	clang := b.usable(ctx, "clang")
	if lib := b.libclang(clang, ""); lib == "" {
		return "", errors.New("Install LLVM on this Windows host and set LIBCLANG_PATH to its bin directory, then retry.")
	} else {
		b.set("LIBCLANG_PATH", lib)
	}
	if esbuild := b.esbuild(ctx); esbuild == "" {
		return "", errors.New("Install esbuild on this Windows host with bun add --global esbuild, then retry.")
	} else {
		b.set("ESBUILD", esbuild)
	}
	root := b.get("VCPKG_ROOT")
	if root != "" && !filepath.IsAbs(root) {
		return "", errors.New("Install vcpkg on this Windows host and set VCPKG_ROOT to its directory, then retry.")
	}
	if root != "" {
		b.addPaths("PATH", root)
	}
	vcpkg := ""
	// vcpkg uses `version`, rather than the --version convention.
	for _, path := range b.find("vcpkg") {
		if _, err := b.output(ctx, path, "version"); err == nil {
			vcpkg = path
			break
		}
	}
	if vcpkg == "" {
		return "", errors.New("Install vcpkg on this Windows host and set VCPKG_ROOT to its directory, then retry.")
	}
	if root == "" {
		root = filepath.Dir(vcpkg)
	}
	b.set("VCPKG_ROOT", root)
	b.set("VCPKG_DISABLE_METRICS", "1")
	b.step("Preparing Windows dependencies with vcpkg…")
	// vcpkg skips packages already installed for this triplet. Classic mode
	// matches vcpkg-rs's discovery in the upstream build script.
	cmd := b.command(ctx, vcpkg, "install", "x264", "libyuv", "opus", "--triplet", triplet, "--classic", "--vcpkg-root="+root)
	cmd.Stdout, cmd.Stderr = b.log, b.log
	if err := b.run(cmd); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", errors.New("Could not prepare voxl dependencies with vcpkg. Check Visual Studio C++ Build Tools, vcpkg and network access, then retry.")
	}
	return cargo, nil
}
