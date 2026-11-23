package workspace

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"spool/internal/tailnet"
	"spool/relay"
)

const FederationTrustPath = "/api/federation/trust"

const (
	trustProbe  = "probe"
	trustSubmit = "submit"
	trustPoll   = "poll"

	federationTrustPendingLimit     = 256
	federationTrustInboxRecordLimit = 1024
)

var (
	errFederationTrustClosed = errors.New("The destination is not accepting server trust applications")
	errFederationTrustRoute  = errors.New("Could not reach the destination’s server trust endpoint")
	errFederationTrustPeer   = errors.New("The server identity changed or could not be verified")
	errFederationTrustQueue  = errors.New("The destination’s server trust inbox is full")
)

type FederationTrustPreview struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Accepting bool   `json:"accepting"`
}

type trustNetworkCall struct {
	Action string          `json:"action"`
	Nonce  string          `json:"nonce"`
	Code   string          `json:"code,omitempty"`
	Auth   json.RawMessage `json:"auth,omitempty"`
}

type trustNetworkResult struct {
	Peer    FederationTrustIdentity `json:"peer"`
	Receipt string                  `json:"receipt,omitempty"`
	Status  string                  `json:"status"`
}

func (s *Store) SetFederationTrustInbox(open bool) error {
	query := `DELETE FROM federation_trust_inbox WHERE id=1`
	if open {
		query = `INSERT OR IGNORE INTO federation_trust_inbox VALUES(1)`
	}
	_, err := s.db.Exec(query)
	return err
}

func federationTrustInbox(q federationQuery) (bool, error) {
	var open bool
	err := q.QueryRow(`SELECT EXISTS(SELECT 1 FROM federation_trust_inbox WHERE id=1)`).Scan(&open)
	return open, err
}

func federationTrustSavedRoute(q federationQuery, id string) (p FederationPeer, err error) {
	var data string
	if err = q.QueryRow(`SELECT config FROM federation_trust_routes WHERE request_id=?`, id).Scan(&data); err == nil {
		err = json.Unmarshal([]byte(data), &p)
	}
	return
}

func federationTrustRoute(transport, address string) (FederationPeer, error) {
	p := FederationPeer{Transport: transport}
	address = strings.TrimSpace(address)
	switch transport {
	case "https", "tailscale":
		u, err := federationHTTPSURL(address)
		if err != nil || (u.Path != "" && u.Path != "/") {
			return p, errors.New("Enter an HTTPS origin without a path")
		}
		p.Address = strings.TrimRight(address, "/")
	case "relay":
		route, err := relay.ParsePairing(address)
		if err != nil || route.Invitation != "" || len(route.LAN) != 0 {
			return p, errors.New("Enter the destination’s server trust Relay route, not a device invitation")
		}
		p.ID, p.PublicKey, p.RelayCode = route.ID(), base64.RawURLEncoding.EncodeToString(route.PublicKey), address
	default:
		return p, errors.New("Select a server connection method")
	}
	return p, nil
}

func (a *App) trustNetworkCall(ctx context.Context, route FederationPeer, action, code string) (FederationTrustIdentity, string, string, error) {
	var peer FederationTrustIdentity
	call := trustNetworkCall{Action: action, Nonce: randomToken(), Code: code}
	if code != "" {
		local, err := a.store.federationKey()
		if err != nil {
			return peer, "", "", err
		}
		call.Auth, err = (federationMessage{Version: federationVersion, From: local.ID(), To: route.ID, Action: "trust-" + action, Nonce: call.Nonce, SentAt: time.Now().UnixMilli(), Value: tokenHash(code)}).encode(local)
		if err != nil {
			return peer, "", "", err
		}
	}
	body, err := json.Marshal(call)
	if err != nil {
		return peer, "", "", err
	}
	ctx, cancel := context.WithTimeout(ctx, federationTimeout)
	defer cancel()
	data, err := a.trustDial(ctx, route, FederationTrustPath, body)
	if err != nil {
		// 网络错误可能含代理信息或 Relay 入网凭据，不返回底层错误。
		return peer, "", "", errFederationTrustRoute
	}
	var answer federationMessage
	if len(data) > federationMaxBody || json.Unmarshal(data, &answer) != nil || answer.Subject != nil || answer.Trust != nil || !answer.Response || !answer.fresh() || answer.Nonce != call.Nonce || answer.Action != "trust-"+action || answer.To != tokenHash(code) {
		return peer, "", "", errFederationTrustPeer
	}
	var result trustNetworkResult
	if json.Unmarshal([]byte(answer.Value), &result) != nil || !federationTrustName(result.Peer.Name) || !answer.signedBy(result.Peer.peer()) || (route.ID != "" && (route.ID != result.Peer.ID || route.PublicKey != result.Peer.PublicKey)) {
		return peer, "", "", errFederationTrustPeer
	}
	return result.Peer, result.Status, result.Receipt, nil
}

// federationTrustClient 允许管理命令在不启动 Web 或 Agent 的情况下拨号。
func (s *Service) federationTrustClient(ctx context.Context, transport string, call func(context.Context, *App) error) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("service is closed")
	}
	app, runtime := s.app, s.tailnet
	reuseOutbound := runtime != nil && runtime.loginOnly && runtime.dial != nil
	s.workers.Add(1)
	s.mu.Unlock()
	defer s.workers.Done()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	if app == nil {
		app = &App{store: s.Store}
		app.trustDial = app.dialFederationPath
		app.federationDial = app.dialFederation
		if transport == "tailscale" && reuseOutbound {
			app.federationTailnet.Store(runtime)
		} else if transport == "tailscale" {
			if runtime != nil && runtime.ctx.Err() == nil {
				return errFederationTrustRoute
			}
			settings, err := s.Store.Settings()
			if err != nil {
				return err
			}
			if !settings.TSNetEnabled {
				needed, err := s.Store.federationNeedsOutboundTailnet()
				if err != nil {
					return err
				}
				if !needed {
					return errors.New("Enable embedded Tailscale before connecting to a server")
				}
			}
			if !s.tailnetMu.TryLock() {
				return errFederationTrustRoute
			}
			defer s.tailnetMu.Unlock()
			if err := ctx.Err(); err != nil {
				return err
			}
			startup, stop := context.WithTimeout(ctx, 20*time.Second)
			defer stop()
			endpoint, err := tailnet.Start(startup, s.Store.Root(), tailnet.Options{Hostname: settings.TSNetHostname, Port: settings.TSNetPort}, nil)
			if err != nil {
				return errFederationTrustRoute
			}
			defer endpoint.Close()
			if err := endpoint.Connect(startup); err != nil {
				return errFederationTrustRoute
			}
			app.federationTailnet.Store(&tailnetRuntime{ctx: ctx, dial: endpoint.DialContext})
		}
	}
	return call(ctx, app)
}

func (s *Service) PreviewFederationTrust(ctx context.Context, transport, address string) (result FederationTrustPreview, err error) {
	route, err := federationTrustRoute(transport, address)
	if err != nil {
		return result, err
	}
	err = s.federationTrustClient(ctx, transport, func(ctx context.Context, a *App) error {
		var err error
		result, _, err = a.previewFederationTrust(ctx, route)
		return err
	})
	return
}

func (s *Service) SubmitFederationTrust(ctx context.Context, transport, address, expected string) error {
	if !federationTrustServerID(expected) {
		return errFederationTrustPeer
	}
	route, err := federationTrustRoute(transport, address)
	if err != nil {
		return err
	}
	return s.federationTrustClient(ctx, transport, func(ctx context.Context, a *App) error {
		preview, route, err := a.previewFederationTrust(ctx, route)
		if err != nil {
			return err
		}
		if preview.ID != expected {
			return errFederationTrustPeer
		}
		if !preview.Accepting {
			return errFederationTrustClosed
		}
		return a.submitFederationTrust(ctx, route)
	})
}

func (s *Service) CheckFederationTrust(ctx context.Context, requestID string) error {
	route, err := federationTrustSavedRoute(s.Store.db, requestID)
	if errors.Is(err, sql.ErrNoRows) {
		r, _, _, lookup := federationTrustRequest(s.Store.db, requestID)
		if lookup == nil && r.Outgoing && r.Status != federationTrustPending {
			return nil
		}
		return errFederationTrustRequest
	}
	if err != nil {
		return err
	}
	if route.Transport == "tailscale" {
		// Retry starting an outbound-only node if its background startup failed.
		s.EnsureFederationTailnet()
	}
	return s.federationTrustClient(ctx, route.Transport, func(ctx context.Context, a *App) error {
		return a.checkFederationTrust(ctx, requestID)
	})
}

func (a *App) previewFederationTrust(ctx context.Context, route FederationPeer) (FederationTrustPreview, FederationPeer, error) {
	peer, status, _, err := a.trustNetworkCall(ctx, route, trustProbe, "")
	if err != nil {
		return FederationTrustPreview{}, route, err
	}
	local, err := a.store.federationKey()
	if err != nil {
		return FederationTrustPreview{}, route, err
	}
	if local.ID() == peer.ID {
		return FederationTrustPreview{}, route, errors.New("Enter a valid ID for a different server")
	}
	route.ID, route.PublicKey, route.Name = peer.ID, peer.PublicKey, peer.Name
	return FederationTrustPreview{ID: peer.ID, Name: peer.Name, Accepting: status == "open"}, route, nil
}

func (a *App) submitFederationTrust(ctx context.Context, route FederationPeer) error {
	code, err := a.store.requestFederationTrust(route)
	if err != nil {
		return err
	}
	return a.deliverFederationTrust(ctx, route, trustSubmit, code)
}

func (a *App) checkFederationTrust(ctx context.Context, requestID string) error {
	r, code, _, err := federationTrustRequest(a.store.db, requestID)
	if err != nil || !r.Outgoing || r.Status != federationTrustPending || r.ExpiresAt <= time.Now().Unix() {
		return errFederationTrustRequest
	}
	route, err := federationTrustSavedRoute(a.store.db, requestID)
	if err != nil {
		return errFederationTrustRequest
	}
	if _, _, _, err := a.trustNetworkCall(ctx, route, trustProbe, ""); err != nil {
		return err
	}
	if err := a.deliverFederationTrust(ctx, route, trustPoll, code); errors.Is(err, errFederationTrustRequest) {
		return a.deliverFederationTrust(ctx, route, trustSubmit, code)
	} else {
		return err
	}
}

func (a *App) deliverFederationTrust(ctx context.Context, route FederationPeer, action, code string) error {
	_, status, receipt, err := a.trustNetworkCall(ctx, route, action, code)
	if err != nil {
		return err
	}
	switch status {
	case federationTrustApproved, federationTrustRejected:
		if receipt == "" {
			return errFederationTrustPeer
		}
		_, err := a.store.confirmFederationTrust(receipt)
		return err
	case federationTrustPending:
		return nil
	case "closed":
		return errFederationTrustClosed
	case "full":
		return errFederationTrustQueue
	default:
		return errFederationTrustRequest
	}
}

func (a *App) serveFederationTrust(w http.ResponseWriter, r *http.Request) {
	refuse := func() { apiError(w, http.StatusForbidden, "Server trust request refused") }
	if r.Header.Get("Origin") != "" || r.Header.Get("Content-Type") != "application/json" {
		refuse()
		return
	}
	var call trustNetworkCall
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, federationMaxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&call); err != nil || dec.Decode(new(any)) != io.EOF || !federationSecret(call.Nonce) {
		refuse()
		return
	}
	if call.Action != trustProbe && call.Action != trustSubmit && call.Action != trustPoll {
		refuse()
		return
	}
	client := a.loginClientIP(r)
	if client == "" {
		client = r.RemoteAddr
	}
	if !a.federationTrustLimiter.allow(client) {
		apiError(w, http.StatusTooManyRequests, "Too many server trust requests")
		return
	}
	local, peer, err := a.store.federationTrustIdentity()
	if err != nil {
		apiError(w, http.StatusServiceUnavailable, "Server trust endpoint unavailable")
		return
	}
	relayKey, viaRelay := relay.FederationTrustPeerKey(r, local.ID())
	status, receipt := "", ""
	if call.Action == trustProbe {
		if call.Code != "" || len(call.Auth) != 0 {
			refuse()
			return
		}
		open, openErr := federationTrustInbox(a.store.db)
		err = openErr
		status = "closed"
		if open {
			status = "open"
		}
	} else {
		m, parseErr := parseFederationTrustCode(call.Code, time.Now())
		var auth federationMessage
		if parseErr != nil || m.Decision != "" || m.RequestHash != "" || m.Target != local.ID() || json.Unmarshal(call.Auth, &auth) != nil ||
			!auth.signedBy(m.Sender.peer()) || auth.Response || auth.Active || auth.Subject != nil || auth.Trust != nil || auth.To != local.ID() || !auth.fresh() || auth.Nonce != call.Nonce || auth.Action != "trust-"+call.Action || auth.Value != tokenHash(m.code()) {
			refuse()
			return
		}
		if viaRelay {
			expected, _ := m.Sender.peer().key()
			if subtle.ConstantTimeCompare(relayKey, expected) != 1 {
				refuse()
				return
			}
		}
		status, receipt, err = a.store.federationTrustNetworkResult(m, call.Action == trustSubmit)
	}
	if err != nil {
		apiError(w, http.StatusServiceUnavailable, "Server trust endpoint unavailable")
		return
	}
	value, _ := json.Marshal(trustNetworkResult{Peer: peer, Receipt: receipt, Status: status})
	answer := federationMessage{Version: federationVersion, From: local.ID(), To: tokenHash(call.Code), Action: "trust-" + call.Action, Nonce: call.Nonce, SentAt: time.Now().UnixMilli(), Response: true, Value: string(value)}
	data, err := answer.encode(local)
	if err != nil {
		apiError(w, http.StatusServiceUnavailable, "Server trust endpoint unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

func (s *Store) federationTrustNetworkResult(m federationTrustMessage, submit bool) (string, string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	if m.Expires <= now {
		return "missing", "", nil
	}
	code := m.code()
	r, saved, receipt, err := federationTrustRequest(tx, m.ID)
	if err == nil {
		if r.Outgoing || saved != code {
			return "missing", "", nil
		}
		return r.Status, receipt, nil
	}
	if !errors.Is(err, sql.ErrNoRows) || !submit {
		if errors.Is(err, sql.ErrNoRows) {
			return "missing", "", nil
		}
		return "", "", err
	}
	open, err := federationTrustInbox(tx)
	if err != nil || !open {
		return "closed", "", err
	}
	if err := deleteExpiredFederationTrustRequests(tx, now); err != nil {
		return "", "", err
	}
	var total, pending int
	var duplicate bool
	if err := tx.QueryRow(`SELECT count(*),count(*) FILTER (WHERE decision=''),
		count(*) FILTER (WHERE peer_id=? AND decision='')>0 FROM federation_trust_requests WHERE outgoing=0 AND expires_at>?`, m.Sender.ID, now).
		Scan(&total, &pending, &duplicate); err != nil {
		return "", "", err
	}
	// A decision frees a pending slot in place. Reserve receipt storage at
	// admission so accepting a new request never evicts an unexpired decision.
	if pending >= federationTrustPendingLimit || total >= federationTrustInboxRecordLimit || duplicate {
		return "full", "", tx.Commit()
	}
	if _, err := tx.Exec(`INSERT INTO federation_trust_requests(id,peer_id,peer_name,outgoing,expires_at,request) VALUES(?,?,?,0,?,?)`, m.ID, m.Sender.ID, m.Sender.Name, m.Expires, code); err != nil {
		return "", "", err
	}
	return federationTrustPending, "", tx.Commit()
}
