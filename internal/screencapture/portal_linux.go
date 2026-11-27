//go:build linux && (amd64 || arm64)

package screencapture

import (
	"context"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/google/uuid"
)

const portalDestination = "org.freedesktop.portal.Desktop"
const portalRequestInterface = "org.freedesktop.portal.Request"

func portalScreenshot(ctx context.Context) ([]byte, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, ErrFailed
	}
	defer conn.Close()
	signals := make(chan *dbus.Signal, 8)
	conn.Signal(signals)
	defer conn.RemoveSignal(signals)
	rule := []dbus.MatchOption{dbus.WithMatchSender(portalDestination), dbus.WithMatchInterface(portalRequestInterface), dbus.WithMatchMember("Response")}
	if err := conn.AddMatchSignalContext(ctx, rule...); err != nil {
		return nil, ErrFailed
	}
	defer conn.RemoveMatchSignal(rule...)
	token := "spool_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	var sender string
	for _, name := range conn.Names() {
		if strings.HasPrefix(name, ":") {
			sender = strings.ReplaceAll(strings.TrimPrefix(name, ":"), ".", "_")
			break
		}
	}
	if sender == "" {
		return nil, ErrFailed
	}
	request := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/" + sender + "/" + token)
	// Closing a pending request dismisses the system picker on timeout/shutdown.
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = conn.Object(portalDestination, request).CallWithContext(closeCtx, portalRequestInterface+".Close", 0).Err
	}()
	options := map[string]dbus.Variant{"handle_token": dbus.MakeVariant(token), "interactive": dbus.MakeVariant(true), "modal": dbus.MakeVariant(true)}
	call := conn.Object(portalDestination, "/org/freedesktop/portal/desktop").CallWithContext(ctx, "org.freedesktop.portal.Screenshot.Screenshot", 0, "", options)
	if err := call.Store(&request); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrFailed
	}
	if !request.IsValid() || !strings.HasPrefix(string(request), "/org/freedesktop/portal/desktop/request/") {
		return nil, ErrFailed
	}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case signal, ok := <-signals:
			if !ok {
				return nil, ErrFailed
			}
			if signal == nil || signal.Path != request || signal.Name != portalRequestInterface+".Response" {
				continue
			}
			var code uint32
			var results map[string]dbus.Variant
			if err := dbus.Store(signal.Body, &code, &results); err != nil {
				return nil, ErrFailed
			}
			if code == 1 {
				return nil, context.Canceled
			}
			if code != 0 {
				return nil, ErrFailed
			}
			uri, ok := results["uri"].Value().(string)
			if !ok {
				return nil, ErrFailed
			}
			return readPortalImage(uri)
		}
	}
}
