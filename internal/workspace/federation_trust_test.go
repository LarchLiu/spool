package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"spool/relay"
)

// 旧关系夹具模拟从早期数据库保留手工配对；生产配置入口不允许新建。
func seedLegacyFederationPeer(s *Store, p FederationPeer) error {
	data, err := json.Marshal(FederationPeer{ID: p.ID, Name: p.Name, PublicKey: p.PublicKey})
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(`INSERT INTO federation_peers VALUES(?,?,?)`, p.ID, data, randomToken()); err != nil {
		return err
	}
	return s.UpdateFederationPeer(p)
}

func TestFederationTrustApprovalIsDirectionalAndDoesNotGrantLogin(t *testing.T) {
	a, b := testStore(t), testStore(t)
	alice := testUser(t, a, "alice")
	source, target, address := onlineTrustPairForTest(t, a, b)
	aPub, err := a.FederationIdentity()
	if err != nil {
		t.Fatal(err)
	}
	bPub, err := b.FederationIdentity()
	if err != nil {
		t.Fatal(err)
	}
	pending := submitTrustForTest(t, source, target, address)
	if pending.Outgoing || pending.Status != federationTrustPending || pending.PeerID != aPub.ID {
		t.Fatalf("unexpected incoming request: %+v", pending)
	}
	if again := submitTrustForTest(t, source, target, address); again != pending {
		t.Fatal("resubmitting did not preserve the pending request")
	}
	if err := a.DecideFederationTrust(pending.ID, true, FederationTrustApproval{}); !errors.Is(err, errFederationTrustRequest) {
		t.Fatal("the requester could approve its own request")
	}
	if err := b.UpdateFederationPeer(aPub); err == nil {
		t.Fatal("a pending request could bypass approval through policy configuration")
	}
	if err := a.SetFederationTrustUsers(bPub.ID, false, []string{alice.ID}); err == nil {
		t.Fatal("a pending target accepted an allowlist")
	}
	for _, s := range []*Store{a, b} {
		v, err := s.FederationTrustSnapshot()
		if err != nil || len(v.Requests) != 1 || len(v.Peers) != 0 {
			t.Fatalf("pending request exposed a trusted peer: %+v, %v", v, err)
		}
	}

	app := NewApp(b, nil, "", "")
	local, err := a.federationKey()
	if err != nil {
		t.Fatal(err)
	}
	wire, err := (federationMessage{Version: federationVersion, From: aPub.ID, To: bPub.ID, Action: federationCheck, Nonce: randomToken(), Value: randomToken(), SentAt: time.Now().UnixMilli()}).encode(local)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "https://b.example.test"+FederationControlPath, bytes.NewReader(wire)))
	if response.Code != http.StatusForbidden || b.federationRelayAdmission().Allow(local.PublicKey()) {
		t.Fatal("a pending request gained HTTPS or Relay control admission")
	}

	if err := b.DecideFederationTrust(pending.ID, true, FederationTrustApproval{}); err != nil {
		t.Fatal(err)
	}
	if v, err := a.FederationTrustSnapshot(); err != nil || len(v.Peers) != 0 {
		t.Fatal("A trusted B before checking the decision")
	}
	if err := source.CheckFederationTrust(t.Context(), pending.ID); err != nil {
		t.Fatal(err)
	}
	if confirmed, _, _, err := federationTrustRequest(a.db, pending.ID); err != nil || confirmed.Status != federationTrustApproved || confirmed.PeerID != bPub.ID {
		t.Fatalf("unexpected confirmation: %+v, %v", confirmed, err)
	}
	for _, tc := range []struct {
		store    *Store
		id       string
		outgoing bool
	}{{a, bPub.ID, true}, {b, aPub.ID, false}} {
		v, err := tc.store.FederationTrustSnapshot()
		if err != nil || len(v.Peers) != 1 || v.Peers[0].ID != tc.id || v.Peers[0].Outgoing != tc.outgoing || v.Peers[0].Incoming == tc.outgoing {
			t.Fatalf("wrong trust direction: %+v, %v", v.Peers, err)
		}
		p, err := federationPeer(tc.store.db, tc.id)
		if err != nil || p.Accept || p.AllUsers || len(p.Users) != 0 || len(p.Projects) != 0 {
			t.Fatalf("approval changed login policy: %+v, %v", p, err)
		}
		if tc.outgoing {
			p.Transport, p.Address, p.Accept = "https", "https://b.example.test", true
		} else {
			p.AllUsers = true
		}
		if err := tc.store.UpdateFederationPeer(p); err == nil {
			t.Fatal("configuration could enable the unapproved direction")
		}
	}
	if targets, err := a.federationTargets(authIdentity{User: alice}); err != nil || len(targets) != 0 {
		t.Fatal("the approved relationship appeared as a user login target")
	}
	if err := source.CheckFederationTrust(t.Context(), pending.ID); err != nil {
		t.Fatalf("could not recheck the same decision: %v", err)
	}
}

func TestFederationTrustReciprocalApprovalMergesOnlyBlankRoutes(t *testing.T) {
	for _, tc := range []struct {
		name          string
		transport     string
		manualControl bool
		manualClient  bool
	}{
		{name: "blank peer over HTTPS", transport: "https"},
		{name: "blank peer over Tailscale", transport: "tailscale"},
		{name: "manual control route", transport: "tailscale", manualControl: true},
		{name: "manual client URL and mode", transport: "https", manualClient: true},
		{name: "manual control and direct client URL", transport: "tailscale", manualControl: true, manualClient: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := testStore(t), testStore(t)
			project := FederationProject{ID: testProject(t, a).ID, Access: "control", OwnAccess: "create"}
			bs, as, aURL := onlineTrustPairForTest(t, b, a)
			incoming := submitTrustForTest(t, bs, as, aURL)
			if err := a.DecideFederationTrust(incoming.ID, true, FederationTrustApproval{Projects: []FederationProject{project}}); err != nil {
				t.Fatal(err)
			}
			before, err := federationPeer(a.db, incoming.PeerID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.manualControl || tc.manualClient {
				before.Name = "Administrator label"
				if tc.manualControl {
					before.Transport, before.Address, before.Accept = "https", "https://manual-control.example.test", true
				}
				if tc.manualClient {
					before.ClientURL = "https://manual-client.example.test/"
					if tc.transport == "https" {
						before.ClientMode = FederationClientModeTailscale
					}
				}
				if err := a.UpdateFederationPeer(before); err != nil {
					t.Fatal(err)
				}
				before, err = federationPeer(a.db, incoming.PeerID)
				if err != nil {
					t.Fatal(err)
				}
			}
			as, bs, bURL := onlineTrustPairForTest(t, a, b)
			outgoing := submitTrustWithTransportForTest(t, as, bs, tc.transport, bURL)
			if err := b.DecideFederationTrust(outgoing.ID, true, FederationTrustApproval{}); err != nil {
				t.Fatal(err)
			}
			_, _, receipt, err := federationTrustRequest(b.db, outgoing.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := as.CheckFederationTrust(t.Context(), outgoing.ID); err != nil {
				t.Fatal(err)
			}
			p, err := federationPeer(a.db, before.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := before
			if !tc.manualControl {
				want.Transport, want.Address = tc.transport, bURL
				if p.generation == before.generation {
					t.Fatal("adding a control route did not invalidate in-flight requests")
				}
				want.generation = p.generation
			}
			if !tc.manualClient {
				want.ClientURL = bURL + "/"
				if tc.transport == "tailscale" {
					want.ClientMode = FederationClientModeTailscale
				}
			}
			if !reflect.DeepEqual(p, want) {
				t.Fatalf("reciprocal approval changed the wrong peer fields: got %+v, want %+v", p, want)
			}
			bob := testUser(t, a, "bob")
			if err := a.SetFederationTrustUsers(p.ID, false, []string{bob.ID}); err != nil {
				t.Fatalf("reciprocal approval did not grant outbound policy configuration: %v", err)
			}
			if _, err := a.confirmFederationTrust(receipt); err != nil {
				t.Fatalf("reconfirming a receipt failed: %v", err)
			}
			p, err = federationPeer(a.db, before.ID)
			want.Users = []string{bob.ID}
			if err != nil || !reflect.DeepEqual(p, want) {
				t.Fatalf("reconfirmation changed the saved peer: got %+v, want %+v: %v", p, want, err)
			}
		})
	}
}

func TestUpdateFederationTrustReturnRoutePreservesPolicy(t *testing.T) {
	a, b := testStore(t), testStore(t)
	alice := testUser(t, b, "alice")
	project := FederationProject{ID: testProject(t, b).ID, Access: "read-only", OwnAccess: "create"}
	source, target, address := onlineTrustPairForTest(t, a, b)
	pending := submitTrustForTest(t, source, target, address)
	if err := b.UpdateFederationTrustReturnRoute(pending.PeerID, "https", "https://source.example.test"); err == nil {
		t.Fatal("a pending request accepted a return route")
	}
	if err := b.DecideFederationTrust(pending.ID, true, FederationTrustApproval{Projects: []FederationProject{project}}); err != nil {
		t.Fatal(err)
	}
	if err := source.CheckFederationTrust(t.Context(), pending.ID); err != nil {
		t.Fatal(err)
	}
	bID, err := b.FederationIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.UpdateFederationTrustReturnRoute(bID.ID, "https", "https://target.example.test"); err == nil {
		t.Fatal("an outgoing-only grant accepted a return route")
	}
	source, target, address = onlineTrustPairForTest(t, b, a)
	reverse := submitTrustWithTransportForTest(t, source, target, "tailscale", address)
	if err := a.DecideFederationTrust(reverse.ID, true, FederationTrustApproval{}); err != nil {
		t.Fatal(err)
	}
	if err := source.CheckFederationTrust(t.Context(), reverse.ID); err != nil {
		t.Fatal(err)
	}
	if err := b.SetFederationTrustUsers(pending.PeerID, false, []string{alice.ID}); err != nil {
		t.Fatal(err)
	}
	before, err := federationPeer(b.db, pending.PeerID)
	if err != nil {
		t.Fatal(err)
	}
	if before.Accept || before.ClientMode != FederationClientModeTailscale {
		t.Fatalf("unexpected policy before configuring a return route: %+v", before)
	}
	key, err := a.federationKey()
	if err != nil {
		t.Fatal(err)
	}
	route := relay.Pairing{Version: 2, RelayURL: "https://relay.example.test", PublicKey: key.PublicKey(), AccessToken: key.AccessToken}
	validCode, err := route.Encode()
	if err != nil {
		t.Fatal(err)
	}
	other, err := b.federationKey()
	if err != nil {
		t.Fatal(err)
	}
	route.PublicKey = other.PublicKey()
	wrongCode, err := route.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := b.UpdateFederationTrustReturnRoute(before.ID, "relay", wrongCode); !errors.Is(err, errFederationTrustPeer) {
		t.Fatalf("mismatched Relay ID was not rejected: %v", err)
	}
	if err := b.UpdateFederationTrustReturnRoute(before.ID, "https", "https://source.example.test/a/path"); err == nil {
		t.Fatal("a non-origin HTTPS address was accepted")
	}
	afterInvalid, err := federationPeer(b.db, before.ID)
	if err != nil || !reflect.DeepEqual(afterInvalid, before) {
		t.Fatalf("invalid route changed the saved peer: %+v: %v", afterInvalid, err)
	}
	if err := b.UpdateFederationTrustReturnRoute(before.ID, "relay", validCode); err != nil {
		t.Fatal(err)
	}
	got, err := federationPeer(b.db, before.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.generation == before.generation {
		t.Fatal("setting the return route did not invalidate in-flight control requests")
	}
	want := before
	want.Transport, want.Address, want.RelayCode, want.Accept = "relay", "", validCode, true
	want.generation = got.generation
	if !reflect.DeepEqual(got, want) {
		t.Fatal("setting the return route did not enable login and preserve other peer settings")
	}
	snapshot, err := b.FederationTrustSnapshot()
	if err != nil || len(snapshot.Peers) != 1 || !snapshot.Peers[0].Accept {
		t.Fatalf("snapshot did not show inbound login as enabled: %+v: %v", snapshot.Peers, err)
	}
	data, err := json.Marshal(snapshot)
	if err != nil || !bytes.Contains(data, []byte(`"accept":true`)) || bytes.Contains(data, []byte(validCode)) || bytes.Contains(data, []byte(`"relay_code"`)) {
		t.Fatal("trust snapshot did not expose only the return route status")
	}
	if err := b.UpdateFederationTrustReturnRoute(before.ID, "relay", validCode); err != nil {
		t.Fatal(err)
	}
	got, err = federationPeer(b.db, before.ID)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("saving an unchanged route rewrote the peer or invalidated in-flight requests")
	}
}

func TestFederationPeerValidatesClientMode(t *testing.T) {
	a, b := testStore(t), testStore(t)
	p, err := b.FederationIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if err := seedLegacyFederationPeer(a, p); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		mode, url string
	}{
		{"https", "https://source.example.test/"},
		{FederationClientModeTailscale, ""},
		{FederationClientModeTailscale, "https://relay.example.test" + relay.WebPath(p.ID)},
	} {
		p.ClientMode, p.ClientURL = tc.mode, tc.url
		if err := a.UpdateFederationPeer(p); err == nil {
			t.Fatalf("invalid client routing configuration saved: %+v", tc)
		}
	}
	p.ClientMode, p.ClientURL = FederationClientModeTailscale, "https://source.example.test"
	if err := a.UpdateFederationPeer(p); err != nil {
		t.Fatalf("explicit Tailscale client URL without a Tailscale server route was rejected: %v", err)
	}
	p, err = federationPeer(a.db, p.ID)
	if err != nil || p.Transport != "" || p.ClientMode != FederationClientModeTailscale || p.ClientURL != "https://source.example.test/" {
		t.Fatalf("explicit client mode was not saved: %+v: %v", p, err)
	}
}

func TestFederationTrustRejectsExpiredAndMismatchedReceipts(t *testing.T) {
	a, b, c := testStore(t), testStore(t), testStore(t)
	source, target, address := onlineTrustPairForTest(t, a, b)
	p := submitTrustForTest(t, source, target, address)
	if err := b.DecideFederationTrust(p.ID, false, FederationTrustApproval{}); err != nil {
		t.Fatal(err)
	}
	if err := b.DecideFederationTrust(p.ID, true, FederationTrustApproval{}); !errors.Is(err, errFederationTrustDecided) {
		t.Fatal("a retry changed the rejected request to approved")
	}
	if err := source.CheckFederationTrust(t.Context(), p.ID); err != nil {
		t.Fatal(err)
	}
	r, _, receipt, err := federationTrustRequest(a.db, p.ID)
	if err != nil || r.Status != federationTrustRejected {
		t.Fatalf("rejection was not recorded: %+v, %v", r, err)
	}
	if next := submitTrustForTest(t, source, target, address); next.ID == p.ID {
		t.Fatal("could not create a new request after rejection")
	}
	for _, s := range []*Store{a, b} {
		if v, err := s.FederationTrustSnapshot(); err != nil || len(v.Peers) != 0 {
			t.Fatal("a rejected application created a peer")
		}
	}
	m, err := parseFederationTrustCode(receipt, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	m.Decision = federationTrustApproved
	if _, err := a.confirmFederationTrust(m.code()); !errors.Is(err, errFederationTrustCode) {
		t.Fatal("a decision with an invalid signature was accepted")
	}
	key, pub, err := c.federationTrustIdentity()
	if err != nil {
		t.Fatal(err)
	}
	m.Sender = pub
	if _, err := a.confirmFederationTrust(m.sign(key).code()); !errors.Is(err, errFederationTrustRequest) {
		t.Fatal("a different server could approve the request")
	}
	key, pub, err = b.federationTrustIdentity()
	if err != nil {
		t.Fatal(err)
	}
	m.Sender = pub
	m.Expires = time.Now().Add(-time.Second).Unix()
	if _, err := a.confirmFederationTrust(m.sign(key).code()); !errors.Is(err, errFederationTrustCode) {
		t.Fatal("an expired receipt was accepted")
	}
}
