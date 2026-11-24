package spoolnet

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"spool/relay"
)

// ValidatePairing canonicalizes an administrator-supplied code before saving it.
func ValidatePairing(code string) (string, error) {
	p, err := relay.ParsePairing(code)
	if err != nil {
		return "", err
	}
	return p.Encode()
}
func PairingServerID(code string) (string, error) {
	p, err := relay.ParsePairing(code)
	if err != nil {
		return "", err
	}
	return p.ID(), nil
}

// NewRelayClient uses one identity and HTTPS origin for both LAN and relay.
// The outer relay HTTP client has no Spool cookie jar and never inherits proxies.
func NewRelayClient(dataDir, code string) (*Client, error) {
	return newRelayClient(dataDir, code, false)
}

// NewFederationRelayClient proves a private client key without a device
// invitation. Business access still requires a session obtained by a successful
// one-time federation exchange on this exact TLS identity.
func NewFederationRelayClient(dataDir, code string) (*Client, error) {
	return newRelayClient(dataDir, code, true)
}

func newRelayClient(dataDir, code string, federated bool) (*Client, error) {
	if !filepath.IsAbs(dataDir) {
		return nil, errors.New("native state directory must be absolute")
	}
	p, err := relay.ParsePairing(code)
	if err != nil {
		return nil, err
	}
	var device savedDevice
	if federated {
		if p.Invitation != "" || len(p.LAN) != 0 {
			return nil, errors.New("invalid federated Relay route")
		}
		identity, err := relay.LoadIdentity(filepath.Join(dataDir, "federation-client"))
		if err != nil {
			return nil, err
		}
		device.Seed = identity.Seed
	} else {
		device, err = loadDevice(dataDir, p.ID())
		if err != nil || !device.Approved {
			return nil, errors.New("This phone is not paired. Scan a new code in Spool Desktop.")
		}
	}
	makeTLS := func() (*tls.Config, error) { return relay.DeviceTLSConfig(p, device.Seed, false) }
	if federated {
		makeTLS = func() (*tls.Config, error) { return relay.FederationClientTLSConfig(p, device.Seed) }
	}
	cfg, err := makeTLS()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	outerTransport := http.DefaultTransport.(*http.Transport).Clone()
	outerTransport.Proxy = nil
	outer := &http.Client{Transport: outerTransport, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("relay redirects are disabled") }}
	dialer, err := relay.NewDialer(ctx, p, outer)
	if err != nil {
		cancel()
		return nil, err
	}
	dialer.TLSConfig = cfg
	if device.AccessToken != "" {
		if err := dialer.SetAccessToken(device.AccessToken); err != nil {
			cancel()
			dialer.Close()
			return nil, errors.New("invalid saved device identity; pair again")
		}
	}
	httpClient, transport := nativeHTTPClient(nil)
	transport.DialTLSContext = dialer.DialTLSContext
	origin, _ := url.Parse(relay.Origin(p.ID()))
	httpClient.Jar, err = newSessionJar(dataDir, origin)
	if err != nil {
		cancel()
		dialer.Close()
		return nil, err
	}
	c := &Client{direct: true, origin: origin, ctx: ctx, cancel: cancel, transport: transport, http: httpClient, relay: dialer, relayHTTP: outerTransport,
		admissionSync: make(chan struct{}, 1), admissionDone: make(chan struct{})}
	if federated {
		close(c.admissionDone)
	} else {
		go c.keepAdmissionCurrent(dataDir, p, device.Seed)
		c.requestAdmissionSync()
	}
	return c, nil
}

// SetLANAddresses accepts resolved NSD addresses for this paired identity.
// Discovery is untrusted: TLS still verifies the paired key before each request.
// Existing requests finish on their current path; only idle connections retire.
func (c *Client) SetLANAddresses(addresses string) error {
	if c.transportOwner != nil {
		return c.transportOwner.SetLANAddresses(addresses)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("connection is closed")
	}
	if c.relay == nil {
		return errors.New("not a paired connection")
	}
	if addresses == c.relayLAN {
		return nil
	}
	list := []string{}
	for _, address := range strings.Split(addresses, ",") {
		if address = strings.TrimSpace(address); address != "" {
			list = append(list, address)
		}
	}
	for _, hint := range c.relay.Pairing.LAN {
		found := false
		for _, address := range list {
			if address == hint {
				found = true
				break
			}
		}
		if !found && len(list) < 16 {
			list = append(list, hint)
		}
	}
	if err := c.relay.SetLANAddresses(list); err != nil {
		return err
	}
	c.relayLAN = addresses
	c.transport.CloseIdleConnections()
	return nil
}
