package voxl

import (
	"context"
	"os"
	"strings"
	"time"
)

func (b *buildEnvironment) prepareLinux(ctx context.Context) (string, error) {
	var missing []string
	cargo, rustc := b.usable(ctx, "cargo"), b.usable(ctx, "rustc")
	if cargo == "" {
		missing = append(missing, "cargo")
	}
	if rustc == "" {
		missing = append(missing, "rustc")
	} else {
		b.set("RUSTC", rustc)
	}
	// The native Rust toolchain links through cc; bindgen also needs libclang.
	if b.usable(ctx, "cc") == "" {
		missing = append(missing, "cc")
	}
	clang := b.usable(ctx, "clang")
	if clang == "" {
		missing = append(missing, "clang")
	}
	if lib := b.libclang(clang, ""); lib == "" {
		missing = append(missing, "libclang")
	} else {
		b.set("LIBCLANG_PATH", lib)
	}
	if esbuild := b.esbuild(ctx); esbuild == "" {
		missing = append(missing, "esbuild")
	} else {
		b.set("ESBUILD", esbuild)
	}
	pkg := b.usable(ctx, "pkg-config")
	if pkg == "" {
		missing = append(missing, "pkg-config")
	} else {
		b.set("PKG_CONFIG", pkg)
	}
	for _, lib := range []string{"x264", "opus", "libpulse-simple"} {
		if pkg == "" {
			missing = append(missing, lib)
		} else if _, err := b.output(ctx, pkg, "--exists", lib); err != nil {
			missing = append(missing, lib)
		}
	}
	if !b.hasLibyuv(ctx, pkg, clang) {
		missing = append(missing, "libyuv")
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if len(missing) > 0 {
		return "", dependencyError("Install the missing Linux voxl build dependencies with your system package manager, then retry. See the Spool README for package names.", missing)
	}
	// DISPLAY is needed when streaming, not when compiling; headless hosts can
	// build before starting X11/Xvfb. Wayland alone is unsupported upstream.
	return cargo, nil
}

func (b *buildEnvironment) hasLibyuv(ctx context.Context, pkg, clang string) bool {
	if pkg != "" {
		if _, err := b.output(ctx, pkg, "--exists", "libyuv"); err == nil {
			return true
		}
	}
	// Debian and Arch's libyuv packages may have no .pc file. Match upstream's
	// -lyuv fallback with a link probe; never run the resulting executable.
	if clang == "" || ctx.Err() != nil {
		return false
	}
	file, err := os.CreateTemp(b.get("TMPDIR"), ".voxl-yuv-*")
	if err != nil {
		return false
	}
	name := file.Name()
	_ = file.Close()
	defer os.Remove(name)
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := b.command(probeCtx, clang, "-x", "c", "-", "-o", name, "-lyuv")
	cmd.Stdin = strings.NewReader("int main(void) { return 0; }\n")
	cmd.Stdout, cmd.Stderr = b.log, b.log
	return b.run(cmd) == nil
}
