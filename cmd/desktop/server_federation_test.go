package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/egoist/mygo"
)

var testFederationTarget = strings.Repeat("b", 52)
var testFederationTicket = "spool1." + strings.Repeat("a", 52) + "." + base64.RawURLEncoding.EncodeToString(make([]byte, 32))

type fakeFederationCookies struct {
	mu         sync.Mutex
	items      []mygo.Cookie
	sets       int
	onSet      func()
	unreadable string
}

func (f *fakeFederationCookies) List(address string) ([]mygo.Cookie, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if address == f.unreadable && address != "" {
		return nil, errors.New("cookie store unavailable")
	}
	return append([]mygo.Cookie(nil), f.items...), nil
}

func (f *fakeFederationCookies) Set(cookie mygo.Cookie) error {
	if f.onSet != nil {
		f.onSet()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sets++
	for i, old := range f.items {
		if old.Name == cookie.Name && old.Path == cookie.Path {
			f.items[i] = cookie
			return nil
		}
	}
	f.items = append(f.items, cookie)
	return nil
}

func TestDesktopFederationNavigationBindsTheSourceAndTarget(t *testing.T) {
	const page = "http://127.0.0.1:4000/web/abc/"
	login, reserved, ok := desktopFederationNavigation(page, "http://127.0.0.1:4000/__desktop/federation-login?source=remote-a&target="+testFederationTarget+"&scroll_left=123.5")
	if !reserved || !ok || login.source != "remote-a" || login.target != testFederationTarget || login.scrollLeft == nil || *login.scrollLeft != 123.5 {
		t.Fatalf("selection = %+v reserved=%v ok=%v", login, reserved, ok)
	}
	for _, target := range []string{
		"http://127.0.0.1:4000/__desktop/federation-login",
		"http://127.0.0.1:4000/__desktop/federation-login?target=short",
		"http://127.0.0.1:4000/__desktop/federation-login?target=" + testFederationTarget + "&source=remote-a&source=remote-d",
		"http://127.0.0.1:4000/__desktop/federation-login?target=" + testFederationTarget + "&source=remote-a&scroll_left=NaN",
		"http://127.0.0.1:4000/__desktop/federation-login?target=" + testFederationTarget + "&target=" + testFederationTarget,
		"http://127.0.0.1:4000/__desktop/federation-login?target=" + testFederationTarget + "&ticket=" + testFederationTicket,
		"http://127.0.0.1:4000/__desktop/federation-login?target=" + testFederationTarget + "#x",
		"http://127.0.0.1:4000/__desktop/federation-login?target=" + testFederationTarget + "&client_url=https%3A%2F%2Fb.example",
		"http://127.0.0.1:4000/__desktop/federation-ticket?ticket=" + testFederationTicket,
	} {
		// Still reserved: a malformed hint must never load as a document.
		if login, reserved, ok := desktopFederationNavigation(page, target); !reserved || ok || login.target != "" {
			t.Errorf("%s: login=%+v reserved=%v ok=%v", target, login, reserved, ok)
		}
	}
	if _, reserved, ok := desktopFederationNavigation(page, "http://127.0.0.1:4001/__desktop/federation-login?target="+testFederationTarget); reserved || ok {
		t.Fatal("another origin started a federated sign-in")
	}
}

func TestDesktopFederationNeverChoosesADestinationByItsOwnClaim(t *testing.T) {
	profiles, err := defaultDesktopServers().save(desktopServer{Direct: "https://other.example", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	profiles, err = profiles.save(desktopServer{Direct: "https://b.example", Tailscale: "https://b.example.ts.net"})
	if err != nil {
		t.Fatal(err)
	}
	other, published := profiles.Entries[1], profiles.Entries[2]
	asked := 0
	p := &panel{servers: profiles}
	// Every server claims to be the target; only this desktop's own may be asked.
	p.federation.identify = func(_ context.Context, server desktopServer) string {
		asked++
		if server.ID != localDesktopServer {
			t.Errorf("asked %q for its identity", server.Address)
		}
		return ""
	}
	resolve := func(candidates []desktopServer, address, mode string) desktopFederationResolution {
		return p.resolveDesktopFederation(t.Context(), candidates, desktopFederationTarget{ID: testFederationTarget, ClientURL: address, ClientMode: mode})
	}
	candidates := []desktopServer{other, published}
	if got := resolve(candidates, "", ""); got.found || got.proposed.Address != "" || got.existing != "" {
		t.Fatalf("resolved without a trusted route: %+v", got)
	}
	if got := resolve(candidates, "http://b.example", ""); got.found || got.proposed.Address != "" {
		t.Fatalf("accepted a plaintext address: %+v", got)
	}
	got := resolve(candidates, "https://B.example:443", "tailscale")
	if !got.found || got.server.ID != published.ID || got.address != "https://b.example/" {
		t.Fatalf("published address = %+v", got)
	}
	withPort := published
	withPort.Direct, withPort.Address = "https://b.example:443/", "https://b.example:443/"
	got = resolve([]desktopServer{withPort}, "https://b.example", "")
	if !got.found || got.server != withPort || got.address != "https://b.example/" {
		t.Fatalf("saved connection with default port = %+v", got)
	}
	// A saved but unused connection is offered to the user, not switched to.
	got = resolve(candidates, "https://b.example.ts.net:443", "tailscale")
	if got.found || got.existing != published.ID || got.proposed.Address != "" {
		t.Fatalf("unused connection = %+v", got)
	}
	tailnet := published.connection("tailscale")
	got = resolve([]desktopServer{tailnet}, "https://b.example.ts.net:443", "")
	if !got.found || got.server != tailnet || got.address != "https://b.example.ts.net/" {
		t.Fatalf("active Tailscale connection = %+v", got)
	}
	got = resolve(candidates, "https://b.example:8443", "")
	if got.found || got.existing != "" || got.proposed.Direct != "https://b.example:8443/" || got.proposed.Mode != "direct" {
		t.Fatalf("different HTTPS port = %+v", got)
	}
	got = resolve(candidates, "https://new.example", "tailscale")
	if got.found || got.proposed.Tailscale != "https://new.example/" || got.proposed.Mode != "tailscale" || got.proposed.Direct != "" {
		t.Fatalf("new Tailscale route = %+v", got)
	}
	for _, got := range []desktopFederationResolution{
		resolve(candidates, "https://new.example", "future-mode"),
		resolve(candidates, "https://new.example/web/"+testFederationTarget+"/", "tailscale"),
	} {
		if got.found || got.proposed.Address != "" {
			t.Fatalf("invalid native route fell back to Direct: %+v", got)
		}
	}
	local := desktopServer{ID: localDesktopServer, Mode: "direct", Address: "http://127.0.0.1:1/", Enabled: true}
	p.federation.identify = func(context.Context, desktopServer) string { return testFederationTarget }
	if got = resolve(append(candidates, local), "", ""); !got.found || got.server.ID != localDesktopServer {
		t.Fatalf("local service = %+v", got)
	}
	if asked != 0 {
		t.Fatalf("identity requests = %d", asked)
	}
}

type federationFixture struct {
	t                   *testing.T
	gateway             *desktopServerGateway
	sourceProfile       desktopServer
	destinationProfile  desktopServer
	destinationURL      string
	work                desktopFederationWork
	store               *fakeFederationCookies
	mu                  sync.Mutex
	clientURL           string
	clientMode          string
	targetsStatus       int
	targetsBody         string
	destinationID       string
	issued              int
	exchanged           int
	sourceSawOthers     bool
	destinationSawOther bool
}

func newFederationFixture(t *testing.T) *federationFixture {
	f := &federationFixture{t: t, store: &fakeFederationCookies{}, destinationID: testFederationTarget}
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		cookies := r.Cookies()
		signedIn := len(cookies) == 1 && cookies[0].Name == "spool_session" && cookies[0].Value == "source-session"
		if len(cookies) > 1 || len(cookies) == 1 && !signedIn {
			f.sourceSawOthers = true
		}
		if !signedIn {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /api/me":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"csrf": "source-csrf", "federation_targets": []desktopFederationTarget{{ID: testFederationTarget, ClientURL: f.clientURL, ClientMode: f.clientMode}},
			})
		case "GET /api/federation/targets":
			if f.targetsStatus != 0 {
				w.WriteHeader(f.targetsStatus)
				return
			}
			if f.targetsBody != "" {
				_, _ = w.Write([]byte(f.targetsBody))
				return
			}
			_ = json.NewEncoder(w).Encode([]desktopFederationTarget{{ID: testFederationTarget, ClientURL: f.clientURL, ClientMode: f.clientMode}})
		case "POST /api/federation/issue":
			var input struct{ Target string }
			if r.Header.Get("X-CSRF-Token") != "source-csrf" || json.NewDecoder(r.Body).Decode(&input) != nil || input.Target != testFederationTarget {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			f.issued++
			_ = json.NewEncoder(w).Encode(map[string]string{"ticket": testFederationTicket})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(source.Close)
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, cookie := range r.Cookies() {
			if cookie.Name != "spool_session" || cookie.Value == "source-session" {
				f.destinationSawOther = true
			}
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /api/version":
			_ = json.NewEncoder(w).Encode(map[string]string{"version": "test", "server_id": f.destinationID})
		case "GET /api/me":
			if cookie, err := r.Cookie("spool_session"); err != nil || cookie.Value != "existing-session" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"csrf": "destination-csrf"})
		case "POST /api/federation/exchange":
			var input struct{ Ticket string }
			if len(r.Cookies()) != 0 || json.NewDecoder(r.Body).Decode(&input) != nil || input.Ticket != testFederationTicket {
				f.destinationSawOther = f.destinationSawOther || len(r.Cookies()) != 0
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			f.exchanged++
			http.SetCookie(w, &http.Cookie{Name: "spool_session", Value: "federated-session", Path: "/", HttpOnly: true, MaxAge: 3600})
			// Nothing but the session cookie may reach the WebView store.
			http.SetCookie(w, &http.Cookie{Name: "tracking", Value: "x", Path: "/"})
			_ = json.NewEncoder(w).Encode(map[string]string{"csrf": "new"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(destination.Close)
	profiles, err := defaultDesktopServers().save(desktopServer{Direct: source.URL, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	profiles, err = profiles.save(desktopServer{Direct: destination.URL, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	f.gateway, err = startDesktopServerGateway(nil, profiles)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.gateway.close)
	p := &panel{}
	site := func(server desktopServer) desktopFederationSite {
		return p.desktopFederationSite(server, &desktopServerChat{address: f.gateway.url + desktopServerMount(server)})
	}
	f.sourceProfile, f.destinationProfile = profiles.Entries[1], profiles.Entries[2]
	f.destinationURL = destination.URL
	f.work = desktopFederationWork{source: site(f.sourceProfile), destination: site(f.destinationProfile), target: testFederationTarget}
	f.store.items = []mygo.Cookie{
		{Name: f.work.source.prefix + "spool_session", Value: "source-session", Path: f.work.source.path(), HTTPOnly: true},
		// The local service shares the loopback host with every mount.
		{Name: "spool_session", Value: "local-session", Path: "/", HTTPOnly: true},
	}
	return f
}

func (f *federationFixture) exchange() desktopFederationOutcome {
	return f.work.exchange(context.Background(), desktopFederationClient(), f.store)
}

func TestDesktopFederationExchangesThroughEachServersOwnMount(t *testing.T) {
	f := newFederationFixture(t)
	outcome := f.exchange()
	if outcome.reason != "" || outcome.signedIn || outcome.cookie == nil {
		t.Fatalf("outcome = %+v", outcome)
	}
	cookie := outcome.cookie
	if cookie.Name != f.work.destination.prefix+"spool_session" || cookie.Value != "federated-session" || cookie.Path != f.work.destination.path() ||
		cookie.Domain != "127.0.0.1" || !cookie.HTTPOnly || cookie.Secure || cookie.SameSite != mygo.CookieSameSiteStrict || cookie.Expires.IsZero() {
		t.Fatalf("session cookie = %+v", cookie)
	}
	if f.store.sets != 0 {
		t.Fatal("stored a session before the panel validated the attempt")
	}
	if f.issued != 1 || f.exchanged != 1 || f.sourceSawOthers || f.destinationSawOther {
		t.Fatalf("issued=%d exchanged=%d source leak=%v destination leak=%v", f.issued, f.exchanged, f.sourceSawOthers, f.destinationSawOther)
	}
}

func TestDesktopFederationIssuesNoTicketForAnUnconfirmedDestination(t *testing.T) {
	for name, change := range map[string]func(*federationFixture){
		"address not in the authorized record": func(f *federationFixture) {
			f.work.address, f.clientURL = "https://b.example/", "https://elsewhere.example"
		},
		"record without an address": func(f *federationFixture) { f.work.address = "https://b.example/" },
		"published transport changed": func(f *federationFixture) {
			tailscale := "tailscale"
			f.work.address, f.clientURL, f.work.publishedMode = "https://b.example/", "https://b.example", &tailscale
		},
		"different identity on the published Tailscale route": func(f *federationFixture) {
			tailscale := "tailscale"
			f.work.address, f.clientURL, f.work.publishedMode, f.clientMode = "https://b.example/", "https://b.example", &tailscale, tailscale
			f.destinationID = strings.Repeat("c", 52)
		},
		"target not authorized":    func(f *federationFixture) { f.work.target = strings.Repeat("c", 52) },
		"signed out of the source": func(f *federationFixture) { f.store.items = nil },
	} {
		t.Run(name, func(t *testing.T) {
			f := newFederationFixture(t)
			change(f)
			if outcome := f.exchange(); outcome.reason == "" || outcome.cookie != nil || f.issued != 0 || f.exchanged != 0 {
				t.Fatalf("outcome=%+v issued=%d exchanged=%d", outcome, f.issued, f.exchanged)
			}
		})
	}
	f := newFederationFixture(t)
	f.work.address, f.clientURL = "https://b.example/", "https://B.example:443"
	if outcome := f.exchange(); outcome.reason != "" || f.issued != 1 {
		t.Fatalf("matching record: outcome=%+v issued=%d", outcome, f.issued)
	}
}

func TestDesktopFederationKeepsAnExistingDestinationSession(t *testing.T) {
	f := newFederationFixture(t)
	existing := mygo.Cookie{Name: f.work.destination.prefix + "spool_session", Value: "existing-session", Path: f.work.destination.path(), HTTPOnly: true}
	f.store.items = append(f.store.items, existing)
	if outcome := f.exchange(); outcome.reason != "" || !outcome.signedIn || f.issued != 0 || f.exchanged != 0 {
		t.Fatalf("outcome=%+v issued=%d exchanged=%d", outcome, f.issued, f.exchanged)
	}
}

type federationPanel struct {
	*panel
	source, destination desktopServer
	sourceChat, chat    *desktopServerChat
	jobs                []func(context.Context) func()
	events              []desktopFederationState
	presented           []string
	store               *fakeFederationCookies
}

func newFederationPanel(t *testing.T) *federationPanel {
	profiles, err := defaultDesktopServers().save(desktopServer{Direct: "https://a.example", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	profiles, err = profiles.save(desktopServer{Direct: "https://b.example", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	f := &federationPanel{source: profiles.Entries[1], destination: profiles.Entries[2], store: &fakeFederationCookies{}}
	profiles.Selected = f.source.ID
	f.sourceChat = &desktopServerChat{status: "connected", pageStatus: "connected", authenticated: true, loaded: true, address: "http://127.0.0.1:1/web/a/"}
	f.chat = &desktopServerChat{status: "login", pageStatus: "login", loaded: true, address: "http://127.0.0.1:1/web/b/"}
	f.panel = &panel{
		servers: profiles, serversPath: filepath.Join(t.TempDir(), "profiles.json"),
		serverChats: map[string]*desktopServerChat{f.source.ID: f.sourceChat, f.destination.ID: f.chat},
	}
	f.federation.emit = func(chat *desktopServerChat, state desktopFederationState) {
		if chat != f.sourceChat {
			t.Errorf("progress sent to another page: %+v", state)
		}
		f.events = append(f.events, state)
	}
	f.federation.run = func(job func(context.Context) func()) { f.jobs = append(f.jobs, job) }
	f.federation.present = func(id string, reload bool) {
		f.presented = append(f.presented, id)
		if !reload {
			f.presented = append(f.presented, "kept")
		}
	}
	f.federation.cookies = f.store
	return f
}

func (f *federationPanel) useSource(t *testing.T, address string) *federationFixture {
	t.Helper()
	fixture := newFederationFixture(t)
	fixture.clientURL = address
	delete(f.serverChats, f.source.ID)
	f.source = fixture.sourceProfile
	f.servers.Entries[1], f.servers.Selected = f.source, f.source.ID
	f.serverChats[f.source.ID] = f.sourceChat
	f.sourceChat.address = fixture.work.source.base
	f.store.items = append([]mygo.Cookie(nil), fixture.store.items...)
	return fixture
}

// Runs the resolution only; the exchange job stays queued for the test.
func (f *federationPanel) start(t *testing.T) (*desktopFederationAttempt, desktopFederationWork) {
	t.Helper()
	f.useSource(t, "https://b.example")
	f.startDesktopFederation(f.source.ID, f.sourceChat, testFederationTarget, true)
	if len(f.jobs) != 1 {
		t.Fatalf("jobs = %d events = %+v", len(f.jobs), f.events)
	}
	f.jobs[0](context.Background())()
	a := f.federation.attempt
	if a == nil || a.phase != "working" || a.chat != f.chat || a.address != "https://b.example/" || len(f.jobs) != 2 {
		t.Fatalf("attempt = %+v jobs = %d events = %+v", a, len(f.jobs), f.events)
	}
	return a, desktopFederationWork{destination: f.desktopFederationSite(f.destination, f.chat), target: a.target}
}

func (f *federationPanel) last() desktopFederationState {
	if len(f.events) == 0 {
		return desktopFederationState{}
	}
	return f.events[len(f.events)-1]
}

func TestDesktopAutomaticFederationFromAnotherTab(t *testing.T) {
	addSource := func(p *federationPanel, f *federationFixture) *federationFixture {
		other := newFederationFixture(t)
		other.clientURL = f.clientURL
		p.servers.Entries = append(p.servers.Entries, other.sourceProfile)
		p.serverChats[other.sourceProfile.ID] = &desktopServerChat{loaded: true, authenticated: true, address: other.work.source.base}
		p.store.items = append(p.store.items, other.store.items[0])
		return other
	}
	for _, test := range []struct {
		name           string
		change         func(*federationPanel, *federationFixture)
		beforeCommit   func(*federationPanel, *federationFixture)
		issued, writes int
	}{
		{name: "reuse A from C", issued: 1, writes: 1},
		{name: "B page still shows an expired session", issued: 1, writes: 1, change: func(p *federationPanel, f *federationFixture) {
			p.chat.authenticated, p.chat.status, p.chat.pageStatus = true, "connected", "connected"
			p.store.items = append(p.store.items, mygo.Cookie{Name: f.work.destination.prefix + "spool_session", Value: "expired-session", Path: f.work.destination.path(), HTTPOnly: true})
		}},
		{name: "keep existing B", change: func(p *federationPanel, f *federationFixture) {
			p.store.items = append(p.store.items, mygo.Cookie{Name: f.work.destination.prefix + "spool_session", Value: "existing-session", Path: f.work.destination.path(), HTTPOnly: true})
		}},
		{name: "unpublished route", change: func(_ *federationPanel, f *federationFixture) { f.clientURL = "https://elsewhere.example" }},
		{name: "authorization lookup unavailable", change: func(_ *federationPanel, f *federationFixture) { f.targetsStatus = http.StatusServiceUnavailable }},
		{name: "multiple authorized sources", change: func(p *federationPanel, f *federationFixture) { addSource(p, f) }},
		{name: "second source lookup fails", change: func(p *federationPanel, f *federationFixture) {
			addSource(p, f).targetsStatus = http.StatusInternalServerError
		}},
		{name: "second source transport fails", change: func(p *federationPanel, f *federationFixture) {
			other := addSource(p, f)
			server := httptest.NewServer(nil)
			server.Close()
			p.serverChats[other.sourceProfile.ID].address = server.URL + other.work.source.path()
		}},
		{name: "second source cookie lookup fails", change: func(p *federationPanel, f *federationFixture) {
			p.store.unreadable = addSource(p, f).work.source.base
		}},
		{name: "second source list is malformed", change: func(p *federationPanel, f *federationFixture) {
			addSource(p, f).targetsBody = `[] {}`
		}},
		{name: "second source signed out", issued: 1, writes: 1, change: func(p *federationPanel, f *federationFixture) {
			addSource(p, f).targetsStatus = http.StatusUnauthorized
		}},
		{name: "second source conclusively unauthorized", issued: 1, writes: 1, change: func(p *federationPanel, f *federationFixture) {
			addSource(p, f).targetsBody = `[]`
		}},
		{name: "C selected again", issued: 1, beforeCommit: func(p *federationPanel, _ *federationFixture) {
			if err := p.selectDesktopServer(localDesktopServer); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "A account changed", issued: 1, beforeCommit: func(p *federationPanel, _ *federationFixture) { p.store.items[0].Value = "other-source-session" }},
		{name: "B signed in meanwhile", issued: 1, beforeCommit: func(p *federationPanel, f *federationFixture) {
			p.store.items = append(p.store.items, mygo.Cookie{Name: f.work.destination.prefix + "spool_session", Value: "existing-session", Path: f.work.destination.path(), HTTPOnly: true})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := newFederationPanel(t)
			f := p.useSource(t, "https://b.example")
			p.destination.Direct, p.destination.Address = "https://b.example/", "https://b.example/"
			p.servers.Entries[2] = p.destination
			// 保留真实 HTTPS 配置和由它派生的 Cookie 作用域，仅在测试网关内替换上游。
			mount := desktopServerMount(p.destination)
			target, err := url.Parse(f.destinationURL)
			if err != nil {
				t.Fatal(err)
			}
			f.gateway.mu.Lock()
			f.gateway.routes[mount] = remoteDesktopAPI(target, mount)
			f.gateway.mu.Unlock()
			p.chat.address = f.gateway.url + mount
			f.work.destination = p.desktopFederationSite(p.destination, p.chat)
			p.servers.Selected = localDesktopServer
			if test.change != nil {
				test.change(p, f)
			}
			if err := p.selectDesktopServerTab(p.destination.ID, nil); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < len(p.jobs); i++ {
				if i == 2 && test.beforeCommit != nil {
					test.beforeCommit(p, f)
				}
				p.jobs[i](t.Context())()
			}
			if f.issued != test.issued || f.exchanged != test.issued || p.store.sets != test.writes || len(p.events) != 0 || p.federation.attempt != nil || f.sourceSawOthers || f.destinationSawOther {
				t.Fatalf("jobs=%d issued=%d exchanged=%d writes=%d events=%v attempt=%v sourceSawOthers=%v destinationSawOther=%v", len(p.jobs), f.issued, f.exchanged, p.store.sets, p.events, p.federation.attempt != nil, f.sourceSawOthers, f.destinationSawOther)
			}
		})
	}
}

func TestDesktopFederationTargetsIgnoreAStaleReadAndClearOnlyTheirSource(t *testing.T) {
	p := newFederationPanel(t)
	f := p.useSource(t, "https://b.example")
	p.servers.Selected = localDesktopServer
	p.refreshDesktopFederationTargets(p.source.ID, p.sourceChat)
	if len(p.jobs) != 1 {
		t.Fatalf("expected one background list read, got %d", len(p.jobs))
	}
	p.jobs[0](t.Context())()
	if len(p.desktopServerTabs().Entries[1].FederationTargets) != 1 || f.issued != 0 {
		t.Fatal("the source list should be visible from another page without issuing a ticket")
	}
	p.refreshDesktopFederationTargets(p.source.ID, p.sourceChat)
	stale := p.jobs[1](t.Context())
	f.targetsBody = `[]`
	p.refreshDesktopFederationTargets(p.source.ID, p.sourceChat)
	stale()
	if len(p.jobs) != 3 {
		t.Fatalf("revocation should trigger one coalesced read; got %d jobs", len(p.jobs))
	}
	p.jobs[2](t.Context())()
	if len(p.desktopServerTabs().Entries[1].FederationTargets) != 0 || len(p.servers.Entries) != 3 || f.issued != 0 {
		t.Fatal("revocation should remove the alias without changing any saved profiles")
	}
}

func TestDesktopFederationUsesTheClickedGroupFromAnotherPage(t *testing.T) {
	for _, scenario := range []string{"first", "second", "existing", "expired", "revoked", "switch", "account changed", "stale account"} {
		t.Run(scenario, func(t *testing.T) {
			p := newFederationPanel(t)
			first := p.useSource(t, "https://b.example")
			second := newFederationFixture(t)
			second.clientURL = first.clientURL
			other := &desktopServerChat{loaded: true, authenticated: true, address: second.work.source.base}
			p.servers.Entries = append(p.servers.Entries, second.sourceProfile)
			p.serverChats[second.sourceProfile.ID] = other
			p.store.items = append(p.store.items, second.store.items[0])
			for _, source := range []string{p.source.ID, second.sourceProfile.ID} {
				p.refreshDesktopFederationTargets(source, p.serverChats[source])
			}
			for _, job := range p.jobs {
				job(t.Context())()
			}
			p.jobs = nil
			mount := desktopServerMount(p.destination)
			upstream, err := url.Parse(first.destinationURL)
			if err != nil {
				t.Fatal(err)
			}
			first.gateway.mu.Lock()
			first.gateway.routes[mount] = remoteDesktopAPI(upstream, mount)
			first.gateway.mu.Unlock()
			p.chat.address = first.gateway.url + mount
			view := &desktopServerChat{loaded: true, authenticated: true}
			p.servers.Selected, p.serverChats[localDesktopServer] = localDesktopServer, view
			p.federation.emit = func(page *desktopServerChat, state desktopFederationState) {
				if page != view {
					t.Error("progress sent to a source instead of the clicking page")
				}
				p.events = append(p.events, state)
			}
			selected := p.source.ID
			if scenario == "second" {
				selected = second.sourceProfile.ID
			}
			if scenario == "existing" || scenario == "expired" {
				value := scenario + "-session"
				site := p.desktopFederationSite(p.destination, p.chat)
				p.store.items = append(p.store.items, mygo.Cookie{Name: site.prefix + "spool_session", Value: value, Path: mount, HTTPOnly: true})
				p.chat.authenticated, p.chat.pageStatus = true, "connected"
			}
			p.selectDesktopFederationTab(localDesktopServer, view, desktopFederationSelection{source: selected, target: testFederationTarget})
			if scenario == "stale account" {
				p.store.items[0].Value = "different-account"
			}
			for i := 0; i < len(p.jobs); i++ {
				apply := p.jobs[i](t.Context())
				if i == 1 {
					switch scenario {
					case "revoked":
						p.recordDesktopFederationTargets(p.sourceChat, nil)
					case "switch":
						p.servers.Selected = second.sourceProfile.ID
					case "account changed":
						p.store.items[0].Value = "changed-session"
					}
				}
				apply()
			}
			wantFirst, wantSecond, writes := 1, 0, 1
			success := true
			switch scenario {
			case "second":
				wantFirst, wantSecond = 0, 1
			case "existing":
				wantFirst, writes = 0, 0
			case "revoked", "switch", "account changed":
				writes, success = 0, false
			case "stale account":
				wantFirst, writes, success = 0, 0, false
			}
			if first.issued != wantFirst || second.issued != wantSecond || p.store.sets != writes || (len(p.presented) != 0) != success || p.last().Source != selected {
				t.Fatalf("requests A=%d D=%d writes=%d presented=%v events=%v", first.issued, second.issued, p.store.sets, p.presented, p.events)
			}
		})
	}
}

func TestDesktopFederationStartsOnlyFromThePresentedSignedInPage(t *testing.T) {
	login := testFederationTarget
	f := newFederationPanel(t)
	f.startDesktopFederation(f.source.ID, f.sourceChat, login, false)
	if f.federation.attempt != nil || len(f.events) != 0 {
		t.Fatal("a background page started a federated sign-in")
	}
	f.sourceChat.authenticated = false
	f.startDesktopFederation(f.source.ID, f.sourceChat, login, true)
	if f.federation.attempt != nil || f.last().State != "failed" || f.last().Reason != "source" {
		t.Fatalf("signed-out page: %+v", f.events)
	}
	// Without a trusted route only the source is contacted and no ticket is requested.
	f = newFederationPanel(t)
	fixture := f.useSource(t, "")
	f.startDesktopFederation(f.source.ID, f.sourceChat, login, true)
	f.jobs[0](context.Background())()
	if f.federation.attempt != nil || len(f.jobs) != 1 || f.last().Reason != "unconfigured" || fixture.issued != 0 {
		t.Fatalf("no trusted route: jobs=%d events=%+v", len(f.jobs), f.events)
	}
}

func TestDesktopFederationConfirmsOnlyAddressesPublishedByTheSource(t *testing.T) {
	f := newFederationPanel(t)
	fixture := f.useSource(t, "https://new-server.example")
	var prompted []string
	f.federation.confirm = func(address string) bool {
		prompted = append(prompted, address)
		return false
	}
	f.startDesktopFederation(f.source.ID, f.sourceChat, strings.Repeat("c", 52), true)
	f.jobs[0](context.Background())()
	if len(prompted) != 0 || f.last().Reason != "unauthorized" {
		t.Fatalf("unlisted target prompted for an address: %v, events=%+v", prompted, f.events)
	}
	f.startDesktopFederation(f.source.ID, f.sourceChat, testFederationTarget, true)
	f.jobs[1](context.Background())()
	if len(prompted) != 1 || prompted[0] != "https://new-server.example/" || fixture.issued != 0 || f.last().Reason != "cancelled" {
		t.Fatalf("prompted=%v, issued=%d, events=%+v", prompted, fixture.issued, f.events)
	}
}

func TestDesktopFederationEnrollsPublishedTailscaleRoute(t *testing.T) {
	const endpoint = "https://b.example.ts.net/"
	const login = "https://login.tailscale.com/a/test"
	for _, tc := range []struct {
		name, link string
		cancel     bool
	}{
		{name: "wait for enrollment and page recovery", link: login},
		{name: "open saved settings when no valid link is available", link: "https://elsewhere.example/a/test"},
		{name: "discard late login link after changing tabs", link: login, cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFederationPanel(t)
			fixture := f.useSource(t, endpoint)
			fixture.clientMode = "tailscale"
			f.federation.confirm = func(address string) bool { return address == endpoint }
			var opened, configured string
			f.federation.openLogin = func(address string) { opened = address }
			f.federation.configure = func(id string) { configured = id }
			f.federation.tailnetLogin = func(server desktopServer) string {
				if server.Mode != "tailscale" || server.Address != endpoint || !server.Enabled {
					t.Fatalf("enrollment used the wrong route: %+v", server)
				}
				return tc.link
			}
			f.startDesktopFederation(f.source.ID, f.sourceChat, testFederationTarget, true)
			f.jobs[0](t.Context())()
			a := f.federation.attempt
			if a == nil || a.phase != "waiting" || a.server.Mode != "tailscale" || a.server.Tailscale != endpoint || a.server.Direct != "" || !a.server.Enabled {
				t.Fatalf("new Tailscale route = %+v, events=%+v", a, f.events)
			}
			saved, err := readDesktopServers(f.serversPath)
			if server, ok := saved.find(a.server.ID); err != nil || !ok || server != a.server {
				t.Fatalf("saved route = %+v, error = %v", server, err)
			}
			f.serverChats[a.server.ID] = &desktopServerChat{address: "http://127.0.0.1:1" + desktopServerMount(a.server), loaded: true}
			f.recordDesktopServerState(a.server.ID, "error")
			if len(f.jobs) != 2 || a.phase != "network" || fixture.issued != 0 {
				t.Fatalf("unreachable route: jobs=%d phase=%q tickets=%d", len(f.jobs), a.phase, fixture.issued)
			}
			apply := f.jobs[1](t.Context())
			if tc.cancel {
				if err := f.selectDesktopServer(localDesktopServer); err != nil {
					t.Fatal(err)
				}
			}
			apply()
			switch {
			case tc.cancel:
				if opened != "" || configured != "" || f.federation.attempt != nil {
					t.Fatal("changed selection did not cancel enrollment")
				}
			case tc.link != login:
				if opened != "" || configured != a.server.ID || f.federation.attempt != nil {
					t.Fatal("did not offer the saved settings as a fallback")
				}
			default:
				if opened != login || configured != "" || a.phase != "enrolling" || len(f.jobs) != 2 {
					t.Fatalf("enrollment did not pause exchange: phase=%q jobs=%d", a.phase, len(f.jobs))
				}
				f.recordDesktopServerState(a.server.ID, "login")
				if a.phase != "working" || len(f.jobs) != 3 || fixture.issued != 0 {
					t.Fatalf("recovered route: phase=%q jobs=%d tickets=%d", a.phase, len(f.jobs), fixture.issued)
				}
			}
		})
	}
}

func TestDesktopFederationOpensSavedInactiveTailscaleRoute(t *testing.T) {
	const endpoint = "https://b.example.ts.net/"
	f := newFederationPanel(t)
	fixture := f.useSource(t, endpoint)
	fixture.clientMode = "tailscale"
	server := f.destination
	server.Address, server.Direct, server.Tailscale, server.Enabled = "", "", endpoint, false
	f.servers.Entries[2] = server
	var configured string
	f.federation.configure = func(id string) { configured = id }
	f.federation.confirm = func(string) bool { t.Fatal("proposed adding a duplicate server"); return false }
	f.startDesktopFederation(f.source.ID, f.sourceChat, testFederationTarget, true)
	f.jobs[0](t.Context())()
	if configured != server.ID || f.servers.Entries[2] != server || len(f.servers.Entries) != 3 || fixture.issued != 0 {
		t.Fatalf("fallback did not preserve saved profile: configured=%q servers=%+v", configured, f.servers.Entries)
	}
}

func TestDesktopFederationStoresTheSessionOnlyForTheOriginalDestination(t *testing.T) {
	session := desktopFederationOutcome{cookie: &mygo.Cookie{Name: "spool_remote_b_spool_session", Value: "federated-session", Path: "/web/b/", HTTPOnly: true}}
	for name, test := range map[string]struct {
		change func(*federationPanel)
		reason string
	}{
		"address edited": {func(f *federationPanel) {
			f.servers.Entries[2].Direct, f.servers.Entries[2].Address = "https://c.example/", "https://c.example/"
		}, "changed"},
		"connection switched": {func(f *federationPanel) { f.servers.Entries[2].Mode = "tailscale" }, "changed"},
		"disabled":            {func(f *federationPanel) { f.servers.Entries[2].Enabled = false }, "changed"},
		"removed": {func(f *federationPanel) {
			f.servers, _ = f.servers.remove(f.destination.ID)
			delete(f.serverChats, f.destination.ID)
		}, "changed"},
		"replacement page": {func(f *federationPanel) {
			f.serverChats[f.destination.ID] = &desktopServerChat{status: "login", pageStatus: "login", loaded: true}
		}, "changed"},
		"source signed out": {func(f *federationPanel) { f.sourceChat.authenticated = false }, "source"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFederationPanel(t)
			a, work := f.start(t)
			test.change(f)
			f.applyDesktopFederationOutcome(a, work, session)
			if f.federation.attempt != nil || len(f.jobs) != 2 || f.store.sets != 0 || len(f.presented) != 0 || f.last().State != "failed" || f.last().Reason != test.reason {
				t.Fatalf("jobs=%d sets=%d presented=%v events=%+v", len(f.jobs), f.store.sets, f.presented, f.events)
			}
		})
	}

	f := newFederationPanel(t)
	a, work := f.start(t)
	f.applyDesktopFederationOutcome(a, work, session)
	if a.phase != "storing" || len(f.jobs) != 3 || f.store.sets != 0 {
		t.Fatalf("phase=%q jobs=%d sets=%d", a.phase, len(f.jobs), f.store.sets)
	}
	apply := f.jobs[2](context.Background())
	if f.store.sets != 0 {
		t.Fatal("background worker wrote a session before the panel checked the attempt")
	}
	apply()
	if f.federation.attempt != nil || f.store.sets != 1 || len(f.presented) != 1 || f.presented[0] != f.destination.ID || f.last().State != "done" || !f.chat.federated {
		t.Fatalf("sets=%d presented=%v events=%+v", f.store.sets, f.presented, f.events)
	}
	for _, event := range f.events {
		if body, _ := json.Marshal(event); strings.Contains(string(body), "session") || strings.Contains(string(body), "spool1.") {
			t.Fatalf("progress carries a credential: %s", body)
		}
	}
}

func TestDesktopFederationDoesNotWriteAfterCancellation(t *testing.T) {
	for _, beforeRead := range []bool{true, false} {
		name := "after cookie read"
		if beforeRead {
			name = "before cookie read"
		}
		t.Run(name, func(t *testing.T) {
			f := newFederationPanel(t)
			a, work := f.start(t)
			f.applyDesktopFederationOutcome(a, work, desktopFederationOutcome{cookie: &mygo.Cookie{
				Name: work.destination.prefix + "spool_session", Value: "federated-session", Path: work.destination.path(), HTTPOnly: true,
			}})
			job := f.jobs[2]
			var apply func()
			if !beforeRead {
				apply = job(t.Context())
			}
			next := f.servers
			next.Selected = localDesktopServer
			if err := f.setDesktopServers(next); err != nil {
				t.Fatal(err)
			}
			if beforeRead {
				apply = job(t.Context())
			}
			apply()
			if f.store.sets != 0 || len(f.presented) != 0 || f.federation.attempt != nil || f.last().Reason != "source" {
				t.Fatalf("sets=%d presented=%v events=%+v", f.store.sets, f.presented, f.events)
			}
		})
	}
}

func TestDesktopFederationDoesNotUseAnAccountThatChangedWhileConnecting(t *testing.T) {
	f := newFederationPanel(t)
	fixture := f.useSource(t, "https://b.example")
	f.chat.loaded = false
	f.startDesktopFederation(f.source.ID, f.sourceChat, testFederationTarget, true)
	f.jobs[0](t.Context())()
	if a := f.federation.attempt; a == nil || a.phase != "waiting" || len(f.jobs) != 1 {
		t.Fatalf("did not wait for the destination: %+v, jobs=%d", a, len(f.jobs))
	}
	f.store.items[0].Value = "different-account-session"
	f.chat.loaded = true
	f.advanceDesktopFederation()
	f.jobs[1](t.Context())()
	if f.federation.attempt != nil || f.last().Reason != "source" || fixture.issued != 0 || f.store.sets != 0 {
		t.Fatalf("changed source account was not rejected: events=%+v issued=%d writes=%d", f.events, fixture.issued, f.store.sets)
	}
}

func TestDesktopFederationDoesNotSwitchTabsAfterCookieCommitStarts(t *testing.T) {
	f := newFederationPanel(t)
	a, work := f.start(t)
	f.applyDesktopFederationOutcome(a, work, desktopFederationOutcome{cookie: &mygo.Cookie{
		Name: work.destination.prefix + "spool_session", Value: "federated-session", Path: work.destination.path(), HTTPOnly: true,
	}})
	f.store.onSet = func() {
		// 模拟 MyGo 等待 Cookie 写入时处理的标签切换，包括离开后切回。
		for _, id := range []string{localDesktopServer, f.source.ID} {
			next := f.servers
			next.Selected = id
			if err := f.setDesktopServers(next); err != nil {
				t.Fatal(err)
			}
		}
	}
	f.jobs[2](t.Context())()
	if f.store.sets != 1 || len(f.presented) != 0 || f.federation.attempt != nil || f.last().State != "done" {
		t.Fatalf("sets=%d presented=%v events=%+v", f.store.sets, f.presented, f.events)
	}
}

func TestDesktopFederationDoesNotOverwriteCookieThatAppearedWhileWaiting(t *testing.T) {
	f := newFederationPanel(t)
	a, work := f.start(t)
	existing := mygo.Cookie{Name: work.destination.prefix + "spool_session", Value: "existing-session", Path: work.destination.path(), HTTPOnly: true}
	cookie := existing
	cookie.Value = "federated-session"
	f.applyDesktopFederationOutcome(a, work, desktopFederationOutcome{cookie: &cookie})
	f.store.items = append(f.store.items, existing)
	f.jobs[2](t.Context())()
	current, err := work.destination.sessions(f.store)
	if err != nil || len(current) != 1 || current[0].Value != existing.Value || f.store.sets != 0 || f.last().Reason != "changed" {
		t.Fatalf("destination session was not kept: cookies=%d writes=%d err=%v events=%+v", len(current), f.store.sets, err, f.events)
	}
}

func TestDesktopFederationKeepsAnAccountThatSignedInMeanwhile(t *testing.T) {
	f := newFederationPanel(t)
	a, work := f.start(t)
	f.recordDesktopServerState(f.destination.ID, "connected")
	if f.federation.attempt != a || len(f.presented) != 0 {
		t.Fatal("destination signed in before the federation checks completed")
	}
	// After validation, prefer the signed-in account over the exchange result.
	f.applyDesktopFederationOutcome(a, work, desktopFederationOutcome{cookie: &mygo.Cookie{Name: "spool_remote_b_spool_session", Value: "x"}})
	if f.federation.attempt != nil || len(f.presented) != 2 || f.presented[1] != "kept" || f.chat.federated {
		t.Fatalf("presented=%v events=%+v", f.presented, f.events)
	}
	if f.store.sets != 0 || len(f.jobs) != 2 {
		t.Fatalf("sets=%d jobs=%d", f.store.sets, len(f.jobs))
	}
}
