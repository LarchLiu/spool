// Package macapp installs and launches the locally built macOS desktop helper.
// The small native executable is independent of the Spool release binary, so an
// ordinary Spool update does not replace the app to which TCC grants access.
package macapp

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

const bundleName = "Voxl Helper.app"

// This is an installation format, not the Spool version. Do not bump it for
// ordinary application releases: replacing an ad-hoc signed app loses identity.
const format = "2\n"

//go:embed native/helper.m
var source []byte

//go:embed native/Info.plist
var info []byte

func Bundle(root string) string { return filepath.Join(root, "bin", bundleName) }
func Executable(root string) string {
	return filepath.Join(Bundle(root), "Contents", "MacOS", "voxl")
}
func launcher(root string) string {
	return filepath.Join(Bundle(root), "Contents", "MacOS", "voxl-helper")
}

func Installed(root string) bool {
	return installedFormat(root) == format
}

// NeedsUpgrade recognizes the original helper, whose startup handshake could
// leave an unowned engine. Only an explicit local installation replaces it.
func NeedsUpgrade(root string) bool {
	return installedFormat(root) == "1\n"
}

func installedFormat(root string) string {
	for _, path := range []string{Executable(root), launcher(root), filepath.Join(Bundle(root), "Contents", "Info.plist")} {
		if stat, err := os.Stat(path); err != nil || !stat.Mode().IsRegular() {
			return ""
		}
	}
	data, err := os.ReadFile(filepath.Join(Bundle(root), "Contents", "Resources", "installation-format"))
	if err != nil {
		return ""
	}
	return string(data)
}

// Install publishes one complete application atomically. Nothing launches or
// captures a screen during installation. The caller has already probed voxl.
func Install(ctx context.Context, root, voxl string) error {
	if Installed(root) {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(root, ".helper-install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	app := filepath.Join(stage, bundleName)
	bin := filepath.Join(app, "Contents", "MacOS")
	resources := filepath.Join(app, "Contents", "Resources")
	for _, path := range []string{bin, resources, filepath.Join(stage, "tmp")} {
		if err := os.MkdirAll(path, 0700); err != nil {
			return err
		}
	}
	for path, data := range map[string][]byte{
		filepath.Join(stage, "helper.m"):                source,
		filepath.Join(app, "Contents", "Info.plist"):    info,
		filepath.Join(resources, "installation-format"): []byte(format),
	} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			return err
		}
	}
	if err := copyExecutable(voxl, filepath.Join(bin, "voxl")); err != nil {
		return err
	}
	// These tools are supplied by macOS and Xcode Command Line Tools. Never
	// invoke a package manager, request a certificate, or open the CLT installer.
	run := func(name string, args ...string) error {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = stage
		cmd.Env = append(os.Environ(), "TMPDIR="+filepath.Join(stage, "tmp"), "TMP="+filepath.Join(stage, "tmp"), "TEMP="+filepath.Join(stage, "tmp"))
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
		configureToolCommand(cmd)
		return cmd.Run()
	}
	if err := run("/usr/bin/xcrun", "--sdk", "macosx", "clang", "-O2", "-fobjc-arc", "-Wall", "-Wextra", "-Werror", "-mmacosx-version-min=12.0", "-framework", "AppKit", "-framework", "Foundation", filepath.Join(stage, "helper.m"), "-o", filepath.Join(bin, "voxl-helper")); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("Could not build Voxl Helper. Install Xcode Command Line Tools on this Mac, then retry.")
	}
	if err := run("/usr/bin/codesign", "--force", "--sign", "-", "--identifier", "com.cloudgeek.spool.voxl.engine", filepath.Join(bin, "voxl")); err != nil {
		return errors.New("Could not sign Voxl Helper locally.")
	}
	if err := run("/usr/bin/codesign", "--force", "--sign", "-", app); err != nil {
		return errors.New("Could not sign Voxl Helper locally.")
	}
	if err := run("/usr/bin/codesign", "--verify", "--deep", "--strict", app); err != nil {
		return errors.New("Could not verify Voxl Helper.")
	}
	if err := run(filepath.Join(bin, "voxl"), "--version"); err != nil {
		return errors.New("Could not run the local voxl executable. Check its executable format and runtime libraries.")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if NeedsUpgrade(root) {
		// The old bundle moves into staging only after the replacement is fully
		// verified. Failed publication leaves the previous installation intact.
		return exchangeBundles(app, Bundle(root))
	}
	return os.Rename(app, Bundle(root))
}

func copyExecutable(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	return errors.Join(copyErr, out.Close())
}

// Command starts a native launch client, which asks LaunchServices to launch
// the independent application and waits for it to stop. Its stdin is a lifetime
// lease: cancellation or the death of Spool closes it. The app then stops and
// reaps voxl before the launch client exits. No credentials are put in argv or
// files. Call cleanup after Wait, or after a failed Start.
func Command(ctx context.Context, root string, args ...string) (*exec.Cmd, func(), error) {
	read, write, err := os.Pipe()
	if err != nil {
		return nil, nil, fmt.Errorf("create helper lifetime pipe: %w", err)
	}
	var once sync.Once
	stop := func() { once.Do(func() { _ = write.Close() }) }
	cmd := exec.CommandContext(ctx, launcher(root), append([]string{"--launch"}, args...)...)
	cmd.Stdin = read
	cmd.Cancel = func() error { stop(); return nil }
	cmd.WaitDelay = 10 * time.Second
	return cmd, func() { stop(); _ = read.Close() }, nil
}
