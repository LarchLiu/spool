package workspace

import (
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	chatmodel "spool/internal/model"
	"spool/relay"
)

const federationSchema = `
CREATE TABLE IF NOT EXISTS federation_peers(id TEXT PRIMARY KEY, config TEXT NOT NULL, generation TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS federation_trust_requests(
  id TEXT PRIMARY KEY, peer_id TEXT NOT NULL, peer_name TEXT NOT NULL, outgoing INTEGER NOT NULL,
  expires_at INTEGER NOT NULL, request TEXT NOT NULL, decision TEXT NOT NULL DEFAULT '', receipt TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS federation_trust_peer ON federation_trust_requests(peer_id,outgoing,expires_at);
CREATE TABLE IF NOT EXISTS federation_users(peer_id TEXT NOT NULL, subject TEXT NOT NULL, user_id TEXT NOT NULL UNIQUE REFERENCES users(id), PRIMARY KEY(peer_id,subject));
CREATE TABLE IF NOT EXISTS federation_sources(ref TEXT PRIMARY KEY, peer_id TEXT NOT NULL, session_hash TEXT NOT NULL, subject TEXT NOT NULL, expires_at INTEGER NOT NULL, notified INTEGER NOT NULL DEFAULT 0, revoked INTEGER NOT NULL DEFAULT 0);
CREATE UNIQUE INDEX IF NOT EXISTS federation_source_active ON federation_sources(peer_id,session_hash) WHERE revoked=0;
CREATE TABLE IF NOT EXISTS federation_tickets(hash TEXT PRIMARY KEY, source_ref TEXT NOT NULL REFERENCES federation_sources(ref) ON DELETE CASCADE, expires_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS federation_sessions(session_hash TEXT PRIMARY KEY REFERENCES auth_sessions(token_hash) ON DELETE CASCADE, peer_id TEXT NOT NULL, source_ref TEXT NOT NULL, checked_at INTEGER NOT NULL, retry_at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS federation_sessions_source ON federation_sessions(peer_id,source_ref);
CREATE TABLE IF NOT EXISTS federation_revocations(peer_id TEXT NOT NULL, source_ref TEXT NOT NULL, expires_at INTEGER NOT NULL, PRIMARY KEY(peer_id,source_ref));
`

const (
	federationTicketTTL = time.Minute
	federationRecheck   = 10 * time.Minute
	federationOffline   = 24 * time.Hour
	federationRetry     = time.Minute
	loginSessionTTL     = 7 * 24 * time.Hour
)

var errFederationDenied = errors.New("federated login was not permitted")

type FederationProject struct {
	ID        string `json:"id"`
	Access    string `json:"access"`
	OwnAccess string `json:"own_access"`
}

type FederationPeer struct {
	ID        string              `json:"id"`
	PublicKey string              `json:"public_key"`
	Name      string              `json:"name"`
	Transport string              `json:"transport"`
	Address   string              `json:"address,omitempty"`
	RelayCode string              `json:"relay_code,omitempty"`
	ClientURL string              `json:"client_url,omitempty"`
	Accept    bool                `json:"accept"`
	AllUsers  bool                `json:"all_users"`
	Users     []string            `json:"users,omitempty"`
	Projects  []FederationProject `json:"projects,omitempty"`

	generation string
}

func (s *Store) federationKey() (*relay.Identity, error) {
	return relay.LoadIdentity(filepath.Join(s.Root(), "relay"))
}

func (s *Store) federationIdentityIfConfigured() (string, error) {
	var configured bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM federation_peers)`).Scan(&configured); err != nil || !configured {
		return "", err
	}
	id, err := s.federationKey()
	if err != nil {
		return "", err
	}
	return id.ID(), nil
}

func (s *Store) FederationIdentity() (FederationPeer, error) {
	settings, err := s.Settings()
	if err != nil {
		return FederationPeer{}, err
	}
	id, err := s.federationKey()
	if err != nil {
		return FederationPeer{}, err
	}
	return FederationPeer{ID: id.ID(), PublicKey: base64.RawURLEncoding.EncodeToString(id.PublicKey()), Name: settings.ServerName}, nil
}

// FederationRelayRoute exports a tunnel route, not a device invitation.
// Share it privately with administrators of servers paired to this server.
func (s *Store) FederationRelayRoute() (string, error) {
	settings, err := s.Settings()
	if err != nil {
		return "", err
	}
	if !settings.RelayEnabled {
		return "", errors.New("enable Relay before exporting a federation route")
	}
	id, err := s.federationKey()
	if err != nil {
		return "", err
	}
	return (relay.Pairing{Version: 2, RelayURL: relayOrigin(settings.RelayURL), PublicKey: id.PublicKey(), AccessToken: id.AccessToken}).Encode()
}

type federationQuery interface {
	QueryRow(query string, args ...any) *sql.Row
}

func federationPeer(q federationQuery, id string) (FederationPeer, error) {
	var peer FederationPeer
	var data string
	if err := q.QueryRow(`SELECT config,generation FROM federation_peers WHERE id=?`, id).Scan(&data, &peer.generation); err != nil {
		return peer, err
	}
	err := json.Unmarshal([]byte(data), &peer)
	return peer, err
}

func (s *Store) FederationPeers() ([]FederationPeer, error) {
	rows, err := s.db.Query(`SELECT config,generation FROM federation_peers ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	peers := []FederationPeer{}
	for rows.Next() {
		var peer FederationPeer
		var data string
		if err := rows.Scan(&data, &peer.generation); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(data), &peer); err != nil {
			return nil, err
		}
		peers = append(peers, peer)
	}
	return peers, rows.Err()
}

func (p FederationPeer) key() (ed25519.PublicKey, bool) {
	key, err := base64.RawURLEncoding.DecodeString(p.PublicKey)
	return ed25519.PublicKey(key), err == nil && len(key) == ed25519.PublicKeySize && relay.ServerID(key) == p.ID
}

func (p FederationPeer) allows(user string) bool {
	if p.AllUsers {
		return true
	}
	for _, id := range p.Users {
		if id == user {
			return true
		}
	}
	return false
}

func federationHTTPSURL(address string) (*url.URL, error) {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || strings.ContainsAny(address, "#\\\r\n\t ") {
		return nil, errors.New("invalid HTTPS route")
	}
	if port := u.Port(); port != "" {
		if n, err := strconv.ParseUint(port, 10, 16); err != nil || n == 0 {
			return nil, errors.New("invalid HTTPS port")
		}
	}
	return u, nil
}

func (s *Store) SaveFederationPeer(p FederationPeer) error {
	if _, ok := p.key(); !ok {
		return errors.New("invalid federation public key or server ID")
	}
	self, err := s.federationKey()
	if err != nil {
		return err
	}
	if p.ID == self.ID() {
		return errors.New("cannot pair this server with itself")
	}
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" || len(p.Name) > 128 {
		return errors.New("federation peer name is required (128 byte maximum)")
	}
	p.ClientURL = strings.TrimSpace(p.ClientURL)
	if p.ClientURL != "" {
		u, err := federationHTTPSURL(p.ClientURL)
		if err != nil || (u.Path != "" && u.Path != "/" && u.Path != relay.WebPath(p.ID)) {
			return errors.New("client_url must be an HTTPS origin or this server's Relay Web URL")
		}
		p.ClientURL = strings.TrimRight(u.String(), "/") + "/"
	}
	switch p.Transport {
	case "":
		if p.Accept || p.Address != "" || p.RelayCode != "" {
			return errors.New("accepting federated logins requires a route to the source server")
		}
	case "https", "tailscale":
		u, err := federationHTTPSURL(p.Address)
		if err != nil || (u.Path != "" && u.Path != "/") || p.RelayCode != "" {
			return errors.New("federation address must be an HTTPS origin")
		}
		p.Address = strings.TrimSuffix(p.Address, "/")
	case "relay":
		route, err := relay.ParsePairing(p.RelayCode)
		if err != nil || route.ID() != p.ID || p.Address != "" {
			return errors.New("federation Relay route must match the paired server")
		}
		// A server route never needs or preserves a device-pairing invitation.
		route.Invitation, route.ExpiresAt, route.LAN = "", 0, nil
		p.RelayCode, err = route.Encode()
		if err != nil {
			return err
		}
	default:
		return errors.New("unknown federation transport")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range p.Users {
		var one int
		if err = tx.QueryRow(`SELECT 1 FROM users WHERE id=? AND NOT EXISTS(SELECT 1 FROM federation_users WHERE user_id=?)`, id, id).Scan(&one); err != nil {
			return fmt.Errorf("unknown local user %q", id)
		}
	}
	projects := make(map[string]bool)
	for _, project := range p.Projects {
		if projects[project.ID] || (project.Access != "read-only" && project.Access != "control") || (project.OwnAccess != "read-only" && project.OwnAccess != "create") {
			return fmt.Errorf("invalid default access for project %q", project.ID)
		}
		projects[project.ID] = true
		var one int
		if err = tx.QueryRow(`SELECT 1 FROM projects WHERE id=? AND archived=0`, project.ID).Scan(&one); err != nil {
			return fmt.Errorf("unknown active project %q", project.ID)
		}
	}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO federation_peers VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET config=excluded.config,generation=excluded.generation`, p.ID, data, randomToken()); err != nil {
		return err
	}
	if !p.Accept {
		if _, err = tx.Exec(`DELETE FROM auth_sessions WHERE token_hash IN (SELECT session_hash FROM federation_sessions WHERE peer_id=?)`, p.ID); err != nil {
			return err
		}
	}
	// Losing outbound eligibility permanently invalidates existing references;
	// granting it again creates a new reference rather than restoring sessions.
	rows, err := tx.Query(`SELECT ref,subject FROM federation_sources WHERE peer_id=? AND revoked=0`, p.ID)
	if err != nil {
		return err
	}
	var revoke []string
	for rows.Next() {
		var ref, user string
		if err = rows.Scan(&ref, &user); err != nil {
			rows.Close()
			return err
		}
		if !p.allows(user) {
			revoke = append(revoke, ref)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, ref := range revoke {
		if _, err = tx.Exec(`UPDATE federation_sources SET revoked=1 WHERE ref=?`, ref); err != nil {
			return err
		}
	}
	return s.notify(tx.Commit(), changeAuth)
}

func (s *Store) federationIssue(id authIdentity, target string) (string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	peer, err := federationPeer(tx, target)
	if err != nil || !peer.allows(id.User.ID) {
		return "", errFederationDenied
	}
	now := time.Now()
	var expires int64
	if err = tx.QueryRow(`SELECT a.expires_at FROM auth_sessions a JOIN users u ON u.id=a.user_id WHERE a.token_hash=? AND a.user_id=? AND a.expires_at>? AND u.disabled=0 AND NOT EXISTS(SELECT 1 FROM federation_users WHERE user_id=u.id)`, id.TokenHash, id.User.ID, now.UnixMilli()).Scan(&expires); err != nil {
		return "", errFederationDenied
	}
	if _, err = tx.Exec(`DELETE FROM federation_tickets WHERE expires_at<=?`, now.UnixMilli()); err != nil {
		return "", err
	}
	ref := randomToken()
	if _, err = tx.Exec(`INSERT INTO federation_sources(ref,peer_id,session_hash,subject,expires_at) VALUES(?,?,?,?,?) ON CONFLICT(peer_id,session_hash) WHERE revoked=0 DO NOTHING`, ref, target, id.TokenHash, id.User.ID, expires); err != nil {
		return "", err
	}
	if err = tx.QueryRow(`SELECT ref FROM federation_sources WHERE peer_id=? AND session_hash=? AND revoked=0`, target, id.TokenHash).Scan(&ref); err != nil {
		return "", err
	}
	secret := randomToken()
	if _, err = tx.Exec(`INSERT INTO federation_tickets VALUES(?,?,?)`, tokenHash(secret), ref, min(expires, now.Add(federationTicketTTL).UnixMilli())); err != nil {
		return "", err
	}
	return secret, tx.Commit()
}

type federationSubject struct {
	ID        string `json:"subject"`
	Nickname  string `json:"nickname"`
	Avatar    Avatar `json:"avatar"`
	SourceRef string `json:"source_session"`
	Remaining int64  `json:"session_remaining_millis"`
}

// Verify and consume in the same transaction. A revoked or expired local
// session cannot be resurrected by a previously issued ticket.
func (s *Store) federationVerify(peer FederationPeer, value string, consume bool) (federationSubject, error) {
	var subject federationSubject
	tx, err := s.db.Begin()
	if err != nil {
		return subject, err
	}
	defer tx.Rollback()
	current, err := federationPeer(tx, peer.ID)
	if err != nil || current.generation != peer.generation {
		return subject, errFederationDenied
	}
	condition := `f.ref=?`
	if consume {
		condition = `f.ref=(SELECT source_ref FROM federation_tickets WHERE hash=? AND expires_at>?)`
		value = tokenHash(value)
	}
	now := time.Now().UnixMilli()
	args := []any{DefaultAvatar().Style, peer.ID, now, value}
	if consume {
		args = append(args, now)
	}
	var expires int64
	err = tx.QueryRow(`SELECT u.id,u.nickname,COALESCE(v.style,?),COALESCE(v.seed,''),f.ref,a.expires_at
		FROM federation_sources f JOIN auth_sessions a ON a.token_hash=f.session_hash JOIN users u ON u.id=a.user_id
		LEFT JOIN user_avatars v ON v.user_id=u.id
		WHERE f.peer_id=? AND f.revoked=0 AND a.expires_at>? AND u.disabled=0 AND `+condition, args...).Scan(&subject.ID, &subject.Nickname, &subject.Avatar.Style, &subject.Avatar.Seed, &subject.SourceRef, &expires)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !current.allows(subject.ID)) {
		return federationSubject{}, errFederationDenied
	}
	if err != nil {
		return federationSubject{}, err
	}
	if consume {
		if _, err = tx.Exec(`DELETE FROM federation_tickets WHERE hash=?`, value); err != nil {
			return federationSubject{}, err
		}
	}
	subject.Remaining = expires - now
	return subject, tx.Commit()
}

func (s *Store) federationLogin(peer FederationPeer, person federationSubject, checked time.Time) (string, time.Time, error) {
	now := time.Now()
	if !chatmodel.ValidID(person.ID) || len(person.Nickname) == 0 || len(person.Nickname) > 100 || person.Avatar.Validate() != nil || !federationSecret(person.SourceRef) || person.Remaining <= 0 {
		return "", time.Time{}, errFederationDenied
	}
	expires := checked.Add(time.Duration(min(person.Remaining, loginSessionTTL.Milliseconds())) * time.Millisecond)
	if !expires.After(now) {
		return "", time.Time{}, errFederationDenied
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", time.Time{}, err
	}
	defer tx.Rollback()
	current, err := federationPeer(tx, peer.ID)
	if err != nil || current.generation != peer.generation || !current.Accept {
		return "", time.Time{}, errFederationDenied
	}
	var revoked bool
	if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM federation_revocations WHERE peer_id=? AND source_ref=?)`, peer.ID, person.SourceRef).Scan(&revoked); err != nil || revoked {
		return "", time.Time{}, errFederationDenied
	}
	var user string
	var disabled bool
	err = tx.QueryRow(`SELECT u.id,u.disabled FROM federation_users f JOIN users u ON u.id=f.user_id WHERE f.peer_id=? AND f.subject=?`, peer.ID, person.ID).Scan(&user, &disabled)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		user = chatmodel.NewID()
		// ':' is deliberately outside the local namespace. The account has no
		// password and is independently excluded from the password login query.
		if _, err = tx.Exec(`INSERT INTO users VALUES(?,?,?,?,0,?)`, user, "external:"+user, person.Nickname, "", now.UnixMilli()); err != nil {
			return "", time.Time{}, err
		}
		if _, err = tx.Exec(`INSERT INTO federation_users VALUES(?,?,?)`, peer.ID, person.ID, user); err != nil {
			return "", time.Time{}, err
		}
		for _, project := range current.Projects {
			result, err := tx.Exec(`INSERT INTO project_members SELECT id,? FROM projects WHERE id=? AND archived=0`, user, project.ID)
			if err != nil {
				return "", time.Time{}, err
			}
			added, err := result.RowsAffected()
			if err != nil {
				return "", time.Time{}, err
			}
			if added == 0 {
				continue
			}
			if _, err = tx.Exec(`INSERT INTO project_member_access VALUES(?,?,?)`, project.ID, user, project.Access); err != nil {
				return "", time.Time{}, err
			}
			if _, err = tx.Exec(`INSERT INTO project_member_own_access VALUES(?,?,?)`, project.ID, user, project.OwnAccess); err != nil {
				return "", time.Time{}, err
			}
		}
	case err != nil:
		return "", time.Time{}, err
	case disabled:
		return "", time.Time{}, errFederationDenied
	default:
		if _, err = tx.Exec(`UPDATE users SET nickname=? WHERE id=?`, person.Nickname, user); err != nil {
			return "", time.Time{}, err
		}
	}
	if _, err = tx.Exec(`INSERT INTO user_avatars VALUES(?,?,?) ON CONFLICT(user_id) DO UPDATE SET style=excluded.style,seed=excluded.seed`, user, person.Avatar.Style, person.Avatar.Seed); err != nil {
		return "", time.Time{}, err
	}
	if err = bumpProfileRevision(tx); err != nil {
		return "", time.Time{}, err
	}
	token := randomToken()
	hash := tokenHash(token)
	if _, err = tx.Exec(`INSERT INTO auth_sessions VALUES(?,?,?,?)`, hash, user, randomToken(), expires.UnixMilli()); err != nil {
		return "", time.Time{}, err
	}
	if _, err = tx.Exec(`INSERT INTO federation_sessions VALUES(?,?,?,?,?)`, hash, peer.ID, person.SourceRef, checked.UnixMilli(), checked.Add(federationRecheck).UnixMilli()); err != nil {
		return "", time.Time{}, err
	}
	return token, expires, s.notify(tx.Commit(), changeNavigation|changeProfiles)
}

func (s *Store) federationRevoke(peer FederationPeer, ref string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := federationPeer(tx, peer.ID)
	if err != nil || !current.Accept || current.generation != peer.generation {
		return errFederationDenied
	}
	if _, err = tx.Exec(`INSERT OR IGNORE INTO federation_revocations VALUES(?,?,?)`, peer.ID, ref, time.Now().Add(loginSessionTTL).UnixMilli()); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM auth_sessions WHERE token_hash IN (SELECT session_hash FROM federation_sessions WHERE peer_id=? AND source_ref=?)`, peer.ID, ref); err != nil {
		return err
	}
	return s.notify(tx.Commit(), changeAuth|changePresence)
}
