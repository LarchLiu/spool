package workspace

import (
	"testing"
	"time"
)

func TestFederationUserChecksWaitForApprovalSynchronization(t *testing.T) {
	source, target, _ := revocationPairForTest(t, false)
	user := testUser(t, source.Store, "alice")
	pa, pb := trustPeerForTest(t, source.Store), trustPeerForTest(t, target.Store)
	if err := source.Store.SetFederationTrustUsers(pa.ID, false, []string{user.ID}); err != nil {
		t.Fatal(err)
	}
	token, _, err := target.Store.federationLogin(mustFederationPeer(t, target.Store, pb.ID), federationSubject{ID: user.ID, Nickname: user.Nickname, Avatar: DefaultAvatar(), SourceRef: randomToken(), Remaining: loginSessionTTL.Milliseconds()}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	identity, err := target.Store.authenticate(token)
	if err != nil {
		t.Fatal(err)
	}
	project := testProject(t, target.Store, identity.User)
	// The target can approve a renewal before the source consumes its receipt.
	fresh := submitTrustForTest(t, source, target, mustFederationPeer(t, source.Store, pa.ID).Address)
	if err := target.Store.DecideFederationTrust(fresh.ID, true, FederationTrustApproval{}); err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		if _, err := target.Store.db.Exec(`UPDATE federation_user_checks SET retry_at=0`); err != nil {
			t.Fatal(err)
		}
		target.app.checkFederationUsers(t.Context())
	}
	check()
	if _, err := target.Store.authenticate(token); err != nil || !target.Store.CanAccessProject(identity.User.ID, project.ID) {
		t.Fatal("an unconsumed approval disabled a still-allowed user or removed permissions", err)
	}
	if err := source.CheckFederationTrust(t.Context(), fresh.ID); err != nil {
		t.Fatal(err)
	}
	check()
	if _, err := target.Store.authenticate(token); err != nil || !target.Store.CanAccessProject(identity.User.ID, project.ID) {
		t.Fatal("approval synchronization did not preserve the user's access", err)
	}
	// Once the approval agrees, a real allowlist removal must still revoke access.
	if err := source.Store.SetFederationTrustUsers(pa.ID, false, nil); err != nil {
		t.Fatal(err)
	}
	check()
	if _, err := target.Store.authenticate(token); err == nil || target.Store.CanAccessProject(identity.User.ID, project.ID) {
		t.Fatal("a confirmed eligibility denial did not revoke the user's access")
	}
}

func TestFederationUserRevocationWaitsForApprovalEvidence(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		name := "missing"
		if ambiguous {
			name = "ambiguous"
		}
		for _, save := range []bool{false, true} {
			action := "DeleteUser"
			if save {
				action = "SaveUser"
			}
			t.Run(name+"/"+action, func(t *testing.T) {
				source, target, approval := revocationPairForTest(t, false)
				user := testUser(t, source.Store, "alice")
				pa, pb := trustPeerForTest(t, source.Store), trustPeerForTest(t, target.Store)
				if err := source.Store.SetFederationTrustUsers(pa.ID, false, []string{user.ID}); err != nil {
					t.Fatal(err)
				}
				localToken, _, err := source.Store.login(user.Username, "test-password-2026")
				if err != nil {
					t.Fatal(err)
				}
				localProject := testProject(t, source.Store, user)
				token, _, err := target.Store.federationLogin(mustFederationPeer(t, target.Store, pb.ID), federationSubject{ID: user.ID, Nickname: user.Nickname, Avatar: DefaultAvatar(), SourceRef: randomToken(), Remaining: loginSessionTTL.Milliseconds()}, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				identity, err := target.Store.authenticate(token)
				if err != nil {
					t.Fatal(err)
				}
				project := testProject(t, target.Store, identity.User)
				request, code, receipt, err := federationTrustRequest(source.Store.db, approval.ID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := source.Store.db.Exec(`DELETE FROM federation_trust_epochs WHERE peer_id=? AND outgoing=1`, pa.ID); err != nil {
					t.Fatal(err)
				}
				duplicate := randomToken()
				if ambiguous {
					_, err = source.Store.db.Exec(`INSERT INTO federation_trust_requests(id,peer_id,peer_name,outgoing,expires_at,request,decision,receipt)
						SELECT ?,peer_id,peer_name,outgoing,expires_at,request,decision,receipt FROM federation_trust_requests WHERE id=?`, duplicate, approval.ID)
				} else {
					_, err = source.Store.db.Exec(`DELETE FROM federation_trust_requests WHERE id=?`, approval.ID)
				}
				if err != nil {
					t.Fatal(err)
				}
				if save {
					_, err = source.Store.SaveUser(UserInput{ID: user.ID, Username: user.Username, Nickname: user.Nickname, Disabled: true})
				} else {
					err = source.Store.DeleteUser(user.ID)
				}
				if err != nil {
					t.Fatal("missing approval evidence prevented a local disable", err)
				}
				users, err := source.Store.Users()
				if err != nil || len(users) != 1 || !users[0].Disabled {
					t.Fatal("the local account remained enabled", users, err)
				}
				if _, err := source.Store.authenticate(localToken); err == nil || source.Store.CanAccessProject(user.ID, localProject.ID) {
					t.Fatal("the local disable retained a session or project access")
				}
				var members int
				if err := source.Store.db.QueryRow(`SELECT count(*) FROM project_members WHERE user_id=?`, user.ID).Scan(&members); err != nil || members != 0 {
					t.Fatal("the local disable retained project membership", members, err)
				}
				// An unresolved notification survives restart without sending an empty
				// approval, and eligibility checks treat missing evidence as unavailable.
				root := source.Store.Root()
				if err := source.Store.Close(); err != nil {
					t.Fatal(err)
				}
				reopened, err := OpenStore(root)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { reopened.Close() })
				source.Store, source.app.store = reopened, reopened
				source.app.syncFederationUserRevocations(t.Context())
				var grant string
				if err := source.Store.db.QueryRow(`SELECT grant_id FROM federation_user_revocations WHERE peer_id=? AND subject=?`, pa.ID, user.ID).Scan(&grant); err != nil || grant != "" {
					t.Fatal("an unresolved notification was lost or bound to an unknown approval", grant, err)
				}
				if _, err := target.Store.db.Exec(`UPDATE federation_user_checks SET retry_at=0`); err != nil {
					t.Fatal(err)
				}
				target.app.checkFederationUsers(t.Context())
				if _, err := target.Store.authenticate(token); err != nil || !target.Store.CanAccessProject(identity.User.ID, project.ID) {
					t.Fatal("unknown approval evidence was treated as a definitive denial", err)
				}
				// Recover the original approval evidence; the retained notice must
				// now reach the target and revoke its account and project membership.
				if ambiguous {
					_, err = source.Store.db.Exec(`DELETE FROM federation_trust_requests WHERE id=?`, duplicate)
				} else {
					_, err = source.Store.db.Exec(`INSERT INTO federation_trust_requests(id,peer_id,peer_name,outgoing,expires_at,request,decision,receipt)
						VALUES(?,?,?,?,?,?,?,?)`, request.ID, request.PeerID, request.PeerName, request.Outgoing, request.ExpiresAt, code, federationTrustApproved, receipt)
				}
				if err != nil {
					t.Fatal(err)
				}
				if _, err := source.Store.db.Exec(`UPDATE federation_user_revocations SET retry_at=0`); err != nil {
					t.Fatal(err)
				}
				source.app.syncFederationUserRevocations(t.Context())
				if _, err := target.Store.authenticate(token); err == nil || target.Store.CanAccessProject(identity.User.ID, project.ID) {
					t.Fatal("recovering the approval did not deliver the retained revocation")
				}
				var pending int
				if err := source.Store.db.QueryRow(`SELECT count(*) FROM federation_user_revocations`).Scan(&pending); err != nil || pending != 0 {
					t.Fatal("the delivered notification remained pending", pending, err)
				}
			})
		}
	}
}

func TestFederationUserUnresolvedRevocationDoesNotCrossApproval(t *testing.T) {
	source, target, approval := revocationPairForTest(t, false)
	user := testUser(t, source.Store, "alice")
	pa, pb := trustPeerForTest(t, source.Store), trustPeerForTest(t, target.Store)
	if err := source.Store.SetFederationTrustUsers(pa.ID, false, []string{user.ID}); err != nil {
		t.Fatal(err)
	}
	token, _, err := target.Store.federationLogin(mustFederationPeer(t, target.Store, pb.ID), federationSubject{ID: user.ID, Nickname: user.Nickname, Avatar: DefaultAvatar(), SourceRef: randomToken(), Remaining: loginSessionTTL.Milliseconds()}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	identity, err := target.Store.authenticate(token)
	if err != nil {
		t.Fatal(err)
	}
	project := testProject(t, target.Store, identity.User)
	if _, err := source.Store.db.Exec(`DELETE FROM federation_trust_epochs WHERE peer_id=? AND outgoing=1`, pa.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Store.db.Exec(`DELETE FROM federation_trust_requests WHERE id=?`, approval.ID); err != nil {
		t.Fatal(err)
	}
	if err := source.Store.DeleteUser(user.ID); err != nil {
		t.Fatal(err)
	}
	var grant string
	if err := source.Store.db.QueryRow(`SELECT grant_id FROM federation_user_revocations WHERE peer_id=? AND subject=?`, pa.ID, user.ID).Scan(&grant); err != nil || grant != "" {
		t.Fatal("the old revocation was not retained without an approval", grant, err)
	}
	fresh := submitTrustForTest(t, source, target, mustFederationPeer(t, source.Store, pa.ID).Address)
	if err := target.Store.DecideFederationTrust(fresh.ID, true, FederationTrustApproval{}); err != nil {
		t.Fatal(err)
	}
	if err := source.CheckFederationTrust(t.Context(), fresh.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Store.SaveUser(UserInput{ID: user.ID, Username: user.Username, Nickname: user.Nickname}); err != nil {
		t.Fatal(err)
	}
	source.app.syncFederationUserRevocations(t.Context())
	if _, err := target.Store.authenticate(token); err != nil || !target.Store.CanAccessProject(identity.User.ID, project.ID) {
		t.Fatal("an unresolved old notification revoked the new approval", err)
	}
	var pending int
	if err := source.Store.db.QueryRow(`SELECT count(*) FROM federation_user_revocations`).Scan(&pending); err != nil || pending != 0 {
		t.Fatal("the new approval retained an unbound old notification", pending, err)
	}
}
