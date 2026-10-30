package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"spool/internal/tailnet"
)

type testTailnetConnection struct {
	listener net.Listener
	closing  func()
}

func (c *testTailnetConnection) Listener() net.Listener { return c.listener }
func (c *testTailnetConnection) Origin() string         { return "https://spool.example.ts.net" }
func (c *testTailnetConnection) Close() error {
	if c.closing != nil {
		c.closing()
	}
	return c.listener.Close()
}

func newTestTailnetConnection(t *testing.T) *testTailnetConnection {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return &testTailnetConnection{listener: listener}
}

func waitTailnet(t *testing.T, service *Service, match func(ServiceStatus) bool) ServiceStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if status := service.Status(); match(status) {
			return status
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for tailnet status", service.Status())
	return ServiceStatus{}
}

func enableTestTailnet(t *testing.T, service *Service, settings Settings, open tailnetOpener) Settings {
	t.Helper()
	if err := service.Stop(); err != nil {
		t.Fatal(err)
	}
	service.openTailnet = open
	settings.TSNetEnabled = true
	if err := service.Store.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	return settings
}

func TestTailnetEnrollmentDoesNotBlockLocalServiceAndStopCancelsIt(t *testing.T) {
	service, settings := settingsService(t)
	canceled := make(chan struct{})
	enableTestTailnet(t, service, settings, func(ctx context.Context, _ string, options tailnet.Options, report func(tailnet.Status)) (tailnetConnection, error) {
		if options.Hostname != "spool-server" || options.Port != 443 || options.AuthKey != "" {
			t.Error("unexpected enrollment options", options.Hostname, options.Port)
		}
		report(tailnet.Status{State: "NeedsLogin", AuthURL: "https://login.tailscale.com/a/test"})
		<-ctx.Done()
		close(canceled)
		return nil, ctx.Err()
	})
	status := waitTailnet(t, service, func(s ServiceStatus) bool { return s.TSNet.State == "NeedsLogin" })
	if !status.Running || status.RestartRequired || status.TSNet.AuthURL == "" {
		t.Fatal("pending enrollment stopped the service", status)
	}
	local, err := service.LocalWebURL()
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: time.Second}
	t.Cleanup(client.CloseIdleConnections)
	response, err := client.Get(local + "/healthz")
	if err != nil {
		t.Fatal("enrollment blocked local HTTP", err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.Status)
	}
	if err := service.Stop(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("stop did not cancel enrollment")
	}
	if status := service.Status(); status.Running || status.TSNet.State != "Stopped" || status.TSNet.AuthURL != "" {
		t.Fatal("stopped service retained a login URL", status)
	}
}

func TestTailnetAuthKeyIsMemoryOnlyAndConsumedAfterEnrollment(t *testing.T) {
	service, settings := settingsService(t)
	const key = "tskey-auth-test-enrollment-secret"
	if err := service.SetTSNetAuthKey("  " + key + "\n"); err != nil {
		t.Fatal(err)
	}
	connection := newTestTailnetConnection(t)
	allowEnrollment := make(chan struct{})
	defer close(allowEnrollment)
	passed := make(chan string, 1)
	settings = enableTestTailnet(t, service, settings, func(ctx context.Context, _ string, options tailnet.Options, report func(tailnet.Status)) (tailnetConnection, error) {
		passed <- options.AuthKey
		report(tailnet.Status{State: "NeedsMachineAuth"})
		select {
		case <-allowEnrollment:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return connection, nil
	})
	status := waitTailnet(t, service, func(s ServiceStatus) bool { return s.TSNet.State == "NeedsMachineAuth" })
	if !status.TSNetAuthKeySet || status.RestartRequired {
		t.Fatal("staged key was not applied to this runtime", status)
	}
	if got := <-passed; got != key {
		t.Fatal("enrollment did not receive the staged key")
	}
	body, err := json.Marshal(status)
	if err != nil || strings.Contains(string(body), key) {
		t.Fatal("status disclosed the enrollment key", err)
	}
	var stored string
	if err := service.Store.db.QueryRow(`SELECT value FROM settings WHERE key='server'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, key) || strings.Contains(stored, "auth_key") {
		t.Fatal("settings persisted the enrollment key")
	}
	// An unrelated autosave may finish while enrollment is still pending.
	settings.IdleMinutes++
	if err := service.Store.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	allowEnrollment <- struct{}{}
	status = waitTailnet(t, service, func(s ServiceStatus) bool { return s.TSNet.URL != "" })
	if status.TSNetAuthKeySet || !status.RestartRequired {
		t.Fatal("enrollment did not consume the key or lost the pending idle change", status)
	}
	saved, err := service.Store.Settings()
	if err != nil || saved.PublicURL != connection.Origin() || saved.IdleMinutes != settings.IdleMinutes || service.WebURL() != connection.Origin() {
		t.Fatal("enrollment overwrote newer settings or failed to publish its origin", saved, err)
	}
	// A stale autosave must not restore the empty pre-enrollment origin.
	settings.HistoryCacheMiB++
	if err := service.Store.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	saved, _ = service.Store.Settings()
	if saved.PublicURL != connection.Origin() {
		t.Fatal("stale autosave overwrote the managed origin")
	}
	saved.TSNetEnabled = false
	if err := service.Store.SaveSettings(saved); err != nil {
		t.Fatal(err)
	}
	saved, _ = service.Store.Settings()
	if saved.PublicURL != "" {
		t.Fatal("disabling tsnet retained its managed public origin")
	}
}

func TestTailnetRestartClosesOldNodeBeforeOpeningNewNode(t *testing.T) {
	service, settings := settingsService(t)
	first, second := newTestTailnetConnection(t), newTestTailnetConnection(t)
	closing, allowClose := make(chan struct{}), make(chan struct{})
	// Registered after service cleanup, so a failed assertion cannot deadlock Close.
	t.Cleanup(func() { close(allowClose) })
	first.closing = func() { close(closing); <-allowClose }
	var opens atomic.Int32
	enableTestTailnet(t, service, settings, func(context.Context, string, tailnet.Options, func(tailnet.Status)) (tailnetConnection, error) {
		if opens.Add(1) == 1 {
			return first, nil
		}
		return second, nil
	})
	status := waitTailnet(t, service, func(s ServiceStatus) bool { return s.TSNet.URL != "" })
	if status.RestartRequired {
		t.Fatal("automatically assigned origin caused a false restart notice", status)
	}
	app := service.app
	if err := service.SetTSNetAuthKey("tskey-auth-replacement"); err != nil {
		t.Fatal(err)
	}
	if !service.Status().RestartRequired || service.app != app || opens.Load() != 1 {
		t.Fatal("staging a key interrupted the active service or failed to mark restart")
	}
	if err := service.Restart(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-closing:
	case <-time.After(3 * time.Second):
		t.Fatal("restart did not close the old node")
	}
	if opens.Load() != 1 {
		t.Fatal("two nodes opened the same state directory concurrently")
	}
	allowClose <- struct{}{}
	status = waitTailnet(t, service, func(s ServiceStatus) bool { return s.TSNet.URL != "" })
	if status.RestartRequired || status.TSNetAuthKeySet || opens.Load() != 2 {
		t.Fatal("replacement node did not apply the staged key", status)
	}
}

func TestTailnetAuthKeyFailureIsRedactedAndCanBeCleared(t *testing.T) {
	service, settings := settingsService(t)
	const key = "tskey-auth-invalid-test-secret"
	const envKey = "tskey-auth-environment-test-secret"
	t.Setenv("TS_AUTHKEY", envKey)
	if err := service.SetTSNetAuthKey(key); err != nil {
		t.Fatal(err)
	}
	enableTestTailnet(t, service, settings, func(context.Context, string, tailnet.Options, func(tailnet.Status)) (tailnetConnection, error) {
		return nil, fmt.Errorf("rejected %s and %s", key, envKey)
	})
	status := waitTailnet(t, service, func(s ServiceStatus) bool { return s.TSNet.State == "Error" })
	if !status.Running || !status.TSNetAuthKeySet || strings.Contains(status.TSNet.Error, key) || strings.Contains(status.TSNet.Error, envKey) || !strings.Contains(status.TSNet.Error, "[redacted]") {
		t.Fatal("failed enrollment leaked credentials or stopped local service")
	}
	if err := service.SetTSNetAuthKey(""); err != nil {
		t.Fatal(err)
	}
	if status := service.Status(); status.TSNetAuthKeySet || !status.RestartRequired {
		t.Fatal("clearing a failed key was not staged for explicit restart", status)
	}
}

func TestTailnetAuthKeyValidationDoesNotEchoInput(t *testing.T) {
	service, _ := settingsService(t)
	for _, key := range []string{"tskey-auth-", "tskey-api-secret", "tskey-auth-line\nbreak", "tskey-auth-" + strings.Repeat("s", 512)} {
		err := service.SetTSNetAuthKey(key)
		if err == nil || strings.Contains(err.Error(), key) || service.Status().TSNetAuthKeySet {
			t.Fatal("invalid key was accepted or disclosed")
		}
	}
}

func TestTailnetLegacySettingsDefaultToDisabled(t *testing.T) {
	service, _ := settingsService(t)
	_, err := service.Store.db.Exec(`UPDATE settings SET value='{"host":"127.0.0.1","port":8787,"idle_minutes":15,"history_cache_mib":64,"auto_start":true}' WHERE key='server'`)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := service.Store.Settings()
	if err != nil || settings.TSNetEnabled || settings.TSNetHostname != "spool-server" || settings.TSNetPort != 443 {
		t.Fatal("legacy settings did not inherit safe defaults", settings, err)
	}
}

func TestTailnetLoginRequestCanGenerateMissingLinkAndIsCanceledOnStop(t *testing.T) {
	service, settings := settingsService(t)
	enableTestTailnet(t, service, settings, func(ctx context.Context, _ string, _ tailnet.Options, report func(tailnet.Status)) (tailnetConnection, error) {
		report(tailnet.Status{State: "NeedsLogin"})
		<-ctx.Done()
		return nil, ctx.Err()
	})
	waitTailnet(t, service, func(s ServiceStatus) bool { return s.TSNet.State == "NeedsLogin" })
	service.mu.Lock()
	runtime := service.tailnet
	service.mu.Unlock()
	const address = "https://login.tailscale.com/a/test"
	runtime.setLogin(func(context.Context) (string, error) { return address, nil })
	if got, err := service.TSNetLoginURL(context.Background()); err != nil || got != address {
		t.Fatal("could not request a link while enrollment was pending", err)
	}
	started := make(chan struct{})
	runtime.setLogin(func(ctx context.Context) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	})
	done := make(chan error, 1)
	go func() { _, err := service.TSNetLoginURL(context.Background()); done <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("login request was not started")
	}
	saved := make(chan error, 1)
	go func() {
		next, err := service.Store.Settings()
		if err == nil {
			next.HistoryCacheMiB++
			err = service.SaveSettings(next)
		}
		saved <- err
	}()
	select {
	case err := <-saved:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("pending login request blocked unrelated settings saves")
	}
	if err := service.Stop(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "stopped") {
			t.Fatal("stopped node's login request was not canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("login request blocked service shutdown")
	}
}

func TestTailnetLoginRequestValidatesLinksAndReportsTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime := &tailnetRuntime{ctx: ctx, cancel: cancel, status: tailnet.Status{State: "NeedsLogin"}}
	service := &Service{tailnet: runtime}
	const address = "https://login.tailscale.com/a/test"
	runtime.report(tailnet.Status{State: "NeedsLogin", AuthURL: address})
	if got, err := service.TSNetLoginURL(ctx); err != nil || got != address {
		t.Fatal("existing link was not returned", err)
	}
	runtime.report(tailnet.Status{State: "NeedsLogin"})
	runtime.setLogin(func(context.Context) (string, error) { return "https://evil.example/a/test", nil })
	if got, err := service.TSNetLoginURL(ctx); got != "" || err == nil || !strings.Contains(err.Error(), "invalid login link") {
		t.Fatal("untrusted login URL accepted", err)
	}
	runtime.setLogin(func(ctx context.Context) (string, error) { <-ctx.Done(); return "", ctx.Err() })
	deadline, stop := context.WithTimeout(ctx, 30*time.Millisecond)
	defer stop()
	if got, err := service.TSNetLoginURL(deadline); got != "" || err == nil || !strings.Contains(err.Error(), "Check network access") {
		t.Fatal("missing link did not produce an actionable timeout", err)
	}
	if _, err := (&Service{}).TSNetLoginURL(ctx); err == nil {
		t.Fatal("stopped service accepted a login request")
	}
}

func TestTailnetAuthKeyRegistersImmediatelyWithoutRestartingLocalService(t *testing.T) {
	service, settings := settingsService(t)
	settings.TSNetEnabled = true
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	app, runner := service.app, service.runner
	const key = "tskey-auth-registration-secret"
	started, finish := make(chan struct{}), make(chan struct{})
	service.enrollTailnet = func(ctx context.Context, root string, options tailnet.Options, report func(tailnet.Status)) error {
		if root != service.Store.Root() || options.AuthKey != key {
			t.Error("wrong enrollment state or key")
		}
		close(started)
		select {
		case <-finish:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	done := make(chan error, 1)
	go func() { done <- service.RegisterTSNetAuthKey(context.Background(), "  "+key+"\n") }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("button did not start enrollment immediately")
	}
	if status := service.Status(); !status.Running || status.TSNet.State != "Registering" || status.TSNetAuthKeySet {
		t.Fatal("registration staged credentials or stopped the local service", status)
	}
	settings.HistoryCacheMiB++
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterTSNetAuthKey(context.Background(), key); err == nil || !strings.Contains(err.Error(), "already in progress") {
		t.Fatal("duplicate enrollment was accepted", err)
	}
	close(finish)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("enrollment did not finish")
	}
	status := service.Status()
	if status.TSNet.State != "Registered" || !status.RestartRequired || !status.Running || status.TSNet.URL != "" || service.app != app || service.runner != runner {
		t.Fatal("registration activated an ingress or restarted active tasks", status)
	}
	stored, err := service.Store.Settings()
	if err != nil || stored.PublicURL != "" || stored.HistoryCacheMiB != settings.HistoryCacheMiB {
		t.Fatal("registration overwrote settings", err)
	}
	encoded, _ := json.Marshal([]any{stored, status})
	if strings.Contains(string(encoded), key) || status.TSNetAuthKeySet {
		t.Fatal("registration retained the Auth Key")
	}
	connection := newTestTailnetConnection(t)
	service.openTailnet = func(ctx context.Context, _ string, options tailnet.Options, report func(tailnet.Status)) (tailnetConnection, error) {
		if options.AuthKey != "" {
			t.Error("activation reused the consumed key instead of the saved identity")
		}
		return connection, nil
	}
	if err := service.Restart(); err != nil {
		t.Fatal(err)
	}
	status = waitTailnet(t, service, func(status ServiceStatus) bool { return status.TSNet.URL != "" })
	if status.RestartRequired || status.TSNet.State != "Running" {
		t.Fatal("explicit activation did not clear the restart notice", status)
	}
}

func TestTailnetImmediateRegistrationFailureIsRedactedAndCanRetry(t *testing.T) {
	service, settings := settingsService(t)
	settings.TSNetEnabled = true
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	app := service.app
	const key = "tskey-auth-rejected-secret"
	service.enrollTailnet = func(context.Context, string, tailnet.Options, func(tailnet.Status)) error {
		return fmt.Errorf("rejected %s", key)
	}
	err := service.RegisterTSNetAuthKey(context.Background(), key)
	if err == nil || strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), "[redacted]") {
		t.Fatal("failed registration disclosed credentials", err)
	}
	if status := service.Status(); !status.Running || status.TSNetAuthKeySet || status.TSNet.State != "Error" || strings.Contains(status.TSNet.Error, key) || service.app != app {
		t.Fatal("failed registration interrupted local tasks or retained key", status)
	}
	service.enrollTailnet = func(context.Context, string, tailnet.Options, func(tailnet.Status)) error { return nil }
	if err := service.RegisterTSNetAuthKey(context.Background(), key); err != nil {
		t.Fatal("failed registration could not retry", err)
	}
}

func TestTailnetImmediateRegistrationCancelsWhenDisabled(t *testing.T) {
	service, settings := settingsService(t)
	settings.TSNetEnabled = true
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	service.enrollTailnet = func(ctx context.Context, _ string, _ tailnet.Options, _ func(tailnet.Status)) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	done := make(chan error, 1)
	go func() { done <- service.RegisterTSNetAuthKey(context.Background(), "tskey-auth-cancel-test") }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("registration did not start")
	}
	settings.TSNetEnabled = false
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "canceled") {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("disabling Tailscale did not cancel enrollment")
	}
	if status := service.Status(); !status.Running || status.TSNet.State != "Disabled" {
		t.Fatal("canceled enrollment changed active local service", status)
	}
}
