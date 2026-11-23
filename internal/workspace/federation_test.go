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
	if err = seedApprovedFederationPeer(store, p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFederationExchangeUsesTargetMembershipAndRevokes(t *testing.T) {
	sa, sb := testStore(t), testStore(t)
	alice := testUser(t, sa, "alice")
	project := testProject(t, sb)
	project2 := testProject(t, sb)
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
	source, target := trustServicesForTest(t, a, b, as, bs)
	pb, err := sb.FederationIdentity()
	if err != nil {
		t.Fatal(err)
	}
	pendingTrust := submitTrustForTest(t, source, target, bs.URL)
	policy := FederationTrustApproval{Transport: "https", Address: as.URL, Projects: []FederationProject{
		{project.ID, "read-only", "create"}, {project2.ID, "control", "read-only"},
	}}
	invalid := policy
	invalid.Projects = []FederationProject{{"missing-project", "read-only", "read-only"}}
	if err := sb.DecideFederationTrust(pendingTrust.ID, true, invalid); err == nil {
		t.Fatal("approval accepted an unknown default project")
	}
	if err := sb.DecideFederationTrust(pendingTrust.ID, true, policy); err != nil {
		t.Fatal(err)
	}
	if err := source.CheckFederationTrust(t.Context(), pendingTrust.ID); err != nil {
		t.Fatal(err)
	}
	pb.Transport, pb.Address = "https", bs.URL
	pb.ClientURL = "https://chat.example.test"
	if err := sa.UpdateFederationPeer(pb); err != nil {
		t.Fatal(err)
	}
	if err := sa.SetFederationTrustUsers(pb.ID, false, []string{alice.ID}); err != nil {
		t.Fatal(err)
	}
	if snap, err := sa.FederationTrustSnapshot(); err != nil || len(snap.Peers) != 1 || len(snap.Peers[0].Users) != 1 || snap.Peers[0].Users[0] != alice.ID {
		t.Fatalf("saved allowlist did not appear in the trust settings: %v", err)
	}
	pa, err := federationPeer(sb.db, pendingTrust.PeerID)
	if err != nil {
		t.Fatal(err)
	}
	if !pa.Accept || pa.Address != as.URL || len(pa.Projects) != 2 {
		t.Fatalf("approval did not save the return route and default projects: %+v", pa.Projects)
	}
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

	if users, err := sb.Users(); err != nil || len(users) != 0 {
		t.Fatal("target imported source users before their first login", users, err)
	}
	ticket := issue()
	bc := exchange(ticket, http.StatusOK)
	exchange(ticket, http.StatusUnauthorized)
	id, err := sb.authenticate(bc.Value)
	if err != nil || id.User.SourceServerID != pa.ID || !sb.CanCreateChat(id.User.ID, project.ID) || id.User.RemoteDesktop || id.User.ADBBridge {
		t.Fatalf("unexpected first-login access: %v", err)
	}
	if users, err := sb.Users(); err != nil || len(users) != 1 || users[0].ID != id.User.ID || users[0].SourceServerID != pa.ID || users[0].SourceServerName != pa.Name || users[0].SourceUserID != alice.ID {
		t.Fatal("external visitor missing from target's managed people", users, err)
	}
	projects, err := sb.Projects()
	if err != nil || len(projects) != 2 {
		t.Fatalf("could not inspect approved projects: %v", err)
	}
	for _, loaded := range projects {
		wantAccess, wantOwn := "read-only", "create"
		if loaded.ID == project2.ID {
			wantAccess, wantOwn = "control", "read-only"
		}
		if !sb.CanAccessProject(id.User.ID, loaded.ID) || loaded.MemberAccess[id.User.ID] != wantAccess || loaded.MemberOwnAccess[id.User.ID] != wantOwn ||
			sb.CanCreateChat(id.User.ID, loaded.ID) != (wantOwn == "create") {
			t.Errorf("first login did not grant %s/%s on %s, got %s/%s", wantAccess, wantOwn, loaded.ID, loaded.MemberAccess[id.User.ID], loaded.MemberOwnAccess[id.User.ID])
		}
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
	// 即使 B 也有到 A 的出站批准，外部账号也不能作为下一跳身份。
	if err := sb.SetFederationTrustUsers(pa.ID, true, nil); err == nil {
		t.Fatal("incoming-only approval accepted an outbound allowlist")
	}
	incoming := submitTrustForTest(t, target, source, as.URL)
	if err := sa.DecideFederationTrust(incoming.ID, true, FederationTrustApproval{}); err != nil {
		t.Fatal(err)
	}
	if err := target.CheckFederationTrust(t.Context(), incoming.ID); err != nil {
		t.Fatal(err)
	}
	if err := sb.SetFederationTrustUsers(pa.ID, false, []string{id.User.ID}); err == nil {
		t.Fatal("external account was accepted on an outbound allowlist")
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
	if err := sa.SetFederationTrustUsers(pb.ID, false, nil); err != nil {
		t.Fatal(err)
	}
	if got := request(a, "/api/federation/targets", nil, ac, ""); got.Code != http.StatusOK || strings.TrimSpace(got.Body.String()) != "[]" {
		t.Fatal("removed target remained visible to the signed-in user")
	}
	exchange(pending, http.StatusUnauthorized)
	a.federationMaintain(t.Context())
	if _, err := sb.authenticate(bc.Value); err == nil {
		t.Fatal("B session remained valid after removing the user from the allowlist")
	}
	if err := sa.SetFederationTrustUsers(pb.ID, true, nil); err != nil {
		t.Fatal(err)
	}
	if got := request(a, "/api/federation/targets", nil, bobCookie, ""); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), pb.ID) {
		t.Fatal("all-local mode did not allow another local user to discover the target")
	}
	bc = exchange(issue(), http.StatusOK)
	pending = issue()
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

func TestFederationSourceCanRevokeTrust(t *testing.T) {
	for _, notify := range []bool{true, false} {
		name := "notification"
		if !notify {
			name = "recheck without notification route"
		}
		t.Run(name, func(t *testing.T) {
			sa, sb := testStore(t), testStore(t)
			alice := testUser(t, sa, "alice")
			a, b := NewApp(sa, nil, "", ""), NewApp(sb, nil, "", "")
			as, bs := httptest.NewTLSServer(a), httptest.NewTLSServer(b)
			t.Cleanup(as.Close)
			t.Cleanup(bs.Close)
			for app, remote := range map[*App]*httptest.Server{a: bs, b: as} {
				app.federationDial = func(ctx context.Context, peer FederationPeer, body []byte) ([]byte, error) {
					return federationHTTP(ctx, peer.Address, body, remote.Client().Transport.(*http.Transport).Clone())
				}
			}
			source, target := trustServicesForTest(t, a, b, as, bs)
			pending := submitTrustForTest(t, source, target, bs.URL)
			if err := sb.DecideFederationTrust(pending.ID, true, FederationTrustApproval{Transport: "https", Address: as.URL}); err != nil {
				t.Fatal(err)
			}
			if err := source.CheckFederationTrust(t.Context(), pending.ID); err != nil {
				t.Fatal(err)
			}
			pb, err := sb.FederationIdentity()
			if err != nil {
				t.Fatal(err)
			}
			if err := sa.SetFederationTrustUsers(pb.ID, true, nil); err != nil {
				t.Fatal(err)
			}
			pb, err = federationPeer(sa.db, pb.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !notify {
				pb.Transport, pb.Address = "", ""
				if err := sa.UpdateFederationPeer(pb); err != nil {
					t.Fatal(err)
				}
				pb, err = federationPeer(sa.db, pb.ID)
				if err != nil {
					t.Fatal(err)
				}
			}
			pa, err := federationPeer(sb.db, pending.PeerID)
			if err != nil {
				t.Fatal(err)
			}
			localToken, localIdentity, err := sa.login(alice.Username, "test-password-2026")
			if err != nil {
				t.Fatal(err)
			}
			ticket, err := sa.federationIssue(localIdentity, pb.ID)
			if err != nil {
				t.Fatal(err)
			}
			person, err := sa.federationVerify(pb, ticket, true)
			if err != nil {
				t.Fatal(err)
			}
			remoteToken, _, err := sb.federationLogin(pa, person, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			pendingTicket, err := sa.federationIssue(localIdentity, pb.ID)
			if err != nil {
				t.Fatal(err)
			}
			// A delayed approval of an application already sent before revocation must not restore it.
			late := submitTrustForTest(t, source, target, bs.URL)
			if err := sb.DecideFederationTrust(late.ID, true, FederationTrustApproval{}); err != nil {
				t.Fatal(err)
			}
			_, _, receipt, err := federationTrustRequest(sb.db, late.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := sa.WithdrawFederationTrust(pb.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := sa.authenticate(localToken); err != nil {
				t.Fatal("source revocation signed out the local account", err)
			}
			if targets, err := sa.federationTargets(localIdentity); err != nil || len(targets) != 0 {
				t.Fatal("source revocation left the target discoverable", err)
			}
			if _, err := sa.federationIssue(localIdentity, pb.ID); !errors.Is(err, errFederationDenied) {
				t.Fatal("source issued another ticket after revoking trust", err)
			}
			current, err := federationPeer(sa.db, pb.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, peer := range []FederationPeer{pb, current} {
				if _, err := sa.federationVerify(peer, pendingTicket, true); !errors.Is(err, errFederationDenied) {
					t.Fatal("a previously issued ticket survived source revocation", err)
				}
			}
			if _, err := sa.confirmFederationTrust(receipt); !errors.Is(err, errFederationTrustRequest) {
				t.Fatal("a delayed receipt restored source trust", err)
			}
			if err := sa.SetFederationTrustUsers(pb.ID, true, nil); err == nil {
				t.Fatal("changing the allowlist restored revoked trust")
			}
			if notify {
				a.federationMaintain(t.Context())
			} else {
				if _, err := sb.db.Exec(`UPDATE federation_sessions SET retry_at=0 WHERE session_hash=?`, tokenHash(remoteToken)); err != nil {
					t.Fatal(err)
				}
				b.federationMaintain(t.Context())
			}
			if _, err := sb.authenticate(remoteToken); err == nil {
				t.Fatal("remote session survived source revocation notification or recheck")
			}
			if err := source.CheckFederationTrust(t.Context(), pending.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := sa.federationIssue(localIdentity, pb.ID); !errors.Is(err, errFederationDenied) {
				t.Fatal("replaying an old confirmed request restored trust", err)
			}
		})
	}
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

func TestSaveFederationUserRejectsIdentityEditsWithoutRevokingSession(t *testing.T) {
	local, remote := testStore(t), testStore(t)
	peer := federationPairForTest(t, local, remote, "https://source.spool.test", true, nil, nil)
	peer, err := federationPeer(local.db, peer.ID)
	if err != nil {
		t.Fatal(err)
	}
	person := federationSubject{ID: "source-user", Nickname: "External", Avatar: DefaultAvatar(), SourceRef: randomToken(), Remaining: loginSessionTTL.Milliseconds()}
	token, _, err := local.federationLogin(peer, person, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	identity, err := local.authenticate(token)
	if err != nil {
		t.Fatal(err)
	}
	user := identity.User
	for _, tc := range []struct {
		name  string
		input UserInput
	}{
		{"username", UserInput{Username: "new-name"}},
		{"display name", UserInput{Nickname: "Other name"}},
		{"password", UserInput{Password: "new-test-password"}},
		{"mixed update", UserInput{Nickname: "Other name", Disabled: true, RemoteDesktop: true, ADBBridge: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := tc.input
			input.ID = user.ID
			if _, err := local.SaveUser(input); err == nil {
				t.Fatal("external identity edit was accepted")
			}
			current, err := local.authenticate(token)
			if err != nil || current.User != user {
				t.Fatalf("rejected update changed the user or revoked the session: %+v, %v", current.User, err)
			}
		})
	}
	for _, input := range []UserInput{{ID: user.ID}, {ID: user.ID, Username: user.Username, Nickname: user.Nickname}} {
		if saved, err := local.SaveUser(input); err != nil || saved != user {
			t.Fatalf("no-op update did not return existing user: %+v, %v", saved, err)
		}
		if _, err := local.authenticate(token); err != nil {
			t.Fatalf("no-op update revoked the session: %v", err)
		}
	}
	want := user
	want.RemoteDesktop, want.ADBBridge = true, true
	if saved, err := local.SaveUser(UserInput{ID: user.ID, RemoteDesktop: true, ADBBridge: true}); err != nil || saved != want {
		t.Fatalf("local permissions did not update: %+v, %v", saved, err)
	}
	if _, err := local.authenticate(token); err == nil {
		t.Fatal("changing local permissions must still revoke the old session")
	}
	users, err := local.Users()
	want.SourceServerName, want.SourceUserID = peer.Name, person.ID
	if err != nil || len(users) != 1 || users[0] != want {
		t.Fatalf("local permissions or source profile did not persist: %+v, %v", users, err)
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
	if err = local.UpdateFederationPeer(configured); err != nil {
		t.Fatal(err)
	}
	configured.Accept = true
	if err = local.UpdateFederationPeer(configured); err != nil {
		t.Fatal(err)
	}
	if _, _, err = local.federationLogin(original, person, started); !errors.Is(err, errFederationDenied) {
		t.Fatalf("response from before the policy change was accepted: %v", err)
	}
	if err = local.federationRefresh(original, person.SourceRef, started, &person); err != nil {
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
