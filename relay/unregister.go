package relay

import (
	"context"
	"crypto/ed25519"
	"errors"
	"net/http"
)

// Unregister invalidates only this identity's credential and disconnects its
// clients. Retrying with the same credential and key is safe after a lost reply.
func Unregister(ctx context.Context, url, token string, id *Identity, client *http.Client) error {
	if !validToken(token) {
		return ErrRegistrationRejected
	}
	h, err := NewHost(ctx, url, token, id, client)
	if err != nil {
		return err
	}
	defer h.Close()
	ctx, cancel := context.WithTimeout(h.ctx, handshakeTimeout)
	defer cancel()
	c, err := dialWebsocket(ctx, client, url, "/v1/unregister", token)
	if err != nil {
		return err
	}
	defer c.CloseNow()
	m, err := readControl(ctx, c)
	if err != nil || m.Type != "challenge" || !validToken(m.Challenge) {
		return errors.New("relay unregister challenge failed")
	}
	if err := writeControl(ctx, c, controlMessage{Type: "unregister", PublicKey: id.PublicKey(), Signature: ed25519.Sign(id.PrivateKey(), unregisterProof(m.Challenge, token))}); err != nil {
		return err
	}
	m, err = readControl(ctx, c)
	if err == nil && m.Type == "registration_rejected" {
		return ErrRegistrationRejected
	}
	if err != nil || m.Type != "unregistered" {
		return errors.New("relay did not confirm unregistration")
	}
	return nil
}

func (s *Server) unregister(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	record, allowed := s.credentialForRequestLocked(r)
	allowed = allowed && record.ServerID != ""
	full := s.closed || s.registering >= s.opts.MaxHosts
	if allowed && !full {
		s.registering++
	}
	s.mu.Unlock()
	if !allowed {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if full {
		http.Error(w, "relay unavailable", http.StatusServiceUnavailable)
		return
	}
	defer func() { s.mu.Lock(); s.registering--; s.mu.Unlock() }()
	c, err := accept(w, r)
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx, cancel := context.WithTimeout(r.Context(), handshakeTimeout)
	defer cancel()
	challenge := randomID()
	if writeControl(ctx, c, controlMessage{Type: "challenge", Challenge: challenge}) != nil {
		return
	}
	m, err := readControl(ctx, c)
	token := r.Header.Get("Authorization")[len("Bearer "):]
	if err != nil || m.Type != "unregister" || len(m.PublicKey) != ed25519.PublicKeySize ||
		ServerID(m.PublicKey) != record.ServerID || !ed25519.Verify(m.PublicKey, unregisterProof(challenge, token), m.Signature) {
		writeControl(ctx, c, controlMessage{Type: "registration_rejected"})
		return
	}
	s.mu.Lock()
	current, allowed := s.credentialForRequestLocked(r)
	if s.closed || !allowed || current.ID != record.ID || current.ServerID != record.ServerID {
		s.mu.Unlock()
		writeControl(ctx, c, controlMessage{Type: "registration_rejected"})
		return
	}
	err = s.unregisterLocked(current)
	s.mu.Unlock()
	if err == nil {
		writeControl(ctx, c, controlMessage{Type: "unregistered"})
	}
}
