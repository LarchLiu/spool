package main

import (
	"context"
	"io"
	"path/filepath"

	"spool/internal/voxl"
	"spool/internal/workspace"
)

type localVoxl struct {
	voxl.Status
	Enabled bool `json:"enabled"`
}

func writeVoxlStatus(out io.Writer, s *workspace.Service) error {
	settings, err := s.Store.Settings()
	if err != nil {
		return err
	}
	return writeJSON(out, localVoxl{Status: s.VoxlStatus(), Enabled: settings.RemoteDesktopEnabled})
}

func addVoxl(root *command, out io.Writer) {
	c := root.add("voxl", "Install and inspect the local remote desktop helper", "Run locally as the service OS user while other instances using this data directory are stopped.\nEnable with 'config set --remote-desktop=true' and grant access with\n'user update --user USER --remote-desktop=true', then run 'serve' in a desktop session.\nInstallation and enablement do not start capture; voxl starts when an authorized viewer connects.")
	status := c.add("status", "Inspect the local installation and saved enablement", "This does not start the service or desktop capture; running is false while the service is stopped.")
	status.run = func(_ context.Context, s *workspace.Service) error { return writeVoxlStatus(out, s) }
	install := c.add("install", "Install or upgrade voxl locally, then exit", "A current helper is reused unchanged. macOS helper installation format 1 requires an upgrade before connecting to apply the startup cleanup fix. Format 2 and later remain usable while upgrading is optional. A macOS helper version upgrade checks crates.io and builds a newer stable engine if available; otherwise it repackages the bundled engine.\nFor a first installation, searches ~/.cargo/bin and PATH before building the pinned voxl release.\nmacOS needs Xcode Command Line Tools and uses Homebrew for missing build dependencies;\nLinux needs system build libraries; Windows needs MSVC, LLVM, esbuild and vcpkg.\nOn macOS this prepares Voxl Helper.app; replacing it may require Screen Recording and Accessibility permissions again.\nUse --binary to import an explicit local engine without checking upstream.")
	binary := install.flags.String("binary", "", "Absolute path to an existing local voxl executable; empty uses discovery/build")
	install.validate = func() error {
		if *binary != "" && !filepath.IsAbs(*binary) {
			return usage("--binary must be an absolute path")
		}
		return nil
	}
	install.run = func(ctx context.Context, s *workspace.Service) error {
		if err := s.InstallVoxl(ctx, *binary); err != nil {
			return err
		}
		return writeVoxlStatus(out, s)
	}
}
