package voxl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"spool/internal/voxl/macapp"

	"golang.org/x/mod/semver"
)

// Only an explicit local helper upgrade checks upstream. A current helper is
// reused unchanged; status polling and remote connections never check releases.
func (m *Manager) upgradeHelper(ctx context.Context) error {
	m.installStep("Checking for a newer voxl release…")
	current, err := engineVersion(ctx, macapp.Executable(m.root))
	if err != nil {
		if ctx.Err() != nil {
			return m.fail("voxl installation was canceled.")
		}
		return m.fail("Could not read the installed voxl version. Use --binary to import a working engine, then retry.")
	}
	latest, err := m.latestRelease(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return m.fail("voxl installation was canceled.")
		}
		return m.fail("Could not check the latest voxl release. Check access to crates.io, then retry.")
	}
	if semver.Compare("v"+latest, current) > 0 {
		return m.buildRelease(ctx, latest)
	}
	// No engine upgrade (or downgrade) is needed. Repackage it with the new
	// helper, even when the old Cargo installation has no separate bin/voxl.
	return m.installBinary(ctx, macapp.Executable(m.root))
}

func engineVersion(ctx context.Context, binary string) (string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, binary, "--version")
	configureCommand(cmd)
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	for _, field := range strings.Fields(string(output)) {
		if version := semver.Canonical("v" + strings.TrimPrefix(field, "v")); version != "" {
			return version, nil
		}
	}
	return "", errors.New("voxl returned no semantic version")
}

const maxReleaseMetadata = 2 * 1024 * 1024

func latestVoxlRelease(ctx context.Context) (string, error) {
	// The default transport follows Spool's configured outbound proxy policy.
	client := &http.Client{Timeout: 30 * time.Second}
	return fetchLatestVoxlRelease(ctx, client, "https://crates.io/api/v1/crates/voxl-server")
}

func fetchLatestVoxlRelease(ctx context.Context, client *http.Client, address string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Spool Voxl helper updater")
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("crates.io returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxReleaseMetadata+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxReleaseMetadata {
		return "", errors.New("crates.io release metadata exceeds size limit")
	}
	var metadata struct {
		Versions []struct {
			Number string `json:"num"`
			Yanked bool   `json:"yanked"`
		} `json:"versions"`
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return "", err
	}
	latest := ""
	for _, release := range metadata.Versions {
		version := "v" + release.Number
		if release.Yanked || !semver.IsValid(version) || semver.Prerelease(version) != "" {
			continue
		}
		if semver.Compare(version, latest) > 0 {
			latest = version
		}
	}
	if latest == "" {
		return "", errors.New("crates.io returned no stable voxl release")
	}
	return strings.TrimPrefix(latest, "v"), nil
}
