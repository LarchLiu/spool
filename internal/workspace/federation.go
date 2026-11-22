package workspace

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"spool/relay"
)

const FederationControlPath = "/api/federation/control"

const (
	federationVersion = 1
	federationWindow  = 90 * time.Second
	federationTimeout = 8 * time.Second
	federationMaxBody = 8 << 10
)

const (
	federationRedeem = "redeem"
	federationCheck  = "check"
	federationCancel = "revoke"
)

type federationMessage struct {
	Version   int                `json:"version"`
	From      string             `json:"from"`
	To        string             `json:"to"`
	Action    string             `json:"action"`
	Nonce     string             `json:"nonce"`
	SentAt    int64              `json:"sent_at"`
	Value     string             `json:"value,omitempty"`
	Response  bool               `json:"response,omitempty"`
	Active    bool               `json:"active,omitempty"`
	Subject   *federationSubject `json:"subject,omitempty"`
	Signature string             `json:"signature"`
}

func federationSecret(s string) bool {
	if len(s) != 43 {
		return false
	}
	secret, err := base64.RawURLEncoding.Strict().DecodeString(s)
	return err == nil && len(secret) == 32
}

func (m federationMessage) signingBytes() []byte {
	m.Signature = ""
	body, _ := json.Marshal(m)
	return append([]byte("Spool federation control v1\x00"), body...)
}

func (m federationMessage) encode(id *relay.Identity) ([]byte, error) {
	m.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(id.PrivateKey(), m.signingBytes()))
	return json.Marshal(m)
}

func (m federationMessage) signedBy(p FederationPeer) bool {
	key, ok := p.key()
	sig, err := base64.RawURLEncoding.DecodeString(m.Signature)
	return ok && m.From == p.ID && m.Version == federationVersion && err == nil && ed25519.Verify(key, m.signingBytes(), sig)
}

func (m federationMessage) fresh() bool {
	now := time.Now()
	stamp := time.UnixMilli(m.SentAt)
	return !stamp.Before(now.Add(-federationWindow)) && !stamp.After(now.Add(federationWindow))
}

func (a *App) federationControl(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var m federationMessage
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, federationMaxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil || dec.Decode(new(any)) != io.EOF || m.Response || m.Active || m.Subject != nil || !m.fresh() || !federationSecret(m.Nonce) || !federationSecret(m.Value) {
		apiError(w, http.StatusForbidden, "Federation request refused")
		return
	}
	peer, err := federationPeer(a.store.db, m.From)
	if err != nil || !m.signedBy(peer) {
		apiError(w, http.StatusForbidden, "Federation request refused")
		return
	}
	local, err := a.store.federationKey()
	if err != nil || m.To != local.ID() {
		apiError(w, http.StatusForbidden, "Federation request refused")
		return
	}
	if key, ok := relay.FederationPeerKey(r, local.ID()); ok && relay.ServerID(key) != m.From {
		apiError(w, http.StatusForbidden, "Federation request refused")
		return
	}
	answer := federationMessage{Version: federationVersion, From: local.ID(), To: peer.ID, Action: m.Action, Nonce: m.Nonce, SentAt: time.Now().UnixMilli(), Response: true}
	switch m.Action {
	case federationRedeem, federationCheck:
		person, err := a.store.federationVerify(peer, m.Value, m.Action == federationRedeem)
		if err == nil {
			answer.Active = true
			// Periodic checks confirm eligibility without returning profile data.
			if m.Action == federationRedeem {
				answer.Subject = &person
			}
		} else if !errors.Is(err, errFederationDenied) {
			apiError(w, http.StatusServiceUnavailable, "Federation source unavailable")
			return
		}
	case federationCancel:
		if err := a.store.federationRevoke(peer, m.Value); err != nil {
			if !errors.Is(err, errFederationDenied) {
				apiError(w, http.StatusServiceUnavailable, "Federation source unavailable")
				return
			}
		} else {
			answer.Active = true
		}
	default:
		apiError(w, http.StatusForbidden, "Federation request refused")
		return
	}
	encoded, err := answer.encode(local)
	if err != nil {
		apiError(w, http.StatusServiceUnavailable, "Federation source unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(encoded)
}

func (a *App) federationCall(ctx context.Context, peer FederationPeer, action, value string) (federationMessage, error) {
	var answer federationMessage
	local, err := a.store.federationKey()
	if err != nil {
		return answer, err
	}
	m := federationMessage{Version: federationVersion, From: local.ID(), To: peer.ID, Action: action, Nonce: randomToken(), SentAt: time.Now().UnixMilli(), Value: value}
	body, err := m.encode(local)
	if err != nil {
		return answer, err
	}
	if a.federationDial == nil {
		return answer, errors.New("federation transport unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, federationTimeout)
	defer cancel()
	data, err := a.federationDial(ctx, peer, body)
	if err != nil {
		return answer, err
	}
	if len(data) > federationMaxBody || json.Unmarshal(data, &answer) != nil || !answer.Response || !answer.fresh() || answer.To != local.ID() || answer.Nonce != m.Nonce || answer.Action != action || answer.Value != "" || !answer.signedBy(peer) {
		return federationMessage{}, errors.New("invalid federation control response")
	}
	if !answer.Active {
		return federationMessage{}, errFederationDenied
	}
	return answer, nil
}

type federationTarget struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ClientURL string `json:"client_url,omitempty"`
}

func (s *Store) federationTargets(id authIdentity) ([]federationTarget, error) {
	targets := []federationTarget{}
	if id.User.SourceServerID != "" {
		return targets, nil
	}
	peers, err := s.FederationPeers()
	if err != nil {
		return nil, err
	}
	for _, peer := range peers {
		if peer.allows(id.User.ID) {
			targets = append(targets, federationTarget{ID: peer.ID, Name: peer.Name, ClientURL: peer.ClientURL})
		}
	}
	return targets, nil
}

func (a *App) federationTargets(w http.ResponseWriter, id authIdentity) {
	targets, err := a.store.federationTargets(id)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "Could not load federation targets")
		return
	}
	apiJSON(w, http.StatusOK, targets)
}

func (a *App) federationIssue(w http.ResponseWriter, r *http.Request, id authIdentity) {
	var input struct {
		Target string `json:"target"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	local, err := a.store.federationKey()
	if err != nil {
		apiError(w, http.StatusServiceUnavailable, "Federation source unavailable")
		return
	}
	secret, err := a.store.federationIssue(id, input.Target)
	if errors.Is(err, errFederationDenied) {
		apiError(w, http.StatusForbidden, "Federated login was not permitted")
		return
	}
	if err != nil {
		apiError(w, http.StatusServiceUnavailable, "Federation source unavailable")
		return
	}
	apiJSON(w, http.StatusOK, map[string]string{"ticket": "spool1." + local.ID() + "." + secret})
}

func (a *App) federationExchange(w http.ResponseWriter, r *http.Request) {
	if !a.sameOrigin(r) {
		apiError(w, http.StatusForbidden, "Federated login was not permitted")
		return
	}
	if !a.federationLimiter.allow(a.loginClientIP(r)) {
		apiError(w, http.StatusTooManyRequests, "登录尝试过多，请稍后重试")
		return
	}
	var input struct {
		Ticket string `json:"ticket"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	parts := strings.Split(input.Ticket, ".")
	if len(parts) != 3 || parts[0] != "spool1" || len(parts[1]) != 52 || !federationSecret(parts[2]) {
		apiError(w, http.StatusUnauthorized, "Federated login was not permitted")
		return
	}
	peer, err := federationPeer(a.store.db, parts[1])
	if err != nil || !peer.Accept {
		apiError(w, http.StatusUnauthorized, "Federated login was not permitted")
		return
	}
	if a.federationRejected.rejected(tokenHash(input.Ticket)) {
		apiError(w, http.StatusUnauthorized, "Federated login was not permitted")
		return
	}
	checked := time.Now()
	answer, err := a.federationCall(r.Context(), peer, federationRedeem, parts[2])
	if errors.Is(err, errFederationDenied) {
		a.federationRejected.reject(tokenHash(input.Ticket))
		apiError(w, http.StatusUnauthorized, "Federated login was not permitted")
		return
	}
	if err != nil || answer.Subject == nil {
		apiError(w, http.StatusServiceUnavailable, "Federation source unavailable")
		return
	}
	token, expires, err := a.store.federationLogin(peer, *answer.Subject, checked)
	if errors.Is(err, errFederationDenied) {
		apiError(w, http.StatusUnauthorized, "Federated login was not permitted")
		return
	}
	if err != nil {
		apiError(w, http.StatusServiceUnavailable, "Federation source unavailable")
		return
	}
	id, err := a.store.authenticate(token)
	if err != nil {
		apiError(w, http.StatusUnauthorized, "Federated login was not permitted")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: requestAuthCookie(r), Value: token, Path: requestWebPath(r), HttpOnly: true, Secure: a.secureCookie(r), SameSite: http.SameSiteStrictMode, Expires: expires, MaxAge: max(1, int(time.Until(expires)/time.Second))})
	a.signedInResponse(w, id)
}
