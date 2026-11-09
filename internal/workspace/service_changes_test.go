package workspace

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/coder/websocket"
	"spool/internal/tailnet"
	"spool/internal/testutil/voxlfixture"
)

func availableServicePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}

func requireServiceHealth(t *testing.T, address string) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	response, err := client.Get(address + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.Status)
	}
}

func TestApplyWebChangesKeepsDesktopTailnetAndActiveWork(t *testing.T) {
	service, settings := settingsService(t)
	connection := newTestTailnetConnection(t)
	settings = enableTestTailnet(t, service, settings, func(context.Context, string, tailnet.Options, func(tailnet.Status)) (tailnetConnection, error) {
		return connection, nil
	})
	waitTailnet(t, service, func(status ServiceStatus) bool { return status.TSNet.URL != "" })
	app, runner, node := service.app, service.runner, service.tailnet
	desktop, _ := service.LocalWebURL()
	oldAddress := "http://" + service.Status().Address
	// Hold real turn admission across the restart, just as an executing agent does.
	app.turnMu.Lock()
	work, control := app.registerTurn(context.Background(), "inflight", "turn")
	app.turnMu.Unlock()
	defer app.releaseTurn("inflight", control)
	user := testUser(t, service.Store, "entry-restart")
	token, identity, err := service.Store.login(user.Username, "test-password-2026")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cookie := (&http.Cookie{Name: authCookie, Value: token}).String()
	socket, _, err := websocket.Dial(ctx, oldAddress+"/api/events", &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {oldAddress}, "Cookie": {cookie}}})
	if err != nil {
		t.Fatal(err)
	}
	defer socket.CloseNow()
	realtimeWrite(t, socket, realtimeInput{Type: "hello", CSRF: identity.CSRF, Visible: true})
	realtimeReady(t, socket)

	settings.Port = availableServicePort(t)
	settings.PublicURL = "https://new-entry.example.test"
	settings.TSNetHostname = "pending-node"
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(service.Status().PendingChanges, []string{"web", "tsnet"}) {
		t.Fatal(service.Status())
	}
	if err := service.ApplyServiceChanges("web"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(service.Status().PendingChanges, []string{"tsnet"}) {
		t.Fatal("web applied other pending entries", service.Status())
	}
	if service.app != app || service.runner != runner || service.tailnet != node || work.Err() != nil {
		t.Fatal("web restart replaced core or Tailscale")
	}
	if address, _ := service.LocalWebURL(); address != desktop {
		t.Fatal("desktop endpoint changed")
	}
	if service.WebURL() != settings.PublicURL {
		t.Fatal("public origin did not apply")
	}
	requireServiceHealth(t, desktop)
	requireServiceHealth(t, "http://"+service.Status().Address)
	requireServiceHealth(t, "http://"+connection.Listener().Addr().String())
	if _, _, err := socket.Read(ctx); err == nil || ctx.Err() != nil {
		t.Fatal("old upgraded Web connection survived restart", err)
	}
}

func TestCancelServiceChangesRestoresOnlySelectedFields(t *testing.T) {
	service, original := settingsService(t)
	next := original
	next.Port = availableServicePort(t)
	next.TSNetEnabled, next.TSNetHostname = true, "pending-node"
	next.IdleMinutes++
	next.HistoryCacheMiB, next.AutoStart = original.HistoryCacheMiB+1, false
	if err := service.SaveSettings(next); err != nil {
		t.Fatal(err)
	}
	if err := service.CancelServiceChanges("web"); err != nil {
		t.Fatal(err)
	}
	saved, _ := service.Store.Settings()
	if saved.Port != original.Port || saved.TSNetHostname != next.TSNetHostname || saved.IdleMinutes != next.IdleMinutes || saved.HistoryCacheMiB != next.HistoryCacheMiB || saved.AutoStart {
		t.Fatal("cancel overwrote another entry", saved)
	}
	if !slices.Equal(service.Status().PendingChanges, []string{"tsnet", "runtime"}) {
		t.Fatal(service.Status())
	}
	if err := service.CancelServiceChanges("tsnet"); err != nil {
		t.Fatal(err)
	}
	if err := service.CancelServiceChanges("runtime"); err != nil {
		t.Fatal(err)
	}
	if service.Status().RestartRequired {
		t.Fatal("cancel left a restart notice", service.Status())
	}
	if err := service.CancelServiceChanges("unknown"); err == nil {
		t.Fatal("unknown cancel target accepted")
	}
}

func TestDisableWebEntryKeepsDesktopTailnetAndActiveWork(t *testing.T) {
	service, settings := settingsService(t)
	connection := newTestTailnetConnection(t)
	settings = enableTestTailnet(t, service, settings, func(context.Context, string, tailnet.Options, func(tailnet.Status)) (tailnetConnection, error) {
		return connection, nil
	})
	waitTailnet(t, service, func(status ServiceStatus) bool { return status.TSNet.URL != "" })
	app, runner, node := service.app, service.runner, service.tailnet
	desktop, _ := service.LocalWebURL()
	address := service.Status().Address
	app.turnMu.Lock()
	work, control := app.registerTurn(context.Background(), "web-disable", "turn")
	app.turnMu.Unlock()
	defer app.releaseTurn("web-disable", control)
	settings.WebEnabled = false
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(service.Status().PendingChanges, []string{"web"}) {
		t.Fatal("Web disable was not pending", service.Status())
	}
	requireServiceHealth(t, "http://"+address)
	if err := service.CancelServiceChanges("web"); err != nil {
		t.Fatal(err)
	}
	if saved, err := service.Store.Settings(); err != nil || !saved.WebEnabled || service.Status().RestartRequired {
		t.Fatal("cancel did not restore Web enablement", saved, err)
	}
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := service.ApplyServiceChanges("web"); err != nil {
		t.Fatal(err)
	}
	if status := service.Status(); !status.Running || status.Address != "" || status.RestartRequired || service.web != nil {
		t.Fatal("disabled Web entry is still active or pending", status)
	}
	if socket, err := net.DialTimeout("tcp", address, time.Second); err == nil {
		socket.Close()
		t.Fatal("disabled Web listener still accepts connections")
	}
	requireServiceHealth(t, desktop)
	requireServiceHealth(t, "http://"+connection.Listener().Addr().String())
	if service.app != app || service.runner != runner || service.tailnet != node || work.Err() != nil {
		t.Fatal("disabling Web interrupted another entry or active work")
	}
	settings.WebEnabled = true
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := service.ApplyServiceChanges("web"); err != nil {
		t.Fatal(err)
	}
	requireServiceHealth(t, "http://"+service.Status().Address)
	if service.Status().RestartRequired || service.app != app || service.tailnet != node || work.Err() != nil {
		t.Fatal("re-enabling Web affected other entries or work")
	}
}

func TestWebDisabledStartupAndFailedEnable(t *testing.T) {
	service, settings := settingsService(t)
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	settings.WebEnabled = false
	settings.Port = occupied.Addr().(*net.TCPAddr).Port
	settings.PublicURL = "https://disabled.example.test"
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := service.Restart(); err != nil {
		t.Fatal("disabled Web tried to bind an occupied port", err)
	}
	desktop, _ := service.LocalWebURL()
	app := service.app
	requireServiceHealth(t, desktop)
	if status := service.Status(); status.Address != "" || status.RestartRequired || service.WebURL() != desktop {
		t.Fatal("disabled startup exposed a stale Web address", status, service.WebURL())
	}
	settings.WebEnabled = true
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := service.ApplyServiceChanges("web"); err == nil {
		t.Fatal("Web accepted an occupied port")
	}
	if service.web != nil || service.app != app || !slices.Equal(service.Status().PendingChanges, []string{"web"}) || service.WebURL() != desktop {
		t.Fatal("failed Web enablement changed the disabled entry", service.Status())
	}
	requireServiceHealth(t, desktop)
	if err := service.CancelServiceChanges("web"); err != nil {
		t.Fatal(err)
	}
	if service.Status().RestartRequired || service.Status().WebError != "" {
		t.Fatal("cancel left a disabled Web error", service.Status())
	}
}

func TestFailedWebApplyRestoresOldListenerWithoutStoppingCore(t *testing.T) {
	service, settings := settingsService(t)
	app, runner := service.app, service.runner
	originalAddress := service.Status().Address
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	settings.Port = occupied.Addr().(*net.TCPAddr).Port
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := service.ApplyServiceChanges("web"); err == nil {
		t.Fatal("occupied port accepted")
	}
	status := service.Status()
	if !status.Running || status.WebError == "" || !slices.Equal(status.PendingChanges, []string{"web"}) || service.app != app || service.runner != runner || status.Address != originalAddress {
		t.Fatal("failed bind harmed core", status)
	}
	requireServiceHealth(t, "http://"+originalAddress)
	if err := service.CancelServiceChanges("web"); err != nil {
		t.Fatal(err)
	}
	if service.Status().WebError != "" || service.Status().RestartRequired {
		t.Fatal(service.Status())
	}
}

func TestApplyTailnetChangesKeepsWebAndCoreAndSerializesNodes(t *testing.T) {
	service, settings := settingsService(t)
	first, second := newTestTailnetConnection(t), newTestTailnetConnection(t)
	opens := 0
	settings = enableTestTailnet(t, service, settings, func(context.Context, string, tailnet.Options, func(tailnet.Status)) (tailnetConnection, error) {
		opens++
		if opens == 1 {
			return first, nil
		}
		return second, nil
	})
	waitTailnet(t, service, func(status ServiceStatus) bool { return status.TSNet.URL != "" })
	app, runner, web := service.app, service.runner, service.web
	desktop, _ := service.LocalWebURL()
	settings.TSNetHostname = "new-node"
	settings.Port = availableServicePort(t)
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := service.ApplyServiceChanges("tsnet"); err != nil {
		t.Fatal(err)
	}
	waitTailnet(t, service, func(status ServiceStatus) bool { return status.TSNet.URL != "" })
	if service.app != app || service.runner != runner || service.web != web {
		t.Fatal("Tailscale restart replaced another entry")
	}
	if !slices.Equal(service.Status().PendingChanges, []string{"web"}) {
		t.Fatal(service.Status())
	}
	requireServiceHealth(t, desktop)
	requireServiceHealth(t, "http://"+service.Status().Address)
	if conn, err := net.DialTimeout("tcp", first.Listener().Addr().String(), time.Second); err == nil {
		conn.Close()
		t.Fatal("old Tailscale listener remains open")
	}
	settings.TSNetEnabled = false
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := service.ApplyServiceChanges("tsnet"); err != nil {
		t.Fatal(err)
	}
	if service.Status().TSNet.State != "Disabled" || service.app != app {
		t.Fatal(service.Status())
	}
}

func TestApplyRelayChangesSharesCoreAndUpdatesPublicWebMount(t *testing.T) {
	t.Setenv("SPOOL_RELAY_REGISTRATION_TOKEN", "")
	service, settings := settingsService(t)
	broker, token := testRelayBroker(t)
	public := httptest.NewServer(broker)
	defer public.Close()
	defer service.Stop()
	settings.RelayURL, settings.RelayLANPort = public.URL, availableServicePort(t)
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterRelay(t.Context(), public.URL, token); err != nil {
		t.Fatal(err)
	}
	app, runner, web := service.app, service.runner, service.web
	settings.RelayEnabled, settings.RelayWebEnabled = true, true
	settings.Port = availableServicePort(t)
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := service.ApplyServiceChanges("relay"); err != nil {
		t.Fatal(err)
	}
	status := waitTailnet(t, service, func(status ServiceStatus) bool { return status.Relay.WebURL != "" })
	if service.app != app || service.runner != runner || service.web != web || !slices.Equal(status.PendingChanges, []string{"web"}) {
		t.Fatal("relay apply replaced core or applied Web settings", status)
	}
	client := &http.Client{Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()
	response, err := client.Get(status.Relay.WebURL + "api/me")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatal("public Web route did not apply", response.Status)
	}
	settings.RelayEnabled = false
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := service.CancelServiceChanges("relay"); err != nil {
		t.Fatal(err)
	}
	if service.relay == nil {
		t.Fatal("cancel stopped active relay")
	}
	saved, _ := service.Store.Settings()
	saved.RelayEnabled = false
	if err := service.SaveSettings(saved); err != nil {
		t.Fatal(err)
	}
	if err := service.ApplyServiceChanges("relay"); err != nil {
		t.Fatal(err)
	}
	if service.relay != nil || service.app != app || service.web != web {
		t.Fatal("stopping relay stopped another component")
	}
	requireServiceHealth(t, "http://"+service.Status().Address)
}

func TestServiceChangeActionsRespectUpdateAndStoppedState(t *testing.T) {
	service, _ := settingsService(t)
	release, err := service.AcquireUpdate()
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"web", "relay", "tsnet", "runtime"} {
		if err := service.ApplyServiceChanges(target); !errors.Is(err, ErrUpdateInProgress) {
			t.Fatal(target, err)
		}
		if err := service.CancelServiceChanges(target); !errors.Is(err, ErrUpdateInProgress) {
			t.Fatal(target, err)
		}
	}
	release()
	if err := service.ApplyServiceChanges("unknown"); err == nil {
		t.Fatal("unknown apply target accepted")
	}
	if err := service.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := service.ApplyServiceChanges("web"); err == nil {
		t.Fatal("stopped service accepted apply")
	}
	if err := service.CancelServiceChanges("web"); err == nil {
		t.Fatal("stopped service accepted cancel")
	}
}

func TestRuntimeApplyPreservesOtherPendingEntrySettings(t *testing.T) {
	service, original := settingsService(t)
	oldApp := service.app
	next := original
	next.WebEnabled = false
	next.Port = availableServicePort(t)
	next.PublicURL = "https://pending.example.test"
	next.IdleMinutes++
	next.ProxyEnabled = false
	if err := service.SaveSettings(next); err != nil {
		t.Fatal(err)
	}
	if err := service.ApplyServiceChanges("runtime"); err != nil {
		t.Fatal(err)
	}
	if service.app == oldApp || service.runner.IdleTimeout != time.Duration(next.IdleMinutes)*time.Minute {
		t.Fatal("runtime settings did not apply")
	}
	if !slices.Equal(service.Status().PendingChanges, []string{"web"}) || service.app.publicURL() != original.PublicURL || service.WebURL() == next.PublicURL {
		t.Fatal("core restart applied unrelated pending settings", service.Status())
	}
	if service.networkProxy.Enabled {
		t.Fatal("runtime apply reverted the immediately active proxy policy")
	}
	saved, _ := service.Store.Settings()
	if saved.Port != next.Port || saved.PublicURL != next.PublicURL {
		t.Fatal("runtime apply discarded pending Web settings", saved)
	}
}

func TestRuntimeApplyPreservesRemoteDesktopEnablement(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		name := "disabled"
		if enabled {
			name = "enabled"
		}
		t.Run(name, func(t *testing.T) {
			service, settings := settingsService(t)
			voxlfixture.Install(t, filepath.Join(service.Store.Root(), "voxl"))
			settings.RemoteDesktopEnabled = !enabled
			if err := service.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			if err := service.Restart(); err != nil {
				t.Fatal(err)
			}
			if err := service.SetRemoteDesktopEnabled(enabled); err != nil {
				t.Fatal(err)
			}
			if service.desktop.Available() != enabled {
				t.Fatal("remote desktop toggle did not apply immediately")
			}
			next, err := service.Store.Settings()
			if err != nil {
				t.Fatal(err)
			}
			next.IdleMinutes++
			if err := service.SaveSettings(next); err != nil {
				t.Fatal(err)
			}
			if err := service.ApplyServiceChanges("runtime"); err != nil {
				t.Fatal(err)
			}
			saved, err := service.Store.Settings()
			if err != nil {
				t.Fatal(err)
			}
			if service.desktop.Available() != enabled || saved.RemoteDesktopEnabled != enabled {
				t.Fatalf("runtime apply reverted remote desktop: available=%v saved=%v want=%v", service.desktop.Available(), saved.RemoteDesktopEnabled, enabled)
			}
		})
	}
}

func TestFailedRelayApplyRestoresPreviousConnection(t *testing.T) {
	t.Setenv("SPOOL_RELAY_REGISTRATION_TOKEN", "")
	service, settings := settingsService(t)
	broker, token := testRelayBroker(t)
	public := httptest.NewServer(broker)
	defer public.Close()
	defer service.Stop()
	settings.RelayURL, settings.RelayLANPort = public.URL, availableServicePort(t)
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterRelay(t.Context(), public.URL, token); err != nil {
		t.Fatal(err)
	}
	settings.RelayEnabled = true
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := service.ApplyServiceChanges("relay"); err != nil {
		t.Fatal(err)
	}
	waitTailnet(t, service, func(s ServiceStatus) bool { return s.Relay.State == "connected" })
	app, web := service.app, service.web
	occupied, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	settings.RelayLANPort = occupied.Addr().(*net.TCPAddr).Port
	if err := service.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := service.ApplyServiceChanges("relay"); err == nil {
		t.Fatal("relay accepted an occupied port")
	}
	status := waitTailnet(t, service, func(s ServiceStatus) bool { return s.Relay.State == "connected" })
	if status.Relay.Error == "" || !slices.Equal(status.PendingChanges, []string{"relay"}) || service.app != app || service.web != web {
		t.Fatal("failed relay restart harmed other entries", status)
	}
	if err := service.CancelServiceChanges("relay"); err != nil {
		t.Fatal(err)
	}
	if service.Status().RestartRequired || service.Status().Relay.Error != "" {
		t.Fatal("cancel did not restore relay settings", service.Status())
	}
}

func TestCancelTailnetActivationAfterRegistration(t *testing.T) {
	service, settings := settingsService(t)
	enableTestTailnet(t, service, settings, func(ctx context.Context, _ string, _ tailnet.Options, report func(tailnet.Status)) (tailnetConnection, error) {
		report(tailnet.Status{State: "NeedsLogin"})
		<-ctx.Done()
		return nil, ctx.Err()
	})
	waitTailnet(t, service, func(s ServiceStatus) bool { return s.TSNet.State == "NeedsLogin" })
	service.enrollTailnet = func(context.Context, string, tailnet.Options, func(tailnet.Status)) error { return nil }
	if err := service.RegisterTSNetAuthKey(t.Context(), testTailnetAuthKey()); err != nil {
		t.Fatal(err)
	}
	app, runner := service.app, service.runner
	if service.Status().TSNet.State != "Registered" {
		t.Fatal(service.Status())
	}
	if err := service.CancelServiceChanges("tsnet"); err != nil {
		t.Fatal(err)
	}
	status := service.Status()
	if status.RestartRequired || status.TSNet.State != "Disabled" || service.app != app || service.runner != runner {
		t.Fatal("cancel activation changed core or left pending status", status)
	}
}
