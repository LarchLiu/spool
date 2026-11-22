package workspace

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/subtle"
	"errors"
	"io"
	"net"
	"net/http"

	"spool/internal/netproxy"
	"spool/relay"
)

func federationHTTP(ctx context.Context, origin string, body []byte, transport *http.Transport) ([]byte, error) {
	if len(body) > federationMaxBody {
		return nil, errors.New("federation request too large")
	}
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+FederationControlPath, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Transport: transport, Timeout: federationTimeout, CheckRedirect: federationNoRedirect}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("federation control request refused")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, federationMaxBody+1))
	if err == nil && len(data) > federationMaxBody {
		err = errors.New("federation response too large")
	}
	return data, err
}

func federationNoRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

func federationHTTPTransport() *http.Transport {
	return &http.Transport{
		DisableKeepAlives: true, DisableCompression: true, TLSHandshakeTimeout: federationTimeout,
		ResponseHeaderTimeout: federationTimeout, MaxResponseHeaderBytes: 16 << 10,
	}
}

func (a *App) dialFederation(ctx context.Context, peer FederationPeer, body []byte) ([]byte, error) {
	transport := federationHTTPTransport()
	switch peer.Transport {
	case "https":
		transport.Proxy = netproxy.Proxy
		return federationHTTP(ctx, peer.Address, body, transport)
	case "tailscale":
		runtime := a.federationTailnet.Load()
		if runtime == nil || runtime.ctx.Err() != nil {
			return nil, errors.New("federation Tailscale endpoint is not running")
		}
		call, cancel := context.WithCancel(ctx)
		defer cancel()
		stop := context.AfterFunc(runtime.ctx, cancel)
		defer stop()
		// Transport may detach the dial from the request context. Always use
		// the bounded control request and the active node's lifetime.
		transport.DialContext = func(_ context.Context, network, address string) (net.Conn, error) {
			return runtime.dial(call, network, address)
		}
		return federationHTTP(call, peer.Address, body, transport)
	case "relay":
		route, err := relay.ParsePairing(peer.RelayCode)
		key, ok := peer.key()
		if err != nil || !ok || subtle.ConstantTimeCompare(route.PublicKey, key) != 1 {
			return nil, errors.New("invalid federation Relay route")
		}
		local, err := a.store.federationKey()
		if err != nil {
			return nil, err
		}
		cfg, err := relay.FederationTLSConfig(route, local)
		if err != nil {
			return nil, err
		}
		broker := federationHTTPTransport()
		broker.Proxy = netproxy.Proxy
		defer broker.CloseIdleConnections()
		// Use the configured relay tunnel. Do not fall back to other routes
		// after an identity error, or reuse device admission credentials as identity.
		route.LAN = nil
		dialer, err := relay.NewDialer(ctx, route, &http.Client{Transport: broker, CheckRedirect: federationNoRedirect})
		if err != nil {
			return nil, err
		}
		defer dialer.Close()
		dialer.TLSConfig = cfg
		transport.DialTLSContext = dialer.DialTLSContext
		return federationHTTP(ctx, relay.Origin(peer.ID), body, transport)
	default:
		return nil, errors.New("federation transport not available")
	}
}

func (s *Store) federationRelayAdmission() relay.FederationAdmission {
	return relay.FederationAdmission{Path: FederationControlPath, Allow: func(key ed25519.PublicKey) bool {
		peer, err := federationPeer(s.db, relay.ServerID(key))
		if err != nil {
			return false
		}
		expected, ok := peer.key()
		return ok && subtle.ConstantTimeCompare(key, expected) == 1
	}}
}
