package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func onlineTrustPairForTest(t *testing.T, sa, sb *Store) (source, target *Service, address string) {
	t.Helper()
	a, b := NewApp(sa, nil, "", ""), NewApp(sb, nil, "", "")
	as, bs := httptest.NewTLSServer(a), httptest.NewTLSServer(b)
	t.Cleanup(as.Close)
	t.Cleanup(bs.Close)
	source, target = trustServicesForTest(t, a, b, as, bs)
	return source, target, bs.URL
}

func trustServicesForTest(t *testing.T, a, b *App, as, bs *httptest.Server) (*Service, *Service) {
	t.Helper()
	for app, remote := range map[*App]*httptest.Server{a: bs, b: as} {
		app.trustDial = func(ctx context.Context, peer FederationPeer, path string, body []byte) ([]byte, error) {
			return federationHTTPPost(ctx, peer.Address, path, body, remote.Client().Transport.(*http.Transport).Clone())
		}
	}
	return &Service{Store: a.store, app: a, ctx: t.Context()}, &Service{Store: b.store, app: b, ctx: t.Context()}
}

func submitTrustForTest(t *testing.T, source, target *Service, address string) FederationTrustRequest {
	t.Helper()
	return submitTrustWithTransportForTest(t, source, target, "https", address)
}

func submitTrustWithTransportForTest(t *testing.T, source, target *Service, transport, address string) FederationTrustRequest {
	t.Helper()
	if err := target.Store.SetFederationTrustInbox(true); err != nil {
		t.Fatal(err)
	}
	peer, err := target.Store.FederationIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if err := source.SubmitFederationTrust(t.Context(), transport, address, peer.ID); err != nil {
		t.Fatal(err)
	}
	v, err := target.Store.FederationTrustSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range v.Requests {
		if !request.Outgoing && request.Status == federationTrustPending {
			return request
		}
	}
	t.Fatal("the target did not receive an online request")
	return FederationTrustRequest{}
}

func TestFederationTrustOverHTTPS(t *testing.T) {
	as, bs, cs := testStore(t), testStore(t), testStore(t)
	a, b := NewApp(as, nil, "", ""), NewApp(bs, nil, "", "")
	server := httptest.NewTLSServer(b)
	defer server.Close()
	a.trustDial = func(ctx context.Context, peer FederationPeer, path string, body []byte) ([]byte, error) {
		return federationHTTPPost(ctx, peer.Address, path, body, server.Client().Transport.(*http.Transport).Clone())
	}
	s := &Service{Store: as, app: a, ctx: t.Context()}
	preview, err := s.PreviewFederationTrust(t.Context(), "https", server.URL)
	if err != nil || preview.ID == "" || preview.Accepting {
		t.Fatalf("unopened inbox preview: %+v: %v", preview, err)
	}
	if err := s.SubmitFederationTrust(t.Context(), "https", server.URL, preview.ID); !errors.Is(err, errFederationTrustClosed) {
		t.Fatalf("closed inbox accepted a request: %v", err)
	}
	if err := bs.SetFederationTrustInbox(true); err != nil {
		t.Fatal(err)
	}
	other, err := cs.FederationIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SubmitFederationTrust(t.Context(), "https", server.URL, other.ID); !errors.Is(err, errFederationTrustPeer) {
		t.Fatalf("a different identity could receive the submitted request: %v", err)
	}
	if v, err := bs.FederationTrustSnapshot(); err != nil || len(v.Requests) != 0 {
		t.Fatal("preview or mismatched identity created a pending request")
	}
	if err := s.SubmitFederationTrust(t.Context(), "https", server.URL, preview.ID); err != nil {
		t.Fatal(err)
	}
	out, err := as.FederationTrustSnapshot()
	if err != nil || len(out.Requests) != 1 || !out.Requests[0].Network || len(out.Peers) != 0 {
		t.Fatalf("unexpected source state: %+v: %v", out, err)
	}
	in, err := bs.FederationTrustSnapshot()
	if err != nil || len(in.Requests) != 1 || in.Requests[0].ID != out.Requests[0].ID || len(in.Peers) != 0 {
		t.Fatalf("unexpected destination state: %+v: %v", in, err)
	}
	if err := s.CheckFederationTrust(t.Context(), out.Requests[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := bs.SetFederationTrustInbox(false); err != nil {
		t.Fatal(err)
	}
	if err := bs.DecideFederationTrust(in.Requests[0].ID, true, FederationTrustApproval{}); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckFederationTrust(t.Context(), out.Requests[0].ID); err != nil {
		t.Fatal(err)
	}
	server.Close()
	if err := s.CheckFederationTrust(t.Context(), out.Requests[0].ID); err != nil {
		t.Fatalf("rechecking a saved decision should not require the peer to be online: %v", err)
	}
	peer, err := federationPeer(as.db, preview.ID)
	if err != nil || peer.Transport != "https" || peer.Address != server.URL || peer.ClientURL != server.URL+"/" || peer.Accept || peer.AllUsers || len(peer.Users) != 0 {
		t.Fatalf("approval did not preserve the route or accidentally granted login: %+v: %v", peer, err)
	}
}

func TestFederationTrustInboxSeparatesPendingCapacityFromReceipts(t *testing.T) {
	a, b, destination := testStore(t), testStore(t), testStore(t)
	if err := destination.SetFederationTrustInbox(true); err != nil {
		t.Fatal(err)
	}
	target, err := destination.FederationIdentity()
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(federationTrustTTL).Unix()
	request := func(s *Store) federationTrustMessage {
		t.Helper()
		key, sender, err := s.federationTrustIdentity()
		if err != nil {
			t.Fatal(err)
		}
		return (federationTrustMessage{ID: randomToken(), Sender: sender, Target: target.ID, Expires: expires}).sign(key)
	}
	fill := func(prefix, decision string, count int) {
		t.Helper()
		_, err := destination.db.Exec(`WITH RECURSIVE fixture(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM fixture WHERE i<?)
			INSERT INTO federation_trust_requests(id,peer_id,peer_name,outgoing,expires_at,request,decision)
			SELECT ?||i,?||i,'Fixture',0,?,'',? FROM fixture`, count, prefix, prefix, expires, decision)
		if err != nil {
			t.Fatal(err)
		}
	}
	check := func(m federationTrustMessage, submit bool, want string) string {
		t.Helper()
		status, receipt, err := destination.federationTrustNetworkResult(m, submit)
		if err != nil || status != want {
			t.Fatalf("trust request returned %q, want %q: %v", status, want, err)
		}
		return receipt
	}
	first, second, third := request(a), request(b), request(a)
	check(first, true, federationTrustPending)
	check(third, true, "full") // One pending request per peer even below the global cap.
	fill("pending-", "", federationTrustPendingLimit-1)
	check(second, true, "full")
	if err := destination.DecideFederationTrust(first.ID, false, FederationTrustApproval{}); err != nil {
		t.Fatal(err)
	}
	check(second, true, federationTrustPending)
	if err := destination.DecideFederationTrust(second.ID, true, FederationTrustApproval{}); err != nil {
		t.Fatal(err)
	}
	receipt := check(first, false, federationTrustRejected)
	decision, err := parseFederationTrustCode(receipt, time.Now())
	if err != nil || decision.Sender.ID != target.ID || decision.Target != first.Sender.ID || decision.RequestHash != tokenHash(first.code()) || decision.Decision != federationTrustRejected {
		t.Fatalf("unexpected signed decision: %+v: %v", decision, err)
	}

	fill("decision-", federationTrustRejected, federationTrustInboxRecordLimit-federationTrustPendingLimit-1)
	check(third, true, "full") // Storage, not pending capacity, is exhausted.
	if err := destination.SetFederationTrustInbox(false); err != nil {
		t.Fatal(err)
	}
	if got := check(first, true, federationTrustRejected); got != receipt {
		t.Fatal("exact resubmission did not return the original signed decision")
	}
	check(second, false, federationTrustApproved)
	check(third, true, "closed")
	if err := destination.SetFederationTrustInbox(true); err != nil {
		t.Fatal(err)
	}
	if _, err := destination.db.Exec(`UPDATE federation_trust_requests SET expires_at=? WHERE id=?`, time.Now().Unix(), "decision-1"); err != nil {
		t.Fatal(err)
	}
	check(third, true, federationTrustPending)
}

func TestFederationTrustRejectsPlainHTTPAndUnsignedApplications(t *testing.T) {
	as, bs := testStore(t), testStore(t)
	b := NewApp(bs, nil, "", "")
	if err := bs.SetFederationTrustInbox(true); err != nil {
		t.Fatal(err)
	}
	peer, err := bs.FederationIdentity()
	if err != nil {
		t.Fatal(err)
	}
	code, err := as.requestFederationTrust(peer)
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(trustNetworkCall{Action: trustSubmit, Code: code, Nonce: randomToken()})
	if err != nil {
		t.Fatal(err)
	}
	for origin, status := range map[string]int{"http://127.0.0.1": http.StatusNotFound, "https://b.example.test": http.StatusForbidden} {
		r := httptest.NewRequest(http.MethodPost, origin+FederationTrustPath, bytes.NewReader(input))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("unexpected %d for %s", w.Code, origin)
		}
	}
	if v, err := bs.FederationTrustSnapshot(); err != nil || len(v.Requests) != 0 {
		t.Fatal("the unauthenticated request entered the inbox")
	}
}
