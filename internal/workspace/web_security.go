package workspace

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

const remoteHTTPSRequired = "Use HTTPS for remote access. HTTP is only available on this computer."

func parseWebTrustedProxies(value string) ([]netip.Prefix, error) {
	var networks []netip.Prefix
	if strings.TrimSpace(value) == "" {
		return networks, nil
	}
	for _, item := range strings.Split(value, ",") {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(item))
		if err != nil {
			return nil, fmt.Errorf("Web trusted proxies must be comma-separated IP CIDRs")
		}
		networks = append(networks, prefix.Masked())
	}
	return networks, nil
}

func requestPeerIP(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return netip.Addr{}
	}
	ip, _ := netip.ParseAddr(host)
	return ip.Unmap().WithZone("")
}

func trustsWebProxy(networks []netip.Prefix, peer netip.Addr) bool {
	for _, network := range networks {
		if network.Contains(peer) {
			return true
		}
	}
	return false
}

func webProxyNetworks(settings Settings) ([]netip.Prefix, error) {
	networks, err := parseWebTrustedProxies(settings.WebTrustedProxies)
	if err != nil {
		return nil, err
	}
	// Automatically discovered trust is one replaceable peer, bound to the
	// verified public origin. Manual entries remain under the operator's control.
	if settings.WebDetectedOrigin == settings.PublicURL && settings.WebDetectedOrigin != "" {
		prefix, err := netip.ParsePrefix(settings.WebDetectedProxy)
		if err == nil && prefix.Bits() == prefix.Addr().BitLen() {
			networks = append(networks, prefix)
		}
	}
	return networks, nil
}

// Only the ordinary HTTP backend consumes Web proxy headers. TLS entries such
// as Relay and Tailscale already supply the peer through their own transport.
// Walk from the TCP peer through the trusted suffix; a client cannot choose its
// login bucket by prepending an address to X-Forwarded-For.
func (a *App) loginClientIP(r *http.Request) string {
	peer := requestPeerIP(r)
	if !peer.IsValid() {
		// Relay's authenticated control channel uses a synthetic address such
		// as relay-client/<ip>; preserve that transport-supplied identity.
		return r.RemoteAddr
	}
	networks, err := webProxyNetworks(a.activeSettings())
	if r.TLS != nil || err != nil || !trustsWebProxy(networks, peer) {
		return peer.String()
	}
	forwarded := strings.Join(r.Header.Values("X-Forwarded-For"), ",")
	if forwarded == "" {
		return peer.String()
	}
	chain := strings.Split(forwarded, ",")
	client := peer
	for i := len(chain) - 1; i >= 0 && trustsWebProxy(networks, client); i-- {
		// Bound trusted hops, not an untrusted prefix: an attacker must not
		// select the proxy's fallback bucket by prepending a long list.
		if len(chain)-1-i >= 64 {
			return peer.String()
		}
		next, err := netip.ParseAddr(strings.TrimSpace(chain[i]))
		if err != nil || next.Zone() != "" {
			return peer.String()
		}
		client = next.Unmap()
	}
	return client.String()
}

// A public proxy may rewrite Host to its loopback upstream. Forwarding headers
// and a non-local Origin distinguish that request from the desktop's own HTTP.
func directLoopbackRequest(r *http.Request) bool {
	if r.TLS != nil || !requestPeerIP(r).IsLoopback() {
		return false
	}
	for _, name := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Forwarded-Port", "X-Real-IP", "Via"} {
		if len(r.Header.Values(name)) != 0 {
			return false
		}
	}
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = strings.Trim(r.Host, "[]")
	}
	if !strings.EqualFold(host, "localhost") && !net.ParseIP(host).IsLoopback() {
		return false
	}
	origin := r.Header.Get("Origin")
	return origin == "" || origin == "http://"+r.Host
}

func singleProxyHeader(r *http.Request, name string) (string, bool) {
	values := r.Header.Values(name)
	if len(values) != 1 || strings.Contains(values[0], ",") {
		return "", false
	}
	value := strings.TrimSpace(values[0])
	return value, value != ""
}

// Plain HTTP is for this computer or an explicitly trusted HTTPS terminator.
// PublicURL and client-supplied headers alone never establish that trust.
func (a *App) allowWebTransport(r *http.Request) bool {
	if r.TLS != nil || directLoopbackRequest(r) {
		return true
	}
	settings := a.activeSettings()
	public, err := url.Parse(settings.PublicURL)
	if err != nil || public.Scheme != "https" || public.Host == "" {
		return false
	}
	networks, err := webProxyNetworks(settings)
	if err != nil {
		return false
	}
	peer := requestPeerIP(r)
	proto, valid := singleProxyHeader(r, "X-Forwarded-Proto")
	if !trustsWebProxy(networks, peer) || !valid || proto != "https" {
		return false
	}
	host := r.Host
	if len(r.Header.Values("X-Forwarded-Host")) != 0 {
		var valid bool
		host, valid = singleProxyHeader(r, "X-Forwarded-Host")
		if !valid {
			return false
		}
	}
	return strings.EqualFold(host, public.Host)
}
