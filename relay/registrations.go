package relay

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	ErrInvalidRegistration      = errors.New("a registration name of 1–128 bytes without control characters is required")
	ErrInvalidRegistrationCount = errors.New("count must be an integer between 1 and 100")
	ErrRegistrationExists       = errors.New("this server already has an active registration")
	ErrRegistrationNotFound     = errors.New("registration not found")
	ErrRegistrationRevoked      = errors.New("server registration was revoked")
	// Returned for a new identity enrolling without a token. Enrolling with an
	// administrator-issued token is not limited.
	ErrRegistrationLimited = errors.New("too many new automatic registrations from this network")
)

// Registration contains public administrative metadata. Tokens are returned
// once at creation; only their SHA-256 digests are persisted on the relay.
type Registration struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	ServerID       string     `json:"server_id,omitempty"`
	Automatic      bool       `json:"automatic,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	RevokedAt      *time.Time `json:"revoked_at,omitempty"`
	UnregisteredAt *time.Time `json:"unregistered_at,omitempty"`
	RegistrationIP string     `json:"registration_ip,omitempty"`
	WebPublished   bool       `json:"web_published"`
	WebBlocked     bool       `json:"web_blocked"`
}

type registrationRecord struct {
	Registration
	TokenHash string `json:"token_hash"`
}

func (r registrationRecord) active() bool { return r.RevokedAt == nil && r.UnregisteredAt == nil }

type registrationMetadata struct {
	name, ip   string
	enrollment bool
}

func (r *registrationRecord) applyMetadata(metadata registrationMetadata, first bool) {
	if metadata.name != "" {
		r.Name = metadata.name
	}
	if r.RegistrationIP == "" && (first || metadata.enrollment) {
		r.RegistrationIP = metadata.ip
	}
}

type registrationStatus struct {
	Registration
	Connected   bool   `json:"connected"`
	Connections int    `json:"connections"`
	WebURL      string `json:"web_url,omitempty"`
}

const maxRegistryBytes = 8 << 20
const maxRegistrations = 10000
const maxRegistrationBatch = 100

type issuedRegistration struct {
	Registration Registration `json:"registration"`
	Token        string       `json:"token"`
}

func randomRegistrationName() (string, error) {
	const letters = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	var name [12]byte
	for i := range name {
		index, err := rand.Int(rand.Reader, big.NewInt(int64(len(letters))))
		if err != nil {
			return "", err
		}
		name[i] = letters[index.Int64()]
	}
	return string(name[:]), nil
}

func validServerID(id string) bool {
	if len(id) != 52 || strings.ToLower(id) != id {
		return false
	}
	encoding := base32.StdEncoding.WithPadding(base32.NoPadding)
	b, err := encoding.DecodeString(strings.ToUpper(id))
	return err == nil && len(b) == sha256.Size && strings.ToLower(encoding.EncodeToString(b)) == id
}

func validRegistrationName(name string) bool {
	return name != "" && len(name) <= 128 && utf8.ValidString(name) && strings.IndexFunc(name, unicode.IsControl) < 0
}

func tokenDigest(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

func (s *Server) loadRegistrations() error {
	if err := os.MkdirAll(s.opts.DataDir, 0700); err != nil {
		return err
	}
	file, err := os.Open(filepath.Join(s.opts.DataDir, "registrations.json"))
	if os.IsNotExist(err) {
		return s.saveRegistrations(s.registrations)
	}
	if err != nil {
		return err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxRegistryBytes+1))
	if err != nil {
		return err
	}
	var saved struct {
		Version       int                  `json:"version"`
		Registrations []registrationRecord `json:"registrations"`
	}
	if len(data) > maxRegistryBytes || json.Unmarshal(data, &saved) != nil || (saved.Version != 1 && saved.Version != 2) || len(saved.Registrations) > maxRegistrations {
		return errors.New("invalid relay registration store")
	}
	active := make(map[string]bool)
	for _, record := range saved.Registrations {
		digest, err := hex.DecodeString(record.TokenHash)
		if record.RegistrationIP != "" {
			if _, err := netip.ParseAddr(record.RegistrationIP); err != nil {
				return errors.New("invalid relay registration IP")
			}
		}
		if !validToken(record.ID) || !validRegistrationName(record.Name) || (record.ServerID != "" && !validServerID(record.ServerID)) || record.CreatedAt.IsZero() ||
			err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != record.TokenHash || s.registrations[record.ID].ID != "" || s.tokenIndex[record.TokenHash] != "" {
			return errors.New("invalid relay registration record")
		}
		if record.active() && record.ServerID != "" {
			if active[record.ServerID] {
				return errors.New("duplicate active relay registration")
			}
			active[record.ServerID] = true
		}
		s.registrations[record.ID] = record
		s.tokenIndex[record.TokenHash] = record.ID
	}
	return nil
}

// Caller holds Server.mu. Save a complete snapshot before publishing changes
// to authentication state. A failed write/rename leaves the old state intact.
func (s *Server) saveRegistrations(records map[string]registrationRecord) error {
	list := make([]registrationRecord, 0, len(records))
	for _, record := range records {
		list = append(list, record)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	data, err := json.MarshalIndent(struct {
		Version       int                  `json:"version"`
		Registrations []registrationRecord `json:"registrations"`
	}{2, list}, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > maxRegistryBytes {
		return errors.New("relay registration store is full")
	}
	file, err := os.CreateTemp(s.opts.DataDir, ".registrations-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), filepath.Join(s.opts.DataDir, "registrations.json"))
}

func (s *Server) registrationForRequestLocked(r *http.Request) (registrationRecord, bool) {
	record, ok := s.credentialForRequestLocked(r)
	return record, ok && record.active()
}

// Inactive credentials are accepted only by the signed unregister endpoint,
// so a lost acknowledgement can be retried without reactivating a token.
func (s *Server) credentialForRequestLocked(r *http.Request) (registrationRecord, bool) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || !validToken(token) {
		return registrationRecord{}, false
	}
	record, ok := s.registrations[s.tokenIndex[tokenDigest(token)]]
	return record, ok
}

// CreateRegistration issues an unused credential. Its first authenticated
// registration records the server identity; later connections must use that key.
func (s *Server) CreateRegistration(name string) (Registration, string, error) {
	issued, err := s.createRegistrations([]string{name})
	if err != nil {
		return Registration{}, "", err
	}
	return issued[0].Registration, issued[0].Token, nil
}

// createRegistrations commits the whole batch before returning any credentials.
// Empty names receive distinct random names; only token digests are persisted.
func (s *Server) createRegistrations(names []string) ([]issuedRegistration, error) {
	if len(names) < 1 || len(names) > maxRegistrationBatch {
		return nil, ErrInvalidRegistrationCount
	}
	for i, name := range names {
		names[i] = strings.TrimSpace(name)
		if names[i] != "" && !validRegistrationName(names[i]) {
			return nil, ErrInvalidRegistration
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("relay is closed")
	}
	if len(s.registrations)+len(names) > maxRegistrations {
		return nil, errors.New("relay registration store is full")
	}
	next := make(map[string]registrationRecord, len(s.registrations)+len(names))
	usedNames := make(map[string]bool, len(s.registrations)+len(names))
	for id, value := range s.registrations {
		next[id] = value
		usedNames[value.Name] = true
	}
	issued := make([]issuedRegistration, 0, len(names))
	for _, name := range names {
		if name == "" {
			for {
				var err error
				name, err = randomRegistrationName()
				if err != nil {
					return nil, err
				}
				if !usedNames[name] {
					break
				}
			}
		}
		usedNames[name] = true
		token := randomID()
		record := registrationRecord{Registration: Registration{ID: randomID(), Name: name, CreatedAt: time.Now().UTC()}, TokenHash: tokenDigest(token)}
		next[record.ID] = record
		issued = append(issued, issuedRegistration{record.Registration, token})
	}
	if err := s.saveRegistrations(next); err != nil {
		return nil, fmt.Errorf("save relay registration: %w", err)
	}
	s.registrations = next
	for _, result := range issued {
		s.tokenIndex[tokenDigest(result.Token)] = result.Registration.ID
	}
	return issued, nil
}

// Caller holds Server.mu and has verified the signed challenge. An explicit
// enrollment with an unused token can replace this identity's active credential.
// Persist both changes before disconnecting the previous host.
func (s *Server) claimRegistrationLocked(record registrationRecord, serverID string, metadata registrationMetadata) error {
	if record.ServerID != "" && record.ServerID != serverID {
		return ErrRegistrationExists
	}
	var replaced []registrationRecord
	for _, other := range s.registrations {
		if other.ID != record.ID && other.ServerID == serverID && other.active() {
			if !metadata.enrollment || record.ServerID != "" {
				return ErrRegistrationExists
			}
			// Owner-initiated replacement retires the credential without an
			// administrative revocation that would block future open enrollment.
			when := time.Now().UTC()
			other.UnregisteredAt = &when
			replaced = append(replaced, other)
		}
	}
	previous := record
	record.WebBlocked = record.WebBlocked || s.webBlockedLocked(serverID)
	record.applyMetadata(metadata, record.ServerID == "")
	record.ServerID = serverID
	if record == previous {
		return nil
	}
	next := make(map[string]registrationRecord, len(s.registrations))
	for id, value := range s.registrations {
		next[id] = value
	}
	for _, previous := range replaced {
		next[previous.ID] = previous
	}
	next[record.ID] = record
	if err := s.saveRegistrations(next); err != nil {
		return err
	}
	s.registrations = next
	for _, previous := range replaced {
		s.disconnectRegistrationLocked(previous)
	}
	return nil
}

// In open registration mode a signed identity creates its own credential.
// Retrying without a saved credential rotates the existing active record rather
// than adding another. A revoked identity cannot silently enroll again; an
// administrator must issue a replacement token for it.
func (s *Server) claimIdentityLocked(serverID string, metadata registrationMetadata) (registrationRecord, string, error) {
	var record registrationRecord
	var revoked bool
	for _, other := range s.registrations {
		if other.ServerID != serverID {
			continue
		}
		if other.RevokedAt != nil {
			revoked = true
		} else if other.active() {
			record = other
		}
	}
	now := time.Now()
	created := record.ID == ""
	if created {
		if revoked {
			return registrationRecord{}, "", ErrRegistrationRevoked
		}
		if len(s.registrations) >= maxRegistrations-automaticRegistrationReserve {
			return registrationRecord{}, "", errors.New("relay registration store is full")
		}
		if !s.enrollments.allowed(metadata.ip, now) {
			return registrationRecord{}, "", ErrRegistrationLimited
		}
		record.Registration = Registration{ID: randomID(), Name: "Spool-" + serverID[:12], ServerID: serverID, Automatic: true, CreatedAt: now.UTC()}
	}
	record.WebBlocked = s.webBlockedLocked(serverID)
	record.applyMetadata(metadata, record.TokenHash == "")
	token, oldHash := randomID(), record.TokenHash
	record.TokenHash = tokenDigest(token)
	next := make(map[string]registrationRecord, len(s.registrations)+1)
	for id, value := range s.registrations {
		next[id] = value
	}
	next[record.ID] = record
	if err := s.saveRegistrations(next); err != nil {
		return registrationRecord{}, "", err
	}
	s.registrations = next
	delete(s.tokenIndex, oldHash)
	s.tokenIndex[record.TokenHash] = record.ID
	if created {
		s.enrollments.add(metadata.ip, now)
	}
	return record, token, nil
}

// PurgeRegistrations permanently removes records and disconnects any that are
// connected. Unlike revocation it leaves no tombstone: where automatic
// registration is allowed the identity may enroll again, and a Web block held
// only by these records is lifted. Unknown IDs are ignored.
func (s *Server) PurgeRegistrations(ids []string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make(map[string]registrationRecord, len(s.registrations))
	for id, value := range s.registrations {
		next[id] = value
	}
	var removed []registrationRecord
	for _, id := range ids {
		if record, ok := next[id]; ok {
			delete(next, id)
			removed = append(removed, record)
		}
	}
	if len(removed) == 0 {
		return 0, nil
	}
	if err := s.saveRegistrations(next); err != nil {
		return 0, fmt.Errorf("save relay purge: %w", err)
	}
	s.registrations = next
	for _, record := range removed {
		delete(s.tokenIndex, record.TokenHash)
		s.disconnectRegistrationLocked(record)
	}
	return len(removed), nil
}

// RevokeRegistration persists a tombstone and cancels only this registration's
// control and data connections. Registration handshakes recheck under the same
// lock, so an in-progress handshake cannot reconnect with the revoked token.
func (s *Server) RevokeRegistration(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.registrations[id]
	if !ok {
		return ErrRegistrationNotFound
	}
	if record.RevokedAt != nil {
		return nil
	}
	when := time.Now().UTC()
	record.RevokedAt = &when
	next := make(map[string]registrationRecord, len(s.registrations))
	for key, value := range s.registrations {
		next[key] = value
	}
	next[id] = record
	if err := s.saveRegistrations(next); err != nil {
		return fmt.Errorf("save relay revocation: %w", err)
	}
	s.registrations = next
	s.disconnectRegistrationLocked(record)
	return nil
}

func (s *Server) disconnectRegistrationLocked(record registrationRecord) {
	if host := s.hosts[record.ServerID]; host != nil && host.registrationID == record.ID {
		s.disconnectHostLocked(host)
	}
}

// Self-unregistration disables this credential but permits a future enrollment.
// Administrative revocations remain intact and still block open enrollment.
func (s *Server) unregisterLocked(record registrationRecord) error {
	if !record.active() {
		return nil
	}
	when := time.Now().UTC()
	record.UnregisteredAt = &when
	next := make(map[string]registrationRecord, len(s.registrations))
	for id, value := range s.registrations {
		next[id] = value
	}
	next[record.ID] = record
	if err := s.saveRegistrations(next); err != nil {
		return err
	}
	s.registrations = next
	s.disconnectRegistrationLocked(record)
	return nil
}

func (s *Server) registrationStatuses(id string) []registrationStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := make([]registrationStatus, 0, len(s.registrations))
	for _, record := range s.registrations {
		if id != "" && record.ID != id {
			continue
		}
		status := registrationStatus{Registration: record.Registration}
		if record.active() && record.WebPublished && !record.WebBlocked {
			status.WebURL = s.webURLLocked(record.ServerID)
		}
		if host := s.hosts[record.ServerID]; host != nil && host.registrationID == record.ID && host.ctx.Err() == nil {
			status.Connected, status.Connections = true, len(host.pairs)
		}
		list = append(list, status)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].CreatedAt.Equal(list[j].CreatedAt) {
			return list[i].ID < list[j].ID
		}
		return list[i].CreatedAt.Before(list[j].CreatedAt)
	})
	return list
}

// Publishing intent survives temporary disconnects and relay restarts. Only a
// successfully authenticated host handshake may update this field.
func (s *Server) setWebPublishedLocked(id string, enabled bool) error {
	record := s.registrations[id]
	if record.ID == "" || record.WebPublished == enabled {
		return nil
	}
	next := make(map[string]registrationRecord, len(s.registrations))
	for key, value := range s.registrations {
		next[key] = value
	}
	record.WebPublished = enabled
	next[id] = record
	if err := s.saveRegistrations(next); err != nil {
		return err
	}
	s.registrations = next
	return nil
}

// Caller holds Server.mu. Web restrictions belong to the identity, including
// after self-unregistration or credential replacement.
func (s *Server) webBlockedLocked(serverID string) bool {
	for _, record := range s.registrations {
		if serverID != "" && record.ServerID == serverID && record.WebBlocked {
			return true
		}
	}
	return false
}

func (s *Server) setWebBlocked(id string, blocked bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.registrations[id]
	if !ok || !record.active() {
		return ErrRegistrationNotFound
	}
	next := make(map[string]registrationRecord, len(s.registrations))
	for key, value := range s.registrations {
		if key == id || record.ServerID != "" && value.ServerID == record.ServerID {
			value.WebBlocked = blocked
		}
		next[key] = value
	}
	if err := s.saveRegistrations(next); err != nil {
		return err
	}
	s.registrations = next
	// Reconnect to update the advertised URL and close existing Web streams.
	// The native registration and paired identities remain valid.
	s.disconnectRegistrationLocked(record)
	return nil
}
