package workspace

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"spool/internal/adbbridge"
)

// A device shared through the ADB bridge is visible to every process of this
// host's user, agents included, exactly as if it were plugged in here, and the
// client decides what that "device" says to adb. Sharing therefore needs two
// local decisions: the host-level switch, off by default, and a grant for the
// user. Project access never implies either.

// ADBBridgeStatus is the host's side of the bridge for local management.
type ADBBridgeStatus struct {
	// Path is empty when adb is not installed on this host.
	Path    string
	Devices []adbbridge.Device
}

type adbBridgeHello struct {
	Type      string `json:"type"`
	CSRF      string `json:"csrf"`
	Transport string `json:"transport"`
	Device    string `json:"device"`
	Code      string `json:"code"`
}

var (
	adbBridgeHelloTimeout = 10 * time.Second
	adbBridgeRecheck      = 5 * time.Second
)

func (a *App) adbBridgeEnabled() bool {
	enabled, _ := a.adbBridgeAccess()
	return enabled
}

func (a *App) adbBridgeAccess() (bool, uint64) {
	a.configMu.RLock()
	defer a.configMu.RUnlock()
	return a.adb != nil && a.settings.ADBBridgeEnabled, a.adbEpoch
}

func (a *App) setADBBridgeEnabled(enabled bool) {
	a.configMu.Lock()
	changed := a.settings.ADBBridgeEnabled != enabled
	if changed && !enabled {
		a.adbEpoch++
	}
	a.settings.ADBBridgeEnabled = enabled
	a.configMu.Unlock()
	if changed {
		a.store.changes.publish(changeADBBridge)
	}
}

// adbBridgeStatus tells a signed-in client whether it can share a device and,
// if not, what is missing. A user without the grant learns nothing about the
// host's configuration.
func (a *App) adbBridgeStatus(w http.ResponseWriter, id authIdentity) {
	var status struct {
		Available bool   `json:"available"`
		Reason    string `json:"reason,omitempty"`
	}
	switch {
	case !id.User.ADBBridge:
		status.Reason = "not_authorized"
	case !a.adbBridgeEnabled():
		status.Reason = "disabled"
	case !a.adb.Available():
		status.Reason = "adb_missing"
	default:
		status.Available = true
	}
	apiJSON(w, http.StatusOK, status)
}

// adbBridgeStream carries one device's ADB transport, or one pairing exchange,
// between a client and this host's adb server.
func (a *App) adbBridgeStream(w http.ResponseWriter, r *http.Request, id authIdentity) {
	if r.Header.Get("Origin") == "" || !a.sameOrigin(r) {
		apiError(w, http.StatusForbidden, "请求来源或 CSRF 校验失败")
		return
	}
	enabled, hostEpoch := a.adbBridgeAccess()
	if !enabled || !id.User.ADBBridge {
		apiError(w, http.StatusForbidden, "The ADB bridge is unavailable or not authorized.")
		return
	}
	a.turnMu.Lock()
	if a.closing {
		a.turnMu.Unlock()
		apiError(w, http.StatusServiceUnavailable, "agent service is stopping")
		return
	}
	a.streams.Add(1)
	a.turnMu.Unlock()
	defer a.streams.Done()
	// The exact-origin check above also covers a public URL behind a proxy.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(4096)
	ctx, cancel := context.WithCancelCause(r.Context())
	defer cancel(nil)
	// Hijacked connections are not closed by http.Server.Close, and a device has
	// to leave adb before the service finishes stopping.
	go func() {
		select {
		case <-a.shutdown:
			cancel(&adbbridge.Error{Code: "stopping", Message: "The Spool service is stopping."})
		case <-ctx.Done():
		}
	}()

	helloCtx, helloDone := context.WithTimeout(ctx, adbBridgeHelloTimeout)
	kind, data, err := conn.Read(helloCtx)
	helloDone()
	var hello adbBridgeHello
	if err != nil || kind != websocket.MessageText || json.Unmarshal(data, &hello) != nil {
		return
	}
	if len(hello.CSRF) != len(id.CSRF) || subtle.ConstantTimeCompare([]byte(hello.CSRF), []byte(id.CSRF)) != 1 {
		_ = conn.Close(websocket.StatusPolicyViolation, "invalid credentials")
		return
	}
	if hello.Type != "attach" && hello.Type != "pair" {
		_ = conn.Close(websocket.StatusPolicyViolation, "invalid message")
		return
	}

	revoked := &adbbridge.Error{Code: "revoked", Message: "ADB bridge access ended."}
	// Compare both revocation epochs, not only the current switches: the change
	// hub can coalesce a revocation and a subsequent grant into one wakeup.
	allowed := func() bool {
		current, err := a.identity(r)
		enabled, epoch := a.adbBridgeAccess()
		return err == nil && current.User.ID == id.User.ID && current.User.ADBBridge && current.adbEpoch == id.adbEpoch && enabled && epoch == hostEpoch
	}
	sub := a.store.changes.subscribe()
	defer sub.close()
	// Subscribing before this check closes the race with a concurrent sign-out,
	// a revoked grant or the bridge being turned off.
	if !allowed() {
		cancel(revoked)
	}
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		ticker := time.NewTicker(adbBridgeRecheck)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-sub.wake:
				if sub.take()&(changeAuth|changeADBBridge) == 0 {
					continue
				}
			case <-ticker.C:
			}
			if !allowed() {
				cancel(revoked)
				return
			}
		}
	}()
	defer func() { cancel(nil); <-watchDone }()

	// The pairing code and CSRF token are never logged.
	log.Printf("event=adb_bridge_opened user=%q kind=%s transport=%q", id.User.Username, hello.Type, hello.Transport)
	var serial string
	if hello.Type == "pair" {
		err = a.adb.Pair(ctx, conn, hello.Code)
	} else {
		serial, err = a.adb.Attach(ctx, conn, adbbridge.Request{Owner: id.User.ID, Transport: adbbridge.Transport(hello.Transport), Device: hello.Device})
	}
	reason := "completed"
	if err != nil {
		reason = err.Error()
	}
	log.Printf("event=adb_bridge_closed user=%q kind=%s serial=%q reason=%q", id.User.Username, hello.Type, serial, reason)
}

// SetADBBridgeEnabled is a local management operation that applies at once.
// Turning the bridge off detaches every shared device.
func (s *Service) SetADBBridgeEnabled(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("service is closed")
	}
	settings, err := s.Store.Settings()
	if err != nil {
		return err
	}
	settings.ADBBridgeEnabled = enabled
	if err = s.Store.SaveSettings(settings); err != nil {
		return err
	}
	s.applyRuntimeSettingsLocked(settings)
	return nil
}

func (s *Service) adbBridgeStatusLocked() ADBBridgeStatus {
	status := ADBBridgeStatus{Devices: []adbbridge.Device{}}
	if s.adb != nil {
		status.Devices = s.adb.Devices()
	}
	status.Path, _ = adbbridge.Locate()
	return status
}
