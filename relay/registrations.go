package relay

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	ErrInvalidRegistration  = errors.New("a registration name of 1–128 bytes without control characters is required")
	ErrRegistrationExists   = errors.New("this server already has an active registration")
	ErrRegistrationNotFound = errors.New("registration not found")
	ErrRegistrationRevoked  = errors.New("server registration was revoked")
)

// Registration contains public administrative metadata. Tokens are returned
// once at creation; only their SHA-256 digests are persisted on the relay.
type Registration struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	ServerID  string     `json:"server_id,omitempty"`
	Automatic bool       `json:"automatic,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

type registrationRecord struct {
	Registration
	TokenHash string `json:"token_hash"`
}

type registrationStatus struct {
	Registration
	Connected   bool `json:"connected"`
	Connections int  `json:"connections"`
}

const maxRegistryBytes = 8 << 20
const maxRegistrations = 10000

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
	if len(data) > maxRegistryBytes || json.Unmarshal(data, &saved) != nil || saved.Version != 1 || len(saved.Registrations) > maxRegistrations {
		return errors.New("invalid relay registration store")
	}
	active := make(map[string]bool)
	for _, record := range saved.Registrations {
		digest, err := hex.DecodeString(record.TokenHash)
		if !validToken(record.ID) || !validRegistrationName(record.Name) || (record.ServerID != "" && !validServerID(record.ServerID)) || record.CreatedAt.IsZero() ||
			err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != record.TokenHash || s.registrations[record.ID].ID != "" || s.tokenIndex[record.TokenHash] != "" {
			return errors.New("invalid relay registration record")
		}
		if record.RevokedAt == nil && record.ServerID != "" {
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
	}{1, list}, "", "  ")
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
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || !validToken(token) {
		return registrationRecord{}, false
	}
	record, ok := s.registrations[s.tokenIndex[tokenDigest(token)]]
	return record, ok && record.RevokedAt == nil
}

// CreateRegistration issues an unused credential. Its first authenticated
// registration records the server identity; later connections must use that key.
func (s *Server) CreateRegistration(name string) (Registration, string, error) {
	name = strings.TrimSpace(name)
	if !validRegistrationName(name) {
		return Registration{}, "", ErrInvalidRegistration
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Registration{}, "", errors.New("relay is closed")
	}
	if len(s.registrations) >= maxRegistrations {
		return Registration{}, "", errors.New("relay registration store is full")
	}
	token := randomID()
	record := registrationRecord{Registration: Registration{ID: randomID(), Name: name, CreatedAt: time.Now().UTC()}, TokenHash: tokenDigest(token)}
	next := make(map[string]registrationRecord, len(s.registrations)+1)
	for id, value := range s.registrations {
		next[id] = value
	}
	next[record.ID] = record
	if err := s.saveRegistrations(next); err != nil {
		return Registration{}, "", fmt.Errorf("save relay registration: %w", err)
	}
	s.registrations = next
	s.tokenIndex[record.TokenHash] = record.ID
	return record.Registration, token, nil
}

// Caller holds Server.mu and has verified the signed challenge. Persist the
// first identity before making the host available, including after a restart.
func (s *Server) claimRegistrationLocked(record registrationRecord, serverID string) error {
	if record.ServerID == serverID {
		return nil
	}
	if record.ServerID != "" {
		return ErrRegistrationExists
	}
	for _, other := range s.registrations {
		if other.ServerID == serverID && other.RevokedAt == nil {
			return ErrRegistrationExists
		}
	}
	record.ServerID = serverID
	next := make(map[string]registrationRecord, len(s.registrations))
	for id, value := range s.registrations {
		next[id] = value
	}
	next[record.ID] = record
	if err := s.saveRegistrations(next); err != nil {
		return err
	}
	s.registrations = next
	return nil
}

// In open registration mode a signed identity creates its own credential.
// Retrying without a saved credential rotates the existing active record rather
// than adding another. A revoked identity cannot silently enroll again; an
// administrator must issue a replacement token for it.
func (s *Server) claimIdentityLocked(serverID string) (registrationRecord, string, error) {
	var record registrationRecord
	var revoked bool
	for _, other := range s.registrations {
		if other.ServerID != serverID {
			continue
		}
		if other.RevokedAt != nil {
			revoked = true
		} else {
			record = other
		}
	}
	if record.ID == "" {
		if revoked {
			return registrationRecord{}, "", ErrRegistrationRevoked
		}
		if len(s.registrations) >= maxRegistrations {
			return registrationRecord{}, "", errors.New("relay registration store is full")
		}
		record.Registration = Registration{ID: randomID(), Name: "Spool-" + serverID[:12], ServerID: serverID, Automatic: true, CreatedAt: time.Now().UTC()}
	}
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
	return record, token, nil
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
	if host := s.hosts[record.ServerID]; host != nil && host.registrationID == id {
		delete(s.hosts, host.id)
		host.cancel()
		for _, pair := range host.pairs {
			pair.cancel()
		}
		host.control.CloseNow()
	}
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
