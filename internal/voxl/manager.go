// Package voxl owns the locally installed remote-desktop helper. It deliberately
// exposes no HTTP installation or configuration endpoints.
package voxl

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"spool/internal/voxl/macapp"
)

const Version = "0.3.3"

type Status struct {
	Installed       bool   `json:"installed"`
	UpgradeRequired bool   `json:"upgrade_required,omitempty"`
	Installing      bool   `json:"installing"`
	Running         bool   `json:"running"`
	Directory       string `json:"directory"`
	Error           string `json:"error,omitempty"`
	InstallStep     string `json:"install_step,omitempty"`
	Application     string `json:"application,omitempty"`
}

type Manager struct {
	root           string
	changed        func()
	op             sync.Mutex
	install        sync.Mutex
	mu             sync.Mutex
	status         Status
	cancel         context.CancelFunc
	done           chan struct{}
	url            string
	secret         string
	allowed        bool
	parent         context.Context
	active         *connections // guarded by op; releases belong to one helper process
	connectTimeout time.Duration
}

type connections struct {
	viewers   int
	transfers int
	idle      *time.Timer
}

func New(root string, changed func()) *Manager {
	return &Manager{root: root, changed: changed, connectTimeout: 10 * time.Second}
}

func (m *Manager) executable() string {
	if runtime.GOOS == "darwin" {
		return macapp.Executable(m.root)
	}
	name := "voxl"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(m.root, "bin", name)
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	v := m.status
	v.Directory = m.root
	info, err := os.Stat(m.executable())
	v.Installed = err == nil && info.Mode().IsRegular()
	if runtime.GOOS == "darwin" {
		v.Application = macapp.Bundle(m.root)
		v.Installed = macapp.Installed(m.root)
		v.UpgradeRequired = !v.Installed && macapp.NeedsUpgrade(m.root)
	}
	return v
}

func (m *Manager) notify() {
	if m.changed != nil {
		m.changed()
	}
}

// Endpoint is available only after the authenticated readiness probe succeeds.
// The password stays in the host process and is injected by the Spool proxy.
func (m *Manager) Endpoint() (address, password string, done <-chan struct{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.status.Running {
		return "", "", nil
	}
	return m.url, m.secret, m.done
}

// SetEnabled only opens/closes the local capability gate. Enabling never starts
// capture. The service calls it with false before stopping its HTTP listener.
func (m *Manager) SetEnabled(parent context.Context, enabled bool) {
	if !enabled {
		// Cancel startup without waiting for its readiness probe to release op.
		m.mu.Lock()
		m.allowed = false
		cancel := m.cancel
		m.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	}
	m.op.Lock()
	defer m.op.Unlock()
	m.mu.Lock()
	m.allowed, m.parent = enabled, parent
	m.mu.Unlock()
	if !enabled {
		m.stopLocked()
	} else {
		m.notify()
	}
}

func (m *Manager) Available() bool {
	m.mu.Lock()
	allowed := m.allowed && m.parent != nil && m.parent.Err() == nil
	m.mu.Unlock()
	return allowed && m.Status().Installed
}

// Acquire is called only for authorized viewer resources. A page or WebSocket
// request starts the helper. Assets/stats never restart it or extend the initial
// connection deadline. The last viewer/transfer closing stops capture immediately.
func (m *Manager) Acquire(ctx context.Context, resource string) (address, password string, stopped <-chan struct{}, release func(), err error) {
	m.op.Lock()
	defer m.op.Unlock()
	if err := ctx.Err(); err != nil {
		return "", "", nil, nil, err
	}
	if !m.Available() {
		return "", "", nil, nil, errors.New("Remote desktop is unavailable or not authorized.")
	}
	viewer := resource == "/ws"
	transfer := resource == "/files/upload" || resource == "/files/download"
	if !m.Status().Running {
		if !viewer && resource != "/" && resource != "/index.html" {
			return "", "", nil, nil, errors.New("Remote desktop is unavailable or not authorized.")
		}
		if err := m.startLocked(ctx, m.parent); err != nil {
			return "", "", nil, nil, err
		}
		m.active = &connections{}
		if !viewer {
			current := m.active
			current.idle = time.AfterFunc(m.connectTimeout, func() {
				m.op.Lock()
				defer m.op.Unlock()
				if m.active == current && current.viewers == 0 && current.transfers == 0 {
					m.stopLocked()
				}
			})
		}
	}
	current := m.active
	if transfer {
		current.transfers++
	}
	if viewer {
		current.viewers++
		if current.idle != nil {
			current.idle.Stop()
			current.idle = nil
		}
	}
	address, password, stopped = m.Endpoint()
	var once sync.Once
	release = func() {
		once.Do(func() {
			m.op.Lock()
			defer m.op.Unlock()
			if (!viewer && !transfer) || m.active != current {
				return
			}
			if viewer {
				current.viewers--
			} else {
				current.transfers--
			}
			if current.viewers == 0 && current.transfers == 0 {
				m.stopLocked()
			}
		})
	}
	return address, password, stopped, release, nil
}

func (m *Manager) startLocked(request, parent context.Context) error {
	if m.Status().Running {
		return nil
	}
	if !m.Status().Installed {
		return errors.New("Install voxl locally before enabling remote desktop.")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		return err
	}
	password := base64.RawURLEncoding.EncodeToString(secret)
	shared := filepath.Join(m.root, "shared")
	if err = os.MkdirAll(shared, 0700); err != nil {
		return m.fail("Could not prepare the remote desktop shared folder.")
	}
	ctx, cancel := context.WithCancel(parent)
	stopRequestCancel := context.AfterFunc(request, cancel)
	defer stopRequestCancel()
	cmd, cleanup, err := m.desktopCommand(ctx, "--addr=127.0.0.1", fmt.Sprintf("--port=%d", port),
		"--file-transfers=upload,download", "--files-root="+shared, "--upload-dir=",
		"--enable-clipboard=false", "--microphone-enabled=false", "--audio-enabled=false", "--enable-resize=false",
		// Spool validates the real browser origin before rewriting it to the
		// loopback origin. No wildcard host/origin exceptions are needed here.
		"--enable-basic-auth=true", "--title=Spool")
	if err != nil {
		cancel()
		return m.fail("Could not start voxl. Check its local installation and system permissions.")
	}
	cmd.Env = append(withoutEnv(os.Environ(), "VOXL_PASSWORD"), "VOXL_PASSWORD="+password)
	cmd.Dir = m.root
	// voxl can print its sign-in URL. Never copy helper output into host logs.
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err = cmd.Start(); err != nil {
		cancel()
		cleanup()
		return m.fail("Could not start voxl. Check its local installation and system permissions.")
	}
	done := make(chan struct{})
	address := fmt.Sprintf("http://127.0.0.1:%d", port)
	m.mu.Lock()
	m.cancel, m.done, m.url, m.secret = cancel, done, address, password
	m.status.Error = ""
	if !m.allowed {
		cancel()
	}
	m.mu.Unlock()
	go func() {
		err := cmd.Wait()
		cleanup()
		m.mu.Lock()
		m.status.Running = false
		m.url, m.secret = "", ""
		if ctx.Err() == nil {
			m.status.Error = "voxl stopped. Check screen recording and accessibility permissions on the host."
			if err == nil {
				m.status.Error = "voxl stopped."
			}
		}
		close(done)
		m.mu.Unlock()
		m.notify()
	}()
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: time.Second}
	defer client.CloseIdleConnections()
	deadline := time.NewTimer(12 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			cancel()
			<-done
			return ctx.Err()
		case <-done:
			cancel()
			return errors.New(m.Status().Error)
		case <-deadline.C:
			cancel()
			<-done
			return m.fail("voxl did not become ready. Check screen recording and accessibility permissions on the host.")
		case <-tick.C:
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, address+"/stats", nil)
			req.Header.Set("Authorization", "Bearer "+password)
			response, err := client.Do(req)
			if err != nil {
				continue
			}
			_ = response.Body.Close()
			if response.StatusCode != http.StatusOK {
				continue
			}
			m.mu.Lock()
			if ctx.Err() != nil {
				m.mu.Unlock()
				cancel()
				<-done
				return ctx.Err()
			}
			select {
			case <-done:
				m.mu.Unlock()
				cancel()
				return errors.New("voxl stopped before it became ready.")
			default:
			}
			m.status.Running = true
			m.mu.Unlock()
			m.notify()
			return nil
		}
	}
}

func (m *Manager) desktopCommand(ctx context.Context, args ...string) (*exec.Cmd, func(), error) {
	if runtime.GOOS == "darwin" {
		return macapp.Command(ctx, m.root, args...)
	}
	cmd := exec.CommandContext(ctx, m.executable(), args...)
	configureCommand(cmd)
	return cmd, func() {}, nil
}

func (m *Manager) Stop() {
	m.op.Lock()
	defer m.op.Unlock()
	m.stopLocked()
}

func (m *Manager) stopLocked() {
	if m.active != nil && m.active.idle != nil {
		m.active.idle.Stop()
	}
	m.active = nil
	m.mu.Lock()
	cancel, done := m.cancel, m.done
	m.cancel, m.done, m.url, m.secret = nil, nil, "", ""
	m.status.Running = false
	m.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	m.notify()
}

func (m *Manager) fail(message string) error {
	m.mu.Lock()
	m.status.Error = message
	m.mu.Unlock()
	m.notify()
	return errors.New(message)
}

// Install imports an existing binary, or downloads a pinned crates.io release
// and builds it locally. macOS fills missing dependencies with Homebrew, Linux
// checks system libraries, and Windows prepares libraries with vcpkg. Remote
// clients cannot invoke installation.
func (m *Manager) Install(ctx context.Context, source ...string) (installErr error) {
	if !m.install.TryLock() {
		return errors.New("voxl is busy. Try again shortly.")
	}
	defer m.install.Unlock()
	m.op.Lock()
	if m.Status().Running {
		m.op.Unlock()
		return errors.New("Disable remote desktop before installing voxl.")
	}
	if m.Status().Installed {
		m.op.Unlock()
		return nil
	}
	m.mu.Lock()
	m.status.Installing, m.status.Error = true, ""
	m.mu.Unlock()
	m.op.Unlock()
	m.notify()
	defer func() {
		m.mu.Lock()
		m.status.Installing = false
		m.status.InstallStep = ""
		if installErr != nil {
			m.status.Error = installErr.Error()
		}
		m.mu.Unlock()
		m.notify()
	}()
	if len(source) > 0 && source[0] != "" {
		return m.installBinary(ctx, source[0])
	}
	home, _ := os.UserHomeDir()
	name := "voxl"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	candidates := []string{filepath.Join(home, ".cargo", "bin", name)}
	if runtime.GOOS == "darwin" {
		// Complete an old installation locally without rebuilding Rust or
		// launching capture. Remote connection requests never install helpers.
		candidates = append([]string{filepath.Join(m.root, "bin", "voxl")}, candidates...)
		if macapp.NeedsUpgrade(m.root) {
			candidates = append([]string{macapp.Executable(m.root)}, candidates...)
		}
	}
	if path, err := exec.LookPath(name); err == nil {
		candidates = append(candidates, path)
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return m.installBinary(ctx, candidate)
		}
	}
	if err := os.MkdirAll(m.root, 0700); err != nil {
		return err
	}
	// Staging stays on the installation filesystem, allowing an atomic rename.
	stage, err := os.MkdirTemp(m.root, ".install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	for _, name := range []string{"tmp", "target"} {
		if err = os.MkdirAll(filepath.Join(stage, name), 0700); err != nil {
			return err
		}
	}
	// Keep compiler output local and disposable; cargo errors can include local
	// environment paths. The UI receives an actionable dependency message.
	log, err := os.OpenFile(filepath.Join(stage, "build.log"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	env := withoutEnv(os.Environ(), "TMPDIR", "TMP", "TEMP")
	env = append(env, "TMPDIR="+filepath.Join(stage, "tmp"), "TMP="+filepath.Join(stage, "tmp"), "TEMP="+filepath.Join(stage, "tmp"))
	tools := newBuildEnvironment(env, log, m.installStep)
	cargo, err := tools.prepare(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return m.fail("voxl installation was canceled.")
		}
		return err
	}
	m.installStep("Building voxl…")
	cmd := exec.CommandContext(ctx, cargo, "install", "voxl-server", "--version", "="+Version, "--locked", "--registry", "crates-io", "--root", stage, "--target-dir", filepath.Join(stage, "target"))
	cmd.Env = tools.env
	configureCommand(cmd)
	cmd.Stdout, cmd.Stderr = log, log
	err = cmd.Run()
	if err != nil {
		if ctx.Err() != nil {
			return m.fail("voxl installation was canceled.")
		}
		return m.fail("Could not build voxl. Check Rust, the native libraries, esbuild and access to crates.io, then retry.")
	}
	return m.finishInstall(ctx, filepath.Join(stage, "bin"))
}

func (m *Manager) installStep(step string) {
	m.mu.Lock()
	m.status.InstallStep = step
	m.mu.Unlock()
	m.notify()
}

func (m *Manager) installBinary(ctx context.Context, source string) error {
	m.installStep("Checking local voxl…")
	if !filepath.IsAbs(source) {
		return m.fail("Choose an absolute path to the local voxl executable.")
	}
	if err := os.MkdirAll(m.root, 0700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(m.root, ".install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	dir := filepath.Join(stage, "bin")
	if err = os.Mkdir(dir, 0700); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return m.fail("Could not read the local voxl executable.")
	}
	defer in.Close()
	target := filepath.Join(dir, filepath.Base(m.executable()))
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if err = errors.Join(copyErr, closeErr); err != nil {
		return err
	}
	// Probe the installed copy, so executable format and library loading are
	// checked at the destination. --version never starts desktop capture.
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, target, "--version")
	configureCommand(cmd)
	if err = cmd.Run(); err != nil {
		return m.fail("Could not run the local voxl executable. Check its executable format and runtime libraries.")
	}
	return m.finishInstall(ctx, dir)
}

// Publish only the prepared executable. The bin directory can already contain
// renamed helpers or other files, which must survive an installation retry.
// Both binary imports and Cargo builds use this path.
func (m *Manager) finishInstall(ctx context.Context, dir string) error {
	if runtime.GOOS == "darwin" {
		m.installStep("Preparing Voxl Helper…")
		if err := macapp.Install(ctx, m.root, filepath.Join(dir, "voxl")); err != nil {
			if ctx.Err() != nil {
				return errors.New("voxl installation was canceled.")
			}
			return fmt.Errorf("Could not finish installing voxl.\n%w", err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(m.executable()), 0700); err != nil {
		return fmt.Errorf("Could not finish installing voxl. %w", err)
	}
	if err := os.Rename(filepath.Join(dir, filepath.Base(m.executable())), m.executable()); err != nil {
		return fmt.Errorf("Could not finish installing voxl. %w", err)
	}
	return nil
}

func withoutEnv(env []string, keys ...string) []string {
	out := make([]string, 0, len(env))
	for _, item := range env {
		key, _, _ := strings.Cut(item, "=")
		drop := false
		for _, excluded := range keys {
			drop = drop || strings.EqualFold(key, excluded)
		}
		if !drop {
			out = append(out, item)
		}
	}
	return out
}
