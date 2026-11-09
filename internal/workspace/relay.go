package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/grandcat/zeroconf"
	qrcode "github.com/skip2/go-qrcode"
	"spool/internal/netproxy"
	"spool/relay"
)

type RelayStatus struct {
	State, ServerID, LANAddress, DiscoveryError string
	WebURL                                      string
	Error                                       string
	RegistrationTokenSet                        bool
	Registered, Registering                     bool
}
type relayRuntime struct {
	cancel  context.CancelFunc
	host    *relay.Host
	lan     net.Listener
	ingress *httpIngress
	devices *relay.Devices
	mu      sync.Mutex
	status  RelayStatus
}

func (r *relayRuntime) snapshot() RelayStatus { r.mu.Lock(); defer r.mu.Unlock(); return r.status }
func (r *relayRuntime) close() {
	r.cancel()
	r.host.Close()
	r.lan.Close()
	r.ingress.close()
	r.devices.CloseConnections()
}
func sameRelaySettings(a, b Settings) bool {
	return a.RelayEnabled == b.RelayEnabled && (!a.RelayEnabled || a.RelayURL == b.RelayURL && a.RelayLANPort == b.RelayLANPort && a.RelayWebEnabled == b.RelayWebEnabled)
}

func (s *Service) startRelayLocked(settings Settings) error {
	registrations, err := s.loadRelayRegistrationsLocked()
	if err != nil {
		return err
	}
	id, err := relay.LoadIdentity(filepath.Join(s.Store.Root(), "relay"))
	if err != nil {
		return err
	}
	tlsConfig, err := id.TLSConfig()
	if err != nil {
		return err
	}
	devices, err := s.relayDevicesLocked()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(s.ctx)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = netproxy.Proxy
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("relay redirects are disabled") }}
	token := registrations[relayOrigin(settings.RelayURL)].Token
	host, err := relay.NewHost(ctx, settings.RelayURL, token, id, client)
	if err != nil {
		cancel()
		return err
	}
	listener, err := net.Listen("tcp", net.JoinHostPort("", strconv.Itoa(settings.RelayLANPort)))
	if err != nil {
		cancel()
		host.Close()
		return fmt.Errorf("listen for secure LAN connections: %w", err)
	}
	// Admission follows this applied ingress. Saved pending settings take effect
	// only when ApplyServiceChanges replaces it, including rollback on failure.
	webAllowed := func() bool { return ctx.Err() == nil && settings.RelayEnabled && settings.RelayWebEnabled }
	ingress := s.newIngressLocked()
	ingress.server.Handler = devices.Handler(s.app, id.ID(), webAllowed)
	runtime := &relayRuntime{ingress: ingress, cancel: cancel, host: host, lan: listener, devices: devices, status: RelayStatus{State: "connecting", ServerID: id.ID(), LANAddress: listener.Addr().String(), RegistrationTokenSet: token != ""}}
	host.WebEnabled = settings.RelayWebEnabled
	host.OnWebURL = func(address string) {
		runtime.mu.Lock()
		runtime.status.WebURL = address
		runtime.mu.Unlock()
		if address != "" {
			log.Printf("Spool Relay Web: %s", address)
		}
	}
	host.OnStatus = func(state string) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.relay != runtime {
			return
		}
		runtime.mu.Lock()
		runtime.status.State = state
		if state != "connected" {
			runtime.status.WebURL = ""
		}
		runtime.mu.Unlock()
		if state == "registration_required" && !s.relayRegistering && s.relayRegistrationToken(settings.RelayURL) == host.RegistrationToken() && s.relayRegisteredLocked(settings.RelayURL) {
			// A network outage preserves registration. An explicit rejection
			// requires enrollment again, including after an app restart.
			if err := s.invalidateRelayRegistrationLocked(settings.RelayURL); err != nil {
				log.Printf("event=relay_registration_invalidation_failed error=%q", err)
			}
		}
	}
	s.relay = runtime
	host.OnRegistrationToken = func(issued string) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		// Do not overwrite a manual update or a replacement service runtime.
		if s.relay != runtime || ctx.Err() != nil || s.relayRegistrationToken(settings.RelayURL) != token {
			return fmt.Errorf("relay registration settings changed")
		}
		return s.saveRelayRegistrationTokenLocked(settings.RelayURL, issued)
	}
	host.OnRegistered = func(credential string) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.relay != runtime || ctx.Err() != nil || s.relayRegistrationToken(settings.RelayURL) != credential {
			return fmt.Errorf("relay registration settings changed")
		}
		return s.confirmRelayRegistrationLocked(settings.RelayURL, id, credential)
	}
	s.serveLocked(runtime.ingress.server, devices.Listener(host, tlsConfig, id.ID(), webAllowed, true))
	s.serveLocked(runtime.ingress.server, devices.Listener(listener, tlsConfig, id.ID(), webAllowed, false))
	s.workers.Add(2)
	go func() { defer s.workers.Done(); defer transport.CloseIdleConnections(); host.Run(); host.Wait() }()
	go func() {
		defer s.workers.Done()
		// Refresh advertisements after interface changes without restarting
		// Spool. Only addresses are published; pairing secrets stay local.
		var advert *zeroconf.Server
		defer func() {
			if advert != nil {
				advert.Shutdown()
			}
		}()
		var previous string
		timer := time.NewTicker(15 * time.Second)
		defer timer.Stop()
		for {
			addresses := relayLANAddresses(settings.RelayLANPort)
			fingerprint := fmt.Sprint(addresses)
			if advert == nil || fingerprint != previous {
				if advert != nil {
					advert.Shutdown()
					advert = nil
				}
				if len(addresses) > 0 {
					advert, err = zeroconf.Register("Spool-"+id.ID()[:12], relay.ServiceType, "local.", settings.RelayLANPort, []string{"id=" + id.ID(), "v=1"}, nil)
					runtime.mu.Lock()
					runtime.status.DiscoveryError = ""
					if err != nil {
						runtime.status.DiscoveryError = "LAN discovery unavailable; paired addresses and relay remain usable"
					}
					runtime.mu.Unlock()
				}
				previous = fingerprint
			}
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
		}
	}()
	log.Printf("event=relay_started server_id=%q lan_port=%d", id.ID(), settings.RelayLANPort)
	return nil
}

func relayLANAddresses(port int) []string {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	out := []string{}
	for _, address := range addresses {
		ip, _, err := net.ParseCIDR(address.String())
		if err != nil || !ip.IsPrivate() {
			continue
		}
		out = append(out, net.JoinHostPort(ip.String(), strconv.Itoa(port)))
	}
	sort.Strings(out)
	if len(out) > 16 {
		out = out[:16]
	}
	return out
}

// RelayPairingCode is a local management operation. It is deliberately absent
// from the web API. The invitation is short-lived, single-device and requires
// a separate local approval before any application request is admitted.
func (s *Service) RelayPairingCode() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.relay == nil {
		return "", fmt.Errorf("Start Spool Relay before pairing a phone.")
	}
	// Use the running host and listener, even if another address is saved but
	// still waiting to be applied. A code must describe a reachable ingress.
	id := s.relay.host.Identity
	port := s.relay.lan.Addr().(*net.TCPAddr).Port
	devices, err := s.relayDevicesLocked()
	if err != nil {
		return "", err
	}
	invitation, expires := devices.Invite()
	return (relay.Pairing{Version: 2, RelayURL: s.relay.host.URL, PublicKey: id.PublicKey(), AccessToken: id.AccessToken, LAN: relayLANAddresses(port), Invitation: invitation, ExpiresAt: expires}).Encode()
}

// RegisterRelay enrolls while the relay switch remains off. Only a confirmed
// remote registration is saved; errors never unlock the enable switch.
func (s *Service) RegisterRelay(ctx context.Context, address, token string) error {
	return s.RegisterRelayNamed(ctx, address, token, "")
}

func (s *Service) RegisterRelayNamed(ctx context.Context, address, token, name string) error {
	address, token = strings.TrimSpace(address), strings.TrimSpace(token)
	if relayOrigin(address) == "" {
		return fmt.Errorf("Enter a valid relay HTTPS address before registering.")
	}
	s.mu.Lock()
	if s.closed || s.relayRegistering {
		s.mu.Unlock()
		return fmt.Errorf("Relay registration is unavailable or already in progress.")
	}
	settings, err := s.Store.Settings()
	if err != nil || relayOrigin(settings.RelayURL) != relayOrigin(address) {
		s.mu.Unlock()
		return fmt.Errorf("Save the relay address before registering.")
	}
	registrations, err := s.loadRelayRegistrationsLocked()
	if err != nil {
		s.mu.Unlock()
		return err
	}
	saved := registrations[relayOrigin(address)]
	if (token == "" || token == saved.Token) && s.relayRegisteredLocked(address) {
		s.mu.Unlock()
		return nil
	}
	id, err := relay.LoadIdentity(filepath.Join(s.Store.Root(), "relay"))
	if err != nil {
		s.mu.Unlock()
		return err
	}
	s.relayRegistering = true
	s.workers.Add(1)
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.relayRegistering = false
		s.mu.Unlock()
		s.workers.Done()
	}()
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = netproxy.Proxy
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("relay redirects are disabled") }}
	credential, err := relay.RegisterNamed(ctx, address, token, name, id, client)
	if err != nil {
		if errors.Is(err, relay.ErrInvalidRegistration) {
			return fmt.Errorf("Relay name must be at most 128 bytes and contain no control characters.")
		}
		if errors.Is(err, relay.ErrRegistrationRejected) {
			return fmt.Errorf("Relay registration was rejected. Check the token or whether automatic registration is allowed.")
		}
		if ctx.Err() != nil {
			return fmt.Errorf("Relay registration timed out or was canceled. Try again.")
		}
		return fmt.Errorf("Could not register with the relay. Check its address and network connection.")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.Store.Settings()
	if err != nil || s.closed || relayOrigin(current.RelayURL) != relayOrigin(address) {
		return fmt.Errorf("Relay settings changed during registration. Try again.")
	}
	return s.confirmRelayRegistrationLocked(address, id, credential)
}

// UnregisterRelay clears only the selected origin after a signed remote
// acknowledgement. A failure preserves its credential so the user can retry.
func (s *Service) UnregisterRelay(ctx context.Context, address string) error {
	origin := relayOrigin(address)
	s.mu.Lock()
	if s.closed || s.relayRegistering {
		s.mu.Unlock()
		return fmt.Errorf("Relay registration is unavailable or already in progress.")
	}
	settings, err := s.Store.Settings()
	if err != nil || origin == "" || relayOrigin(settings.RelayURL) != origin {
		s.mu.Unlock()
		return fmt.Errorf("Save the relay address before registering.")
	}
	registrations, err := s.loadRelayRegistrationsLocked()
	if err != nil {
		s.mu.Unlock()
		return err
	}
	token := registrations[origin].Token
	if token == "" {
		s.mu.Unlock()
		return fmt.Errorf("No saved registration for this relay.")
	}
	id, err := relay.LoadIdentity(filepath.Join(s.Store.Root(), "relay"))
	if err != nil {
		s.mu.Unlock()
		return err
	}
	s.relayRegistering = true
	s.workers.Add(1)
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.relayRegistering = false; s.mu.Unlock(); s.workers.Done() }()
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = netproxy.Proxy
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("relay redirects are disabled") }}
	if err := relay.Unregister(ctx, address, token, id, client); err != nil {
		return fmt.Errorf("Could not unregister from the relay. Check its address and network connection, then retry.")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("service is closed")
	}
	if s.relay != nil && relayOrigin(s.relay.host.URL) == origin {
		s.relay.close()
		s.relay = nil
	}
	current, err := s.Store.Settings()
	if err != nil {
		return err
	}
	if relayOrigin(current.RelayURL) == origin {
		current.RelayEnabled = false
		if err := s.Store.SaveSettings(current); err != nil {
			return err
		}
		if relayOrigin(s.activeSettings.RelayURL) == origin {
			s.activeSettings.RelayEnabled = false
			if s.app != nil {
				s.app.configureRelayWeb(s.activeSettings)
			}
		}
	}
	registrations, err = s.loadRelayRegistrationsLocked()
	if err != nil {
		return err
	}
	delete(registrations, origin)
	return s.saveRelayRegistrationsLocked(registrations)
}

func (s *Service) saveRelaySecretLocked(name, value string) error {
	dir := filepath.Join(s.Store.Root(), "relay")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, "token-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.WriteString(value)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), filepath.Join(dir, name))
}

type RelayPairingInfo struct {
	Code        string `json:"code"`
	QR          string `json:"qr"`
	RelayURL    string `json:"relay_url"`
	ServerID    string `json:"server_id"`
	Fingerprint string `json:"fingerprint"`
	ExpiresAt   int64  `json:"expires_at"`
}

func (s *Service) RelayPairingInfo() (RelayPairingInfo, error) {
	code, err := s.RelayPairingCode()
	if err != nil {
		return RelayPairingInfo{}, err
	}
	p, err := relay.ParsePairing(code)
	if err != nil {
		return RelayPairingInfo{}, err
	}
	png, err := qrcode.Encode(code, qrcode.Medium, 512)
	if err != nil {
		return RelayPairingInfo{}, err
	}
	hash := sha256.Sum256(p.PublicKey)
	hexValue := hex.EncodeToString(hash[:])
	parts := []string{}
	for i := 0; i < len(hexValue); i += 8 {
		parts = append(parts, hexValue[i:i+8])
	}
	return RelayPairingInfo{Code: code, QR: "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), RelayURL: p.RelayURL, ServerID: p.ID(), Fingerprint: strings.Join(parts, " "), ExpiresAt: p.ExpiresAt}, nil
}

func (s *Service) relayDevicesLocked() (*relay.Devices, error) {
	if s.relayDevices == nil {
		devices, err := relay.OpenDevices(filepath.Join(s.Store.Root(), "relay"))
		if err != nil {
			return nil, err
		}
		s.relayDevices = devices
	}
	return s.relayDevices, nil
}

func (s *Service) RelayDevices() (relay.DeviceSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.relayDevicesLocked()
	if err != nil {
		return relay.DeviceSnapshot{}, err
	}
	return d.Snapshot(), nil
}
func (s *Service) DecideRelayDevice(requestID string, approve bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.relayDevicesLocked()
	if err != nil {
		return err
	}
	return d.Decide(requestID, approve)
}
func (s *Service) RevokeRelayDevice(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.relayDevicesLocked()
	if err != nil {
		return err
	}
	return d.Revoke(id)
}
func (s *Service) CancelRelayPairing(code string) error {
	p, err := relay.ParsePairing(code)
	if err != nil {
		return err
	}
	if p.Invitation == "" {
		return fmt.Errorf("pairing invitation required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.relayDevicesLocked()
	if err != nil {
		return err
	}
	d.CancelInvitation(p.Invitation)
	return nil
}
