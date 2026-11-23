package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"
)

const desktopFederationHealthInterval = 15 * time.Second
const desktopFederationHealthConcurrency = 4

type desktopFederationHealthKey struct{ id, address string }

type desktopFederationHealth struct {
	lastCheck time.Time
	status    string
	cancel    context.CancelFunc
}

func desktopFederationHealthRoute(target desktopFederationTarget) (desktopFederationHealthKey, bool) {
	// 未配置的 Tailnet 目标没有可重用的本机节点，不能用公网 DNS 代查。
	if target.ClientMode != "" || !validDesktopFederationID(target.ID) {
		return desktopFederationHealthKey{}, false
	}
	address := desktopFederationClientAddress(target.ClientURL)
	if address == "" {
		return desktopFederationHealthKey{}, false
	}
	u, _ := url.Parse(address)
	if u.Path != "/" && u.Path != "/web/"+target.ID+"/" {
		return desktopFederationHealthKey{}, false
	}
	return desktopFederationHealthKey{target.ID, address}, true
}

// 已保存的线路由它自己的 WebView 和普通健康检查管理。未保存的目标只读连通性，
// 不创建档案或客户端，不启用已关闭的档案，也不参与票据签发或线路选择。
func (p *panel) desktopFederationHealthRoutes() map[desktopFederationHealthKey]bool {
	ids, addresses := map[string]bool{}, map[string]bool{}
	for _, server := range p.servers.Entries {
		if chat := p.serverChats[server.ID]; chat != nil && chat.serverID != "" {
			ids[chat.serverID] = true
		}
		if id := desktopRelayServerID(server); id != "" {
			ids[id] = true
		}
		for _, address := range []string{server.Address, server.Direct, server.Tailscale} {
			if address = desktopFederationClientAddress(address); address != "" {
				addresses[address] = true
			}
		}
	}
	wanted := map[desktopFederationHealthKey]bool{}
	for _, server := range p.servers.Entries {
		chat := p.serverChats[server.ID]
		if !server.Enabled || chat == nil || !chat.authenticated || chat.failed {
			continue
		}
		for _, target := range chat.federationTargets {
			if key, ok := desktopFederationHealthRoute(target); ok && !ids[key.id] && !addresses[key.address] {
				wanted[key] = true
			}
		}
	}
	return wanted
}

func (p *panel) checkDesktopFederationHealth(now time.Time) {
	if p.quitting || p.serverNetworks == nil || (p.window == nil && p.federation.run == nil) {
		return
	}
	wanted := p.desktopFederationHealthRoutes()
	if p.federation.health == nil {
		p.federation.health = make(map[desktopFederationHealthKey]*desktopFederationHealth)
	}
	changed := false
	for key, health := range p.federation.health {
		if !wanted[key] {
			if health.cancel != nil {
				health.cancel()
			}
			delete(p.federation.health, key)
			changed = changed || health.status != ""
		}
	}
	var due []desktopFederationHealthKey
	for key := range wanted {
		if p.federation.health[key] == nil {
			p.federation.health[key] = &desktopFederationHealth{}
		}
		health := p.federation.health[key]
		if health.cancel == nil && now.Sub(health.lastCheck) >= desktopFederationHealthInterval {
			due = append(due, key)
		}
	}
	slices.SortFunc(due, func(a, b desktopFederationHealthKey) int {
		return p.federation.health[a].lastCheck.Compare(p.federation.health[b].lastCheck)
	})
	capacity := desktopFederationHealthConcurrency - p.federation.healthRunning
	for _, key := range due[:min(len(due), capacity)] {
		health := p.federation.health[key]
		ctx, cancel := context.WithTimeout(p.serverNetworks.ctx, 4*time.Second)
		if health.lastCheck.IsZero() {
			health.status = "connecting"
			changed = true
		}
		health.lastCheck, health.cancel = now, cancel
		p.federation.healthRunning++
		probe := p.federation.probeHealth
		if probe == nil {
			probe = probeDesktopFederationHealth
		}
		p.runDesktopFederation(func(context.Context) func() {
			defer cancel()
			status := probe(ctx, key)
			return func() {
				p.federation.healthRunning--
				health.cancel = nil
				if p.quitting || p.federation.health[key] != health || !p.desktopFederationHealthRoutes()[key] {
					return
				}
				if health.status != status {
					health.status = status
					p.sendDesktopServers()
				}
			}
		})
	}
	if changed {
		p.sendDesktopServers()
	}
}

// 地址来自远端服务器而不是本机用户。匿名探测不得经代理、跳转或 DNS
// 重新解析进入本机/局域网；显式配置的连接不受这个后台探测限制。
var errDesktopFederationNonPublic = errors.New("federation health requires public HTTPS")

var desktopFederationNonPublicRanges = [...]netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
}

func desktopFederationPublicAddress(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.Zone() != "" || (ip.Is6() && ip.As16()[0]&0xe0 != 0x20) {
		return false
	}
	for _, reserved := range desktopFederationNonPublicRanges {
		if reserved.Contains(ip) {
			return false
		}
	}
	return true
}

func dialDesktopFederationHealth(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	var ips []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		ips = []netip.Addr{ip}
	} else {
		if !strings.Contains(host, ".") || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
			return nil, errDesktopFederationNonPublic
		}
		// Resolve an absolute DNS name; never try local search-domain suffixes.
		ips, err = net.DefaultResolver.LookupNetIP(ctx, "ip", host+".")
		if err != nil {
			return nil, err
		}
	}
	for _, ip := range ips {
		if !desktopFederationPublicAddress(ip) {
			return nil, errDesktopFederationNonPublic
		}
	}
	lastErr := errors.New("federation health host has no addresses")
	for _, ip := range ips {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func probeDesktopFederationHealth(ctx context.Context, key desktopFederationHealthKey) string {
	transport := &http.Transport{
		// Transport may detach its dial from the request; keep DNS and TCP
		// bounded by the probe's deadline and cancellation.
		DialContext: func(_ context.Context, network, address string) (net.Conn, error) {
			return dialDesktopFederationHealth(ctx, network, address)
		},
		DisableKeepAlives: true, TLSHandshakeTimeout: 4 * time.Second,
		MaxResponseHeaderBytes: 64 << 10,
	}
	return desktopFederationHealthStatus(ctx, key, transport)
}

func desktopFederationHealthStatus(ctx context.Context, key desktopFederationHealthKey, transport http.RoundTripper) string {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, key.address+"api/version", nil)
	if err != nil {
		return "error"
	}
	response, err := transport.RoundTrip(request)
	if errors.Is(err, errDesktopFederationNonPublic) {
		return ""
	}
	if err != nil {
		return "error"
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || desktopFederationServerID(response.Body) != key.id {
		return "error"
	}
	return "login" // 匿名探测只能确认目标可达，不能证明已有会话。
}
