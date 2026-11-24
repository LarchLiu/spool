package relay

import (
	"crypto/ed25519"
	"crypto/tls"
	"net/http"
	"strings"
)

func federationServerName(id string) string      { return "federation." + id + ".spool.invalid" }
func federationTrustServerName(id string) string { return "federation-trust." + id + ".spool.invalid" }
func federationClientServerName(id string) string {
	return "federation-client." + id + ".spool.invalid"
}

// FederationAdmission separates paired control from the trust application path.
type FederationAdmission struct {
	Path      string
	TrustPath string
	Allow     func(ed25519.PublicKey) bool
	// Client access is bound to a federated session and its TLS key, separately
	// from approved devices and server control identities.
	AllowClient func(ed25519.PublicKey, *http.Request) bool
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

// FederationClientTLSConfig pins the target and proves possession of this
// client's key. Only a successful federated exchange grants business access.
func FederationClientTLSConfig(p Pairing, seed []byte) (*tls.Config, error) {
	cfg, err := DeviceTLSConfig(p, seed, false)
	if err == nil {
		cfg.ServerName = federationClientServerName(p.ID())
	}
	return cfg, err
}

func FederationClientKey(r *http.Request, id string) (ed25519.PublicKey, bool) {
	if r.TLS == nil || r.TLS.ServerName != federationClientServerName(id) {
		return nil, false
	}
	key, err := DevicePublicKey(*r.TLS)
	return key, err == nil
}

func (f FederationAdmission) serveClient(w http.ResponseWriter, r *http.Request, next http.Handler) {
	key, err := DevicePublicKey(*r.TLS)
	if err != nil || f.AllowClient == nil {
		http.Error(w, "client authentication required", http.StatusUnauthorized)
		return
	}
	// Bootstrap exposes only public metadata and the one-time login exchange.
	// Server control, device pairing, local login and Relay Web stay separate.
	bootstrap := r.URL.RawQuery == "" && !r.URL.ForceQuery && r.URL.RawPath == "" &&
		(r.Method == http.MethodGet && (r.URL.Path == "/healthz" || r.URL.Path == "/api/version") ||
			r.Method == http.MethodPost && r.URL.Path == "/api/federation/exchange")
	if !bootstrap && (!strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == f.Path || r.URL.Path == f.TrustPath || strings.TrimRight(r.URL.Path, "/") == "/api/login") {
		http.NotFound(w, r)
		return
	}
	if !bootstrap && !f.AllowClient(key, r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"Federated sign-in required"}`))
		return
	}
	next.ServeHTTP(w, r)
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
