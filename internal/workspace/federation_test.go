package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func federationPairForTest(t *testing.T, store, remote *Store, address string, accept bool, users []string, projects []FederationProject) FederationPeer {
	t.Helper()
	p, err := remote.FederationIdentity()
	if err != nil {
		t.Fatal(err)
	}
	p.Transport, p.Address, p.Accept, p.Users, p.Projects = "https", address, accept, users, projects
	if err = store.SaveFederationPeer(p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFederationExchangeUsesTargetMembershipAndRevokes(t *testing.T) {
	sa, sb := testStore(t), testStore(t)
	alice := testUser(t, sa, "alice")
	project := testProject(t, sb)
	a, b := NewApp(sa, nil, "", ""), NewApp(sb, nil, "", "")
	as := httptest.NewTLSServer(a)
	defer as.Close()
	bs := httptest.NewTLSServer(b)
	defer bs.Close()
	for app, remote := range map[*App]*httptest.Server{a: bs, b: as} {
		app.federationDial = func(ctx context.Context, peer FederationPeer, body []byte) ([]byte, error) {
			return federationHTTP(ctx, peer.Address, body, remote.Client().Transport.(*http.Transport).Clone())
		}
	}
	pb := federationPairForTest(t, sa, sb, bs.URL, false, []string{alice.ID}, nil)
	pb.ClientURL = "https://chat.example.test"
	if err := sa.SaveFederationPeer(pb); err != nil {
		t.Fatal(err)
	}
	pa := federationPairForTest(t, sb, sa, as.URL, true, nil, []FederationProject{{project.ID, "read-only", "create"}})
	ac, csrf := loginRequest(t, a, "alice")

	request := func(handler *App, path string, body any, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
		t.Helper()
		var req *http.Request
		if body == nil {
			req = httptest.NewRequest(http.MethodGet, "https://chat.example.test"+path, nil)
		} else {
			data, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			req = httptest.NewRequest(http.MethodPost, "https://chat.example.test"+path, bytes.NewReader(data))
			req.Header.Set("Content-Type", "application/json")
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		req.Header.Set("X-CSRF-Token", csrf)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	issue := func() string {
		t.Helper()
		res := request(a, "/api/federation/issue", map[string]string{"target": pb.ID}, ac, csrf)
		var ticket map[string]string
		if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &ticket) != nil || !strings.HasPrefix(ticket["ticket"], "spool1."+pa.ID+".") {
			t.Fatalf("issue status %d, missing ticket", res.Code)
		}
		return ticket["ticket"]
	}
	exchange := func(ticket string, want int) *http.Cookie {
		t.Helper()
		res := request(b, "/api/federation/exchange", map[string]string{"ticket": ticket}, nil, "")
		if res.Code != want {
			t.Fatalf("exchange status %d, want %d: %s", res.Code, want, res.Body.String())
		}
		if want != http.StatusOK {
			return nil
		}
		cookies := res.Result().Cookies()
		if len(cookies) != 1 || !cookies[0].HttpOnly || !cookies[0].Secure {
			t.Fatal("expected a secure B session cookie")
		}
		return cookies[0]
	}

	targets := request(a, "/api/federation/targets", nil, ac, "")
	var discovered []federationTarget
	if targets.Code != http.StatusOK || json.Unmarshal(targets.Body.Bytes(), &discovered) != nil || len(discovered) != 1 || discovered[0].ID != pb.ID || discovered[0].ClientURL != pb.ClientURL+"/" {
		t.Fatal("eligible A user cannot discover B's client route")
	}
	if strings.Contains(targets.Body.String(), bs.URL) || strings.Contains(targets.Body.String(), pb.PublicKey) {
		t.Fatal("the client listing disclosed the server control configuration")
	}
	me := request(a, "/api/me", nil, ac, "")
	var signedIn struct {
		Targets []federationTarget `json:"federation_targets"`
	}
	if me.Code != http.StatusOK || json.Unmarshal(me.Body.Bytes(), &signedIn) != nil || len(signedIn.Targets) != 1 || signedIn.Targets[0] != discovered[0] {
		t.Fatal("signed-in profile is missing the authorized server tab")
	}
	if got := request(a, "/api/federation/issue", map[string]string{"target": pb.ID}, ac, "").Code; got != http.StatusForbidden {
		t.Fatalf("issue without CSRF got %d", got)
	}
	bob := testUser(t, sa, "bob")
	bobCookie, bobCSRF := loginRequest(t, a, bob.Username)
	if got := request(a, "/api/federation/targets", nil, bobCookie, ""); got.Code != http.StatusOK || strings.TrimSpace(got.Body.String()) != "[]" {
		t.Fatal("unlisted user can discover a federation peer")
	}
	if got := request(a, "/api/federation/issue", map[string]string{"target": pb.ID}, bobCookie, bobCSRF).Code; got != http.StatusForbidden {
		t.Fatalf("unlisted user got %d", got)
	}

	ticket := issue()
	bc := exchange(ticket, http.StatusOK)
	exchange(ticket, http.StatusUnauthorized)
	id, err := sb.authenticate(bc.Value)
	if err != nil || id.User.SourceServerID != pa.ID || !sb.CanCreateChat(id.User.ID, project.ID) || id.User.RemoteDesktop || id.User.ADBBridge {
		t.Fatalf("unexpected first-login access: %v", err)
	}
	if got := request(b, "/api/login", map[string]string{"username": id.User.Username, "password": "test-password-2026"}, nil, "").Code; got != http.StatusUnauthorized {
		t.Fatalf("external user got password login status %d", got)
	}
	if got := request(b, "/api/federation/targets", nil, bc, ""); got.Code != http.StatusOK || strings.TrimSpace(got.Body.String()) != "[]" {
		t.Fatal("external account must not act as an identity source")
	}
	if _, err = sb.SaveUser(UserInput{ID: id.User.ID, Password: "new-test-password"}); err == nil {
		t.Fatal("an external user could be assigned a local password")
	}
	if _, err = sb.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	bc = exchange(issue(), http.StatusOK)
	second, err := sb.authenticate(bc.Value)
	if err != nil || second.User.ID != id.User.ID || sb.CanAccessProject(second.User.ID, project.ID) {
		t.Fatal("repeated login changed the account or restored a removed project")
	}
	pending := issue()
	if got := request(a, "/api/logout", map[string]string{}, ac, csrf).Code; got != http.StatusOK {
		t.Fatalf("A logout got %d", got)
	}
	exchange(pending, http.StatusUnauthorized)
	a.federationMaintain(t.Context())
	if _, err = sb.authenticate(bc.Value); err == nil {
		t.Fatal("B session remained valid after A's logout notification")
	}
	ac, csrf = loginRequest(t, a, "alice")
	bc = exchange(issue(), http.StatusOK)
	if err = sa.logout(tokenHash(ac.Value)); err != nil {
		t.Fatal(err)
	}
	if _, err = sb.db.Exec(`UPDATE federation_sessions SET retry_at=0 WHERE session_hash=?`, tokenHash(bc.Value)); err != nil {
		t.Fatal(err)
	}
	b.federationMaintain(t.Context())
	if _, err = sb.authenticate(bc.Value); err == nil {
		t.Fatal("B session remained valid after a check without a revocation notification")
	}
	if _, err = sb.SaveUser(UserInput{ID: id.User.ID, Disabled: true}); err != nil {
		t.Fatal(err)
	}
	ac, csrf = loginRequest(t, a, "alice")
	exchange(issue(), http.StatusUnauthorized)
}

func TestFederationTicketIsBoundToTargetAndExpires(t *testing.T) {
	source := testStore(t)
	alice := testUser(t, source, "alice")
	a := NewApp(source, nil, "", "")
	cookie, _ := loginRequest(t, a, alice.Username)
	identity, err := source.authenticate(cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	pair := func() FederationPeer {
		t.Helper()
		p := federationPairForTest(t, source, testStore(t), "https://partner.example.test", false, []string{alice.ID}, nil)
		p, err := federationPeer(source.db, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	b, c := pair(), pair()
	ticket, err := source.federationIssue(identity, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.federationVerify(c, ticket, true); !errors.Is(err, errFederationDenied) {
		t.Fatalf("ticket for B redeemed by C: %v", err)
	}
	if _, err = source.federationVerify(b, ticket, true); err != nil {
		t.Fatalf("wrong-target attempt consumed B's ticket: %v", err)
	}
	ticket, err = source.federationIssue(identity, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.db.Exec(`UPDATE federation_tickets SET expires_at=? WHERE hash=?`, time.Now().Add(-time.Second).UnixMilli(), tokenHash(ticket)); err != nil {
		t.Fatal(err)
	}
	if _, err = source.federationVerify(b, ticket, true); !errors.Is(err, errFederationDenied) {
		t.Fatalf("expired ticket was accepted: %v", err)
	}
}

func TestFederationDelayedVerificationCannotRestoreAccess(t *testing.T) {
	local, remote := testStore(t), testStore(t)
	configured := federationPairForTest(t, local, remote, "https://source.spool.test", true, nil, nil)
	original, err := federationPeer(local.db, configured.ID)
	if err != nil {
		t.Fatal(err)
	}
	person := federationSubject{ID: "source-user", Nickname: "External", Avatar: DefaultAvatar(), SourceRef: randomToken(), Remaining: loginSessionTTL.Milliseconds()}
	started := time.Now()
	if err = local.federationRevoke(original, person.SourceRef); err != nil {
		t.Fatal(err)
	}
	if _, _, err = local.federationLogin(original, person, started); !errors.Is(err, errFederationDenied) {
		t.Fatalf("response received after revocation was accepted: %v", err)
	}
	person.SourceRef = randomToken()
	token, _, err := local.federationLogin(original, person, started)
	if err != nil {
		t.Fatal(err)
	}
	configured.Accept = false
	if err = local.SaveFederationPeer(configured); err != nil {
		t.Fatal(err)
	}
	configured.Accept = true
	if err = local.SaveFederationPeer(configured); err != nil {
		t.Fatal(err)
	}
	if _, _, err = local.federationLogin(original, person, started); !errors.Is(err, errFederationDenied) {
		t.Fatalf("response from before the policy change was accepted: %v", err)
	}
	if err = local.federationRefresh(original, person.SourceRef, started); err != nil {
		t.Fatal(err)
	}
	if _, err = local.authenticate(token); err == nil {
		t.Fatal("refresh restored the old session after the peer was reenabled")
	}
}

func TestFederationControlRefusesPlainHTTPAndUnsignedMessages(t *testing.T) {
	a := NewApp(testStore(t), nil, "", "")
	for _, target := range []struct {
		url  string
		want int
	}{
		{"http://127.0.0.1" + FederationControlPath, http.StatusNotFound},
		{"https://spool.test" + FederationControlPath, http.StatusForbidden},
		{"https://spool.test" + FederationControlPath + "?x=1", http.StatusNotFound},
	} {
		r := httptest.NewRequest(http.MethodPost, target.url, strings.NewReader(`{}`))
		r.RemoteAddr = "127.0.0.1:51111"
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		if w.Code != target.want {
			t.Errorf("%s returned %d, want %d", target.url, w.Code, target.want)
		}
	}
}

func TestFederationHTTPSDoesNotFollowRedirects(t *testing.T) {
	redirected := make(chan struct{}, 1)
	destination := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case redirected <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer destination.Close()
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+FederationControlPath, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	transport := source.Client().Transport.(*http.Transport).Clone()
	if _, err := federationHTTP(t.Context(), source.URL, []byte(`{}`), transport); err == nil {
		t.Fatal("accepted a redirect instead of returning a transport failure")
	}
	select {
	case <-redirected:
		t.Fatal("forwarded a control request to an unconfigured endpoint")
	default:
	}
}

func TestFederationOfflineLimitIsEnforcedDuringAuthentication(t *testing.T) {
	local, remote := testStore(t), testStore(t)
	peer := federationPairForTest(t, local, remote, "https://remote.spool.test", true, nil, nil)
	peer, err := federationPeer(local.db, peer.ID)
	if err != nil {
		t.Fatal(err)
	}
	person := federationSubject{ID: "source-user", Nickname: "External", Avatar: DefaultAvatar(), SourceRef: randomToken(), Remaining: loginSessionTTL.Milliseconds()}
	token, _, err := local.federationLogin(peer, person, time.Now().Add(-federationOffline-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := local.authenticate(token); err == nil {
		t.Fatal("authentication exceeded the last successful check's offline limit")
	}
}
