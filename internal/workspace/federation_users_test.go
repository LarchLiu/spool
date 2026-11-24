package workspace

import (
	"errors"
	"testing"
	"time"

	chatmodel "spool/internal/model"
)

func TestFederationUserRevocationDisablesAccountsWithoutSessions(t *testing.T) {
	for _, name := range []string{"notification", "eligibility check", "source disabled"} {
		t.Run(name, func(t *testing.T) {
			a, b, _ := revocationPairForTest(t, false)
			source := testUser(t, a.Store, "alice")
			other := testUser(t, b.Store, "local")
			pa, pb := trustPeerForTest(t, a.Store), mustFederationPeer(t, b.Store, trustPeerForTest(t, b.Store).ID)
			if err := a.Store.SetFederationTrustUsers(pa.ID, false, []string{source.ID}); err != nil {
				t.Fatal(err)
			}
			person := federationSubject{ID: source.ID, Nickname: "Visitor", Avatar: DefaultAvatar(), SourceRef: randomToken(), Remaining: loginSessionTTL.Milliseconds()}
			token, _, err := b.Store.federationLogin(pb, person, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			identity, err := b.Store.authenticate(token)
			if err != nil {
				t.Fatal(err)
			}
			project := testProject(t, b.Store, identity.User, other)
			// The source login may be gone and the target already signed out.
			// Removing the allowlist entry must still revoke the account itself.
			if err := b.Store.logout(tokenHash(token)); err != nil {
				t.Fatal(err)
			}
			if name == "source disabled" {
				if _, err := a.Store.SaveUser(UserInput{ID: source.ID, Username: source.Username, Nickname: source.Nickname, Disabled: true}); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := a.Store.SetFederationTrustUsers(pa.ID, false, nil); err != nil {
					t.Fatal(err)
				}
			}
			if name != "eligibility check" {
				a.app.federationMaintain(t.Context())
			} else {
				if _, err := b.Store.db.Exec(`UPDATE federation_user_checks SET retry_at=0`); err != nil {
					t.Fatal(err)
				}
				b.app.federationMaintain(t.Context())
			}
			users, err := b.Store.Users()
			if err != nil {
				t.Fatal(err)
			}
			for _, user := range users {
				if user.ID == identity.User.ID && !user.Disabled || user.ID == other.ID && user.Disabled {
					t.Fatalf("wrong account disabled: %+v", user)
				}
			}
			for _, table := range []string{"project_members", "project_member_access", "project_member_own_access"} {
				var count int
				if err := b.Store.db.QueryRow(`SELECT count(*) FROM `+table+` WHERE user_id=?`, identity.User.ID).Scan(&count); err != nil || count != 0 {
					t.Fatalf("revoked permissions remain in %s: %d, %v", table, count, err)
				}
			}
			if !b.Store.CanAccessProject(other.ID, project.ID) {
				t.Fatal("revocation affected another project member")
			}
			// A stale project editor must not be able to restore membership.
			if _, err := b.Store.SaveProject(project); err != nil {
				t.Fatal(err)
			}
			projects, err := b.Store.Projects()
			if err != nil || len(projects[0].Members) != 1 || projects[0].Members[0] != other.ID {
				t.Fatal("disabled user appeared in project management", err)
			}
			if err := a.Store.SetFederationTrustUsers(pa.ID, true, nil); err != nil {
				t.Fatal(err)
			}
			if _, _, err := b.Store.federationLogin(pb, person, time.Now()); !errors.Is(err, errFederationDenied) {
				t.Fatal("source re-allowing silently re-enabled the visitor", err)
			}
		})
	}
}

func TestFederationUserChecksDoNotDisableForLogoutOrNetworkFailure(t *testing.T) {
	a, b, _ := revocationPairForTest(t, false)
	source := testUser(t, a.Store, "alice")
	pa, pb := trustPeerForTest(t, a.Store), mustFederationPeer(t, b.Store, trustPeerForTest(t, b.Store).ID)
	if err := a.Store.SetFederationTrustUsers(pa.ID, false, []string{source.ID}); err != nil {
		t.Fatal(err)
	}
	token, _, err := b.Store.federationLogin(pb, federationSubject{ID: source.ID, Nickname: "Visitor", Avatar: DefaultAvatar(), SourceRef: randomToken(), Remaining: loginSessionTTL.Milliseconds()}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	identity, _ := b.Store.authenticate(token)
	project := testProject(t, b.Store, identity.User)
	for _, offline := range []bool{false, true} {
		if offline {
			b.app.federationDial = nil
		}
		if _, err := b.Store.db.Exec(`UPDATE federation_user_checks SET retry_at=0; UPDATE federation_sessions SET retry_at=0`); err != nil {
			t.Fatal(err)
		}
		b.app.federationMaintain(t.Context())
		users, err := b.Store.Users()
		if err != nil || len(users) != 1 || users[0].Disabled || !b.Store.CanAccessProject(identity.User.ID, project.ID) {
			t.Fatal("logout or a network failure disabled the user or removed permissions", err)
		}
	}
}

func TestRemoveDisabledUserPreservesHistoryAndRejectsRestoration(t *testing.T) {
	s := testStore(t)
	user := testUser(t, s, "alice")
	project := testProject(t, s, user)
	chat, err := s.CreateChat(user.ID, project.ID, chatmodel.BackendCodex)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveDisabledUser(user.ID); err == nil {
		t.Fatal("an enabled account could be deleted")
	}
	if _, err := s.SaveUser(UserInput{ID: user.ID, Username: user.Username, Nickname: user.Nickname, Disabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveDisabledUser(user.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveDisabledUser(user.ID); err != nil {
		t.Fatal("delete was not idempotent", err)
	}
	users, err := s.Users()
	if err != nil || len(users) != 0 {
		t.Fatal("deleted user remained in management", err)
	}
	var owner string
	if err := s.db.QueryRow(`SELECT user_id FROM sessions WHERE id=?`, chat.ID).Scan(&owner); err != nil || owner != user.ID {
		t.Fatal("deleting a person lost history ownership", err)
	}
	if _, err := s.SaveUser(UserInput{ID: user.ID, Username: user.Username, Nickname: user.Nickname}); err == nil {
		t.Fatal("a stale user editor restored a deleted account")
	}
}

func TestFederationUserRevocationDoesNotReplayAfterManualEnable(t *testing.T) {
	a, b, request := revocationPairForTest(t, false)
	peer := mustFederationPeer(t, b.Store, request.PeerID)
	person := federationSubject{ID: "source-visitor", Nickname: "Visitor", Avatar: DefaultAvatar(), SourceRef: randomToken(), Remaining: loginSessionTTL.Milliseconds()}
	token, _, err := b.Store.federationLogin(peer, person, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	identity, _ := b.Store.authenticate(token)
	access := federationUserAccess{Subject: person.ID, Grant: request.ID}
	nonce := randomToken()
	if err := b.Store.revokeFederationUser(peer, access, nonce); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Store.SaveUser(UserInput{ID: identity.User.ID}); err != nil {
		t.Fatal(err)
	}
	project := testProject(t, b.Store, identity.User)
	if err := b.Store.revokeFederationUser(peer, access, nonce); err != nil {
		t.Fatal(err)
	}
	if !b.Store.CanAccessProject(identity.User.ID, project.ID) {
		t.Fatal("a repeated notification undid manual reauthorization")
	}
	// Re-approval gives the relationship a different identity. Delayed checks
	// and even previously unseen notifications for the old grant are ignored.
	if err := a.Store.WithdrawFederationTrust(trustPeerForTest(t, a.Store).ID); err != nil {
		t.Fatal(err)
	}
	fresh := submitTrustForTest(t, a, b, mustFederationPeer(t, a.Store, trustPeerForTest(t, a.Store).ID).Address)
	if err := b.Store.DecideFederationTrust(fresh.ID, true, FederationTrustApproval{Transport: peer.Transport, Address: peer.Address}); err != nil {
		t.Fatal(err)
	}
	current := mustFederationPeer(t, b.Store, peer.ID)
	for _, nonce := range []string{"", randomToken()} {
		if err := b.Store.revokeFederationUser(current, access, nonce); err != nil {
			t.Fatal(err)
		}
	}
	if !b.Store.CanAccessProject(identity.User.ID, project.ID) {
		t.Fatal("an old approval revoked a newly authorized relationship")
	}
	if err := b.Store.DeleteUser(identity.User.ID); err != nil {
		t.Fatal(err)
	}
	if err := b.Store.RemoveDisabledUser(identity.User.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Store.federationLogin(current, person, time.Now()); !errors.Is(err, errFederationDenied) {
		t.Fatal("logging in recreated a deleted external person", err)
	}
}

func TestFederationUserRevocationRejectsAnInflightFirstLogin(t *testing.T) {
	_, target, request := revocationPairForTest(t, false)
	peer := mustFederationPeer(t, target.Store, request.PeerID)
	checked := time.Now().Add(-time.Second)
	person := federationSubject{ID: "new-visitor", Nickname: "Visitor", Avatar: DefaultAvatar(), SourceRef: randomToken(), Remaining: loginSessionTTL.Milliseconds()}
	access := federationUserAccess{Subject: person.ID, Grant: request.ID}
	if err := target.Store.revokeFederationUser(peer, access, randomToken()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := target.Store.federationLogin(peer, person, checked); !errors.Is(err, errFederationDenied) {
		t.Fatal("an in-flight response created an account after its access was revoked", err)
	}
	users, err := target.Store.Users()
	if err != nil || len(users) != 0 {
		t.Fatal("a notification imported a person who had never signed in", err)
	}
}

func TestFederationUserRevocationRetriesAfterTransportFailure(t *testing.T) {
	source, target, _ := revocationPairForTest(t, false)
	user := testUser(t, source.Store, "alice")
	peer := trustPeerForTest(t, source.Store)
	if err := source.Store.SetFederationTrustUsers(peer.ID, false, []string{user.ID}); err != nil {
		t.Fatal(err)
	}
	if err := source.Store.SetFederationTrustUsers(peer.ID, false, nil); err != nil {
		t.Fatal(err)
	}
	dial := source.app.federationDial
	source.app.federationDial = nil
	source.app.syncFederationUserRevocations(t.Context())
	var count int
	if err := source.Store.db.QueryRow(`SELECT count(*) FROM federation_user_revocations WHERE subject=?`, user.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("a failed notification was not retained", err)
	}
	// Removing an allowed user who never logged in must not import an account.
	source.app.federationDial = dial
	if _, err := source.Store.db.Exec(`UPDATE federation_user_revocations SET retry_at=0`); err != nil {
		t.Fatal(err)
	}
	source.app.syncFederationUserRevocations(t.Context())
	if err := source.Store.db.QueryRow(`SELECT count(*) FROM federation_user_revocations`).Scan(&count); err != nil || count != 0 {
		t.Fatal("successful delivery did not clear the pending notification", err)
	}
	users, err := target.Store.Users()
	if err != nil || len(users) != 0 {
		t.Fatal("revocation imported an unused account", err)
	}
}
