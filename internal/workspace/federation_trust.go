package workspace

import (
	"crypto/ed25519"
	"database/sql"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"

	"spool/relay"
)

const (
	federationTrustPrefix = "spool-trust1."
	federationTrustTTL    = 7 * 24 * time.Hour
	// 允许服务器时钟有少量偏差。
	federationTrustClockSkew = 5 * time.Minute
	federationTrustPending   = "pending"
	federationTrustApproved  = "approved"
	federationTrustRejected  = "rejected"
	federationTrustCodeLimit = 4096
)

var (
	errFederationTrustCode    = errors.New("Invalid or expired server trust message")
	errFederationTrustRequest = errors.New("No matching server trust request")
	errFederationTrustDecided = errors.New("This server trust request already has a decision")
)

type FederationTrustIdentity struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	PublicKey string `json:"public_key"`
}

func (p FederationTrustIdentity) peer() FederationPeer {
	return FederationPeer{ID: p.ID, Name: p.Name, PublicKey: p.PublicKey}
}

type federationTrustMessage struct {
	ID          string                  `json:"id"`
	Sender      FederationTrustIdentity `json:"sender"`
	Target      string                  `json:"target"`
	Expires     int64                   `json:"expires_at"`
	Decision    string                  `json:"decision,omitempty"`
	RequestHash string                  `json:"request_hash,omitempty"`
	Signature   string                  `json:"signature,omitempty"`
}

func (m federationTrustMessage) bytes() []byte {
	data, _ := json.Marshal(m)
	return data
}

func (m federationTrustMessage) signingBytes() []byte {
	m.Signature = ""
	return append([]byte("Spool server trust v1\x00"), m.bytes()...)
}

func (m federationTrustMessage) code() string {
	return federationTrustPrefix + base64.RawURLEncoding.EncodeToString(m.bytes())
}

func (m federationTrustMessage) sign(local *relay.Identity) federationTrustMessage {
	m.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(local.PrivateKey(), m.signingBytes()))
	return m
}

func federationTrustServerID(id string) bool {
	if len(id) != 52 || id != strings.ToLower(id) {
		return false
	}
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(id))
	return err == nil && len(key) == 32 && strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(key)) == id
}

func federationTrustName(name string) bool {
	return name != "" && len(name) <= 128 && name == strings.TrimSpace(name) && strings.IndexFunc(name, unicode.IsControl) == -1
}

func parseFederationTrustCode(code string, now time.Time) (federationTrustMessage, error) {
	var m federationTrustMessage
	data, ok := strings.CutPrefix(strings.TrimSpace(code), federationTrustPrefix)
	if !ok || len(code) > federationTrustCodeLimit {
		return m, errFederationTrustCode
	}
	body, err := base64.RawURLEncoding.Strict().DecodeString(data)
	if err != nil || json.Unmarshal(body, &m) != nil || string(body) != string(m.bytes()) || !federationSecret(m.ID) || m.Target == m.Sender.ID || !federationTrustServerID(m.Target) ||
		!federationTrustName(m.Sender.Name) || m.Expires <= now.Unix() || m.Expires > now.Add(federationTrustTTL+federationTrustClockSkew).Unix() {
		return m, errFederationTrustCode
	}
	key, ok := m.Sender.peer().key()
	sig, err := base64.RawURLEncoding.Strict().DecodeString(m.Signature)
	if !ok || err != nil || !ed25519.Verify(key, m.signingBytes(), sig) {
		return m, errFederationTrustCode
	}
	return m, nil
}

type FederationTrustRequest struct {
	ID        string `json:"id"`
	PeerID    string `json:"peer_id"`
	PeerName  string `json:"peer_name,omitempty"`
	Outgoing  bool   `json:"outgoing"`
	ExpiresAt int64  `json:"expires_at"`
	Status    string `json:"status"`
	Network   bool   `json:"network,omitempty"`
}

type FederationTrustPeer struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Incoming bool     `json:"incoming"`
	Outgoing bool     `json:"outgoing"`
	Accept   bool     `json:"accept"`
	AllUsers bool     `json:"all_users,omitempty"`
	Users    []string `json:"users,omitempty"`
}

// SetFederationTrustUsers 修改已确认的出站授权，不重写连接凭据或入站策略。
func (s *Store) SetFederationTrustUsers(target string, allUsers bool, users []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var approved bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM federation_trust_grants WHERE peer_id=? AND outgoing=1)`, target).Scan(&approved); err != nil {
		return err
	}
	if !approved {
		return errors.New("Confirm the target server’s approval before allowing users")
	}
	if allUsers {
		users = nil
	}
	if err := validateFederationUsers(tx, users); err != nil {
		return err
	}
	p, err := federationPeer(tx, target)
	if err != nil {
		return err
	}
	p.AllUsers, p.Users = allUsers, users
	// 只更新人员字段；核验在事务中重新读取当前名单，不需要废弃其他用户的会话。
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE federation_peers SET config=? WHERE id=?`, data, target); err != nil {
		return err
	}
	return s.commitFederationUsers(tx, p)
}

// UpdateFederationTrustReturnRoute 配置已批准来源的回连线路，不修改其他登录策略。
func (s *Store) UpdateFederationTrustReturnRoute(target, transport, address string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var approved bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM federation_trust_grants WHERE peer_id=? AND incoming=1)`, target).Scan(&approved); err != nil {
		return err
	}
	if !approved {
		return errors.New("Approve an incoming server trust request before configuring its return route")
	}
	p, err := federationPeer(tx, target)
	if err != nil {
		return err
	}
	generation := p.generation
	if err := setFederationTrustReturnRoute(&p, transport, address); err != nil {
		return err
	}
	if p.generation == generation {
		return nil
	}
	if err := updateFederationPeerConfig(tx, p); err != nil {
		return err
	}
	return s.notify(tx.Commit(), changeAuth)
}

func setFederationTrustReturnRoute(p *FederationPeer, transport, address string) error {
	route, err := federationTrustRoute(transport, address)
	if err != nil {
		return err
	}
	if route.ID != "" && route.ID != p.ID {
		return errFederationTrustPeer
	}
	if !p.Accept || p.Transport != route.Transport || p.Address != route.Address || p.RelayCode != route.RelayCode {
		p.Transport, p.Address, p.RelayCode, p.Accept = route.Transport, route.Address, route.RelayCode, true
		p.generation = randomToken()
	}
	return nil
}

type FederationTrustSnapshot struct {
	ServerID  string                   `json:"server_id"`
	InboxOpen bool                     `json:"inbox_open"`
	Requests  []FederationTrustRequest `json:"requests"`
	Peers     []FederationTrustPeer    `json:"peers"`
}

func federationTrustRequest(q federationQuery, id string) (r FederationTrustRequest, request, receipt string, err error) {
	err = q.QueryRow(`SELECT id,peer_id,peer_name,outgoing,expires_at,decision,request,receipt FROM federation_trust_requests WHERE id=?`, id).
		Scan(&r.ID, &r.PeerID, &r.PeerName, &r.Outgoing, &r.ExpiresAt, &r.Status, &request, &receipt)
	if r.Status == "" {
		r.Status = federationTrustPending
	}
	return
}

func (s *Store) federationTrustIdentity() (*relay.Identity, FederationTrustIdentity, error) {
	settings, err := s.Settings()
	if err != nil {
		return nil, FederationTrustIdentity{}, err
	}
	id, err := s.federationKey()
	if err != nil {
		return nil, FederationTrustIdentity{}, err
	}
	name := strings.TrimSpace(settings.ServerName)
	if !federationTrustName(name) {
		return nil, FederationTrustIdentity{}, errors.New("Set a valid server name before requesting server trust")
	}
	return id, FederationTrustIdentity{ID: id.ID(), Name: name, PublicKey: base64.RawURLEncoding.EncodeToString(id.PublicKey())}, nil
}

func (s *Store) FederationTrustSnapshot() (FederationTrustSnapshot, error) {
	local, err := s.federationKey()
	if err != nil {
		return FederationTrustSnapshot{}, err
	}
	v := FederationTrustSnapshot{ServerID: local.ID(), Requests: []FederationTrustRequest{}, Peers: []FederationTrustPeer{}}
	tx, err := s.db.Begin()
	if err != nil {
		return v, err
	}
	defer tx.Rollback()
	if v.InboxOpen, err = federationTrustInbox(tx); err != nil {
		return v, err
	}
	rows, err := tx.Query(`SELECT r.id,r.peer_id,r.peer_name,r.outgoing,r.expires_at,r.decision,p.request_id IS NOT NULL
		FROM federation_trust_requests r LEFT JOIN federation_trust_routes p ON p.request_id=r.id WHERE r.expires_at>? ORDER BY r.expires_at DESC,r.id`, time.Now().Unix())
	if err != nil {
		return v, err
	}
	for rows.Next() {
		var r FederationTrustRequest
		if err := rows.Scan(&r.ID, &r.PeerID, &r.PeerName, &r.Outgoing, &r.ExpiresAt, &r.Status, &r.Network); err != nil {
			rows.Close()
			return v, err
		}
		if r.Status == "" {
			r.Status = federationTrustPending
		}
		v.Requests = append(v.Requests, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return v, err
	}
	rows, err = tx.Query(`SELECT p.id,json_extract(p.config,'$.name'),coalesce(g.incoming,0),coalesce(g.outgoing,0),
		coalesce(json_extract(p.config,'$.accept'),0),coalesce(json_extract(p.config,'$.all_users'),0),coalesce(json_extract(p.config,'$.users'),'[]')
		FROM federation_peers p LEFT JOIN federation_trust_grants g ON g.peer_id=p.id ORDER BY 2,p.id`)
	if err != nil {
		return v, err
	}
	defer rows.Close()
	for rows.Next() {
		var p FederationTrustPeer
		var users string
		if err := rows.Scan(&p.ID, &p.Name, &p.Incoming, &p.Outgoing, &p.Accept, &p.AllUsers, &users); err != nil {
			return v, err
		}
		if p.Outgoing {
			if err := json.Unmarshal([]byte(users), &p.Users); err != nil {
				return v, err
			}
		} else {
			p.AllUsers = false
		}
		v.Peers = append(v.Peers, p)
	}
	return v, rows.Err()
}

func (s *Store) requestFederationTrust(route FederationPeer) (string, error) {
	local, sender, err := s.federationTrustIdentity()
	if err != nil {
		return "", err
	}
	target := route.ID
	if !federationTrustServerID(target) || target == sender.ID {
		return "", errors.New("Enter a valid ID for a different server")
	}
	now := time.Now()
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM federation_trust_requests WHERE expires_at<=?`, now.Unix()); err != nil {
		return "", err
	}
	var id, code string
	err = tx.QueryRow(`SELECT id,request FROM federation_trust_requests WHERE peer_id=? AND outgoing=1 AND decision='' ORDER BY expires_at DESC LIMIT 1`, target).Scan(&id, &code)
	if errors.Is(err, sql.ErrNoRows) {
		m := (federationTrustMessage{ID: randomToken(), Sender: sender, Target: target, Expires: now.Add(federationTrustTTL).Unix()}).sign(local)
		id, code = m.ID, m.code()
		_, err = tx.Exec(`INSERT INTO federation_trust_requests(id,peer_id,peer_name,outgoing,expires_at,request) VALUES(?,?,'',1,?,?)`, id, target, m.Expires, code)
	}
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(route)
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(`INSERT INTO federation_trust_routes VALUES(?,?) ON CONFLICT(request_id) DO UPDATE SET config=excluded.config`, id, string(data)); err != nil {
		return "", err
	}
	if _, err := tx.Exec(`UPDATE federation_trust_requests SET peer_name=? WHERE id=?`, route.Name, id); err != nil {
		return "", err
	}
	return code, tx.Commit()
}

type FederationTrustApproval struct {
	Projects  []FederationProject `json:"projects"`
	Transport string              `json:"transport"`
	Address   string              `json:"address"`
}

func (s *Store) DecideFederationTrust(requestID string, approve bool, settings FederationTrustApproval) error {
	local, sender, err := s.federationTrustIdentity()
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, code, _, err := federationTrustRequest(tx, requestID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && r.Outgoing) {
		return errFederationTrustRequest
	}
	if err != nil {
		return err
	}
	m, err := parseFederationTrustCode(code, time.Now())
	if err != nil || m.Target != sender.ID {
		return errFederationTrustCode
	}
	decision := federationTrustRejected
	if approve {
		decision = federationTrustApproved
	}
	if r.Status != federationTrustPending {
		if r.Status == decision {
			return nil
		}
		return errFederationTrustDecided
	}
	answer := (federationTrustMessage{ID: m.ID, Sender: sender, Target: m.Sender.ID, Expires: m.Expires, Decision: decision, RequestHash: tokenHash(code)}).sign(local)
	if approve {
		if err := validateFederationProjects(tx, settings.Projects); err != nil {
			return err
		}
		identity := m.Sender.peer()
		if err := saveFederationTrustGrant(tx, identity, false); err != nil {
			return err
		}
		if settings.Projects != nil || settings.Transport != "" || settings.Address != "" {
			peer, err := federationPeer(tx, identity.ID)
			if err != nil {
				return err
			}
			generation := peer.generation
			if settings.Projects != nil {
				peer.Projects = settings.Projects
				peer.generation = randomToken()
			}
			if settings.Transport != "" || settings.Address != "" {
				if err := setFederationTrustReturnRoute(&peer, settings.Transport, settings.Address); err != nil {
					return err
				}
			}
			if peer.generation != generation {
				if err := updateFederationPeerConfig(tx, peer); err != nil {
					return err
				}
			}
		}
	}
	receipt := answer.code()
	if _, err := tx.Exec(`UPDATE federation_trust_requests SET decision=?,receipt=? WHERE id=?`, decision, receipt, r.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) confirmFederationTrust(code string) (FederationTrustRequest, error) {
	m, err := parseFederationTrustCode(code, time.Now())
	if err != nil || (m.Decision != federationTrustApproved && m.Decision != federationTrustRejected) {
		return FederationTrustRequest{}, errFederationTrustCode
	}
	local, err := s.federationKey()
	if err != nil {
		return FederationTrustRequest{}, err
	}
	if m.Target != local.ID() {
		return FederationTrustRequest{}, errFederationTrustCode
	}
	tx, err := s.db.Begin()
	if err != nil {
		return FederationTrustRequest{}, err
	}
	defer tx.Rollback()
	r, request, receipt, err := federationTrustRequest(tx, m.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return r, errFederationTrustRequest
	}
	if err != nil {
		return r, err
	}
	if !r.Outgoing || m.Sender.ID != r.PeerID || m.Expires != r.ExpiresAt || m.RequestHash != tokenHash(request) {
		return r, errFederationTrustRequest
	}
	code = m.code()
	if r.Status != federationTrustPending {
		if receipt == code {
			return r, nil
		}
		return r, errFederationTrustDecided
	}
	if m.Decision == federationTrustApproved {
		peer := m.Sender.peer()
		route, err := federationTrustSavedRoute(tx, m.ID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return r, err
		}
		if err == nil {
			peer.Transport, peer.Address, peer.RelayCode = route.Transport, route.Address, route.RelayCode
			if route.Transport == "https" || route.Transport == "tailscale" {
				peer.ClientURL = route.Address + "/"
				if route.Transport == "tailscale" {
					peer.ClientMode = FederationClientModeTailscale
				}
			}
		}
		if err := saveFederationTrustGrant(tx, peer, true); err != nil {
			return r, err
		}
	}
	if _, err := tx.Exec(`UPDATE federation_trust_requests SET peer_name=?,decision=?,receipt=? WHERE id=?`, m.Sender.Name, m.Decision, code, r.ID); err != nil {
		return r, err
	}
	if _, err := tx.Exec(`DELETE FROM federation_trust_routes WHERE request_id=?`, r.ID); err != nil {
		return r, err
	}
	r.Status, r.PeerName = m.Decision, m.Sender.Name
	return r, s.notify(tx.Commit(), changeFederationTargets)
}

// 新关系采用管理员预览后确认的线路；出站回执只能补齐已有关系的空线路，不覆盖权限。
func saveFederationTrustGrant(tx *sql.Tx, p FederationPeer, outgoing bool) error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	result, err := tx.Exec(`INSERT INTO federation_peers VALUES(?,?,?) ON CONFLICT(id) DO NOTHING`, p.ID, data, randomToken())
	if err != nil {
		return err
	}
	added, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if added == 0 && outgoing {
		current, err := federationPeer(tx, p.ID)
		if err != nil {
			return err
		}
		var changed bool
		if current.Transport == "" && current.Address == "" && current.RelayCode == "" && p.Transport != "" {
			current.Transport, current.Address, current.RelayCode = p.Transport, p.Address, p.RelayCode
			// In-flight control requests must not cross a change of route.
			current.generation = randomToken()
			changed = true
		}
		if current.ClientURL == "" && current.ClientMode == "" && p.ClientURL != "" {
			current.ClientURL, current.ClientMode = p.ClientURL, p.ClientMode
			changed = true
		}
		if changed {
			if err := updateFederationPeerConfig(tx, current); err != nil {
				return err
			}
		}
	}
	_, err = tx.Exec(`INSERT INTO federation_trust_grants(peer_id,incoming,outgoing,managed) VALUES(?,?,?,?)
		ON CONFLICT(peer_id) DO UPDATE SET incoming=max(incoming,excluded.incoming),outgoing=max(outgoing,excluded.outgoing)`,
		p.ID, !outgoing, outgoing, added > 0)
	return err
}

func validateFederationTrustPolicy(tx *sql.Tx, p FederationPeer) error {
	var incoming, outgoing, managed bool
	err := tx.QueryRow(`SELECT incoming,outgoing,managed FROM federation_trust_grants WHERE peer_id=?`, p.ID).Scan(&incoming, &outgoing, &managed)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !managed) {
		// 兼容此前由双方管理员手工保存的对端。
		return nil
	}
	if err != nil {
		return err
	}
	if p.Accept && !incoming {
		return errors.New("Approve an incoming server trust request before accepting logins from this server")
	}
	if (p.AllUsers || len(p.Users) > 0) && !outgoing {
		return errors.New("Confirm the target server’s approval before allowing users")
	}
	return nil
}
