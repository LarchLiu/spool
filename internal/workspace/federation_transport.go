package workspace

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/subtle"
	"database/sql"
	"errors"
	"io"
	"net"
	"net/http"
	"time"

	"spool/internal/netproxy"
	"spool/relay"
)

func federationHTTP(ctx context.Context, origin string, body []byte, transport *http.Transport) ([]byte, error) {
	return federationHTTPPost(ctx, origin, FederationControlPath, body, transport)
}

func federationHTTPPost(ctx context.Context, origin, path string, body []byte, transport *http.Transport) ([]byte, error) {
	if len(body) > federationWireMaxBody {
		return nil, errors.New("federation request too large")
	}
	defer transport.CloseIdleConnections()
	// Every caller bounds its exchange with a context deadline. Agent
	// operations wait longer than an ordinary control message for the answer.
	timeout := federationTimeout
	if deadline, ok := ctx.Deadline(); ok {
		timeout = max(timeout, time.Until(deadline))
	}
	transport.ResponseHeaderTimeout = timeout
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: federationNoRedirect}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("federation control request refused")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, federationWireMaxBody+1))
	if err == nil && len(data) > federationWireMaxBody {
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
	return a.dialFederationPath(ctx, peer, FederationControlPath, body)
}

func (a *App) dialFederationPath(ctx context.Context, peer FederationPeer, path string, body []byte) ([]byte, error) {
	transport := federationHTTPTransport()
	switch peer.Transport {
	case "https":
		transport.Proxy = netproxy.Proxy
		return federationHTTPPost(ctx, peer.Address, path, body, transport)
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
		return federationHTTPPost(call, peer.Address, path, body, transport)
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
		tlsConfig := relay.FederationTLSConfig
		if path == FederationTrustPath {
			tlsConfig = relay.FederationTrustTLSConfig
		}
		cfg, err := tlsConfig(route, local)
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
		return federationHTTPPost(ctx, relay.Origin(peer.ID), path, body, transport)
	default:
		return nil, errors.New("federation transport not available")
	}
}

func (s *Store) federationRelayAdmission() relay.FederationAdmission {
	return relay.FederationAdmission{Path: FederationControlPath, TrustPath: FederationTrustPath, AllowClient: s.allowFederationRelayClient, Allow: func(key ed25519.PublicKey) bool {
		peer, err := federationPeer(s.db, relay.ServerID(key))
		if errors.Is(err, sql.ErrNoRows) {
			peer, err = s.pendingFederationTrustPeer(relay.ServerID(key))
		}
		if err != nil {
			return false
		}
		expected, ok := peer.key()
		return ok && subtle.ConstantTimeCompare(key, expected) == 1
	}}
}
