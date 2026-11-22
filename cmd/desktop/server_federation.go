package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"spool/relay"
)

const desktopFederationPrefix = "/__desktop/federation-"
const desktopFederationLoginPath = desktopFederationPrefix + "login"
const desktopFederationTimeout = 45 * time.Second

// Progress shown by the page that asked. It never carries a ticket, cookie or
// account: the whole exchange stays in native code.
type desktopFederationState struct {
	Target string `json:"target"`
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

var desktopFederationChanged = mygo.NewEvent[desktopFederationState]("spool:federation")

type desktopFederationAttempt struct {
	target string
	// The published address the destination was chosen by, if any.
	address    string
	source     string
	sourceChat *desktopServerChat
	// resolving, confirming, waiting, working or storing.
	phase     string
	exchanged bool
	server    desktopServer
	chat      *desktopServerChat
	started   time.Time
}

type desktopFederationResolution struct {
	server   desktopServer
	found    bool
	address  string
	proposed string
	existing string
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
	// Test seams; nil selects the native implementation.
	emit     func(*desktopServerChat, desktopFederationState)
	identify func(context.Context, desktopServer) string
	confirm  func(address string) bool
	present  func(id string, reload bool)
	run      func(func(context.Context) func())
	cookies  desktopFederationCookies
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

// The only federation request a page can make: which authorized server to
// sign in to. reserved paths never load as documents, even when malformed.
func desktopFederationNavigation(address, navigation string) (target string, reserved, ok bool) {
	if !chatNavigationAllowed(address, navigation) {
		return "", false, false
	}
	u, _ := url.Parse(navigation)
	if !strings.HasPrefix(u.Path, desktopFederationPrefix) {
		return "", false, false
	}
	params, err := url.ParseQuery(u.RawQuery)
	if err != nil || u.Path != desktopFederationLoginPath || u.RawPath != "" || u.Fragment != "" || len(params) != 1 || len(params["target"]) != 1 {
		return "", true, false
	}
	target = params.Get("target")
	if !validDesktopFederationID(target) {
		return "", true, false
	}
	return target, true, true
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
	values := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		values = append(values, cookie.Name+"="+cookie.Value)
	}
	sort.Strings(values)
	return strings.Join(values, "\n")
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
	return response.StatusCode == http.StatusOK && json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(value) == nil
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
	// Set when the destination was chosen by its published client address.
	address string
}

type desktopFederationOutcome struct {
	reason   string
	signedIn bool
	cookies  []mygo.Cookie
	previous string
}

// Read the source's current authorization, not an address supplied by a page.
func (s desktopFederationSite) authorization(ctx context.Context, client *http.Client, account []mygo.Cookie, target string) (csrf, address, reason string) {
	if len(account) == 0 {
		return "", "", "source"
	}
	var me struct {
		CSRF    string `json:"csrf"`
		Targets []struct {
			ID        string `json:"id"`
			ClientURL string `json:"client_url"`
		} `json:"federation_targets"`
	}
	response, err := s.do(ctx, client, "GET", "/api/me", nil, account, "")
	if err != nil || !desktopFederationJSON(response, &me) || me.CSRF == "" {
		return "", "", "source"
	}
	for _, peer := range me.Targets {
		if peer.ID == target {
			return me.CSRF, desktopFederationClientAddress(peer.ClientURL), ""
		}
	}
	return "", "", "unauthorized"
}

// Obtains the destination's session without storing it. The ticket lives only
// in this call: issued with the source's cookies, redeemed with none.
func (w desktopFederationWork) exchange(ctx context.Context, client *http.Client, store desktopFederationCookies) desktopFederationOutcome {
	account, err := w.source.sessions(store)
	if err != nil {
		return desktopFederationOutcome{reason: "source"}
	}
	csrf, address, reason := w.source.authorization(ctx, client, account, w.target)
	if reason != "" {
		return desktopFederationOutcome{reason: reason}
	}
	if w.address != "" && address != w.address {
		return desktopFederationOutcome{reason: "unauthorized"}
	}
	response, err := w.destination.do(ctx, client, "GET", "/api/version", nil, nil, "")
	if err != nil {
		return desktopFederationOutcome{reason: "unreachable"}
	}
	identity := ""
	if response.StatusCode == http.StatusOK {
		identity = desktopFederationServerID(response.Body)
	}
	response.Body.Close()
	if identity != w.target {
		return desktopFederationOutcome{reason: "identity"}
	}
	existing, err := w.destination.sessions(store)
	if err != nil {
		return desktopFederationOutcome{reason: "exchange"}
	}
	outcome := desktopFederationOutcome{previous: desktopFederationFingerprint(existing)}
	if len(existing) != 0 {
		response, err = w.destination.do(ctx, client, "GET", "/api/me", nil, existing, "")
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
	if response, err = w.source.do(ctx, client, "POST", "/api/federation/issue", map[string]string{"target": w.target}, account, csrf); err != nil || !desktopFederationJSON(response, &issued) || !validDesktopFederationTicket(issued.Ticket) {
		return desktopFederationOutcome{reason: "issue"}
	}
	response, err = w.destination.do(ctx, client, "POST", "/api/federation/exchange", map[string]string{"ticket": issued.Ticket}, nil, "")
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
		outcome.cookies = append(outcome.cookies, mygo.Cookie{
			Name: cookie.Name, Value: cookie.Value, Domain: strings.ToLower(base.Hostname()), Path: path,
			Expires: expires, Secure: cookie.Secure && base.Scheme == "https", HTTPOnly: true, SameSite: mygo.CookieSameSiteStrict,
		})
	}
	if len(outcome.cookies) == 0 {
		return desktopFederationOutcome{reason: "exchange"}
	}
	return outcome
}

// Stores the new session unless an account appeared there in the meantime.
func (w desktopFederationWork) store(store desktopFederationCookies, outcome desktopFederationOutcome) string {
	current, err := w.destination.sessions(store)
	if err != nil {
		return "exchange"
	}
	if desktopFederationFingerprint(current) != outcome.previous {
		return "changed"
	}
	for _, cookie := range outcome.cookies {
		if err := store.Set(cookie); err != nil {
			return "exchange"
		}
	}
	return ""
}

func (p *panel) desktopFederationCookies() desktopFederationCookies {
	if p.federation.cookies != nil {
		return p.federation.cookies
	}
	return desktopWebViewCookies{}
}

// Cookie and network waits belong to a worker; only apply touches the panel.
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

func (p *panel) failDesktopFederation(reason string) {
	a := p.federation.attempt
	if a == nil {
		return
	}
	p.federation.attempt = nil
	if p.serverChats[a.source] == a.sourceChat {
		p.emitDesktopFederation(a.sourceChat, desktopFederationState{Target: a.target, State: "failed", Reason: reason})
	}
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
	if p.serverChats[a.source] == a.sourceChat {
		p.emitDesktopFederation(a.sourceChat, desktopFederationState{Target: a.target, State: "done"})
	}
	if p.federation.present != nil {
		p.federation.present(a.server.ID, reload)
		return
	}
	if reload && p.window != nil && a.chat != nil && p.serverChats[a.server.ID] == a.chat {
		// The page validates its new HTTPOnly session as on any other start.
		a.chat.failed = true
		_ = p.loadDesktopServerChat(a.server.ID, a.chat.address)
	}
	// Follow the request only while its source is still presented.
	if p.window != nil && p.selectedDesktopServer() == a.source {
		_ = p.selectDesktopServer(a.server.ID)
	}
}

// Only the presented page of a signed-in account may start a sign-in.
func (p *panel) startDesktopFederation(source string, chat *desktopServerChat, target string, foreground bool) {
	if p.quitting || !foreground {
		return
	}
	if !chat.authenticated || chat.status != "connected" {
		p.emitDesktopFederation(chat, desktopFederationState{Target: target, State: "failed", Reason: "source"})
		return
	}
	if old := p.federation.attempt; old != nil {
		if old.sourceChat == chat && old.target == target {
			return
		}
		p.failDesktopFederation("cancelled")
	}
	a := &desktopFederationAttempt{target: target, source: source, sourceChat: chat, phase: "resolving", started: time.Now()}
	p.federation.attempt = a
	p.emitDesktopFederation(chat, desktopFederationState{Target: a.target, State: "pending"})
	server, _ := p.servers.find(source)
	site := p.desktopFederationSite(server, chat)
	store := p.desktopFederationCookies()
	candidates := p.desktopFederationCandidates(source)
	p.runDesktopFederation(func(lifetime context.Context) func() {
		ctx, cancel := context.WithTimeout(lifetime, 8*time.Second)
		defer cancel()
		account, err := site.sessions(store)
		if err != nil {
			return func() { p.failDesktopFederationResolution(a, "source") }
		}
		_, address, reason := site.authorization(ctx, desktopFederationClient(), account, target)
		if reason != "" {
			return func() { p.failDesktopFederationResolution(a, reason) }
		}
		resolution := p.resolveDesktopFederation(ctx, candidates, target, address)
		return func() { p.applyDesktopFederationResolution(a, resolution) }
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
		if server.Address != "" {
			candidates = append(candidates, server)
		}
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

// The address the source server's administrator published for a target.
func desktopFederationClientAddress(clientURL string) string {
	address, err := desktopServerAddress(clientURL)
	if clientURL == "" || err != nil || !strings.HasPrefix(address, "https://") {
		return ""
	}
	return address
}

// A ticket lets its holder sign in as the user, so a destination is never
// chosen by what a saved server says about itself. Only three routes count:
// a native Relay profile whose pairing key derives the target identity, a
// profile using exactly the address the source published for the target, and
// this desktop's own local service.
func (p *panel) resolveDesktopFederation(ctx context.Context, candidates []desktopServer, target, clientURL string) desktopFederationResolution {
	address := desktopFederationClientAddress(clientURL)
	var resolution desktopFederationResolution
	for _, server := range candidates {
		if server.ID == localDesktopServer {
			continue
		}
		pinned := desktopRelayServerID(server) == target
		published := address != "" && (server.Mode == "direct" || server.Mode == "tailscale") && server.Address == address
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
		if server.ID != localDesktopServer && (server.Direct == address || server.Tailscale == address) {
			resolution.existing = server.ID
			return resolution
		}
	}
	resolution.proposed = address
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
	if resolution.proposed == "" {
		p.failDesktopFederation("unconfigured")
		if p.window == nil {
			return
		}
		if resolution.existing != "" {
			p.showServerDialog(resolution.existing)
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
	confirmed := p.confirmDesktopFederationServer(resolution.proposed)
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
	next, err := p.servers.save(desktopServer{Direct: resolution.proposed, Mode: "direct"})
	if err != nil {
		p.failDesktopFederation("unconfigured")
		return
	}
	a.started, a.address = time.Now(), resolution.proposed
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
	return p.selectedDesktopServer() == a.source && p.serverChats[a.source] == a.sourceChat && a.sourceChat.authenticated
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
	if time.Since(a.started) > desktopFederationTimeout {
		p.failDesktopFederation("timeout")
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
	if a.phase != "waiting" {
		return
	}
	if chat.failed || chat.pageStatus == "error" || chat.pageStatus == "disconnected" {
		p.failDesktopFederation("unreachable")
		return
	}
	if !chat.loaded || (!chat.authenticated && chat.pageStatus != "login") {
		return
	}
	source, _ := p.servers.find(a.source)
	work := desktopFederationWork{
		source:      p.desktopFederationSite(source, a.sourceChat),
		destination: p.desktopFederationSite(a.server, chat),
		target:      a.target,
		address:     a.address,
	}
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
	if outcome.signedIn || a.chat.authenticated {
		// An account is already signed in there: keep it and discard ours.
		p.finishDesktopFederation(!a.chat.authenticated)
		return
	}
	store := p.desktopFederationCookies()
	a.phase = "storing"
	p.runDesktopFederation(func(context.Context) func() {
		reason := work.store(store, outcome)
		return func() {
			if p.quitting || p.federation.attempt != a || a.phase != "storing" {
				return
			}
			if reason == "" {
				reason = p.desktopFederationBlocked(a)
			}
			if reason != "" {
				p.failDesktopFederation(reason)
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
