package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/egoist/mygo"
)

var testFederationTarget = strings.Repeat("b", 52)
var testFederationTicket = "spool1." + strings.Repeat("a", 52) + "." + base64.RawURLEncoding.EncodeToString(make([]byte, 32))

type fakeFederationCookies struct {
	mu    sync.Mutex
	items []mygo.Cookie
	sets  int
}

func (f *fakeFederationCookies) List(string) ([]mygo.Cookie, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]mygo.Cookie(nil), f.items...), nil
}

func (f *fakeFederationCookies) Set(cookie mygo.Cookie) error {
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

func TestDesktopFederationNavigationAcceptsOnlyATargetRequest(t *testing.T) {
	const page = "http://127.0.0.1:4000/web/abc/"
	login, reserved, ok := desktopFederationNavigation(page, "http://127.0.0.1:4000/__desktop/federation-login?target="+testFederationTarget)
	if !reserved || !ok || login != testFederationTarget {
		t.Fatalf("target = %q reserved=%v ok=%v", login, reserved, ok)
	}
	for _, target := range []string{
		"http://127.0.0.1:4000/__desktop/federation-login",
		"http://127.0.0.1:4000/__desktop/federation-login?target=short",
		"http://127.0.0.1:4000/__desktop/federation-login?target=" + testFederationTarget + "&target=" + testFederationTarget,
		"http://127.0.0.1:4000/__desktop/federation-login?target=" + testFederationTarget + "&ticket=" + testFederationTicket,
		"http://127.0.0.1:4000/__desktop/federation-login?target=" + testFederationTarget + "#x",
		"http://127.0.0.1:4000/__desktop/federation-login?target=" + testFederationTarget + "&client_url=https%3A%2F%2Fb.example",
		"http://127.0.0.1:4000/__desktop/federation-ticket?ticket=" + testFederationTicket,
	} {
		// Still reserved: a malformed hint must never load as a document.
		if login, reserved, ok := desktopFederationNavigation(page, target); !reserved || ok || login != "" {
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
	candidates := []desktopServer{other, published}
	if got := p.resolveDesktopFederation(context.Background(), candidates, testFederationTarget, ""); got.found || got.proposed != "" || got.existing != "" {
		t.Fatalf("resolved without a trusted route: %+v", got)
	}
	if got := p.resolveDesktopFederation(context.Background(), candidates, testFederationTarget, "http://b.example"); got.found || got.proposed != "" {
		t.Fatalf("accepted a plaintext address: %+v", got)
	}
	got := p.resolveDesktopFederation(context.Background(), candidates, testFederationTarget, "https://B.example")
	if !got.found || got.server.ID != published.ID || got.address != "https://b.example/" {
		t.Fatalf("published address = %+v", got)
	}
	// A saved but unused connection is offered to the user, not switched to.
	got = p.resolveDesktopFederation(context.Background(), candidates, testFederationTarget, "https://b.example.ts.net")
	if got.found || got.existing != published.ID || got.proposed != "" {
		t.Fatalf("unused connection = %+v", got)
	}
	got = p.resolveDesktopFederation(context.Background(), candidates, testFederationTarget, "https://new.example")
	if got.found || got.proposed != "https://new.example/" {
		t.Fatalf("new address = %+v", got)
	}
	local := desktopServer{ID: localDesktopServer, Mode: "direct", Address: "http://127.0.0.1:1/", Enabled: true}
	p.federation.identify = func(context.Context, desktopServer) string { return testFederationTarget }
	if got = p.resolveDesktopFederation(context.Background(), append(candidates, local), testFederationTarget, ""); !got.found || got.server.ID != localDesktopServer {
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
	work                desktopFederationWork
	store               *fakeFederationCookies
	mu                  sync.Mutex
	clientURL           string
	issued              int
	exchanged           int
	sourceSawOthers     bool
	destinationSawOther bool
}

func newFederationFixture(t *testing.T) *federationFixture {
	f := &federationFixture{t: t, store: &fakeFederationCookies{}}
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
				"csrf": "source-csrf", "federation_targets": []map[string]string{{"id": testFederationTarget, "name": "B", "client_url": f.clientURL}},
			})
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
			_ = json.NewEncoder(w).Encode(map[string]string{"version": "test", "server_id": testFederationTarget})
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
	f.sourceProfile = profiles.Entries[1]
	f.work = desktopFederationWork{source: site(f.sourceProfile), destination: site(profiles.Entries[2]), target: testFederationTarget}
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
	if outcome.reason != "" || outcome.signedIn || len(outcome.cookies) != 1 {
		t.Fatalf("outcome = %+v", outcome)
	}
	cookie := outcome.cookies[0]
	if cookie.Name != f.work.destination.prefix+"spool_session" || cookie.Value != "federated-session" || cookie.Path != f.work.destination.path() ||
		cookie.Domain != "127.0.0.1" || !cookie.HTTPOnly || cookie.Secure || cookie.SameSite != mygo.CookieSameSiteStrict || cookie.Expires.IsZero() {
		t.Fatalf("session cookie = %+v", cookie)
	}
	if f.store.sets != 0 {
		t.Fatal("stored a session before the panel validated the attempt")
	}
	if reason := f.work.store(f.store, outcome); reason != "" || f.store.sets != 1 {
		t.Fatalf("store = %q sets=%d", reason, f.store.sets)
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
		"target not authorized":     func(f *federationFixture) { f.work.target = strings.Repeat("c", 52) },
		"signed out of the source":  func(f *federationFixture) { f.store.items = nil },
	} {
		t.Run(name, func(t *testing.T) {
			f := newFederationFixture(t)
			change(f)
			if outcome := f.exchange(); outcome.reason == "" || len(outcome.cookies) != 0 || f.issued != 0 || f.exchanged != 0 {
				t.Fatalf("outcome=%+v issued=%d exchanged=%d", outcome, f.issued, f.exchanged)
			}
		})
	}
	f := newFederationFixture(t)
	f.work.address, f.clientURL = "https://b.example/", "https://B.example"
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

	// An account that signs in while the exchange is in flight is kept too.
	f = newFederationFixture(t)
	outcome := f.exchange()
	if outcome.reason != "" || len(outcome.cookies) != 1 {
		t.Fatalf("outcome = %+v", outcome)
	}
	existing.Name, existing.Path = f.work.destination.prefix+"spool_session", f.work.destination.path()
	f.store.items = append(f.store.items, existing)
	if reason := f.work.store(f.store, outcome); reason != "changed" || f.store.sets != 0 {
		t.Fatalf("store = %q sets=%d", reason, f.store.sets)
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

func TestDesktopFederationStoresTheSessionOnlyForTheOriginalDestination(t *testing.T) {
	session := desktopFederationOutcome{cookies: []mygo.Cookie{{Name: "spool_remote_b_spool_session", Value: "federated-session", Path: "/web/b/", HTTPOnly: true}}}
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
	f.jobs[2](context.Background())()
	if f.federation.attempt != nil || f.store.sets != 1 || len(f.presented) != 1 || f.presented[0] != f.destination.ID || f.last().State != "done" || !f.chat.federated {
		t.Fatalf("sets=%d presented=%v events=%+v", f.store.sets, f.presented, f.events)
	}
	for _, event := range f.events {
		if body, _ := json.Marshal(event); strings.Contains(string(body), "session") || strings.Contains(string(body), "spool1.") {
			t.Fatalf("progress carries a credential: %s", body)
		}
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
	f.applyDesktopFederationOutcome(a, work, desktopFederationOutcome{cookies: []mygo.Cookie{{Name: "spool_remote_b_spool_session", Value: "x"}}})
	if f.federation.attempt != nil || len(f.presented) != 2 || f.presented[1] != "kept" || f.chat.federated {
		t.Fatalf("presented=%v events=%+v", f.presented, f.events)
	}
	if f.store.sets != 0 || len(f.jobs) != 2 {
		t.Fatalf("sets=%d jobs=%d", f.store.sets, len(f.jobs))
	}
}
