package workspace

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

type federationProfileSessionState struct {
	Checked, Retry, Expires int64
	CSRF                    string
}

func federationProfileSessionForTest(t *testing.T, s *Store, token string) (federationProfileSessionState, bool) {
	t.Helper()
	var state federationProfileSessionState
	err := s.db.QueryRow(`SELECT f.checked_at,f.retry_at,a.expires_at,a.csrf FROM federation_sessions f
		JOIN auth_sessions a ON a.token_hash=f.session_hash WHERE f.session_hash=?`, tokenHash(token)).
		Scan(&state.Checked, &state.Retry, &state.Expires, &state.CSRF)
	if errors.Is(err, sql.ErrNoRows) {
		return state, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return state, true
}

func TestFederationCheckUpdatesProfileWithoutChangingLocalAccess(t *testing.T) {
	sa, sb := testStore(t), testStore(t)
	alice := testUser(t, sa, "alice")
	project := testProject(t, sb)
	pb := federationPairForTest(t, sa, sb, "https://target.spool.test", false, []string{alice.ID}, nil)
	pa := federationPairForTest(t, sb, sa, "https://source.spool.test", true, nil, []FederationProject{{project.ID, "control", "create"}})
	pb, err := federationPeer(sa.db, pb.ID)
	if err != nil {
		t.Fatal(err)
	}
	pa, err = federationPeer(sb.db, pa.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, sourceIdentity, err := sa.login(alice.Username, "test-password-2026")
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := sa.federationIssue(sourceIdentity, pb.ID)
	if err != nil {
		t.Fatal(err)
	}
	person, err := sa.federationVerify(pb, ticket, true)
	if err != nil {
		t.Fatal(err)
	}
	// Multiple B logins from the same A session share one eligibility request.
	checked := time.Now().Add(-time.Minute)
	var tokens []string
	for range 2 {
		token, _, err := sb.federationLogin(pa, person, checked)
		if err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, token)
	}
	before, err := sb.authenticate(tokens[0])
	if err != nil {
		t.Fatal(err)
	}
	// Keep local capability settings and remove the first-login project grant.
	if _, err := sb.db.Exec(`INSERT INTO user_remote_desktop VALUES(?,1); INSERT INTO user_adb_bridge VALUES(?,1)`, before.User.ID, before.User.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := sb.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	before, err = sb.authenticate(tokens[0])
	if err != nil {
		t.Fatal(err)
	}
	states := make([]federationProfileSessionState, len(tokens))
	for i, token := range tokens {
		states[i], _ = federationProfileSessionForTest(t, sb, token)
	}
	a, b := NewApp(sa, nil, "", ""), NewApp(sb, nil, "", "")
	t.Cleanup(func() { a.Close(); b.Close() })
	b.presence.Touch(before, "profile-test", true)
	checks := 0
	unavailable, legacy := false, false
	b.federationDial = func(ctx context.Context, peer FederationPeer, body []byte) ([]byte, error) {
		var request federationMessage
		if err := json.Unmarshal(body, &request); err != nil || request.Action != federationCheck || peer.ID != pa.ID {
			t.Fatal("unexpected federation request", err)
		}
		checks++
		if unavailable {
			return nil, errors.New("source unavailable")
		}
		r := httptest.NewRequest(http.MethodPost, peer.Address+FederationControlPath, bytes.NewReader(body)).WithContext(ctx)
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("check returned %d", w.Code)
		}
		if legacy {
			var reply federationMessage
			if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
				t.Fatal(err)
			}
			reply.Subject = nil
			key, err := sa.federationKey()
			if err != nil {
				t.Fatal(err)
			}
			return reply.encode(key)
		}
		return w.Body.Bytes(), nil
	}
	profileBefore, err := sb.userProfiles(before.User.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Change source metadata without revoking the A session, so this tests the
	// profile refresh independently of administrative session revocation.
	if _, err := sa.db.Exec(`UPDATE users SET nickname=? WHERE id=?`, "Alice Updated", alice.ID); err != nil {
		t.Fatal(err)
	}
	avatar := Avatar{Style: "thumbs", Seed: "updated-profile"}
	if err := sa.SaveAvatar(alice.ID, avatar); err != nil {
		t.Fatal(err)
	}
	sub := sb.changes.subscribe()
	defer sub.close()
	forceCheck := func() {
		t.Helper()
		if _, err := sb.db.Exec(`UPDATE federation_sessions SET retry_at=0`); err != nil {
			t.Fatal(err)
		}
		b.federationMaintain(t.Context())
	}
	// Merely scanning before the deadline must not request or update anything.
	b.federationMaintain(t.Context())
	if checks != 0 {
		t.Fatal("checked a session before its deadline")
	}
	forceCheck()
	if checks != 1 {
		t.Fatalf("wanted one grouped request, got %d", checks)
	}
	want := before.User
	want.Nickname, want.Avatar = "Alice Updated", avatar
	for i, token := range tokens {
		current, err := sb.authenticate(token)
		if err != nil || current.User != want || current.CSRF != states[i].CSRF {
			t.Fatalf("check changed local identity, capabilities or credentials: %+v, %v", current.User, err)
		}
		state, exists := federationProfileSessionForTest(t, sb, token)
		if !exists || state.Expires != states[i].Expires || state.Checked <= states[i].Checked || state.Retry-state.Checked != federationRecheck.Milliseconds() {
			t.Fatal("check did not refresh freshness while preserving expiry")
		}
	}
	if sb.CanAccessProject(want.ID, project.ID) {
		t.Fatal("profile refresh restored a removed project membership")
	}
	profileAfter, err := sb.userProfiles(want.ID, nil)
	if err != nil || profileAfter.Revision != profileBefore.Revision+1 || len(profileAfter.People) != 1 || profileAfter.People[0] != (UserProfile{ID: want.ID, Nickname: want.Nickname, Avatar: avatar}) {
		t.Fatal("profile snapshot was not updated transactionally", err)
	}
	if changes := sub.take(); changes != changeProfiles|changePresence {
		t.Fatalf("missing profile refresh notification: %v", changes)
	}
	if people := b.presence.List(sb); len(people) != 1 || people[0].Nickname != want.Nickname || people[0].Avatar != avatar {
		t.Fatal("online presence retained the old profile")
	}
	forceCheck()
	unchanged, err := sb.userProfiles(want.ID, nil)
	if err != nil || !reflect.DeepEqual(unchanged, profileAfter) || sub.take() != 0 {
		t.Fatal("unchanged metadata advanced the revision or notified clients", err)
	}
	// A temporary transport failure must retain the last verified profile and
	// freshness, scheduling the ordinary one-minute retry instead.
	unavailable = true
	stateBefore, _ := federationProfileSessionForTest(t, sb, tokens[0])
	forceCheck()
	stateAfter, _ := federationProfileSessionForTest(t, sb, tokens[0])
	if stateAfter.Checked != stateBefore.Checked || stateAfter.Expires != stateBefore.Expires || stateAfter.Retry <= time.Now().UnixMilli() || stateAfter.Retry > time.Now().Add(federationRetry).UnixMilli() {
		t.Fatal("failed check changed freshness, expiry or the retry policy")
	}
	// A signed reply from an older source can omit the profile altogether.
	unavailable, legacy = false, true
	if err := sa.SaveAvatar(alice.ID, Avatar{Style: "clay", Seed: "newer-source-profile"}); err != nil {
		t.Fatal(err)
	}
	forceCheck()
	final, err := sb.userProfiles(want.ID, nil)
	if err != nil || !reflect.DeepEqual(final, profileAfter) || sub.take() != 0 {
		t.Fatal("a failed or legacy check overwrote the profile", err)
	}
	stateAfter, _ = federationProfileSessionForTest(t, sb, tokens[0])
	if stateAfter.Retry-stateAfter.Checked != federationRecheck.Milliseconds() {
		t.Fatal("legacy reply did not refresh session freshness")
	}
}

func TestFederationRefreshIgnoresInvalidOrDelayedProfiles(t *testing.T) {
	for _, name := range []string{
		"different user", "different source session", "empty nickname", "long nickname", "invalid avatar", "expired source session",
		"newer check", "offline limit", "expired target session", "revoked session", "changed relationship", "disabled user",
	} {
		t.Run(name, func(t *testing.T) {
			local, remote := testStore(t), testStore(t)
			peer := federationPairForTest(t, local, remote, "https://source.spool.test", true, nil, nil)
			peer, err := federationPeer(local.db, peer.ID)
			if err != nil {
				t.Fatal(err)
			}
			person := federationSubject{ID: "source-user", Nickname: "Original", Avatar: DefaultAvatar(), SourceRef: randomToken(), Remaining: loginSessionTTL.Milliseconds()}
			token, _, err := local.federationLogin(peer, person, time.Now().Add(-time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			id, err := local.authenticate(token)
			if err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			reply := person
			reply.Nickname, reply.Avatar = "Changed", Avatar{Style: "thumbs", Seed: "changed"}
			var setupErr error
			switch name {
			case "different user":
				reply.ID = "another-user"
			case "different source session":
				reply.SourceRef = randomToken()
			case "empty nickname":
				reply.Nickname = ""
			case "long nickname":
				reply.Nickname = strings.Repeat("x", 101)
			case "invalid avatar":
				reply.Avatar.Style = "unknown"
			case "expired source session":
				reply.Remaining = 0
			case "newer check":
				_, setupErr = local.db.Exec(`UPDATE federation_sessions SET checked_at=?`, started.Add(time.Second).UnixMilli())
			case "offline limit":
				_, setupErr = local.db.Exec(`UPDATE federation_sessions SET checked_at=?`, started.Add(-federationOffline-time.Second).UnixMilli())
			case "expired target session":
				_, setupErr = local.db.Exec(`UPDATE auth_sessions SET expires_at=?`, started.Add(-time.Second).UnixMilli())
			case "revoked session":
				setupErr = local.federationRevoke(peer, person.SourceRef)
			case "changed relationship":
				setupErr = local.UpdateFederationPeer(peer)
			case "disabled user":
				_, setupErr = local.SaveUser(UserInput{ID: id.User.ID, Disabled: true})
			}
			if setupErr != nil {
				t.Fatal(setupErr)
			}
			before, err := local.userProfiles(id.User.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			stateBefore, existedBefore := federationProfileSessionForTest(t, local, token)
			sub := local.changes.subscribe()
			defer sub.close()
			if err := local.federationRefresh(peer, person.SourceRef, started, &reply); err != nil && !errors.Is(err, errFederationDenied) {
				t.Fatal(err)
			}
			after, err := local.userProfiles(id.User.ID, nil)
			if err != nil || !reflect.DeepEqual(before, after) || sub.take() != 0 {
				t.Fatal("invalid or delayed reply changed the profile", err)
			}
			stateAfter, existsAfter := federationProfileSessionForTest(t, local, token)
			if existsAfter != existedBefore || stateAfter != stateBefore {
				t.Fatal("invalid or delayed reply refreshed or restored a session")
			}
		})
	}
}
