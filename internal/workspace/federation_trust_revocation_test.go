package workspace

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func revocationPairForTest(t *testing.T, reciprocal bool) (a, b *Service, pending FederationTrustRequest) {
	t.Helper()
	sa, sb := testStore(t), testStore(t)
	aa, ba := NewApp(sa, nil, "", ""), NewApp(sb, nil, "", "")
	as, bs := httptest.NewTLSServer(aa), httptest.NewTLSServer(ba)
	t.Cleanup(as.Close)
	t.Cleanup(bs.Close)
	a, b = trustServicesForTest(t, aa, ba, as, bs)
	pending = submitTrustForTest(t, a, b, bs.URL)
	if err := sb.DecideFederationTrust(pending.ID, true, FederationTrustApproval{Transport: "https", Address: as.URL}); err != nil {
		t.Fatal(err)
	}
	if err := a.CheckFederationTrust(t.Context(), pending.ID); err != nil {
		t.Fatal(err)
	}
	if reciprocal {
		reverse := submitTrustForTest(t, b, a, as.URL)
		if err := sa.DecideFederationTrust(reverse.ID, true, FederationTrustApproval{Transport: "https", Address: bs.URL}); err != nil {
			t.Fatal(err)
		}
		if err := b.CheckFederationTrust(t.Context(), reverse.ID); err != nil {
			t.Fatal(err)
		}
	}
	return
}

func trustPeerForTest(t *testing.T, s *Store) FederationTrustPeer {
	t.Helper()
	snapshot, err := s.FederationTrustSnapshot()
	if err != nil || len(snapshot.Peers) != 1 {
		t.Fatalf("trust snapshot: %+v, %v", snapshot.Peers, err)
	}
	return snapshot.Peers[0]
}

func TestFederationTrustRevocationSynchronizesDirection(t *testing.T) {
	for _, sourceRevokes := range []bool{true, false} {
		name := "destination revokes incoming"
		if sourceRevokes {
			name = "source withdraws outgoing"
		}
		t.Run(name, func(t *testing.T) {
			a, b, request := revocationPairForTest(t, true)
			pa, pb := trustPeerForTest(t, a.Store), trustPeerForTest(t, b.Store)
			alice := testUser(t, a.Store, "alice")
			bob := testUser(t, b.Store, "bob")
			for _, input := range []struct {
				s          *Store
				peer, user string
			}{{a.Store, pa.ID, alice.ID}, {b.Store, pb.ID, bob.ID}} {
				if err := input.s.SetFederationTrustUsers(input.peer, false, []string{input.user}); err != nil {
					t.Fatal(err)
				}
			}
			login := func(s *Store, peerID string) string {
				t.Helper()
				peer, err := federationPeer(s.db, peerID)
				if err != nil {
					t.Fatal(err)
				}
				token, _, err := s.federationLogin(peer, federationSubject{ID: "source-user", Nickname: "Visitor", Avatar: DefaultAvatar(), SourceRef: randomToken(), Remaining: loginSessionTTL.Milliseconds()}, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				return token
			}
			forward, reverse := login(b.Store, pb.ID), login(a.Store, pa.ID)
			var err error
			if sourceRevokes {
				err = a.RevokeFederationTrust(t.Context(), pa.ID, "outgoing")
			} else {
				err = b.RevokeFederationTrust(t.Context(), pb.ID, "incoming")
			}
			if err != nil {
				t.Fatal(err)
			}
			pa, pb = trustPeerForTest(t, a.Store), trustPeerForTest(t, b.Store)
			if pa.Outgoing || !pa.Incoming || !pa.Accept || pb.Incoming || pb.Accept || !pb.Outgoing || pa.SyncPending || pb.SyncPending {
				t.Fatalf("direction did not synchronize: source=%+v target=%+v", pa, pb)
			}
			initiator := pa.ID
			if sourceRevokes {
				initiator = pb.ID
			}
			for _, check := range []struct {
				peer     FederationTrustPeer
				outgoing bool
			}{{pa, true}, {pb, false}} {
				if len(check.peer.Revocations) != 1 {
					t.Fatalf("missing revocation initiator: %+v", check.peer)
				}
				record := check.peer.Revocations[0]
				if record.Initiator != initiator || record.Outgoing != check.outgoing || record.RecordedAt <= 0 || record.SyncPending {
					t.Fatalf("wrong revocation record: %+v", record)
				}
			}
			if _, err := b.Store.authenticate(forward); err == nil {
				t.Fatal("forward session survived revocation")
			}
			if _, err := a.Store.authenticate(reverse); err != nil {
				t.Fatal("independent reverse session was revoked", err)
			}
			if targets, err := a.Store.federationTargets(authIdentity{User: alice}); err != nil || len(targets) != 0 {
				t.Fatal("source still exposes revoked target", err)
			}
			if targets, err := b.Store.federationTargets(authIdentity{User: bob}); err != nil || len(targets) != 1 {
				t.Fatal("reverse target disappeared", err)
			}
			// A lost acknowledgement can cause another delivery; it must be a no-op.
			peer, err := federationPeer(a.Store.db, pa.ID)
			if err != nil {
				t.Fatal(err)
			}
			notice := federationTrustRevocation{Outgoing: false, Grant: request.ID}
			if _, err := b.app.federationSend(t.Context(), mustFederationPeer(t, b.Store, pb.ID), federationMessage{Action: federationTrustRevoke, Value: randomToken(), Trust: &notice}); err != nil {
				t.Fatal(err)
			}
			if current := mustFederationPeer(t, a.Store, pa.ID); current.generation != peer.generation {
				t.Fatal("duplicate delivery changed the reverse relationship generation")
			}
			if current := trustPeerForTest(t, a.Store); len(current.Revocations) != 1 || current.Revocations[0] != pa.Revocations[0] {
				t.Fatal("duplicate notification changed the original revocation initiator")
			}
		})
	}
}

func TestFederationTrustRevocationResolvesApprovalAcrossUpgrade(t *testing.T) {
	for _, oldSource := range []bool{false, true} {
		for _, sourceRevokes := range []bool{false, true} {
			name := "old target"
			if oldSource {
				name = "old source"
			}
			if sourceRevokes {
				name += "/source withdraws"
			} else {
				name += "/target revokes"
			}
			t.Run(name, func(t *testing.T) {
				a, b, request := revocationPairForTest(t, true)
				old := b
				if oldSource {
					old = a
				}
				// The previous release saved the same approval and receipt but did
				// not record epochs. Opening that database must not need a migration.
				if _, err := old.Store.db.Exec(`DELETE FROM federation_trust_epochs`); err != nil {
					t.Fatal(err)
				}
				if _, err := old.Store.db.Exec(`UPDATE federation_trust_requests SET expires_at=? WHERE id=?`, time.Now().Add(-time.Hour).Unix(), request.ID); err != nil {
					t.Fatal(err)
				}
				root := old.Store.Root()
				if err := old.Store.Close(); err != nil {
					t.Fatal(err)
				}
				reopened, err := OpenStore(root)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { reopened.Close() })
				old.Store, old.app.store = reopened, reopened
				// New requests exercise normal expiry cleanup before the old grant
				// is used. Expired approval evidence must remain recoverable.
				third, err := testStore(t).FederationIdentity()
				if err != nil {
					t.Fatal(err)
				}
				if _, err := reopened.requestFederationTrust(third); err != nil {
					t.Fatal(err)
				}
				var epochs int
				if err := reopened.db.QueryRow(`SELECT count(*) FROM federation_trust_epochs`).Scan(&epochs); err != nil || epochs != 0 {
					t.Fatalf("opening the store or submitting a request migrated epochs: %d, %v", epochs, err)
				}
				pa, pb := trustPeerForTest(t, a.Store), trustPeerForTest(t, b.Store)
				if sourceRevokes {
					err = a.RevokeFederationTrust(t.Context(), pa.ID, "outgoing")
				} else {
					err = b.RevokeFederationTrust(t.Context(), pb.ID, "incoming")
				}
				if err != nil {
					t.Fatal(err)
				}
				pa, pb = trustPeerForTest(t, a.Store), trustPeerForTest(t, b.Store)
				if pa.Outgoing || !pa.Incoming || pb.Incoming || !pb.Outgoing || pa.SyncPending || pb.SyncPending {
					t.Fatalf("upgrade revocation did not synchronize: source=%+v target=%+v", pa, pb)
				}
				var grant string
				var revoked bool
				peer := pa.ID
				if !oldSource {
					peer = pb.ID
				}
				if err := reopened.db.QueryRow(`SELECT request_id,revoked FROM federation_trust_epochs WHERE peer_id=? AND outgoing=?`, peer, oldSource).Scan(&grant, &revoked); err != nil || grant != request.ID || !revoked {
					t.Fatalf("approval identity was not recovered: %q, %v, %v", grant, revoked, err)
				}
			})
		}
	}
}

func TestFederationTrustRevocationKeepsNotificationWhenApprovalIsUnknown(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		name := "missing evidence"
		if ambiguous {
			name = "ambiguous evidence"
		}
		t.Run(name, func(t *testing.T) {
			a, b, request := revocationPairForTest(t, false)
			if _, err := b.Store.db.Exec(`DELETE FROM federation_trust_epochs`); err != nil {
				t.Fatal(err)
			}
			if ambiguous {
				if _, err := b.Store.db.Exec(`INSERT INTO federation_trust_requests(id,peer_id,peer_name,outgoing,expires_at,request,decision,receipt)
					SELECT ?,peer_id,peer_name,outgoing,expires_at,request,decision,receipt FROM federation_trust_requests WHERE id=?`, randomToken(), request.ID); err != nil {
					t.Fatal(err)
				}
			} else if _, err := b.Store.db.Exec(`DELETE FROM federation_trust_requests`); err != nil {
				t.Fatal(err)
			}
			pa, pb := trustPeerForTest(t, a.Store), trustPeerForTest(t, b.Store)
			if err := a.RevokeFederationTrust(t.Context(), pa.ID, "outgoing"); err != nil {
				t.Fatal(err)
			}
			if !trustPeerForTest(t, a.Store).SyncPending || !trustPeerForTest(t, b.Store).Incoming {
				t.Fatal("unresolved approval was acknowledged and discarded")
			}
			// An empty grant must never act as a wildcard for an active grant.
			notice := federationTrustRevocation{Outgoing: true}
			if _, err := a.app.federationSend(t.Context(), mustFederationPeer(t, a.Store, pa.ID), federationMessage{Action: federationTrustRevoke, Value: randomToken(), Trust: &notice}); err == nil {
				t.Fatal("empty approval identity was acknowledged")
			}
			if current := trustPeerForTest(t, b.Store); current.ID != pb.ID || !current.Incoming {
				t.Fatal("unidentified notification revoked the target")
			}
			fresh := submitTrustForTest(t, a, b, mustFederationPeer(t, a.Store, pa.ID).Address)
			if err := b.Store.DecideFederationTrust(fresh.ID, true, FederationTrustApproval{}); err != nil {
				t.Fatal("a fresh approval could not replace unresolved history", err)
			}
			if err := a.CheckFederationTrust(t.Context(), fresh.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := a.Store.db.Exec(`UPDATE federation_trust_notifications SET retry_at=0`); err != nil {
				t.Fatal(err)
			}
			a.app.syncFederationTrustRevocations(t.Context(), "")
			if trustPeerForTest(t, a.Store).SyncPending || !trustPeerForTest(t, b.Store).Incoming {
				t.Fatal("retry of the old revocation affected a fresh approval")
			}
		})
	}
}

func TestFederationTrustRevocationReturnRouteEligibility(t *testing.T) {
	for _, sourceRevokes := range []bool{true, false} {
		name := "target revokes incoming"
		if sourceRevokes {
			name = "source withdraws outgoing"
		}
		t.Run(name, func(t *testing.T) {
			a, b, _ := revocationPairForTest(t, false)
			local := b
			if sourceRevokes {
				local = a
			}
			peer := trustPeerForTest(t, local.Store)
			if err := local.Store.revokeFederationTrust(peer.ID, sourceRevokes); err != nil {
				t.Fatal(err)
			}
			pending := trustPeerForTest(t, local.Store)
			if !pending.SyncPending || len(pending.Revocations) != 1 || !pending.Revocations[0].SyncPending {
				t.Fatalf("missing directional pending status: %+v", pending)
			}
			before := mustFederationPeer(t, local.Store, peer.ID)
			err := local.Store.UpdateFederationTrustReturnRoute(peer.ID, before.Transport, before.Address)
			if (err == nil) == sourceRevokes {
				t.Fatalf("wrong route eligibility for source=%v: %v", sourceRevokes, err)
			}
			if mustFederationPeer(t, local.Store, peer.ID).Accept {
				t.Fatal("notification route restored revoked sign-in")
			}
			local.app.syncFederationTrustRevocations(t.Context(), peer.ID)
			complete := trustPeerForTest(t, local.Store)
			if complete.SyncPending || complete.Revocations[0].SyncPending {
				t.Fatalf("acknowledgement retained pending status: %+v", complete)
			}
			if err := local.Store.UpdateFederationTrustReturnRoute(peer.ID, before.Transport, before.Address); err == nil {
				t.Fatal("completed revocation accepted a notification route")
			}
		})
	}
	t.Run("only outgoing revocation remains pending", func(t *testing.T) {
		a, b, _ := revocationPairForTest(t, true)
		peer := trustPeerForTest(t, a.Store)
		if err := a.RevokeFederationTrust(t.Context(), peer.ID, "incoming"); err != nil {
			t.Fatal(err)
		}
		if err := a.Store.WithdrawFederationTrust(peer.ID); err != nil {
			t.Fatal(err)
		}
		pending := trustPeerForTest(t, a.Store)
		if !pending.SyncPending || len(pending.Revocations) != 2 {
			t.Fatalf("missing independently revoked directions: %+v", pending)
		}
		for _, record := range pending.Revocations {
			if record.SyncPending != record.Outgoing {
				t.Fatalf("pending state leaked between directions: %+v", record)
			}
		}
		before := mustFederationPeer(t, a.Store, peer.ID)
		if err := a.Store.UpdateFederationTrustReturnRoute(peer.ID, before.Transport, before.Address); err == nil {
			t.Fatal("pending outgoing revocation allowed an incoming notification route")
		}
		if !trustPeerForTest(t, b.Store).Incoming {
			t.Fatal("unsent outgoing notification unexpectedly reached the target")
		}
	})
}

func mustFederationPeer(t *testing.T, store *Store, id string) FederationPeer {
	t.Helper()
	p, err := federationPeer(store.db, id)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFederationTrustRevocationPersistsAndRetries(t *testing.T) {
	a, b, _ := revocationPairForTest(t, false)
	peer := trustPeerForTest(t, a.Store)
	dial := a.app.federationDial
	a.app.federationDial = func(context.Context, FederationPeer, []byte) ([]byte, error) {
		return nil, errors.New("offline")
	}
	if err := a.RevokeFederationTrust(t.Context(), peer.ID, "outgoing"); err != nil {
		t.Fatal(err)
	}
	if !trustPeerForTest(t, a.Store).SyncPending || !trustPeerForTest(t, b.Store).Incoming {
		t.Fatal("failed notification was not retained")
	}
	root := a.Store.Root()
	if err := a.Store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	a.Store, a.app.store, a.app.federationDial = reopened, reopened, dial
	restored := trustPeerForTest(t, reopened)
	if !restored.SyncPending || !restored.Revoked || len(restored.Revocations) != 1 || restored.Revocations[0].Initiator == peer.ID {
		t.Fatal("restart lost pending notification")
	}
	if _, err := reopened.db.Exec(`UPDATE federation_trust_notifications SET retry_at=0`); err != nil {
		t.Fatal(err)
	}
	a.app.syncFederationTrustRevocations(t.Context(), "")
	if trustPeerForTest(t, reopened).SyncPending || trustPeerForTest(t, b.Store).Incoming {
		t.Fatal("retry did not synchronize both stores")
	}
}

func TestFederationTrustRevocationReturnsBeforeSynchronization(t *testing.T) {
	a, b, _ := revocationPairForTest(t, false)
	peer := trustPeerForTest(t, a.Store)
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	dial := a.app.federationDial
	a.app.federationDial = func(ctx context.Context, peer FederationPeer, body []byte) ([]byte, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-release:
			return dial(ctx, peer, body)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	a.app.startFederation()
	t.Cleanup(func() { a.app.federationStop(); <-a.app.federationDone })
	result := make(chan error, 1)
	go func() { result <- a.RevokeFederationTrust(t.Context(), peer.ID, "outgoing") }()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("local revocation waited for peer synchronization")
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("local revocation did not wake the synchronization worker")
	}
	current := trustPeerForTest(t, a.Store)
	if !current.Revoked || current.Incoming || current.Outgoing || !current.SyncPending || len(current.Revocations) != 1 {
		t.Fatalf("local revocation is not final while sync is pending: %+v", current)
	}
	if !trustPeerForTest(t, b.Store).Incoming {
		t.Fatal("blocked peer unexpectedly synchronized")
	}
	close(release)
}

func TestFederationTrustRevocationWorkerWakesImmediately(t *testing.T) {
	a, b, _ := revocationPairForTest(t, false)
	peer := trustPeerForTest(t, a.Store)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); a.app.runFederationTrustRevocations(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	if err := a.Store.WithdrawFederationTrust(peer.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if !trustPeerForTest(t, b.Store).Incoming && !trustPeerForTest(t, a.Store).SyncPending {
			return
		}
		select {
		case <-deadline:
			t.Fatal("revocation waited for periodic session maintenance")
		case <-tick.C:
		}
	}
}

func TestFederationTrustRevocationIgnoresOldGrantAndRejectsSpoof(t *testing.T) {
	a, b, old := revocationPairForTest(t, false)
	pa := trustPeerForTest(t, a.Store)
	pb := trustPeerForTest(t, b.Store)
	if err := a.RevokeFederationTrust(t.Context(), pa.ID, "outgoing"); err != nil {
		t.Fatal(err)
	}
	fresh := submitTrustForTest(t, a, b, mustFederationPeer(t, a.Store, pa.ID).Address)
	if err := b.Store.DecideFederationTrust(fresh.ID, true, FederationTrustApproval{}); err != nil {
		t.Fatal(err)
	}
	if err := a.CheckFederationTrust(t.Context(), fresh.ID); err != nil {
		t.Fatal(err)
	}
	if len(trustPeerForTest(t, a.Store).Revocations) != 0 || len(trustPeerForTest(t, b.Store).Revocations) != 0 {
		t.Fatal("fresh approval displayed an old cancellation")
	}
	notice := federationTrustRevocation{Outgoing: true, Grant: old.ID}
	if _, err := a.app.federationSend(t.Context(), mustFederationPeer(t, a.Store, pa.ID), federationMessage{Action: federationTrustRevoke, Value: randomToken(), Trust: &notice}); err != nil {
		t.Fatal(err)
	}
	if !trustPeerForTest(t, b.Store).Incoming {
		t.Fatal("stale notification revoked a new approval")
	}
	attacker := testStore(t)
	key, err := attacker.federationKey()
	if err != nil {
		t.Fatal(err)
	}
	notice.Grant = fresh.ID
	body, err := (federationMessage{Version: federationVersion, From: pb.ID, To: pa.ID, Action: federationTrustRevoke, Nonce: randomToken(), Value: randomToken(), SentAt: time.Now().UnixMilli(), Trust: &notice}).encode(key)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	b.app.federationControl(response, httptest.NewRequest(http.MethodPost, FederationControlPath, bytes.NewReader(body)))
	if response.Code != http.StatusForbidden || !trustPeerForTest(t, b.Store).Incoming {
		t.Fatal("untrusted signature revoked trust", response.Code)
	}
}

func TestFederationTrustRevocationBeforeReceipt(t *testing.T) {
	for _, reciprocal := range []bool{false, true} {
		name := "new peer"
		if reciprocal {
			name = "existing reverse direction"
		}
		t.Run(name, func(t *testing.T) {
			a, b, _ := revocationPairForTest(t, reciprocal)
			pa, pb := trustPeerForTest(t, a.Store), trustPeerForTest(t, b.Store)
			address := mustFederationPeer(t, a.Store, pa.ID).Address
			// Remove the forward approval, then leave its fresh receipt unconsumed.
			if err := a.RevokeFederationTrust(t.Context(), pa.ID, "outgoing"); err != nil {
				t.Fatal(err)
			}
			if !reciprocal {
				if _, err := a.Store.db.Exec(`DELETE FROM federation_trust_notifications; DELETE FROM federation_trust_grants; DELETE FROM federation_peers`); err != nil {
					t.Fatal(err)
				}
			}
			fresh := submitTrustForTest(t, a, b, address)
			if err := b.Store.DecideFederationTrust(fresh.ID, true, FederationTrustApproval{}); err != nil {
				t.Fatal(err)
			}
			_, _, receipt, err := federationTrustRequest(b.Store.db, fresh.ID)
			if err != nil {
				t.Fatal(err)
			}
			pinned, err := a.Store.pendingFederationTrustPeer(pa.ID)
			if err != nil {
				t.Fatal(err)
			}
			key, ok := pinned.key()
			if !ok || !a.Store.federationRelayAdmission().Allow(key) {
				t.Fatal("pinned pending peer cannot deliver Relay cancellation")
			}
			dial := b.app.federationDial
			b.app.federationDial = func(ctx context.Context, peer FederationPeer, body []byte) ([]byte, error) {
				if _, err := dial(ctx, peer, body); err != nil {
					return nil, err
				}
				return nil, errors.New("acknowledgement lost")
			}
			if err := b.RevokeFederationTrust(t.Context(), pb.ID, "incoming"); err != nil {
				t.Fatal(err)
			}
			if _, err := a.Store.confirmFederationTrust(receipt); err == nil {
				t.Fatal("delayed receipt restored revoked approval")
			}
			current := trustPeerForTest(t, a.Store)
			if current.Outgoing || current.Incoming != reciprocal || current.Accept != reciprocal {
				t.Fatalf("early cancellation changed the wrong direction: %+v", current)
			}
			b.app.federationDial = dial
			if _, err := b.Store.db.Exec(`UPDATE federation_trust_notifications SET retry_at=0`); err != nil {
				t.Fatal(err)
			}
			b.app.syncFederationTrustRevocations(t.Context(), "")
			if trustPeerForTest(t, b.Store).SyncPending {
				t.Fatal("duplicate early cancellation was not acknowledged")
			}
		})
	}
}

func TestFederationTrustRevocationCancelsUnconsumedRenewal(t *testing.T) {
	a, b, _ := revocationPairForTest(t, false)
	pa, pb := trustPeerForTest(t, a.Store), trustPeerForTest(t, b.Store)
	late := submitTrustForTest(t, a, b, mustFederationPeer(t, a.Store, pa.ID).Address)
	if err := b.Store.DecideFederationTrust(late.ID, true, FederationTrustApproval{}); err != nil {
		t.Fatal(err)
	}
	_, _, receipt, err := federationTrustRequest(b.Store.db, late.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.RevokeFederationTrust(t.Context(), pa.ID, "outgoing"); err != nil {
		t.Fatal(err)
	}
	if trustPeerForTest(t, b.Store).Incoming || trustPeerForTest(t, a.Store).SyncPending {
		t.Fatal("unconsumed renewal survived source withdrawal")
	}
	if _, err := a.Store.confirmFederationTrust(receipt); err == nil {
		t.Fatal("old receipt restored the withdrawn renewal")
	}
	if current := trustPeerForTest(t, b.Store); current.ID != pb.ID || !current.Revoked {
		t.Fatalf("unexpected renewed target state: %+v", current)
	}
}
