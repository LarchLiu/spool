package workspace

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
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
	RegistrationTokenSet                        bool
	Registered, Registering                     bool
}
type relayRuntime struct {
	cancel context.CancelFunc
	host   *relay.Host
	lan    net.Listener
	mu     sync.Mutex
	status RelayStatus
}

func (r *relayRuntime) snapshot() RelayStatus { r.mu.Lock(); defer r.mu.Unlock(); return r.status }
func (r *relayRuntime) close()                { r.cancel(); r.host.Close(); r.lan.Close() }
func sameRelaySettings(a, b Settings) bool {
	return a.RelayEnabled == b.RelayEnabled && (!a.RelayEnabled || a.RelayURL == b.RelayURL && a.RelayLANPort == b.RelayLANPort)
}

func (s *Service) startRelayLocked(settings Settings) error {
	id, err := relay.LoadIdentity(filepath.Join(s.Store.Root(), "relay"))
	if err != nil {
		return err
	}
	tlsConfig, err := id.TLSConfig()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(s.ctx)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = netproxy.Proxy
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("relay redirects are disabled") }}
	token := s.relayRegistrationToken()
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
	runtime := &relayRuntime{cancel: cancel, host: host, lan: listener, status: RelayStatus{State: "connecting", ServerID: id.ID(), LANAddress: listener.Addr().String(), RegistrationTokenSet: token != ""}}
	host.OnStatus = func(state string) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.relay != runtime {
			return
		}
		runtime.mu.Lock()
		runtime.status.State = state
		runtime.mu.Unlock()
		if state == "registration_required" && s.relayRegistrationToken() == host.RegistrationToken() && s.relayRegisteredLocked(settings.RelayURL) {
			// A network outage preserves registration. An explicit rejection
			// requires enrollment again, including after an app restart.
			if err := os.Remove(filepath.Join(s.Store.Root(), "relay", "registration.json")); err != nil && !os.IsNotExist(err) {
				log.Printf("event=relay_registration_invalidation_failed error=%q", err)
			}
		}
	}
	s.relay = runtime
	host.OnRegistrationToken = func(issued string) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		// Do not overwrite a manual update or a replacement service runtime.
		if s.relay != runtime || ctx.Err() != nil || s.relayRegistrationToken() != token {
			return fmt.Errorf("relay registration settings changed")
		}
		return s.saveRelayRegistrationTokenLocked(issued)
	}
	host.OnRegistered = func(credential string) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.relay != runtime || ctx.Err() != nil || s.relayRegistrationToken() != credential {
			return fmt.Errorf("relay registration settings changed")
		}
		return s.confirmRelayRegistrationLocked(settings.RelayURL, id, credential)
	}
	s.serveLocked(s.server, tls.NewListener(host, tlsConfig))
	s.serveLocked(s.server, tls.NewListener(listener, tlsConfig))
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
// from the web API: knowledge of the code permits relay admission, not login.
func (s *Service) RelayPairingCode() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	settings, err := s.Store.Settings()
	if err != nil {
		return "", err
	}
	if !settings.RelayEnabled {
		return "", fmt.Errorf("enable relay and configure its URL first")
	}
	id, err := relay.LoadIdentity(filepath.Join(s.Store.Root(), "relay"))
	if err != nil {
		return "", err
	}
	return (relay.Pairing{Version: 1, RelayURL: settings.RelayURL, PublicKey: id.PublicKey(), AccessToken: id.AccessToken, LAN: relayLANAddresses(settings.RelayLANPort)}).Encode()
}

func (s *Service) relayRegistrationToken() string {
	data, err := os.ReadFile(filepath.Join(s.Store.Root(), "relay", "registration-token"))
	if err == nil {
		return strings.TrimSpace(string(data))
	}
	return strings.TrimSpace(os.Getenv("SPOOL_RELAY_REGISTRATION_TOKEN"))
}

type relayRegistration struct {
	URL       string `json:"url"`
	ServerID  string `json:"server_id"`
	TokenHash string `json:"token_hash"`
}

func relayOrigin(address string) string {
	u, err := relay.ParseRelayURL(strings.TrimSpace(address))
	if err != nil {
		return ""
	}
	return u.String()
}

// A stored token alone is not proof of enrollment. Bind the successful relay
// response to this origin, identity and credential, so changing any invalidates it.
func (s *Service) relayRegisteredLocked(address string) bool {
	origin := relayOrigin(address)
	if origin == "" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(s.Store.Root(), "relay", "registration.json"))
	var confirmed relayRegistration
	if err != nil || json.Unmarshal(data, &confirmed) != nil || confirmed.URL != origin {
		return false
	}
	data, err = os.ReadFile(filepath.Join(s.Store.Root(), "relay", "identity.json"))
	var id relay.Identity
	if err != nil || json.Unmarshal(data, &id) != nil || len(id.Seed) != ed25519.SeedSize || id.ID() != confirmed.ServerID {
		return false
	}
	token := s.relayRegistrationToken()
	hash := sha256.Sum256([]byte(token))
	return token != "" && confirmed.TokenHash == hex.EncodeToString(hash[:])
}

func (s *Service) confirmRelayRegistrationLocked(address string, id *relay.Identity, token string) error {
	saved, err := os.ReadFile(filepath.Join(s.Store.Root(), "relay", "registration-token"))
	if err != nil || strings.TrimSpace(string(saved)) != token {
		if err := s.saveRelayRegistrationTokenLocked(token); err != nil {
			return err
		}
	}
	if s.relayRegisteredLocked(address) {
		return nil
	}
	hash := sha256.Sum256([]byte(token))
	data, err := json.Marshal(relayRegistration{URL: relayOrigin(address), ServerID: id.ID(), TokenHash: hex.EncodeToString(hash[:])})
	if err != nil {
		return err
	}
	return s.saveRelaySecretLocked("registration.json", string(data))
}

// RegisterRelay enrolls while the relay switch remains off. Only a confirmed
// remote registration is saved; errors never unlock the enable switch.
func (s *Service) RegisterRelay(ctx context.Context, address, token string) error {
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
	credential, err := relay.Register(ctx, address, token, id, client)
	if err != nil {
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

// SetRelayRegistrationToken imports a credential without confirming enrollment.
// Desktop uses RegisterRelay to verify and save it in one operation.
func (s *Service) SetRelayRegistrationToken(token string) error {
	token = strings.TrimSpace(token)
	if token != "" && (len(token) < 32 || len(token) > 512 || strings.ContainsAny(token, "\r\n")) {
		return fmt.Errorf("relay registration token must be empty or contain 32–512 characters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveRelayRegistrationTokenLocked(token)
}

func (s *Service) saveRelayRegistrationTokenLocked(token string) error {
	return s.saveRelaySecretLocked("registration-token", token)
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
	return RelayPairingInfo{Code: code, QR: "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), RelayURL: p.RelayURL, ServerID: p.ID(), Fingerprint: strings.Join(parts, " ")}, nil
}
