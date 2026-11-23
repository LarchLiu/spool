package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"spool/internal/tailnet"
	"spool/relay"
)

const desktopFederationPrefix = "/__desktop/federation-"
const desktopFederationLoginPath = desktopFederationPrefix + "login"
const desktopFederationTargetsPath = desktopFederationPrefix + "targets"
const desktopFederationTimeout = 45 * time.Second

// Progress shown by the page that asked. It never carries a ticket, cookie or
// account: the whole exchange stays in native code.
type desktopFederationState struct {
	Target string `json:"target"`
	Source string `json:"source,omitempty"`
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

var desktopFederationChanged = mygo.NewEvent[desktopFederationState]("spool:federation")

type desktopFederationAttempt struct {
	target string
	// The published address the destination was chosen by, if any.
	address           string
	publishedMode     *string // Set only for a newly created route.
	source            string
	sourceChat        *desktopServerChat
	origin            string
	originChat        *desktopServerChat
	grouped           bool
	scrollLeft        *float64
	sourceFingerprint string
	// resolving, confirming, waiting, network, enrolling, working, storing, or committing.
	phase     string
	exchanged bool
	detached  bool
	automatic bool
	server    desktopServer
	chat      *desktopServerChat
	started   time.Time
	// 自动登录核对 Cookie 后，允许替换尚未收到过期通知的缓存页面。
	destinationAuthRevision uint64
}

func (a *desktopFederationAttempt) destinationSignedIn() bool {
	return a.chat.authenticated && (!a.automatic && !a.grouped || a.chat.statusRevision != a.destinationAuthRevision)
}

type desktopFederationResolution struct {
	server        desktopServer
	found         bool
	address       string
	proposed      desktopServer
	publishedMode string
	existing      string
}

type desktopFederationCookies interface {
	List(address string) ([]mygo.Cookie, error)
	Set(mygo.Cookie) error
}

type desktopWebViewCookies struct{}

func (desktopWebViewCookies) List(address string) ([]mygo.Cookie, error) {
	return mygo.Cookies.List(mygo.CookieFilter{URL: address})
}
func (desktopWebViewCookies) Set(cookie mygo.Cookie) error { return mygo.Cookies.Set(cookie) }

type desktopFederation struct {
	attempt *desktopFederationAttempt
	// 仅响应一次服务器标签选择，不因后台状态变化或退出登录而重试。
	selected string
	// Selected alias for the foreground server; not persisted in the profile.
	selectedTab   string
	health        map[desktopFederationHealthKey]*desktopFederationHealth
	healthRunning int
	// Test seams; nil selects the native implementation.
	probeHealth  func(context.Context, desktopFederationHealthKey) string
	emit         func(*desktopServerChat, desktopFederationState)
	identify     func(context.Context, desktopServer) string
	confirm      func(address string) bool
	present      func(id string, reload bool)
	run          func(func(context.Context) func())
	cookies      desktopFederationCookies
	tailnetLogin func(desktopServer) string
	openLogin    func(string)
	configure    func(id string)
}

func validDesktopFederationID(id string) bool {
	return len(id) == 52 && strings.Trim(id, "abcdefghijklmnopqrstuvwxyz234567") == ""
}

func validDesktopFederationTicket(ticket string) bool {
	parts := strings.Split(ticket, ".")
	if len(parts) != 3 || parts[0] != "spool1" || !validDesktopFederationID(parts[1]) || len(parts[2]) != 43 {
		return false
	}
	secret, err := base64.RawURLEncoding.Strict().DecodeString(parts[2])
	return err == nil && len(secret) == 32
}

type desktopFederationSelection struct {
	source, target string
	scrollLeft     *float64
}

func desktopFederationTabKey(source, target string) string {
	return "federation:" + source + ":" + target
}

// 页面只能指定标签中的来源和目标。保留路径不会载入为文档。
func desktopFederationNavigation(address, navigation string) (request desktopFederationSelection, reserved, ok bool) {
	if !chatNavigationAllowed(address, navigation) {
		return request, false, false
	}
	u, _ := url.Parse(navigation)
	if !strings.HasPrefix(u.Path, desktopFederationPrefix) {
		return request, false, false
	}
	params, err := url.ParseQuery(u.RawQuery)
	if err != nil || u.Path != desktopFederationLoginPath || u.RawPath != "" || u.Fragment != "" || len(params["target"]) != 1 || !validDesktopFederationID(params.Get("target")) {
		return request, true, false
	}
	if values, present := params["scroll_left"]; present {
		if request.scrollLeft = desktopTabScroll(values); request.scrollLeft == nil {
			return request, true, false
		}
		delete(params, "scroll_left")
	}
	if values, present := params["source"]; present {
		if len(params) != 2 || len(values) != 1 || values[0] == "" || len(values[0]) > 128 || strings.Trim(values[0], "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-") != "" {
			return request, true, false
		}
		request.source = values[0]
	} else if len(params) != 1 {
		return request, true, false
	}
	request.target = params.Get("target")
	return request, true, true
}

// A server's public federation identity, read without any account credentials
// and without following redirects.
func (n *desktopServerNetworks) identity(ctx context.Context, server desktopServer) string {
	request, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(server.Address, "/")+"/api/version", nil)
	if err != nil {
		return ""
	}
	var transport http.RoundTripper = http.DefaultTransport
	if server.Mode == "tailscale" || server.Mode == "relay" {
		transport = desktopNetworkTransport{n, server}
	}
	response, err := transport.RoundTrip(request)
	if err != nil {
		return ""
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ""
	}
	return desktopFederationServerID(response.Body)
}

func desktopFederationServerID(body io.Reader) string {
	var version struct {
		ServerID string `json:"server_id"`
	}
	if json.NewDecoder(io.LimitReader(body, 64<<10)).Decode(&version) != nil || !validDesktopFederationID(version.ServerID) {
		return ""
	}
	return version.ServerID
}

// One server as its page reaches it: the local service, or a remote server's
// fixed gateway mount with its own cookie namespace.
type desktopFederationSite struct {
	base   string
	prefix string
}

func (p *panel) desktopFederationSite(server desktopServer, chat *desktopServerChat) desktopFederationSite {
	site := desktopFederationSite{base: chat.address}
	if server.ID != localDesktopServer {
		site.prefix = "spool_remote_" + strings.Split(desktopServerMount(server), "/")[2] + "_"
	}
	return site
}

func (s desktopFederationSite) path() string {
	u, err := url.Parse(s.base)
	if err != nil || u.Path == "" {
		return "/"
	}
	return u.Path
}

func (s desktopFederationSite) session(name string) bool {
	if s.prefix == "" {
		return name == "spool_session"
	}
	name, ok := strings.CutPrefix(name, s.prefix)
	return ok && (name == "spool_session" || name == "__Host-spool_session")
}

// Only this server's own session cookies, never another mount's or the
// local service's, which share the loopback host.
func (s desktopFederationSite) sessions(store desktopFederationCookies) ([]mygo.Cookie, error) {
	all, err := store.List(s.base)
	if err != nil {
		return nil, err
	}
	var owned []mygo.Cookie
	for _, cookie := range all {
		path := cookie.Path
		if path == "" {
			path = "/"
		}
		if s.session(cookie.Name) && path == s.path() && cookie.HTTPOnly {
			owned = append(owned, cookie)
		}
	}
	return owned, nil
}

func desktopFederationFingerprint(cookies []mygo.Cookie) string {
	if len(cookies) == 0 {
		return ""
	}
	values := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		values = append(values, cookie.Name+"="+cookie.Value)
	}
	sort.Strings(values)
	sum := sha256.Sum256([]byte(strings.Join(values, "\n")))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func (s desktopFederationSite) do(ctx context.Context, client *http.Client, method, path string, body any, cookies []mygo.Cookie, csrf string) (*http.Response, error) {
	u, err := url.Parse(s.base)
	if err != nil || u.Host == "" {
		return nil, errors.New("invalid server address")
	}
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		payload = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(s.base, "/")+path, payload)
	if err != nil {
		return nil, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", u.Scheme+"://"+u.Host)
	}
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}
	for _, cookie := range cookies {
		request.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value})
	}
	return client.Do(request)
}

func desktopFederationJSON(response *http.Response, value any) bool {
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false
	}
	const limit = 64 << 10
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	return err == nil && len(body) <= limit && json.Unmarshal(body, value) == nil
}

// Loopback only: both sites are the local service or the desktop gateway,
// which applies each remote server's transport and cookie isolation.
func desktopFederationClient() *http.Client {
	return &http.Client{
		Transport:     &http.Transport{Proxy: nil, DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

type desktopFederationWork struct {
	source, destination desktopFederationSite
	target              string
	// Recheck the published route before issuing a ticket when it was used to select the destination.
	address           string
	publishedMode     *string
	checkDestination  bool // Automatic sign-in requires an explicit signed-out response.
	sourceFingerprint string
}

type desktopFederationOutcome struct {
	reason   string
	signedIn bool
	cookie   *mygo.Cookie
	previous string
}

type desktopFederationTarget struct {
	ID         string `json:"id"`
	Name       string `json:"name,omitempty"`
	ClientURL  string `json:"client_url,omitempty"`
	ClientMode string `json:"client_mode,omitempty"`
}

type desktopFederationTargetTab struct {
	desktopFederationTarget
	Status string `json:"status,omitempty"`
}

// The hint carries no display data and needs no IPC privileges. Coalesce
// hints while reading the source's own account through its native cookie jar.
func (p *panel) refreshDesktopFederationTargets(id string, chat *desktopServerChat) {
	server, ok := p.servers.find(id)
	if p.quitting || p.serverChats[id] != chat || !ok || !server.Enabled || !chat.authenticated || chat.failed {
		return
	}
	chat.federationRevision++
	if chat.federationChecking {
		return
	}
	chat.federationChecking = true
	revision := chat.federationRevision
	site := p.desktopFederationSite(server, chat)
	store := p.desktopFederationCookies()
	p.runDesktopFederation(func(lifetime context.Context) func() {
		ctx, cancel := context.WithTimeout(lifetime, 8*time.Second)
		defer cancel()
		var targets []desktopFederationTarget
		account, err := site.sessions(store)
		fingerprint := desktopFederationFingerprint(account)
		valid := err == nil && len(account) == 0
		if err == nil && len(account) != 0 {
			response, err := site.do(ctx, desktopFederationClient(), "GET", "/api/federation/targets", nil, account, "")
			if err == nil {
				valid = response.StatusCode == http.StatusUnauthorized
				valid = desktopFederationJSON(response, &targets) || valid
			}
			if valid {
				current, err := site.sessions(store)
				valid = err == nil && desktopFederationFingerprint(current) == fingerprint
			}
		}
		return func() {
			chat.federationChecking = false
			if p.quitting || p.serverChats[id] != chat || !chat.authenticated || chat.failed {
				return
			}
			if chat.federationRevision != revision {
				p.refreshDesktopFederationTargets(id, chat)
				return
			}
			if valid {
				chat.federationFingerprint = fingerprint
				p.recordDesktopFederationTargets(chat, targets)
				p.advanceDesktopFederation()
			}
		}
	})
}

// Read the source's current authorization, not an address supplied by a page.
func (s desktopFederationSite) authorization(ctx context.Context, client *http.Client, account []mygo.Cookie, target string) (csrf string, peer desktopFederationTarget, reason string) {
	if len(account) == 0 {
		return "", peer, "source"
	}
	var me struct {
		CSRF    string                    `json:"csrf"`
		Targets []desktopFederationTarget `json:"federation_targets"`
	}
	response, err := s.do(ctx, client, "GET", "/api/me", nil, account, "")
	if err != nil || !desktopFederationJSON(response, &me) || me.CSRF == "" {
		return "", peer, "source"
	}
	for _, peer := range me.Targets {
		if peer.ID == target {
			return me.CSRF, peer, ""
		}
	}
	return "", peer, "unauthorized"
}

// Obtains the destination's session without storing it. The ticket lives only
// in this call: issued with the source's cookies, redeemed with none.
func (w desktopFederationWork) exchange(ctx context.Context, client *http.Client, store desktopFederationCookies) desktopFederationOutcome {
	account, err := w.source.sessions(store)
	if err != nil || (w.sourceFingerprint != "" && desktopFederationFingerprint(account) != w.sourceFingerprint) {
		return desktopFederationOutcome{reason: "source"}
	}
	csrf, peer, reason := w.source.authorization(ctx, client, account, w.target)
	if reason != "" {
		return desktopFederationOutcome{reason: reason}
	}
	if w.address != "" && desktopFederationClientAddress(peer.ClientURL) != w.address ||
		w.publishedMode != nil && peer.ClientMode != *w.publishedMode {
		return desktopFederationOutcome{reason: "unauthorized"}
	}
	if w.destination.identity(ctx, client) != w.target {
		return desktopFederationOutcome{reason: "identity"}
	}
	existing, err := w.destination.sessions(store)
	if err != nil {
		return desktopFederationOutcome{reason: "exchange"}
	}
	outcome := desktopFederationOutcome{previous: desktopFederationFingerprint(existing)}
	if w.checkDestination || len(existing) != 0 {
		response, err := w.destination.do(ctx, client, "GET", "/api/me", nil, existing, "")
		if err != nil {
			return desktopFederationOutcome{reason: "unreachable"}
		}
		response.Body.Close()
		if response.StatusCode == http.StatusOK {
			outcome.signedIn = true
			return outcome
		}
		if response.StatusCode != http.StatusUnauthorized {
			return desktopFederationOutcome{reason: "unreachable"}
		}
	}
	var issued struct {
		Ticket string `json:"ticket"`
	}
	if response, err := w.source.do(ctx, client, "POST", "/api/federation/issue", map[string]string{"target": w.target}, account, csrf); err != nil || !desktopFederationJSON(response, &issued) || !validDesktopFederationTicket(issued.Ticket) {
		return desktopFederationOutcome{reason: "issue"}
	}
	response, err := w.destination.do(ctx, client, "POST", "/api/federation/exchange", map[string]string{"ticket": issued.Ticket}, nil, "")
	if err != nil {
		return desktopFederationOutcome{reason: "exchange"}
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return desktopFederationOutcome{reason: "exchange"}
	}
	base, _ := url.Parse(w.destination.base)
	for _, cookie := range response.Cookies() {
		path := cookie.Path
		if path == "" {
			path = "/"
		}
		// Accept nothing but the destination's own HTTPOnly session cookie.
		if !w.destination.session(cookie.Name) || path != w.destination.path() || !cookie.HttpOnly || cookie.Value == "" || cookie.MaxAge < 0 {
			continue
		}
		expires := cookie.Expires
		if cookie.MaxAge > 0 {
			expires = time.Now().Add(time.Duration(cookie.MaxAge) * time.Second)
		}
		if outcome.cookie != nil {
			return desktopFederationOutcome{reason: "exchange"}
		}
		outcome.cookie = &mygo.Cookie{
			Name: cookie.Name, Value: cookie.Value, Domain: strings.ToLower(base.Hostname()), Path: path,
			Expires: expires, Secure: cookie.Secure && base.Scheme == "https", HTTPOnly: true, SameSite: mygo.CookieSameSiteStrict,
		}
	}
	if outcome.cookie == nil {
		return desktopFederationOutcome{reason: "exchange"}
	}
	return outcome
}

func (p *panel) desktopFederationCookies() desktopFederationCookies {
	if p.federation.cookies != nil {
		return p.federation.cookies
	}
	return desktopWebViewCookies{}
}

// 网络请求和 Cookie 读取在后台执行；面板状态只在 UI 回调中更新。
func (p *panel) runDesktopFederation(job func(context.Context) func()) {
	if p.federation.run != nil {
		p.federation.run(job)
		return
	}
	if p.serverNetworks == nil || p.window == nil {
		return
	}
	p.serverNetworks.run(func(lifetime context.Context) {
		apply := job(lifetime)
		p.window.Update(apply)
	})
}

func (p *panel) desktopFederationIdentity(ctx context.Context, server desktopServer) string {
	if p.federation.identify != nil {
		return p.federation.identify(ctx, server)
	}
	if p.serverNetworks == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	return p.serverNetworks.identity(ctx, server)
}

func (p *panel) emitDesktopFederation(chat *desktopServerChat, state desktopFederationState) {
	if p.federation.emit != nil {
		p.federation.emit(chat, state)
	} else if chat != nil && chat.view != nil {
		_ = desktopFederationChanged.EmitPage(chat.view.Page(), state)
	}
}

func (p *panel) emitDesktopFederationAttempt(a *desktopFederationAttempt, state, reason string) {
	if !a.automatic && p.serverChats[a.origin] == a.originChat {
		p.emitDesktopFederation(a.originChat, desktopFederationState{Target: a.target, Source: a.source, State: state, Reason: reason})
	}
}

func (p *panel) failDesktopFederation(reason string) {
	a := p.federation.attempt
	if a == nil {
		return
	}
	p.federation.attempt = nil
	p.emitDesktopFederationAttempt(a, "failed", reason)
}

func (p *panel) finishDesktopFederation(reload bool) {
	a := p.federation.attempt
	if a == nil {
		return
	}
	p.federation.attempt = nil
	if a.chat != nil && a.exchanged {
		// Its session came from this exchange with the identified server.
		a.chat.serverID, a.chat.federated = a.target, true
	}
	p.emitDesktopFederationAttempt(a, "done", "")
	if p.federation.present != nil {
		if !a.detached {
			p.federation.present(a.server.ID, reload)
		}
		return
	}
	if reload && p.window != nil && a.chat != nil && p.serverChats[a.server.ID] == a.chat {
		// 页面照常验证新写入的 HTTPOnly 会话。
		a.chat.failed = true
		_ = p.loadDesktopServerChat(a.server.ID, a.chat.address)
	}
	// 写入一旦发起就不能取消，但不应再覆盖用户随后的标签选择。
	if !a.automatic && !a.detached && p.window != nil && p.selectedDesktopServer() == a.origin {
		tab := ""
		if a.grouped {
			tab = desktopFederationTabKey(a.source, a.target)
		}
		_ = p.selectDesktopServerEntry(a.server.ID, a.scrollLeft, tab)
	}
}

func (s desktopFederationSite) identity(ctx context.Context, client *http.Client) string {
	response, err := s.do(ctx, client, "GET", "/api/version", nil, nil, "")
	if err != nil {
		return ""
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ""
	}
	return desktopFederationServerID(response.Body)
}

type desktopFederationSource struct {
	id   string
	chat *desktopServerChat
	site desktopFederationSite
}

// Use the dedicated list, since /api/me omits targets both when the list is
// empty and when the server could not load it. An unavailable source is not
// evidence that the user has only one eligible account.
func (s desktopFederationSite) automaticEligibility(ctx context.Context, client *http.Client, account []mygo.Cookie, target, address string) (eligible, known bool) {
	if len(account) == 0 {
		return false, true
	}
	response, err := s.do(ctx, client, "GET", "/api/federation/targets", nil, account, "")
	if err != nil {
		return false, false
	}
	if response.StatusCode == http.StatusUnauthorized {
		response.Body.Close()
		return false, true
	}
	var peers []desktopFederationTarget
	if !desktopFederationJSON(response, &peers) || peers == nil {
		return false, false
	}
	seen := make(map[string]bool, len(peers))
	for _, peer := range peers {
		if !validDesktopFederationID(peer.ID) || seen[peer.ID] {
			return false, false
		}
		seen[peer.ID] = true
		if peer.ID == target && (address == "" || desktopFederationClientAddress(peer.ClientURL) == address) {
			eligible = true
		}
	}
	return eligible, true
}

// Only choose an account when the complete candidate set leaves exactly one.
func desktopAutomaticFederation(ctx context.Context, client *http.Client, store desktopFederationCookies, destination desktopFederationSite, server desktopServer, sources []desktopFederationSource) (desktopFederationSource, desktopFederationWork) {
	existing, err := destination.sessions(store)
	if err != nil {
		return desktopFederationSource{}, desktopFederationWork{}
	}
	response, err := destination.do(ctx, client, "GET", "/api/me", nil, existing, "")
	if err != nil {
		return desktopFederationSource{}, desktopFederationWork{}
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		return desktopFederationSource{}, desktopFederationWork{}
	}
	target := destination.identity(ctx, client)
	if target == "" {
		return desktopFederationSource{}, desktopFederationWork{}
	}
	address := ""
	switch {
	case server.ID == localDesktopServer, desktopRelayServerID(server) == target:
	case server.Mode == "direct" || server.Mode == "tailscale":
		address = desktopFederationClientAddress(server.Address)
		if address == "" {
			return desktopFederationSource{}, desktopFederationWork{}
		}
	default:
		return desktopFederationSource{}, desktopFederationWork{}
	}
	var chosen desktopFederationSource
	var fingerprint string
	for _, source := range sources {
		account, err := source.site.sessions(store)
		if err != nil {
			return desktopFederationSource{}, desktopFederationWork{}
		}
		probe, cancel := context.WithTimeout(ctx, 2*time.Second)
		eligible, known := source.site.automaticEligibility(probe, client, account, target, address)
		cancel()
		if !known {
			return desktopFederationSource{}, desktopFederationWork{}
		}
		if !eligible {
			continue
		}
		if chosen.id != "" {
			return desktopFederationSource{}, desktopFederationWork{}
		}
		chosen, fingerprint = source, desktopFederationFingerprint(account)
	}
	if chosen.id == "" || ctx.Err() != nil {
		return desktopFederationSource{}, desktopFederationWork{}
	}
	return chosen, desktopFederationWork{
		source: chosen.site, destination: destination, target: target, address: address,
		checkDestination: true, sourceFingerprint: fingerprint,
	}
}

func (p *panel) maybeDesktopAutomaticFederation() {
	id := p.federation.selected
	if p.quitting || id == "" || p.selectedDesktopServer() != id {
		return
	}
	chat := p.serverChats[id]
	if chat == nil || !chat.loaded {
		return
	}
	if chat.pageStatus == "connecting" {
		return
	}
	p.federation.selected = ""
	server, ok := p.servers.find(id)
	if !ok || !server.Enabled || (!chat.authenticated && chat.status != "login") || p.federation.attempt != nil {
		return
	}
	var sources []desktopFederationSource
	for _, profile := range p.servers.Entries {
		if source := p.serverChats[profile.ID]; profile.ID != id && profile.Enabled && source != nil && source.authenticated {
			sources = append(sources, desktopFederationSource{profile.ID, source, p.desktopFederationSite(profile, source)})
		}
	}
	if len(sources) == 0 {
		return
	}
	a := &desktopFederationAttempt{automatic: true, server: server, chat: chat, phase: "resolving", started: time.Now()}
	p.federation.attempt = a
	destination, store := p.desktopFederationSite(server, chat), p.desktopFederationCookies()
	p.runDesktopFederation(func(lifetime context.Context) func() {
		ctx, cancel := context.WithTimeout(lifetime, 8*time.Second)
		defer cancel()
		source, work := desktopAutomaticFederation(ctx, desktopFederationClient(), store, destination, server, sources)
		return func() {
			if p.quitting || p.federation.attempt != a || a.phase != "resolving" {
				return
			}
			a.source, a.sourceChat, a.target = source.id, source.chat, work.target
			if source.id == "" || p.desktopFederationBlocked(a) != "" {
				p.failDesktopFederation("cancelled")
				return
			}
			// 刚失效的 Cookie 可能仍对应一个显示为已登录的缓存页面。
			// 后台任务已收到 401，提交前仍会重新核对当前 Cookie。
			a.destinationAuthRevision = chat.statusRevision
			p.exchangeDesktopFederation(a, work)
		}
	})
}

func (p *panel) selectDesktopFederationTab(origin string, page *desktopServerChat, request desktopFederationSelection) {
	if request.source == "" {
		p.startDesktopFederation(origin, page, request.target, true)
		return
	}
	p.startDesktopFederationFrom(origin, page, request.source, p.serverChats[request.source], request.target, true, request.scrollLeft)
}

func (p *panel) startDesktopFederation(source string, chat *desktopServerChat, target string, foreground bool) {
	if foreground {
		p.startDesktopFederationFrom(source, chat, source, chat, target, false, nil)
	}
}

func (p *panel) startDesktopFederationFrom(origin string, page *desktopServerChat, source string, chat *desktopServerChat, target string, grouped bool, scrollLeft *float64) {
	if p.quitting || p.selectedDesktopServer() != origin || p.serverChats[origin] != page {
		return
	}
	a := &desktopFederationAttempt{target: target, source: source, sourceChat: chat, origin: origin, originChat: page, grouped: grouped, scrollLeft: scrollLeft, phase: "resolving", started: time.Now()}
	if !p.desktopFederationSourceCurrent(a) {
		p.emitDesktopFederationAttempt(a, "failed", "source")
		return
	}
	if old := p.federation.attempt; old != nil {
		if old.sourceChat == chat && old.target == target {
			return
		}
		if old.phase == "committing" {
			p.emitDesktopFederationAttempt(a, "failed", "busy")
			return
		}
		p.failDesktopFederation("cancelled")
	}
	p.federation.attempt = a
	p.emitDesktopFederationAttempt(a, "pending", "")
	server, _ := p.servers.find(source)
	site := p.desktopFederationSite(server, chat)
	fingerprint := chat.federationFingerprint
	store := p.desktopFederationCookies()
	candidates := p.desktopFederationCandidates(source)
	p.runDesktopFederation(func(lifetime context.Context) func() {
		ctx, cancel := context.WithTimeout(lifetime, 8*time.Second)
		defer cancel()
		account, err := site.sessions(store)
		current := desktopFederationFingerprint(account)
		if err != nil || grouped && current != fingerprint {
			return func() { p.failDesktopFederationResolution(a, "source") }
		}
		_, peer, reason := site.authorization(ctx, desktopFederationClient(), account, target)
		if reason != "" {
			return func() { p.failDesktopFederationResolution(a, reason) }
		}
		resolution := p.resolveDesktopFederation(ctx, candidates, peer)
		return func() {
			a.sourceFingerprint = current
			p.applyDesktopFederationResolution(a, resolution)
		}
	})
}

func (p *panel) failDesktopFederationResolution(a *desktopFederationAttempt, reason string) {
	if !p.quitting && p.federation.attempt == a && a.phase == "resolving" {
		p.failDesktopFederation(reason)
	}
}

// Every other configured server may be the destination. Each is asked through
// its own configured connection only.
func (p *panel) desktopFederationCandidates(source string) []desktopServer {
	var candidates []desktopServer
	for _, server := range p.servers.Entries {
		if server.ID == source {
			continue
		}
		if server.ID == localDesktopServer {
			if p.service == nil {
				continue
			}
			address, err := p.service.LocalWebURL()
			if err != nil {
				continue
			}
			server.Address, server.Mode, server.Enabled = address, "direct", true
		}
		// A saved inactive endpoint is still useful as a settings fallback,
		// even if the selected connection has not been configured.
		candidates = append(candidates, server)
	}
	return candidates
}

// The identity a native Relay profile is pinned to by its own pairing code.
func desktopRelayServerID(server desktopServer) string {
	if server.Mode != "relay" {
		return ""
	}
	pairing, err := relay.ParsePairing(server.Relay)
	if err != nil {
		return ""
	}
	return pairing.ID()
}

// 与 Web 标签一致：比较 HTTPS 地址时忽略显式的默认端口，保留路径和非默认端口。
// 仅用于互信匹配，不改变已保存的连接地址或 Cookie 作用域。
func desktopFederationClientAddress(clientURL string) string {
	address, err := desktopServerAddress(clientURL)
	if err != nil || !strings.HasPrefix(address, "https://") {
		return ""
	}
	u, _ := url.Parse(address)
	if port := u.Port(); port != "" {
		number, _ := strconv.Atoi(port) // desktopServerAddress 已验证端口。
		u.Host = strings.TrimSuffix(u.Host, ":"+port)
		if number != 443 {
			u.Host += ":" + strconv.Itoa(number)
		}
	}
	return u.String()
}

// A ticket lets its holder sign in as the user, so a destination is never
// chosen by what a saved server says about itself. Only three routes count:
// a native Relay profile whose pairing key derives the target identity, a
// profile using exactly the address the source published for the target, and
// this desktop's own local service.
func (p *panel) resolveDesktopFederation(ctx context.Context, candidates []desktopServer, peer desktopFederationTarget) desktopFederationResolution {
	target, address := peer.ID, desktopFederationClientAddress(peer.ClientURL)
	var resolution desktopFederationResolution
	for _, server := range candidates {
		if server.ID == localDesktopServer {
			continue
		}
		pinned := desktopRelayServerID(server) == target
		published := address != "" && (server.Mode == "direct" || server.Mode == "tailscale") && desktopFederationClientAddress(server.Address) == address
		if (pinned || published) && (!resolution.found || server.Enabled && !resolution.server.Enabled) {
			resolution.server, resolution.found, resolution.address = server, true, ""
			if !pinned {
				resolution.address = address
			}
		}
	}
	if resolution.found {
		return resolution
	}
	for _, server := range candidates {
		if server.ID == localDesktopServer && p.desktopFederationIdentity(ctx, server) == target {
			resolution.server, resolution.found = server, true
			return resolution
		}
	}
	if address == "" {
		return resolution
	}
	for _, server := range candidates {
		// Saved under a connection that is not in use: the user chooses it.
		if server.ID != localDesktopServer && (desktopFederationClientAddress(server.Direct) == address || desktopFederationClientAddress(server.Tailscale) == address) {
			resolution.existing = server.ID
			return resolution
		}
	}
	proposed := desktopServer{Mode: "direct", Direct: address}
	switch peer.ClientMode {
	case "": // Legacy/public HTTPS.
	case "tailscale":
		proposed = desktopServer{Mode: "tailscale", Tailscale: address}
	default:
		return resolution // An unknown transport must not fall back to Direct.
	}
	if server, err := validateDesktopServer(proposed); err == nil {
		resolution.proposed, resolution.address, resolution.publishedMode = server, address, peer.ClientMode
	}
	return resolution
}

func (p *panel) applyDesktopFederationResolution(a *desktopFederationAttempt, resolution desktopFederationResolution) {
	if p.quitting || p.federation.attempt != a || a.phase != "resolving" {
		return
	}
	if !p.desktopFederationSourceCurrent(a) {
		p.failDesktopFederation("source")
		return
	}
	if resolution.found {
		server := resolution.server
		if server.ID != localDesktopServer {
			current, ok := p.servers.find(server.ID)
			if !ok || desktopNetworkKey(current) != desktopNetworkKey(server) {
				p.failDesktopFederation("changed")
				return
			}
			server = current
		}
		a.address = resolution.address
		p.attachDesktopFederation(a, server, p.servers)
		return
	}
	if resolution.proposed.Address == "" {
		p.failDesktopFederation("unconfigured")
		if resolution.existing != "" {
			p.configureDesktopFederationServer(resolution.existing)
			return
		}
		if p.window == nil {
			return
		}
		// Say what is missing instead of trying some other saved server.
		_, _ = mygo.Dialog.Message(mygo.MessageOptions{
			Parent: p.window, Type: mygo.MessageInfo, Title: p.tr("Add server"),
			Message: p.tr("No trusted address is known for this server."),
			Detail:  p.tr("Ask the administrator of the current server to set this server’s client address, or add it here with a Relay pairing code."),
		})
		return
	}
	a.phase = "confirming"
	confirmed := p.confirmDesktopFederationServer(resolution.proposed.Address)
	// A native dialog runs a nested event loop; the request may have ended.
	if p.quitting || p.federation.attempt != a {
		return
	}
	if !p.desktopFederationSourceCurrent(a) {
		p.failDesktopFederation("source")
		return
	}
	if !confirmed {
		p.failDesktopFederation("cancelled")
		return
	}
	next, err := p.servers.save(resolution.proposed)
	if err != nil {
		p.failDesktopFederation("unconfigured")
		return
	}
	a.started, a.address, a.publishedMode = time.Now(), resolution.address, &resolution.publishedMode
	p.attachDesktopFederation(a, next.Entries[len(next.Entries)-1], next)
}

func (p *panel) confirmDesktopFederationServer(address string) bool {
	if p.federation.confirm != nil {
		return p.federation.confirm(address)
	}
	if p.window == nil {
		return false
	}
	result, err := mygo.Dialog.Message(mygo.MessageOptions{
		Parent: p.window, Type: mygo.MessageQuestion, Title: p.tr("Add server"),
		Message: p.tr("Add {address} and sign in with this account?", "address", address),
		Buttons: []string{p.tr("Add server"), p.tr("Cancel")}, CancelButton: 1,
	})
	return err == nil && result.Button == 0
}

func (p *panel) configureDesktopFederationServer(id string) {
	if p.federation.configure != nil {
		p.federation.configure(id)
		return
	}
	p.showServerDialog(id)
}

func (p *panel) desktopFederationTailnetLogin(server desktopServer) string {
	if p.federation.tailnetLogin != nil {
		return p.federation.tailnetLogin(server)
	}
	if p.serverNetworks == nil {
		return ""
	}
	client, err := p.serverNetworks.client(server)
	if err != nil {
		return ""
	}
	var status struct {
		State string `json:"state"`
	}
	value, err := client.Status()
	if err != nil || json.Unmarshal([]byte(value), &status) != nil || status.State != "NeedsLogin" {
		return ""
	}
	address, _ := client.LoginURL()
	return address
}

func (p *panel) prepareDesktopFederationTailnet(a *desktopFederationAttempt) {
	a.phase = "network"
	server := a.server
	p.runDesktopFederation(func(ctx context.Context) func() {
		address := p.desktopFederationTailnetLogin(server)
		return func() {
			if p.quitting || ctx.Err() != nil || p.federation.attempt != a || a.phase != "network" {
				return
			}
			if reason := p.desktopFederationBlocked(a); reason != "" {
				p.failDesktopFederation(reason)
				return
			}
			if !a.chat.failed && a.chat.pageStatus != "error" && a.chat.pageStatus != "disconnected" {
				// An ordinary page/health retry recovered while the node was checked.
				a.phase = "waiting"
				p.advanceDesktopFederation()
				return
			}
			if !tailnet.IsLoginURL(address) {
				p.failDesktopFederation("unreachable")
				p.configureDesktopFederationServer(server.ID)
				return
			}
			// Keep the same profile and request while the user enrolls the node.
			// Normal health checks reload its page when the node can reach it.
			a.phase, a.started = "enrolling", time.Now()
			if p.federation.openLogin != nil {
				p.federation.openLogin(address)
			} else {
				p.openChatLink(address)
			}
		}
	})
}

func (p *panel) attachDesktopFederation(a *desktopFederationAttempt, server desktopServer, profiles desktopServers) {
	a.server, a.phase = server, "waiting"
	if server.ID != localDesktopServer && !server.Enabled {
		// Asking to sign in is an explicit request to connect, as Connect is.
		server.Enabled = true
		next, err := profiles.save(server)
		if err == nil {
			a.server, _ = next.find(server.ID)
			err = p.setDesktopServers(next)
		}
		if err != nil {
			p.failDesktopFederation("unconfigured")
			return
		}
	}
	if p.federation.attempt != a {
		return
	}
	if chat := p.serverChats[server.ID]; chat == nil {
		p.connectEnabledDesktopServers()
	} else if !chat.authenticated && (chat.failed || chat.pageStatus == "error" || chat.pageStatus == "disconnected") && p.window != nil {
		// The destination just answered; replace its stale failed page.
		chat.failed = true
		_ = p.loadDesktopServerChat(server.ID, chat.address)
	}
	p.advanceDesktopFederation()
}

func (p *panel) desktopFederationSourceCurrent(a *desktopFederationAttempt) bool {
	if a.automatic {
		if p.selectedDesktopServer() != a.server.ID {
			return false
		}
		// 查找候选来源时还未绑定某一个账号。
		if a.sourceChat == nil {
			return true
		}
	} else if p.selectedDesktopServer() != a.origin || p.serverChats[a.origin] != a.originChat {
		return false
	}
	if a.sourceChat == nil || p.serverChats[a.source] != a.sourceChat || !a.sourceChat.authenticated || a.sourceChat.failed {
		return false
	}
	if a.grouped {
		return slices.ContainsFunc(a.sourceChat.federationTargets, func(peer desktopFederationTarget) bool { return peer.ID == a.target })
	}
	return true
}

// A reason to abandon the attempt: the source or destination changed.
func (p *panel) desktopFederationBlocked(a *desktopFederationAttempt) string {
	if !p.desktopFederationSourceCurrent(a) {
		return "source"
	}
	if a.server.ID != localDesktopServer {
		current, ok := p.servers.find(a.server.ID)
		if !ok || !current.Enabled || desktopNetworkKey(current) != desktopNetworkKey(a.server) {
			return "changed"
		}
	}
	chat := p.serverChats[a.server.ID]
	if a.chat == nil {
		a.chat = chat
	}
	if chat == nil || chat != a.chat {
		return "changed"
	}
	return ""
}

func (p *panel) advanceDesktopFederation() {
	a := p.federation.attempt
	if a == nil {
		return
	}
	if p.quitting {
		p.federation.attempt = nil
		return
	}
	if a.phase == "confirming" {
		return
	}
	if a.phase == "committing" {
		// MyGo 等待原生 Cookie 写入时仍会处理窗口事件。写入已经发起，
		// 不再宣告取消，只阻止完成后自动切换到目标服务器。
		a.detached = a.detached || p.desktopFederationBlocked(a) != ""
		return
	}
	if time.Since(a.started) > desktopFederationTimeout {
		configure := a.phase == "enrolling" && p.desktopFederationBlocked(a) == ""
		p.failDesktopFederation("timeout")
		if configure {
			p.configureDesktopFederationServer(a.server.ID)
		}
		return
	}
	if a.phase == "resolving" {
		if !p.desktopFederationSourceCurrent(a) {
			p.failDesktopFederation("source")
		}
		return
	}
	if a.chat == nil && p.serverChats[a.server.ID] == nil {
		// Its page is still being created; the deadline bounds this wait.
		return
	}
	if reason := p.desktopFederationBlocked(a); reason != "" {
		p.failDesktopFederation(reason)
		return
	}
	chat := a.chat
	if a.phase != "waiting" && a.phase != "enrolling" {
		return
	}
	if chat.failed || chat.pageStatus == "error" || chat.pageStatus == "disconnected" {
		if a.phase == "enrolling" {
			return // Health checks may recover the page after node enrollment.
		}
		if a.server.Mode == "tailscale" {
			p.prepareDesktopFederationTailnet(a)
			return
		}
		p.failDesktopFederation("unreachable")
		return
	}
	if !chat.loaded || (!chat.authenticated && chat.pageStatus != "login") {
		return
	}
	source, _ := p.servers.find(a.source)
	a.destinationAuthRevision = chat.statusRevision
	work := desktopFederationWork{
		source:            p.desktopFederationSite(source, a.sourceChat),
		destination:       p.desktopFederationSite(a.server, chat),
		target:            a.target,
		address:           a.address,
		publishedMode:     a.publishedMode,
		checkDestination:  a.grouped,
		sourceFingerprint: a.sourceFingerprint,
	}
	p.exchangeDesktopFederation(a, work)
}

func (p *panel) exchangeDesktopFederation(a *desktopFederationAttempt, work desktopFederationWork) {
	store := p.desktopFederationCookies()
	a.phase = "working"
	p.runDesktopFederation(func(lifetime context.Context) func() {
		ctx, cancel := context.WithTimeout(lifetime, 20*time.Second)
		outcome := work.exchange(ctx, desktopFederationClient(), store)
		cancel()
		return func() { p.applyDesktopFederationOutcome(a, work, outcome) }
	})
}

func (p *panel) applyDesktopFederationOutcome(a *desktopFederationAttempt, work desktopFederationWork, outcome desktopFederationOutcome) {
	if p.quitting || p.federation.attempt != a || a.phase != "working" {
		return
	}
	reason := outcome.reason
	if reason == "" {
		reason = p.desktopFederationBlocked(a)
	}
	if reason != "" {
		p.failDesktopFederation(reason)
		return
	}
	if outcome.signedIn || a.destinationSignedIn() {
		// An account is already signed in there: keep it and discard ours.
		p.finishDesktopFederation(!a.chat.authenticated)
		return
	}
	store := p.desktopFederationCookies()
	a.phase = "storing"
	p.runDesktopFederation(func(context.Context) func() {
		current, err := work.destination.sessions(store)
		sourceChanged := false
		if work.sourceFingerprint != "" {
			source, sourceErr := work.source.sessions(store)
			sourceChanged = sourceErr != nil || desktopFederationFingerprint(source) != work.sourceFingerprint
		}
		return func() {
			if p.quitting || p.federation.attempt != a || a.phase != "storing" {
				return
			}
			if time.Since(a.started) > desktopFederationTimeout {
				p.failDesktopFederation("timeout")
				return
			}
			if reason := p.desktopFederationBlocked(a); reason != "" {
				p.failDesktopFederation(reason)
				return
			}
			if a.destinationSignedIn() {
				p.finishDesktopFederation(false)
				return
			}
			if sourceChanged {
				p.failDesktopFederation("source")
				return
			}
			if err != nil || outcome.cookie == nil {
				p.failDesktopFederation("exchange")
				return
			}
			if desktopFederationFingerprint(current) != outcome.previous {
				p.failDesktopFederation("changed")
				return
			}
			// 后台任务只读 Cookie；在 UI 线程确认尝试仍有效后才发起写入。
			a.phase = "committing"
			writeErr := store.Set(*outcome.cookie)
			if p.quitting || p.federation.attempt != a {
				return
			}
			if writeErr != nil {
				p.failDesktopFederation("exchange")
				return
			}
			a.exchanged = true
			p.finishDesktopFederation(true)
		}
	})
}

// Tabs use an identity only to hide sign-in entries for connected servers.
// Only this desktop's own service is asked; remote ones come from a Relay
// pairing key or a completed sign-in.
func (p *panel) learnDesktopServerIdentity(id string, chat *desktopServerChat) {
	if id != localDesktopServer || p.quitting || p.service == nil || p.serverNetworks == nil || p.window == nil || chat.identityChecking {
		return
	}
	address, err := p.service.LocalWebURL()
	if err != nil {
		return
	}
	chat.identityChecking = true
	p.serverNetworks.run(func(lifetime context.Context) {
		identity := p.desktopFederationIdentity(lifetime, desktopServer{ID: id, Mode: "direct", Address: address})
		p.window.Update(func() {
			chat.identityChecking = false
			if p.quitting || p.serverChats[id] != chat || chat.serverID == identity {
				return
			}
			chat.serverID = identity
			p.sendDesktopServers()
		})
	})
}
