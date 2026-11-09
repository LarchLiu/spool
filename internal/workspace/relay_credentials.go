package workspace

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"spool/relay"
)

// Credentials stay in a private file, never in settings or status responses.
// ServerID is set only after this origin confirms enrollment with this token.
type relayRegistration struct {
	Token    string `json:"token"`
	ServerID string `json:"server_id,omitempty"`
}

func relayOrigin(address string) string {
	u, err := relay.ParseRelayURL(strings.TrimSpace(address))
	if err != nil {
		return ""
	}
	host, port := strings.ToLower(u.Hostname()), u.Port()
	if u.Scheme == "https" && port == "443" || u.Scheme == "http" && port == "80" {
		port = ""
	}
	u.Host = host
	if port != "" {
		u.Host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		u.Host = "[" + host + "]"
	}
	return u.String()
}

func (s *Service) loadRelayRegistrationsLocked() (map[string]relayRegistration, error) {
	data, err := os.ReadFile(filepath.Join(s.Store.Root(), "relay", "registrations.json"))
	if os.IsNotExist(err) {
		return s.migrateRelayRegistrationsLocked()
	}
	if err != nil {
		return nil, fmt.Errorf("read relay credentials: %w", err)
	}
	var registrations map[string]relayRegistration
	if json.Unmarshal(data, &registrations) != nil || registrations == nil {
		return nil, fmt.Errorf("invalid saved relay credentials")
	}
	return registrations, nil
}

// Import the old single credential once. Its confirmation owns the origin even
// if the user has already selected another address. An unconfirmed legacy or
// environment token belongs only to the address configured at startup.
func (s *Service) migrateRelayRegistrationsLocked() (map[string]relayRegistration, error) {
	registrations := make(map[string]relayRegistration)
	settings, err := s.Store.Settings()
	if err != nil {
		return nil, err
	}
	origin := relayOrigin(settings.RelayURL)
	var previous struct {
		URL       string `json:"url"`
		ServerID  string `json:"server_id"`
		TokenHash string `json:"token_hash"`
	}
	data, err := os.ReadFile(filepath.Join(s.Store.Root(), "relay", "registration.json"))
	if err == nil {
		if json.Unmarshal(data, &previous) != nil || relayOrigin(previous.URL) == "" {
			return nil, fmt.Errorf("invalid legacy relay registration")
		}
		origin = relayOrigin(previous.URL)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	data, err = os.ReadFile(filepath.Join(s.Store.Root(), "relay", "registration-token"))
	token := strings.TrimSpace(string(data))
	if os.IsNotExist(err) {
		token = strings.TrimSpace(os.Getenv("SPOOL_RELAY_REGISTRATION_TOKEN"))
	} else if err != nil {
		return nil, err
	}
	if origin != "" {
		registration := relayRegistration{Token: token}
		hash := sha256.Sum256([]byte(token))
		if token != "" && previous.TokenHash == hex.EncodeToString(hash[:]) {
			registration.ServerID = previous.ServerID
		}
		registrations[origin] = registration
	}
	// Leave a fresh workspace untouched until relay is configured. An existing
	// unscoped credential still needs an empty migration marker so it cannot
	// later be reused for a different relay.
	if origin == "" && token == "" && previous.URL == "" {
		return registrations, nil
	}
	if err := s.saveRelayRegistrationsLocked(registrations); err != nil {
		return nil, err
	}
	return registrations, nil
}

func (s *Service) saveRelayRegistrationsLocked(registrations map[string]relayRegistration) error {
	data, err := json.Marshal(registrations)
	if err != nil {
		return err
	}
	return s.saveRelaySecretLocked("registrations.json", string(data))
}

func (s *Service) relayRegistrationToken(address string) string {
	registrations, err := s.loadRelayRegistrationsLocked()
	if err != nil {
		return ""
	}
	return registrations[relayOrigin(address)].Token
}

func (s *Service) relayRegisteredLocked(address string) bool {
	registrations, err := s.loadRelayRegistrationsLocked()
	if err != nil {
		return false
	}
	registration := registrations[relayOrigin(address)]
	if registration.Token == "" || registration.ServerID == "" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(s.Store.Root(), "relay", "identity.json"))
	var id relay.Identity
	return err == nil && json.Unmarshal(data, &id) == nil && len(id.Seed) == ed25519.SeedSize && id.ID() == registration.ServerID
}

func (s *Service) confirmRelayRegistrationLocked(address string, id *relay.Identity, token string) error {
	registrations, err := s.loadRelayRegistrationsLocked()
	if err != nil {
		return err
	}
	origin := relayOrigin(address)
	confirmed := relayRegistration{Token: token, ServerID: id.ID()}
	if registrations[origin] == confirmed {
		return nil
	}
	registrations[origin] = confirmed
	return s.saveRelayRegistrationsLocked(registrations)
}

func (s *Service) invalidateRelayRegistrationLocked(address string) error {
	registrations, err := s.loadRelayRegistrationsLocked()
	if err != nil {
		return err
	}
	origin := relayOrigin(address)
	registration := registrations[origin]
	registration.ServerID = ""
	registrations[origin] = registration
	return s.saveRelayRegistrationsLocked(registrations)
}

// SetRelayRegistrationToken imports a credential for the selected origin without
// confirming enrollment. Desktop verifies and saves through RegisterRelay.
func (s *Service) SetRelayRegistrationToken(token string) error {
	token = strings.TrimSpace(token)
	if token != "" && (len(token) < 32 || len(token) > 512 || strings.ContainsAny(token, "\r\n")) {
		return fmt.Errorf("relay registration token must be empty or contain 32–512 characters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.relayRegistering {
		return fmt.Errorf("Relay registration is unavailable or already in progress.")
	}
	settings, err := s.Store.Settings()
	if err != nil {
		return err
	}
	return s.saveRelayRegistrationTokenLocked(settings.RelayURL, token)
}

func (s *Service) saveRelayRegistrationTokenLocked(address, token string) error {
	origin := relayOrigin(address)
	if origin == "" {
		return fmt.Errorf("Enter a valid relay HTTPS address before registering.")
	}
	registrations, err := s.loadRelayRegistrationsLocked()
	if err != nil {
		return err
	}
	if registration, ok := registrations[origin]; ok && registration.Token == token {
		return nil
	}
	registrations[origin] = relayRegistration{Token: token}
	return s.saveRelayRegistrationsLocked(registrations)
}
