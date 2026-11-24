package workspace

import (
	"crypto/ed25519"
	"crypto/subtle"
	"errors"
	"net/http"

	"spool/relay"
)

// The broker token admits an encrypted tunnel, not a Spool account. Clients
// receive neither a device invitation nor a server identity/private key.
func federationRelayClientRoute(peer FederationPeer) (string, error) {
	code := peer.ClientRelayCode
	if code == "" && peer.Transport == "relay" {
		code = peer.RelayCode
	}
	route, err := relay.ParsePairing(code)
	key, ok := peer.key()
	if err != nil || !ok || subtle.ConstantTimeCompare(route.PublicKey, key) != 1 {
		return "", errors.New("no trusted Relay route")
	}
	route.Invitation, route.ExpiresAt, route.LAN = "", 0, nil
	return route.Encode()
}

func (a *App) federationRelayRoute(w http.ResponseWriter, r *http.Request, id authIdentity) {
	var input struct {
		Target string `json:"target"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	peer, err := federationPeer(a.store.db, input.Target)
	if err != nil || id.User.SourceServerID != "" || !peer.allows(id.User.ID) {
		apiError(w, http.StatusForbidden, "Federated login was not permitted")
		return
	}
	code, err := federationRelayClientRoute(peer)
	if err != nil {
		apiError(w, http.StatusConflict, "No trusted address is known for this server.")
		return
	}
	apiJSON(w, http.StatusOK, map[string]string{"relay_code": code})
}

func (s *Store) allowFederationRelayClient(key ed25519.PublicKey, r *http.Request) bool {
	cookie, err := r.Cookie(authCookie)
	if err != nil {
		return false
	}
	id, err := s.authenticate(cookie.Value)
	if err != nil || id.User.SourceServerID == "" {
		return false
	}
	var matches bool
	err = s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM federation_relay_sessions WHERE session_hash=? AND client_key=?)`, id.TokenHash, relay.DeviceFingerprint(key)).Scan(&matches)
	return err == nil && matches
}
