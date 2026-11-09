package relay

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"math/big"
	"time"
)

// Distinct TLS names select admission policy, never authorization by themselves.
// Every mode still pins the same server key and certificate identity.
func DeviceServerName(id string) string  { return "device." + id + ".spool.invalid" }
func PairingServerName(id string) string { return "pair." + id + ".spool.invalid" }

func DeviceFingerprint(key []byte) string {
	h := sha256.Sum256(key)
	return hex.EncodeToString(h[:])
}

// DeviceTLSConfig proves possession through TLS CertificateVerify. The seed
// stays in the app's private, backup-excluded directory and never enters a QR.
func DeviceTLSConfig(p Pairing, seed []byte, pairing bool) (*tls.Config, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, errors.New("invalid device key")
	}
	key := ed25519.NewKeyFromSeed(seed)
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	cert := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: DeviceFingerprint(key.Public().(ed25519.PublicKey))},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(1, 0, 0),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, key.Public(), key)
	if err != nil {
		return nil, err
	}
	cfg := p.TLSConfig()
	cfg.ServerName = DeviceServerName(p.ID())
	if pairing {
		cfg.ServerName = PairingServerName(p.ID())
	}
	cfg.Certificates = []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}
	return cfg, nil
}

func DevicePublicKey(state tls.ConnectionState) (ed25519.PublicKey, error) {
	if len(state.PeerCertificates) != 1 {
		return nil, errors.New("device certificate required")
	}
	c := state.PeerCertificates[0]
	key, ok := c.PublicKey.(ed25519.PublicKey)
	if !ok || len(key) != ed25519.PublicKeySize || time.Now().Before(c.NotBefore) || time.Now().After(c.NotAfter) {
		return nil, errors.New("invalid device certificate")
	}
	if err := c.CheckSignature(c.SignatureAlgorithm, c.RawTBSCertificate, c.Signature); err != nil {
		return nil, err
	}
	return key, nil
}
