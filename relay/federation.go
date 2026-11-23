package relay

import (
	"crypto/ed25519"
	"crypto/tls"
	"net/http"
)

func federationServerName(id string) string      { return "federation." + id + ".spool.invalid" }
func federationTrustServerName(id string) string { return "federation-trust." + id + ".spool.invalid" }

// FederationAdmission separates paired control from the trust application path.
type FederationAdmission struct {
	Path      string
	TrustPath string
	Allow     func(ed25519.PublicKey) bool
}

func (f FederationAdmission) allows(state tls.ConnectionState) bool {
	if f.Allow == nil || f.Path == "" {
		return false
	}
	key, err := DevicePublicKey(state)
	return err == nil && f.Allow(key)
}

func FederationTLSConfig(p Pairing, id *Identity) (*tls.Config, error) {
	cfg, err := DeviceTLSConfig(p, id.Seed, false)
	if err != nil {
		return nil, err
	}
	cfg.ServerName = federationServerName(p.ID())
	return cfg, nil
}

func FederationTrustTLSConfig(p Pairing, id *Identity) (*tls.Config, error) {
	cfg, err := FederationTLSConfig(p, id)
	if err != nil {
		return nil, err
	}
	cfg.ServerName = federationTrustServerName(p.ID())
	return cfg, nil
}

// FederationTrustPeerKey identifies an applicant, not an approved peer.
func FederationTrustPeerKey(r *http.Request, id string) (ed25519.PublicKey, bool) {
	if r.TLS == nil || r.TLS.ServerName != federationTrustServerName(id) {
		return nil, false
	}
	key, err := DevicePublicKey(*r.TLS)
	return key, err == nil
}

// FederationPeerKey returns the peer key only on the dedicated control ingress.
func FederationPeerKey(r *http.Request, id string) (ed25519.PublicKey, bool) {
	if r.TLS == nil || r.TLS.ServerName != federationServerName(id) {
		return nil, false
	}
	key, err := DevicePublicKey(*r.TLS)
	return key, err == nil
}

func (f FederationAdmission) serve(w http.ResponseWriter, r *http.Request, next http.Handler) {
	if r.Method != http.MethodPost || r.URL.Path != f.Path || r.URL.EscapedPath() != f.Path || r.URL.RawQuery != "" || !f.allows(*r.TLS) {
		http.NotFound(w, r)
		return
	}
	next.ServeHTTP(w, r)
}
