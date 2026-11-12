package voxl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Only the local Linux voxl install job calls this, after native prerequisites
// pass. Its owner cleans up after Cargo exits, including failure/cancellation.
func (b *buildEnvironment) prepareTemporaryRust(ctx context.Context) (_ string, installErr error) {
	host := map[string]string{"amd64": "x86_64-unknown-linux-gnu", "arm64": "aarch64-unknown-linux-gnu"}[runtime.GOARCH]
	if host == "" {
		return "", errors.New("Install Rust/Cargo 1.85+ for this Linux architecture, then retry.")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	root, err := os.MkdirTemp(b.get("TMPDIR"), ".voxl-rust-")
	if err != nil {
		return "", err
	}
	b.rustRoot = root
	defer func() {
		if installErr != nil {
			b.cleanupTemporaryRust()
		}
	}()
	b.step("Preparing a temporary Rust toolchain for voxl…")
	address := "https://static.rust-lang.org/rustup/dist/" + host + "/rustup-init"
	download := b.download
	if download == nil {
		download = downloadBuildTool
	}
	binary, err := download(ctx, address, 64*1024*1024)
	if err != nil {
		return "", fmt.Errorf("Could not download temporary Rust tooling.\n%w", err)
	}
	checksums, err := download(ctx, address+".sha256", 1024)
	if err != nil {
		return "", fmt.Errorf("Could not verify temporary Rust tooling.\n%w", err)
	}
	fields := strings.Fields(string(checksums))
	digest := sha256.Sum256(binary)
	if len(fields) == 0 || fields[0] != hex.EncodeToString(digest[:]) {
		return "", errors.New("Temporary Rust installer checksum mismatch.")
	}
	installer := filepath.Join(root, "rustup-init")
	if err := os.WriteFile(installer, binary, 0700); err != nil {
		return "", err
	}
	// Even when the user already owns rustup, run a separate installer against
	// private homes. Never call their rustup or modify their default toolchain.
	cargoHome := filepath.Join(root, "cargo")
	bin := filepath.Join(cargoHome, "bin")
	b.env = withoutEnv(b.env, "RUSTC", "RUSTDOC", "RUSTC_WRAPPER", "RUSTC_WORKSPACE_WRAPPER", "CARGO_BUILD_RUSTC", "CARGO_BUILD_RUSTC_WRAPPER", "CARGO_BUILD_RUSTC_WORKSPACE_WRAPPER")
	b.set("CARGO_HOME", cargoHome)
	b.set("RUSTUP_HOME", filepath.Join(root, "rustup"))
	b.set("RUSTUP_TOOLCHAIN", "stable")
	b.set("RUSTUP_AUTO_INSTALL", "0")
	b.set("RUSTUP_INIT_SKIP_PATH_CHECK", "yes")
	b.set("PATH", bin+string(os.PathListSeparator)+b.get("PATH"))
	cmd := b.command(ctx, installer, "-y", "--no-modify-path", "--profile", "minimal", "--default-host", host, "--default-toolchain", "stable")
	cmd.Stdout, cmd.Stderr = b.log, b.log
	if err := b.run(cmd); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", errors.New("Could not prepare temporary Rust. Check access to static.rust-lang.org, then retry.")
	}
	cargo, rustc := filepath.Join(bin, "cargo"), filepath.Join(bin, "rustc")
	for _, tool := range []string{cargo, rustc} {
		version, err := b.output(ctx, tool, "--version")
		if err != nil || !compatibleRustVersion(version, filepath.Base(tool)) {
			return "", errors.New("The temporary Rust toolchain is unavailable or older than 1.85.")
		}
	}
	b.set("RUSTC", rustc)
	b.set("RUSTDOC", filepath.Join(bin, "rustdoc"))
	return cargo, nil
}

func (b *buildEnvironment) cleanupTemporaryRust() {
	if b.rustRoot != "" {
		_ = os.RemoveAll(b.rustRoot)
	}
}

func compatibleRustVersion(version, name string) bool {
	var major, minor int
	n, _ := fmt.Sscanf(version, name+" %d.%d", &major, &minor)
	return n == 2 && (major > 1 || major == 1 && minor >= 85)
}

func downloadBuildTool(ctx context.Context, address string, limit int64) ([]byte, error) {
	client := &http.Client{Timeout: 3 * time.Minute, CheckRedirect: func(request *http.Request, via []*http.Request) error {
		if request.URL.Scheme != "https" || len(via) >= 5 {
			return errors.New("invalid tool download redirect")
		}
		return nil
	}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tool download returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("tool download exceeds size limit")
	}
	return data, nil
}
