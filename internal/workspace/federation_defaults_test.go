package workspace

import (
	"reflect"
	"testing"
	"time"
)

func TestFederationDefaultProjectsOnlyApplyOnFirstLogin(t *testing.T) {
	source, otherSource, target := testStore(t), testStore(t), testStore(t)
	removed := testProject(t, target)
	changed := testProject(t, target)
	manual := testProject(t, target)
	future := testProject(t, target)
	peer := federationPairForTest(t, target, source, "https://source.example.test", true, nil, []FederationProject{
		{removed.ID, "control", "create"}, {changed.ID, "control", "create"},
	})
	otherPeer := federationPairForTest(t, target, otherSource, "https://other.example.test", true, nil, []FederationProject{
		{future.ID, "control", "read-only"},
	})
	login := func(peer FederationPeer, subject string) (string, User) {
		t.Helper()
		person := federationSubject{ID: "source-" + subject, Nickname: subject, Avatar: DefaultAvatar(), SourceRef: randomToken(), Remaining: loginSessionTTL.Milliseconds()}
		token, _, err := target.federationLogin(peer, person, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		identity, err := target.authenticate(token)
		if err != nil {
			t.Fatal(err)
		}
		return token, identity.User
	}
	assertAccess := func(user User, want []FederationProject) {
		t.Helper()
		projects, err := target.Projects()
		if err != nil {
			t.Fatal(err)
		}
		for _, project := range projects {
			var expected *FederationProject
			for _, rule := range want {
				if rule.ID == project.ID {
					expected = &rule
					break
				}
			}
			if expected == nil {
				if target.CanAccessProject(user.ID, project.ID) {
					t.Errorf("user %s unexpectedly has access to project %s", user.ID, project.ID)
				}
				continue
			}
			if !target.CanAccessProject(user.ID, project.ID) || project.MemberAccess[user.ID] != expected.Access ||
				project.MemberOwnAccess[user.ID] != expected.OwnAccess || target.CanCreateChat(user.ID, project.ID) != (expected.OwnAccess == "create") {
				t.Errorf("user %s project %s: got %s/%s, want %s/%s", user.ID, project.ID, project.MemberAccess[user.ID], project.MemberOwnAccess[user.ID], expected.Access, expected.OwnAccess)
			}
		}
	}

	token, existing := login(peer, "visitor")
	assertAccess(existing, peer.Projects)
	_, otherVisitor := login(otherPeer, "visitor")
	assertAccess(otherVisitor, otherPeer.Projects)
	if otherVisitor.ID == existing.ID {
		t.Fatal("different source servers shared an external account")
	}
	// The administrator removes one default project, changes both permissions on
	// another, and manually adds a project outside the defaults.
	if _, err := target.SaveProject(removed); err != nil {
		t.Fatal(err)
	}
	changed.Members = []string{existing.ID}
	changed.MemberAccess = map[string]string{existing.ID: "read-only"}
	changed.MemberOwnAccess = map[string]string{existing.ID: "read-only"}
	if _, err := target.SaveProject(changed); err != nil {
		t.Fatal(err)
	}
	manual.Members = []string{existing.ID}
	if _, err := target.SaveProject(manual); err != nil {
		t.Fatal(err)
	}
	wantExisting := []FederationProject{{changed.ID, "read-only", "read-only"}, {manual.ID, "control", "create"}}
	defaults := []FederationProject{{removed.ID, "read-only", "read-only"}, {changed.ID, "control", "create"}, {future.ID, "read-only", "create"}}
	if err := target.SetFederationTrustProjects(peer.ID, defaults); err != nil {
		t.Fatal(err)
	}
	current := mustFederationPeer(t, target, peer.ID)
	wantPeer := peer
	wantPeer.Projects = defaults
	if !reflect.DeepEqual(current, wantPeer) {
		t.Fatal("editing defaults changed another peer setting or its generation")
	}
	if _, err := target.authenticate(token); err != nil {
		t.Fatal("editing defaults invalidated an existing session", err)
	}
	assertAccess(existing, wantExisting)
	assertAccess(otherVisitor, otherPeer.Projects)
	if err := target.logout(tokenHash(token)); err != nil {
		t.Fatal(err)
	}
	_, returning := login(peer, "visitor")
	if returning.ID != existing.ID {
		t.Fatal("repeat login replaced the existing account")
	}
	assertAccess(returning, wantExisting)
	_, newcomer := login(peer, "newcomer")
	assertAccess(newcomer, defaults)
	_, otherNewcomer := login(otherPeer, "newcomer")
	assertAccess(otherNewcomer, otherPeer.Projects)
	snapshot, err := target.FederationTrustSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range snapshot.Peers {
		if p.ID == peer.ID && !reflect.DeepEqual(p.Projects, defaults) {
			t.Fatal("trust settings did not expose the saved defaults", p.Projects)
		}
	}
	// Clearing defaults also affects only new accounts. An account created with
	// no projects remains empty when defaults are added later.
	if err := target.SetFederationTrustProjects(peer.ID, nil); err != nil {
		t.Fatal(err)
	}
	_, empty := login(peer, "empty")
	assertAccess(empty, nil)
	assertAccess(newcomer, defaults)
	if err := target.SetFederationTrustProjects(peer.ID, defaults); err != nil {
		t.Fatal(err)
	}
	_, returningEmpty := login(peer, "empty")
	if returningEmpty.ID != empty.ID {
		t.Fatal("repeat login replaced the empty account")
	}
	assertAccess(returningEmpty, nil)

	// A new approval for the same source may change defaults without resetting
	// accounts created under the previous approval.
	requester, approver, address := onlineTrustPairForTest(t, source, target)
	pending := submitTrustForTest(t, requester, approver, address)
	if err := target.DecideFederationTrust(pending.ID, true, FederationTrustApproval{Projects: defaults}); err != nil {
		t.Fatal(err)
	}
	_, returning = login(mustFederationPeer(t, target, peer.ID), "visitor")
	if returning.ID != existing.ID {
		t.Fatal("renewed trust replaced the existing account")
	}
	assertAccess(returning, wantExisting)
}

func TestFederationDefaultProjectsRequireIncomingApproval(t *testing.T) {
	for _, direction := range []string{"none", "outgoing", "revoked", "incoming"} {
		t.Run(direction, func(t *testing.T) {
			local, remote := testStore(t), testStore(t)
			project := testProject(t, local)
			peer := federationPairForTest(t, local, remote, "https://source.example.test", false, nil, nil)
			if direction == "none" {
				if _, err := local.db.Exec(`DELETE FROM federation_trust_grants WHERE peer_id=?`, peer.ID); err != nil {
					t.Fatal(err)
				}
			} else if _, err := local.db.Exec(`UPDATE federation_trust_grants SET incoming=?,outgoing=? WHERE peer_id=?`, direction == "incoming", direction == "outgoing", peer.ID); err != nil {
				t.Fatal(err)
			}
			defaults := []FederationProject{{project.ID, "read-only", "create"}}
			err := local.SetFederationTrustProjects(peer.ID, defaults)
			if (err == nil) != (direction == "incoming") {
				t.Fatalf("setting defaults with %s approval: %v", direction, err)
			}
			current := mustFederationPeer(t, local, peer.ID)
			if current.Accept || current.generation != peer.generation {
				t.Fatal("editing defaults enabled sign-in or changed the generation")
			}
			snapshot, err := local.FederationTrustSnapshot()
			if err != nil {
				t.Fatal(err)
			}
			if direction == "incoming" {
				if len(snapshot.Peers) != 1 || !reflect.DeepEqual(snapshot.Peers[0].Projects, defaults) {
					t.Fatal("incoming defaults missing from settings", snapshot.Peers)
				}
			} else {
				for _, p := range snapshot.Peers {
					if len(p.Projects) != 0 {
						t.Fatal("unapproved defaults appeared in settings", p)
					}
				}
			}
		})
	}
}

func TestFederationDefaultProjectsRejectInvalidUpdates(t *testing.T) {
	local, remote := testStore(t), testStore(t)
	project, archived := testProject(t, local), testProject(t, local)
	archived.Archived = true
	if _, err := local.SaveProject(archived); err != nil {
		t.Fatal(err)
	}
	valid := FederationProject{project.ID, "read-only", "create"}
	peer := federationPairForTest(t, local, remote, "https://source.example.test", true, nil, []FederationProject{valid})
	for _, invalid := range [][]FederationProject{
		{{"missing", "read-only", "create"}},
		{{archived.ID, "read-only", "create"}},
		{{project.ID, "invalid", "create"}},
		{{project.ID, "read-only", "invalid"}},
		{valid, valid},
	} {
		if err := local.SetFederationTrustProjects(peer.ID, invalid); err == nil {
			t.Fatal("invalid defaults were accepted", invalid)
		}
		if current := mustFederationPeer(t, local, peer.ID); !reflect.DeepEqual(current, peer) {
			t.Fatal("invalid update changed the saved peer")
		}
	}
}
