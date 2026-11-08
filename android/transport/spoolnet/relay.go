package spoolnet

import (
	"context"
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
	if !filepath.IsAbs(dataDir) {
		return nil, errors.New("native state directory must be absolute")
	}
	p, err := relay.ParsePairing(code)
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
	httpClient, transport := nativeHTTPClient(nil)
	transport.DialTLSContext = dialer.DialTLSContext
	origin, _ := url.Parse(relay.Origin(p.ID()))
	httpClient.Jar, err = newSessionJar(dataDir, origin)
	if err != nil {
		cancel()
		dialer.Close()
		return nil, err
	}
	return &Client{direct: true, origin: origin, ctx: ctx, cancel: cancel, transport: transport, http: httpClient, relay: dialer, relayHTTP: outerTransport}, nil
}

// SetLANAddresses accepts resolved NSD addresses for this paired identity.
// Discovery is untrusted: TLS still verifies the paired key before each request.
// Existing requests finish on their current path; only idle connections retire.
func (c *Client) SetLANAddresses(addresses string) error {
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

func (c *Client) UsePairedServerSession(directory, directURL, tailscaleURL, code string) error {
	p, err := relay.ParsePairing(code)
	if err != nil {
		return err
	}
	return c.useServerSession(directory, directURL, tailscaleURL, relay.Origin(p.ID()))
}
