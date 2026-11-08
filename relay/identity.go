package relay

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const ServiceType = "_spool._tcp"

// Identity is local-only. Neither the signing seed nor relay registration
// credentials appear in a pairing code, discovery advertisement or API response.
type Identity struct {
	Seed        []byte `json:"seed"`
	AccessToken string `json:"access_token"`
}

func NewIdentity() (*Identity, error) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &Identity{Seed: private.Seed(), AccessToken: randomID()}, nil
}

func LoadIdentity(directory string) (*Identity, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(directory, "identity.json")
	data, err := os.ReadFile(path)
	if err == nil {
		var id Identity
		if json.Unmarshal(data, &id) != nil || len(id.Seed) != ed25519.SeedSize || !validToken(id.AccessToken) {
			return nil, errors.New("invalid relay identity; restore the original identity file")
		}
		return &id, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	id, err := NewIdentity()
	if err != nil {
		return nil, err
	}
	data, _ = json.Marshal(id)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	_, err = file.Write(data)
	closeErr := file.Close()
	if err != nil {
		return nil, err
	}
	return id, closeErr
}

func (i *Identity) PrivateKey() ed25519.PrivateKey { return ed25519.NewKeyFromSeed(i.Seed) }
func (i *Identity) PublicKey() ed25519.PublicKey   { return i.PrivateKey().Public().(ed25519.PublicKey) }
func ServerID(key []byte) string {
	sum := sha256.Sum256(key)
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:]))
}
func (i *Identity) ID() string { return ServerID(i.PublicKey()) }
func Origin(id string) string  { return "https://" + id + ".spool.invalid" }

func (i *Identity) TLSConfig() (*tls.Config, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	cert := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: i.ID()},
		DNSNames: []string{i.ID() + ".spool.invalid"}, NotBefore: time.Now().Add(-24 * time.Hour), NotAfter: time.Now().AddDate(10, 0, 0),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, i.PublicKey(), i.PrivateKey())
	if err != nil {
		return nil, err
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, NextProtos: []string{"http/1.1"},
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: i.PrivateKey()}}}, nil
}

// Pairing is transferred locally by the administrator. LAN addresses and mDNS
// are only hints; a TLS handshake must prove possession of the pinned key before
// any HTTP headers, cookies, passwords or request bodies are sent.
type Pairing struct {
	Version     int      `json:"v"`
	RelayURL    string   `json:"relay"`
	PublicKey   []byte   `json:"key"`
	AccessToken string   `json:"token"`
	LAN         []string `json:"lan,omitempty"`
}

func (p Pairing) ID() string { return ServerID(p.PublicKey) }
func (p Pairing) Encode() (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	data, err := json.Marshal(p)
	return "spool-pair:" + base64.RawURLEncoding.EncodeToString(data), err
}
func ParsePairing(code string) (Pairing, error) {
	var p Pairing
	code = strings.TrimSpace(code)
	if len(code) > 8192 || !strings.HasPrefix(code, "spool-pair:") {
		return p, errors.New("enter a Spool pairing code")
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(code, "spool-pair:"))
	if err != nil || json.Unmarshal(data, &p) != nil {
		return p, errors.New("invalid Spool pairing code")
	}
	return p, p.Validate()
}
func (p Pairing) Validate() error {
	if p.Version != 1 || len(p.PublicKey) != ed25519.PublicKeySize || !validToken(p.AccessToken) || len(p.LAN) > 16 {
		return errors.New("invalid or unsupported Spool pairing code")
	}
	if _, err := ParseRelayURL(p.RelayURL); err != nil {
		return err
	}
	for _, address := range p.LAN {
		if !ValidLANAddress(address) {
			return errors.New("invalid LAN address")
		}
	}
	return nil
}

// ParseRelayURL permits plaintext only on numeric loopback for development or
// a reverse proxy on the same machine. Public deployments always use HTTPS.
func ParseRelayURL(address string) (*url.URL, error) {
	u, err := url.Parse(address)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		strings.Contains(address, "#") || (u.Path != "" && u.Path != "/") ||
		(u.Scheme != "https" && !(u.Scheme == "http" && net.ParseIP(u.Hostname()).IsLoopback())) {
		return nil, errors.New("relay must be an HTTPS origin (HTTP allowed only on numeric loopback)")
	}
	if u.Port() != "" {
		n, e := strconv.Atoi(u.Port())
		if e != nil || n < 1 || n > 65535 {
			return nil, errors.New("invalid relay port")
		}
	}
	u.Path = ""
	return u, nil
}

func ValidLANAddress(address string) bool {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	if before, _, ok := strings.Cut(host, "%"); ok {
		host = before
	}
	ip := net.ParseIP(host)
	n, err := strconv.Atoi(port)
	return err == nil && n > 0 && n <= 65535 && ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast())
}

func (p Pairing) TLSConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS13, ServerName: p.ID() + ".spool.invalid", NextProtos: []string{"http/1.1"},
		// A private identity has no public CA certificate. VerifyConnection pins
		// its full public key, name, validity and signature on every handshake.
		InsecureSkipVerify: true,
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) != 1 {
				return errors.New("unexpected Spool certificate chain")
			}
			cert := state.PeerCertificates[0]
			key, ok := cert.PublicKey.(ed25519.PublicKey)
			if !ok || !bytes.Equal(key, p.PublicKey) {
				return errors.New("Spool server identity mismatch")
			}
			if time.Now().Before(cert.NotBefore) || time.Now().After(cert.NotAfter) {
				return errors.New("Spool certificate expired or not yet valid")
			}
			if err := cert.VerifyHostname(p.ID() + ".spool.invalid"); err != nil {
				return err
			}
			return cert.CheckSignature(cert.SignatureAlgorithm, cert.RawTBSCertificate, cert.Signature)
		}}
}
