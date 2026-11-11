package relay

import (
	"context"
	"crypto/ed25519"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/coder/websocket"
)

// registrationAdmission is what the request's credential allows, decided
// before the relay accepts the WebSocket or does any handshake work.
type registrationAdmission struct {
	// The credential presented. Zero for automatic enrollment.
	record registrationRecord
	// No credential was presented and automatic registration is allowed.
	withoutToken bool
	// The credential's identity is already connected, so this connection can
	// only replace that entry.
	reconnecting bool
}

// registrationClaim is a registration message whose signature was verified
// against this connection's challenge.
type registrationClaim struct {
	publicKey   ed25519.PublicKey
	accessToken string
	name        string
	// The host signed the isolated-subdomain Web opt-in. Earlier Web opt-ins
	// still verify, but never publish: old clients keep native connectivity
	// and cannot reintroduce shared-origin Web mounts.
	web bool
}

// registrationOutcome is what a committed registration tells the host.
type registrationOutcome struct {
	// Set when automatic enrollment created or rotated the credential.
	issuedToken string
	webURL      string
	limit       int
}

// A registration the relay will not accept right now, without a reason the
// host could act on: the relay is closing, at capacity, or this identity is
// already connected.
var errRegistrationUnavailable = errors.New("relay cannot accept this registration")

// register serves /v1/register and /v1/host. Both prove an identity and bind
// it to a credential. Enrollment stops there; a host connection then stays
// open as that server's control connection.
func (s *Server) register(w http.ResponseWriter, r *http.Request, enrollOnly bool) {
	admission, refused := s.admitRegistration(r, enrollOnly)
	switch refused {
	case http.StatusUnauthorized:
		http.Error(w, "unauthorized", refused)
		return
	case http.StatusServiceUnavailable:
		http.Error(w, "relay unavailable", refused)
		return
	}
	// A connected host stops counting as a handshake in progress.
	connected := false
	defer func() {
		if !connected {
			s.mu.Lock()
			s.registering--
			s.mu.Unlock()
		}
	}()
	c, err := accept(w, r)
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	claim, ok := readRegistrationClaim(ctx, c, enrollOnly)
	if !ok {
		return
	}
	h := &registeredHost{id: ServerID(claim.publicKey), tokens: []string{claim.accessToken}, publicKey: claim.publicKey, registrationID: admission.record.ID, control: c, ctx: ctx, cancel: cancel, open: make(chan controlMessage, 32), pairs: make(map[string]*relayPair)}
	h.web = newWebPool(func(ctx context.Context, clientIP string) (net.Conn, error) { return s.dialWeb(ctx, h, clientIP) })
	var unreachable *registeredHost
	if admission.reconnecting {
		unreachable = s.unreachableHost(ctx, h.id, admission.record.ID)
	}
	metadata := registrationMetadata{name: claim.name, ip: s.clientIP(r), enrollment: enrollOnly}
	outcome, err := s.commitRegistration(r, admission, claim, metadata, h, unreachable, enrollOnly)
	switch {
	case err == nil:
	case errors.Is(err, ErrRegistrationRejected), errors.Is(err, ErrRegistrationExists), errors.Is(err, ErrRegistrationRevoked):
		writeControl(ctx, c, controlMessage{Type: "registration_rejected"})
		return
	case errors.Is(err, ErrRegistrationLimited):
		// Not a rejection of this server's credential: it has none yet.
		writeControl(ctx, c, controlMessage{Type: "registration_limited"})
		return
	default:
		return
	}
	if enrollOnly {
		writeControl(ctx, c, controlMessage{Type: "ready", RegistrationToken: outcome.issuedToken})
		return
	}
	connected = true
	s.serveHost(h, outcome)
}

// admitRegistration reserves a handshake slot for a request that presents an
// active credential, or none where automatic registration is allowed. It
// returns the HTTP status to refuse with, or zero.
func (s *Server) admitRegistration(r *http.Request, enrollOnly bool) (registrationAdmission, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, active := s.registrationForRequestLocked(r)
	admission := registrationAdmission{record: record, withoutToken: s.opts.AllowUnregisteredHosts && r.Header.Get("Authorization") == ""}
	if !active && !admission.withoutToken {
		return admission, http.StatusUnauthorized
	}
	admission.reconnecting = !enrollOnly && active && record.ServerID != "" && s.hosts[record.ServerID] != nil
	// Enrollment adds no host, and a reconnecting identity replaces its own.
	addsHost := !enrollOnly && !admission.reconnecting
	if s.closed || (addsHost && len(s.hosts) >= s.opts.MaxHosts) || s.registering >= s.opts.MaxHosts {
		return admission, http.StatusServiceUnavailable
	}
	s.registering++
	return admission, 0
}

// readRegistrationClaim issues the challenge and verifies the reply. The
// identity is whichever key signed it; whether the request's credential may
// be used by that identity is decided in commitRegistration.
func readRegistrationClaim(ctx context.Context, c *websocket.Conn, enrollOnly bool) (registrationClaim, bool) {
	challenge := randomID()
	if writeControl(ctx, c, controlMessage{Type: "challenge", Challenge: challenge}) != nil {
		return registrationClaim{}, false
	}
	authCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	m, err := readControl(authCtx, c)
	cancel()
	if err != nil || m.Type != "register" || len(m.PublicKey) != ed25519.PublicKeySize || !validToken(m.AccessToken) {
		return registrationClaim{}, false
	}
	if !ed25519.Verify(m.PublicKey, hostRegistrationProof(challenge, m.AccessToken, m.WebOrigin, m.Name, m.WebVersion), m.Signature) {
		return registrationClaim{}, false
	}
	// Only an explicit enrollment may label the server.
	name := strings.TrimSpace(m.Name)
	if name != "" && (!enrollOnly || !validRegistrationName(name)) {
		writeControl(ctx, c, controlMessage{Type: "invalid_registration"})
		return registrationClaim{}, false
	}
	if m.WebOrigin != "" {
		if u, err := ParseRelayURL(m.WebOrigin); err != nil || u.String() != m.WebOrigin {
			return registrationClaim{}, false
		}
	}
	if m.WebVersion != 0 && m.WebVersion != 2 {
		return registrationClaim{}, false
	}
	return registrationClaim{publicKey: append(ed25519.PublicKey(nil), m.PublicKey...), accessToken: m.AccessToken, name: name, web: m.WebOrigin != "" && m.WebVersion == 2}, true
}

// commitRegistration binds the verified identity to its credential and, for a
// host connection, publishes h as connected. Everything is decided under one
// lock against the current store, so a credential that was revoked or
// replaced during the handshake is refused here.
func (s *Server) commitRegistration(r *http.Request, admission registrationAdmission, claim registrationClaim, metadata registrationMetadata, h, unreachable *registeredHost, enrollOnly bool) (registrationOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, active := s.registrationForRequestLocked(r)
	if !admission.withoutToken && (!active || current.ID != admission.record.ID) {
		return registrationOutcome{}, ErrRegistrationRejected
	}
	// A connected identity is only replaced when its control connection failed
	// the liveness probe. A live duplicate keeps its connection, so two
	// instances sharing one identity cannot disconnect each other in turn.
	// Enrolling with a credential never touches the connected host.
	existing := s.hosts[h.id]
	replaces := existing != nil && existing == unreachable
	conflicts := existing != nil && !replaces && (!enrollOnly || admission.withoutToken)
	addsHost := !enrollOnly && !replaces
	if s.closed || conflicts || (addsHost && len(s.hosts) >= s.opts.MaxHosts) {
		return registrationOutcome{}, errRegistrationUnavailable
	}
	var outcome registrationOutcome
	var err error
	if admission.withoutToken {
		current, outcome.issuedToken, err = s.claimIdentityLocked(h.id, metadata)
	} else {
		err = s.claimRegistrationLocked(current, h.id, metadata)
	}
	if err != nil || enrollOnly {
		return outcome, err
	}
	if err := s.setWebPublishedLocked(current.ID, claim.web); err != nil {
		return registrationOutcome{}, err
	}
	if claim.web && !s.registrations[current.ID].WebBlocked {
		h.webOrigin = strings.TrimSuffix(s.webURLLocked(h.id), "/")
		outcome.webURL = h.webOrigin + "/"
	}
	if replaces {
		s.disconnectHostLocked(existing)
	}
	h.registrationID = current.ID
	s.hosts[h.id] = h
	outcome.limit = s.opts.MaxConnectionsPerHost
	s.registering--
	return outcome, nil
}

// serveHost runs a published control connection until it ends, then retires
// the host and everything it owns.
func (s *Server) serveHost(h *registeredHost, outcome registrationOutcome) {
	ctx, c := h.ctx, h.control
	defer func() {
		h.cancel()
		s.mu.Lock()
		if s.hosts[h.id] == h {
			delete(s.hosts, h.id)
		}
		for _, p := range h.pairs {
			p.cancel()
		}
		s.mu.Unlock()
		h.web.close()
	}()
	if writeControl(ctx, c, controlMessage{Type: "ready", RegistrationToken: outcome.issuedToken, WebURL: outcome.webURL, MaxConnections: outcome.limit, AdmissionUpdates: true}) != nil {
		return
	}
	go keepAlive(ctx, c)
	// The host only sends refusals and admission changes after registration.
	// Reading also processes pongs and detects a dead host when no client is
	// connected.
	go func() {
		defer h.cancel()
		for {
			m, err := readControl(ctx, c)
			if err != nil {
				return
			}
			switch m.Type {
			case "reject":
				s.rejectPair(h, m.ID)
			case "admission":
				s.setAdmission(h, m.Tokens)
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case message := <-h.open:
			if writeControl(ctx, c, message) != nil {
				return
			}
		}
	}
}

// unreachableHost returns the connected host for id only when its control
// connection fails a liveness probe. The caller has verified the identity's
// signature; the credential must also be the one that connection registered.
func (s *Server) unreachableHost(ctx context.Context, id, registrationID string) *registeredHost {
	s.mu.Lock()
	existing := s.hosts[id]
	s.mu.Unlock()
	if existing == nil || existing.registrationID != registrationID {
		return nil
	}
	probe, cancel := context.WithTimeout(ctx, s.takeoverProbe)
	defer cancel()
	// A canceled newcomer proves nothing about the existing connection.
	if existing.control.Ping(probe) == nil || ctx.Err() != nil {
		return nil
	}
	return existing
}
