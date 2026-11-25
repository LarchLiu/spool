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
CREATE TABLE IF NOT EXISTS federation_trust_inbox(id INTEGER PRIMARY KEY CHECK(id=1));
CREATE TABLE IF NOT EXISTS federation_trust_routes(
  request_id TEXT PRIMARY KEY REFERENCES federation_trust_requests(id) ON DELETE CASCADE,
  config TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS federation_trust_grants(
  peer_id TEXT PRIMARY KEY REFERENCES federation_peers(id) ON DELETE CASCADE,
  incoming INTEGER NOT NULL, outgoing INTEGER NOT NULL, managed INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS federation_trust_epochs(
  peer_id TEXT NOT NULL, outgoing INTEGER NOT NULL, request_id TEXT NOT NULL, revoked INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(peer_id,outgoing)
);
CREATE TABLE IF NOT EXISTS federation_trust_notifications(
  peer_id TEXT NOT NULL REFERENCES federation_peers(id), outgoing INTEGER NOT NULL, request_id TEXT NOT NULL,
  nonce TEXT NOT NULL, retry_at INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(peer_id,outgoing,request_id)
);
CREATE TABLE IF NOT EXISTS federation_trust_revocation_log(
  peer_id TEXT NOT NULL, outgoing INTEGER NOT NULL, request_id TEXT NOT NULL,
  initiator TEXT NOT NULL, recorded_at INTEGER NOT NULL,
  PRIMARY KEY(peer_id,outgoing,request_id)
);
CREATE TABLE IF NOT EXISTS federation_users(peer_id TEXT NOT NULL, subject TEXT NOT NULL, user_id TEXT NOT NULL UNIQUE REFERENCES users(id), PRIMARY KEY(peer_id,subject));
CREATE TABLE IF NOT EXISTS federation_sources(ref TEXT PRIMARY KEY, peer_id TEXT NOT NULL, session_hash TEXT NOT NULL, subject TEXT NOT NULL, expires_at INTEGER NOT NULL, notified INTEGER NOT NULL DEFAULT 0, revoked INTEGER NOT NULL DEFAULT 0);
CREATE UNIQUE INDEX IF NOT EXISTS federation_source_active ON federation_sources(peer_id,session_hash) WHERE revoked=0;
CREATE TABLE IF NOT EXISTS federation_tickets(hash TEXT PRIMARY KEY, source_ref TEXT NOT NULL REFERENCES federation_sources(ref) ON DELETE CASCADE, expires_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS federation_sessions(session_hash TEXT PRIMARY KEY REFERENCES auth_sessions(token_hash) ON DELETE CASCADE, peer_id TEXT NOT NULL, source_ref TEXT NOT NULL, checked_at INTEGER NOT NULL, retry_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS federation_relay_sessions(session_hash TEXT PRIMARY KEY REFERENCES auth_sessions(token_hash) ON DELETE CASCADE, client_key TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS federation_sessions_source ON federation_sessions(peer_id,source_ref);
CREATE TABLE IF NOT EXISTS federation_revocations(peer_id TEXT NOT NULL, source_ref TEXT NOT NULL, expires_at INTEGER NOT NULL, PRIMARY KEY(peer_id,source_ref));
CREATE TABLE IF NOT EXISTS federation_user_checks(user_id TEXT PRIMARY KEY REFERENCES users(id),retry_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS federation_user_revocations(peer_id TEXT NOT NULL REFERENCES federation_peers(id),subject TEXT NOT NULL,grant_id TEXT NOT NULL,nonce TEXT NOT NULL,retry_at INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(peer_id,subject));
CREATE TABLE IF NOT EXISTS federation_user_revocation_receipts(peer_id TEXT NOT NULL,nonce TEXT NOT NULL,PRIMARY KEY(peer_id,nonce));
CREATE TABLE IF NOT EXISTS federation_user_access_revocations(peer_id TEXT NOT NULL,subject TEXT NOT NULL,revoked_at INTEGER NOT NULL,PRIMARY KEY(peer_id,subject));
CREATE TABLE IF NOT EXISTS federation_agent_tasks(
 id TEXT PRIMARY KEY,peer_id TEXT NOT NULL,subject TEXT NOT NULL,origin_chat TEXT NOT NULL,origin_turn TEXT NOT NULL,
 request_id TEXT NOT NULL,fingerprint TEXT NOT NULL,chat_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
 state TEXT NOT NULL,body TEXT NOT NULL,created_at INTEGER NOT NULL,
 UNIQUE(peer_id,subject,request_id)
);
CREATE INDEX IF NOT EXISTS federation_agent_conversations ON federation_agent_tasks(peer_id,subject,origin_chat,chat_id);
CREATE TABLE IF NOT EXISTS federation_agent_calls(
 user_id TEXT NOT NULL REFERENCES users(id),origin_chat TEXT NOT NULL,peer_id TEXT NOT NULL,request_id TEXT NOT NULL,
 body TEXT NOT NULL,updated_at INTEGER NOT NULL,PRIMARY KEY(user_id,peer_id,request_id)
);
`

const (
	federationTicketTTL = time.Minute
	federationRecheck   = 10 * time.Minute
	federationOffline   = 24 * time.Hour
	federationRetry     = time.Minute
	loginSessionTTL     = 7 * 24 * time.Hour
)

// FederationClientModeTailscale identifies a client URL that needs a tailnet connection.
const FederationClientModeTailscale = "tailscale"
const FederationClientModeRelay = "relay"

var errFederationDenied = errors.New("federated login was not permitted")

type FederationProject struct {
	ID        string `json:"id"`
	Access    string `json:"access"`
	OwnAccess string `json:"own_access"`
}

type FederationPeer struct {
	ID         string `json:"id"`
	PublicKey  string `json:"public_key"`
	Name       string `json:"name"`
	Transport  string `json:"transport"`
	Address    string `json:"address,omitempty"`
	RelayCode  string `json:"relay_code,omitempty"`
	ClientURL  string `json:"client_url,omitempty"`
	ClientMode string `json:"client_mode,omitempty"`
	// The approved outgoing Relay route is independent of the control return route.
	ClientRelayCode string              `json:"client_relay_code,omitempty"`
	Accept          bool                `json:"accept"`
	AllUsers        bool                `json:"all_users"`
	Users           []string            `json:"users,omitempty"`
	Projects        []FederationProject `json:"projects,omitempty"`

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
	return (relay.Pairing{Version: 2, RelayURL: relayOrigin(settings.RelayURL), PublicKey: id.PublicKey(), AccessToken: id.FederationAccessToken()}).Encode()
}

type federationQuery interface {
	QueryRow(query string, args ...any) *sql.Row
}

const federationPeerSelect = `SELECT p.config,p.generation,coalesce(g.incoming,0),coalesce(g.outgoing,0)
	FROM federation_peers p LEFT JOIN federation_trust_grants g ON g.peer_id=p.id`

func scanFederationPeer(row interface{ Scan(...any) error }) (FederationPeer, error) {
	var peer FederationPeer
	var data string
	var incoming, outgoing bool
	if err := row.Scan(&data, &peer.generation, &incoming, &outgoing); err != nil {
		return peer, err
	}
	if err := json.Unmarshal([]byte(data), &peer); err != nil {
		return peer, err
	}
	if !incoming {
		peer.Accept, peer.Projects = false, nil
	}
	if !outgoing {
		peer.AllUsers, peer.Users = false, nil
	}
	return peer, nil
}

func federationPeer(q federationQuery, id string) (FederationPeer, error) {
	return scanFederationPeer(q.QueryRow(federationPeerSelect+` WHERE p.id=?`, id))
}

func (s *Store) FederationPeers() ([]FederationPeer, error) {
	rows, err := s.db.Query(federationPeerSelect + ` WHERE g.peer_id IS NOT NULL ORDER BY p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	peers := []FederationPeer{}
	for rows.Next() {
		peer, err := scanFederationPeer(rows)
		if err != nil {
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

// UpdateFederationPeer 只配置已批准的关系，不能创建关系或开启未批准的方向。
func (s *Store) UpdateFederationPeer(p FederationPeer) error {
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
	if p.ClientMode != "" && p.ClientMode != FederationClientModeTailscale && p.ClientMode != FederationClientModeRelay {
		return errors.New("unknown federation client mode")
	}
	if p.ClientRelayCode != "" || p.ClientMode == FederationClientModeRelay {
		route, err := relay.ParsePairing(p.ClientRelayCode)
		if err != nil || route.ID() != p.ID || p.ClientMode != FederationClientModeRelay || route.Invitation != "" || len(route.LAN) != 0 {
			return errors.New("federation client Relay route must match the paired server")
		}
		p.ClientRelayCode, err = route.Encode()
		if err != nil {
			return err
		}
		p.ClientURL = relay.Origin(p.ID) + "/"
	}
	p.ClientURL = strings.TrimSpace(p.ClientURL)
	if p.ClientURL != "" || p.ClientMode != "" {
		u, err := federationHTTPSURL(p.ClientURL)
		if p.ClientMode == FederationClientModeTailscale {
			if err != nil || (u.Path != "" && u.Path != "/") {
				return errors.New("a Tailscale client_url must be an HTTPS origin")
			}
		} else if err != nil || (u.Path != "" && u.Path != "/" && u.Path != relay.WebPath(p.ID)) {
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
	previous, err := federationPeer(tx, p.ID)
	if err != nil {
		return err
	}
	if err := validateFederationTrustPolicy(tx, p); err != nil {
		return err
	}
	if err := validateFederationUsers(tx, p.Users); err != nil {
		return err
	}
	if err := validateFederationProjects(tx, p.Projects); err != nil {
		return err
	}
	p.generation = randomToken()
	if err := updateFederationPeerConfig(tx, p); err != nil {
		return err
	}
	if !p.Accept {
		if err = disableManagedUsers(tx, `SELECT user_id FROM federation_users WHERE peer_id=?`, p.ID); err != nil {
			return err
		}
	}
	return s.commitFederationUsers(tx, p, previous)
}

func updateFederationPeerConfig(tx *sql.Tx, p FederationPeer) error {
	var previousName string
	if err := tx.QueryRow(`SELECT COALESCE(json_extract(config,'$.name'),'') FROM federation_peers WHERE id=?`, p.ID).Scan(&previousName); err != nil {
		return err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	updated, err := tx.Exec(`UPDATE federation_peers SET config=?,generation=? WHERE id=?`, data, p.generation, p.ID)
	if err != nil {
		return err
	}
	if n, err := updated.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return errors.New("Complete server trust approval before configuring this peer")
	}
	if previousName != p.Name {
		return bumpProfileRevision(tx)
	}
	return nil
}

func validateFederationUsers(tx *sql.Tx, users []string) error {
	for _, id := range users {
		var one int
		err := tx.QueryRow(`SELECT 1 FROM users WHERE id=? AND NOT EXISTS(SELECT 1 FROM federation_users WHERE user_id=?)`, id, id).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("unknown local user %q", id)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) commitFederationUsers(tx *sql.Tx, p, previous FederationPeer) error {
	if err := queueFederationUserRevocations(tx, p, previous); err != nil {
		return err
	}
	// 放行资格一旦撤销，之后重新放行也不能恢复旧会话。
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
	return s.notify(tx.Commit(), managedUsersChanged|changeFederationTargets)
}

func validateFederationProjects(tx *sql.Tx, defaults []FederationProject) error {
	seen := make(map[string]bool, len(defaults))
	for _, project := range defaults {
		if seen[project.ID] || (project.Access != "read-only" && project.Access != "control") || (project.OwnAccess != "read-only" && project.OwnAccess != "create") {
			return fmt.Errorf("invalid default access for project %q", project.ID)
		}
		seen[project.ID] = true
		var active int
		if err := tx.QueryRow(`SELECT 1 FROM projects WHERE id=? AND archived=0`, project.ID).Scan(&active); err != nil {
			return fmt.Errorf("unknown active project %q", project.ID)
		}
	}
	return nil
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

func (p federationSubject) valid() bool {
	return chatmodel.ValidID(p.ID) && len(p.Nickname) > 0 && len(p.Nickname) <= 100 && p.Avatar.Validate() == nil && federationSecret(p.SourceRef) && p.Remaining > 0
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

func (s *Store) federationLogin(peer FederationPeer, person federationSubject, checked time.Time, clientKey ...ed25519.PublicKey) (string, time.Time, error) {
	now := time.Now()
	if !person.valid() {
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
	if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM federation_user_access_revocations WHERE peer_id=? AND subject=? AND revoked_at>=?)`, peer.ID, person.ID, checked.UnixMilli()).Scan(&revoked); err != nil || revoked {
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
	if len(clientKey) != 0 {
		if len(clientKey) != 1 || len(clientKey[0]) != ed25519.PublicKeySize {
			return "", time.Time{}, errFederationDenied
		}
		if _, err = tx.Exec(`INSERT INTO federation_relay_sessions VALUES(?,?)`, hash, relay.DeviceFingerprint(clientKey[0])); err != nil {
			return "", time.Time{}, err
		}
	}
	if _, err = tx.Exec(`INSERT INTO federation_user_checks VALUES(?,?) ON CONFLICT(user_id) DO NOTHING`, user, checked.Add(federationRecheck).UnixMilli()); err != nil {
		return "", time.Time{}, err
	}
	return token, expires, s.notify(tx.Commit(), changeNavigation|changeProfiles)
}

// federationAgentIdentity resolves an already provisioned remote account whose
// profile is unchanged, without creating a session. Every agent operation
// redeems its own ticket; a login for each would rewrite the profile and
// notify all clients several times a second while a task is being watched.
// It reports false whenever the ordinary login has something to decide.
func (s *Store) federationAgentIdentity(peer FederationPeer, person federationSubject, checked time.Time) (authIdentity, bool) {
	var v authIdentity
	if !person.valid() {
		return v, false
	}
	current, err := federationPeer(s.db, peer.ID)
	if err != nil || current.generation != peer.generation || !current.Accept {
		return v, false
	}
	err = s.db.QueryRow(`SELECT u.id,u.username,u.nickname,u.created_at FROM federation_users f JOIN users u ON u.id=f.user_id
		LEFT JOIN user_avatars av ON av.user_id=u.id
		WHERE f.peer_id=? AND f.subject=? AND u.disabled=0 AND u.nickname=? AND COALESCE(av.style,'')=? AND COALESCE(av.seed,'')=?
		AND NOT EXISTS(SELECT 1 FROM federation_revocations WHERE peer_id=f.peer_id AND source_ref=?)
		AND NOT EXISTS(SELECT 1 FROM federation_user_access_revocations WHERE peer_id=f.peer_id AND subject=f.subject AND revoked_at>=?)`,
		peer.ID, person.ID, person.Nickname, person.Avatar.Style, person.Avatar.Seed, person.SourceRef, checked.UnixMilli()).Scan(&v.User.ID, &v.User.Username, &v.User.Nickname, &v.User.CreatedAt)
	if err != nil {
		return authIdentity{}, false
	}
	v.User.Avatar = person.Avatar
	v.User.SourceServerID, v.User.SourceServerName, v.User.SourceUserID = peer.ID, current.Name, person.ID
	return v, true
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
