package voxl

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"spool/internal/testutil/voxlfixture"
)

func TestMain(m *testing.M) {
	if voxlfixture.Run() {
		return
	}
	os.Exit(m.Run())
}

func TestManagerOwnsAuthenticatedLoopbackHelperAndReapsIt(t *testing.T) {
	root := filepath.Join(t.TempDir(), "voxl")
	m := New(root, nil)
	if m.Status().Installed || m.Status().Running {
		t.Fatal("missing helper reported ready")
	}
	m.SetEnabled(t.Context(), true)
	if _, _, _, _, err := m.Acquire(t.Context(), "/ws"); err == nil {
		t.Fatal("missing helper started")
	}
	voxlfixture.Install(t, root)
	if !m.Available() || m.Status().Running {
		t.Fatal("enabling must allow connections without starting capture")
	}
	_, _, _, release, err := m.Acquire(t.Context(), "/ws")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Stop)
	address, secret, done := m.Endpoint()
	if secret == "" || address == "" || !m.Status().Running {
		t.Fatal("helper did not become ready")
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: time.Second}
	defer client.CloseIdleConnections()
	response, err := client.Get(address + "/stats")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatal("helper allowed unauthenticated loopback requests")
	}
	request, _ := http.NewRequestWithContext(t.Context(), "GET", address+"/stats", nil)
	request.Header.Set("Authorization", "Bearer "+secret)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		FilesRoot string `json:"files_root"`
		Transfers string `json:"transfers"`
	}
	err = json.NewDecoder(response.Body).Decode(&config)
	response.Body.Close()
	if err != nil || config.FilesRoot != filepath.Join(root, "shared") || config.Transfers != "upload,download" {
		t.Fatal("file transfers must use this Spool instance's shared folder", config, err)
	}
	if err := m.Install(context.Background()); err == nil {
		t.Fatal("installation allowed over a running helper")
	}
	secondAddress, secondSecret, _, releaseSecond, err := m.Acquire(t.Context(), "/ws")
	if err != nil || secondAddress != address || secondSecret != secret {
		t.Fatal("viewers did not share a helper", err)
	}
	_, _, _, releaseTransfer, err := m.Acquire(t.Context(), "/files/download")
	if err != nil {
		t.Fatal(err)
	}
	release()
	release() // Duplicate cleanup must not decrement another viewer's lease.
	if !m.Status().Running {
		t.Fatal("one viewer disconnected another viewer")
	}
	releaseSecond()
	if !m.Status().Running {
		t.Fatal("last viewer interrupted an active file transfer")
	}
	releaseTransfer()
	select {
	case <-done:
	default:
		t.Fatal("last viewer returned before the process exited")
	}
	if address, _, _ := m.Endpoint(); address != "" || m.Status().Running {
		t.Fatal("stopped helper remained available")
	}
	if !m.Available() {
		t.Fatal("idle helper hid the connection option")
	}
	_, _, oldDone, oldRelease, err := m.Acquire(t.Context(), "/ws")
	if err != nil {
		t.Fatal("restart failed:", err)
	}
	m.SetEnabled(t.Context(), false)
	select {
	case <-oldDone:
	default:
		t.Fatal("local disable did not reap the helper")
	}
	if m.Available() {
		t.Fatal("disabled helper remained connectable")
	}
	if _, _, _, _, err := m.Acquire(t.Context(), "/ws"); err == nil {
		t.Fatal("connection bypassed local disable")
	}
	m.SetEnabled(t.Context(), true)
	_, _, _, newRelease, err := m.Acquire(t.Context(), "/ws")
	if err != nil {
		t.Fatal(err)
	}
	oldRelease()
	if !m.Status().Running {
		t.Fatal("old connection cleanup stopped the new helper")
	}
	newRelease()
}

func TestManagerPageWithoutViewerExpiresDespiteStatsRequests(t *testing.T) {
	m := New(filepath.Join(t.TempDir(), "voxl"), nil)
	m.connectTimeout = 200 * time.Millisecond
	voxlfixture.Install(t, m.root)
	m.SetEnabled(t.Context(), true)
	t.Cleanup(m.Stop)
	for _, resource := range []string{"/stats", "/worker.js"} {
		if _, _, _, _, err := m.Acquire(t.Context(), resource); err == nil || m.Status().Running {
			t.Fatal("background resource started capture", resource, err)
		}
	}
	_, _, done, release, err := m.Acquire(t.Context(), "/")
	if err != nil {
		t.Fatal(err)
	}
	release()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case <-done:
			if m.Status().Running {
				t.Fatal("expired page left capture running")
			}
			return
		case <-deadline.C:
			t.Fatal("stats polling extended the page's connection deadline")
		case <-tick.C:
			_, _, _, releaseStats, err := m.Acquire(t.Context(), "/stats")
			if err == nil {
				releaseStats()
			}
		}
	}
}

func TestManagerViewerOutlivesPageConnectionDeadline(t *testing.T) {
	m := New(filepath.Join(t.TempDir(), "voxl"), nil)
	m.connectTimeout = 100 * time.Millisecond
	voxlfixture.Install(t, m.root)
	m.SetEnabled(t.Context(), true)
	t.Cleanup(m.Stop)
	_, _, _, releasePage, err := m.Acquire(t.Context(), "/")
	if err != nil {
		t.Fatal(err)
	}
	releasePage()
	_, _, done, releaseViewer, err := m.Acquire(t.Context(), "/ws")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
		t.Fatal("page deadline stopped a connected viewer")
	case <-time.After(200 * time.Millisecond):
	}
	releaseViewer()
	if m.Status().Running {
		t.Fatal("last viewer left capture running")
	}
}

func TestManagerLocalDisableCancelsPendingStartup(t *testing.T) {
	m := New(filepath.Join(t.TempDir(), "voxl"), nil)
	voxlfixture.Install(t, m.root)
	t.Setenv("SPOOL_TEST_VOXL_NOT_READY", "1")
	m.SetEnabled(t.Context(), true)
	t.Cleanup(m.Stop)
	result := make(chan error, 1)
	go func() {
		_, _, _, release, err := m.Acquire(t.Context(), "/ws")
		if release != nil {
			release()
		}
		result <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		m.mu.Lock()
		started := m.cancel != nil
		m.mu.Unlock()
		if started {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("helper did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	m.SetEnabled(t.Context(), false)
	select {
	case err := <-result:
		if err == nil || m.Status().Running || m.Available() {
			t.Fatal("pending connection survived local disable", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("local disable did not cancel startup")
	}
}

func TestInstallExistingLocalBinaryChecksCopyWithoutStartingDesktop(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	voxlfixture.Install(t, source)
	m := New(filepath.Join(t.TempDir(), "voxl"), nil)
	if err := m.Install(t.Context(), filepath.Join(source, "bin", filepath.Base(m.executable()))); err != nil {
		t.Fatal(err)
	}
	if !m.Status().Installed || m.Status().Running {
		t.Fatal("local import must install without starting capture", m.Status())
	}
	entries, err := os.ReadDir(m.root)
	if err != nil || len(entries) != 1 || entries[0].Name() != "bin" {
		t.Fatal("installation left staging data", entries, err)
	}
}

func TestInstallExistingLocalBinaryAcceptsOtherVersions(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	voxlfixture.Install(t, source)
	t.Setenv("SPOOL_TEST_VOXL_VERSION", "voxl 0.4.0-dev")
	m := New(filepath.Join(t.TempDir(), "voxl"), nil)
	if err := m.Install(t.Context(), filepath.Join(source, "bin", filepath.Base(m.executable()))); err != nil {
		t.Fatal("another locally built voxl version was rejected:", err)
	}
	if status := m.Status(); !status.Installed || status.Running || status.Error != "" {
		t.Fatal("version-independent import did not finish cleanly", status)
	}
}

func TestInstallExistingLocalBinaryRejectsAnUnusableExecutable(t *testing.T) {
	source := filepath.Join(t.TempDir(), "unusable-voxl")
	if err := os.WriteFile(source, []byte("not an executable"), 0700); err != nil {
		t.Fatal(err)
	}
	m := New(filepath.Join(t.TempDir(), "voxl"), nil)
	err := m.Install(t.Context(), source)
	if err == nil || !strings.Contains(err.Error(), "Could not run the local voxl executable.") {
		t.Fatal("an unusable executable was accepted", err)
	}
	if status := m.Status(); status.Installed || status.Installing || status.Error != err.Error() {
		t.Fatal("unusable executable reported installed", status)
	}
	entries, err := os.ReadDir(m.root)
	if err != nil || len(entries) != 0 {
		t.Fatal("unusable executable left staging data", entries, err)
	}
}

func TestInstallExistingLocalBinaryPreservesExistingBinDirectory(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	voxlfixture.Install(t, source)
	m := New(filepath.Join(t.TempDir(), "voxl"), nil)
	bin := filepath.Dir(m.executable())
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	retained := filepath.Join(bin, filepath.Base(m.executable())+"--")
	if err := os.WriteFile(retained, []byte("existing renamed executable"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := m.Install(t.Context(), filepath.Join(source, "bin", filepath.Base(m.executable()))); err != nil {
		t.Fatal("installation into a nonempty bin directory failed:", err)
	}
	if status := m.Status(); !status.Installed || status.Running || status.Installing || status.Error != "" {
		t.Fatal("installation did not finish cleanly", status)
	}
	if content, err := os.ReadFile(retained); err != nil || string(content) != "existing renamed executable" {
		t.Fatal("installation changed another file", string(content), err)
	}
	entries, err := os.ReadDir(m.root)
	if err != nil || len(entries) != 1 || entries[0].Name() != "bin" {
		t.Fatal("installation left staging data", entries, err)
	}
}

func TestInstallExistingLocalBinaryReportsFinalPathFailure(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	voxlfixture.Install(t, source)
	m := New(filepath.Join(t.TempDir(), "voxl"), nil)
	// A directory at the executable path is a real destination conflict. Keep
	// its contents intact and report the underlying filesystem failure.
	if err := os.MkdirAll(m.executable(), 0700); err != nil {
		t.Fatal(err)
	}
	retained := filepath.Join(m.executable(), "keep.txt")
	if err := os.WriteFile(retained, []byte("keep me"), 0600); err != nil {
		t.Fatal(err)
	}
	err := m.Install(t.Context(), filepath.Join(source, "bin", filepath.Base(m.executable())))
	var fileErr *os.LinkError
	if !errors.As(err, &fileErr) || !strings.Contains(err.Error(), m.executable()) {
		t.Fatal("installation hid its filesystem error", err)
	}
	if content, err := os.ReadFile(retained); err != nil || string(content) != "keep me" {
		t.Fatal("failed install removed the destination", string(content), err)
	}
	if status := m.Status(); status.Installed || status.Installing || status.Error != err.Error() {
		t.Fatal("failed installation status missing details", status)
	}
	entries, err := os.ReadDir(m.root)
	if err != nil || len(entries) != 1 || entries[0].Name() != "bin" {
		t.Fatal("failed installation left staging data", entries, err)
	}
}
