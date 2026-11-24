package workspace

import (
	"errors"
	"testing"
	"time"

	"spool/relay"
)

func TestFederationTrustRetryRejectsChangedReturnRoutes(t *testing.T) {
	for _, name := range []string{"changed address", "changed transport", "added route", "removed route"} {
		t.Run(name, func(t *testing.T) {
			a, b := testStore(t), testStore(t)
			target, err := b.FederationIdentity()
			if err != nil {
				t.Fatal(err)
			}
			target.Transport, target.Address = "https", "https://target.example.test"
			initial := &FederationTrustReturnRoute{Transport: "https", Address: "https://source.example.test"}
			retry := &FederationTrustReturnRoute{Transport: "https", Address: "https://changed.example.test"}
			switch name {
			case "changed transport":
				key, err := a.federationKey()
				if err != nil {
					t.Fatal(err)
				}
				code, err := (relay.Pairing{Version: 2, RelayURL: "https://relay.example.test", PublicKey: key.PublicKey(), AccessToken: key.FederationAccessToken()}).Encode()
				if err != nil {
					t.Fatal(err)
				}
				retry = &FederationTrustReturnRoute{Transport: "relay", Address: code}
			case "added route":
				initial = nil
			case "removed route":
				retry = &FederationTrustReturnRoute{}
			}
			code, err := a.requestFederationTrust(target, initial)
			if err != nil {
				t.Fatal(err)
			}
			message, err := parseFederationTrustCode(code, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			// The submission may have arrived despite a lost response. Its eventual
			// approval must still match the original signed message on both sides.
			if err := b.SetFederationTrustInbox(true); err != nil {
				t.Fatal(err)
			}
			if _, _, err := b.federationTrustNetworkResult(message, true); err != nil {
				t.Fatal(err)
			}
			if got, err := a.requestFederationTrust(target, retry); !errors.Is(err, errFederationTrustReturnRouteChanged) || got != "" {
				t.Fatal("changed return route silently reused a pending application", err)
			}
			if got, err := a.requestFederationTrust(target, initial); err != nil || got != code {
				t.Fatal("same-route retry changed the original signed application", err)
			}
			snapshot, err := a.FederationTrustSnapshot()
			if err != nil || len(snapshot.Requests) != 1 || snapshot.Requests[0].ID != message.ID {
				t.Fatal("conflicting retry replaced the pending request", err)
			}
			if err := b.DecideFederationTrust(message.ID, true, FederationTrustApproval{}); err != nil {
				t.Fatal(err)
			}
			_, _, receipt, err := federationTrustRequest(b.db, message.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.confirmFederationTrust(receipt); err != nil {
				t.Fatal("conflicting retry invalidated the original receipt", err)
			}
		})
	}
}

func TestFederationTrustRetryAllowsEquivalentReturnRoutes(t *testing.T) {
	for _, method := range []string{"https", "relay"} {
		t.Run(method, func(t *testing.T) {
			a, b := testStore(t), testStore(t)
			target, err := b.FederationIdentity()
			if err != nil {
				t.Fatal(err)
			}
			initial := &FederationTrustReturnRoute{Transport: method, Address: "https://source.example.test/"}
			retry := &FederationTrustReturnRoute{Transport: method, Address: "https://source.example.test"}
			if method == "relay" {
				key, err := a.federationKey()
				if err != nil {
					t.Fatal(err)
				}
				code, err := (relay.Pairing{Version: 2, RelayURL: "https://relay.example.test", PublicKey: key.PublicKey(), AccessToken: key.FederationAccessToken()}).Encode()
				if err != nil {
					t.Fatal(err)
				}
				initial.Address, retry.Address = code, " \n"+code+"\n"
			}
			code, err := a.requestFederationTrust(target, initial)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := a.requestFederationTrust(target, retry); err != nil || got != code {
				t.Fatal("equivalent return route could not retry the same request", err)
			}
		})
	}
}

func TestFederationTrustApplicationCarriesIndependentReturnRoute(t *testing.T) {
	for _, name := range []string{"own Relay", "public return", "configured public return", "configure later", "approver override", "existing public control", "reject"} {
		t.Run(name, func(t *testing.T) {
			a, b := testStore(t), testStore(t)
			settings, err := a.Settings()
			if err != nil {
				t.Fatal(err)
			}
			settings.RelayEnabled, settings.RelayURL = true, "https://source-relay.example.test"
			if name == "configured public return" {
				settings.WebEnabled, settings.PublicURL = true, "https://configured-source.example.test"
			}
			if err := a.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			ownCode, err := a.FederationRelayRoute()
			if err != nil {
				t.Fatal(err)
			}
			bk, err := b.federationKey()
			if err != nil {
				t.Fatal(err)
			}
			target, err := b.FederationIdentity()
			if err != nil {
				t.Fatal(err)
			}
			target.Transport = "relay"
			target.RelayCode, err = (relay.Pairing{Version: 2, RelayURL: "https://target-relay.example.test", PublicKey: bk.PublicKey(), AccessToken: bk.AccessToken}).Encode()
			if err != nil {
				t.Fatal(err)
			}
			if name == "existing public control" {
				existing := target
				existing.Transport, existing.Address, existing.RelayCode = "https", "https://previous-control.example.test", ""
				existing.ClientURL = "https://previous-client.example.test/"
				if err := seedApprovedFederationPeer(a, existing); err != nil {
					t.Fatal(err)
				}
			}
			var reverse *FederationTrustReturnRoute
			if name == "public return" {
				reverse = &FederationTrustReturnRoute{Transport: "https", Address: "https://source.example.test"}
			}
			if name == "configured public return" {
				reverse = &FederationTrustReturnRoute{Transport: "https"}
			}
			if name == "configure later" {
				reverse = &FederationTrustReturnRoute{}
			}
			code, err := a.requestFederationTrust(target, reverse)
			if err != nil {
				t.Fatal(err)
			}
			message, err := parseFederationTrustCode(code, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if err := b.SetFederationTrustInbox(true); err != nil {
				t.Fatal(err)
			}
			if _, _, err := b.federationTrustNetworkResult(message, true); err != nil {
				t.Fatal(err)
			}
			snapshot, err := b.FederationTrustSnapshot()
			if err != nil || len(snapshot.Requests) != 1 {
				t.Fatal("request not visible", err)
			}
			wantTransport := "relay"
			if name == "public return" || name == "configured public return" {
				wantTransport = "https"
			}
			if name == "configure later" {
				wantTransport = ""
			}
			if snapshot.Requests[0].ReturnTransport != wantTransport {
				t.Fatal("approval did not show the submitted return method")
			}
			policy := FederationTrustApproval{}
			if name == "approver override" {
				policy.Transport, policy.Address = "https", "https://override.example.test"
			}
			if err := b.DecideFederationTrust(message.ID, name != "reject", policy); err != nil {
				t.Fatal(err)
			}
			_, _, receipt, err := federationTrustRequest(b.db, message.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.confirmFederationTrust(receipt); err != nil {
				t.Fatal(err)
			}
			if name == "reject" {
				if peers, err := b.FederationPeers(); err != nil || len(peers) != 0 {
					t.Fatal("rejected return route created a relationship")
				}
				return
			}
			out := mustFederationPeer(t, a, target.ID)
			if out.ClientMode != FederationClientModeRelay || out.ClientRelayCode != target.RelayCode {
				t.Fatal("return method changed the outgoing Relay route")
			}
			if name == "existing public control" && (out.Transport != "https" || out.Address != "https://previous-control.example.test") {
				t.Fatal("outgoing Relay client route replaced a separately configured control return")
			}
			if name == "existing public control" {
				if err := a.UpdateFederationTrustReturnRoute(target.ID, "https", "https://new-return.example.test"); err != nil {
					t.Fatal(err)
				}
			}
			out = mustFederationPeer(t, a, target.ID)
			if code, err := federationRelayClientRoute(out); err != nil || code != target.RelayCode {
				t.Fatal("changing return settings lost the approved target Relay route", err)
			}
			in := mustFederationPeer(t, b, message.Sender.ID)
			switch name {
			case "own Relay":
				if !in.Accept || in.Transport != "relay" || in.RelayCode != ownCode {
					t.Fatal("approval did not install the signed Relay return route")
				}
			case "public return":
				if !in.Accept || in.Transport != "https" || in.Address != reverse.Address {
					t.Fatal("Relay application lost its independent Public HTTPS return route")
				}
			case "configured public return":
				if !in.Accept || in.Transport != "https" || in.Address != settings.PublicURL || reverse.Address != "" {
					t.Fatal("selected Public HTTPS did not include the server's configured URL")
				}
			case "configure later":
				if in.Accept || in.Transport != "" {
					t.Fatal("an omitted return route unexpectedly opened sign-in")
				}
			case "approver override":
				if !in.Accept || in.Transport != policy.Transport || in.Address != policy.Address {
					t.Fatal("applicant route replaced the explicit approval route")
				}
			}
		})
	}
}

func TestFederationTrustCannotAutomaticallyIncludeAnUnconfiguredReturnMethod(t *testing.T) {
	for _, name := range []string{"no public URL", "web disabled", "Relay disabled"} {
		t.Run(name, func(t *testing.T) {
			a, b := testStore(t), testStore(t)
			settings, _ := a.Settings()
			settings.WebEnabled, settings.PublicURL, settings.RelayEnabled = true, "", false
			if name == "web disabled" {
				settings.WebEnabled, settings.PublicURL = false, "https://source.example.test"
			}
			if err := a.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			target, _ := b.FederationIdentity()
			method := "https"
			if name == "Relay disabled" {
				method = "relay"
			}
			if _, err := a.requestFederationTrust(target, &FederationTrustReturnRoute{Transport: method}); err == nil {
				t.Fatal("an unavailable local return method was included")
			}
			snapshot, err := a.FederationTrustSnapshot()
			if err != nil || len(snapshot.Requests) != 0 {
				t.Fatal("failed local route resolution left a submitted request", err)
			}
		})
	}
}

func TestFederationLegacyRelayTargetSurvivesAChangedReturnRoute(t *testing.T) {
	a, b := testStore(t), testStore(t)
	key, _ := b.federationKey()
	code, _ := (relay.Pairing{Version: 2, RelayURL: "https://relay.example.test", PublicKey: key.PublicKey(), AccessToken: key.AccessToken}).Encode()
	peer, _ := b.FederationIdentity()
	peer.Transport, peer.RelayCode = "relay", code
	if err := seedApprovedFederationPeer(a, peer); err != nil {
		t.Fatal(err)
	}
	if err := a.UpdateFederationTrustReturnRoute(peer.ID, "https", "https://return.example.test"); err != nil {
		t.Fatal(err)
	}
	peer = mustFederationPeer(t, a, peer.ID)
	if route, err := federationRelayClientRoute(peer); err != nil || route != code || peer.Transport != "https" {
		t.Fatal("legacy outgoing Relay route followed a changed return route", err)
	}
	if err := a.UpdateFederationPeer(peer); err != nil {
		t.Fatal("saved independent Relay route was invalid", err)
	}
}

func TestFederationTrustReturnRouteIsSignedAndMatchesTheApplicant(t *testing.T) {
	a, b, other := testStore(t), testStore(t), testStore(t)
	target, _ := b.FederationIdentity()
	reverse := &FederationTrustReturnRoute{Transport: "https", Address: "https://source.example.test"}
	code, err := a.requestFederationTrust(target, reverse)
	if err != nil {
		t.Fatal(err)
	}
	m, err := parseFederationTrustCode(code, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	m.ReturnRoute.Address = "https://changed.example.test"
	if _, err := parseFederationTrustCode(m.code(), time.Now()); err == nil {
		t.Fatal("return route is not covered by the application signature")
	}
	key, _ := other.federationKey()
	foreign, _ := (relay.Pairing{Version: 2, RelayURL: "https://relay.example.test", PublicKey: key.PublicKey(), AccessToken: key.AccessToken}).Encode()
	m.ReturnRoute = &FederationTrustReturnRoute{Transport: "relay", Address: foreign}
	local, _ := a.federationKey()
	m = m.sign(local)
	if _, err := parseFederationTrustCode(m.code(), time.Now()); err == nil {
		t.Fatal("an applicant can advertise another server's Relay key")
	}
	if _, err := a.requestFederationTrust(target, m.ReturnRoute); err == nil {
		t.Fatal("source accepted a foreign Relay return identity")
	}
}
